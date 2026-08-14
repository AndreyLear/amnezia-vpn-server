package backup

// M9.3 boot snapshot + sentinel tests.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

func testDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	return filepath.Join(dir, "data")
}

// writeDB creates a real migrated SQLite database in dir (clients:
// alice) and returns its path — the boot snapshot is taken with
// VACUUM INTO, so the fixture must be a real database.
func writeDB(t *testing.T, dir string, content string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "amnezia.sqlite")
	handle, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	if err := db.Migrate(handle); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateClient(handle, "10.8.0.1/24", db.NewClient{
		Name: content, PrivateKey: bsnapPriv, PublicKey: bsnapPub,
	}); err != nil {
		t.Fatal(err)
	}
	return path
}

const (
	bsnapPriv = "4OSe6B1rDXdY4RdVJ+eenaEJOqRVZ9kxx25z9bI0t28="
	bsnapPub  = "qXvOE2SbilF5RNpHXPU8xQCTvUIxYPeTNI6R6p+bqnw="
	bsnapPub2 = "a1IqKSrVxcXN16cMZIUZ6kof0oU0kvwb0w6Q8bgGggQ="
)

// readDBNames opens the database at path and returns the client names
// (used to assert snapshot content without byte equality — VACUUM INTO
// rebuilds the file).
func readDBNames(t *testing.T, path string) []string {
	t.Helper()
	handle, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	clients, err := db.ClientsAll(handle)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, c := range clients {
		names = append(names, c.Name)
	}
	return names
}

func snapshots(t *testing.T, dir string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, bootSnapshotGlob))
	if err != nil {
		t.Fatal(err)
	}
	return matches
}

// integrityOK runs PRAGMA integrity_check on the database file.
func integrityOK(t *testing.T, path string) bool {
	t.Helper()
	handle, err := db.Open(path)
	if err != nil {
		return false
	}
	defer handle.Close()
	var result string
	if err := handle.QueryRow(`PRAGMA integrity_check`).Scan(&result); err != nil {
		return false
	}
	return result == "ok"
}

func TestBootSnapshotCopiesLiveDB(t *testing.T) {
	dir := testDir(t)
	path := writeDB(t, dir, "alice")

	snapshot, err := BootSnapshot(path)
	if err != nil {
		t.Fatalf("BootSnapshot: %v", err)
	}
	if !strings.Contains(filepath.Base(snapshot), "amnezia.sqlite.boot-") {
		t.Fatalf("snapshot name %q", snapshot)
	}
	if got := readDBNames(t, snapshot); len(got) != 1 || got[0] != "alice" {
		t.Fatalf("snapshot content = %v, want [alice]", got)
	}
	if !integrityOK(t, snapshot) {
		t.Fatalf("snapshot failed integrity_check")
	}
	fi, err := os.Stat(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("snapshot mode = %o, want 0600", fi.Mode().Perm())
	}
	// the live file is untouched
	if got := readDBNames(t, path); len(got) != 1 || got[0] != "alice" {
		t.Fatalf("live db content = %v, want [alice]", got)
	}
}

// TestBootSnapshotConsistentWhileWriting (T-115): a snapshot taken
// while another connection keeps writing passes the integrity check —
// the VACUUM INTO snapshot is consistent where a raw file copy could
// be torn.
func TestBootSnapshotConsistentWhileWriting(t *testing.T) {
	dir := testDir(t)
	path := writeDB(t, dir, "alice")
	writer, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; ; i++ {
			if _, err := db.CreateClient(writer, "10.8.0.1/24", db.NewClient{
				Name: fmt.Sprintf("w%d", i), PrivateKey: bsnapPriv, PublicKey: bsnapPub2,
			}); err != nil {
				return // address space exhausted — stop writing
			}
		}
	}()

	for i := 0; i < 5; i++ {
		snapshot, err := BootSnapshot(path)
		if err != nil {
			t.Fatalf("BootSnapshot #%d: %v", i, err)
		}
		if !integrityOK(t, snapshot) {
			t.Fatalf("snapshot #%d failed integrity_check", i)
		}
	}
	writer.Close()
	<-done
}

func TestBootSnapshotNoLiveDBIsNoop(t *testing.T) {
	dir := testDir(t)
	snapshot, err := BootSnapshot(filepath.Join(dir, "amnezia.sqlite"))
	if err != nil {
		t.Fatalf("BootSnapshot: %v", err)
	}
	if snapshot != "" {
		t.Fatalf("snapshot = %q, want empty for a missing live db", snapshot)
	}
}

func TestBootSnapshotRefusesSymlink(t *testing.T) {
	dir := testDir(t)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "amnezia.sqlite")
	if err := os.Symlink("/nonexistent-target", link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if _, err := BootSnapshot(link); err == nil {
		t.Fatal("BootSnapshot followed a symlink, want refusal")
	} else if !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("error = %v", err)
	}
}

func TestBootSnapshotRotation(t *testing.T) {
	dir := testDir(t)
	path := writeDB(t, dir, "state")
	seen := map[string]bool{}
	for i := 0; i < bootSnapshotKeep+2; i++ {
		snapshot, err := BootSnapshot(path)
		if err != nil {
			t.Fatalf("BootSnapshot #%d: %v", i, err)
		}
		seen[snapshot] = true
	}
	got := snapshots(t, dir)
	if len(got) != bootSnapshotKeep {
		t.Fatalf("snapshots kept = %d, want %d (%v)", len(got), bootSnapshotKeep, got)
	}
	// the retained ones are the newest
	for _, s := range got {
		if !seen[s] {
			t.Fatalf("unknown snapshot file %s", s)
		}
	}
}

// withFault injects an error at the named testFault step for the
// duration of fn, then restores the (nil) hook.
func withFault(t *testing.T, step string, fn func() error) error {
	t.Helper()
	prev := testFault
	testFault = func(s string) error {
		if s == step {
			return errors.New("injected: " + step)
		}
		return nil
	}
	defer func() { testFault = prev }()
	return fn()
}

func faultSnapshot(t *testing.T, dbPath, step, want string, wantLeftover []string) {
	t.Helper()
	err := withFault(t, step, func() error {
		_, err := BootSnapshot(dbPath)
		return err
	})
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("BootSnapshot step %s: err = %v, want containing %q", step, err, want)
	}
	for _, leftover := range wantLeftover {
		if _, err := os.Lstat(leftover); !os.IsNotExist(err) {
			t.Fatalf("step %s: leftover %s present (stat err = %v)", step, leftover, err)
		}
	}
}

// TestBootSnapshotCreateFailure: a non-writable data directory makes
// snapshot creation fail; the live database is left untouched.
// Permission-based failure injection cannot be provoked when the tests
// run as root (CI containers) — the directory is writable regardless
// of mode bits, so the test is skipped there.
func TestBootSnapshotCreateFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("mode-bit failure injection is ineffective for root")
	}
	dir := testDir(t)
	path := writeDB(t, dir, "state")
	parent := filepath.Dir(path)
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(parent, 0o700)

	_, err := BootSnapshot(path)
	if err == nil || !strings.Contains(err.Error(), "copy to") {
		t.Fatalf("BootSnapshot: err = %v, want a copy/create failure", err)
	}
	if got := snapshots(t, dir); len(got) != 0 {
		t.Fatalf("snapshots left behind: %v", got)
	}
	if got := readDBNames(t, path); len(got) != 1 || got[0] != "state" {
		t.Fatalf("live db after failure: %v (err %v)", got, err)
	}
}

func TestBootSnapshotFaultInjection(t *testing.T) {
	dir := testDir(t)
	path := writeDB(t, dir, "state")

	faultSnapshot(t, path, "snap.stat", "stat live database", nil)
	faultSnapshot(t, path, "snap.open", "open live database", nil)
	faultSnapshot(t, path, "snap.copy", "copy to", nil)
	faultSnapshot(t, path, "snap.sync", "sync", nil)
	faultSnapshot(t, path, "snap.close", "close", nil)

	if got := snapshots(t, dir); len(got) != 0 {
		t.Fatalf("faulted runs left snapshots behind: %v", got)
	}
	if got := readDBNames(t, path); len(got) != 1 || got[0] != "state" {
		t.Fatalf("live db after faulted runs: %v", got)
	}
}

func TestBootSnapshotFaultRotation(t *testing.T) {
	dir := testDir(t)
	path := writeDB(t, dir, "state")
	for i := 0; i < bootSnapshotKeep+1; i++ {
		if _, err := BootSnapshot(path); err != nil {
			t.Fatalf("seed snapshot #%d: %v", i, err)
		}
	}
	faultSnapshot(t, path, "snap.rotate-glob", "glob snapshots", nil)
	faultSnapshot(t, path, "snap.rotate-remove", "remove old snapshot", nil)
}

func TestSentinelFaults(t *testing.T) {
	dir := testDir(t)
	path := filepath.Join(dir, "amnezia.sqlite")

	if err := withFault(t, "sentinel.present", func() error {
		_, err := SentinelPresent(path)
		return err
	}); err == nil || !strings.Contains(err.Error(), "sentinel") {
		t.Fatalf("sentinel.present fault: err = %v", err)
	}
	if err := withFault(t, "sentinel.lstat", func() error {
		return WriteSentinel(path)
	}); err == nil || !strings.Contains(err.Error(), "sentinel") {
		t.Fatalf("sentinel.lstat fault: err = %v", err)
	}
	steps := []struct {
		step string
		want string
	}{
		{"sentinel.mkdir", "mkdir"},
		{"sentinel.write", "write"},
		{"sentinel.rename", "install"},
	}
	for _, s := range steps {
		if _, err := os.Lstat(SentinelPath(path)); !os.IsNotExist(err) {
			t.Fatalf("step %s precondition: sentinel present", s.step)
		}
		err := withFault(t, s.step, func() error {
			return WriteSentinel(path)
		})
		if err == nil || !strings.Contains(err.Error(), s.want) {
			t.Fatalf("step %s: err = %v, want containing %q", s.step, err, s.want)
		}
		if _, err := os.Lstat(SentinelPath(path)); !os.IsNotExist(err) {
			t.Fatalf("step %s: sentinel installed despite the failure", s.step)
		}
		if _, err := os.Lstat(SentinelPath(path) + ".tmp"); !os.IsNotExist(err) {
			t.Fatalf("step %s: tmp leftover present", s.step)
		}
	}
	if err := WriteSentinel(path); err != nil {
		t.Fatalf("final WriteSentinel: %v", err)
	}
}

func TestSentinelLifecycle(t *testing.T) {
	dir := testDir(t)
	path := filepath.Join(dir, "amnezia.sqlite")

	if ok, err := SentinelPresent(path); err != nil || ok {
		t.Fatalf("fresh dir: present=%v err=%v, want false/nil", ok, err)
	}
	if err := WriteSentinel(path); err != nil {
		t.Fatalf("WriteSentinel: %v", err)
	}
	fi, err := os.Stat(SentinelPath(path))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("sentinel mode = %o, want 0600", fi.Mode().Perm())
	}
	if ok, err := SentinelPresent(path); err != nil || !ok {
		t.Fatalf("after write: present=%v err=%v, want true/nil", ok, err)
	}
	// idempotent
	if err := WriteSentinel(path); err != nil {
		t.Fatalf("second WriteSentinel: %v", err)
	}
}
