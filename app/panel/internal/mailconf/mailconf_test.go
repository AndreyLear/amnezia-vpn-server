package mailconf

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sample() *File {
	return &File{
		Host: "smtp.example.org", Port: 465, Username: "vpn@example.org",
		Password: `p"a\ss = word`, Recipient: "owner@example.org",
	}
}

// Записанный файл читается обратно без искажений пароля с кавычками и
// обратной косой, права 0600, временных файлов не остаётся
// (amnezia-vpn-server-2kr4).
func TestWriteLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mail.conf")
	if err := Write(path, sample()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("права %o, ждали 600", mode)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if *got != *sample() {
		t.Fatalf("прочитано %+v, записано %+v", got, sample())
	}
	if leftovers, _ := filepath.Glob(filepath.Join(dir, "mail.conf.tmp-*")); len(leftovers) != 0 {
		t.Errorf("остались временные файлы: %v", leftovers)
	}
}

func TestWriteNilRemoves(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mail.conf")
	if err := Write(path, sample()); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, nil); err != nil {
		t.Fatalf("Write(nil): %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("файл остался: %v", err)
	}
	if err := Write(path, nil); err != nil {
		t.Fatalf("удалять уже удалённое — не ошибка: %v", err)
	}
}

// Отсутствующий файл отличим от испорченного: первое значит «почта
// выключена», второе — поломку. Текст ошибки не цитирует содержимое.
func TestLoadMissingAndBroken(t *testing.T) {
	dir := t.TempDir()
	if _, err := Load(filepath.Join(dir, "none.conf")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("нет файла: err = %v, ждали ErrNotExist", err)
	}
	cases := map[string]string{
		"не JSON":    `{"password":"secret-in-broken-file",`,
		"без пароля": `{"host":"h","port":587,"username":"u","password":"","recipient":"r"}`,
		"порт ноль":  `{"host":"h","port":0,"username":"u","password":"secret-in-broken-file","recipient":"r"}`,
		"без адреса": `{"host":"h","port":587,"username":"u","password":"secret-in-broken-file"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(dir, "broken.conf")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path)
			if !errors.Is(err, ErrUnusable) {
				t.Fatalf("err = %v, ждали ErrUnusable", err)
			}
			if strings.Contains(err.Error(), "secret-in-broken-file") {
				t.Fatal("ошибка цитирует пароль из файла")
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
