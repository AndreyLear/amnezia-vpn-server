package web

import (
	"net/http"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/auth"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

type accountPasswordReq struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
	Confirm     string `json:"confirm_password"`
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
