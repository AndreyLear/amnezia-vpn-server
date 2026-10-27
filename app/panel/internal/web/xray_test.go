package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/xray"
)

// deadXrayAPI направляет API Xray на закрытый порт: тест не должен
// достучаться ни до чего настоящего на машине.
func deadXrayAPI(t *testing.T) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	t.Setenv("AMNEZIA_XRAY_API_ADDR", addr)
	t.Setenv("AMNEZIA_XRAY_CONFIG_PATH", "")
}

func xrayEmails(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("конфиг Xray не записан: %v", err)
	}
	var cfg struct {
		Inbounds []struct {
			Settings struct {
				Clients []struct{ ID, Email string }
			}
		}
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, in := range cfg.Inbounds {
		for _, c := range in.Settings.Clients {
			out[c.Email] = c.ID
		}
	}
	return out
}

// Каждое изменение клиентов в панели перегенерирует и конфиг Xray, как
// awg0.conf; недоступный API Xray изменение не срывает
// (amnezia-vpn-server-14y0.2).
func TestClientChangesRegenerateXrayConfig(t *testing.T) {
	deadXrayAPI(t)
	var logs bytes.Buffer
	f := newFixtureWithLogger(t, &logs)
	s, err := xray.NewServerSettings("vpn.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SaveXraySettings(f.h, *s); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(filepath.Dir(f.confPath), "xray", "config.json")

	rec := f.apiCSRF(http.MethodPost, "/api/clients", map[string]string{"name": "andrey"})
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("POST /api/clients: %d %s", rec.Code, rec.Body.String())
	}
	id := int64(decodeAPI(t, rec)["id"].(float64))
	c, err := db.ClientByID(f.h, id)
	if err != nil {
		t.Fatal(err)
	}
	email := fmt.Sprintf("c%d", id)
	if got := xrayEmails(t, path); got[email] != c.VLESSUUID {
		t.Fatalf("новый клиент не попал в конфиг Xray: %v", got)
	}

	rec = f.apiCSRF(http.MethodPatch, fmt.Sprintf("/api/clients/%d", id), map[string]any{"enabled": false})
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH: %d %s", rec.Code, rec.Body.String())
	}
	if _, ok := xrayEmails(t, path)[email]; ok {
		t.Fatal("отключённый клиент остался в конфиге Xray")
	}
	if !bytes.Contains(logs.Bytes(), []byte("xray: config written")) {
		t.Fatalf("недоступный API Xray не отмечен в журнале: %q", logs.String())
	}

	if got := f.flashOf(f.post(fmt.Sprintf("/clients/%d/enable", id), nil)); got != flashEnabled {
		t.Fatalf("enable: %q", got)
	}
	if _, ok := xrayEmails(t, path)[email]; !ok {
		t.Fatal("включённый клиент не вернулся в конфиг Xray")
	}

	if rec := f.apiCSRF(http.MethodDelete, fmt.Sprintf("/api/clients/%d", id), nil); rec.Code != http.StatusOK {
		t.Fatalf("DELETE: %d", rec.Code)
	}
	if _, ok := xrayEmails(t, path)[email]; ok {
		t.Fatal("удалённый клиент остался в конфиге Xray")
	}
}

// Без настроек Xray панель работает как прежде и файла не пишет.
func TestNoXrayConfigWithoutSettings(t *testing.T) {
	deadXrayAPI(t)
	f := newFixtureWithLogger(t, log.Writer())
	rec := f.apiCSRF(http.MethodPost, "/api/clients", map[string]string{"name": "andrey"})
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("POST /api/clients: %d", rec.Code)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(f.confPath), "xray")); !os.IsNotExist(err) {
		t.Fatalf("каталог Xray появился без настроек: %v", err)
	}
}
