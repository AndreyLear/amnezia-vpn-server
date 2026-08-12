// M5 gap tests: the awgstatus binary entry point. The producer logic is
// covered by internal/status; these tests exercise main(): argument
// handling and the exec path against a fake `awg` shim.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var binPath string

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "awgstatus-test-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	binPath = filepath.Join(tmp, "awgstatus")
	if out, err := exec.Command("go", "build", "-o", binPath, ".").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build awgstatus: %v\n%s", err, out)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(tmp)
	os.Exit(code)
}

// fakeAWG writes an executable `awg` shim into dir. The shim prints
// dump (a valid `awg show <iface> dump` payload) or fails.
func fakeAWG(t *testing.T, dir, dump string, fail bool) {
	t.Helper()
	var body string
	if fail {
		body = "#!/bin/sh\necho 'awg: no such interface' >&2\nexit 1\n"
	} else {
		body = "#!/bin/sh\nprintf '%s' '" + dump + "'\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "awg"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func runWithPATH(t *testing.T, path string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(binPath, args...)
	cmd.Env = append(os.Environ(), "PATH="+path)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestUsageErrors(t *testing.T) {
	for _, args := range [][]string{nil, {"awg0"}, {"awg0", "a", "b"}, {"a", "b", "c"}} {
		out, err := runWithPATH(t, "/nonexistent", args...)
		if err == nil {
			t.Fatalf("args %v: exit 0, want 1", args)
		}
		if !strings.Contains(out, "usage: awgstatus <interface> <status-file>") {
			t.Fatalf("args %v: stderr = %q, want usage", args, out)
		}
	}
}

// happyPathDump is a valid two-line dump (interface + one peer); the
// private/preshared fields are marked secrets that must not appear in
// the written status.json.
const happyPathDump = "PRIV0\tPUB0\t51820\t3\t21\t31\t904\t737\t0\t0\t(null)\t(null)\t(null)\t(null)\t(null)\t(null)\t(null)\t(null)\t(null)\toff\nPUB1\tPSK1\t192.0.2.10:51820\t10.8.0.4/32\t0\t900\t1200\toff\n"

func TestGenerateHappyPath(t *testing.T) {
	shim := t.TempDir()
	fakeAWG(t, shim, happyPathDump, false)
	outPath := filepath.Join(t.TempDir(), "status.json")

	if out, err := runWithPATH(t, shim, "awg0", outPath); err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}

	raw, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	var st map[string]interface{}
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatalf("status.json is not JSON: %v\n%s", err, raw)
	}
	if st["schema"] != "v1" {
		t.Fatalf("schema = %v", st["schema"])
	}
	body := string(raw)
	for _, secret := range []string{"PRIV0", "PSK1"} {
		if strings.Contains(body, secret) {
			t.Fatalf("status.json leaks %q", secret)
		}
	}
	fi, err := os.Stat(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 0600", fi.Mode().Perm())
	}
}

// TestAWGMissingWritesNoInterface: when `awg` is not on PATH, the
// producer fails open — a has_interface=false snapshot, exit 0.
func TestAWGMissingWritesNoInterface(t *testing.T) {
	outPath := filepath.Join(t.TempDir(), "status.json")
	if out, err := runWithPATH(t, t.TempDir(), "awg0", outPath); err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	raw, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	var st struct {
		Interface *json.RawMessage `json:"interface"`
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatalf("bad json: %v\n%s", err, raw)
	}
	if st.Interface != nil {
		t.Fatalf("interface = %s, want null", st.Interface)
	}
}

// TestAWGFailKeepsPrevious: awg failing (exit 1) still writes the
// has_interface=false snapshot per the entrypoint contract.
func TestAWGFailWritesNoInterface(t *testing.T) {
	shim := t.TempDir()
	fakeAWG(t, shim, "", true)
	outPath := filepath.Join(t.TempDir(), "status.json")
	if out, err := runWithPATH(t, shim, "awg0", outPath); err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	raw, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	if strings.Contains(string(raw), `"has_interface":true`) {
		t.Fatalf("expected has_interface=false snapshot:\n%s", raw)
	}
}
