package web

import (
	"net/http"
	"net/url"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/auth"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

const accountError = "Операция не выполнена."
const accountPasswordUnchanged = "Новый пароль должен отличаться от старого."

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	sess, _ := auth.CurrentUser(r.Context())
	old, newp, confirm := r.PostForm.Get("old_password"), r.PostForm.Get("new_password"), r.PostForm.Get("confirm_password")
	u, err := db.AuthUserByUsername(s.db(), sess.Username)
	if err != nil || !auth.VerifyPassword(old, u.PasswordHash) || newp != confirm {
		s.redirectAccountError(w)
		return
	}
	if old == newp {
		http.Error(w, accountPasswordUnchanged, http.StatusBadRequest)
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

func (s *Server) rotateCurrentSession(w http.ResponseWriter, r *http.Request) error {
	sess, _ := auth.CurrentUser(r.Context())
	next, err := s.cfg.Sessions.Rotate(sess.ID, sess.Username)
	if err != nil {
		return err
	}
	s.cfg.Sessions.DeleteByUsername(sess.Username, next.ID)
	auth.WriteSessionCookie(w, next.ID, next.ExpiresAt)
	return nil
}

func (s *Server) redirectAccountError(w http.ResponseWriter) {
	http.Error(w, accountError, http.StatusBadRequest)
}
