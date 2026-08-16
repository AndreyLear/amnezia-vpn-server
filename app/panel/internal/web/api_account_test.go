package web

import (
	"net/http"
	"strings"
	"testing"

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

func TestAPIAccountPasswordIgnoresStoredTOTP(t *testing.T) {
	f := newFixture(t)
	addUser(t, f, f.username, testPassword)
	configureTOTPUser(t, f, f.username, "2fa")

	ok := f.apiCSRF(http.MethodPost, "/api/account/password", map[string]string{
		"old_password":     testPassword,
		"new_password":     "new-password",
		"confirm_password": "new-password",
	})
	if ok.Code != http.StatusOK {
		t.Fatalf("password change with totp_secret set: code = %d body=%s", ok.Code, ok.Body.String())
	}
}
