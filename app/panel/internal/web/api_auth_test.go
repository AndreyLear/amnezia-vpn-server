package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/auth"
)

func apiJSON(t *testing.T, method, path string, body any) *http.Request {
	t.Helper()
	var buf *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		buf = bytes.NewReader(b)
	}
	var req *http.Request
	if buf != nil {
		req = httptest.NewRequest(method, path, buf)
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	assignLoginRemoteAddr(req)
	return req
}

func decodeAPI(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("content-type = %q, want application/json; body=%s", ct, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("json: %v body=%s", err, rec.Body.String())
	}
	return out
}

func TestAPILoginWrongPassword(t *testing.T) {
	f := newFixture(t)
	addUser(t, f, "alice", testPassword)

	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, apiJSON(t, http.MethodPost, "/api/login", map[string]string{
		"username": "alice",
		"password": "wrong-password",
	}))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
	got := decodeAPI(t, rec)
	if got["ok"] != false {
		t.Fatalf("ok = %v, want false", got["ok"])
	}
	if got["message"] != loginErrorText {
		t.Fatalf("message = %q, want %q", got["message"], loginErrorText)
	}
	if sessionCookie(t, rec) != nil {
		t.Fatal("wrong password must not Set-Cookie")
	}
}

func TestAPILoginNeedCode(t *testing.T) {
	f := newFixture(t)
	addUser(t, f, "alice", testPassword)
	configureTOTPUser(t, f, "alice", "2fa")

	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, apiJSON(t, http.MethodPost, "/api/login", map[string]string{
		"username": "alice",
		"password": testPassword,
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	got := decodeAPI(t, rec)
	if got["ok"] != false {
		t.Fatalf("ok = %v, want false", got["ok"])
	}
	if got["need_code"] != true {
		t.Fatalf("need_code = %v, want true", got["need_code"])
	}
	if sessionCookie(t, rec) != nil || activeSessionCount(f, "alice") != 0 {
		t.Fatal("need_code must not issue a session cookie")
	}
}

func TestAPILoginTOTPThenMe(t *testing.T) {
	t.Setenv("AMNEZIA_SECURE_COOKIES", "")
	f := newFixture(t)
	addUser(t, f, "alice", testPassword)
	secret := configureTOTPUser(t, f, "alice", "2fa")
	code, err := auth.TOTPCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, apiJSON(t, http.MethodPost, "/api/login", map[string]string{
		"username": "alice",
		"password": testPassword,
		"code":     code,
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("login code = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	got := decodeAPI(t, rec)
	if got["ok"] != true {
		t.Fatalf("ok = %v, want true", got["ok"])
	}
	c := sessionCookie(t, rec)
	if c == nil {
		t.Fatal("TOTP success must Set-Cookie")
	}

	me := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: c.Value})
	f.server.ServeHTTP(me, req)
	if me.Code != http.StatusOK {
		t.Fatalf("GET /api/me code = %d, want 200; body=%s", me.Code, me.Body.String())
	}
	body := decodeAPI(t, me)
	if body["username"] != "alice" {
		t.Fatalf("username = %v", body["username"])
	}
	csrf, _ := body["csrf"].(string)
	if csrf == "" {
		t.Fatal("csrf missing")
	}
	totp, _ := body["totp"].(map[string]any)
	if totp == nil || totp["enabled"] != true {
		t.Fatalf("totp = %v, want enabled true", body["totp"])
	}
}

func TestAPIMeUnauthorized(t *testing.T) {
	f := newFixture(t)
	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "" {
		t.Fatalf("API must not 303 to login: Location %q", loc)
	}
	got := decodeAPI(t, rec)
	if got["ok"] != false {
		t.Fatalf("ok = %v", got["ok"])
	}
}

func TestAPILoginWrongCode(t *testing.T) {
	f := newFixture(t)
	addUser(t, f, "alice", testPassword)
	configureTOTPUser(t, f, "alice", "2fa")

	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, apiJSON(t, http.MethodPost, "/api/login", map[string]string{
		"username": "alice",
		"password": testPassword,
		"code":     "000000",
	}))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
	got := decodeAPI(t, rec)
	if got["message"] != "Неверный код." {
		t.Fatalf("message = %q", got["message"])
	}
	if sessionCookie(t, rec) != nil {
		t.Fatal("wrong code must not Set-Cookie")
	}
}

func TestAPICSRFHeaderOK(t *testing.T) {
	f := newFixture(t)
	req := httptest.NewRequest(http.MethodPost, "/api/logout", nil)
	req.Header.Set(auth.CSRFHeaderName, f.csrf)
	rec := httptest.NewRecorder()
	f.serve(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("logout with header: code = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	got := decodeAPI(t, rec)
	if got["ok"] != true {
		t.Fatalf("ok = %v", got["ok"])
	}
}

func TestAPICSRFCookieStillForbidden(t *testing.T) {
	f := newFixture(t)
	req := httptest.NewRequest(http.MethodPost, "/api/logout", nil)
	req.AddCookie(&http.Cookie{Name: auth.CSRFFieldName, Value: f.csrf})
	rec := httptest.NewRecorder()
	f.serve(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cookie csrf: code = %d, want 403", rec.Code)
	}
	got := decodeAPI(t, rec)
	if got["ok"] != false || got["message"] != "Forbidden." {
		t.Fatalf("body = %v", got)
	}
}

func TestAPICSRFFormFieldOK(t *testing.T) {
	f := newFixture(t)
	rec := csrfPOST(f, "/api/logout", f.csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("form _csrf logout: code = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

func TestAPIMeRestorePending(t *testing.T) {
	f := newFixture(t)
	rec := f.get("/api/me")
	got := decodeAPI(t, rec)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d body=%s", rec.Code, rec.Body.String())
	}
	if got["restore_pending"] != false {
		t.Fatalf("restore_pending = %v, want false", got["restore_pending"])
	}

	dir := setBackupsPath(t)
	preparedRestore(t, f, dir)
	again := decodeAPI(t, f.get("/api/me"))
	if again["restore_pending"] != true {
		t.Fatalf("after prepare restore_pending = %v, want true", again["restore_pending"])
	}
}

func TestAPIOversizedJSON413(t *testing.T) {
	f := newFixture(t)
	big := strings.Repeat("x", MaxBodyBytes+1)
	req := httptest.NewRequest(http.MethodPost, "/api/clients", strings.NewReader(`{"name":"`+big+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(auth.CSRFHeaderName, f.csrf)
	rec := httptest.NewRecorder()
	f.serve(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("code = %d, want 413; body=%s", rec.Code, rec.Body.String())
	}
	got := decodeAPI(t, rec)
	if got["ok"] != false || got["message"] != "Тело запроса слишком большое." {
		t.Fatalf("413 JSON = %v", got)
	}
}

func TestAPIInternalFailureJSON(t *testing.T) {
	f := newFixture(t)
	if err := f.h.Close(); err != nil {
		t.Fatal(err)
	}
	rec := f.get("/api/clients")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("code = %d, want 500; body=%s", rec.Code, rec.Body.String())
	}
	got := decodeAPI(t, rec)
	if got["ok"] != false || got["message"] != "Внутренняя ошибка сервера." {
		t.Fatalf("500 JSON = %v", got)
	}
}

func TestHTMLClientsNewCSRFHeaderOK(t *testing.T) {
	f := newFixture(t)
	req := httptest.NewRequest(http.MethodPost, "/clients/new", strings.NewReader("name=header-csrf"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set(auth.CSRFHeaderName, f.csrf)
	rec := httptest.NewRecorder()
	f.serve(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("HTML POST with CSRF header: code = %d, want 303; body=%s", rec.Code, rec.Body.String())
	}
}
