package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/status"
)

// Отметка «спрашивает имена мимо туннеля» (amnezia-vpn-server-g0vd).
// Панель не чинит этот случай — со стороны сервера он не чинится вовсе, —
// а показывает владельцу, что запросов от клиента нет.
func writeDNSSeen(t *testing.T, statusPath string, addrs ...string) {
	t.Helper()
	seen := status.DNSSeen{
		Schema:      "v1",
		GeneratedAt: time.Now().UTC(),
		Addresses:   addrs,
	}
	data, err := json.Marshal(seen)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(filepath.Dir(statusPath), "dns-seen.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDNSBypassFlag(t *testing.T) {
	now := time.Now()
	fresh := now.Add(-time.Second)

	cases := []struct {
		name      string
		seen      []string
		online    bool
		rx        uint64
		hasReport bool
		want      bool
	}{
		{
			name: "спрашивает у нас — отметки нет",
			seen: []string{"10.8.0.2"}, online: true, rx: 50 << 20, hasReport: true, want: false,
		},
		{
			name: "есть трафик, запросов нет — отметка",
			seen: []string{"10.8.0.99"}, online: true, rx: 50 << 20, hasReport: true, want: true,
		},
		{
			name: "не на связи — сказать нечего",
			seen: nil, online: false, rx: 50 << 20, hasReport: true, want: false,
		},
		{
			name: "только подключился, ничего не передал — не обход",
			seen: nil, online: true, rx: 1024, hasReport: true, want: false,
		},
		{
			name: "снимка нет вовсе — панель молчит, а не обвиняет",
			seen: nil, online: true, rx: 50 << 20, hasReport: false, want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			c, _, _ := f.addClient("dns-probe")
			if tc.hasReport {
				writeDNSSeen(t, f.statusPath, tc.seen...)
			}
			var handshake *time.Time
			if tc.online {
				handshake = &fresh
			}
			f.setStatus(&status.Status{
				Schema:      status.SchemaVersion,
				GeneratedAt: time.Now().UTC(),
				Interface: &status.Interface{
					Iface: "awg0", HasInterface: true, ListenPort: 51820, FWMark: "off",
				},
				Peers: []status.Peer{{
					PublicKey:        c.PublicKey,
					LastHandshakeUTC: handshake,
					RxBytes:          tc.rx,
				}},
			})

			rec := f.apiCSRF(http.MethodGet, fmt.Sprintf("/api/clients/%d", c.ID), nil)
			got := decodeAPI(t, rec)
			if got["dns_bypass"] != tc.want {
				t.Fatalf("dns_bypass = %v, ожидалось %v (ответ: %v)", got["dns_bypass"], tc.want, got)
			}
		})
	}
}
