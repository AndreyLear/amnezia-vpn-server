// M7.7 full auth lifecycle (internal/web): one journey over the real
// HTTP stack — bootstrap user → anonymous login form → successful
// login (session + CSRF) → dashboard → full CRUD over HTTP → logout →
// closed panel → re-login. Every step uses the real request path; the
// only fixture shortcut is the store itself (in-memory, exactly as in
// production). Individual failure modes are covered by their own
// matrix tests; this test pins the happy-path integration between
// authentication, sessions, CSRF, the M6 CRUD layer and the database.
package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/auth"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

// postAuth posts a form to path carrying the given session id and CSRF
// token — the same pair a real browser would send — and serves it.
func postAuth(f *fixture, path, sid, csrf string, form url.Values) *httptest.ResponseRecorder {
	f.t.Helper()
	if form == nil {
		form = url.Values{}
	}
	if csrf != "" {
		form.Set(auth.CSRFFieldName, csrf)
	}
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, req)
	return rec
}

func TestAuthFullLifecycle(t *testing.T) {
	f := newFixture(t)
	addUser(t, f, "alice", testPassword)

	// 1. Bootstrap: the only public page before any session is the
	//    login form.
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, sessionRequest(t, http.MethodGet, "/login", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /login: code = %d, want 200", rec.Code)
	}
	if strings.Contains(rec.Body.String(), auth.CSRFFieldName) {
		t.Fatal("public login page must not carry a CSRF token")
	}

	// 2. Wrong password: generic failure, zero sessions, no cookie.
	rec = httptest.NewRecorder()
	f.server.ServeHTTP(rec, loginForm(t, "alice", "wrong-password"))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), loginErrorText) {
		t.Fatalf("wrong password: code = %d, want 200 + generic error", rec.Code)
	}
	if n := activeSessionCount(f, "alice"); n != 0 {
		t.Fatalf("failed login created %d sessions", n)
	}

	// 3. Login: 303 → /, session cookie issued, exactly one live
	//    session with a CSRF token.
	rec = httptest.NewRecorder()
	f.server.ServeHTTP(rec, loginForm(t, "alice", testPassword))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/" {
		t.Fatalf("login: code = %d, Location = %q; want 303 /", rec.Code, rec.Header().Get("Location"))
	}
	c := sessionCookie(t, rec)
	if c == nil {
		t.Fatal("login must Set-Cookie amnezia_session")
	}
	sess, ok := f.sessions.Get(c.Value)
	if !ok {
		t.Fatal("issued session must be live in the store")
	}
	if sess.CSRFToken == "" {
		t.Fatal("live session must carry a CSRF token")
	}
	if n := activeSessionCount(f, "alice"); n != 1 {
		t.Fatalf("sessions after login = %d, want 1", n)
	}

	// 4. Dashboard: 200, without exposing the signed-in principal in the
	//    chrome, and the CSRF token rendered only inside a hidden form input.
	rec = httptest.NewRecorder()
	f.server.ServeHTTP(rec, sessionRequest(t, http.MethodGet, "/", c.Value))
	if rec.Code != http.StatusOK {
		t.Fatalf("dashboard: code = %d, want 200", rec.Code)
	}
	dash := rec.Body.String()
	if strings.Contains(dash, "Вы вошли как alice") {
		t.Fatalf("dashboard must not expose the signed-in principal: %q", dash)
	}
	if !strings.Contains(dash, `<input type="hidden" name="_csrf" value="`+sess.CSRFToken+`">`) {
		t.Fatal("dashboard must embed the session CSRF token in a hidden input")
	}

	// 5. Full CRUD over HTTP (PRG 303 everywhere, real token).
	rec = postAuth(f, "/clients/new", c.Value, sess.CSRFToken, url.Values{"name": {"carol"}})
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "/?msg=") {
		t.Fatalf("add: code = %d, Location = %q; want 303 + flash", rec.Code, rec.Header().Get("Location"))
	}
	if !strings.Contains(rec.Header().Get("Location"), url.QueryEscape(flashAdded)) {
		t.Fatalf("add: Location = %q, want the %q flash", rec.Header().Get("Location"), flashAdded)
	}
	clients, err := db.ClientsAll(f.h)
	if err != nil {
		t.Fatalf("ClientsAll: %v", err)
	}
	if len(clients) != 1 || clients[0].Name != "carol" {
		t.Fatalf("DB after add: %+v", clients)
	}
	carolID := clients[0].ID

	mutations := []struct {
		path  string
		form  url.Values
		check func(t *testing.T, row *db.ClientRecord)
	}{
		{fmt.Sprintf("/clients/%d/rename", carolID), url.Values{"name": {"carol-renamed"}}, func(t *testing.T, row *db.ClientRecord) {
			if row.Name != "carol-renamed" {
				t.Fatalf("rename: name = %q, want carol-renamed", row.Name)
			}
		}},
		{fmt.Sprintf("/clients/%d/enable", carolID), nil, func(t *testing.T, row *db.ClientRecord) {
			if !row.Enabled {
				t.Fatal("enable: client must be enabled")
			}
		}},
		{fmt.Sprintf("/clients/%d/disable", carolID), nil, func(t *testing.T, row *db.ClientRecord) {
			if row.Enabled {
				t.Fatal("disable: client must be disabled")
			}
		}},
	}
	for _, m := range mutations {
		rec = postAuth(f, m.path, c.Value, sess.CSRFToken, m.form)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("%s: code = %d, want 303", m.path, rec.Code)
		}
		row, err := db.ClientByID(f.h, carolID)
		if err != nil {
			t.Fatalf("ClientByID: %v", err)
		}
		m.check(t, row)
	}

	// 6. Logout: 303 → /login, cookie cleared, store session gone.
	rec = postAuth(f, "/logout", c.Value, sess.CSRFToken, nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Fatalf("logout: code = %d, Location = %q; want 303 /login", rec.Code, rec.Header().Get("Location"))
	}
	if _, ok := f.sessions.Get(c.Value); ok {
		t.Fatal("logout must delete the store session")
	}

	// 7. Closed panel: the old SID and the old CSRF token authenticate
	//    nothing anymore.
	rec = httptest.NewRecorder()
	f.server.ServeHTTP(rec, sessionRequest(t, http.MethodGet, "/", c.Value))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Fatalf("GET / after logout: code = %d, want 303 /login", rec.Code)
	}
	rec = postAuth(f, "/clients/new", c.Value, sess.CSRFToken, url.Values{"name": {"intruder"}})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("POST after logout: code = %d, want 401", rec.Code)
	}
	if clients, _ := db.ClientsAll(f.h); len(clients) != 1 {
		t.Fatalf("post-logout POST mutated the DB: %d clients", len(clients))
	}

	// 8. Re-login: a fresh session works again.
	rec = httptest.NewRecorder()
	f.server.ServeHTTP(rec, loginForm(t, "alice", testPassword))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("re-login: code = %d, want 303", rec.Code)
	}
	reCookie := sessionCookie(t, rec)
	if reCookie == nil {
		t.Fatal("re-login: no session cookie")
	}
	rec = httptest.NewRecorder()
	f.server.ServeHTTP(rec, sessionRequest(t, http.MethodGet, "/", reCookie.Value))
	if rec.Code != http.StatusOK {
		t.Fatalf("dashboard after re-login: code = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "carol-renamed") {
		t.Fatal("dashboard after re-login must still show the renamed client")
	}
}
