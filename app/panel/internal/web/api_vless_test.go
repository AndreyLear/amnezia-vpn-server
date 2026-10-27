package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/xray"
)

func vlessFixture(t *testing.T) (*fixture, *bytes.Buffer, *db.ClientRecord) {
	t.Helper()
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
	if err := db.SaveXrayEntry(f.h, db.XrayEntry{
		Name: "ru", LinkHost: "ru.example.com", LinkPort: 443, SNI: "ru.example.com",
		PublicKey: s.PublicKey, ShortID: "a1b2", Path: "/entry", LinkPrefix: "RU ",
	}); err != nil {
		t.Fatal(err)
	}
	c, _, _ := f.addClient("andrey")
	return f, &logs, c
}

func decodeVLESS(t *testing.T, rec *httptest.ResponseRecorder) vlessJSON {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("code %d: %s", rec.Code, rec.Body.String())
	}
	var out vlessJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// Окно клиента получает ссылки VLESS по всем маршрутам — напрямую и через
// каждый узел-вход — и QR для каждой (amnezia-vpn-server-14y0.5).
func TestAPIClientVLESS(t *testing.T) {
	f, logs, c := vlessFixture(t)
	path := fmt.Sprintf("/api/clients/%d/vless", c.ID)

	rec := f.get(path)
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control = %q: ссылка — секрет, в кеше ей не место", cc)
	}
	got := decodeVLESS(t, rec)
	direct, _ := xray.ClientLink(f.h, c.ID)
	if !got.Enabled || len(got.Routes) != 2 ||
		got.Routes[0] != (vlessRouteJSON{Link: direct}) ||
		got.Routes[1].Entry != "ru" || got.Routes[1].Prefix != "RU " ||
		!strings.HasSuffix(got.Routes[1].Link, "#RU%20andrey") {
		t.Fatalf("ответ: %+v", got)
	}

	for _, tc := range []struct{ query, want string }{
		{"", direct},
		{"?entry=ru", got.Routes[1].Link},
	} {
		qr := f.get(path + "/qr" + tc.query)
		if qr.Code != http.StatusOK || qr.Header().Get("Content-Type") != "image/png" ||
			qr.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("QR%s: %d %v", tc.query, qr.Code, qr.Header())
		}
		if text := decodeQR(t, qr.Body.Bytes()); text != tc.want {
			t.Fatalf("QR%s несёт %q, ожидалась ссылка маршрута", tc.query, text)
		}
	}
	if rec := f.get(path + "/qr?entry=nope"); rec.Code != http.StatusNotFound {
		t.Fatalf("QR неизвестного узла: %d", rec.Code)
	}
	if strings.Contains(logs.String(), "vless://") {
		t.Fatal("ссылка попала в журнал")
	}
}

// Отключённому клиенту ссылок нет — ни списком, ни QR.
func TestAPIClientVLESSDisabled(t *testing.T) {
	f, _, c := vlessFixture(t)
	if err := db.SetClientEnabled(f.h, c.ID, false); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/clients/%d/vless", c.ID)
	got := decodeVLESS(t, f.get(path))
	if got.Enabled || len(got.Routes) != 0 {
		t.Fatalf("отключённому: %+v", got)
	}
	if rec := f.get(path + "/qr"); rec.Code != http.StatusNotFound {
		t.Fatalf("QR отключённому: %d", rec.Code)
	}
}

func TestAPIClientVLESSUnknownAndUnauthorized(t *testing.T) {
	f, _, c := vlessFixture(t)
	if rec := f.get("/api/clients/999/vless"); rec.Code != http.StatusNotFound {
		t.Fatalf("неизвестный клиент: %d", rec.Code)
	}
	if rec := f.get("/api/clients/999/vless/qr"); rec.Code != http.StatusNotFound {
		t.Fatalf("QR неизвестного клиента: %d", rec.Code)
	}
	for _, p := range []string{"/vless", "/vless/qr"} {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/clients/%d%s", c.ID, p), nil)
		rec := httptest.NewRecorder()
		f.server.ServeHTTP(rec, req)
		assertAPIUnauthorized(t, rec)
		if strings.Contains(rec.Body.String(), "vless://") {
			t.Fatal("ссылка выдана без входа")
		}
	}
}
