package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func loginRequest(t *testing.T, remoteAddr string, fields url.Values) *http.Request {
	t.Helper()
	req := loginFields(t, fields)
	req.RemoteAddr = remoteAddr
	return req
}

func postLogin(t *testing.T, f *fixture, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, req)
	return rec
}

func TestLoginRateLimitSixthWrongPassword429(t *testing.T) {
	f := newFixture(t)
	addUser(t, f, "alice", testPassword)
	ip := "203.0.113.10:12345"
	wrong := url.Values{"username": {"alice"}, "password": {"wrong-password"}}

	for i := 1; i <= 5; i++ {
		rec := postLogin(t, f, loginRequest(t, ip, wrong))
		if rec.Code != http.StatusOK {
			t.Fatalf("fail %d: code = %d, want 200", i, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), loginErrorText) {
			t.Fatalf("fail %d: missing generic password error", i)
		}
	}

	rec := postLogin(t, f, loginRequest(t, ip, wrong))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("6th fail: code = %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("429 must set Retry-After")
	}
	if _, err := strconv.Atoi(rec.Header().Get("Retry-After")); err != nil {
		t.Fatalf("Retry-After = %q, want seconds", rec.Header().Get("Retry-After"))
	}
	if sessionCookie(t, rec) != nil {
		t.Fatal("429 must not Set-Cookie a session")
	}
	body := rec.Body.String()
	if !strings.Contains(body, loginLimitMessage) {
		t.Fatalf("429 body missing limit text: %q", body)
	}
	if strings.Contains(body, "wrong-password") {
		t.Fatal("429 body must not echo the password")
	}
}

func TestLoginRateLimitIsPerIP(t *testing.T) {
	f := newFixture(t)
	addUser(t, f, "alice", testPassword)
	wrong := url.Values{"username": {"alice"}, "password": {"wrong-password"}}
	ipA := "203.0.113.10:12345"
	ipB := "203.0.113.20:12345"

	for i := 1; i <= 5; i++ {
		rec := postLogin(t, f, loginRequest(t, ipA, wrong))
		if rec.Code != http.StatusOK {
			t.Fatalf("IP A fail %d: code = %d, want 200", i, rec.Code)
		}
	}

	rec := postLogin(t, f, loginRequest(t, ipB, wrong))
	if rec.Code != http.StatusOK {
		t.Fatalf("IP B after A locked: code = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), loginErrorText) {
		t.Fatal("IP B should still get a normal failed-login form")
	}

	ok := postLogin(t, f, loginRequest(t, ipB, url.Values{"username": {"alice"}, "password": {testPassword}}))
	if ok.Code != http.StatusSeeOther {
		t.Fatalf("IP B success: code = %d, want 303", ok.Code)
	}
}

func TestLoginRateLimitKeysOnXRealIP(t *testing.T) {
	f := newFixture(t)
	addUser(t, f, "alice", testPassword)
	wrong := url.Values{"username": {"alice"}, "password": {"wrong-password"}}

	for i := 1; i <= 5; i++ {
		req := loginRequest(t, "127.0.0.1:12345", wrong)
		req.Header.Set("X-Real-IP", "198.51.100.9")
		rec := postLogin(t, f, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("X-Real-IP fail %d: code = %d, want 200", i, rec.Code)
		}
	}

	loopback := postLogin(t, f, loginRequest(t, "127.0.0.1:12345", wrong))
	if loopback.Code != http.StatusOK {
		t.Fatalf("loopback without X-Real-IP: code = %d, want 200 (different key)", loopback.Code)
	}

	limited := loginRequest(t, "127.0.0.1:9", wrong)
	limited.Header.Set("X-Real-IP", "198.51.100.9")
	rec := postLogin(t, f, limited)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("same X-Real-IP 6th: code = %d, want 429", rec.Code)
	}
}

func TestLoginStoredTOTPPasswordSuccessDoesNotCount(t *testing.T) {
	t.Setenv("AMNEZIA_SECURE_COOKIES", "")
	f := newFixture(t)
	addUser(t, f, "alice", testPassword)
	configureTOTPUser(t, f, "alice", "2fa")
	ip := "203.0.113.10:12345"
	passwordOnly := url.Values{"username": {"alice"}, "password": {testPassword}}

	rec := postLogin(t, f, loginRequest(t, ip, passwordOnly))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("password-only with stored TOTP: code = %d, want 303", rec.Code)
	}
	if sessionCookie(t, rec) == nil {
		t.Fatal("must issue a session")
	}
}

func TestLoginSuccessClearsLimit(t *testing.T) {
	t.Setenv("AMNEZIA_SECURE_COOKIES", "")
	f := newFixture(t)
	addUser(t, f, "alice", testPassword)
	ip := "203.0.113.10:12345"
	wrong := url.Values{"username": {"alice"}, "password": {"wrong-password"}}
	good := url.Values{"username": {"alice"}, "password": {testPassword}}

	for i := 1; i <= 3; i++ {
		rec := postLogin(t, f, loginRequest(t, ip, wrong))
		if rec.Code != http.StatusOK {
			t.Fatalf("pre-success fail %d: code = %d", i, rec.Code)
		}
	}

	ok := postLogin(t, f, loginRequest(t, ip, good))
	if ok.Code != http.StatusSeeOther || sessionCookie(t, ok) == nil {
		t.Fatalf("success: code = %d cookie=%v", ok.Code, sessionCookie(t, ok))
	}

	rec := postLogin(t, f, loginRequest(t, ip, wrong))
	if rec.Code != http.StatusOK {
		t.Fatalf("wrong password after success: code = %d, want 200 (fail #1, not 429)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), loginErrorText) {
		t.Fatal("post-clear failure must be a normal login error")
	}
}

func TestClientIPPrefersValidXRealIP(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/login", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("X-Real-IP", "198.51.100.9")
	req.Header.Set("X-Forwarded-For", "203.0.113.1, 192.0.2.1")
	if got := clientIP(req); got != "198.51.100.9" {
		t.Fatalf("clientIP = %q, want X-Real-IP", got)
	}

	req.Header.Set("X-Real-IP", "not-an-ip")
	if got := clientIP(req); got != "127.0.0.1" {
		t.Fatalf("invalid X-Real-IP: clientIP = %q, want RemoteAddr host", got)
	}

	plain := httptest.NewRequest(http.MethodPost, "/login", nil)
	plain.RemoteAddr = "203.0.113.10:12345"
	if got := clientIP(plain); got != "203.0.113.10" {
		t.Fatalf("RemoteAddr host: clientIP = %q", got)
	}
}

func TestLoginGETUnlimited(t *testing.T) {
	f := newFixture(t)
	addUser(t, f, "alice", testPassword)
	ip := "203.0.113.10:12345"
	wrong := url.Values{"username": {"alice"}, "password": {"wrong-password"}}
	for i := 1; i <= 5; i++ {
		postLogin(t, f, loginRequest(t, ip, wrong))
	}
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	req.RemoteAddr = ip
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /login while POST-limited: code = %d, want 200", rec.Code)
	}
	if strings.Contains(rec.Body.String(), loginLimitMessage) {
		t.Fatal("GET /login must not be rate-limited")
	}
}
