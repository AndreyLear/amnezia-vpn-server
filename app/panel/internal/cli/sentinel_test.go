package cli

// M9.3 init data-loss guard tests: boot snapshots and the
// .server-initialized sentinel at the panel-init contract.

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/backup"
)

// withCliFault injects an error at the named cli testFault step for the
// duration of fn, then restores the (nil) hook.
func withCliFault(t *testing.T, step string, fn func() (int, string, string)) (int, string, string) {
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

func bootSnapshotsOf(t *testing.T, dir string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "amnezia.sqlite.boot-*"))
	if err != nil {
		t.Fatal(err)
	}
	return matches
}

// TestInitTakesBootSnapshot: a live database is snapshotted before the
// first init runs (recoverable without keys).
func TestInitTakesBootSnapshot(t *testing.T) {
	c := newCtx(t)
	c.seedServer("", "")
	if got := bootSnapshotsOf(t, filepath.Dir(c.dbPath)); len(got) != 0 {
		t.Fatalf("snapshots before init = %v, want none", got)
	}
	out := c.mustRun("init")
	if !strings.Contains(out, "awg0.conf generated") {
		t.Fatalf("init stdout = %q", out)
	}
	if got := bootSnapshotsOf(t, filepath.Dir(c.dbPath)); len(got) != 1 {
		t.Fatalf("snapshots after init = %v, want exactly one", got)
	}
}

// TestInitRefusesLostDatabaseWithSentinel: .server-initialized present
// but the database file gone — init must refuse with restore
// instructions and must NOT create a fresh schema file.
func TestInitRefusesLostDatabaseWithSentinel(t *testing.T) {
	c := newCtx(t)
	c.seedServer("", "")
	if err := backup.WriteSentinel(c.dbPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(c.dbPath); err != nil {
		t.Fatal(err)
	}

	code, out, errb := c.run("init")
	if code != 1 {
		t.Fatalf("init exit = %d, want 1 (stdout=%q, stderr=%q)", code, out, errb)
	}
	for _, want := range []string{
		".server-initialized",
		"refusing to create a fresh one",
		"restore",
	} {
		if !strings.Contains(errb, want) {
			t.Fatalf("stderr missing %q:\n%s", want, errb)
		}
	}
	if _, err := os.Lstat(c.dbPath); !os.IsNotExist(err) {
		t.Fatalf("init created a fresh database despite the sentinel (stat err = %v)", err)
	}
}

// TestInitRefusesEmptyDatabaseWithSentinel: sentinel present, the
// database exists but carries no server row (schema-only, exactly the
// silent-wipe outcome) — init must refuse instead of regenerating.
func TestInitRefusesEmptyDatabaseWithSentinel(t *testing.T) {
	c := newCtx(t)
	c.seedServer("", "")
	if err := backup.WriteSentinel(c.dbPath); err != nil {
		t.Fatal(err)
	}
	// simulate the wiped state: a schema-only database, no server row
	if err := os.Remove(c.dbPath); err != nil {
		t.Fatal(err)
	}
	h := c.openDB() // creates + migrates the fresh schema
	h.Close()

	code, out, errb := c.run("init")
	if code != 1 {
		t.Fatalf("init exit = %d, want 1 (stdout=%q, stderr=%q)", code, out, errb)
	}
	if !strings.Contains(errb, "no server row was found") {
		t.Fatalf("stderr missing the no-server-row diagnosis:\n%s", errb)
	}
}

// TestServerInitWritesSentinel: `panel server init` records the
// initialization marker.
func TestServerInitWritesSentinel(t *testing.T) {
	c := newCtx(t)
	c.mustRun("server", "init", testServerCIDR, "51820", "--endpoint", testEndpoint)
	if ok, err := backup.SentinelPresent(c.dbPath); err != nil || !ok {
		t.Fatalf("sentinel after server init: present=%v err=%v, want true/nil", ok, err)
	}
}

// TestInitSelfHealsSentinelForExistingDeployment: an initialized
// deployment predating the sentinel gets the marker written on the
// first successful init (upgrade path).
func TestInitSelfHealsSentinelForExistingDeployment(t *testing.T) {
	c := newCtx(t)
	c.seedServer("", "")
	if ok, _ := backup.SentinelPresent(c.dbPath); ok {
		t.Fatal("test setup: sentinel unexpectedly present")
	}
	c.mustRun("init")
	if ok, err := backup.SentinelPresent(c.dbPath); err != nil || !ok {
		t.Fatalf("sentinel after init: present=%v err=%v, want true/nil", ok, err)
	}
}

// TestInitWithoutServerDoesNotWriteSentinel: init on a fresh schema
// without a server row exits 1 with the M3 message and must NOT record
// the sentinel — the marker claims "the server row exists" (regression:
// the upgrade self-heal must not fire for a schema created by this very
// init, or a subsequent wipe would be misread as an initialized
// deployment).
func TestInitWithoutServerDoesNotWriteSentinel(t *testing.T) {
	c := newCtx(t)
	code, out, errb := c.run("init")
	if code != 1 {
		t.Fatalf("init exit = %d, want 1 (stdout=%q, stderr=%q)", code, out, errb)
	}
	if !strings.Contains(errb, "no server row") {
		t.Fatalf("stderr missing the M3 no-server-row message:\n%s", errb)
	}
	if ok, err := backup.SentinelPresent(c.dbPath); err != nil || ok {
		t.Fatalf("sentinel after M3-failed init: present=%v err=%v, want false/nil", ok, err)
	}
	if got := bootSnapshotsOf(t, filepath.Dir(c.dbPath)); len(got) != 0 {
		t.Fatalf("boot snapshots after M3-failed init: %v, want none", got)
	}
}

// TestInitBootSnapshotFailure: a non-writable data directory makes the
// boot snapshot fail; init must exit 1 without touching the database.
// Permission-based failure injection cannot be provoked when the tests
// run as root (CI containers) — the directory is writable regardless
// of mode bits, so the test is skipped there.
func TestInitBootSnapshotFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("mode-bit failure injection is ineffective for root")
	}
	c := newCtx(t)
	c.seedServer("", "")
	dir := filepath.Dir(c.dbPath)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o700) // restore for TempDir cleanup

	code, out, errb := c.run("init")
	if code != 1 {
		t.Fatalf("init exit = %d, want 1 (stdout=%q, stderr=%q)", code, out, errb)
	}
	if !strings.Contains(errb, "boot snapshot") {
		t.Fatalf("stderr missing boot-snapshot failure:\n%s", errb)
	}
	if _, err := os.Lstat(c.dbPath); err != nil {
		t.Fatalf("live database disturbed: %v", err)
	}
}

// TestInitGenerateFailure: an unusable config path makes awg0.conf
// generation fail; init must exit 1 and leave the database untouched.
func TestInitGenerateFailure(t *testing.T) {
	c := newCtx(t)
	c.seedServer("", "")
	blocker := filepath.Join(filepath.Dir(c.dbPath), "notadir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AMNEZIA_CONFIG_PATH", filepath.Join(blocker, "awg0.conf"))

	code, out, errb := c.run("init")
	if code != 1 {
		t.Fatalf("init exit = %d, want 1 (stdout=%q, stderr=%q)", code, out, errb)
	}
	if !strings.Contains(errb, "awg0.conf") {
		t.Fatalf("stderr missing the config path:\n%s", errb)
	}
	if got := bootSnapshotsOf(t, filepath.Dir(c.dbPath)); len(got) != 1 {
		t.Fatalf("boot snapshot missing after failed generate: %v", got)
	}
}

// TestInitErrorPathFaults exercises the pure-defensive error branches
// of the init contract via fault injection: sentinel present/lstat
// failures, server-row query failure and the self-heal marker failure.
func TestInitErrorPathFaults(t *testing.T) {
	cases := []struct {
		step     string
		setup    func(c *ctx)
		wantIn   string
		sentinel bool
	}{
		{"migrate", func(c *ctx) {}, "migrate database", false},
		{"init.sentinel-present", func(c *ctx) {}, "injected", false},
		{"init.sentinel-lstat", func(c *ctx) {
			if err := backup.WriteSentinel(c.dbPath); err != nil {
				t.Fatal(err)
			}
		}, "injected", true},
		{"init.server-row", func(c *ctx) {
			if err := backup.WriteSentinel(c.dbPath); err != nil {
				t.Fatal(err)
			}
		}, "injected", true},
		{"init.selfheal", func(c *ctx) {}, "write init sentinel", false},
	}
	for _, tc := range cases {
		t.Run(tc.step, func(t *testing.T) {
			c := newCtx(t)
			c.seedServer("", "")
			if tc.sentinel {
				if err := backup.WriteSentinel(c.dbPath); err != nil {
					t.Fatal(err)
				}
			}
			code, out, errb := withCliFault(t, tc.step, func() (int, string, string) {
				return c.run("init")
			})
			if code != 1 {
				t.Fatalf("init exit = %d, want 1 (stdout=%q, stderr=%q)", code, out, errb)
			}
			if !strings.Contains(errb, tc.wantIn) {
				t.Fatalf("stderr missing %q:\n%s", tc.wantIn, errb)
			}
		})
	}
}

// TestServerInitErrorPathFaults exercises the error branches of
// `panel server init` that cannot be provoked through the public API.
func TestServerInitErrorPathFaults(t *testing.T) {
	cases := []struct {
		step   string
		wantIn string
	}{
		{"server-init.keys", "generate server keys"},
		{"server-init.sentinel", "injected"},
		{"server-init.create", "create server"},
		{"migrate", "migrate database"},
	}
	for _, tc := range cases {
		t.Run(tc.step, func(t *testing.T) {
			c := newCtx(t)
			code, out, errb := withCliFault(t, tc.step, func() (int, string, string) {
				return c.run("server", "init", testServerCIDR, "51820", "--endpoint", testEndpoint)
			})
			if code != 1 {
				t.Fatalf("server init exit = %d, want 1 (stdout=%q, stderr=%q)", code, out, errb)
			}
			if !strings.Contains(errb, tc.wantIn) {
				t.Fatalf("stderr missing %q:\n%s", tc.wantIn, errb)
			}
		})
	}
}

// TestServerInitRejectsBadEndpoint: a non-numeric or out-of-range port
// fails the endpoint pre-flight check with exit 2.
func TestServerInitRejectsBadEndpoint(t *testing.T) {
	for _, bad := range []string{"10.0.0.1:abc", "10.0.0.1:0", "10.0.0.1:99999"} {
		c := newCtx(t)
		code, out, errb := c.run("server", "init", testServerCIDR, "51820", "--endpoint", bad)
		if code != 2 {
			t.Fatalf("endpoint %q: exit = %d, want 2 (stdout=%q, stderr=%q)", bad, code, out, errb)
		}
		if !strings.Contains(errb, "invalid endpoint") {
			t.Fatalf("endpoint %q: stderr = %q", bad, errb)
		}
	}
}

// TestServerInitGenerateFailure: an unusable config path makes the
// post-init awg0.conf generation fail; exit 1 with the config error.
func TestServerInitGenerateFailure(t *testing.T) {
	c := newCtx(t)
	blocker := filepath.Join(filepath.Dir(c.dbPath), "notadir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AMNEZIA_CONFIG_PATH", filepath.Join(blocker, "awg0.conf"))

	code, out, errb := c.run("server", "init", testServerCIDR, "51820", "--endpoint", testEndpoint)
	if code != 1 {
		t.Fatalf("server init exit = %d, want 1 (stdout=%q, stderr=%q)", code, out, errb)
	}
	if !strings.Contains(errb, "awg0.conf") {
		t.Fatalf("stderr missing the config path:\n%s", errb)
	}
	// the sentinel was already recorded: the failure is config-only
	if ok, err := backup.SentinelPresent(c.dbPath); err != nil || !ok {
		t.Fatalf("sentinel after failed generate: present=%v err=%v", ok, err)
	}
}

// TestConfigPathDefault: without AMNEZIA_CONFIG_PATH the default
// /config/awg0.conf is used.
func TestConfigPathDefault(t *testing.T) {
	prev, had := os.LookupEnv("AMNEZIA_CONFIG_PATH")
	t.Cleanup(func() {
		if had {
			os.Setenv("AMNEZIA_CONFIG_PATH", prev)
		} else {
			os.Unsetenv("AMNEZIA_CONFIG_PATH")
		}
	})
	os.Unsetenv("AMNEZIA_CONFIG_PATH")
	if got := configPath(); got != "/config/awg0.conf" {
		t.Fatalf("configPath() = %q, want /config/awg0.conf", got)
	}
}

// TestServeListenFailure: an occupied listen address makes serve exit 1.
func TestServeListenFailure(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	c := newCtx(t)
	code, out, errb := c.run("serve", "--addr", ln.Addr().String())
	if code != 1 {
		t.Fatalf("serve exit = %d, want 1 (stdout=%q, stderr=%q)", code, out, errb)
	}
	if !strings.Contains(errb, "address already in use") {
		t.Fatalf("stderr missing the bind error:\n%s", errb)
	}
}
