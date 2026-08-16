package web

import (
	"net/http"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/auth"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/backup"
)

type loginReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) apiLogin(w http.ResponseWriter, r *http.Request) {
	if s.rejectLimitedLogin(w, r) {
		return
	}
	var req loginReq
	if !decodeJSON(w, r, &req) {
		return
	}
	outcome, err := s.evaluateLogin(r, req.Username, req.Password)
	if err != nil {
		internalFailure(w, r, s, "api login: read user", err)
		return
	}
	if outcome.message != "" {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "message": outcome.message})
		return
	}
	if err := s.issueLoginSession(w, r, req.Username); err != nil {
		internalFailure(w, r, s, "api login: create session", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) apiMe(w http.ResponseWriter, r *http.Request) {
	sess, _ := auth.CurrentUser(r.Context())
	pending := false
	if _, ok, err := backup.PendingPath(s.cfg.DBPath); err == nil {
		pending = ok
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"username":        sess.Username,
		"csrf":            sess.CSRFToken,
		"restore_pending": pending,
	})
}

func (s *Server) apiLogout(w http.ResponseWriter, r *http.Request) {
	if sid, ok := auth.ReadSessionID(r); ok {
		s.cfg.Sessions.Delete(sid)
	}
	auth.ClearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
