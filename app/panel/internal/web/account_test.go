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
	if rec := post("/account/totp/enroll", url.Values{"password": {testPassword}}, f.sid); rec.Code != http.StatusSeeOther {
		t.Fatalf("enroll = %d", rec.Code)
	}
	row, _ := db.AuthUserByUsername(f.h, f.username)
	if row.TOTPSecret != "" {
		t.Fatal("secret saved before confirmation")
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/account/totp/qr", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: f.sid})
	f.server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "image/png") {
		t.Fatalf("totp qr: code=%d ct=%q", rec.Code, rec.Header().Get("Content-Type"))
	}
	secret := f.server.pendingTOTP[f.username]
	code, err := auth.TOTPCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if rec := post("/account/totp/confirm", url.Values{"password": {testPassword}, "code": {code}}, f.sid); rec.Code != http.StatusSeeOther {
		t.Fatalf("confirm = %d", rec.Code)
	}
	row, _ = db.AuthUserByUsername(f.h, f.username)
	if row.TOTPSecret == "" {
		t.Fatal("secret not saved after confirmation")
	}
	if row.TOTPMode != "2fa" {
		t.Fatalf("totp_mode after confirm = %q, want 2fa", row.TOTPMode)
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
	code, err := auth.TOTPCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	postMode("2fa", code)
	u, _ := db.AuthUserByUsername(f.h, f.username)
	if u.TOTPMode != "2fa" {
		t.Fatalf("mode after 2fa = %q", u.TOTPMode)
	}
	code, err = auth.TOTPCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	postMode("2fa", code)
	u, _ = db.AuthUserByUsername(f.h, f.username)
	if u.TOTPMode != "2fa" {
		t.Fatalf("mode after re-enable 2fa = %q", u.TOTPMode)
	}
	code, err = auth.TOTPCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	postMode("passwordless", code)
	u, _ = db.AuthUserByUsername(f.h, f.username)
	if u.TOTPMode != "2fa" {
		t.Fatalf("POST passwordless must store 2fa, got %q", u.TOTPMode)
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

func TestAccountTOTPEnrollConfirmRequirePassword(t *testing.T) {
	f := newFixture(t)
	addUser(t, f, f.username, testPassword)
	post := func(path string, values url.Values) *httptest.ResponseRecorder {
		values.Set(auth.CSRFFieldName, f.csrf)
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(values.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: f.sid})
		rec := httptest.NewRecorder()
		f.server.ServeHTTP(rec, req)
		return rec
	}
	enroll := post("/account/totp/enroll", url.Values{})
	if enroll.Code != http.StatusBadRequest || enroll.Body.String() != accountError+"\n" {
		t.Fatalf("enroll without password = %d %q", enroll.Code, enroll.Body.String())
	}
	if _, ok := f.server.pendingTOTP[f.username]; ok {
		t.Fatal("enroll without password must not store a pending secret")
	}
	if rec := post("/account/totp/enroll", url.Values{"password": {testPassword}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("enroll with password = %d", rec.Code)
	}
	secret := f.server.pendingTOTP[f.username]
	code, err := auth.TOTPCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	confirm := post("/account/totp/confirm", url.Values{"code": {code}})
	if confirm.Code != http.StatusBadRequest || confirm.Body.String() != accountError+"\n" {
		t.Fatalf("confirm without password = %d %q", confirm.Code, confirm.Body.String())
	}
	u, _ := db.AuthUserByUsername(f.h, f.username)
	if u.TOTPSecret != "" || u.TOTPMode != "" {
		t.Fatalf("confirm without password stored TOTP: %+v", u)
	}
}

func TestAccountTOTPEnrollRefusesExistingSecret(t *testing.T) {
	f := newFixture(t)
	addUser(t, f, f.username, testPassword)
	existing := configureTOTPUser(t, f, f.username, "2fa")
	form := url.Values{"password": {testPassword}, auth.CSRFFieldName: {f.csrf}}
	req := httptest.NewRequest(http.MethodPost, "/account/totp/enroll", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: f.sid})
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || rec.Body.String() != accountError+"\n" {
		t.Fatalf("enroll with existing secret = %d %q", rec.Code, rec.Body.String())
	}
	u, _ := db.AuthUserByUsername(f.h, f.username)
	if u.TOTPSecret != existing {
		t.Fatalf("existing secret replaced: %q", u.TOTPSecret)
	}
	if _, ok := f.server.pendingTOTP[f.username]; ok {
		t.Fatal("enroll must not stage a replacement secret")
	}

	pending, err := auth.NewTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	f.server.pendingTOTP[f.username] = pending
	code, err := auth.TOTPCode(pending, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	confirmForm := url.Values{"password": {testPassword}, "code": {code}, auth.CSRFFieldName: {f.csrf}}
	creq := httptest.NewRequest(http.MethodPost, "/account/totp/confirm", strings.NewReader(confirmForm.Encode()))
	creq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	creq.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: f.sid})
	crec := httptest.NewRecorder()
	f.server.ServeHTTP(crec, creq)
	if crec.Code != http.StatusBadRequest {
		t.Fatalf("confirm replace existing secret = %d", crec.Code)
	}
	u, _ = db.AuthUserByUsername(f.h, f.username)
	if u.TOTPSecret != existing {
		t.Fatalf("confirm replaced secret: %q", u.TOTPSecret)
	}
}

func TestAccountTOTPConfirmEnforcesLoginTOTP(t *testing.T) {
	f := newFixture(t)
	addUser(t, f, f.username, testPassword)
	post := func(path string, values url.Values) *httptest.ResponseRecorder {
		values.Set(auth.CSRFFieldName, f.csrf)
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(values.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: f.sid})
		rec := httptest.NewRecorder()
		f.server.ServeHTTP(rec, req)
		return rec
	}
	if rec := post("/account/totp/enroll", url.Values{"password": {testPassword}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("enroll = %d", rec.Code)
	}
	secret := f.server.pendingTOTP[f.username]
	code, err := auth.TOTPCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if rec := post("/account/totp/confirm", url.Values{"password": {testPassword}, "code": {code}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("confirm = %d", rec.Code)
	}
	u, _ := db.AuthUserByUsername(f.h, f.username)
	if u.TOTPMode != "2fa" || u.TOTPSecret == "" {
		t.Fatalf("after confirm: mode=%q secret empty=%t", u.TOTPMode, u.TOTPSecret == "")
	}
	login := httptest.NewRecorder()
	f.server.ServeHTTP(login, loginForm(t, f.username, testPassword))
	if login.Code != http.StatusOK || !strings.Contains(login.Body.String(), `name="code"`) {
		t.Fatalf("password-only login after confirm = %d %q", login.Code, login.Body.String())
	}
	if strings.Contains(login.Body.String(), "Неверный код.") {
		t.Fatal("password-only 2FA login must prompt for a code without a false error")
	}
	if sessionCookie(t, login) != nil {
		t.Fatal("password-only login must not issue a session after 2FA confirm")
	}
	totp, err := auth.TOTPCode(u.TOTPSecret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	okLogin := httptest.NewRecorder()
	f.server.ServeHTTP(okLogin, loginFields(t, url.Values{"username": {f.username}, "password": {testPassword}, "code": {totp}}))
	if okLogin.Code != http.StatusSeeOther || sessionCookie(t, okLogin) == nil {
		t.Fatalf("password+TOTP login after confirm = %d", okLogin.Code)
	}
}

func TestAccountPasswordChangeLegacyPasswordlessRequiresCode(t *testing.T) {
	f := newFixture(t)
	addUser(t, f, f.username, testPassword)
	secret := seedLegacyPasswordless(t, f, f.username)
	assertSPA(t, f.get("/account"))
	body := f.get("/").Body.String()
	if strings.Contains(body, "Только код") || strings.Contains(body, `value="passwordless"`) {
		t.Fatal("SPA must not offer passwordless as a selectable mode")
	}
	post := func(values url.Values) *httptest.ResponseRecorder {
		values.Set(auth.CSRFFieldName, f.csrf)
		req := httptest.NewRequest(http.MethodPost, "/account/password", strings.NewReader(values.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: f.sid})
		rec := httptest.NewRecorder()
		f.server.ServeHTTP(rec, req)
		return rec
	}
	missing := post(url.Values{"old_password": {testPassword}, "new_password": {"new-password"}, "confirm_password": {"new-password"}})
	if missing.Code != http.StatusBadRequest {
		t.Fatalf("legacy passwordless change without code = %d", missing.Code)
	}
	code, err := auth.TOTPCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ok := post(url.Values{"old_password": {testPassword}, "new_password": {"new-password"}, "confirm_password": {"new-password"}, "code": {code}})
	if ok.Code != http.StatusSeeOther {
		t.Fatalf("legacy passwordless change with code = %d", ok.Code)
	}
	row, _ := db.AuthUserByUsername(f.h, f.username)
	if !auth.VerifyPassword("new-password", row.PasswordHash) {
		t.Fatal("legacy passwordless change with code did not store new password")
	}
	if row.TOTPMode != "passwordless" || row.TOTPSecret != secret {
		t.Fatalf("password change must keep the legacy secret: %+v", row)
	}
}

func TestAccountTOTPModeRequiresCodeWhenEnabling2FA(t *testing.T) {
	f := newFixture(t)
	addUser(t, f, f.username, testPassword)
	configureTOTPUser(t, f, f.username, "")
	form := url.Values{"mode": {"2fa"}, "password": {testPassword}, auth.CSRFFieldName: {f.csrf}}
	req := httptest.NewRequest(http.MethodPost, "/account/totp/mode", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: f.sid})
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("enable 2fa without TOTP = %d", rec.Code)
	}
	u, _ := db.AuthUserByUsername(f.h, f.username)
	if u.TOTPMode != "" {
		t.Fatalf("mode changed without TOTP: %q", u.TOTPMode)
	}
}
