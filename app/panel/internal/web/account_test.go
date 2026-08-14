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

func TestAccountTOTPEnrollmentAndPasswordRotation(t *testing.T) {
	f := newFixture(t)
	addUser(t, f, f.username, testPassword)
	other, err := f.sessions.Create(f.username)
	if err != nil {
		t.Fatal(err)
	}
	post := func(path string, values url.Values, sid string) *httptest.ResponseRecorder {
		values.Set(auth.CSRFFieldName, f.csrf)
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(values.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
		rec := httptest.NewRecorder()
		f.server.ServeHTTP(rec, req)
		return rec
	}
	if rec := post("/account/totp/enroll", url.Values{}, f.sid); rec.Code != http.StatusSeeOther {
		t.Fatalf("enroll = %d", rec.Code)
	}
	row, _ := db.AuthUserByUsername(f.h, f.username)
	if row.TOTPSecret != "" {
		t.Fatal("secret saved before confirmation")
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/account", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: f.sid})
	f.server.ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, "otpauth://totp/") {
		t.Fatal("account must show otpauth payload")
	}
	secret := f.server.pendingTOTP[f.username]
	code, err := auth.TOTPCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if rec := post("/account/totp/confirm", url.Values{"code": {code}}, f.sid); rec.Code != http.StatusSeeOther {
		t.Fatalf("confirm = %d", rec.Code)
	}
	row, _ = db.AuthUserByUsername(f.h, f.username)
	if row.TOTPSecret == "" {
		t.Fatal("secret not saved after confirmation")
	}
	if _, ok := f.sessions.Get(other.ID); ok {
		t.Fatal("confirmation must invalidate other sessions")
	}
}

func TestAccountPasswordChangeFailureAndSessionInvalidation(t *testing.T) {
	f := newFixture(t)
	addUser(t, f, f.username, testPassword)
	other, err := f.sessions.Create(f.username)
	if err != nil {
		t.Fatal(err)
	}
	post := func(values url.Values, sid string) *httptest.ResponseRecorder {
		values.Set(auth.CSRFFieldName, f.csrf)
		req := httptest.NewRequest(http.MethodPost, "/account/password", strings.NewReader(values.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
		rec := httptest.NewRecorder()
		f.server.ServeHTTP(rec, req)
		return rec
	}
	bad := post(url.Values{"old_password": {"wrong"}, "new_password": {"new-password"}, "confirm_password": {"new-password"}}, f.sid)
	if bad.Code != http.StatusBadRequest || bad.Body.String() != accountError+"\n" {
		t.Fatalf("bad password response = %d %q", bad.Code, bad.Body.String())
	}
	row, _ := db.AuthUserByUsername(f.h, f.username)
	if !auth.VerifyPassword(testPassword, row.PasswordHash) {
		t.Fatal("failed change modified password")
	}
	rec := post(url.Values{"old_password": {testPassword}, "new_password": {"new-password"}, "confirm_password": {"new-password"}}, f.sid)
	if rec.Code != http.StatusSeeOther || sessionCookie(t, rec) == nil {
		t.Fatalf("valid password change = %d", rec.Code)
	}
	if _, ok := f.sessions.Get(f.sid); ok {
		t.Fatal("old session must be invalidated")
	}
	if _, ok := f.sessions.Get(other.ID); ok {
		t.Fatal("other sessions must be invalidated")
	}
	row, _ = db.AuthUserByUsername(f.h, f.username)
	if !auth.VerifyPassword("new-password", row.PasswordHash) {
		t.Fatal("new password not stored")
	}
}

func TestAccountDisableFailureIsGenericAndKeepsTOTP(t *testing.T) {
	f := newFixture(t)
	addUser(t, f, f.username, testPassword)
	secret := configureTOTPUser(t, f, f.username, "2fa")
	form := url.Values{"password": {"wrong"}, "code": {"000000"}, auth.CSRFFieldName: {f.csrf}}
	req := httptest.NewRequest(http.MethodPost, "/account/totp/disable", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: f.sid})
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || rec.Body.String() != accountError+"\n" {
		t.Fatalf("disable failure = %d %q", rec.Code, rec.Body.String())
	}
	u, _ := db.AuthUserByUsername(f.h, f.username)
	if u.TOTPSecret != secret || u.TOTPMode != "2fa" {
		t.Fatalf("failed disable changed TOTP: %+v", u)
	}
}

func TestAccountTOTPDisableSuccess(t *testing.T) {
	f := newFixture(t)
	addUser(t, f, f.username, testPassword)
	secret := configureTOTPUser(t, f, f.username, "2fa")
	other, err := f.sessions.Create(f.username)
	if err != nil {
		t.Fatal(err)
	}
	code, err := auth.TOTPCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"password": {testPassword}, "code": {code}, auth.CSRFFieldName: {f.csrf}}
	req := httptest.NewRequest(http.MethodPost, "/account/totp/disable", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: f.sid})
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("disable = %d, want 303", rec.Code)
	}
	u, _ := db.AuthUserByUsername(f.h, f.username)
	if u.TOTPSecret != "" || u.TOTPMode != "" {
		t.Fatalf("2FA remains enabled: %+v", u)
	}
	if _, ok := f.sessions.Get(f.sid); ok {
		t.Fatal("disable must rotate current session")
	}
	if _, ok := f.sessions.Get(other.ID); ok {
		t.Fatal("disable must invalidate other sessions")
	}
}

func TestAccountTOTPModeTransitions(t *testing.T) {
	f := newFixture(t)
	addUser(t, f, f.username, testPassword)
	secret := configureTOTPUser(t, f, f.username, "")
	sid, csrf := f.sid, f.csrf
	postMode := func(mode, code string) {
		form := url.Values{"mode": {mode}, "password": {testPassword}, "code": {code}, auth.CSRFFieldName: {csrf}}
		req := httptest.NewRequest(http.MethodPost, "/account/totp/mode", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
		rec := httptest.NewRecorder()
		f.server.ServeHTTP(rec, req)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("mode %q = %d, body=%q", mode, rec.Code, rec.Body.String())
		}
		cookie := sessionCookie(t, rec)
		if cookie == nil {
			t.Fatalf("mode %q did not rotate cookie", mode)
		}
		sess, ok := f.sessions.Get(cookie.Value)
		if !ok {
			t.Fatalf("mode %q issued dead session", mode)
		}
		sid, csrf = sess.ID, sess.CSRFToken
	}
	postMode("2fa", "")
	u, _ := db.AuthUserByUsername(f.h, f.username)
	if u.TOTPMode != "2fa" {
		t.Fatalf("mode after 2fa = %q", u.TOTPMode)
	}
	code, err := auth.TOTPCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	postMode("passwordless", code)
	u, _ = db.AuthUserByUsername(f.h, f.username)
	if u.TOTPMode != "passwordless" {
		t.Fatalf("mode after passwordless = %q", u.TOTPMode)
	}
	code, err = auth.TOTPCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	postMode("", code)
	u, _ = db.AuthUserByUsername(f.h, f.username)
	if u.TOTPMode != "" {
		t.Fatalf("mode after password-only = %q", u.TOTPMode)
	}
}
