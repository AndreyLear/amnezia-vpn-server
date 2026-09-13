package web

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/mailconf"
)

// Восстановление в самой панели убирает mail.conf, оставшийся с прежней
// базы: пароль почты в копию не входит, и файл со старым паролем не должен
// пережить строку, из которой он был выведен (amnezia-vpn-server-2kr4).
func TestRestoreUploadDropsMailConf(t *testing.T) {
	t.Setenv("AMNEZIA_MAIL_CONF_PATH", "")
	f := newFixture(t)
	dir := setBackupsPath(t)
	if err := db.SaveMailSettings(f.h, db.MailSettings{
		Host: "smtp.example.org", Port: 587, Username: "vpn@example.org",
		Password: "mailbox-password-0000000000", Recipient: "owner@example.org",
	}, time.Now()); err != nil {
		t.Fatalf("SaveMailSettings: %v", err)
	}
	mailPath := f.server.cfg.MailConfPath
	if want := filepath.Join(filepath.Dir(f.dbPath), "mail.conf"); mailPath != want {
		t.Fatalf("MailConfPath = %q, ждали %q рядом с базой", mailPath, want)
	}
	if err := mailconf.Render(f.h, mailPath); err != nil {
		t.Fatalf("Render: %v", err)
	}

	name := makeBackup(t, f, dir, time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC))
	archive, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	rec := postRestoreUpload(t, f, restoreFields(f), map[string][]byte{name: archive})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("upload: code = %d, want 303", rec.Code)
	}
	if _, err := os.Stat(mailPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("mail.conf пережил восстановление: %v", err)
	}
	got, err := db.LoadMailSettings(f.server.db())
	if err != nil || !got.PasswordMissing() || got.Host != "smtp.example.org" {
		t.Fatalf("после восстановления %+v, %v: ждали настройки без пароля", got, err)
	}
}
