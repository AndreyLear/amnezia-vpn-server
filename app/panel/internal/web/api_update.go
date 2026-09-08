// Обновление глазами панели (amnezia-vpn-server-8bt5, -tjoq).
//
// Панель наружу не ходит и хостом не распоряжается: она открыта в интернет по
// паролю, и чем меньше она умеет, тем меньше можно сделать, захватив её.
// Всё, что здесь есть, — это чтение файлов, которые пишет хост, и просьба,
// положенная в файл. Проверяет просьбу и выполняет её хостовой агент.
package web

import (
	"fmt"
	"net/http"
	"path/filepath"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/status"
)

type updateJSON struct {
	// Installed — версия этого развёртывания; Latest — последняя, о которой
	// известно хосту. Пусто там, где сказать нечего.
	Installed string `json:"installed"`
	Latest    string `json:"latest"`
	Available bool   `json:"available"`
	Notes     string `json:"notes"`

	// Про последнюю проверку: когда и чем кончилась. «Ни разу не
	// проверяли» и «проверяли, не вышло» — разные новости.
	CheckedAtUTC string `json:"checked_at_utc"`
	CheckResult  string `json:"check_result"`
	CheckReason  string `json:"check_reason"`

	// Что делает агент прямо сейчас или чем кончил. Итог лежит в файле и
	// дожидается: обновление перезапускает саму панель.
	State        string `json:"state"`
	StateFrom    string `json:"state_from"`
	StateTo      string `json:"state_to"`
	StateStep    string `json:"state_step"`
	StateMessage string `json:"state_message"`
	StateAtUTC   string `json:"state_at_utc"`

	// OutcomeSeen — время итога, который уже показали. Итог лежит в файле и
	// дожидается: обновление перезапускает саму панель, и браузер мог быть
	// закрыт всё это время.
	OutcomeSeen string `json:"outcome_seen"`

	// Какую версию владелец убрал с глаз крестиком. Хранится на сервере, а
	// не в браузере: владелец один и тот же на компьютере и на телефоне, и
	// закрытая полоса должна остаться закрытой в обоих
	// (amnezia-vpn-server-tjoq).
	Dismissed string `json:"dismissed"`
}

// dismissedSetting is where the closed banner is remembered. The value is
// the version it was closed for, not a flag: the next release must bring
// the banner back by itself.
const dismissedSetting = "update_banner_dismissed"

// outcomeSeenSetting запоминает, какой итог обновления человеку уже
// показали. Значение — время итога, а не флаг: следующее обновление
// закончится в другую секунду и покажется само (amnezia-vpn-server-tjoq).
const outcomeSeenSetting = "update_outcome_seen"

func (s *Server) statusDir() string {
	return filepath.Dir(s.cfg.StatusPath)
}

// dataDir is where the panel may write: its own volume. The host watches it
// for requests, which is the only channel from the panel to the host.
func (s *Server) dataDir() string {
	return filepath.Dir(s.cfg.DBPath)
}

func (s *Server) apiUpdate(w http.ResponseWriter, r *http.Request) {
	out := updateJSON{Installed: productVersion()}
	dir := s.statusDir()

	// Все записи между установленной версией и свежей, а не только
	// последняя: человек решает, стоит ли обновляться, по тому, что
	// изменится у него (amnezia-vpn-server-tjoq).
	if releases, err := status.ReadReleases(filepath.Join(dir, "update-latest.json")); err == nil {
		out.Latest, out.Notes = status.NotesSince(releases, out.Installed)
	}
	out.Available = status.IsNewer(out.Latest, out.Installed)

	if chk, err := status.ReadUpdateCheck(filepath.Join(dir, "update-check.json")); err == nil && chk != nil {
		out.CheckedAtUTC = chk.CheckedAtUTC
		out.CheckResult = chk.Result
		out.CheckReason = chk.Reason
	}
	if v, ok, err := db.GetSetting(s.db(), dismissedSetting); err == nil && ok {
		out.Dismissed = v
	}
	if v, ok, err := db.GetSetting(s.db(), outcomeSeenSetting); err == nil && ok {
		out.OutcomeSeen = v
	}
	if st, err := status.ReadUpdateState(filepath.Join(dir, "update-state.json")); err == nil && st != nil {
		out.State = st.State
		out.StateFrom = st.From
		out.StateTo = st.To
		out.StateStep = st.Step
		out.StateMessage = st.Message
		out.StateAtUTC = st.AtUTC
	}
	writeJSON(w, http.StatusOK, out)
}

// apiUpdateCheck asks the host to look now. The panel writes an empty file
// and nothing else: a systemd path unit wakes the same daily check.
func (s *Server) apiUpdateCheck(w http.ResponseWriter, r *http.Request) {
	path := filepath.Join(s.dataDir(), "update-check-request")
	if err := writeRequest(path, ""); err != nil {
		s.cfg.Logger.Printf("update check request: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "message": "Не удалось запросить проверку"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// writeRequest drops a request file atomically. Atomically because the host
// watches the path: a half-written request would be read as a whole one.
//
// Тем же способом, что и остальные производные файлы: tmp -> fsync ->
// close -> rename -> fsync каталога, 0600. Прежде здесь стояли WriteFile и
// Rename без fsync — то есть комментарий обещал больше, чем делал код, и
// следующий читатель на это обещание оперся бы (amnezia-vpn-server-i4m6).
func writeRequest(path, body string) error {
	if err := status.WriteAtomic(path, []byte(body)); err != nil {
		return fmt.Errorf("web: write request %s: %w", path, err)
	}
	return nil
}
