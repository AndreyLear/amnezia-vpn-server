// M7.6 CSRF integration matrix (internal/web): every authenticated
// POST route is protected by auth.RequireCSRF — valid token behaves
// normally, missing/wrong/foreign tokens answer a generic 403 without
// touching the session, the token is accepted only from the form body
// (never query/cookie/header), expired sessions fall through to the
// auth-layer challenge, and the token never leaks outside hidden form
// fields. The fixture's POSTs carry the real token from the server-side
// session (web_test.go fixture): the middleware is never bypassed.
package web

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/auth"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

// protectedPosts is the M7.6 route inventory: every authenticated POST
// that must demand a CSRF token. Each entry creates its own clients so
// the matrix is independent of test order. The restore route is the
// one multipart route: its POSTer builds a multipart body (the M7.6
// matrix otherwise posts url-encoded forms, which that route cannot
// parse by design — M8.6).
func protectedPosts(f *fixture) []struct {
	name string
	path func() string
	post func(f *fixture, path, token string) *httptest.ResponseRecorder
} {
	mk := func(prefix string) func() string {
		return func() string {
			c, _, _ := f.addClient(prefix)
			return fmt.Sprintf("/clients/%d/%s", c.ID, prefix)
		}
	}
	entries := []struct {
		name string
		path func() string
		post func(f *fixture, path, token string) *httptest.ResponseRecorder
	}{
		{"new", func() string { return "/clients/new" }, nil},
		{"enable", mk("enable"), nil},
		{"disable", mk("disable"), nil},
		{"delete", mk("delete"), nil},
		{"rename", mk("rename"), nil},
		{"expiry", mk("expiry"), nil},
		{"backup create", func() string { return "/backups/create" }, nil},
		{"backup delete", func() string {
			dir, _ := setBackupsPath(f.t)
			name := makeBackup(f.t, f, dir, time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC))
			return "/backups/" + name + "/delete"
		}, nil},
		{"backup restore", func() string { return "/backups/restore" }, restoreCSRFPost},
		// logout invalidates the session; it must stay the last entry
		// so later subtests reuse a live fixture session.
		{"logout", func() string { return "/logout" }, nil},
	}
	return entries
}

// restoreCSRFPost posts a multipart body carrying only the given CSRF
// token to path (an empty body when token == ""): the restore handler
// answers a fixed 403 for bad tokens and PRG (missing identity flash)
// for the valid one — never a 200, never a mutation.
func restoreCSRFPost(f *fixture, path, token string) *httptest.ResponseRecorder {
	f.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if token != "" {
		if err := mw.WriteField(auth.CSRFFieldName, token); err != nil {
			f.t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		f.t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	f.serve(rec, req)
	return rec
}

// csrfPOST posts to path with the given token (or none when token ==
// "") and returns the recorder. The request carries the fixture's
// admin session.
func csrfPOST(f *fixture, path, token string) *httptest.ResponseRecorder {
	f.t.Helper()
	form := url.Values{}
	if token != "" {
		form.Set(auth.CSRFFieldName, token)
	}
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	f.serve(rec, req)
	return rec
}

// TestCSRFValidTokenNormalBehaviour: for every protected POST, the
// session's real token passes through to the handler (303 PRG or a
// handler answer — never 403).
func TestCSRFValidTokenNormalBehaviour(t *testing.T) {
	f := newFixture(t)
	for _, rt := range protectedPosts(f) {
		path := rt.path()
		post := rt.post
		if post == nil {
			post = csrfPOST
		}
		t.Run(rt.name, func(t *testing.T) {
			rec := post(f, path, f.csrf)
			if rec.Code != http.StatusSeeOther {
				t.Fatalf("valid token %s: code = %d, want 303 (PRG)", path, rec.Code)
			}
			if loc := rec.Header().Get("Location"); loc == "" {
				t.Fatal("valid POST must answer a PRG Location")
			}
		})
	}
}

// TestCSRFMissingTokenForbidden: every protected POST without the token
// answers a generic 403 — no PRG, no mutation, session untouched.
func TestCSRFMissingTokenForbidden(t *testing.T) {
	f := newFixture(t)
	for _, rt := range protectedPosts(f) {
		path := rt.path()
		post := rt.post
		if post == nil {
			post = csrfPOST
		}
		t.Run(rt.name, func(t *testing.T) {
			rec := post(f, path, "")
			if rec.Code != http.StatusForbidden {
				t.Fatalf("missing token %s: code = %d, want 403", path, rec.Code)
			}
			if loc := rec.Header().Get("Location"); loc != "" {
				t.Errorf("CSRF failure must not PRG: Location %q", loc)
			}
			body := rec.Body.String()
			if strings.Contains(body, "amnezia") || strings.Contains(body, f.csrf) || strings.Contains(body, "csrf") {
				t.Errorf("403 must be generic and secret-free: %q", body)
			}
			if _, ok := f.sessions.Get(f.sid); !ok {
				t.Fatal("CSRF failure must not delete the session")
			}
		})
	}
	// The rejected POST must not have mutated anything: only the
	// per-route fixture clients exist (enable/disable/delete/rename/
	// expiry = 5), and none of the rejected adds happened.
	clients, err := db.ClientsAll(f.h)
	if err != nil {
		t.Fatalf("ClientsAll: %v", err)
	}
	if len(clients) != 5 {
		t.Fatalf("rejected POSTs mutated the db: %d clients", len(clients))
	}
}

// TestCSRFWrongTokenForbidden: a wrong token is identical to a missing
// one — fixed generic 403, no details.
func TestCSRFWrongTokenForbidden(t *testing.T) {
	f := newFixture(t)
	for _, rt := range protectedPosts(f) {
		path := rt.path()
		post := rt.post
		if post == nil {
			post = csrfPOST
		}
		t.Run(rt.name, func(t *testing.T) {
			rec := post(f, path, "wrong-token-value")
			if rec.Code != http.StatusForbidden {
				t.Fatalf("wrong token %s: code = %d, want 403", path, rec.Code)
			}
			if strings.Contains(rec.Body.String(), "wrong-token-value") {
				t.Error("403 must not echo the submitted token")
			}
		})
	}
}

// TestCSRFTokenOfAnotherUser: a token from a different session of the
// same panel must be rejected (it cannot be replayed cross-session).
func TestCSRFTokenOfAnotherUser(t *testing.T) {
	f := newFixture(t)
	other, err := f.sessions.Create("mallory")
	if err != nil {
		t.Fatalf("second session: %v", err)
	}
	for _, rt := range protectedPosts(f) {
		path := rt.path()
		post := rt.post
		if post == nil {
			post = csrfPOST
		}
		t.Run(rt.name, func(t *testing.T) {
			rec := post(f, path, other.CSRFToken)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("foreign token %s: code = %d, want 403", path, rec.Code)
			}
		})
	}
}

// TestCSRFExpiredSessionAuthLayer: an expired session is rejected by
// RequireAuth before CSRF ever runs — POST → generic 401, never 403,
// never 200.
func TestCSRFExpiredSessionAuthLayer(t *testing.T) {
	f := newFixtureWithStore(t, io.Discard, 30*time.Millisecond)
	sess, err := f.sessions.Create(f.username)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	time.Sleep(60 * time.Millisecond)
	form := url.Values{auth.CSRFFieldName: {sess.CSRFToken}}
	req := httptest.NewRequest(http.MethodPost, "/clients/new", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sess.ID})
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expired session POST: code = %d, want 401 (auth layer)", rec.Code)
	}
}

// TestCSRFTokenInQueryStringForbidden: the token is accepted only from
// the form body; the same value in the query string is rejected.
func TestCSRFTokenInQueryStringForbidden(t *testing.T) {
	f := newFixture(t)
	req := httptest.NewRequest(http.MethodPost, "/clients/new?"+auth.CSRFFieldName+"="+f.csrf, nil)
	rec := httptest.NewRecorder()
	f.serve(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("token in query: code = %d, want 403", rec.Code)
	}
}

// TestCSRFTokenInCookieForbidden: a token carried in a cookie (even
// under the same field name) is rejected.
func TestCSRFTokenInCookieForbidden(t *testing.T) {
	f := newFixture(t)
	req := httptest.NewRequest(http.MethodPost, "/clients/new", strings.NewReader(""))
	req.AddCookie(&http.Cookie{Name: auth.CSRFFieldName, Value: f.csrf})
	rec := httptest.NewRecorder()
	f.serve(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("token in cookie: code = %d, want 403", rec.Code)
	}
}

// TestCSRFTokenInHeaderForbidden: a token in a custom header is
// rejected (the panel is a non-JS HTML UI; no header channel exists).
func TestCSRFTokenInHeaderForbidden(t *testing.T) {
	f := newFixture(t)
	req := httptest.NewRequest(http.MethodPost, "/clients/new", strings.NewReader(""))
	req.Header.Set("X-CSRF-Token", f.csrf)
	rec := httptest.NewRecorder()
	f.serve(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("token in header: code = %d, want 403", rec.Code)
	}
}

// TestCSRFTokenInRedirectURL: a successful PRG must never carry the
// token in Location, and a flash message must not echo it.
func TestCSRFTokenInRedirectURL(t *testing.T) {
	f := newFixture(t)
	rec := f.post("/clients/new", url.Values{"name": {"csrf-e2e"}})
	loc := rec.Header().Get("Location")
	if loc == "" {
		t.Fatal("PRG must answer a Location")
	}
	if strings.Contains(loc, f.csrf) {
		t.Fatalf("PRG Location leaks the CSRF token: %q", loc)
	}
	// Follow the redirect: the dashboard renders the token only inside
	// hidden inputs.
	dash := f.get("/").Body.String()
	if n := strings.Count(dash, f.csrf); n < 3 {
		t.Fatalf("dashboard must embed the token in every form, got %d occurrences", n)
	}
}

// TestCSRFNotInLogs: driving CSRF failures with a captured log sink —
// the token and the submitted value must never reach the logs.
func TestCSRFNotInLogs(t *testing.T) {
	logBuf := &strings.Builder{}
	f := newFixtureWithLogger(t, logBuf)
	csrfPOST(f, "/clients/new", "attacker-controlled-value")
	csrfPOST(f, "/clients/new", f.csrf)
	f.post("/clients/new", url.Values{"name": {"log-client"}})
	logs := logBuf.String()
	if strings.Contains(logs, f.csrf) {
		t.Fatal("CSRF token leaked into logs")
	}
	if strings.Contains(logs, "attacker-controlled-value") {
		t.Fatal("submitted CSRF value leaked into logs")
	}
}

// TestCSRFNotInGenericError: the 403 body is the fixed string
// "Forbidden.\n" — no token, no session id, no field name.
func TestCSRFNotInGenericError(t *testing.T) {
	f := newFixture(t)
	rec := csrfPOST(f, "/clients/new", "nope")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403", rec.Code)
	}
	if rec.Body.String() != "Forbidden.\n" {
		t.Fatalf("403 body = %q, want the fixed generic text", rec.Body.String())
	}
}

// TestCSRFTokenNotInCookie: the CSRF token never travels as a cookie —
// no Set-Cookie header appears in any protected response.
func TestCSRFTokenNotInCookie(t *testing.T) {
	f := newFixture(t)
	rec := csrfPOST(f, "/clients/new", f.csrf)
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.CSRFFieldName || c.Value == f.csrf {
			t.Fatalf("protected response set a CSRF-token cookie: %+v", c)
		}
	}
	// Successful PRG sets no cookie at all (the session cookie is
	// managed by login/logout only).
	if sc := rec.Header().Get("Set-Cookie"); sc != "" {
		t.Errorf("protected POST unexpectedly set cookies: %q", sc)
	}
}

// TestCSRFDashboardHiddenFields: every mutation form on the dashboard
// carries the token in a hidden input — the forms work without any
// client-side JavaScript.
func TestCSRFDashboardHiddenFields(t *testing.T) {
	f := newFixture(t)
	c, _, _ := f.addClient("alice")
	body := f.get("/").Body.String()
	for _, action := range []string{
		`action="/logout"`,
		`action="/clients/new"`,
		`action="/clients/` + fmt.Sprint(c.ID) + `/delete"`,
		`action="/clients/` + fmt.Sprint(c.ID) + `/rename"`,
		`action="/clients/` + fmt.Sprint(c.ID) + `/expiry"`,
	} {
		if !strings.Contains(body, action) {
			t.Errorf("dashboard misses form %s", action)
		}
	}
	// T-120 round 2 §7: the enable/disable pair collapsed into a single
	// toggle form — an enabled client renders the disable action.
	if !strings.Contains(body, `action="/clients/`+fmt.Sprint(c.ID)+`/disable"`) {
		t.Errorf("dashboard misses the toggle form (enabled client → disable action)")
	}
	if strings.Contains(body, `action="/clients/`+fmt.Sprint(c.ID)+`/enable"`) {
		t.Errorf("enabled client must not render an enable action")
	}
	// Every form on the page contains exactly one hidden _csrf input
	// carrying the session token.
	forms := strings.Count(body, "<form ")
	inputs := strings.Count(body, `<input type="hidden" name="_csrf" value="`+f.csrf+`">`)
	if forms == 0 || inputs != forms {
		t.Fatalf("hidden inputs = %d, forms = %d; every form must carry exactly one token", inputs, forms)
	}
}

// TestCSRFRotationOnLogin: a fresh login (new SID) must carry a fresh
// CSRF token; the token of the previous session must be rejected.
func TestCSRFRotationOnLogin(t *testing.T) {
	f := newFixture(t)
	addUser(t, f, "alice", testPassword)

	// First login → session A.
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, loginForm(t, "alice", testPassword))
	c1 := sessionCookie(t, rec)
	if c1 == nil {
		t.Fatal("login: no cookie")
	}
	sessA, ok := f.sessions.Get(c1.Value)
	if !ok {
		t.Fatal("session A missing")
	}

	// Second login (rotates session A) → session B.
	req := loginForm(t, "alice", testPassword)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: c1.Value})
	rec = httptest.NewRecorder()
	f.server.ServeHTTP(rec, req)
	c2 := sessionCookie(t, rec)
	if c2 == nil {
		t.Fatal("login: no cookie")
	}
	sessB, ok := f.sessions.Get(c2.Value)
	if !ok {
		t.Fatal("session B missing")
	}
	if c1.Value == c2.Value {
		t.Fatal("login must rotate the SID")
	}
	if sessA.CSRFToken == sessB.CSRFToken {
		t.Fatal("login must rotate the CSRF token together with the SID")
	}

	// Session A's token must not authenticate session B.
	form := url.Values{auth.CSRFFieldName: {sessA.CSRFToken}}
	req2 := httptest.NewRequest(http.MethodPost, "/clients/new", strings.NewReader(form.Encode()))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req2.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: c2.Value})
	rec = httptest.NewRecorder()
	f.server.ServeHTTP(rec, req2)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("stale CSRF token after rotate: code = %d, want 403", rec.Code)
	}
}

// TestCSRFNoRotationOnGET: plain GET requests must not rotate the CSRF
// token (or the SID): the dashboard token stays valid across loads.
func TestCSRFNoRotationOnGET(t *testing.T) {
	f := newFixture(t)
	tokenBefore, _ := f.sessions.Get(f.sid)
	for i := 0; i < 5; i++ {
		if rec := f.get("/"); rec.Code != http.StatusOK {
			t.Fatalf("GET / %d: code = %d", i, rec.Code)
		}
	}
	tokenAfter, ok := f.sessions.Get(f.sid)
	if !ok {
		t.Fatal("session lost")
	}
	if tokenBefore.CSRFToken != tokenAfter.CSRFToken || tokenBefore.ID != tokenAfter.ID {
		t.Fatal("GET requests must not rotate the SID or the CSRF token")
	}
}
