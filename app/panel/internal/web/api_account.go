package web

import (
	"net/http"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/auth"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

type accountPasswordReq struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
	Confirm     string `json:"confirm_password"`
	Code        string `json:"code"`
}

type accountTOTPReq struct {
	Password string `json:"password"`
	Code     string `json:"code"`
}

func decodeAccountJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	return decodeJSON(w, r, dst)
}

func accountJSONError(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "message": msg})
}

func (s *Server) apiChangePassword(w http.ResponseWriter, r *http.Request) {
	var req accountPasswordReq
	if !decodeAccountJSON(w, r, &req) {
		return
	}
	sess, _ := auth.CurrentUser(r.Context())
	u, err := db.AuthUserByUsername(s.db(), sess.Username)
	if err != nil || !auth.VerifyPassword(req.OldPassword, u.PasswordHash) || req.NewPassword != req.Confirm {
		accountJSONError(w, accountError)
		return
	}
	if req.OldPassword == req.NewPassword {
		accountJSONError(w, accountPasswordUnchanged)
		return
	}
	if totpLoginRequired(u) && !auth.VerifyTOTP(u.TOTPSecret, req.Code, time.Now()) {
		accountJSONError(w, accountError)
		return
	}
	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		accountJSONError(w, accountError)
		return
	}
	if err := db.UpdateAuthPassword(s.db(), sess.Username, u.PasswordHash, hash); err != nil {
		accountJSONError(w, accountError)
		return
	}
	if err := s.rotateCurrentSession(w, r); err != nil {
		internalFailure(w, r, s, "api password rotate", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "Пароль изменён."})
}

func (s *Server) apiTOTPEnroll(w http.ResponseWriter, r *http.Request) {
	var req accountTOTPReq
	if !decodeAccountJSON(w, r, &req) {
		return
	}
	sess, _ := auth.CurrentUser(r.Context())
	u, err := db.AuthUserByUsername(s.db(), sess.Username)
	if err != nil || !auth.VerifyPassword(req.Password, u.PasswordHash) || u.TOTPSecret != "" {
		accountJSONError(w, accountError)
		return
	}
	secret, err := auth.NewTOTPSecret()
	if err != nil {
		internalFailure(w, r, s, "api totp enroll", err)
		return
	}
	s.mutex.Lock()
	s.pendingTOTP[sess.Username] = secret
	s.mutex.Unlock()
	payload := otpAuthURL(sess.Username, secret)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"qr":      "/account/totp/qr",
		"otpauth": payload,
		"secret":  secret,
	})
}

func (s *Server) apiTOTPConfirm(w http.ResponseWriter, r *http.Request) {
	var req accountTOTPReq
	if !decodeAccountJSON(w, r, &req) {
		return
	}
	sess, _ := auth.CurrentUser(r.Context())
	u, err := db.AuthUserByUsername(s.db(), sess.Username)
	if err != nil || !auth.VerifyPassword(req.Password, u.PasswordHash) || u.TOTPSecret != "" {
		accountJSONError(w, accountError)
		return
	}
	s.mutex.Lock()
	secret := s.pendingTOTP[sess.Username]
	s.mutex.Unlock()
	if secret == "" || !auth.VerifyTOTP(secret, req.Code, time.Now()) {
		accountJSONError(w, accountError)
		return
	}
	if err := db.SetTotpSecret(s.db(), sess.Username, secret); err != nil {
		accountJSONError(w, accountError)
		return
	}
	if err := db.SetTotpMode(s.db(), sess.Username, "2fa"); err != nil {
		accountJSONError(w, accountError)
		return
	}
	s.mutex.Lock()
	delete(s.pendingTOTP, sess.Username)
	s.mutex.Unlock()
	if err := s.rotateCurrentSession(w, r); err != nil {
		internalFailure(w, r, s, "api totp confirm rotate", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "2FA включена."})
}

func (s *Server) apiTOTPDisable(w http.ResponseWriter, r *http.Request) {
	var req accountTOTPReq
	if !decodeAccountJSON(w, r, &req) {
		return
	}
	sess, _ := auth.CurrentUser(r.Context())
	u, err := db.AuthUserByUsername(s.db(), sess.Username)
	if err != nil || !auth.VerifyPassword(req.Password, u.PasswordHash) || !auth.VerifyTOTP(u.TOTPSecret, req.Code, time.Now()) {
		accountJSONError(w, accountError)
		return
	}
	if err := db.ClearTotpSecret(s.db(), sess.Username); err != nil {
		accountJSONError(w, accountError)
		return
	}
	_ = db.SetTotpMode(s.db(), sess.Username, "")
	if err := s.rotateCurrentSession(w, r); err != nil {
		internalFailure(w, r, s, "api totp disable rotate", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "2FA отключена."})
}
