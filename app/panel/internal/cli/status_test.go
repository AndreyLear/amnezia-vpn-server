package cli

// `panel status` (M5 consumer) tests:
//   - missing status.json → "na", exit 0 (never exit 1)
//   - valid status.json → strict-parsed JSON on stdout
//   - tampered/malformed file → exit 1, generic error, file content
//     never echoed (no secret-leak via a manually replaced status.json)
//   - the command never writes status.json (read-only consumer)

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/status"
)

// formKey returns a deterministic 32-byte base64 key: the byte b
// repeated 32 times. Used to build deliberately marked key material.
func formKeyStatus(b byte) string {
	var raw [32]byte
	for i := range raw {
		raw[i] = b
	}
	return base64.StdEncoding.EncodeToString(raw[:])
}

// statusCtx wires the status path used by ReadStatus through the same
// env-var mechanism the binary honors.
func (c *ctx) setStatusPath(t *testing.T, path string) {
	t.Helper()
	t.Setenv("AMNEZIA_STATUS_PATH", path)
}

// writeSampleStatus writes a valid producer-shaped status.json (via the
// status model + atomic writer) and returns its path.
func writeSampleStatus(t *testing.T, c *ctx) string {
	t.Helper()
	path := filepath.Join(c.dir, "status.json")
	st := &status.Status{
		Schema:      status.SchemaVersion,
		GeneratedAt: time.Date(2026, 8, 10, 12, 0, 5, 0, time.UTC),
		Interface: &status.Interface{
			Iface:        "awg0",
			HasInterface: true,
			PublicKey:    formKeyStatus(0x11),
			ListenPort:   51820,
			FWMark:       "off",
			AWGParams:    status.AWGParams{Jc: 3, Jmin: 21, Jmax: 31, S1: 904, S2: 737},
		},
		Peers: []status.Peer{
			{
				PublicKey:           formKeyStatus(0x22),
				Endpoint:            "192.0.2.1:51820",
				AllowedIPs:          []string{"10.8.0.2/32"},
				PersistentKeepalive: "off",
			},
		},
	}
	raw, err := json.Marshal(st)
	if err != nil {
		t.Fatalf("marshal sample: %v", err)
	}
	if err := status.WriteAtomic(path, raw); err != nil {
		t.Fatalf("write sample: %v", err)
	}
	return path
}

func TestStatusMissingFileIsNa(t *testing.T) {
	c := newCtx(t)
	c.setStatusPath(t, filepath.Join(c.dir, "does-not-exist.json"))
	code, out, errb := c.run("status")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (missing file is \"na\")", code)
	}
	if out != "panel status: na\n" {
		t.Fatalf("stdout = %q, want panel status: na", out)
	}
	if errb != "" {
		t.Fatalf("stderr = %q, want empty", errb)
	}
}

func TestStatusHappyPath(t *testing.T) {
	c := newCtx(t)
	c.setStatusPath(t, writeSampleStatus(t, c))
	code, out, errb := c.run("status")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errb)
	}
	if errb != "" {
		t.Fatalf("stderr = %q, want empty", errb)
	}
	for _, want := range []string{
		`"schema": "v1"`,
		`"has_interface": true`,
		`"iface": "awg0"`,
		`"listen_port": 51820`,
		`"awg_params"`,
		`"public_key": "` + formKeyStatus(0x22),
		`"endpoint": "192.0.2.1:51820"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	// the command must reflect the runtime file only — no SQLite is
	// touched, and the JSON parses strictly
	st, err := status.ParseJSON([]byte(out))
	if err != nil {
		t.Fatalf("stdout is not valid status JSON: %v", err)
	}
	if st.Interface == nil || len(st.Peers) != 1 {
		t.Fatalf("parsed = %+v", st)
	}
}

func TestStatusUsageErrors(t *testing.T) {
	c := newCtx(t)
	for _, args := range [][]string{{"status", "extra"}, {"status", "--flag"}} {
		code, _, errb := c.run(args...)
		if code != 2 {
			t.Fatalf("%v: exit = %d, want 2", args, code)
		}
		if !strings.Contains(errb, "panel status:") {
			t.Fatalf("%v: stderr = %q", args, errb)
		}
	}
}

// A manually replaced status.json carrying secret fields must be
// rejected without leaking the payload.
func TestStatusTamperedFileNoLeak(t *testing.T) {
	marked := formKeyStatus(0xAB)
	c := newCtx(t)
	path := filepath.Join(c.dir, "status.json")
	tampered := strings.ReplaceAll(
		sampleStatusJSON(t),
		"__SECRET__", marked,
	)
	if err := os.WriteFile(path, []byte(tampered), 0o600); err != nil {
		t.Fatal(err)
	}
	c.setStatusPath(t, path)

	code, out, errb := c.run("status")
	if code != 1 {
		t.Fatalf("exit = %d, want 1 for a tampered file", code)
	}
	if out != "" {
		t.Fatalf("stdout = %q, want empty on error", out)
	}
	if strings.Contains(errb, marked) {
		t.Fatalf("stderr leaks the tampered secret: %q", errb)
	}
	if strings.Contains(errb, "{") {
		t.Fatalf("stderr echoes file content: %q", errb)
	}
	if !strings.Contains(errb, "panel status:") {
		t.Fatalf("stderr = %q, want panel status: prefix", errb)
	}
}

func TestStatusBrokenJSON(t *testing.T) {
	c := newCtx(t)
	path := filepath.Join(c.dir, "status.json")
	if err := os.WriteFile(path, []byte(`{"schema":`), 0o600); err != nil {
		t.Fatal(err)
	}
	c.setStatusPath(t, path)
	code, _, errb := c.run("status")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if strings.Contains(errb, `{"schema":`) {
		t.Fatalf("stderr echoes broken content: %q", errb)
	}
}

// `panel status` must never write status.json, even when it exists.
func TestStatusIsReadOnly(t *testing.T) {
	c := newCtx(t)
	path := writeSampleStatus(t, c)
	c.setStatusPath(t, path)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	beforeInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if code, _, errb := c.run("status"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errb)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(string(before), string(after)) {
		t.Fatal("panel status modified status.json")
	}
	if info, err := os.Stat(path); err != nil || info.ModTime() != beforeInfo.ModTime() {
		t.Fatal("panel status touched status.json")
	}
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("panel status left a temp file: %s", e.Name())
		}
	}
}

// status command must not open SQLite: running it with an unwritable
// database path still succeeds (runtime status is independent of DB).
func TestStatusDoesNotNeedDatabase(t *testing.T) {
	c := newCtx(t)
	c.setStatusPath(t, writeSampleStatus(t, c))
	t.Setenv("AMNEZIA_DB_PATH", filepath.Join(c.dir, "blocked", "x.sqlite"))
	code, _, errb := c.run("status")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 without DB access; stderr: %s", code, errb)
	}
}

// ---------- test helpers ----------

// sampleStatusJSON returns valid v1 status JSON with a __SECRET__
// placeholder used by the tampering tests.
func sampleStatusJSON(t *testing.T) string {
	t.Helper()
	return `{"schema":"v1","generated_at_utc":"2026-08-10T12:00:05Z","interface":{"iface":"awg0","has_interface":true,"public_key":"` + formKeyStatus(0x11) +
		`","listen_port":51820,"fwmark":"off","awg_params":{"jc":3,"jmin":21,"jmax":31,"s1":904,"s2":737,"s3":0,"s4":0}},"peers":[{"public_key":"` + formKeyStatus(0x22) +
		`","endpoint":"192.0.2.1:51820","allowed_ips":["10.8.0.2/32"],"last_handshake_utc":null,"rx_bytes":0,"tx_bytes":0,"persistent_keepalive":"off"}],"__SECRET__":"__SECRET__"}`
}
