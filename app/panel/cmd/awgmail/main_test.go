package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/mailconf"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/mailer"
)

const password = "mailbox-password-never-in-journal"

type env struct {
	root   string
	stdout bytes.Buffer
	stderr bytes.Buffer
	sent   []mailer.Message
	fail   error
	now    time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	root := t.TempDir()
	for _, d := range []string{"data", "status"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return &env{root: root, now: time.Date(2026, 9, 13, 3, 0, 0, 0, time.UTC)}
}

func (e *env) run(args ...string) int {
	e.stdout.Reset()
	e.stderr.Reset()
	return run(context.Background(), args, &e.stdout, &e.stderr, deps{
		getenv: func(k string) string {
			if k == "AMNEZIA_MAIL_ROOT" {
				return e.root
			}
			return ""
		},
		now: func() time.Time { return e.now },
		send: func(_ context.Context, cfg *mailconf.File, msg mailer.Message) error {
			if cfg.Password != password {
				return errors.New("wrong password passed to sender")
			}
			if e.fail != nil {
				return e.fail
			}
			e.sent = append(e.sent, msg)
			return nil
		},
	})
}

func (e *env) statePath() string { return filepath.Join(e.root, "status", "mail-state.json") }

func (e *env) writeConf(t *testing.T) {
	t.Helper()
	if err := mailconf.Write(filepath.Join(e.root, "data", "mail.conf"), &mailconf.File{
		Host: "smtp.example.org", Port: 587, Username: "vpn@example.org", Password: password, Recipient: "o@example.org",
	}); err != nil {
		t.Fatal(err)
	}
}

func (e *env) queue(t *testing.T, subject string) {
	t.Helper()
	st, err := mailer.LoadState(e.statePath())
	if err != nil {
		t.Fatal(err)
	}
	st.Put("tunnel", mailer.Message{Subject: subject}, e.now)
	if err := st.Save(e.statePath()); err != nil {
		t.Fatal(err)
	}
}

// Почта не настроена — служба молчит, не падает и ничего не пишет на
// диск, даже если в очереди что-то лежит (amnezia-vpn-server-hxgr).
func TestNoMailConfIsSilent(t *testing.T) {
	e := newEnv(t)
	e.queue(t, "Туннель не работает 5 минут")
	before, _ := os.ReadFile(e.statePath())
	if code := e.run(); code != 0 {
		t.Fatalf("exit %d, stderr %s", code, e.stderr.String())
	}
	if e.stdout.Len() != 0 || e.stderr.Len() != 0 {
		t.Fatalf("без настроек служба говорит: %q %q", e.stdout.String(), e.stderr.String())
	}
	if len(e.sent) != 0 {
		t.Fatal("письмо ушло без настроек")
	}
	if after, _ := os.ReadFile(e.statePath()); !bytes.Equal(before, after) {
		t.Fatal("очередь переписана без настроек")
	}
}

// Настроено, но писать не о чем — тишина и никакой записи на диск.
func TestEmptyOutboxIsSilent(t *testing.T) {
	e := newEnv(t)
	e.writeConf(t)
	if code := e.run(); code != 0 || e.stdout.Len() != 0 || e.stderr.Len() != 0 {
		t.Fatalf("exit %d, %q %q", code, e.stdout.String(), e.stderr.String())
	}
	if _, err := os.Stat(e.statePath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("пустой прогон создал файл очереди: %v", err)
	}
}

// Письмо из очереди уходит, неудача планирует повтор; ни в выводе, ни в
// файле очереди нет пароля, даже когда ошибка отправителя его содержит —
// очистка стоит в Sender, а здесь проверяется, что main не добавляет своих
// путей утечки.
func TestDeliverAndRetryWithoutPasswordInJournal(t *testing.T) {
	e := newEnv(t)
	e.writeConf(t)
	e.queue(t, "Туннель не работает 5 минут")

	e.fail = errors.New("421 4.7.0 try again later")
	if code := e.run(); code != 0 {
		t.Fatalf("exit %d: %s", code, e.stderr.String())
	}
	if !strings.Contains(e.stderr.String(), "повтор в 2026-09-13T03:01:00Z") {
		t.Fatalf("нет сообщения о повторе: %q", e.stderr.String())
	}
	state, _ := os.ReadFile(e.statePath())
	for where, s := range map[string]string{"stdout": e.stdout.String(), "stderr": e.stderr.String(), "очередь": string(state)} {
		if strings.Contains(s, password) {
			t.Fatalf("пароль в %s", where)
		}
	}

	e.fail = nil
	e.now = e.now.Add(time.Minute)
	if code := e.run(); code != 0 {
		t.Fatalf("exit %d: %s", code, e.stderr.String())
	}
	if len(e.sent) != 1 || !strings.Contains(e.stdout.String(), "отправлено «Туннель не работает 5 минут»") {
		t.Fatalf("sent=%v stdout=%q", e.sent, e.stdout.String())
	}
	st, err := mailer.LoadState(e.statePath())
	if err != nil || len(st.Pending) != 0 || st.LastSuccessAt == nil {
		t.Fatalf("после отправки %+v, %v", st, err)
	}
}

func TestArguments(t *testing.T) {
	e := newEnv(t)
	if code := e.run("--help"); code != 0 || !strings.Contains(e.stdout.String(), "awgmail") {
		t.Fatalf("--help: %d %q", code, e.stdout.String())
	}
	if code := e.run("--password=" + password); code != 2 {
		t.Fatalf("аргумент принят: exit %d", code)
	}
	if strings.Contains(e.stderr.String(), password) {
		t.Fatal("аргумент с паролем повторён в выводе")
	}
}

func TestBrokenConfFails(t *testing.T) {
	e := newEnv(t)
	if err := os.WriteFile(filepath.Join(e.root, "data", "mail.conf"), []byte(`{"password":"`+password+`"`), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := e.run(); code != 1 {
		t.Fatalf("испорченные настройки: exit %d", code)
	}
	if strings.Contains(e.stderr.String(), password) {
		t.Fatal("пароль из испорченного файла в выводе")
	}
}
