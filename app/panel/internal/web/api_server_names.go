package web

import (
	"errors"
	"net/http"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

// Имена серверов (amnezia-vpn-server-q1do.4, см. db/server_names.go).
//
// Владелец задаёт имя основному серверу и каждому каскаду в панели; шапка,
// «О версиях», полоса обновления каскада и окно клиента показывают его
// вместо подписи, которую раньше выбирали мы. Ответы — короткие фразы,
// которые окно показывает как есть; само имя в них не повторяется.

const (
	msgNameTooLong   = "Имя длиннее 32 символов"
	msgNameInvalid   = "В имени есть недопустимые символы"
	msgNameTaken     = "Такое имя уже у другого каскада"
	msgNameSaveFail  = "Не удалось сохранить имя"
	msgEntryNotFound = "Каскад не найден"
)

// nameChange — подробность для журнала: «было → стало», пустая сторона —
// «без имени». Пишется готовой строкой, как прочие подробности журнала.
func nameChange(old, next string) string {
	side := func(v string) string {
		if v == "" {
			return "без имени"
		}
		return "«" + v + "»"
	}
	return side(old) + " → " + side(next)
}

func nameFailure(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, db.ErrDisplayNameTooLong):
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "message": msgNameTooLong})
	case errors.Is(err, db.ErrDisplayNameInvalid):
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "message": msgNameInvalid})
	case errors.Is(err, db.ErrXrayEntryTitleTaken):
		writeJSON(w, http.StatusConflict, map[string]any{"ok": false, "message": msgNameTaken})
	case errors.Is(err, db.ErrXrayEntryNotFound):
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "message": msgEntryNotFound})
	default:
		return false
	}
	return true
}

// PUT /api/server/name {"name": "..."} — имя основного сервера; пустое —
// сброс.
func (s *Server) apiServerName(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	old, err := db.SetServerName(s.db(), req.Name)
	if err != nil {
		if nameFailure(w, err) {
			return
		}
		s.cfg.Logger.Printf("api server name: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "message": msgNameSaveFail})
		return
	}
	next, _ := db.NormalizeDisplayName(req.Name)
	if old != next {
		s.audit(r, auditServerRename, "", nameChange(old, next))
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": next})
}

// PUT /api/entries/{name}/title {"title": "..."} — имя каскада; пустое —
// сброс к «Каскад»/«Каскад <узел>». Техническое имя узла не меняется:
// секрет, синк, отчёт и запрос обновления держатся на нём.
func (s *Server) apiEntryTitle(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var req struct {
		Title string `json:"title"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	old, err := db.SetXrayEntryTitle(s.db(), name, req.Title)
	if err != nil {
		if nameFailure(w, err) {
			return
		}
		s.cfg.Logger.Printf("api entry title: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "message": msgNameSaveFail})
		return
	}
	next, _ := db.NormalizeDisplayName(req.Title)
	if old != next {
		s.audit(r, auditEntryRename, name, nameChange(old, next))
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "title": next})
}
