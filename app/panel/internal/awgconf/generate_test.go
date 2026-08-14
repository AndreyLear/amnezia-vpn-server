package awgconf

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	seedServer(t, handle, `{"jc":3,"jmin":1,"jmax":5,"s1":1,"s2":2,"s3":3,"s4":4,"h1":"1234567","i1":"<t><r 4>"}`, "1.1.1.1")
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
		"H1 = 1234567",
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
	seedServer(t, handle, `{"jc":3,"h1":"1234567","i1":"<r 4>"}`, "")
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

// TestWriteAtomicConcurrent pins the unique-temp invariant: concurrent
// WriteAtomic calls to the same target must all succeed (a fixed
// ".tmp" name would make losers fail with rename ENOENT or corrupt
// each other's data), the final content must be exactly one of the
// written payloads, and no temp files may be left behind. Runs under
// -race.
func TestWriteAtomicConcurrent(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "awg0.conf")
	old := []byte("old content")
	if err := WriteAtomic(target, old); err != nil {
		t.Fatalf("seed write: %v", err)
	}

	const writers = 16
	payloads := make([][]byte, writers)
	for i := range payloads {
		payloads[i] = []byte(fmt.Sprintf("payload-%d-%s", i, strings.Repeat("x", 256)))
	}

	errs := make([]error, writers)
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			errs[w] = WriteAtomic(target, payloads[w])
		}(w)
	}
	wg.Wait()

	for w, err := range errs {
		if err != nil {
			t.Fatalf("writer %d: %v", w, err)
		}
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	ok := false
	for _, p := range payloads {
		if string(data) == string(p) {
			ok = true
			break
		}
	}
	if !ok {
		t.Fatalf("target content is not one of the written payloads (len=%d)", len(data))
	}

	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat target: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("target mode = %o, want 600", perm)
	}

	leftovers, err := filepath.Glob(filepath.Join(dir, "awg0.conf.tmp-*"))
	if err != nil {
		t.Fatalf("glob leftovers: %v", err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("leftover temp files: %v", leftovers)
	}
}

// TestWriteAtomicRenameFailure: when the rename cannot happen (target
// path is occupied by a directory), WriteAtomic returns an error, the
// directory is left untouched, and no temp files leak behind.
func TestWriteAtomicRenameFailure(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "conf")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	err := WriteAtomic(target, []byte("should not replace a directory"))
	if err == nil {
		t.Fatal("WriteAtomic over a directory succeeded")
	}
	if !strings.Contains(err.Error(), "rename") {
		t.Errorf("error %q does not mention the rename step", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("target disappeared: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("target is no longer a directory")
	}
	leftovers, err := filepath.Glob(filepath.Join(dir, "conf.tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("temp files leaked: %v", leftovers)
	}
}

// TestWriteAtomicTempCreateFailure: when the temp file cannot be
// created (parent path is a regular file, not a directory) the error
// names the create step and the target never comes into existence.
func TestWriteAtomicTempCreateFailure(t *testing.T) {
	dir := t.TempDir()
	notDir := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(notDir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(notDir, "awg0.conf")
	err := WriteAtomic(target, []byte("x"))
	if err == nil {
		t.Fatal("WriteAtomic with an uncreatable temp succeeded")
	}
	if !strings.Contains(err.Error(), "create temp") {
		t.Errorf("error %q does not mention the create-temp step", err)
	}
	if _, statErr := os.Stat(target); statErr == nil {
		t.Fatal("target exists after failed create")
	}
}
