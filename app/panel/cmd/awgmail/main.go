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
//   - reads the outbox (status/mail-state.json, beside the watchdog's
//     services.json, where the panel can read it);
//   - delivers what is due, retrying a failure after 1, 5 and 15 minutes
//     (internal/mailer);
//   - writes the outbox back.
//
// What to write about is decided by the event rules (amnezia-vpn-server-0d2n),
// which put messages into the outbox before delivery.
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

Ключи:
  --help    показать эту справку
`

type deps struct {
	getenv func(string) string
	now    func() time.Time
	send   func(ctx context.Context, cfg *mailconf.File, msg mailer.Message) error
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	sender := &mailer.Sender{}
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr, deps{
		getenv: os.Getenv,
		now:    time.Now,
		send:   sender.Send,
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
		fmt.Fprintln(stderr, "awgmail: аргументы не принимаются, см. --help")
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
		statePath = filepath.Join(root, "status", "mail-state.json")
	}

	cfg, err := mailconf.Load(confPath)
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		fmt.Fprintf(stderr, "awgmail: настройки почты %s не читаются: %v\n", confPath, err)
		return 1
	}

	st, err := mailer.LoadState(statePath)
	if err != nil {
		fmt.Fprintf(stderr, "awgmail: %v\n", err)
	}
	if len(st.Pending) == 0 {
		return 0
	}

	now := d.now()
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
