package web

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/awgconf"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/status"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/xray"
)

// Что стоит на сервере (amnezia-vpn-server-rdcz, -8bt5).
//
// До этого панель не знала ни одной версии — ни своей, ни AmneziaWG, — и на
// вопрос «что у меня установлено» отвечал только SSH. Своя версия приходит
// переменной окружения из versions.lock (одно место, а не два), версии
// AmneziaWG — из файла, который пишет контейнер awg, а всё про хост — из
// файла, который пишет установщик: панель живёт в контейнере и про систему
// сервера знать не может.
//
// Неизвестное значение возвращается пустой строкой или null, а не
// выдумывается: панель на старом развёртывании честно скажет «неизвестно», и
// это правда, а не сбой.
type versionsJSON struct {
	Product        string `json:"product"`
	Latest         string `json:"latest"`
	AmneziaWGGo    string `json:"amneziawg_go"`
	AmneziaWGTools string `json:"amneziawg_tools"`
	// TunnelDriver и AmneziaWGModule — на чём работает туннель и версия
	// модуля ядра, если на нём (amnezia-vpn-server-y7bp).
	TunnelDriver    string `json:"tunnel_driver"`
	AmneziaWGModule string `json:"amneziawg_module"`
	Protocol        string `json:"protocol"`
	Schema          string `json:"schema"`
	TunnelIPv6      *bool  `json:"tunnel_ipv6"`
	TunnelDNS       *bool  `json:"tunnel_dns"`
	Watchdog        *bool  `json:"watchdog"`
	Fail2ban        *bool  `json:"fail2ban"`
	UpdateCheck     *bool  `json:"update_check"`
	OS              string `json:"os"`
	Docker          string `json:"docker"`
	// Xray (amnezia-vpn-server-14y0.16): версия ядра из versions.lock,
	// состояние VLESS — то же, что vless_state в /api/stats/host, и узлы-входы
	// с временем последнего синка. Окно прячет эти строки, когда
	// vless_state = none и узлов нет.
	Xray       string           `json:"xray"`
	VLESSState string           `json:"vless_state"`
	Entries    []entryAboutJSON `json:"entries"`
	// ServerName — имя этого сервера от владельца (amnezia-vpn-server-q1do.4).
	ServerName string `json:"server_name"`
}

// entryAboutJSON — узел-вход для окна «О версиях». Online считает сервер
// по своим часам: часы браузера могут отставать, и «нет связи» зависело бы
// от устройства, на котором открыта панель.
//
// Xray, Release и UptimeSeconds — из отчёта узла (amnezia-vpn-server-q1do.1).
// Версии — последние известные, сколько бы им ни было: при «нет связи»
// они и подсказывают, что обновлять. Аптайм — только из свежего отчёта:
// старый аптайм врёт.
type entryAboutJSON struct {
	Name string `json:"name"`
	// Title — имя каскада от владельца (amnezia-vpn-server-q1do.4).
	Title         string  `json:"title"`
	LastSync      *string `json:"last_sync_utc"`
	Online        bool    `json:"online"`
	Xray          string  `json:"xray"`
	Release       string  `json:"release"`
	UptimeSeconds *int64  `json:"uptime_seconds"`
	// Update — самообновление узла (amnezia-vpn-server-q1do.2).
	Update *entryUpdateJSON `json:"update"`
}

// entryOfflineAfter — после скольких минут без синка узел считается без
// связи (просьба владельца, amnezia-vpn-server-14y0.16): узел ходит раз в
// минуту, пять пропусков подряд — уже не случайность.
const entryOfflineAfter = 5 * time.Minute

// xrayVersion — версия Xray из versions.lock, которую compose передаёт
// панели; пусто — контейнер старше этой возможности.
func xrayVersion() string {
	return os.Getenv("AMNEZIA_XRAY_VERSION")
}

func entriesAbout(reports []db.XrayEntryReport, now time.Time) []entryAboutJSON {
	out := make([]entryAboutJSON, 0, len(reports))
	for _, s := range reports {
		e := entryAboutJSON{Name: s.Name, Title: s.Title}
		if !s.LastSync.IsZero() {
			v := s.LastSync.UTC().Format(time.RFC3339)
			e.LastSync = &v
			e.Online = now.Sub(s.LastSync) <= entryOfflineAfter
		}
		if report, ok := storedEntryReport(s.Report); ok {
			e.Xray, e.Release = report.XrayVersion, report.Release
			if entryFresh(s.ReportedAt, now) {
				e.UptimeSeconds = report.UptimeSeconds
			}
		}
		e.Update = entryUpdateOf(s, xray.EntryTargetVersion(productVersion()))
		out = append(out, e)
	}
	return out
}

// entryFresh — было ли событие узла не дольше entryOfflineAfter назад.
func entryFresh(t, now time.Time) bool {
	return !t.IsZero() && now.Sub(t) <= entryOfflineAfter
}

// storedEntryReport — отчёт узла из базы, снова проверенный: строка могла
// остаться от другой версии панели или быть поправлена руками. Пустая или
// нечитаемая — ok=false, и вместо значений прочерки.
func storedEntryReport(raw string) (xray.EntryReport, bool) {
	if raw == "" {
		return xray.EntryReport{}, false
	}
	var r xray.EntryReport
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		return xray.EntryReport{}, false
	}
	return r.Sanitized(), true
}

// productVersion is what this deployment runs; empty when the container
// predates AMNEZIA_VERSION or the value was not passed.
func productVersion() string {
	return os.Getenv("AMNEZIA_VERSION")
}

func boolPtr(v bool) *bool { return &v }

// tunnelProtocol names the obfuscation level from the parameters the server
// actually carries, not from a number stored anywhere. I1–I5 are what
// AmneziaWG 2.0 added; a tunnel generated before that has none of them, and
// saying "2.0" about it would be a claim we cannot back.
func tunnelProtocol(p *awgconf.Params) string {
	if p == nil {
		return ""
	}
	if p.I1 != "" || p.I2 != "" || p.I3 != "" || p.I4 != "" || p.I5 != "" {
		return "AmneziaWG 2.0"
	}
	if p.Jc != nil || p.S1 != nil || p.H1 != "" {
		return "AmneziaWG 1.5"
	}
	return ""
}

// dnsIsOurResolver says whether clients are pointed at the resolver inside
// the tunnel. What proves it is the address handed to clients, not a flag
// somebody could have forgotten to update: the resolver answers on the
// tunnel's own address and nowhere else.
func dnsIsOurResolver(dns, tunnelHost string) bool {
	if dns == "" || tunnelHost == "" {
		return false
	}
	for _, item := range strings.Split(dns, ",") {
		if strings.TrimSpace(item) == tunnelHost {
			return true
		}
	}
	return false
}

func (s *Server) apiVersions(w http.ResponseWriter, r *http.Request) {
	out := versionsJSON{
		Product:    productVersion(),
		Xray:       xrayVersion(),
		VLESSState: s.vlessStateJSON(),
		Entries:    []entryAboutJSON{},
	}
	if reports, err := db.XrayEntryReports(s.db()); err == nil {
		out.Entries = entriesAbout(reports, time.Now().UTC())
	}
	out.ServerName, _ = db.ServerName(s.db())
	dir := filepath.Dir(s.cfg.StatusPath)

	if v, err := status.ReadVersions(filepath.Join(dir, "versions.json")); err == nil && v != nil {
		out.AmneziaWGGo = v.AmneziaWGGo
		out.AmneziaWGTools = v.AmneziaWGTools
		out.TunnelDriver = v.TunnelDriver
		out.AmneziaWGModule = v.AmneziaWGModule
	}
	if releases, err := status.ReadReleases(filepath.Join(dir, "update-latest.json")); err == nil {
		for i := range releases {
			if v := releases[i].Version(); v != "" && (out.Latest == "" || status.IsNewer(v, out.Latest)) {
				out.Latest = v
			}
		}
	}
	if d, err := status.ReadDeployment(filepath.Join(dir, "deployment.json")); err == nil && d != nil {
		out.OS = d.OS
		out.Docker = d.Docker
		out.Watchdog = d.Watchdog
		out.Fail2ban = d.Fail2ban
		out.UpdateCheck = d.UpdateCheck
	}
	if stored, err := db.SchemaVersionStored(s.db()); err == nil {
		out.Schema = stored
	}
	if srv, err := db.ServerRow(s.db()); err == nil && srv != nil {
		// Туннель несёт IPv6 тогда и только тогда, когда у сервера есть
		// адрес в нём; резолвер — когда клиентам выдаётся адрес самого
		// туннеля. И то и другое видно по строке сервера, а не по флагу,
		// который кто-то мог забыть обновить.
		out.TunnelIPv6 = boolPtr(srv.Address6 != "")
		out.TunnelDNS = boolPtr(dnsIsOurResolver(srv.DNS, hostAddress(srv.Address)))
		if p, err := awgconf.ParseParams(srv.AWGParams); err == nil {
			out.Protocol = tunnelProtocol(p)
		}
	}
	writeJSON(w, http.StatusOK, out)
}
