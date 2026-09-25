package web

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

// Пока восстановление из CLI ждёт перезапуска, изменения клиентов из панели
// потом молча теряются: база подменяется образом из архива. Изменяющие
// маршруты отказывают понятным текстом, читающие работают
// (amnezia-vpn-server-cb48).
func TestClientMutationsRefuseWhileRestorePending(t *testing.T) {
	f := newFixture(t)
	c, _, _ := f.addClient("phone")
	id := strconv.FormatInt(c.ID, 10)
	if err := os.Mkdir(filepath.Join(filepath.Dir(f.dbPath), ".restore-pending"), 0o700); err != nil {
		t.Fatal(err)
	}

	api := []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, "/api/clients", map[string]any{"name": "laptop"}},
		{http.MethodPatch, "/api/clients/" + id, map[string]any{"name": "tablet"}},
		{http.MethodPatch, "/api/clients/" + id, map[string]any{"enabled": false}},
		{http.MethodPatch, "/api/clients/" + id, map[string]any{"mtu": 1300}},
		{http.MethodPatch, "/api/clients/" + id, map[string]any{"rate_limit": 10}},
		{http.MethodDelete, "/api/clients/" + id, nil},
	}
	for _, tc := range api {
		rec := f.apiCSRF(tc.method, tc.path, tc.body)
		if rec.Code != http.StatusConflict {
			t.Errorf("%s %s: код %d, ожидался 409: %s", tc.method, tc.path, rec.Code, rec.Body.String())
			continue
		}
		got := decodeAPI(t, rec)
		if got["ok"] != false || got["message"] != flashRestoreBlocksChange {
			t.Errorf("%s %s: ответ %v", tc.method, tc.path, got)
		}
	}

	forms := []struct {
		path string
		form url.Values
	}{
		{"/clients/new", url.Values{"name": {"laptop"}}},
		{"/clients/" + id + "/disable", nil},
		{"/clients/" + id + "/enable", nil},
		{"/clients/" + id + "/rename", url.Values{"name": {"tablet"}}},
		{"/clients/" + id + "/delete", nil},
	}
	for _, tc := range forms {
		if msg := f.flashOf(f.post(tc.path, tc.form)); msg != flashRestoreBlocksChange {
			t.Errorf("POST %s: flash %q", tc.path, msg)
		}
	}

	clients, err := db.ClientsAll(f.h)
	if err != nil {
		t.Fatal(err)
	}
	if len(clients) != 1 || clients[0].Name != "phone" || !clients[0].Enabled ||
		clients[0].MTU != 0 || clients[0].RateLimit != 0 {
		t.Fatalf("база изменена при ожидающем восстановлении: %+v", clients)
	}
	if _, err := os.Stat(f.confPath); !os.IsNotExist(err) {
		t.Fatalf("awg0.conf перезаписан при ожидающем восстановлении: %v", err)
	}
	if rec := f.get("/api/clients"); rec.Code != http.StatusOK {
		t.Fatalf("чтение списка: код %d", rec.Code)
	}
}

// Настройки почты лежат в той же базе и так же сменятся образом из архива:
// сохранение и удаление отказывают тем же текстом (amnezia-vpn-server-cb48).
func TestMailChangesRefuseWhileRestorePending(t *testing.T) {
	f := newFixture(t)
	saved := db.MailSettings{Host: "smtp.example.com", Port: 587, Username: "me", Password: "secret", Recipient: "me@example.com"}
	if err := db.SaveMailSettings(f.h, saved, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(filepath.Dir(f.dbPath), ".restore-pending"), 0o700); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		method string
		body   any
	}{
		{http.MethodPut, map[string]any{"host": "smtp.other.com", "port": 465, "username": "you", "password": "x", "recipient": "you@example.com"}},
		{http.MethodDelete, nil},
	} {
		rec := f.apiCSRF(tc.method, "/api/mail", tc.body)
		if rec.Code != http.StatusConflict {
			t.Errorf("%s /api/mail: код %d, ожидался 409: %s", tc.method, rec.Code, rec.Body.String())
			continue
		}
		if got := decodeAPI(t, rec); got["ok"] != false || got["message"] != flashRestoreBlocksChange {
			t.Errorf("%s /api/mail: ответ %v", tc.method, got)
		}
	}

	got, err := db.LoadMailSettings(f.h)
	if err != nil {
		t.Fatalf("настройки почты пропали: %v", err)
	}
	if got.Host != saved.Host || got.Recipient != saved.Recipient {
		t.Fatalf("настройки почты изменены при ожидающем восстановлении: %+v", got)
	}
}
