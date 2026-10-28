package web

import (
	"errors"
	"net/http"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/status"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/xray"
)

// Самообновление каскада в панели (amnezia-vpn-server-q1do.2, см.
// internal/xray/entry_update.go).
//
// Узел сам догоняет версию панели: она приходит ему в ответе синка. Панель
// показывает, что из этого вышло, — в шапке страницы (полоса рядом с
// обновлением этого сервера) и в окне «О версиях», — и даёт владельцу
// кнопку «Обновить каскад»: она пропускает паузу, которую узел выдерживает
// после неудачи, и оставляет запись в журнале.

// entryUpdateJSON — самообновление узла для панели. Всё — по последнему
// известному отчёту, сколько бы ему ни было: при «нет связи» он и
// подсказывает, на чём узел остановился.
//   - supported — узел сообщает о самообновлении; нет — его entry-sync
//     старше, и обновить его можно только вручную;
//   - available — выпуск узла ниже версии панели;
//   - requested — владелец попросил, а узел запрос ещё не забрал.
type entryUpdateJSON struct {
	Supported bool   `json:"supported"`
	State     string `json:"state"`
	Target    string `json:"target"`
	Reason    string `json:"reason"`
	AtUTC     string `json:"at_utc"`
	Release   string `json:"release"`
	Panel     string `json:"panel"`
	Available bool   `json:"available"`
	Requested bool   `json:"requested"`
}

func entryUpdateOf(r db.XrayEntryReport, panel string) *entryUpdateJSON {
	out := &entryUpdateJSON{Panel: panel}
	report, ok := storedEntryReport(r.Report)
	if !ok {
		return out
	}
	out.Release = report.Release
	out.Available = panel != "" && status.IsNewer(panel, report.Release)
	if u := report.Update; u != nil {
		out.Supported = true
		out.State, out.Target, out.Reason, out.AtUTC = u.State, u.Target, u.Reason, u.AtUTC
		out.Requested = out.Available && r.UpdateRequest != "" && r.UpdateRequest != u.Request
	}
	return out
}

// POST /api/entries/{name}/update — владелец просит обновить каскад до
// версии панели. Ответы — те же короткие фразы, что тост покажет как есть.
func (s *Server) apiEntryUpdate(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	fail := func(code int, msg string) {
		writeJSON(w, code, map[string]any{"ok": false, "message": msg})
	}
	if _, err := db.XrayEntryByName(s.db(), name); errors.Is(err, db.ErrXrayEntryNotFound) {
		fail(http.StatusNotFound, "Каскад не найден")
		return
	} else if err != nil {
		internalFailure(w, r, s, "api entry update", err)
		return
	}
	panel := xray.EntryTargetVersion(productVersion())
	if panel == "" {
		fail(http.StatusBadRequest, "Версия панели неизвестна")
		return
	}
	reports, err := db.XrayEntryReports(s.db())
	if err != nil {
		internalFailure(w, r, s, "api entry update", err)
		return
	}
	var view *entryUpdateJSON
	for _, rep := range reports {
		if rep.Name == name {
			view = entryUpdateOf(rep, panel)
		}
	}
	switch {
	case view == nil || !view.Supported:
		fail(http.StatusConflict, "Этот каскад не умеет обновляться сам. Обновите его вручную")
		return
	case view.State == "updating":
		fail(http.StatusConflict, "Обновление каскада уже идёт")
		return
	case view.Release != "" && !view.Available:
		fail(http.StatusConflict, "Каскад уже на версии панели")
		return
	}
	if err := db.RequestXrayEntryUpdate(s.db(), name, time.Now()); errors.Is(err, db.ErrXrayEntryNotFound) {
		fail(http.StatusNotFound, "Каскад не найден")
		return
	} else if err != nil {
		s.cfg.Logger.Printf("api entry update: %v", err)
		fail(http.StatusInternalServerError, "Не удалось запросить обновление каскада")
		return
	}
	s.audit(r, auditEntryUpdate, name, "До версии "+panel)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
