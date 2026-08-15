package web

import (
	"net/http"
	"net/url"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/auth"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
	"github.com/skip2/go-qrcode"
)

const accountError = "Операция не выполнена."

type accountData struct {
	CSRF, Username, Mode, Secret, OTPAuth, QR, Error, Flash string
	HasTOTP                                                 bool
}

func (s *Server) accountPage(w http.ResponseWriter, r *http.Request) {
	sess, _ := auth.CurrentUser(r.Context())
	u, err := db.AuthUserByUsername(s.db(), sess.Username)
	if err != nil {
		s.errorPage(w, http.StatusInternalServerError)
		return
	}
	s.mutex.Lock()
	secret := s.pendingTOTP[sess.Username]
	s.mutex.Unlock()
	payload := otpAuthURL(sess.Username, secret)
	qr := ""
	if payload != "" {
		qr = "/account/totp/qr"
	}
	s.renderAccount(w, accountData{CSRF: sess.CSRFToken, Username: sess.Username, Mode: u.TOTPMode, HasTOTP: u.TOTPSecret != "", Secret: secret, OTPAuth: payload, QR: qr, Flash: r.URL.Query().Get("msg")})
}

func (s *Server) totpQR(w http.ResponseWriter, r *http.Request) {
	sess, _ := auth.CurrentUser(r.Context())
	s.mutex.Lock()
	secret := s.pendingTOTP[sess.Username]
	s.mutex.Unlock()
	payload := otpAuthURL(sess.Username, secret)
	png, err := qrcode.Encode(payload, qrcode.Medium, 256)
	if err != nil {
		s.errorPage(w, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Write(png)
}

func (s *Server) renderAccount(w http.ResponseWriter, d accountData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tpl.ExecuteTemplate(w, "account", d); err != nil {
		s.cfg.Logger.Printf("render account: %v", err)
	}
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	sess, _ := auth.CurrentUser(r.Context())
	old, newp, confirm := r.PostForm.Get("old_password"), r.PostForm.Get("new_password"), r.PostForm.Get("confirm_password")
	u, err := db.AuthUserByUsername(s.db(), sess.Username)
	if err != nil || !auth.VerifyPassword(old, u.PasswordHash) || newp != confirm {
		s.redirectAccountError(w)
		return
	}
	if totpLoginRequired(u) && !auth.VerifyTOTP(u.TOTPSecret, r.PostForm.Get("code"), time.Now()) {
		s.redirectAccountError(w)
		return
	}
	hash, err := auth.HashPassword(newp)
	if err != nil {
		s.redirectAccountError(w)
		return
	}
	if err := db.UpdateAuthPassword(s.db(), sess.Username, u.PasswordHash, hash); err != nil {
		s.redirectAccountError(w)
		return
	}
	rotated, err := s.cfg.Sessions.Rotate(sess.ID, sess.Username)
	if err != nil {
		s.redirectAccountError(w)
		return
	}
	s.cfg.Sessions.DeleteByUsername(sess.Username, rotated.ID)
	auth.WriteSessionCookie(w, rotated.ID, rotated.ExpiresAt)
	redirect303(w, r, "/account?msg="+url.QueryEscape("Пароль изменён."))
}

func (s *Server) totpEnroll(w http.ResponseWriter, r *http.Request) {
	sess, _ := auth.CurrentUser(r.Context())
	u, err := db.AuthUserByUsername(s.db(), sess.Username)
	if err != nil || !auth.VerifyPassword(r.PostForm.Get("password"), u.PasswordHash) || u.TOTPSecret != "" {
		s.redirectAccountError(w)
		return
	}
	secret, err := auth.NewTOTPSecret()
	if err != nil {
		s.errorPage(w, 500)
		return
	}
	s.mutex.Lock()
	s.pendingTOTP[sess.Username] = secret
	s.mutex.Unlock()
	redirect303(w, r, "/account")
}

func (s *Server) totpConfirm(w http.ResponseWriter, r *http.Request) {
	sess, _ := auth.CurrentUser(r.Context())
	u, err := db.AuthUserByUsername(s.db(), sess.Username)
	if err != nil || !auth.VerifyPassword(r.PostForm.Get("password"), u.PasswordHash) || u.TOTPSecret != "" {
		s.redirectAccountError(w)
		return
	}
	s.mutex.Lock()
	secret := s.pendingTOTP[sess.Username]
	s.mutex.Unlock()
	if secret == "" || !auth.VerifyTOTP(secret, r.PostForm.Get("code"), time.Now()) {
		s.redirectAccountError(w)
		return
	}
	if err := db.SetTotpSecret(s.db(), sess.Username, secret); err != nil {
		s.redirectAccountError(w)
		return
	}
	if err := db.SetTotpMode(s.db(), sess.Username, "2fa"); err != nil {
		s.redirectAccountError(w)
		return
	}
	s.mutex.Lock()
	delete(s.pendingTOTP, sess.Username)
	s.mutex.Unlock()
	s.rotateAccount(w, r, "2FA включена.")
}

func (s *Server) totpDisable(w http.ResponseWriter, r *http.Request) {
	sess, _ := auth.CurrentUser(r.Context())
	u, err := db.AuthUserByUsername(s.db(), sess.Username)
	if err != nil || !auth.VerifyPassword(r.PostForm.Get("password"), u.PasswordHash) || !auth.VerifyTOTP(u.TOTPSecret, r.PostForm.Get("code"), time.Now()) {
		s.redirectAccountError(w)
		return
	}
	if err := db.ClearTotpSecret(s.db(), sess.Username); err != nil {
		s.redirectAccountError(w)
		return
	}
	_ = db.SetTotpMode(s.db(), sess.Username, "")
	s.rotateAccount(w, r, "2FA отключена.")
}

func (s *Server) totpMode(w http.ResponseWriter, r *http.Request) {
	sess, _ := auth.CurrentUser(r.Context())
	u, err := db.AuthUserByUsername(s.db(), sess.Username)
	mode := r.PostForm.Get("mode")
	if mode == "passwordless" {
		mode = "2fa"
	}
	needTOTP := u.TOTPSecret != "" && (u.TOTPMode != "" || mode == "2fa")
	if err != nil || u.TOTPSecret == "" || !auth.VerifyPassword(r.PostForm.Get("password"), u.PasswordHash) || (needTOTP && !auth.VerifyTOTP(u.TOTPSecret, r.PostForm.Get("code"), time.Now())) {
		s.redirectAccountError(w)
		return
	}
	if err := db.SetTotpMode(s.db(), sess.Username, mode); err != nil {
		s.redirectAccountError(w)
		return
	}
	s.rotateAccount(w, r, "Способ входа изменён.")
}

func (s *Server) rotateAccount(w http.ResponseWriter, r *http.Request, msg string) {
	sess, _ := auth.CurrentUser(r.Context())
	next, err := s.cfg.Sessions.Rotate(sess.ID, sess.Username)
	if err != nil {
		s.errorPage(w, 500)
		return
	}
	s.cfg.Sessions.DeleteByUsername(sess.Username, next.ID)
	auth.WriteSessionCookie(w, next.ID, next.ExpiresAt)
	redirect303(w, r, "/account?msg="+url.QueryEscape(msg))
}
func (s *Server) redirectAccountError(w http.ResponseWriter) {
	http.Error(w, accountError, http.StatusBadRequest)
}
func otpAuthURL(user, secret string) string {
	if secret == "" {
		return ""
	}
	return "otpauth://totp/AmneziaVPN:" + url.PathEscape(user) + "?secret=" + secret + "&issuer=AmneziaVPN"
}
