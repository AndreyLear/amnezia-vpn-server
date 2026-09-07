// Кнопка «Обновить» и крестик на полосе (amnezia-vpn-server-tjoq).
//
// Панель хостом не распоряжается: она кладёт просьбу в свой том данных, а
// проверяет её и выполняет хостовой агент. Здесь только те отказы, которые
// человеку лучше услышать сразу, а не через минуту молчания.
package web

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/status"
)

// apiUpdateStart asks the host to install a release. The panel refuses the
// obviously pointless cases so a person gets an answer instead of silence;
// the agent checks everything again anyway, because the panel is on the
// public internet behind a password and its word is not an order.
func (s *Server) apiUpdateStart(w http.ResponseWriter, r *http.Request) {
	dir := s.statusDir()
	installed := productVersion()

	rel, err := status.ReadRelease(filepath.Join(dir, "update-latest.json"))
	if err != nil || rel == nil || !status.IsNewer(rel.Version(), installed) {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok": false, "message": "Обновляться не на что",
		})
		return
	}
	// Два установщика в одном каталоге подерутся. Агент откажет и сам, но
	// человеку лучше услышать это сразу, а не через минуту молчания.
	if st, err := status.ReadUpdateState(filepath.Join(dir, "update-state.json")); err == nil && st != nil && st.State == "running" {
		writeJSON(w, http.StatusConflict, map[string]any{
			"ok": false, "message": "Обновление уже идёт",
		})
		return
	}

	body := fmt.Sprintf(
		`{"schema":"v1","version":%q,"requested_at_utc":%q}`+"\n",
		rel.Version(), time.Now().UTC().Format(time.RFC3339),
	)
	if err := writeRequest(filepath.Join(s.dataDir(), "update-request.json"), body); err != nil {
		s.cfg.Logger.Printf("update request: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"ok": false, "message": "Не удалось запросить обновление",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// apiUpdateDismiss hides the banner until the next release. Stored on the
// server: the same owner opens the panel from a laptop and from a phone,
// and closing it in one place has to hold in the other.
func (s *Server) apiUpdateDismiss(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Version string `json:"version"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	version := strings.TrimSpace(req.Version)
	if version == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok": false, "message": "Не указана версия",
		})
		return
	}
	if err := db.SetSetting(s.db(), dismissedSetting, version); err != nil {
		s.cfg.Logger.Printf("dismiss banner: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"ok": false, "message": "Не удалось сохранить",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
