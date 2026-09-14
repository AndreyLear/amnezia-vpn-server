package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/mailconf"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/mailer"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/notify"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/status"
)

const password = "mailbox-password-never-in-journal"

type env struct {
	root   string
	stdout bytes.Buffer
	stderr bytes.Buffer
	sent   []mailer.Message
	fail   error
	now    time.Time
	// inputs, when set, replaces the observation of real files.
	inputs func(now time.Time) notify.Inputs
}

func newEnv(t *testing.T) *env {
	t.Helper()
	root := t.TempDir()
	for _, d := range []string{"data", "status"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return &env{root: root, now: time.Date(2026, 9, 13, 3, 0, 0, 0, time.UTC), inputs: healthy}
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
		observe: func(root string, now time.Time, cfg *mailconf.File) notify.Inputs {
			if e.inputs != nil {
				return e.inputs(now)
			}
			return observe(root, now, cfg)
		},
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

// healthy is a server where nothing is happening.
func healthy(now time.Time) notify.Inputs {
	return notify.Inputs{Now: now, TunnelUp: true, Installed: "2.10.26"}
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
	// Память правил записана один раз, дальше исправный сервер диск не трогает.
	notifyPath := filepath.Join(e.root, "status", "notify-state.json")
	first, err := os.Stat(notifyPath)
	if err != nil {
		t.Fatalf("память правил не записана: %v", err)
	}
	for i := 0; i < 5; i++ {
		e.now = e.now.Add(time.Minute)
		if code := e.run(); code != 0 || e.stdout.Len() != 0 || e.stderr.Len() != 0 {
			t.Fatalf("exit %d, %q %q", code, e.stdout.String(), e.stderr.String())
		}
	}
	if again, _ := os.Stat(notifyPath); !again.ModTime().Equal(first.ModTime()) {
		t.Error("исправный сервер переписывает память правил каждую минуту")
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
	// Справка с примерами, отказ — с правильным вызовом (cli-for-agents,
	// amnezia-vpn-server-537w).
	if !strings.Contains(e.stdout.String(), "Примеры:") || !strings.Contains(e.stdout.String(), "AMNEZIA_MAIL_ROOT=") {
		t.Errorf("в --help нет примеров:\n%s", e.stdout.String())
	}
	if code := e.run("--password=" + password); code != 2 {
		t.Fatalf("аргумент принят: exit %d", code)
	}
	if !strings.Contains(e.stderr.String(), "«awgmail»") {
		t.Errorf("отказ не показывает правильный вызов: %q", e.stderr.String())
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

// Почту выключили — память правил стирается: включённая снова почта
// начнёт с точки отсчёта, а не с отчёта о том, что было без неё.
func TestMailOffDropsRulesMemory(t *testing.T) {
	e := newEnv(t)
	e.writeConf(t)
	if code := e.run(); code != 0 {
		t.Fatalf("exit %d: %s", code, e.stderr.String())
	}
	notifyPath := filepath.Join(e.root, "status", "notify-state.json")
	if _, err := os.Stat(notifyPath); err != nil {
		t.Fatalf("память правил не записана: %v", err)
	}
	if err := os.Remove(filepath.Join(e.root, "data", "mail.conf")); err != nil {
		t.Fatal(err)
	}
	if code := e.run(); code != 0 || e.stderr.Len() != 0 {
		t.Fatalf("exit %d: %s", code, e.stderr.String())
	}
	if _, err := os.Stat(notifyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("память правил осталась при выключенной почте: %v", err)
	}
}

// Сквозной путь на настоящих файлах: status.json перестал обновляться,
// через пять минут письмо уходит, а тема и тело взяты из правил, адрес
// сервера — из mail.conf (amnezia-vpn-server-0d2n).
func TestTunnelLetterFromRealFiles(t *testing.T) {
	e := newEnv(t)
	e.inputs = nil
	e.writeConfWithServer(t, "vpn.example.org")
	if err := os.WriteFile(filepath.Join(e.root, "versions.lock"), []byte("IMAGE_VERSION=2.10.26\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeStatus := func(at time.Time) {
		st := &status.Status{Schema: "v1", GeneratedAt: at, Interface: &status.Interface{Iface: "awg0", HasInterface: true, PublicKey: "pub", ListenPort: 51820}, Peers: []status.Peer{}}
		if err := status.WriteAtomic(filepath.Join(e.root, "status", "status.json"), mustJSON(t, st)); err != nil {
			t.Fatal(err)
		}
	}
	writeStatus(e.now)
	if code := e.run(); code != 0 || len(e.sent) != 0 {
		t.Fatalf("исправный туннель: exit %d, письма %v, %s", code, e.sent, e.stderr.String())
	}
	// awg замер: файл больше не обновляется.
	for i := 0; i < 8; i++ {
		e.now = e.now.Add(time.Minute)
		if code := e.run(); code != 0 {
			t.Fatalf("exit %d: %s", code, e.stderr.String())
		}
	}
	if len(e.sent) != 1 {
		t.Fatalf("писем %d, ждали одно: %+v", len(e.sent), e.sent)
	}
	if !strings.Contains(e.sent[0].Subject, "Туннель") || !strings.Contains(e.sent[0].Body, "vpn.example.org") {
		t.Fatalf("письмо %+v", e.sent[0])
	}
}

func (e *env) writeConfWithServer(t *testing.T, server string) {
	t.Helper()
	if err := mailconf.Write(filepath.Join(e.root, "data", "mail.conf"), &mailconf.File{
		Host: "smtp.example.org", Port: 587, Username: "vpn@example.org", Password: password, Recipient: "o@example.org", Server: server,
	}); err != nil {
		t.Fatal(err)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestImageVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "versions.lock")
	if err := os.WriteFile(path, []byte("# pinned\nGO_VERSION=1.25\nIMAGE_VERSION=2.10.26\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := imageVersion(path); got != "2.10.26" {
		t.Errorf("imageVersion = %q", got)
	}
	if got := imageVersion(path + ".none"); got != "" {
		t.Errorf("нет файла: %q", got)
	}
}

// Пробное письмо из панели: уходит один раз на просьбу, итог с тем же id
// ложится в status/, пароля в нём нет; старая просьба не исполняется
// (amnezia-vpn-server-8fg2).
func TestTestLetterAnsweredOnce(t *testing.T) {
	e := newEnv(t)
	e.writeConf(t)
	reqPath := mailconf.TestRequestPath(filepath.Join(e.root, "data", "mail.conf"))
	resPath := filepath.Join(e.root, "status", mailconf.TestResultName)
	req, err := mailconf.WriteTestRequest(reqPath, e.now)
	if err != nil {
		t.Fatal(err)
	}
	if code := e.run(); code != 0 {
		t.Fatalf("exit %d: %s", code, e.stderr.String())
	}
	if len(e.sent) != 1 || e.sent[0].Subject != notify.TestLetter("").Subject {
		t.Fatalf("отправлено %+v", e.sent)
	}
	res, err := mailconf.ReadTestResult(resPath)
	if err != nil || res == nil || res.ID != req.ID || !res.OK {
		t.Fatalf("итог %+v, %v", res, err)
	}
	e.now = e.now.Add(time.Minute)
	e.run()
	if len(e.sent) != 1 {
		t.Fatalf("на одну просьбу ушло %d писем", len(e.sent))
	}

	// Неудача: итог с ошибкой, пароля нет ни в итоге, ни в журнале.
	e.fail = errors.New("535 5.7.8 Authentication failed")
	if _, err := mailconf.WriteTestRequest(reqPath, e.now); err != nil {
		t.Fatal(err)
	}
	e.run()
	res, _ = mailconf.ReadTestResult(resPath)
	if res == nil || res.OK || !strings.Contains(res.Error, "535") {
		t.Fatalf("итог неудачи %+v", res)
	}
	raw, _ := os.ReadFile(resPath)
	if strings.Contains(string(raw)+e.stderr.String()+e.stdout.String(), password) {
		t.Fatal("пароль в итоге или журнале")
	}

	// Просьба, которой больше пятнадцати минут, не исполняется.
	e.fail = nil
	sent := len(e.sent)
	if _, err := mailconf.WriteTestRequest(reqPath, e.now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	e.run()
	if len(e.sent) != sent {
		t.Fatal("устаревшая просьба исполнена")
	}
}
