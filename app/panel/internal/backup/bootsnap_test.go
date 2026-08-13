package backup

// M9.3 boot snapshot + sentinel tests.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	return filepath.Join(dir, "data")
}

func writeDB(t *testing.T, dir string, content string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "amnezia.sqlite")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func snapshots(t *testing.T, dir string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, bootSnapshotGlob))
	if err != nil {
		t.Fatal(err)
	}
	return matches
}

func TestBootSnapshotCopiesLiveDB(t *testing.T) {
	dir := testDir(t)
	path := writeDB(t, dir, "live-state-123")

	snapshot, err := BootSnapshot(path)
	if err != nil {
		t.Fatalf("BootSnapshot: %v", err)
	}
	if !strings.Contains(filepath.Base(snapshot), "amnezia.sqlite.boot-") {
		t.Fatalf("snapshot name %q", snapshot)
	}
	data, err := os.ReadFile(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "live-state-123" {
		t.Fatalf("snapshot content = %q", data)
	}
	fi, err := os.Stat(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("snapshot mode = %o, want 0600", fi.Mode().Perm())
	}
	// the live file is untouched
	live, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(live) != "live-state-123" {
		t.Fatalf("live db content = %q", live)
	}
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
	if err == nil || !strings.Contains(err.Error(), "create") {
		t.Fatalf("BootSnapshot: err = %v, want a create failure", err)
	}
	if got := snapshots(t, dir); len(got) != 0 {
		t.Fatalf("snapshots left behind: %v", got)
	}
	live, err := os.ReadFile(path)
	if err != nil || string(live) != "state" {
		t.Fatalf("live db after failure: %q (err %v)", live, err)
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
	live, err := os.ReadFile(path)
	if err != nil || string(live) != "state" {
		t.Fatalf("live db after faulted runs: %q (err %v)", live, err)
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
