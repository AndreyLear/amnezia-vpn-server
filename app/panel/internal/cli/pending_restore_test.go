package cli

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Пока восстановление ждёт перезапуска, изменения CLI потом молча теряются:
// база подменяется образом из архива. Изменяющие команды отказывают,
// читающие работают (amnezia-vpn-server-76mp.9).
func TestMutatingCommandsRefuseWhileRestorePending(t *testing.T) {
	c := newCtx(t)
	c.seedServer("", "")
	id, _, _, _ := c.seedClient("phone")
	sid := strconv.FormatInt(id, 10)
	if err := os.Mkdir(filepath.Join(c.dir, ".restore-pending"), 0o700); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(c.cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	mutating := [][]string{
		{"client", "add", "laptop"},
		{"client", "enable", sid},
		{"client", "disable", sid},
		{"client", "rename", sid, "tablet"},
		{"client", "set-mtu", sid, "1300"},
		{"client", "set-rate", sid, "10"},
		{"client", "set-expiry", sid, "2030-01-01T00:00:00Z"},
		{"client", "delete", sid},
		{"server", "update", "--dns", "9.9.9.9"},
		{"server", "init", "10.9.0.1/24", "51821", "--endpoint", "vpn.example.com:51821"},
	}
	for _, args := range mutating {
		code, _, errb := c.run(args...)
		if code != 1 || !strings.Contains(errb, "restore is pending") {
			t.Errorf("panel %v: exit %d, stderr %q — ждали отказ из-за ожидающего восстановления", args, code, errb)
		}
	}

	out := c.mustRun("client", "list")
	if !strings.Contains(out, "phone") || strings.Contains(out, "laptop") {
		t.Fatalf("client list после отказов:\n%s", out)
	}
	c.mustRun("client", "show", sid)
	after, err := os.ReadFile(c.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("awg0.conf изменён при ожидающем восстановлении")
	}
}
