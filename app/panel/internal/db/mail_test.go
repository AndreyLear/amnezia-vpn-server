package db

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/mailconf"
)

func sampleMail() MailSettings {
	return MailSettings{
		Host: "smtp.example.org", Port: MailDefaultPort, Username: "vpn@example.org",
		Password: "p@ss word \"quoted\"", Recipient: "owner@example.org",
	}
}

// Настройки почты проходят круг: не заданы → записаны → заменены →
// удалены (amnezia-vpn-server-2kr4).
func TestMailSettingsRoundTrip(t *testing.T) {
	d := openSessionsDB(t)
	if _, err := LoadMailSettings(d.h); !errors.Is(err, ErrMailNotConfigured) {
		t.Fatalf("пустая база: err = %v, ждали ErrMailNotConfigured", err)
	}
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	if err := SaveMailSettings(d.h, sampleMail(), now); err != nil {
		t.Fatalf("SaveMailSettings: %v", err)
	}
	got, err := LoadMailSettings(d.h)
	if err != nil {
		t.Fatalf("LoadMailSettings: %v", err)
	}
	want := sampleMail()
	want.UpdatedAt = now
	if got != want {
		t.Fatalf("прочитано %+v, записано %+v", got, want)
	}

	next := sampleMail()
	next.Port = 465
	next.Recipient = "other@example.org"
	if err := SaveMailSettings(d.h, next, now.Add(time.Hour)); err != nil {
		t.Fatalf("повторное сохранение: %v", err)
	}
	if got, _ := LoadMailSettings(d.h); got.Port != 465 || got.Recipient != "other@example.org" {
		t.Fatalf("после замены %+v", got)
	}
	var rows int
	if err := d.h.QueryRow(`SELECT count(*) FROM mail_settings`).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("строк %d (%v), ждали одну", rows, err)
	}

	if err := DeleteMailSettings(d.h); err != nil {
		t.Fatalf("DeleteMailSettings: %v", err)
	}
	if _, err := LoadMailSettings(d.h); !errors.Is(err, ErrMailNotConfigured) {
		t.Fatalf("после удаления err = %v", err)
	}
}

// Повторная миграция не трогает сохранённые настройки.
func TestMailSettingsSurviveMigrate(t *testing.T) {
	d := openSessionsDB(t)
	if err := SaveMailSettings(d.h, sampleMail(), time.Now()); err != nil {
		t.Fatalf("SaveMailSettings: %v", err)
	}
	if err := Migrate(d.h); err != nil {
		t.Fatalf("повторный Migrate: %v", err)
	}
	if got, err := LoadMailSettings(d.h); err != nil || got.Password != sampleMail().Password {
		t.Fatalf("после миграции %+v, %v", got, err)
	}
}

// Без пароля настройки есть, но отправлять нечем — так выглядит база
// после восстановления из копии.
func TestMailPasswordMissing(t *testing.T) {
	m := sampleMail()
	if m.PasswordMissing() {
		t.Fatal("с паролем PasswordMissing = true")
	}
	m.Password = ""
	if !m.PasswordMissing() {
		t.Fatal("без пароля PasswordMissing = false")
	}
}

func TestMailSettingsValidate(t *testing.T) {
	cases := map[string]func(*MailSettings){
		"пустой хост":             func(m *MailSettings) { m.Host = " " },
		"порт ноль":               func(m *MailSettings) { m.Port = 0 },
		"порт больше 65535":       func(m *MailSettings) { m.Port = 65536 },
		"пустой логин":            func(m *MailSettings) { m.Username = "" },
		"пустой получатель":       func(m *MailSettings) { m.Recipient = "" },
		"перевод строки в хосте":  func(m *MailSettings) { m.Host = "smtp.example.org\r\nRCPT TO:<x@y>" },
		"перевод строки в логине": func(m *MailSettings) { m.Username = "a\nb" },
		"перевод строки в пароле": func(m *MailSettings) { m.Password = "a\rb" },
		"перевод строки в адресе": func(m *MailSettings) { m.Recipient = "a@b\nBcc: c@d" },
	}
	d := openSessionsDB(t)
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			m := sampleMail()
			mutate(&m)
			err := SaveMailSettings(d.h, m, time.Now())
			if !errors.Is(err, ErrMailInvalid) {
				t.Fatalf("err = %v, ждали ErrMailInvalid", err)
			}
			if len(m.Password) > 3 && strings.Contains(err.Error(), m.Password) {
				t.Fatal("текст ошибки содержит пароль")
			}
		})
	}
	if _, err := LoadMailSettings(d.h); !errors.Is(err, ErrMailNotConfigured) {
		t.Fatalf("отвергнутые настройки записались: %v", err)
	}
	ok := sampleMail()
	ok.Password = ""
	if err := ok.Validate(); err != nil {
		t.Fatalf("пустой пароль отвергнут: %v — после восстановления настройки без пароля законны", err)
	}
}

// mail.conf повторяет строку базы; когда отправлять нечем, файла нет —
// иначе служба пыталась бы войти со старым паролем (amnezia-vpn-server-2kr4).
func TestRenderMailConf(t *testing.T) {
	d := openSessionsDB(t)
	path := filepath.Join(t.TempDir(), "mail.conf")

	if err := os.WriteFile(path, []byte(`{"password":"old"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RenderMailConf(d.h, path); err != nil {
		t.Fatalf("не настроено: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("почта не настроена, а файл остался: %v", err)
	}

	if err := SaveMailSettings(d.h, sampleMail(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := RenderMailConf(d.h, path); err != nil {
		t.Fatalf("RenderMailConf: %v", err)
	}
	got, err := mailconf.Load(path)
	if err != nil {
		t.Fatalf("mailconf.Load: %v", err)
	}
	m := sampleMail()
	want := mailconf.File{Host: m.Host, Port: m.Port, Username: m.Username, Password: m.Password, Recipient: m.Recipient}
	if *got != want {
		t.Fatalf("в файле %+v, ждали %+v", got, want)
	}

	m.Password = ""
	if err := SaveMailSettings(d.h, m, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := RenderMailConf(d.h, path); err != nil {
		t.Fatalf("без пароля: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("пароля нет, а файл со старым паролем остался: %v", err)
	}
}
