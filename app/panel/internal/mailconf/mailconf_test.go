package mailconf

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

func openDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	dir := t.TempDir()
	h, err := db.Open(filepath.Join(dir, "amnezia.sqlite"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { h.Close() })
	if err := db.Migrate(h); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	return h, dir
}

func sample() db.MailSettings {
	return db.MailSettings{
		Host: "smtp.example.org", Port: 465, Username: "vpn@example.org",
		Password: `p"a\ss = word`, Recipient: "owner@example.org",
	}
}

// Файл для хоста повторяет строку базы, пароль с кавычками и обратной
// косой читается обратно без искажений, права 0600 (amnezia-vpn-server-2kr4).
func TestRenderWritesSettings(t *testing.T) {
	h, dir := openDB(t)
	if err := db.SaveMailSettings(h, sample(), time.Now()); err != nil {
		t.Fatalf("SaveMailSettings: %v", err)
	}
	path := filepath.Join(dir, "mail.conf")
	if err := Render(h, path); err != nil {
		t.Fatalf("Render: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("права %o, ждали 600", mode)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var got File
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("файл не читается как JSON: %v", err)
	}
	s := sample()
	want := File{Host: s.Host, Port: s.Port, Username: s.Username, Password: s.Password, Recipient: s.Recipient}
	if got != want {
		t.Fatalf("в файле %+v, ждали %+v", got, want)
	}
	leftovers, _ := filepath.Glob(filepath.Join(dir, "mail.conf.tmp-*"))
	if len(leftovers) != 0 {
		t.Errorf("остались временные файлы: %v", leftovers)
	}
}

// Отправлять нечем — файла нет. Иначе служба пыталась бы войти со старым
// паролем, который пережил свою строку в базе.
func TestRenderRemovesFileWhenUnusable(t *testing.T) {
	cases := map[string]func(t *testing.T, h *sql.DB){
		"почта не настроена": func(t *testing.T, h *sql.DB) {},
		"пароля нет после восстановления": func(t *testing.T, h *sql.DB) {
			m := sample()
			m.Password = ""
			if err := db.SaveMailSettings(h, m, time.Now()); err != nil {
				t.Fatalf("SaveMailSettings: %v", err)
			}
		},
	}
	for name, seed := range cases {
		t.Run(name, func(t *testing.T) {
			h, dir := openDB(t)
			path := filepath.Join(dir, "mail.conf")
			if err := os.WriteFile(path, []byte(`{"password":"old"}`), 0o600); err != nil {
				t.Fatal(err)
			}
			seed(t, h)
			if err := Render(h, path); err != nil {
				t.Fatalf("Render: %v", err)
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("файл остался: %v", err)
			}
			// Удалять уже удалённое — не ошибка.
			if err := Render(h, path); err != nil {
				t.Fatalf("повторный Render: %v", err)
			}
		})
	}
}

func TestPathFor(t *testing.T) {
	t.Setenv("AMNEZIA_MAIL_CONF_PATH", "")
	if got := PathFor("/data/amnezia.sqlite"); got != "/data/mail.conf" {
		t.Errorf("PathFor = %q, ждали /data/mail.conf", got)
	}
	t.Setenv("AMNEZIA_MAIL_CONF_PATH", "/elsewhere/m.conf")
	if got := PathFor("/data/amnezia.sqlite"); got != "/elsewhere/m.conf" {
		t.Errorf("с переопределением PathFor = %q", got)
	}
}
