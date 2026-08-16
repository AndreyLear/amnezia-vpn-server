// M7.4 web integration tests: RequireAuth guards the whole panel UI,
// /login stays public, challenges never leak the session id, and the
// identity is reachable through the request context. Unit coverage of
// the middleware/cookie helpers lives in internal/auth/auth_test.go.
package web

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/auth"
)

// sessionCookie returns a request carrying the given sid.
func sessionRequest(t *testing.T, method, path, sid string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if sid != "" {
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	}
	return req
}

func TestAuthValidSessionServesDashboard(t *testing.T) {
	f := newFixture(t)
	sess, err := f.sessions.Create(f.username)
	if err != nil {
		t.Fatalf("session create: %v", err)
	}
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, sessionRequest(t, http.MethodGet, "/", sess.ID))
	if rec.Code != http.StatusOK {
		t.Fatalf("authenticated GET /: code = %d, want 200", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "" {
		t.Fatalf("authenticated GET /: unexpected Location %q", loc)
	}
}

// protectedRoutes is the M7.4 route inventory: every panel route must
// be closed without a session.
var protectedRoutes = []struct {
	method string
	path   func(f *fixture) string
}{
	{http.MethodGet, func(*fixture) string { return "/api/me" }},
	{http.MethodPost, func(*fixture) string { return "/clients/new" }},
	{http.MethodPost, func(*fixture) string { return "/logout" }},
	{http.MethodPost, func(f *fixture) string { c, _, _ := f.addClient("c1"); return fmt.Sprintf("/clients/%d/enable", c.ID) }},
	{http.MethodPost, func(f *fixture) string { c, _, _ := f.addClient("c2"); return fmt.Sprintf("/clients/%d/disable", c.ID) }},
	{http.MethodPost, func(f *fixture) string { c, _, _ := f.addClient("c3"); return fmt.Sprintf("/clients/%d/delete", c.ID) }},
	{http.MethodPost, func(f *fixture) string { c, _, _ := f.addClient("c4"); return fmt.Sprintf("/clients/%d/rename", c.ID) }},
	{http.MethodGet, func(f *fixture) string { c, _, _ := f.addClient("c6"); return fmt.Sprintf("/clients/%d/config", c.ID) }},
	{http.MethodGet, func(f *fixture) string { c, _, _ := f.addClient("c7"); return fmt.Sprintf("/clients/%d/qr", c.ID) }},
}

func TestAuthProtectsEveryRoute(t *testing.T) {
	for _, rt := range protectedRoutes {
		f := newFixture(t)
		path := rt.path(f)
		t.Run(rt.method+" "+path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			f.server.ServeHTTP(rec, sessionRequest(t, rt.method, path, ""))
			if rt.method == http.MethodGet && !strings.HasPrefix(path, "/api/") {
				if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
					t.Fatalf("no session: code = %d, Location = %q; want 303 /login", rec.Code, rec.Header().Get("Location"))
				}
			} else if rt.method == http.MethodGet && strings.HasPrefix(path, "/api/") {
				if rec.Code != http.StatusUnauthorized {
					t.Fatalf("no session GET %s: code = %d, want 401", path, rec.Code)
				}
			} else {
				if rec.Code != http.StatusUnauthorized {
					t.Fatalf("no session: code = %d, want 401", rec.Code)
				}
			}
			if strings.Contains(rec.Body.String(), "amnezia_session") {
				t.Fatal("challenge body must not mention the cookie")
			}
		})
	}
}

func TestAuthEveryRouteAvailableWithSession(t *testing.T) {
	f := newFixture(t)
	f.addClient("m-enable")
	f.addClient("m-disable")
	f.addClient("m-delete")
	f.addClient("m-rename")
	dl, _, _ := f.addClient("download")
	cases := []struct {
		method string
		path   string
		code   int
		post   func(path string) *httptest.ResponseRecorder
	}{
		{http.MethodGet, "/", http.StatusOK, nil},
		{http.MethodPost, "/clients/new", http.StatusSeeOther, func(p string) *httptest.ResponseRecorder { return f.post(p, url.Values{}) }},
		{http.MethodPost, "/clients/1/enable", http.StatusSeeOther, func(p string) *httptest.ResponseRecorder { return f.post(p, url.Values{}) }},
		{http.MethodPost, "/clients/2/disable", http.StatusSeeOther, func(p string) *httptest.ResponseRecorder { return f.post(p, url.Values{}) }},
		{http.MethodPost, "/clients/3/delete", http.StatusSeeOther, func(p string) *httptest.ResponseRecorder { return f.post(p, url.Values{}) }},
		{http.MethodPost, "/clients/4/rename", http.StatusSeeOther, func(p string) *httptest.ResponseRecorder { return f.post(p, url.Values{}) }},
		{http.MethodGet, fmt.Sprintf("/clients/%d/config", dl.ID), http.StatusOK, nil},
		{http.MethodGet, fmt.Sprintf("/clients/%d/qr", dl.ID), http.StatusOK, nil},
		// /logout is protected too; it must be the last case because it
		// deletes the fixture's admin session.
		{http.MethodPost, "/logout", http.StatusSeeOther, func(p string) *httptest.ResponseRecorder { return f.post(p, url.Values{}) }},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			var rec *httptest.ResponseRecorder
			if tc.post != nil {
				// M7.6: authenticated POSTs must carry the session's
				// real CSRF token; the empty-but-valid form still
				// reaches the handler and PRGs with a flash.
				rec = tc.post(tc.path)
			} else {
				rec = httptest.NewRecorder()
				f.serve(rec, httptest.NewRequest(tc.method, tc.path, nil))
			}
			if rec.Code != tc.code {
				t.Fatalf("code = %d, want %d", rec.Code, tc.code)
			}
		})
	}
}

func TestAuthLoginIsPublic(t *testing.T) {
	f := newFixture(t)
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, sessionRequest(t, http.MethodGet, "/login", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /login: code = %d, want 200 (M7.5 login form)", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "" {
		t.Fatalf("GET /login must not redirect: %q", loc)
	}
	if rec.Header().Get("Set-Cookie") != "" {
		t.Fatal("public /login must not set cookies")
	}
	assertSPA(t, rec)
	if strings.Contains(rec.Body.String(), "amnezia_session") {
		t.Fatal("public page must not mention the session cookie")
	}

	// Authenticated GET /login still serves the SPA; React redirects to /.
	sess, err := f.sessions.Create(f.username)
	if err != nil {
		t.Fatalf("session create: %v", err)
	}
	rec = httptest.NewRecorder()
	f.server.ServeHTTP(rec, sessionRequest(t, http.MethodGet, "/login", sess.ID))
	assertSPA(t, rec)
}

func TestAuthLoginPageSecretFree(t *testing.T) {
	f := newFixture(t)
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, sessionRequest(t, http.MethodGet, "/login", ""))
	body := rec.Body.String()
	// Values, not field names: the form legitimately contains the
	// words "password"/"username" as input attributes.
	for _, secret := range []string{
		"amnezia_session", samplePrivateKey, samplePresharedKey,
	} {
		if strings.Contains(body, secret) {
			t.Errorf("GET /login leaks %q", secret)
		}
	}
}

func TestAuthUnknownAndMalformedSIDRedirect(t *testing.T) {
	f := newFixture(t)
	bad := []string{
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaab", // well-shaped, unknown
		"garbage",
		strings.Repeat("x", 100),
	}
	for _, sid := range bad {
		t.Run(sid[:min(len(sid), 12)], func(t *testing.T) {
			rec := httptest.NewRecorder()
			f.server.ServeHTTP(rec, sessionRequest(t, http.MethodGet, "/api/me", sid))
			assertAPIUnauthorized(t, rec)
		})
	}
}

func TestAuthExpiredSIDRedirect(t *testing.T) {
	f := newFixtureWithStore(t, io.Discard, 30*time.Millisecond)
	sess, err := f.sessions.Create(f.username)
	if err != nil {
		t.Fatalf("session create: %v", err)
	}
	if got, ok := f.sessions.Get(sess.ID); !ok || got.ID != sess.ID {
		t.Fatal("fresh session must be live")
	}
	time.Sleep(60 * time.Millisecond)
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, sessionRequest(t, http.MethodGet, "/api/me", sess.ID))
	assertAPIUnauthorized(t, rec)
	if _, ok := f.sessions.Get(sess.ID); ok {
		t.Fatal("expired session must be removed from the store")
	}
}

func TestAuthDeletedSessionRedirect(t *testing.T) {
	f := newFixture(t)
	sess, err := f.sessions.Create(f.username)
	if err != nil {
		t.Fatalf("session create: %v", err)
	}
	f.sessions.Delete(sess.ID)
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, sessionRequest(t, http.MethodGet, "/api/me", sess.ID))
	assertAPIUnauthorized(t, rec)
}

// TestAuthSIDNeverLeaks proves the session id appears neither in the
// body nor in any header of protected-route responses (Set-Cookie is
// never written outside the login flow).
func TestAuthSIDNeverLeaks(t *testing.T) {
	f := newFixture(t)
	sid := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaab" // unknown, well-shaped
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, sessionRequest(t, http.MethodGet, "/api/me", sid))
	joined := rec.Body.String() + rec.Header().Get("Location") + rec.Header().Get("Set-Cookie")
	if strings.Contains(joined, sid) {
		t.Fatalf("SID leaked into an unauthorized response: %q", joined)
	}

	rec = httptest.NewRecorder()
	f.server.ServeHTTP(rec, sessionRequest(t, http.MethodPost, "/clients/new", sid))
	joined = rec.Body.String() + rec.Header().Get("Set-Cookie")
	if strings.Contains(joined, sid) {
		t.Fatalf("SID leaked into an unauthorized POST response: %q", joined)
	}

	// An authenticated redirect (PRG flash) must not carry the SID either.
	sess, err := f.sessions.Create(f.username)
	if err != nil {
		t.Fatalf("session create: %v", err)
	}
	c, _, _ := f.addClient("alice")
	rec = httptest.NewRecorder()
	f.server.ServeHTTP(rec, sessionRequest(t, http.MethodGet, fmt.Sprintf("/clients/%d/config", c.ID), sess.ID))
	joined = rec.Header().Get("Content-Disposition") + rec.Header().Get("Location")
	if strings.Contains(joined, sess.ID) {
		t.Fatalf("SID leaked into an authenticated response header: %q", joined)
	}
}

// TestAuthIdentityViaContext wires a test-only protected route that
// renders the identity from the context.
func TestAuthIdentityViaContext(t *testing.T) {
	f := newFixture(t)
	f.server.mux.Handle("GET /whoami", f.server.auth.RequireAuth(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			sess, ok := auth.CurrentUser(r.Context())
			if !ok {
				http.Error(w, "no identity", http.StatusInternalServerError)
				return
			}
			io.WriteString(w, sess.Username)
		})))
	sess, err := f.sessions.Create("root")
	if err != nil {
		t.Fatalf("session create: %v", err)
	}

	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, sessionRequest(t, http.MethodGet, "/whoami", sess.ID))
	if rec.Code != http.StatusOK || rec.Body.String() != "root" {
		t.Fatalf("authenticated /whoami: code = %d, body = %q; want 200 root", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	f.server.ServeHTTP(rec, sessionRequest(t, http.MethodGet, "/whoami", ""))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Fatalf("anonymous /whoami: code = %d, Location = %q; want 303 /login", rec.Code, rec.Header().Get("Location"))
	}
}

func TestAuthConcurrentRequests(t *testing.T) {
	f := newFixture(t)
	valid, err := f.sessions.Create(f.username)
	if err != nil {
		t.Fatalf("session create: %v", err)
	}
	const n = 48
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sid := valid.ID
			want := http.StatusOK
			if i%2 == 1 {
				sid = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaab"
				want = http.StatusUnauthorized
			}
			rec := httptest.NewRecorder()
			f.server.ServeHTTP(rec, sessionRequest(t, http.MethodGet, "/api/me", sid))
			if rec.Code != want {
				t.Errorf("request %d: code = %d, want %d", i, rec.Code, want)
			}
		}(i)
	}
	wg.Wait()
}

// TestAuthConfigAndQRClosedWithoutSession pins the two download-like
// endpoints explicitly (their own test files assert the authenticated
// behavior).
func TestAuthConfigAndQRClosedWithoutSession(t *testing.T) {
	f := newFixture(t)
	client, _, _ := f.addClient("alice")
	for _, p := range []string{
		fmt.Sprintf("/clients/%d/config", client.ID),
		fmt.Sprintf("/clients/%d/qr", client.ID),
	} {
		rec := httptest.NewRecorder()
		f.server.ServeHTTP(rec, sessionRequest(t, http.MethodGet, p, ""))
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
			t.Errorf("GET %s without session: code = %d, Location = %q; want 303 /login", p, rec.Code, rec.Header().Get("Location"))
		}
	}
}

// TestAuthSessionTypeSecretFree is the web-side companion of the
// session structural guarantee in internal/auth (the type may never
// grow secret fields: no password/hash/private/PSK material).
// CSRFToken is the M7.6 per-session CSRF secret — the only added
// field, covered by its own no-leak matrix.
func TestAuthSessionTypeSecretFree(t *testing.T) {
	typ := reflect.TypeOf(auth.Session{})
	var fields []string
	for i := 0; i < typ.NumField(); i++ {
		fields = append(fields, typ.Field(i).Name)
	}
	want := []string{"ID", "Username", "CSRFToken", "CreatedAt", "ExpiresAt"}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("Session fields = %v, want exactly %v (no secret material)", fields, want)
	}
}
