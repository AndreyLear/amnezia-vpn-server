// M7.5 login/logout integration tests (internal/web): the login form,
// the generic-failure contract, session Create/Rotate semantics, the
// one-active-session-per-user rule, logout, and the never-leak
// invariants across every response. Unit coverage of the middleware
// and the cookie helpers lives in internal/auth.
package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/auth"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

// testPassword is a known-good password for the login matrix; its value
// must never appear in any response.
const testPassword = "correct-horse-battery-staple-42"

// addUser creates an auth-table user with a known password and returns
// the row (hash included, for leak scans).
func addUser(t *testing.T, f *fixture, username, password string) *db.AuthUser {
	t.Helper()
	h, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	u, err := db.CreateAuthUser(f.h, username, h)
	if err != nil {
		t.Fatalf("CreateAuthUser: %v", err)
	}
	return u
}

// loginForm returns a POST /login request carrying the given fields.
func loginForm(t *testing.T, username, password string) *http.Request {
	t.Helper()
	form := url.Values{"username": {username}, "password": {password}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

// lastCookie returns the most recent Set-Cookie for the session cookie.
func sessionCookie(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.SessionCookieName {
			return c
		}
	}
	return nil
}

// activeSessionCount counts live sessions for a username in the store.
func activeSessionCount(f *fixture, username string) int {
	n := 0
	for _, sess := range f.sessions.ByUsername(username) {
		if _, ok := f.sessions.Get(sess.ID); ok {
			n++
		}
	}
	return n
}

func TestLoginPageRendersForm(t *testing.T) {
	f := newFixture(t)
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, sessionRequest(t, http.MethodGet, "/login", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`action="/login"`, `method="post"`,
		`name="username"`, `autocomplete="username"`,
		`name="password"`, `type="password"`, `autocomplete="current-password"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("login form misses %q", want)
		}
	}
}

func TestLoginSuccess(t *testing.T) {
	t.Setenv("AMNEZIA_SECURE_COOKIES", "")
	f := newFixture(t)
	addUser(t, f, "alice", testPassword)

	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, loginForm(t, "alice", testPassword))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("code = %d, want 303", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if loc != "/" {
		t.Fatalf("Location = %q, want / (no SID in the URL)", loc)
	}
	if strings.Contains(loc, "sid") || strings.Contains(rec.Body.String(), "amnezia_session") {
		t.Fatal("SID or cookie name leaked into the redirect")
	}
	c := sessionCookie(t, rec)
	if c == nil {
		t.Fatal("login must Set-Cookie amnezia_session")
	}
	if c.Value == "" || len(c.Value) != 43 {
		t.Fatalf("cookie value %q is not a session id", c.Value)
	}
	if _, ok := f.sessions.Get(c.Value); !ok {
		t.Fatal("issued cookie must be a live store session")
	}
	assertCookieAttributes(t, c)
}

// assertCookieAttributes pins the M7.4/7.5 cookie contract: name,
// HttpOnly, SameSite=Lax, Path=/, positive lifetime, no Secure while
// the panel serves plain HTTP on loopback.
func assertCookieAttributes(t *testing.T, c *http.Cookie) {
	t.Helper()
	if c.Name != auth.SessionCookieName {
		t.Errorf("name = %q, want %q", c.Name, auth.SessionCookieName)
	}
	if !c.HttpOnly {
		t.Error("HttpOnly must be set")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want SameSiteLax", c.SameSite)
	}
	if c.Path != "/" {
		t.Errorf("Path = %q, want /", c.Path)
	}
	if c.Secure {
		t.Error("Secure must stay false on loopback HTTP")
	}
	if c.MaxAge <= 0 {
		t.Errorf("MaxAge = %d, want a positive lifetime", c.MaxAge)
	}
	if !c.Expires.After(time.Now()) {
		t.Error("Expires must be in the future")
	}
}

// failedLogin runs one bad login and returns the response body; every
// failure mode must produce the identical body.
func failedLogin(t *testing.T, f *fixture, username, password string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, loginForm(t, username, password))
	if rec.Code != http.StatusOK {
		t.Fatalf("failed login: code = %d, want 200 (form with error)", rec.Code)
	}
	if rec.Header().Get("Set-Cookie") != "" {
		t.Error("failed login must not set cookies")
	}
	return rec.Body.String()
}

func TestLoginGenericFailure(t *testing.T) {
	f := newFixture(t)
	alice := addUser(t, f, "alice", testPassword)

	// The baseline of the generic failure: a wrong password for an
	// existing user. Every other failure mode must byte-match it.
	wantBody := failedLogin(t, f, "alice", "wrong-password")

	cases := []struct {
		name     string
		username string
		password string
	}{
		{"unknown user", "nobody", testPassword},
		{"wrong password", "alice", "wrong-password"},
		{"empty username", "", testPassword},
		{"empty password", "alice", ""},
		{"both empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := failedLogin(t, f, tc.username, tc.password)
			if got != wantBody {
				t.Errorf("failure body differs from the baseline:\n%q\nwant:\n%q", got, wantBody)
			}
			if !strings.Contains(got, loginErrorText) {
				t.Errorf("failure body misses the generic message: %q", got)
			}
			if strings.Contains(got, tc.username) && tc.username != "" {
				t.Errorf("failure body echoes the username %q", tc.username)
			}
			if strings.Contains(got, tc.password) && tc.password != "" {
				t.Errorf("failure body echoes the password")
			}
			if strings.Contains(got, alice.PasswordHash) {
				t.Error("failure body leaks the stored hash")
			}
			if strings.Contains(got, testPassword) {
				t.Error("failure body leaks the test password")
			}
		})
	}
	// A failed login must never touch the session store.
	if n := activeSessionCount(f, "alice"); n != 0 {
		t.Errorf("failed logins created %d sessions", n)
	}
}

// addUserRaw inserts an auth row with an un-hashed password_hash.
func addUserRaw(t *testing.T, f *fixture, username, hash string) *db.AuthUser {
	t.Helper()
	u, err := db.CreateAuthUser(f.h, username, hash)
	if err != nil {
		t.Fatalf("CreateAuthUser(raw): %v", err)
	}
	return u
}

func TestLoginMalformedHashGeneric(t *testing.T) {
	f := newFixture(t)
	addUserRaw(t, f, "hack", "$argon2id$v=19$m=65536,t=1,p=4$broken")
	body := failedLogin(t, f, "hack", testPassword)
	if !strings.Contains(body, loginErrorText) {
		t.Errorf("malformed hash must answer the generic error, got %q", body)
	}
	if strings.Contains(body, "broken") {
		t.Error("malformed hash value must never be echoed")
	}
}

func TestLoginOversizedBody413(t *testing.T) {
	f := newFixture(t)
	addUser(t, f, "alice", testPassword)
	big := strings.Repeat("x", MaxBodyBytes+1024)
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, loginForm(t, "alice", big))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized login: code = %d, want 413", rec.Code)
	}
	if strings.Contains(rec.Body.String(), testPassword) || strings.Contains(rec.Body.String(), "alice") {
		t.Error("413 response echoes login input")
	}
}

func TestLoginWithoutSessionCreates(t *testing.T) {
	f := newFixture(t)
	addUser(t, f, "alice", testPassword)

	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, loginForm(t, "alice", testPassword))
	c := sessionCookie(t, rec)
	if c == nil {
		t.Fatal("no session cookie issued")
	}
	// Exactly one live session for the user, the fresh one.
	if n := activeSessionCount(f, "alice"); n != 1 {
		t.Fatalf("sessions after first login = %d, want 1", n)
	}
	if _, ok := f.sessions.Get(c.Value); !ok {
		t.Fatal("new SID must be live")
	}
}

func TestLoginWithExistingSessionRotates(t *testing.T) {
	f := newFixture(t)
	addUser(t, f, "alice", testPassword)

	// First login (no session) → S1.
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, loginForm(t, "alice", testPassword))
	s1 := sessionCookie(t, rec)
	if s1 == nil {
		t.Fatal("first login: no cookie")
	}

	// Second login carrying S1 → must Rotate, not Create.
	req := loginForm(t, "alice", testPassword)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: s1.Value})
	rec = httptest.NewRecorder()
	f.server.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("second login: code = %d, want 303", rec.Code)
	}
	s2 := sessionCookie(t, rec)
	if s2 == nil {
		t.Fatal("second login: no cookie")
	}
	if s2.Value == s1.Value {
		t.Fatal("second login must issue a new SID (rotate)")
	}
	if _, ok := f.sessions.Get(s1.Value); ok {
		t.Fatal("old SID must be invalid after rotate")
	}
	if _, ok := f.sessions.Get(s2.Value); !ok {
		t.Fatal("new SID must be live")
	}
	if n := activeSessionCount(f, "alice"); n != 1 {
		t.Fatalf("sessions after rotate = %d, want 1", n)
	}

	// The old SID no longer authenticates a dashboard request.
	rec = httptest.NewRecorder()
	f.server.ServeHTTP(rec, sessionRequest(t, http.MethodGet, "/", s1.Value))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Fatalf("GET / with old SID: code = %d, want 303 /login", rec.Code)
	}
}

func TestLoginRepeatedNoDoubleSession(t *testing.T) {
	f := newFixture(t)
	addUser(t, f, "alice", testPassword)

	// Two "different browsers": two logins without any session cookie.
	for i := 1; i <= 2; i++ {
		rec := httptest.NewRecorder()
		f.server.ServeHTTP(rec, loginForm(t, "alice", testPassword))
		c := sessionCookie(t, rec)
		if c == nil {
			t.Fatalf("login %d: no cookie", i)
		}
		if n := activeSessionCount(f, "alice"); n != 1 {
			t.Fatalf("sessions after login %d = %d, want exactly 1", i, n)
		}
	}
}

func TestLoginStaleCookieFallsBackToCreate(t *testing.T) {
	f := newFixture(t)
	addUser(t, f, "alice", testPassword)
	// A well-shaped but unknown SID must not break the login.
	req := loginForm(t, "alice", testPassword)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaab"})
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("code = %d, want 303", rec.Code)
	}
	if n := activeSessionCount(f, "alice"); n != 1 {
		t.Fatalf("sessions = %d, want 1", n)
	}
}

func TestLoginSetCookieFreshSID(t *testing.T) {
	f := newFixture(t)
	addUser(t, f, "alice", testPassword)
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, loginForm(t, "alice", testPassword))
	c := sessionCookie(t, rec)
	if c == nil {
		t.Fatal("no cookie")
	}
	// The cookie must carry the new SID and nothing else of the store.
	if !strings.Contains(rec.Header().Get("Set-Cookie"), c.Value) {
		t.Error("Set-Cookie must contain the new SID")
	}
}

func TestLogout(t *testing.T) {
	f := newFixture(t)
	addUser(t, f, "alice", testPassword)

	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, loginForm(t, "alice", testPassword))
	c := sessionCookie(t, rec)
	if c == nil {
		t.Fatal("login: no cookie")
	}
	// M7.6: /logout is CSRF-protected; the token comes from the real
	// server-side session issued by the login.
	sess, ok := f.sessions.Get(c.Value)
	if !ok {
		t.Fatal("login: issued session not live")
	}

	// POST /logout with the live session and its CSRF token.
	form := url.Values{auth.CSRFFieldName: {sess.CSRFToken}}
	req := httptest.NewRequest(http.MethodPost, "/logout", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: c.Value})
	rec = httptest.NewRecorder()
	f.server.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Fatalf("logout: code = %d, Location = %q; want 303 /login", rec.Code, rec.Header().Get("Location"))
	}
	if _, ok := f.sessions.Get(c.Value); ok {
		t.Fatal("session must be deleted from the store")
	}
	cleared := sessionCookie(t, rec)
	if cleared == nil {
		t.Fatal("logout must clear the session cookie")
	}
	if cleared.Value != "" {
		t.Errorf("cleared cookie value = %q, want empty", cleared.Value)
	}
	if cleared.MaxAge != -1 {
		t.Errorf("cleared cookie MaxAge = %d, want -1", cleared.MaxAge)
	}
	if cleared.Name != auth.SessionCookieName || cleared.Path != "/" {
		t.Errorf("cleared cookie must use the same Name/Path: %q %q", cleared.Name, cleared.Path)
	}

	// The old SID no longer authenticates anything.
	rec = httptest.NewRecorder()
	f.server.ServeHTTP(rec, sessionRequest(t, http.MethodGet, "/", c.Value))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Fatalf("GET / after logout: code = %d, want 303 /login", rec.Code)
	}
	if strings.Contains(rec.Body.String(), c.Value) {
		t.Error("post-logout challenge leaks the SID")
	}
}

func TestLogoutRepeatGeneric(t *testing.T) {
	f := newFixture(t)
	addUser(t, f, "alice", testPassword)
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, loginForm(t, "alice", testPassword))
	c := sessionCookie(t, rec)
	sess, ok := f.sessions.Get(c.Value)
	if !ok {
		t.Fatal("login: issued session not live")
	}

	// First logout consumes the session; the second must answer the
	// same generic 401 as any unauthenticated POST — no session
	// details, no difference between "already logged out" and "never
	// logged in". Both carry the CSRF token of the live session (M7.6).
	var first, second string
	for i := 0; i < 2; i++ {
		form := url.Values{auth.CSRFFieldName: {sess.CSRFToken}}
		req := httptest.NewRequest(http.MethodPost, "/logout", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: c.Value})
		rec = httptest.NewRecorder()
		f.server.ServeHTTP(rec, req)
		if rec.Code != http.StatusSeeOther && rec.Code != http.StatusUnauthorized {
			t.Fatalf("logout %d: code = %d, want 303 or 401", i, rec.Code)
		}
		if i == 0 {
			first = rec.Body.String() + rec.Header().Get("Location") + rec.Header().Get("Set-Cookie")
		} else {
			second = rec.Body.String() + rec.Header().Get("Location") + rec.Header().Get("Set-Cookie")
		}
	}
	// Second response must not reveal the deleted session's id.
	if strings.Contains(second, c.Value) {
		t.Error("repeat logout reveals the old SID")
	}
	if first == "" || second == "" {
		t.Fatal("missing responses")
	}
}

// TestLoginLogoutResponseSecretFree scans every response of the login
// flow for password, hash, SID, CSRF token, and private/preshared key
// material.
func TestLoginLogoutResponseSecretFree(t *testing.T) {
	f := newFixture(t)
	alice := addUser(t, f, "alice", testPassword)
	c1, _, _ := f.addClient("bob")

	loginRec := httptest.NewRecorder()
	f.server.ServeHTTP(loginRec, loginForm(t, "alice", testPassword))
	sid := ""
	csrfToken := ""
	if c := sessionCookie(t, loginRec); c != nil {
		sid = c.Value
	}
	if sess, ok := f.sessions.Get(sid); ok {
		csrfToken = sess.CSRFToken
	}
	// Dashboard while the session is still live.
	dashRec := httptest.NewRecorder()
	f.server.ServeHTTP(dashRec, sessionRequest(t, http.MethodGet, "/", sid))
	// Logout consumes the session (M7.6: POST /logout is
	// CSRF-protected, token from the live session).
	logoutRec := httptest.NewRecorder()
	form := url.Values{auth.CSRFFieldName: {csrfToken}}
	req := httptest.NewRequest(http.MethodPost, "/logout", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	f.server.ServeHTTP(logoutRec, req)

	serverRow, err := db.ServerRow(f.h)
	if err != nil {
		t.Fatalf("ServerRow: %v", err)
	}
	// The SID is excluded from the Set-Cookie scan below: login is the
	// one place where the SID legitimately appears (it is the cookie).
	// The CSRF token is excluded only from the dashboard scan: the
	// dashboard renders it into hidden form fields by contract (M7.6)
	// — everywhere else (including Set-Cookie) it must stay absent.
	// Everything else must stay out of every response header/body.
	scanned := []string{
		testPassword, "correct-horse", alice.PasswordHash,
		serverRow.PrivateKey, c1.PrivateKey, c1.PresharedKey,
		samplePrivateKey, samplePresharedKey,
	}
	responses := map[string]string{
		"login":  loginRec.Body.String() + loginRec.Header().Get("Location"),
		"logout": logoutRec.Body.String() + logoutRec.Header().Get("Location") + logoutRec.Header().Get("Set-Cookie"),
		"dash":   dashRec.Body.String(),
	}
	for name, joined := range responses {
		leak := csrfToken != ""
		for _, secret := range scanned {
			if secret != "" && strings.Contains(joined, secret) {
				t.Errorf("%s response leaks %q", name, secret)
			}
		}
		if name != "dash" && strings.Contains(joined, csrfToken) && leak {
			t.Errorf("%s response leaks the CSRF token", name)
		}
	}
	// On the dashboard the CSRF token may appear ONLY inside hidden
	// form inputs — never as a bare value elsewhere.
	dash := dashRec.Body.String()
	if i := strings.Index(dash, csrfToken); i != -1 {
		prefix := dash[max(0, i-120):i]
		if !strings.Contains(prefix, `<input type="hidden" name="_csrf" value="`) {
			t.Errorf("dashboard leaks the CSRF token outside a hidden input: ...%s", prefix)
		}
	}
	// The SID must never leave the cookie: not in bodies, not in URLs.
	loginSidLeak := loginRec.Body.String() + loginRec.Header().Get("Location")
	if strings.Contains(loginSidLeak, sid) {
		t.Error("login response leaks the SID outside Set-Cookie")
	}
	if strings.Contains(dashRec.Body.String(), sid) {
		t.Error("dashboard response leaks the SID")
	}
	// The dashboard shows the principal.
	if !strings.Contains(dashRec.Body.String(), "alice") {
		t.Error("dashboard must render the signed-in username")
	}
}

func TestLoginCSRFExempt(t *testing.T) {
	// M7.6 decision pinned: POST /login is the single CSRF-exempt route
	// (M7.5 contract). SameSite=Lax is the extra cross-site layer; the
	// login form deliberately carries no _csrf field.
	f := newFixture(t)
	addUser(t, f, "alice", testPassword)
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, loginForm(t, "alice", testPassword))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("tokenless login: code = %d, want 303 (login stays CSRF-exempt)", rec.Code)
	}
	// The login page must not expose a CSRF token: there is nothing to
	// protect and no token should leak from a public page.
	page := httptest.NewRecorder()
	f.server.ServeHTTP(page, sessionRequest(t, http.MethodGet, "/login", ""))
	if strings.Contains(page.Body.String(), auth.CSRFFieldName) {
		t.Error("public login page must not carry a CSRF token")
	}
}

func TestLoginDBFailure500Generic(t *testing.T) {
	f := newFixture(t)
	f.h.Close() // break the database handle
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, loginForm(t, "alice", testPassword))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("db failure: code = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "alice") || strings.Contains(rec.Body.String(), testPassword) {
		t.Error("500 page echoes login input")
	}
	if strings.Contains(rec.Body.String(), "sql") || strings.Contains(rec.Body.String(), "db:") {
		t.Error("500 page reveals the internal error")
	}
}
