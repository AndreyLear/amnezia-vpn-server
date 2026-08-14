// M6.1/M6.2 web tests (M6_AUDIT.md §9): routing, rendering, generic
// errors, HTML escaping, secret absence, body limit, security headers,
// PRG redirect, panic recovery (M6.1) and the dashboard/reconciliation
// HTTP layer with a real SQLite fixture and status files (M6.2).
package web

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/auth"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/keys"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/status"
)

const samplePrivateKey = "4OSe6B1rDXdY4RdVJ+eenaEJOqRVZ9kxx25z9bI0t28="
const samplePresharedKey = "gOYtz2OZILLXQm5hQMqF/e8fP02sqoy6FKLsqI0nwWo="

const webTestServerCIDR = "10.8.0.1/24"
const webTestEndpoint = "vpn.example.com:51820"

// fixture wires a real SQLite database, a status.json location and an
// http.Server under test. Every request through f.get/f.serve carries a
// valid admin session (M7.4: the whole panel is protected by
// RequireAuth); f.post additionally carries the session's real CSRF
// token (M7.6: every POST is CSRF-protected — the token comes from the
// actual server-side session, the middleware is never bypassed).
// Unauthorized-request tests build requests and call
// f.server.ServeHTTP directly or clear the cookie.
type fixture struct {
	t          *testing.T
	h          *sql.DB
	dbPath     string
	statusPath string
	confPath   string
	server     *Server
	sessions   *auth.SessionStore
	sid        string
	csrf       string
	username   string
}

func newFixture(t *testing.T) *fixture {
	return newFixtureWithLogger(t, io.Discard)
}

// newFixtureWithLogger builds the standard fixture with a configurable
// log sink (io.Discard by default; a buffer for log-leak assertions).
func newFixtureWithLogger(t *testing.T, logSink io.Writer) *fixture {
	return newFixtureWithStore(t, logSink, 24*time.Hour)
}

// newFixtureWithStore is the shared fixture builder; ttl controls the
// session store lifetime (short values for expiry tests).
func newFixtureWithStore(t *testing.T, logSink io.Writer, ttl time.Duration) *fixture {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "amnezia.sqlite")
	h, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { h.Close() })
	if err := db.Migrate(h); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	priv, pub, err := keys.GenerateKeyPair()
	if err != nil {
		t.Fatalf("server keys: %v", err)
	}
	if err := db.CreateServer(h, priv, pub, webTestServerCIDR, 51820, "", "{}", webTestEndpoint); err != nil {
		t.Fatalf("db.CreateServer: %v", err)
	}
	statusPath := filepath.Join(t.TempDir(), "status.json")
	confPath := filepath.Join(t.TempDir(), "awg0.conf")
	const testUsername = "admin"
	sessions := auth.NewSessionStore(ttl)
	sess, err := sessions.Create(testUsername)
	if err != nil {
		t.Fatalf("session create: %v", err)
	}
	s, err := New(Config{
		Addr:       "127.0.0.1:0",
		DB:         h,
		Sessions:   sessions,
		StatusPath: statusPath,
		ConfPath:   confPath,
		DBPath:     dbPath,
		Logger:     log.New(logSink, "", 0),
	})
	if err != nil {
		t.Fatalf("web.New: %v", err)
	}
	return &fixture{
		t: t, h: h, dbPath: dbPath, statusPath: statusPath,
		confPath: confPath, server: s, sessions: sessions,
		sid: sess.ID, csrf: sess.CSRFToken, username: testUsername,
	}
}

// files returns the set of every file under the fixture's three
// locations (db, status, config) — used to prove handlers never write
// to the filesystem.
func (f *fixture) files() map[string]struct{} {
	f.t.Helper()
	out := map[string]struct{}{}
	for _, dir := range []string{
		filepath.Dir(f.dbPath),
		filepath.Dir(f.statusPath),
		filepath.Dir(f.confPath),
	} {
		filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if !d.IsDir() {
				out[path] = struct{}{}
			}
			return nil
		})
	}
	return out
}

// addClient inserts a client and returns its record plus the private
// and preshared keys (the keys must never appear in HTTP responses).
func (f *fixture) addClient(name string) (*db.ClientRecord, string, string) {
	f.t.Helper()
	priv, pub, err := keys.GenerateKeyPair()
	if err != nil {
		f.t.Fatalf("client keys: %v", err)
	}
	psk, err := keys.GeneratePresharedKey()
	if err != nil {
		f.t.Fatalf("psk: %v", err)
	}
	rec, err := db.CreateClient(f.h, webTestServerCIDR, db.NewClient{Name: name, PrivateKey: priv, PublicKey: pub, PresharedKey: psk})
	if err != nil {
		f.t.Fatalf("db.CreateClient: %v", err)
	}
	return rec, priv, psk
}

// setStatus writes a status.json snapshot (or raw bytes when raw is
// non-empty).
func (f *fixture) setStatus(st *status.Status) {
	f.t.Helper()
	b, err := json.Marshal(st)
	if err != nil {
		f.t.Fatalf("marshal status: %v", err)
	}
	if err := os.WriteFile(f.statusPath, b, 0o600); err != nil {
		f.t.Fatalf("write status: %v", err)
	}
}

func (f *fixture) setRawStatus(raw string) {
	f.t.Helper()
	if err := os.WriteFile(f.statusPath, []byte(raw), 0o600); err != nil {
		f.t.Fatalf("write raw status: %v", err)
	}
}

func (f *fixture) get(path string) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	f.serve(rec, req)
	return rec
}

// serve runs an authenticated request against the fixture's server: it
// attaches the admin session cookie before dispatching. Session-free
// tests must build their own request and call f.server.ServeHTTP.
func (f *fixture) serve(rec *httptest.ResponseRecorder, req *http.Request) {
	f.t.Helper()
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: f.sid})
	f.server.ServeHTTP(rec, req)
}

func hs(age time.Duration) *time.Time {
	t := time.Now().UTC().Add(-age)
	return &t
}

func peerFor(pub string) status.Peer {
	return status.Peer{
		PublicKey:        pub,
		Endpoint:         "(none)",
		AllowedIPs:       []string{"10.8.0.2/32"},
		LastHandshakeUTC: hs(time.Minute),
		RxBytes:          5000,
		TxBytes:          9000,
	}
}

func upStatusWith(peers ...status.Peer) *status.Status {
	if peers == nil {
		peers = []status.Peer{}
	}
	return &status.Status{
		Schema:      status.SchemaVersion,
		GeneratedAt: time.Now().UTC().Truncate(time.Second),
		Interface: &status.Interface{
			Iface:        "awg0",
			HasInterface: true,
			PublicKey:    "qXvOE2SbilF5RNpHXPU8xQCTvUIxYPeTNI6R6p+bqnw=",
			ListenPort:   51820,
			FWMark:       "off",
			AWGParams:    status.AWGParams{Jc: 3, Jmin: 21, Jmax: 31, S1: 904, S2: 737},
		},
		Peers: peers,
	}
}

func TestNewRequiresDB(t *testing.T) {
	if _, err := New(Config{Addr: "127.0.0.1:0"}); err == nil {
		t.Error("New with nil DB: want error")
	}
	if _, err := New(Config{Addr: "nohost", DB: newFixture(t).h}); err == nil {
		t.Error("New with bad addr: want error")
	}
}

func TestDashboard200AndCardContent(t *testing.T) {
	f := newFixture(t)
	client, _, _ := f.addClient("alice")
	f.setStatus(upStatusWith(peerFor(client.PublicKey)))

	rec := f.get("/")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /: code %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"alice",
		"онлайн",
		"<article class=" + `"card"`, // template literal
		"10.8.0.2/32",
		"Туннель работает",
		// an enabled client's menu toggles to disable
		"/clients/" + fmt.Sprintf("%d", client.ID) + "/disable",
		"/clients/" + fmt.Sprintf("%d", client.ID) + "/qr",
		"action=\"/clients/new\"",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("GET / missing %q", want)
		}
	}
}

func TestDashboardPeerValuesShown(t *testing.T) {
	f := newFixture(t)
	c, _, _ := f.addClient("bob")
	f.setStatus(upStatusWith(peerFor(c.PublicKey)))
	body := f.get("/").Body.String()
	for _, want := range []string{"4.9 KiB", "8.8 KiB", "онлайн"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in body", want)
		}
	}
}

func TestDashboardClientWithoutPeerOffline(t *testing.T) {
	f := newFixture(t)
	f.addClient("no-peer")
	f.setStatus(upStatusWith())
	body := f.get("/").Body.String()
	if !strings.Contains(body, "офлайн") {
		t.Errorf("client without peer must render offline: %s", body)
	}
}

func TestDashboardOrderAndPeerHidden(t *testing.T) {
	f := newFixture(t)
	f.addClient("aaa")
	b, _, _ := f.addClient("bbb")
	f.addClient("ccc")
	f.setStatus(upStatusWith(peerFor(b.PublicKey)))
	body := f.get("/").Body.String()
	if strings.Index(body, "aaa") > strings.Index(body, "ccc") {
		t.Errorf("cards not in ClientsAll order")
	}
}

func TestDashboardExpiredShownAndDBIntact(t *testing.T) {
	f := newFixture(t)
	c, _, _ := f.addClient("old")
	past := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
	if err := db.SetClientExpiry(f.h, c.ID, past); err != nil {
		t.Fatalf("set expiry: %v", err)
	}
	before, err := db.ClientsAll(f.h)
	if err != nil {
		t.Fatalf("ClientsAll: %v", err)
	}
	f.setStatus(upStatusWith())
	body := f.get("/").Body.String()
	if !strings.Contains(body, "срок истёк") {
		t.Errorf("expired client must be marked: %s", body)
	}
	after, err := db.ClientsAll(f.h)
	if err != nil {
		t.Fatalf("ClientsAll after: %v", err)
	}
	if len(before) != 1 || len(after) != 1 || after[0].ID != c.ID {
		t.Fatalf("DB mutated by dashboard: before=%v after=%v", before, after)
	}
}

func TestDashboardStatusNA(t *testing.T) {
	f := newFixture(t)
	f.addClient("x")
	// no status file written
	body := f.get("/").Body.String()
	if !strings.Contains(body, "Туннель: статус недоступен") {
		t.Errorf("NA banner missing: %s", body)
	}
	if !strings.Contains(body, "офлайн") {
		t.Errorf("NA: clients must render offline")
	}
}

func TestDashboardStatusDown(t *testing.T) {
	f := newFixture(t)
	f.addClient("x")
	f.setStatus(&status.Status{Schema: status.SchemaVersion, GeneratedAt: time.Now().UTC(), Interface: nil, Peers: []status.Peer{}})
	body := f.get("/").Body.String()
	if !strings.Contains(body, "Туннель: интерфейс выключен") {
		t.Errorf("down banner missing: %s", body)
	}
}

func TestDashboardStatusErrorGeneric(t *testing.T) {
	f := newFixture(t)
	f.addClient("x")
	// malformed status containing secret-looking material: the parse
	// fails and nothing from the file may leak into the response.
	f.setRawStatus(`{garbage` + `"private_key":"` + samplePrivateKey + `"` + `}`)
	body := f.get("/").Body.String()
	if !strings.Contains(body, "Туннель: ошибка статуса") {
		t.Errorf("error banner missing: %s", body)
	}
	if strings.Contains(body, samplePrivateKey) {
		t.Errorf("malformed status content leaked: %s", body)
	}
}

func TestDashboardEscaping(t *testing.T) {
	f := newFixture(t)
	c, _, _ := f.addClient("<script>alert(\"x\")</script>")
	f.setStatus(upStatusWith(peerFor(c.PublicKey)))
	body := f.get("/").Body.String()
	if strings.Contains(body, "<script>alert") {
		t.Fatalf("client name not escaped: %s", body)
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Errorf("escaped form missing: %s", body)
	}
}

func TestDashboardNoSecrets(t *testing.T) {
	f := newFixture(t)
	serverRow, err := db.ServerRow(f.h)
	if err != nil {
		t.Fatalf("ServerRow: %v", err)
	}
	c, priv, psk := f.addClient("sec")
	f.setStatus(upStatusWith(peerFor(c.PublicKey)))
	body := f.get("/").Body.String()
	for _, secret := range []string{serverRow.PrivateKey, priv, psk, samplePrivateKey, samplePresharedKey} {
		if secret != "" && strings.Contains(body, secret) {
			t.Errorf("secret %q leaked into response", secret)
		}
	}
	for _, field := range []string{"private_key", "preshared_key"} {
		if strings.Contains(body, field) {
			t.Errorf("secret field name %q in response", field)
		}
	}
}

func TestDashboardEmptyClients(t *testing.T) {
	f := newFixture(t)
	f.setStatus(upStatusWith())
	body := f.get("/").Body.String()
	if !strings.Contains(body, "Клиентов пока нет.") {
		t.Errorf("empty state missing: %s", body)
	}
}

// TestDashboardTrafficTotals (T-120 round 2 §9): the statsline sums
// rx/tx over all clients — no history, just the current totals.
func TestDashboardTrafficTotals(t *testing.T) {
	f := newFixture(t)
	a, _, _ := f.addClient("alpha")
	b, _, _ := f.addClient("beta")
	pa := peerFor(a.PublicKey)
	pa.RxBytes = 5000
	pa.TxBytes = 9000
	pb := peerFor(b.PublicKey)
	pb.RxBytes = 5000
	pb.TxBytes = 9000
	f.setStatus(upStatusWith(pa, pb))
	body := f.get("/").Body.String()
	for _, want := range []string{"Вх:", "Исх:", "9.8 KiB"} {
		if !strings.Contains(body, want) {
			t.Errorf("traffic totals missing %q", want)
		}
	}
}

// ---- M6.1 regression: foundation behavior on the same server ----

func TestUnknownRoute404(t *testing.T) {
	f := newFixture(t)
	for _, target := range []string{"/nope", "/status.json"} {
		rec := f.get(target)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s: code = %d, want 404", target, rec.Code)
		}
		if strings.Contains(rec.Body.String(), target) {
			t.Errorf("GET %s: 404 body echoes the path", target)
		}
	}
}

func TestErrorPageGeneric(t *testing.T) {
	f := newFixture(t)
	cases := []struct {
		code int
		want string
	}{
		{http.StatusNotFound, "Страница не найдена."},
		{http.StatusRequestEntityTooLarge, "Тело запроса слишком большое."},
		{http.StatusMethodNotAllowed, "Метод не поддерживается."},
		{http.StatusBadRequest, "Некорректный запрос."},
		{999, "Внутренняя ошибка сервера."},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		f.server.errorPage(rec, c.code)
		body := rec.Body.String()
		if !strings.Contains(body, c.want) {
			t.Errorf("errorPage(%d): body missing %q", c.code, c.want)
		}
		if strings.Contains(body, samplePrivateKey) {
			t.Errorf("errorPage(%d): key material leaked", c.code)
		}
	}
}

func TestPanicRecoveryGeneric500(t *testing.T) {
	f := newFixture(t)
	f.server.Handle("GET", "/panic", func(w http.ResponseWriter, r *http.Request) {
		panic("boom: " + samplePrivateKey)
	})
	req := httptest.NewRequest(http.MethodGet, "/panic", nil)
	rec := httptest.NewRecorder()
	f.serve(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("panic route: code = %d, want 500", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Внутренняя ошибка сервера.") || strings.Contains(body, "boom") {
		t.Errorf("panic details leaked: %q", body)
	}
}

func TestHTMLEscapingFlash(t *testing.T) {
	f := newFixture(t)
	rec := f.get("/?msg=%3Cscript%3Ealert(1)%3C/script%3E")
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Fatalf("flash not escaped: %q", body)
	}
}

func TestSecurityHeaders(t *testing.T) {
	f := newFixture(t)
	rec := f.get("/")
	expected := map[string]string{
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "DENY",
		"Referrer-Policy":         "no-referrer",
		"Content-Security-Policy": "default-src 'self'; frame-ancestors 'none'",
		"Cache-Control":           "no-store",
	}
	for k, want := range expected {
		if got := rec.Header().Get(k); got != want {
			t.Errorf("header %s = %q, want %q", k, got, want)
		}
	}
}

func TestBodyLimitRejectsOversizedPOST(t *testing.T) {
	f := newFixture(t)
	big := strings.Repeat("x", MaxBodyBytes+1)
	body := "name=small&payload=" + big
	req := httptest.NewRequest(http.MethodPost, "/clients/new", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	f.serve(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized POST: code = %d, want 413", rec.Code)
	}
}

func TestBodyLimitSmallPOSTRoutesNormally(t *testing.T) {
	f := newFixture(t)
	req := httptest.NewRequest(http.MethodPost, "/nope", strings.NewReader("name=x"))
	rec := httptest.NewRecorder()
	f.serve(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("small POST to unwired route: code = %d, want 404", rec.Code)
	}
}

func TestRedirect303(t *testing.T) {
	f := newFixture(t)
	f.server.Handle("POST", "/prg", func(w http.ResponseWriter, r *http.Request) {
		redirect303(w, r, "/?msg=ok")
	})
	req := httptest.NewRequest(http.MethodPost, "/prg", nil)
	rec := httptest.NewRecorder()
	f.serve(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("PRG redirect: code = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/?msg=ok" {
		t.Errorf("Location = %q, want /?msg=ok", loc)
	}
}

// TestStaticCSSAndTemplatePurity (T-120): the embedded stylesheet and
// the progressive-enhancement script are served publicly at /static/*
// (the login page needs them before any session exists), and no
// rendered page carries inline styles, inline scripts or inline event
// handlers — the CSP "default-src 'self'" forbids them. The ONLY
// script allowed is the external /static/app.js (round 2 §12); every
// page embeds it exactly once.
func TestStaticCSSAndTemplatePurity(t *testing.T) {
	f := newFixture(t)

	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, sessionRequest(t, http.MethodGet, "/static/style.css", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /static/style.css: code = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Fatalf("css content-type = %q, want text/css", ct)
	}
	if strings.TrimSpace(rec.Body.String()) == "" {
		t.Fatal("css body is empty")
	}

	rec = httptest.NewRecorder()
	f.server.ServeHTTP(rec, sessionRequest(t, http.MethodGet, "/static/app.js", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /static/app.js: code = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Fatalf("app.js content-type = %q, want javascript", ct)
	}
	if strings.TrimSpace(rec.Body.String()) == "" {
		t.Fatal("app.js body is empty")
	}

	for _, path := range []string{"/login", "/", "/backups", "/backups/restore"} {
		rec := httptest.NewRecorder()
		var req *http.Request
		if path == "/login" {
			req = sessionRequest(t, http.MethodGet, path, "")
		} else {
			req = sessionRequest(t, http.MethodGet, path, f.sid)
		}
		f.server.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: code = %d, want 200", path, rec.Code)
		}
		body := rec.Body.String()
		if n := strings.Count(body, `<script src="/static/app.js" defer></script>`); n != 1 {
			t.Fatalf("GET %s: app.js script tag count = %d, want exactly 1", path, n)
		}
		for _, needle := range []string{
			"<script>", "style=", "onclick=", "onerror=", "onload=",
			"onchange=", "oninput=", "onsubmit=", "onmouseover=", "onfocus=",
		} {
			if strings.Contains(body, needle) {
				t.Fatalf("GET %s: page contains %q — CSP-incompatible", path, needle)
			}
		}
	}
}
