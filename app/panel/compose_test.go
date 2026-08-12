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
// backups mount is present on panel only and must be RW (the plain
// entry without a mode suffix); anything else in this matrix is a
// deliberate topology change and must update this test.
func TestComposeMountMatrix(t *testing.T) {
	services := parseCompose(t, composePath(t))

	equal(t, "panel volumes", mustService(t, services, "panel").volumes, []string{
		"./data:/data",
		"./config:/config",
		"./status:/status:ro",
		"./backups:/data/backups",
	})
	equal(t, "panel-init volumes", mustService(t, services, "panel-init").volumes, []string{
		"./data:/data",
		"./config:/config",
	})
	equal(t, "awg volumes", mustService(t, services, "awg").volumes, []string{
		"./config:/config:ro",
		"./status:/status",
	})
}

// TestComposeBackupsMountOnlyOnPanel: exactly one backups mount in the
// whole topology, on panel, RW — awg and panel-init never see it.
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
	if len(mounts) != 1 || mounts[0] != "panel: ./backups:/data/backups" {
		t.Fatalf("backups mounts = %v, want exactly [panel: ./backups:/data/backups]", mounts)
	}
	if strings.HasSuffix(mounts[0], ":ro") {
		t.Fatalf("backups mount must be RW, got %q", mounts[0])
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
