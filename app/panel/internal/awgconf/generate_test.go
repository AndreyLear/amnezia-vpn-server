package awgconf

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

func newTestDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	dir := t.TempDir()
	handle, err := db.Open(filepath.Join(dir, "amnezia.sqlite"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { handle.Close() })
	if err := db.Migrate(handle); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	return handle, dir
}

func seedServer(t *testing.T, handle *sql.DB, awgParams, dns string) {
	t.Helper()
	_, err := handle.Exec(
		`INSERT INTO server (id, private_key, public_key, address, listen_port, dns, awg_params, created_at, updated_at)
		 VALUES (1, ?, ?, '10.8.0.1/24', 51820, ?, ?, '2026-08-10T00:00:00Z', '2026-08-10T00:00:00Z')`,
		testKey(1), testKey(2), sql.NullString{String: dns, Valid: dns != ""}, awgParams,
	)
	if err != nil {
		t.Fatalf("seed server: %v", err)
	}
}

func seedClient(t *testing.T, handle *sql.DB, id int64, psk string, enabled bool, address string) {
	t.Helper()
	_, err := handle.Exec(
		`INSERT INTO clients (id, name, private_key, public_key, preshared_key, address, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, '2026-08-10T00:00:00Z', '2026-08-10T00:00:00Z')`,
		id, fmt.Sprintf("client-%d", id), testKey(uint8(10+id)), testKey(uint8(20+id)),
		sql.NullString{String: psk, Valid: psk != ""}, address, boolInt(enabled),
	)
	if err != nil {
		t.Fatalf("seed client %d: %v", id, err)
	}
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func TestEmptyServerRow(t *testing.T) {
	handle, dir := newTestDB(t)
	target := filepath.Join(dir, "awg0.conf")
	err := Generate(handle, target)
	if !errors.Is(err, ErrNoServerRow) {
		t.Fatalf("Generate() error = %v, want ErrNoServerRow", err)
	}
	if err == nil || err.Error() != "no server row (id=1); insert server configuration first" {
		t.Fatalf("error text = %q", err)
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Fatalf("target file must not be created on error")
	}
}

func TestGenerateIntegration(t *testing.T) {
	handle, dir := newTestDB(t)
	seedServer(t, handle, `{"jc":3,"jmin":1,"jmax":5,"s1":1,"s2":2,"s3":3,"s4":4,"h1":"3-5","i1":"<t><r 4>"}`, "1.1.1.1")
	seedClient(t, handle, 1, testKey(4), true, "10.8.0.2/32")
	seedClient(t, handle, 2, "", true, "10.8.0.3/32")

	target := filepath.Join(dir, "awg0.conf")
	if err := Generate(handle, target); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	got := string(data)

	expect := []string{
		"PrivateKey = " + testKey(1),
		"Address = 10.8.0.1/24",
		"ListenPort = 51820",
		"DNS = 1.1.1.1",
		"Jc = 3",
		"Jmin = 1",
		"Jmax = 5",
		"S1 = 1", "S2 = 2", "S3 = 3", "S4 = 4",
		"H1 = 3-5",
		"I1 = <t><r 4>",
	}
	for _, want := range expect {
		if !strings.Contains(got, want+"\n") {
			t.Errorf("missing line %q in:\n%s", want, got)
		}
	}
	order := []string{"PrivateKey", "Address", "ListenPort", "DNS", "Jc", "Jmin", "Jmax", "S1", "S2", "S3", "S4", "H1", "I1"}
	last := -1
	for _, key := range order {
		idx := strings.Index(got, key+" = ")
		if idx < 0 {
			t.Fatalf("line %q not found", key)
		}
		if idx < last {
			t.Fatalf("lines not in contract order: %q appears after a later key", key)
		}
		last = idx
	}
}

func TestEnabledOnly(t *testing.T) {
	handle, dir := newTestDB(t)
	seedServer(t, handle, "", "")
	seedClient(t, handle, 1, "", false, "10.8.0.2/32")
	seedClient(t, handle, 2, "", true, "10.8.0.3/32")
	seedClient(t, handle, 3, "", false, "10.8.0.4/32")

	target := filepath.Join(dir, "awg0.conf")
	if err := Generate(handle, target); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	data, _ := os.ReadFile(target)
	got := string(data)
	want := "[Peer]\nPublicKey = " + testKey(22) + "\nAllowedIPs = 10.8.0.3/32\n"
	if !strings.Contains(got, want) {
		t.Errorf("expected only enabled peer:\n%s", got)
	}
	for _, seed := range []byte{21, 23} {
		if strings.Contains(got, fmt.Sprintf("PublicKey = %s", testKey(seed))) {
			t.Errorf("disabled client leaked public key %s", testKey(seed))
		}
	}
}

func TestGenerateAtomicWrite(t *testing.T) {
	handle, dir := newTestDB(t)
	seedServer(t, handle, "", "")
	seedClient(t, handle, 1, "", true, "10.8.0.2/32")

	target := filepath.Join(dir, "awg0.conf")
	if err := Generate(handle, target); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	st, err := os.Stat(target)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Fatalf("target mode = %o, want 0600", perm)
	}
	if _, err := os.Stat(target + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("temporary file left behind: %v", err)
	}
}

func TestGenerateIdempotentReinit(t *testing.T) {
	handle, dir := newTestDB(t)
	seedServer(t, handle, `{"jc":3,"h1":"3-5","i1":"<r 4>"}`, "")
	seedClient(t, handle, 1, testKey(4), true, "10.8.0.2/32")

	target := filepath.Join(dir, "awg0.conf")
	if err := Generate(handle, target); err != nil {
		t.Fatalf("Generate #1: %v", err)
	}
	first, _ := os.ReadFile(target)
	if err := Generate(handle, target); err != nil {
		t.Fatalf("Generate #2: %v", err)
	}
	second, _ := os.ReadFile(target)
	if string(first) != string(second) {
		t.Fatalf("re-running Generate changed the file:\n%q\nvs\n%q", first, second)
	}
}

func TestNoSecretsInErrors(t *testing.T) {
	handle, dir := newTestDB(t)
	seedServer(t, handle, "", "")

	secret := "averysecretpresharedkeyvalue123"
	seedClient(t, handle, 1, secret, true, "10.8.0.2/32")
	err := Generate(handle, filepath.Join(dir, "awg0.conf"))
	if err == nil {
		t.Fatal("expected error for invalid preshared key")
	}
	if ps := err.Error(); strings.Contains(ps, secret) {
		t.Fatalf("error leaks preshared key: %v", err)
	}
}

func TestGenerateKeepsTargetOnError(t *testing.T) {
	handle, dir := newTestDB(t)
	seedServer(t, handle, "", "")
	seedClient(t, handle, 1, "not-a-real-key", true, "10.8.0.2/32")

	target := filepath.Join(dir, "awg0.conf")
	if err := os.WriteFile(target, []byte("old content"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := Generate(handle, target); err == nil {
		t.Fatal("expected Generate error for invalid preshared key")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if got := string(data); got != "old content" {
		t.Fatalf("target modified on error: %q", got)
	}
}

func TestGenerateIntegrationBadListenPort(t *testing.T) {
	handle, dir := newTestDB(t)
	if _, err := handle.Exec(
		`INSERT INTO server (id, private_key, public_key, address, listen_port, awg_params, created_at, updated_at)
		 VALUES (1, ?, ?, '10.8.0.1/24', 70000, '{}', '2026-08-10T00:00:00Z', '2026-08-10T00:00:00Z')`,
		testKey(1), testKey(2),
	); err != nil {
		t.Fatalf("seed: %v", err)
	}
	err := Generate(handle, filepath.Join(dir, "awg0.conf"))
	if err == nil {
		t.Fatal("expected error for ListenPort > 65535")
	}
}
