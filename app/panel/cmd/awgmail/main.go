// Command awgmail delivers the operator's notification mail
// (amnezia-vpn-server-hxgr, dfs2). A systemd timer runs it once a minute on
// the host, next to the watchdog; every run is one pass and the process
// exits.
//
// A run:
//
//   - reads data/mail.conf, which the panel derives from its database.
//     No file means mail is off: the run is silent and exits 0. The
//     service must not fail every minute on a server that simply has no
//     mail set up;
//   - observes the server through the files the watchdog, the update agent
//     and awg already write, and lets the rules (internal/notify,
//     amnezia-vpn-server-0d2n) decide what is news; their memory lives in
//     status/notify-state.json;
//   - puts the letters into the outbox (status/mail-state.json, beside the
//     watchdog's services.json, where the panel can read it);
//   - delivers what is due, retrying a failure after 1, 5 and 15 minutes
//     (internal/mailer);
//   - writes the outbox back.
//
// With mail off the rules' memory is dropped, so that switching mail on
// starts from a fresh baseline instead of reporting what happened while
// nobody was listening.
//
// The password never reaches the command line — awgmail takes no
// arguments besides --help, and paths come from the environment — nor
// the journal: errors are redacted by the sender.
//
// Quiet when all is well: it prints only when it sent, failed or gave up.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/mailconf"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/mailer"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/notify"
)

const usageText = `awgmail — отправляет письма-уведомления оператору Amnezia VPN.

Запускается таймером раз в минуту. Читает настройки почты из
data/mail.conf, которые записывает панель; нет файла — почта выключена,
служба молчит. Неотправленное письмо повторяет через 1, 5 и 15 минут.

Использование:
  awgmail

Переменные окружения:
  AMNEZIA_MAIL_ROOT        каталог развёртывания (по умолчанию /opt/amnezia-vpn)
  AMNEZIA_MAIL_CONF_PATH   файл настроек (по умолчанию <root>/data/mail.conf)
  AMNEZIA_MAIL_STATE_PATH  очередь писем (по умолчанию <root>/status/mail-state.json)
  AMNEZIA_NOTIFY_STATE_PATH память правил (по умолчанию <root>/status/notify-state.json)

Ключи:
  --help    показать эту справку

Примеры:
  # один проход, как его делает таймер
  awgmail

  # проход по развёртыванию в другом каталоге
  AMNEZIA_MAIL_ROOT=/srv/amnezia-vpn awgmail

  # что происходило: журнал службы
  journalctl -u amnezia-vpn-mail.service --since today
`

type deps struct {
	getenv  func(string) string
	now     func() time.Time
	send    func(ctx context.Context, cfg *mailconf.File, msg mailer.Message) error
	observe func(root string, now time.Time, cfg *mailconf.File) notify.Inputs
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	sender := &mailer.Sender{}
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr, deps{
		getenv:  os.Getenv,
		now:     time.Now,
		send:    sender.Send,
		observe: observe,
	}))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, d deps) int {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprint(stdout, usageText)
		return 0
	}
	if len(args) > 0 {
		// The argument is not echoed: whatever was typed there might be
		// the very thing that must not reach the journal.
		fmt.Fprintln(stderr, "awgmail: аргументы не принимаются; запуск — просто «awgmail», пути задаются переменными окружения (см. awgmail --help)")
		return 2
	}

	root := d.getenv("AMNEZIA_MAIL_ROOT")
	if root == "" {
		root = "/opt/amnezia-vpn"
	}
	confPath := d.getenv("AMNEZIA_MAIL_CONF_PATH")
	if confPath == "" {
		confPath = filepath.Join(root, "data", "mail.conf")
	}
	statePath := d.getenv("AMNEZIA_MAIL_STATE_PATH")
	if statePath == "" {
		statePath = filepath.Join(root, "status", mailer.StateName)
	}

	notifyPath := d.getenv("AMNEZIA_NOTIFY_STATE_PATH")
	if notifyPath == "" {
		notifyPath = filepath.Join(root, "status", "notify-state.json")
	}

	cfg, err := mailconf.Load(confPath)
	if errors.Is(err, os.ErrNotExist) {
		// Почта выключена: память правил и очередь больше ни о чём. Письма,
		// ждавшие отправки, после повторного включения были бы вчерашними
		// (amnezia-vpn-server-sjkk).
		for _, stale := range []string{notifyPath, statePath} {
			if err := os.Remove(stale); err != nil && !errors.Is(err, os.ErrNotExist) {
				fmt.Fprintf(stderr, "awgmail: %v\n", err)
			}
		}
		return 0
	}
	if err != nil {
		fmt.Fprintf(stderr, "awgmail: настройки почты %s не читаются: %v\n", confPath, err)
		return 1
	}

	now := d.now()
	// The test letter first: someone is looking at the panel and waiting.
	sendTestLetter(ctx, stdout, stderr, d, cfg, mailconf.TestLetterRequestPath(confPath),
		filepath.Join(filepath.Dir(statePath), mailconf.TestLetterResultName), now)

	rules, err := notify.LoadState(notifyPath)
	if err != nil {
		fmt.Fprintf(stderr, "awgmail: %v\n", err)
	}
	before := rules.Marshal()
	letters := notify.Evaluate(d.observe(root, now, cfg), rules)

	st, err := mailer.LoadState(statePath)
	if err != nil {
		fmt.Fprintf(stderr, "awgmail: %v\n", err)
	}
	for _, l := range letters {
		st.Put(l.Key, l.Message, now)
	}
	// The new letters and the rules' memory are written before delivery: a
	// letter is decided once, and a crash during a slow SMTP dialogue must
	// neither lose it nor decide it a second time.
	if len(letters) > 0 {
		if err := st.Save(statePath); err != nil {
			fmt.Fprintf(stderr, "awgmail: очередь писем не записана: %v\n", err)
			return 1
		}
	}
	if after := rules.Marshal(); string(after) != string(before) {
		if err := rules.Save(notifyPath); err != nil {
			fmt.Fprintf(stderr, "awgmail: память правил не записана: %v\n", err)
			return 1
		}
	}
	if len(st.Pending) == 0 {
		return 0
	}

	outcomes := st.Deliver(ctx, now, func(ctx context.Context, msg mailer.Message) error {
		return d.send(ctx, cfg, msg)
	})
	for _, o := range outcomes {
		switch {
		case o.Sent:
			fmt.Fprintf(stdout, "awgmail: отправлено «%s»\n", o.Subject)
		case o.GaveUp:
			fmt.Fprintf(stderr, "awgmail: «%s» не отправлено после %d попыток, отказ: %v\n", o.Subject, o.Attempt, o.Err)
		default:
			fmt.Fprintf(stderr, "awgmail: «%s» не отправлено (попытка %d), повтор в %s: %v\n",
				o.Subject, o.Attempt, o.RetryAt.Format(time.RFC3339), o.Err)
		}
	}
	if len(outcomes) == 0 {
		return 0
	}
	if err := st.Save(statePath); err != nil {
		fmt.Fprintf(stderr, "awgmail: очередь писем не записана: %v\n", err)
		return 1
	}
	return 0
}

// testRequestMaxAge: a request older than this is not answered. Nobody is
// waiting for it any more, and a test letter arriving an hour after the
// button was pressed would only confuse.
const testRequestMaxAge = 15 * time.Minute

// sendTestLetter answers the panel's request for a test letter
// (amnezia-vpn-server-8fg2), once per request id.
func sendTestLetter(ctx context.Context, stdout, stderr io.Writer, d deps, cfg *mailconf.File, requestPath, resultPath string, now time.Time) {
	req, err := mailconf.ReadTestLetterRequest(requestPath)
	if err != nil {
		fmt.Fprintf(stderr, "awgmail: %v\n", err)
		return
	}
	if req == nil || now.Sub(req.AtUTC) > testRequestMaxAge {
		return
	}
	if res, err := mailconf.ReadTestLetterResult(resultPath); err == nil && res != nil && res.ID == req.ID {
		return
	}
	res := &mailconf.TestLetterResult{ID: req.ID, OK: true, AtUTC: now.UTC()}
	if err := d.send(ctx, cfg, notify.TestLetter(cfg.Server)); err != nil {
		res.OK = false
		res.Summary = mailer.Summary(err)
		res.Error = err.Error()
		fmt.Fprintf(stderr, "awgmail: пробное письмо не отправлено: %v\n", err)
	} else {
		fmt.Fprintln(stdout, "awgmail: пробное письмо отправлено")
	}
	if err := mailconf.WriteTestLetterResult(resultPath, res); err != nil {
		fmt.Fprintf(stderr, "awgmail: итог пробного письма не записан: %v\n", err)
	}
}
