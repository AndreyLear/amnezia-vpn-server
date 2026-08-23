package backup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

// Restoring a backup onto a freshly installed server is how a migration
// works, and two settings in the archive belong to the machine it was taken
// on: the endpoint baked into client configs and the tunnel MTU measured
// for that uplink. The panel has to see both before applying anything, so
// it can ask instead of silently carrying them over.
func TestInspectReadsHostSettings(t *testing.T) {
	c := newRestoreCtx(t)
	handle := c.reopen()
	if err := db.SetSetting(handle, "endpoint", "old.example.com:443"); err != nil {
		t.Fatalf("SetSetting endpoint: %v", err)
	}
	if err := db.SetSetting(handle, "mtu", "1380"); err != nil {
		t.Fatalf("SetSetting mtu: %v", err)
	}
	archive, err := Create(handle, c.backupsDir, func() time.Time { return fakeNow })
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := Inspect(archive)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if got.Endpoint != "old.example.com:443" {
		t.Fatalf("Endpoint = %q, want the archived one", got.Endpoint)
	}
	if got.MTU != "1380" {
		t.Fatalf("MTU = %q, want 1380", got.MTU)
	}
}

// An archive taken before these settings existed must not look like an
// error: absent keys are simply empty.
func TestInspectToleratesMissingSettings(t *testing.T) {
	c := newRestoreCtx(t)
	archive, err := Create(c.reopen(), c.backupsDir, func() time.Time { return fakeNow })
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := Inspect(archive)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if got.Endpoint != "" || got.MTU != "" {
		t.Fatalf("unset keys must read as empty, got %+v", got)
	}
}

// Inspect runs before anything is applied: it must leave the live database
// and the pending marker exactly as they were.
func TestInspectTouchesNothing(t *testing.T) {
	c := newRestoreCtx(t)
	c.seedClient("alice", testClientKey)
	archive, err := Create(c.reopen(), c.backupsDir, func() time.Time { return fakeNow })
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	before, backupsBefore := c.liveState()

	if _, err := Inspect(archive); err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	c.assertUntouched(before)
	c.assertNoMarker()
	c.assertNoNewBackups(backupsBefore)
}

// A corrupt archive must fail the same way the restore path fails, without
// echoing anything from inside the file.
func TestInspectRejectsGarbage(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "not-an-archive.tar.zst")
	if err := os.WriteFile(bad, []byte("this is not zstd"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := Inspect(bad)
	if err == nil {
		t.Fatal("a corrupt archive must be rejected")
	}
	if !strings.HasPrefix(err.Error(), "backup: ") {
		t.Fatalf("error must stay in the backup: namespace, got %q", err)
	}
}
