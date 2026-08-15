package cli

// M8.8 restart workflow tests: the stack restart half of the restore
// pipeline — `panel restore` leaves a pending marker, and the next
// `panel init` (panel-init container) applies it atomically, then
// regenerates awg0.conf from the restored state.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/backup"
)

func TestInitAppliesPendingRestore(t *testing.T) {
	c := newCtx(t)
	setBackupsPath(t, c)
	c.seedServer("", "")
	_, alicePub, _, _ := c.seedClient("alice")
	out := c.mustRun("backup", "create")
	name := filepath.Base(strings.TrimSuffix(strings.TrimSpace(out), "\n"))
	_, bobPub, _, _ := c.seedClient("bob")

	// prepare the restore (CLI stops at the pending marker)
	code, out, errb := c.run("restore", name)
	if code != 0 {
		t.Fatalf("restore exit = %d, stderr = %s", code, errb)
	}
	assertSecretFree(t, out+errb, "")

	// stack restart: panel-init runs `panel init`
	code, out, errb = c.run("init")
	if code != 0 {
		t.Fatalf("init exit = %d, stderr = %s", code, errb)
	}
	for _, want := range []string{
		"panel init: pending restore applied",
		"panel init: awg0.conf generated",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("init stdout missing %q:\n%s", want, out)
		}
	}

	// the pending marker is gone: the web UI unblocks mutations
	if _, ok, err := backup.PendingPath(c.dbPath); err != nil {
		t.Fatalf("PendingPath: %v", err)
	} else if ok {
		t.Fatal("pending marker still present after init")
	}

	// the live database is the archived snapshot (alice only)
	if got := clientCount(t, c.dbPath); got != 1 {
		t.Fatalf("live db clients = %d, want 1", got)
	}

	// awg0.conf was regenerated from the restored state
	cfg, err := os.ReadFile(c.cfgPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(cfg), alicePub) {
		t.Fatal("config missing restored client peer")
	}
	if strings.Contains(string(cfg), bobPub) {
		t.Fatal("config contains a peer added after the backup")
	}

	// a second init (no pending) is a plain no-op regeneration
	code, out, errb = c.run("init")
	if code != 0 {
		t.Fatalf("second init exit = %d, stderr = %s", code, errb)
	}
	if strings.Contains(out, "pending restore applied") {
		t.Fatalf("second init reported an apply:\n%s", out)
	}
}

// TestInitFailsOnInconsistentPending: a marker without a restored image
// is refused at init time with exit 1 and the live database untouched.
func TestInitFailsOnInconsistentPending(t *testing.T) {
	c := newCtx(t)
	c.seedServer("", "")
	dir := filepath.Dir(c.dbPath)
	pending := filepath.Join(dir, ".restore-pending")
	if err := os.Mkdir(pending, 0o700); err != nil {
		t.Fatal(err)
	}

	code, out, errb := c.run("init")
	if code != 1 {
		t.Fatalf("init exit = %d, want 1 (stderr=%s)", code, errb)
	}
	if !strings.Contains(errb, "restored image missing") {
		t.Fatalf("stderr missing diagnosis:\n%s", errb)
	}
	if out != "" {
		t.Fatalf("stdout = %q, want empty", out)
	}
	if got := clientCount(t, c.dbPath); got != 0 {
		t.Fatalf("live db clients = %d, want 0 (untouched)", got)
	}
	if _, ok, err := backup.PendingPath(c.dbPath); err != nil {
		t.Fatalf("PendingPath: %v", err)
	} else if !ok {
		t.Fatal("marker removed despite refusal")
	}
}
