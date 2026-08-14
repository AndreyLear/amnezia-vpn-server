// M7.4 unit tests for the session-bearer HTTP middleware and the
// cookie contract (internal/auth/auth.go). The integration matrix
// lives in internal/web/auth_test.go; this file covers the cookie
// helpers, the challenge semantics, the context identity and the
// store/middleware interplay.
package auth

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// writeSID serves as a login-like handler for cookie assertions.
func cookieStub(sid string, expires time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		WriteSessionCookie(w, sid, expires)
	}
}

func TestWriteSessionCookieAttributes(t *testing.T) {
	t.Setenv("AMNEZIA_SECURE_COOKIES", "")
	rec := httptest.NewRecorder()
	expires := time.Now().UTC().Add(25 * time.Minute)
	cookieStub("sid-43-chars-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", expires)(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	res := rec.Result()
	cookies := res.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("got %d cookies, want 1", len(cookies))
	}
	c := cookies[0]
	if c.Name != SessionCookieName {
		t.Errorf("name = %q, want %q", c.Name, SessionCookieName)
	}
	if c.HttpOnly != true {
		t.Error("HttpOnly must be set")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want SameSiteLaxMode", c.SameSite)
	}
	if c.Path != "/" {
		t.Errorf("Path = %q, want /", c.Path)
	}
	if c.Secure {
		t.Error("Secure must stay false while the panel serves plain HTTP on loopback")
	}
	if c.MaxAge <= 0 {
		t.Errorf("MaxAge = %d, want a positive lifetime", c.MaxAge)
	}
	if !c.Expires.Equal(expires.Truncate(time.Second)) && !c.Expires.Equal(expires) {
		t.Errorf("Expires = %v, want %v", c.Expires, expires)
	}
	raw := rec.Header().Get("Set-Cookie")
	if strings.Contains(raw, "Secure") {
		t.Errorf("raw cookie must not contain Secure: %q", raw)
	}
	if !strings.Contains(raw, "HttpOnly") || !strings.Contains(raw, "SameSite=Lax") {
		t.Errorf("raw cookie misses attributes: %q", raw)
	}
}

// TestWriteSessionCookieSecureFlag (T-124): behind a TLS proxy the
// deployment sets AMNEZIA_SECURE_COOKIES=1 and the session cookie must
// carry the Secure attribute (including the raw Set-Cookie header).
func TestWriteSessionCookieSecureFlag(t *testing.T) {
	t.Setenv("AMNEZIA_SECURE_COOKIES", "1")
	rec := httptest.NewRecorder()
	expires := time.Now().UTC().Add(25 * time.Minute)
	cookieStub("sid-43-chars-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", expires)(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("got %d cookies, want 1", len(cookies))
	}
	if !cookies[0].Secure {
		t.Error("Secure must be set when AMNEZIA_SECURE_COOKIES=1")
	}
	if !strings.Contains(rec.Header().Get("Set-Cookie"), "Secure") {
		t.Errorf("raw cookie must contain Secure: %q", rec.Header().Get("Set-Cookie"))
	}
}

func TestClearSessionCookieDeletes(t *testing.T) {
	rec := httptest.NewRecorder()
	ClearSessionCookie(rec)
	for _, c := range rec.Result().Cookies() {
		if c.Name != SessionCookieName {
			continue
		}
		if c.MaxAge != -1 {
			t.Errorf("MaxAge = %d, want -1 (delete)", c.MaxAge)
		}
		if !c.Expires.Before(time.Now()) {
			t.Errorf("Expires = %v, want a past instant", c.Expires)
		}
	}
}

func TestReadSessionID(t *testing.T) {
	s := NewSessionStore(SessionTTL)
	sess, err := s.Create("alice")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sess.ID})
	if got, ok := ReadSessionID(req); !ok || got != sess.ID {
		t.Fatalf("ReadSessionID = %q, %v; want %q, true", got, ok, sess.ID)
	}

	cases := []struct {
		name string
		val  string
	}{
		{"no cookie", ""},
		{"empty value", ""},
		{"short", "abc"},
		{"long garbage", strings.Repeat("x", 44)},
		{"bad alphabet", "!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!%"},
		{"valid length invalid base64", "!!!!" + strings.Repeat("0", 39)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.val != "" {
				req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: tc.val})
			}
			if got, ok := ReadSessionID(req); ok {
				t.Fatalf("ReadSessionID(%q) = %q, true; want false", tc.val, got)
			}
		})
	}
}

func TestRequireAuthConsumesInvalidateSentinel(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "amnezia.sqlite")
	store := NewSessionStore(SessionTTL)
	sess, err := store.Create("alice")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := RequestInvalidateSessions(dbPath, "alice"); err != nil {
		t.Fatal(err)
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sess.ID})
	NewAuth(store).WithDBPath(dbPath).RequireAuth(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Fatalf("after sentinel: %d Location %q; want 303 /login", rec.Code, rec.Header().Get("Location"))
	}
}

func TestRequireAuthValidSession(t *testing.T) {
	store := NewSessionStore(SessionTTL)
	sess, err := store.Create("alice")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	var got Session
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ok bool
		got, ok = CurrentUser(r.Context())
		if !ok {
			t.Error("CurrentUser must report the session")
		}
		w.WriteHeader(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sess.ID})
	NewAuth(store).RequireAuth(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
	if got.Username != "alice" || got.ID != sess.ID {
		t.Fatalf("identity = %+v, want %+v", got, sess)
	}
}

func TestRequireAuthChallengeSemantics(t *testing.T) {
	store := NewSessionStore(SessionTTL)
	sid := "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefddx"
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	auth := NewAuth(store)

	cases := []struct {
		method string
		code   int
		loc    string
	}{
		{http.MethodGet, http.StatusSeeOther, "/login"},
		{http.MethodHead, http.StatusSeeOther, "/login"},
		{http.MethodPost, http.StatusUnauthorized, ""},
		{http.MethodPut, http.StatusUnauthorized, ""},
		{http.MethodDelete, http.StatusUnauthorized, ""},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(tc.method, "/", nil)
		req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sid})
		auth.RequireAuth(next).ServeHTTP(rec, req)
		if rec.Code != tc.code {
			t.Errorf("%s: code = %d, want %d", tc.method, rec.Code, tc.code)
		}
		if got := rec.Header().Get("Location"); got != tc.loc {
			t.Errorf("%s: Location = %q, want %q", tc.method, got, tc.loc)
		}
		// The challenge must never leak the session id or any identity.
		if strings.Contains(rec.Body.String(), sid) || strings.Contains(rec.Header().Get("Location"), sid) {
			t.Errorf("%s: response leaks the session id %q", tc.method, sid)
		}
	}
}

func TestRequireAuthMissingOrUnknownSession(t *testing.T) {
	store := NewSessionStore(SessionTTL)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	auth := NewAuth(store)
	okSid := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaab" // unknown but well-shaped
	for _, tc := range []struct {
		name string
		val  string
	}{
		{"missing", ""},
		{"unknown", okSid},
		{"malformed", "not-a-session"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.val != "" {
				req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: tc.val})
			}
			auth.RequireAuth(next).ServeHTTP(rec, req)
			if rec.Code != http.StatusSeeOther {
				t.Fatalf("code = %d, want 303", rec.Code)
			}
			if rec.Header().Get("Location") != "/login" {
				t.Fatalf("Location = %q, want /login", rec.Header().Get("Location"))
			}
		})
	}
}

func TestRequireAuthExpiredSession(t *testing.T) {
	store := NewSessionStore(30 * time.Millisecond)
	sess, err := store.Create("alice")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	time.Sleep(60 * time.Millisecond)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sess.ID})
	NewAuth(store).RequireAuth(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("code = %d, want 303 (expired) ", rec.Code)
	}
	if _, ok := store.Get(sess.ID); ok {
		t.Fatal("expired session must be removed by the middleware lookup")
	}
}

// TestRequireAuthConcurrent hammers the middleware with mixed valid,
// unknown and malformed cookies: every request must either reach the
// handler with its identity or receive the challenge — never anything
// in between, and never a race.
func TestRequireAuthConcurrent(t *testing.T) {
	store := NewSessionStore(SessionTTL)
	valid, err := store.Create("alice")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := CurrentUser(r.Context()); !ok {
			t.Error("authenticated request without identity")
		}
		w.WriteHeader(http.StatusOK)
	})
	auth := NewAuth(store)
	const n = 64
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			switch i % 3 {
			case 0:
				req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: valid.ID})
			case 1:
				req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "unknown-shape-0000000000000000000000000000000"})
			}
			rec := httptest.NewRecorder()
			auth.RequireAuth(next).ServeHTTP(rec, req)
			switch i % 3 {
			case 0:
				if rec.Code != http.StatusOK {
					t.Errorf("valid session: code = %d, want 200", rec.Code)
				}
			default:
				if rec.Code != http.StatusSeeOther {
					t.Errorf("bad session: code = %d, want 303", rec.Code)
				}
			}
		}(i)
	}
	wg.Wait()
}

func TestCurrentUserEmptyContext(t *testing.T) {
	if _, ok := CurrentUser(context.Background()); ok {
		t.Fatal("CurrentUser must report false outside RequireAuth")
	}
}

// ---- M7.6 CSRF middleware unit tests ----

// csrfForm builds a POST carrying the token in the form body.
func csrfForm(token string) *http.Request {
	form := url.Values{CSRFFieldName: {token}}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

func TestRequireCSRFSafeMethodsPassWithoutToken(t *testing.T) {
	store := NewSessionStore(SessionTTL)
	_, sess := createForCSRF(t, store)
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		req := httptest.NewRequest(method, "/", nil)
		req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sess.ID})
		rec := httptest.NewRecorder()
		csrfHandler(store).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
			t.Errorf("%s: code = %d body = %q, want 200 ok", method, rec.Code, rec.Body.String())
		}
	}
}

func TestRequireCSRFValidTokenPasses(t *testing.T) {
	store := NewSessionStore(SessionTTL)
	_, sess := createForCSRF(t, store)
	req := csrfForm(sess.CSRFToken)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sess.ID})
	rec := httptest.NewRecorder()
	csrfHandler(store).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("valid token: code = %d body = %q, want 200 ok", rec.Code, rec.Body.String())
	}
}

func TestRequireCSRFMissingToken403(t *testing.T) {
	store := NewSessionStore(SessionTTL)
	_, sess := createForCSRF(t, store)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("name=x"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sess.ID})
	rec := httptest.NewRecorder()
	csrfHandler(store).ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("missing token: code = %d, want 403", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, sess.CSRFToken) || strings.Contains(body, sess.ID) || strings.Contains(body, "amnezia") {
		t.Errorf("403 leaks details: %q", body)
	}
	// The failure must not touch the session store.
	if _, ok := store.Get(sess.ID); !ok {
		t.Fatal("CSRF failure must not delete the session")
	}
}

func TestRequireCSRFWrongToken403(t *testing.T) {
	store := NewSessionStore(SessionTTL)
	_, sess := createForCSRF(t, store)
	for _, token := range []string{"wrong-token", "", "short", strings.Repeat("x", 43)} {
		req := csrfForm(token)
		req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sess.ID})
		rec := httptest.NewRecorder()
		csrfHandler(store).ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("token %q: code = %d, want 403", token, rec.Code)
		}
	}
	if _, ok := store.Get(sess.ID); !ok {
		t.Fatal("wrong-token failures must not delete the session")
	}
}

// TestRequireCSRFTokenNotInQueryOrCookie pins the M7.6 rule: the token
// is accepted only from the form body — never from the query string or
// a cookie of the same name.
func TestRequireCSRFTokenNotInQueryOrCookie(t *testing.T) {
	store := NewSessionStore(SessionTTL)
	_, sess := createForCSRF(t, store)
	// Token in the query string, absent from the body.
	req := httptest.NewRequest(http.MethodPost, "/?"+CSRFFieldName+"="+sess.CSRFToken, strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sess.ID})
	rec := httptest.NewRecorder()
	csrfHandler(store).ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("token in query: code = %d, want 403", rec.Code)
	}
	// Token in a cookie, absent from the body.
	req = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sess.ID})
	req.AddCookie(&http.Cookie{Name: CSRFFieldName, Value: sess.CSRFToken})
	rec = httptest.NewRecorder()
	csrfHandler(store).ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("token in cookie: code = %d, want 403", rec.Code)
	}
	// Token in a custom header, absent from the body.
	req = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(""))
	req.Header.Set(CSRFFieldName, sess.CSRFToken)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sess.ID})
	rec = httptest.NewRecorder()
	csrfHandler(store).ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("token in header: code = %d, want 403", rec.Code)
	}
}

func TestRequireCSRFExpiredSessionChallenge(t *testing.T) {
	store := NewSessionStore(30 * time.Millisecond)
	_, sess := createForCSRF(t, store)
	time.Sleep(60 * time.Millisecond)
	req := csrfForm(sess.CSRFToken)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sess.ID})
	rec := httptest.NewRecorder()
	csrfHandler(store).ServeHTTP(rec, req)
	// An expired session is rejected by the auth layer before CSRF:
	// POST → generic 401 (never 403, never 200).
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expired session: code = %d, want 401", rec.Code)
	}
}

func TestRequireCSRFNoSessionChallenge(t *testing.T) {
	store := NewSessionStore(SessionTTL)
	req := csrfForm("whatever")
	rec := httptest.NewRecorder()
	csrfHandler(store).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no session: code = %d, want 401", rec.Code)
	}
}

func TestRequireCSRFOversizedBody413(t *testing.T) {
	store := NewSessionStore(SessionTTL)
	_, sess := createForCSRF(t, store)
	// The web layer wraps the body with MaxBytesReader before auth/CSRF
	// run; the middleware must map that limit violation to the same 413
	// the handlers use, and must not touch the session.
	big := strings.Repeat("x", 65<<10)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(CSRFFieldName+"="+big))
	req.Body = http.MaxBytesReader(httptest.NewRecorder(), req.Body, 64<<10)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sess.ID})
	rec := httptest.NewRecorder()
	csrfHandler(store).ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body: code = %d, want 413", rec.Code)
	}
	if _, ok := store.Get(sess.ID); !ok {
		t.Fatal("413 must not delete the session")
	}
}

func TestRequireCSRFSessionStoreUntouched(t *testing.T) {
	store := NewSessionStore(SessionTTL)
	_, sess := createForCSRF(t, store)
	before, _ := store.Get(sess.ID)
	req := csrfForm("wrong")
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sess.ID})
	rec := httptest.NewRecorder()
	csrfHandler(store).ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403", rec.Code)
	}
	after, ok := store.Get(sess.ID)
	if !ok || after.CSRFToken != before.CSRFToken || after.ID != before.ID {
		t.Fatal("CSRF failure must leave the session untouched (no rotation, no deletion)")
	}
}

// createForCSRF returns a fresh session from store.
func createForCSRF(t *testing.T, store *SessionStore) (*Auth, Session) {
	t.Helper()
	sess, err := store.Create("alice")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return NewAuth(store), sess
}

// csrfHandler builds RequireAuth(RequireCSRF(stub)) around store.
func csrfHandler(store *SessionStore) http.Handler {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	})
	return NewAuth(store).RequireAuth(NewAuth(store).RequireCSRF(next))
}
