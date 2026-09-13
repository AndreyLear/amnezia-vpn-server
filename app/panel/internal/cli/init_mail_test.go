package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

func mailSettingsIn(t *testing.T, path string) (db.MailSettings, error) {
	t.Helper()
	h, err := db.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer h.Close()
	if err := db.Migrate(h); err != nil {
		t.Fatalf("migrate %s: %v", path, err)
	}
	return db.LoadMailSettings(h)
}

// panel-init выводит data/mail.conf из базы, а восстановление из копии
// пароль почты не приносит: после него файла для хоста нет, настройки без
// пароля остаются в базе (amnezia-vpn-server-2kr4).
func TestInitRendersMailConfAndRestoreDropsPassword(t *testing.T) {
	c := newCtx(t)
	setBackupsPath(t, c)
	t.Setenv("AMNEZIA_MAIL_CONF_PATH", "")
	mailPath := filepath.Join(c.dir, "mail.conf")
	c.seedServer("", "")
	const password = "mailbox-password-0000000000"

	h, err := db.Open(c.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(h); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveMailSettings(h, db.MailSettings{
		Host: "smtp.example.org", Port: 587, Username: "vpn@example.org",
		Password: password, Recipient: "owner@example.org",
	}, time.Now()); err != nil {
		t.Fatal(err)
	}
	h.Close()

	code, out, errb := c.run("init")
	if code != 0 {
		t.Fatalf("init exit = %d, stderr = %s", code, errb)
	}
	raw, err := os.ReadFile(mailPath)
	if err != nil {
		t.Fatalf("init не записал mail.conf рядом с базой: %v", err)
	}
	if !strings.Contains(string(raw), password) {
		t.Fatal("в mail.conf нет пароля из базы")
	}
	if strings.Contains(out+errb, password) {
		t.Fatal("пароль попал в вывод init")
	}

	out = c.mustRun("backup", "create")
	name := filepath.Base(strings.TrimSpace(out))
	if code, _, errb := c.run("restore", name); code != 0 {
		t.Fatalf("restore exit = %d, stderr = %s", code, errb)
	}
	code, out, errb = c.run("init")
	if code != 0 {
		t.Fatalf("init после restore exit = %d, stderr = %s", code, errb)
	}
	if !strings.Contains(out, "pending restore applied") {
		t.Fatalf("восстановление не применилось:\n%s", out)
	}
	if _, err := os.Stat(mailPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("mail.conf со старым паролем пережил восстановление: %v", err)
	}
	got, err := mailSettingsIn(t, c.dbPath)
	if err != nil {
		t.Fatalf("настройки почты потерялись при восстановлении: %v", err)
	}
	if !got.PasswordMissing() || got.Host != "smtp.example.org" {
		t.Fatalf("после восстановления %+v: ждали настройки без пароля", got)
	}
}
