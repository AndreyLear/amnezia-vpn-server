package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/auth"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

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

func TestAccountChangePassword(t *testing.T) {
	f := newFixture(t)
	addUser(t, f, f.username, testPassword)
	post := func(values url.Values) *httptest.ResponseRecorder {
		values.Set(auth.CSRFFieldName, f.csrf)
		req := httptest.NewRequest(http.MethodPost, "/account/password", strings.NewReader(values.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: f.sid})
		rec := httptest.NewRecorder()
		f.server.ServeHTTP(rec, req)
		return rec
	}

	same := post(url.Values{"old_password": {testPassword}, "new_password": {testPassword}, "confirm_password": {testPassword}})
	if same.Code == http.StatusSeeOther {
		t.Fatalf("same password must not succeed: %d Location %q", same.Code, same.Header().Get("Location"))
	}
	if !strings.Contains(same.Body.String(), accountPasswordUnchanged) {
		t.Fatalf("same password body = %q, want %q", same.Body.String(), accountPasswordUnchanged)
	}
	if strings.Contains(same.Body.String(), accountError) {
		t.Fatal("same password must not use the generic account error")
	}
	row, _ := db.AuthUserByUsername(f.h, f.username)
	if !auth.VerifyPassword(testPassword, row.PasswordHash) {
		t.Fatal("same-password change must leave hash verifiable with the current password")
	}
	if _, ok := f.sessions.Get(f.sid); !ok {
		t.Fatal("old session must still be valid after same-password rejection")
	}

	mismatch := post(url.Values{"old_password": {testPassword}, "new_password": {"new-password"}, "confirm_password": {"other-password"}})
	if mismatch.Code != http.StatusBadRequest || mismatch.Body.String() != accountError+"\n" {
		t.Fatalf("confirm mismatch = %d %q", mismatch.Code, mismatch.Body.String())
	}
	if strings.Contains(mismatch.Body.String(), accountPasswordUnchanged) {
		t.Fatal("confirm mismatch must not reveal the same-password message")
	}
	wrongOld := post(url.Values{"old_password": {"wrong"}, "new_password": {testPassword}, "confirm_password": {testPassword}})
	if wrongOld.Code != http.StatusBadRequest || wrongOld.Body.String() != accountError+"\n" {
		t.Fatalf("wrong old password = %d %q", wrongOld.Code, wrongOld.Body.String())
	}
}

func TestAccountPasswordChangeIgnoresStoredTOTP(t *testing.T) {
	f := newFixture(t)
	addUser(t, f, f.username, testPassword)
	secret := seedLegacyPasswordless(t, f, f.username)
	post := func(values url.Values) *httptest.ResponseRecorder {
		values.Set(auth.CSRFFieldName, f.csrf)
		req := httptest.NewRequest(http.MethodPost, "/account/password", strings.NewReader(values.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: f.sid})
		rec := httptest.NewRecorder()
		f.server.ServeHTTP(rec, req)
		return rec
	}
	ok := post(url.Values{"old_password": {testPassword}, "new_password": {"new-password"}, "confirm_password": {"new-password"}})
	if ok.Code != http.StatusSeeOther {
		t.Fatalf("password change with totp columns set = %d", ok.Code)
	}
	row, _ := db.AuthUserByUsername(f.h, f.username)
	if !auth.VerifyPassword("new-password", row.PasswordHash) {
		t.Fatal("new password not stored")
	}
	if row.TOTPMode != "passwordless" || row.TOTPSecret != secret {
		t.Fatalf("password change must keep totp columns: %+v", row)
	}
}

func TestAccountTOTPRoutesGone(t *testing.T) {
	f := newFixture(t)
	addUser(t, f, f.username, testPassword)
	posts := []string{
		"/account/totp/enroll",
		"/account/totp/confirm",
		"/account/totp/disable",
		"/account/totp/mode",
		"/api/account/totp/enroll",
		"/api/account/totp/confirm",
		"/api/account/totp/disable",
	}
	for _, path := range posts {
		form := url.Values{auth.CSRFFieldName: {f.csrf}, "password": {testPassword}}
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set(auth.CSRFHeaderName, f.csrf)
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: f.sid})
		rec := httptest.NewRecorder()
		f.server.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("POST %s = %d, want 404", path, rec.Code)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/account/totp/qr", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: f.sid})
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, req)
	if strings.Contains(rec.Header().Get("Content-Type"), "image/png") {
		t.Fatal("GET /account/totp/qr must not serve a TOTP QR image")
	}
}
