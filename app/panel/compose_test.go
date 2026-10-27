// M8.7 compose contract (AUDITS/M8.7_AUDIT.md): compose.yaml is the
// single source of the deployment topology. This test locks it down so
// topology changes are conscious:
//
//   - the exact mount matrix per service — including the M8.7 backups
//     mount ./backups:/data/backups (RW) on panel only;
//   - awg and panel-init never receive the backups mount;
//   - the pre-existing mounts stay byte-for-byte unchanged
//     (panel: /data, /config, /status:ro; panel-init: /data, /config;
//     awg: /config:ro, /status);
//   - docker.sock never appears in any mount;
//   - no inter-container HTTP: the only published port is the panel
//     loopback mapping 127.0.0.1:8787:8787;
//   - the awg privilege set (NET_ADMIN / cap_drop NET_RAW /
//     /dev/net/tun) stays unchanged.
//
// The parser is deliberately minimal and structural: it reads exactly
// the flat shape compose.yaml uses (2-space indent, `- item` lists).
// Any restructure of the file fails loudly here.
package main

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

type composeService struct {
	volumes []string
	ports   []string
	capAdd  []string
	capDrop []string
	devices []string
}

// composePath resolves the repository compose.yaml from the test file
// location (robust regardless of the go test working directory).
func composePath(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	p := filepath.Join(filepath.Dir(file), "..", "..", "compose.yaml")
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("compose.yaml not found at %s: %v", p, err)
	}
	return p
}

// parseCompose extracts the tracked lists (volumes/ports/cap_add/
// cap_drop/devices) from the flat compose.yaml shape. Comment lines
// and scalar key:value lines terminate the current list; list items
// are `- item` lines at the 6-space indent.
func parseCompose(t *testing.T, path string) map[string]*composeService {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	services := map[string]*composeService{}
	var cur string
	var key string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		switch {
		case indent == 0 && trim == "services:":
			cur, key = "", ""
		case indent == 2 && strings.HasSuffix(trim, ":"):
			name := strings.TrimSuffix(trim, ":")
			if services[name] == nil {
				services[name] = &composeService{}
			}
			cur, key = name, ""
		case indent == 4 && strings.HasSuffix(trim, ":") && cur != "":
			key = strings.TrimSuffix(trim, ":")
		case indent == 6 && strings.HasPrefix(trim, "- ") && cur != "":
			item := strings.Trim(strings.TrimPrefix(trim, "- "), `"`)
			s := services[cur]
			switch key {
			case "volumes":
				s.volumes = append(s.volumes, item)
			case "ports":
				s.ports = append(s.ports, item)
			case "cap_add":
				s.capAdd = append(s.capAdd, item)
			case "cap_drop":
				s.capDrop = append(s.capDrop, item)
			case "devices":
				s.devices = append(s.devices, item)
			}
		default:
			key = ""
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return services
}

func mustService(t *testing.T, services map[string]*composeService, name string) *composeService {
	t.Helper()
	s, ok := services[name]
	if !ok {
		t.Fatalf("compose.yaml has no %q service", name)
	}
	return s
}

func equal(t *testing.T, what string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s = %v, want %v", what, got, want)
		}
	}
}

// TestComposeMountMatrix: the exact per-service mount lists. The M8.7
// backups mount is shared by panel and panel-init (RW, plain entry
// without a mode suffix) — the M10.2-FIX makes CLI backup/restore see
// the same store as the web panel; anything else in this matrix is a
// deliberate topology change and must update this test.
func TestComposeMountMatrix(t *testing.T) {
	services := parseCompose(t, composePath(t))

	equal(t, "panel volumes", mustService(t, services, "panel").volumes, []string{
		"./data:/data",
		"./config:/config",
		"./status:/status:ro",
		"./backups:/data/backups",
		"/proc:/host/proc:ro",
	})
	equal(t, "panel-init volumes", mustService(t, services, "panel-init").volumes, []string{
		"./data:/data",
		"./config:/config",
		"./backups:/data/backups",
	})
	equal(t, "awg volumes", mustService(t, services, "awg").volumes, []string{
		"./config:/config:ro",
		"./status:/status",
	})
}

// TestComposeBackupsMountOnlyOnPanel: exactly two backups mounts in the
// whole topology — panel and panel-init, both RW — awg never sees it.
func TestComposeBackupsMountOnlyOnPanel(t *testing.T) {
	services := parseCompose(t, composePath(t))
	var mounts []string
	for name, s := range services {
		for _, v := range s.volumes {
			if strings.Contains(v, "backups") {
				mounts = append(mounts, name+": "+v)
			}
		}
	}
	sort.Strings(mounts)
	if len(mounts) != 2 ||
		mounts[0] != "panel-init: ./backups:/data/backups" ||
		mounts[1] != "panel: ./backups:/data/backups" {
		t.Fatalf("backups mounts = %v, want exactly [panel: ./backups:/data/backups, panel-init: ./backups:/data/backups]", mounts)
	}
	for _, m := range mounts {
		if strings.HasSuffix(m, ":ro") {
			t.Fatalf("backups mount must be RW, got %q", m)
		}
	}
}

// TestComposeNoDockerSocket: the panel never gains the docker socket
// (it restarts nothing by itself — ТЗ §5).
func TestComposeNoDockerSocket(t *testing.T) {
	services := parseCompose(t, composePath(t))
	for name, s := range services {
		for _, v := range s.volumes {
			if strings.Contains(v, "docker.sock") || strings.Contains(v, "docker") {
				t.Fatalf("%s mounts the docker socket: %q", name, v)
			}
		}
	}
}

// TestComposeNoInterContainerHTTP: the only published port is the
// panel loopback mapping; no service exposes a port reachable from
// another container's network (ТЗ §11).
func TestComposeNoInterContainerHTTP(t *testing.T) {
	services := parseCompose(t, composePath(t))
	for name, s := range services {
		if name == "panel" {
			equal(t, "panel ports", s.ports, []string{"127.0.0.1:8787:8787"})
			continue
		}
		if len(s.ports) != 0 {
			t.Fatalf("%s must not publish ports, got %v", name, s.ports)
		}
	}
}

// TestComposeAwgPrivilegesUnchanged: the awg privilege contract stays
// exactly as defined (M1/M5): NET_ADMIN only, NET_RAW dropped,
// /dev/net/tun device.
func TestComposeAwgPrivilegesUnchanged(t *testing.T) {
	services := parseCompose(t, composePath(t))
	awg := mustService(t, services, "awg")
	equal(t, "awg cap_add", awg.capAdd, []string{"NET_ADMIN"})
	equal(t, "awg cap_drop", awg.capDrop, []string{"NET_RAW"})
	equal(t, "awg devices", awg.devices, []string{"/dev/net/tun:/dev/net/tun"})
}

// TestComposeXrayService: Xray (VLESS+XHTTP+Reality, amnezia-vpn-server-14y0.2)
// видит только свой каталог конфига (там приватный ключ Reality, но нет ни
// ключей AWG, ни базы) и пишет журналы в status/, откуда их сможет читать
// панель; без прав, без опубликованных портов, в профиле xray — стек
// поднимается и без него.
func TestComposeXrayService(t *testing.T) {
	services := parseCompose(t, composePath(t))
	x := mustService(t, services, "xray")
	equal(t, "xray volumes", x.volumes, []string{
		"./config/xray:/etc/xray:ro",
		"./status/xray:/var/log/xray",
	})
	equal(t, "xray cap_drop", x.capDrop, []string{"ALL"})
	equal(t, "xray cap_add", x.capAdd, nil)
	equal(t, "xray devices", x.devices, nil)

	data, err := os.ReadFile(composePath(t))
	if err != nil {
		t.Fatal(err)
	}
	block := serviceBlock(string(data), "xray")
	for _, want := range []string{
		"image: ghcr.io/xtls/xray-core:${XRAY_VERSION:?XRAY_VERSION must be set via versions.lock}",
		`command: ["run", "-c", "/etc/xray/config.json"]`,
		"network_mode: host",
		`profiles: ["xray"]`,
		"condition: service_completed_successfully",
		"restart: unless-stopped",
		"no-new-privileges:true",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("xray: нет %q", want)
		}
	}

	lock, err := os.ReadFile(filepath.Join(filepath.Dir(composePath(t)), "versions.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(lock), "\nXRAY_VERSION=26.10.21\n") {
		t.Fatal("versions.lock: XRAY_VERSION не закреплён на 26.10.21")
	}
}

// serviceBlock — строки одного сервиса compose.yaml, до следующего
// сервиса (ключа на отступе 2).
func serviceBlock(compose, name string) string {
	var b strings.Builder
	in := false
	for _, line := range strings.Split(compose, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   ") && strings.HasSuffix(trim, ":") {
			in = trim == name+":"
			continue
		}
		if in {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	return b.String()
}
