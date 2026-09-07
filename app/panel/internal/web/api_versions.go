package web

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/awgconf"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/status"
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
	Protocol       string `json:"protocol"`
	Schema         string `json:"schema"`
	TunnelIPv6     *bool  `json:"tunnel_ipv6"`
	TunnelDNS      *bool  `json:"tunnel_dns"`
	Watchdog       *bool  `json:"watchdog"`
	Fail2ban       *bool  `json:"fail2ban"`
	UpdateCheck    *bool  `json:"update_check"`
	OS             string `json:"os"`
	Docker         string `json:"docker"`
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
	out := versionsJSON{Product: productVersion()}
	dir := filepath.Dir(s.cfg.StatusPath)

	if v, err := status.ReadVersions(filepath.Join(dir, "versions.json")); err == nil && v != nil {
		out.AmneziaWGGo = v.AmneziaWGGo
		out.AmneziaWGTools = v.AmneziaWGTools
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
