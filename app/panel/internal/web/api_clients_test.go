package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/auth"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/status"
)

func (f *fixture) apiCSRF(method, path string, body any) *httptest.ResponseRecorder {
	f.t.Helper()
	req := apiJSON(f.t, method, path, body)
	if method != http.MethodGet {
		req.Header.Set(auth.CSRFHeaderName, f.csrf)
	}
	rec := httptest.NewRecorder()
	f.serve(rec, req)
	return rec
}

func decodeClientList(t *testing.T, rec *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("content-type = %q, want application/json; body=%s", ct, rec.Body.String())
	}
	var out []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("list json: %v body=%s", err, rec.Body.String())
	}
	return out
}

func TestAPIClientsJSONOmitsKeys(t *testing.T) {
	f := newFixture(t)
	_, priv, psk := f.addClient("keys-check")
	rec := f.get("/api/clients")
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/clients code = %d, want 200; body=%s", rec.Code, body)
	}
	lower := strings.ToLower(body)
	for _, needle := range []string{"private_key", "preshared_key", "public_key"} {
		if strings.Contains(lower, needle) {
			t.Fatalf("list JSON must omit %s: %s", needle, body)
		}
	}
	if strings.Contains(body, priv) || strings.Contains(body, psk) {
		t.Fatalf("list JSON leaked key material: %s", body)
	}
}

func TestAPICreateClientWithDescription(t *testing.T) {
	f := newFixture(t)
	rec := f.apiCSRF(http.MethodPost, "/api/clients", map[string]string{
		"name":        "with-note",
		"description": "office laptop",
	})
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("POST /api/clients code = %d; body=%s", rec.Code, rec.Body.String())
	}
	created := decodeAPI(t, rec)
	id, ok := created["id"].(float64)
	if !ok || id < 1 {
		t.Fatalf("created id = %v", created["id"])
	}
	if created["description"] != "office laptop" {
		t.Fatalf("POST description = %v", created["description"])
	}
	got := f.get(fmt.Sprintf("/api/clients/%d", int64(id)))
	one := decodeAPI(t, got)
	if one["name"] != "with-note" || one["description"] != "office laptop" {
		t.Fatalf("GET client = %v", one)
	}
	list := decodeClientList(t, f.get("/api/clients"))
	if len(list) != 1 || list[0]["description"] != "office laptop" {
		t.Fatalf("list = %v", list)
	}
}

func TestAPICreateClientEmptyName(t *testing.T) {
	f := newFixture(t)
	rec := f.apiCSRF(http.MethodPost, "/api/clients", map[string]string{
		"name":        "  ",
		"description": "ignored",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty name code = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	got := decodeAPI(t, rec)
	if got["ok"] != false {
		t.Fatalf("ok = %v", got["ok"])
	}
	if got["message"] != flashInvalidName {
		t.Fatalf("message = %q, want %q", got["message"], flashInvalidName)
	}
}

func TestAPIClientUnknownID(t *testing.T) {
	f := newFixture(t)
	rec := f.get("/api/clients/999")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET unknown code = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	got := decodeAPI(t, rec)
	if got["ok"] != false {
		t.Fatalf("ok = %v", got["ok"])
	}

	patch := f.apiCSRF(http.MethodPatch, "/api/clients/999", map[string]any{"enabled": false})
	if patch.Code != http.StatusNotFound {
		t.Fatalf("PATCH unknown code = %d, want 404; body=%s", patch.Code, patch.Body.String())
	}
	del := f.apiCSRF(http.MethodDelete, "/api/clients/999", nil)
	if del.Code != http.StatusNotFound {
		t.Fatalf("DELETE unknown code = %d, want 404; body=%s", del.Code, del.Body.String())
	}
}

func TestAPIPatchAndDeleteClient(t *testing.T) {
	f := newFixture(t)
	c, _, _ := f.addClient("orig")
	rec := f.apiCSRF(http.MethodPatch, fmt.Sprintf("/api/clients/%d", c.ID), map[string]any{
		"name":        "renamed",
		"description": "updated",
		"enabled":     false,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH code = %d; body=%s", rec.Code, rec.Body.String())
	}
	got := decodeAPI(t, rec)
	if got["name"] != "renamed" || got["description"] != "updated" || got["enabled"] != false {
		t.Fatalf("PATCH body = %v", got)
	}
	stored, err := db.ClientByID(f.h, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Name != "renamed" || stored.Description != "updated" || stored.Enabled {
		t.Fatalf("stored after PATCH = %+v", stored)
	}

	del := f.apiCSRF(http.MethodDelete, fmt.Sprintf("/api/clients/%d", c.ID), nil)
	if del.Code != http.StatusOK {
		t.Fatalf("DELETE code = %d; body=%s", del.Code, del.Body.String())
	}
	if _, err := db.ClientByID(f.h, c.ID); err != db.ErrClientNotFound {
		t.Fatalf("after delete: %v", err)
	}
}

func TestAPIClientHandshakeFields(t *testing.T) {
	f := newFixture(t)
	c, _, _ := f.addClient("online-one")
	hsAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	f.setStatus(&status.Status{
		Schema:      status.SchemaVersion,
		GeneratedAt: time.Now().UTC(),
		Interface: &status.Interface{
			Iface: "awg0", HasInterface: true, PublicKey: "qXvOE2SbilF5RNpHXPU8xQCTvUIxYPeTNI6Rnp+bqnw=",
			ListenPort: 51820, FWMark: "off",
		},
		Peers: []status.Peer{{
			PublicKey:        c.PublicKey,
			LastHandshakeUTC: &hsAt,
			RxBytes:          11,
			TxBytes:          22,
		}},
	})
	got := decodeAPI(t, f.get(fmt.Sprintf("/api/clients/%d", c.ID)))
	if got["online"] != true {
		t.Fatalf("online = %v, want true", got["online"])
	}
	if got["rx_bytes"] != float64(11) || got["tx_bytes"] != float64(22) {
		t.Fatalf("bytes = %v / %v", got["rx_bytes"], got["tx_bytes"])
	}
	raw, _ := got["last_handshake_utc"].(string)
	if raw == "" {
		t.Fatalf("last_handshake_utc = %v", got["last_handshake_utc"])
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		t.Fatalf("last_handshake_utc not RFC3339: %q", raw)
	}
	if !parsed.Equal(hsAt) {
		t.Fatalf("last_handshake_utc = %v, want %v", parsed, hsAt)
	}
}

func TestAPICreateDoesNotWriteDescriptionToConf(t *testing.T) {
	f := newFixture(t)
	rec := f.apiCSRF(http.MethodPost, "/api/clients", map[string]string{
		"name":        "conf-check",
		"description": "SECRET_DESCRIPTION_TOKEN",
	})
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("POST code = %d; body=%s", rec.Code, rec.Body.String())
	}
	raw, err := os.ReadFile(f.confPath)
	if err != nil {
		t.Fatalf("read conf: %v", err)
	}
	if strings.Contains(string(raw), "SECRET_DESCRIPTION_TOKEN") {
		t.Fatalf("awg0.conf must not contain description: %s", raw)
	}
}
