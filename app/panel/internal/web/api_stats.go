package web

import (
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/hostmetrics"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/status"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/xray"
)

type hostStatsResponse struct {
	CPUPercent     *float64 `json:"cpu_percent"`
	RAMPercent     *float64 `json:"ram_percent"`
	DiskPercent    *float64 `json:"disk_percent"`
	RAMUsedBytes   *int64   `json:"ram_used_bytes"`
	RAMTotalBytes  *int64   `json:"ram_total_bytes"`
	DiskUsedBytes  *int64   `json:"disk_used_bytes"`
	DiskTotalBytes *int64   `json:"disk_total_bytes"`
	Iface          string   `json:"iface"`
	IfaceState     string   `json:"iface_state"`
	// VLESSState — состояние Xray для шапки (amnezia-vpn-server-45ds):
	// up/down — ответил ли он на последний опрос, na — опроса ещё не было,
	// none — VLESS не настроен, и шапка его не показывает.
	VLESSState string `json:"vless_state"`
	// Entries — узлы-входы для строк «Каскад» в шапке
	// (amnezia-vpn-server-q1do.1); пустой список — узлов нет.
	Entries []entryHostJSON `json:"entries"`
	// ServerName — имя этого сервера от владельца (amnezia-vpn-server-q1do.4);
	// пусто — строка шапки без подписи, как раньше.
	ServerName string `json:"server_name"`
}

// entryHostJSON — узел-вход в шапке: те же CPU, память и диск, что у
// этого сервера, из последнего отчёта узла. Состояние считает сервер по
// своим часам, как online в окне «О версиях»:
//   - state up — узел синхронизировался или прислал отчёт за последние
//     entryOfflineAfter, down — нет (или не было ни разу);
//   - reporting — отчёт свежий, и числа взяты из него; up без reporting —
//     узел со старым entry-sync, который отчётов не шлёт;
//   - seen — был ли узел на связи хоть раз: «не выходил больше 5 минут» и
//     «ни разу» — разные подсказки.
//
// Числа несвежего отчёта не отдаются: шапка показала бы прочерк вместо
// нагрузки пятиминутной давности, выдающей себя за текущую.
type entryHostJSON struct {
	Name string `json:"name"`
	// Title — имя каскада от владельца (amnezia-vpn-server-q1do.4); пусто —
	// подпись по умолчанию.
	Title     string `json:"title"`
	State     string `json:"state"`
	Reporting bool   `json:"reporting"`
	Seen      bool   `json:"seen"`
	xray.EntryReport
	// Update — самообновление узла (amnezia-vpn-server-q1do.2) по
	// последнему отчёту, а не только свежему: полоса «Обновить каскад»
	// не должна пропадать от одного пропущенного синка. Поле отчёта с тем
	// же именем обнуляется, чтобы в ответе было одно «update».
	Update *entryUpdateJSON `json:"update"`
}

func entriesHost(reports []db.XrayEntryReport, now time.Time) []entryHostJSON {
	out := make([]entryHostJSON, 0, len(reports))
	for _, r := range reports {
		e := entryHostJSON{
			Name:      r.Name,
			Title:     r.Title,
			State:     "down",
			Reporting: entryFresh(r.ReportedAt, now),
			Seen:      !r.LastSync.IsZero() || !r.ReportedAt.IsZero(),
		}
		if e.Reporting || entryFresh(r.LastSync, now) {
			e.State = "up"
		}
		if e.Reporting {
			e.EntryReport, _ = storedEntryReport(r.Report)
			e.EntryReport.Update = nil
		}
		e.Update = entryUpdateOf(r, xray.EntryTargetVersion(productVersion()))
		out = append(out, e)
	}
	return out
}

// vlessStateJSON — состояние VLESS для шапки. Настроен ли Xray, читается
// из базы на каждый запрос, а не берётся из опроса: опрос идёт раз в
// пять секунд и после восстановления копии или настройки VLESS отстал бы.
func (s *Server) vlessStateJSON() string {
	if _, err := db.XraySettingsRow(s.db()); err != nil {
		if errors.Is(err, db.ErrXrayNotConfigured) {
			return "none"
		}
		return "na"
	}
	switch s.xrayAPI.Load() {
	case xrayAPIUp:
		return "up"
	case xrayAPIDown:
		return "down"
	default:
		return "na"
	}
}

func ifaceNameFromConf(confPath string) string {
	name := strings.TrimSuffix(filepath.Base(confPath), ".conf")
	if name == "" || name == "." {
		return "awg0"
	}
	return name
}

func ifaceStateJSON(state InterfaceState) string {
	switch state {
	case IfaceUp:
		return "up"
	case IfaceNA:
		return "na"
	case IfaceDown:
		return "down"
	case IfaceError:
		return "error"
	default:
		return "na"
	}
}

// hostCPUPeriod is the window of the CPU figure; tests shorten it.
var hostCPUPeriod = hostmetrics.CPUPeriod

func (s *Server) apiStatsHost(w http.ResponseWriter, r *http.Request) {
	// CPU is measured in the background over fixed windows; a request only
	// reads the last figure, so several open tabs do not shrink the window
	// to milliseconds (amnezia-vpn-server-76mp.23).
	snap := hostmetrics.ReadMemDisk(s.cfg.HostProcDir, s.cfg.HostDiskPath)
	snap.CPU = hostmetrics.SharedCPUMeter(s.cfg.HostProcDir, hostCPUPeriod).Percent()

	st, readErr := status.ReadStatus(s.cfg.StatusPath)
	rec := Reconcile(nil, st, readErr, time.Now().UTC())
	iface := ifaceNameFromConf(s.cfg.ConfPath)
	if rec.Interface == IfaceUp && st != nil && st.Interface != nil && st.Interface.Iface != "" {
		iface = st.Interface.Iface
	}

	// Узлов не прочитать — шапка без строк каскада, а не без всего.
	entries := []entryHostJSON{}
	if reports, err := db.XrayEntryReports(s.db()); err == nil {
		entries = entriesHost(reports, time.Now().UTC())
	}

	// Имени не прочитать — строка без подписи, как без имени.
	serverName, _ := db.ServerName(s.db())

	writeJSON(w, http.StatusOK, hostStatsResponse{
		CPUPercent:     snap.CPU,
		RAMPercent:     snap.RAM,
		DiskPercent:    snap.Disk,
		RAMUsedBytes:   snap.RAMUsedBytes,
		RAMTotalBytes:  snap.RAMTotalBytes,
		DiskUsedBytes:  snap.DiskUsedBytes,
		DiskTotalBytes: snap.DiskTotalBytes,
		Iface:          iface,
		IfaceState:     ifaceStateJSON(rec.Interface),
		VLESSState:     s.vlessStateJSON(),
		Entries:        entries,
		ServerName:     serverName,
	})
}
