package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/auth"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

func TestAPIAccountPasswordJSON(t *testing.T) {
	f := newFixture(t)
	addUser(t, f, f.username, testPassword)

	bad := f.apiCSRF(http.MethodPost, "/api/account/password", map[string]string{
		"old_password":     "wrong",
		"new_password":     "new-password",
		"confirm_password": "new-password",
	})
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("wrong old: code = %d body=%s", bad.Code, bad.Body.String())
	}
	got := decodeAPI(t, bad)
	if got["ok"] != false || got["message"] != accountError {
		t.Fatalf("wrong old body = %v", got)
	}
	if strings.Contains(bad.Body.String(), testPassword) {
		t.Fatal("password leaked in JSON")
	}

	same := f.apiCSRF(http.MethodPost, "/api/account/password", map[string]string{
		"old_password":     testPassword,
		"new_password":     testPassword,
		"confirm_password": testPassword,
	})
	if same.Code != http.StatusBadRequest {
		t.Fatalf("same password: code = %d", same.Code)
	}
	sameBody := decodeAPI(t, same)
	if sameBody["message"] != accountPasswordUnchanged {
		t.Fatalf("same password message = %v", sameBody["message"])
	}

	ok := f.apiCSRF(http.MethodPost, "/api/account/password", map[string]string{
		"old_password":     testPassword,
		"new_password":     "new-password",
		"confirm_password": "new-password",
	})
	if ok.Code != http.StatusOK {
		t.Fatalf("change: code = %d body=%s", ok.Code, ok.Body.String())
	}
	okBody := decodeAPI(t, ok)
	if okBody["ok"] != true {
		t.Fatalf("ok = %v", okBody)
	}
	row, _ := db.AuthUserByUsername(f.h, f.username)
	if !auth.VerifyPassword("new-password", row.PasswordHash) {
		t.Fatal("new password not stored")
	}
}

func TestAPIAccountPasswordRequiresTOTPCode(t *testing.T) {
	f := newFixture(t)
	addUser(t, f, f.username, testPassword)
	secret := configureTOTPUser(t, f, f.username, "2fa")

	missing := f.apiCSRF(http.MethodPost, "/api/account/password", map[string]string{
		"old_password":     testPassword,
		"new_password":     "new-password",
		"confirm_password": "new-password",
	})
	if missing.Code != http.StatusBadRequest {
		t.Fatalf("without code: code = %d body=%s", missing.Code, missing.Body.String())
	}

	code, err := auth.TOTPCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ok := f.apiCSRF(http.MethodPost, "/api/account/password", map[string]string{
		"old_password":     testPassword,
		"new_password":     "new-password",
		"confirm_password": "new-password",
		"code":             code,
	})
	if ok.Code != http.StatusOK {
		t.Fatalf("with code: code = %d body=%s", ok.Code, ok.Body.String())
	}
}

func TestAPITOTPEnrollJSONContract(t *testing.T) {
	f := newFixture(t)
	u := addUser(t, f, f.username, testPassword)

	rec := f.apiCSRF(http.MethodPost, "/api/account/totp/enroll", map[string]string{
		"password": testPassword,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("enroll: code = %d body=%s", rec.Code, rec.Body.String())
	}
	got := decodeAPI(t, rec)
	if got["ok"] != true {
		t.Fatalf("ok = %v", got)
	}
	qr, _ := got["qr"].(string)
	if qr != "/account/totp/qr" {
		t.Fatalf("qr = %q, want same-origin /account/totp/qr (not data:)", qr)
	}
	qrRec := httptest.NewRecorder()
	qrReq := httptest.NewRequest(http.MethodGet, qr, nil)
	f.serve(qrRec, qrReq)
	if qrRec.Code != http.StatusOK || !strings.Contains(qrRec.Header().Get("Content-Type"), "image/png") {
		t.Fatalf("GET %s: code=%d ct=%q", qr, qrRec.Code, qrRec.Header().Get("Content-Type"))
	}
	otpauth, _ := got["otpauth"].(string)
	if !strings.HasPrefix(otpauth, "otpauth://totp/") {
		t.Fatalf("otpauth = %q", otpauth)
	}
	secret, _ := got["secret"].(string)
	if secret == "" {
		t.Fatal("enroll must return pending secret (HTML contract)")
	}
	body := rec.Body.String()
	if strings.Contains(body, u.PasswordHash) || strings.Contains(body, testPassword) {
		t.Fatal("enroll JSON leaked password material")
	}
	row, _ := db.AuthUserByUsername(f.h, f.username)
	if row.TOTPSecret != "" || row.TOTPMode != "" {
		t.Fatalf("2FA enabled before confirm: %+v", row)
	}

	confirm := f.apiCSRF(http.MethodPost, "/api/account/totp/confirm", map[string]string{
		"password": testPassword,
		"code":     "000000",
	})
	if confirm.Code != http.StatusBadRequest {
		t.Fatalf("bad confirm: code = %d", confirm.Code)
	}
	totp, err := auth.TOTPCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ok := f.apiCSRF(http.MethodPost, "/api/account/totp/confirm", map[string]string{
		"password": testPassword,
		"code":     totp,
	})
	if ok.Code != http.StatusOK {
		t.Fatalf("confirm: code = %d body=%s", ok.Code, ok.Body.String())
	}
	row, _ = db.AuthUserByUsername(f.h, f.username)
	if row.TOTPSecret == "" || row.TOTPMode != "2fa" {
		t.Fatalf("after confirm: %+v", row)
	}
}

func TestAPITOTPDisableRequiresCode(t *testing.T) {
	f := newFixture(t)
	addUser(t, f, f.username, testPassword)
	secret := configureTOTPUser(t, f, f.username, "2fa")

	missing := f.apiCSRF(http.MethodPost, "/api/account/totp/disable", map[string]string{
		"password": testPassword,
	})
	if missing.Code != http.StatusBadRequest {
		t.Fatalf("disable without code: code = %d", missing.Code)
	}
	row, _ := db.AuthUserByUsername(f.h, f.username)
	if row.TOTPSecret != secret {
		t.Fatal("disable without code cleared TOTP")
	}

	code, err := auth.TOTPCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ok := f.apiCSRF(http.MethodPost, "/api/account/totp/disable", map[string]string{
		"password": testPassword,
		"code":     code,
	})
	if ok.Code != http.StatusOK {
		t.Fatalf("disable: code = %d body=%s", ok.Code, ok.Body.String())
	}
	row, _ = db.AuthUserByUsername(f.h, f.username)
	if row.TOTPSecret != "" {
		t.Fatal("TOTP still set after disable")
	}
}
