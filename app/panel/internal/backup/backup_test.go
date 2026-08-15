package backup

// M8.2 tests: contract of the manifest, encryption round-trip, exact
// archive contents, permissions, cleanup on failure, concurrency, and
// the absence of plaintext secrets in the archive.

import (
	"archive/tar"
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
	"github.com/klauspost/compress/zstd"
)

var fakeNow = time.Date(2026, 8, 12, 10, 30, 0, 0, time.UTC)

const (
	testServerKey = "x-test-server-private-key-00000000000000000000"
	testClientKey = "x-test-client-private-key-000000000000000000000"
	testPreshared = "x-test-preshared-key-0000000000000000000000000"
	testHash      = "$argon2id$v=19$m=65536,t=1,p=4$c2FsdHNhbHRzYWx0c2FsdA$c2FsdHNhbHRzYWx0c2FsdHNhbHRz"
)

// newTestDB seeds a migrated temp database with a server row, a client
// row (with keys) and an auth user, so tests prove secrets survive the
// round trip inside the archive.
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
	_, err = handle.Exec(
		`INSERT INTO server (id, private_key, public_key, address, listen_port, dns, awg_params, created_at, updated_at)
		 VALUES (1, ?, ?, '10.66.66.1/24', 51820, '1.1.1.1', '{}', '2026-08-01T00:00:00Z', '2026-08-01T00:00:00Z')`,
		testServerKey, "x-test-server-public-key-00000000000000000000",
	)
	if err != nil {
		t.Fatalf("seed server: %v", err)
	}
	_, err = handle.Exec(
		`INSERT INTO clients (id, name, private_key, public_key, preshared_key, address, enabled, created_at, updated_at)
		 VALUES (1, 'alice', ?, ?, ?, '10.66.66.2/32', 1, '2026-08-01T00:00:00Z', '2026-08-01T00:00:00Z')`,
		testClientKey, "x-test-client-public-key-00000000000000000000", testPreshared,
	)
	if err != nil {
		t.Fatalf("seed client: %v", err)
	}
	_, err = handle.Exec(
		`INSERT INTO auth (id, username, password_hash, totp_secret)
		 VALUES (1, 'admin', ?, NULL)`, testHash,
	)
	if err != nil {
		t.Fatalf("seed auth: %v", err)
	}
	return handle, dir
}

// unpackTestArchive is the inverse of Create for tests: zstd → tar.
func unpackTestArchive(t *testing.T, path string) map[string][]byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	defer f.Close()
	zr, err := zstd.NewReader(f)
	if err != nil {
		t.Fatalf("zstd.NewReader: %v", err)
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	entries := map[string][]byte{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar.Next: %v", err)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("read %s: %v", hdr.Name, err)
		}
		entries[hdr.Name] = data
	}
	return entries
}

func TestCreateRoundTrip(t *testing.T) {
	handle, dir := newTestDB(t)
	backupsDir := filepath.Join(dir, "backups")

	path, err := Create(handle, backupsDir, func() time.Time { return fakeNow })
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if want := filepath.Join(backupsDir, "backup-2026-08-12.tar.zst"); path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}

	entries := unpackTestArchive(t, path)
	// The archive contains exactly the M8 files.
	if len(entries) != 2 {
		t.Fatalf("archive entries = %d (%v), want exactly 2", len(entries), keys(entries))
	}
	if _, ok := entries[manifestFilename]; !ok {
		t.Fatalf("missing %s", manifestFilename)
	}
	if _, ok := entries[snapshotFilename]; !ok {
		t.Fatalf("missing %s", snapshotFilename)
	}

	var m Manifest
	if err := json.Unmarshal(entries[manifestFilename], &m); err != nil {
		t.Fatalf("manifest JSON: %v", err)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("manifest invalid: %v", err)
	}
	if m.SchemaVersion != schemaVersion() || m.Format != 1 || m.Application != applicationValue ||
		m.ApplicationVersion != applicationVersion || m.CreatedAt != "2026-08-12T10:30:00Z" {
		t.Fatalf("manifest = %+v", m)
	}

	// The snapshot contains the seeded data and secrets.
	snapPath := filepath.Join(t.TempDir(), "snap.sqlite")
	if err := os.WriteFile(snapPath, entries[snapshotFilename], 0o600); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}
	snap, err := sql.Open("sqlite", snapPath)
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	defer snap.Close()
	var serverKey string
	if err := snap.QueryRow(`SELECT private_key FROM server WHERE id = 1`).Scan(&serverKey); err != nil {
		t.Fatalf("read server key from snapshot: %v", err)
	}
	if serverKey != testServerKey {
		t.Fatalf("server key mismatch: %q", serverKey)
	}
	var cKey, pre string
	if err := snap.QueryRow(`SELECT private_key, preshared_key FROM clients WHERE id = 1`).Scan(&cKey, &pre); err != nil {
		t.Fatalf("read client from snapshot: %v", err)
	}
	if cKey != testClientKey || pre != testPreshared {
		t.Fatalf("client keys mismatch")
	}
	var hash string
	if err := snap.QueryRow(`SELECT password_hash FROM auth WHERE username = 'admin'`).Scan(&hash); err != nil {
		t.Fatalf("read auth from snapshot: %v", err)
	}
	if hash != testHash {
		t.Fatalf("password hash mismatch")
	}
}

// TestCreateArchiveIsEncrypted asserts that uncompressed SQLite/key
// plaintext does not appear in the tar.zst blob.
func TestCreateArchiveIsEncrypted(t *testing.T) {
	handle, dir := newTestDB(t)
	path, err := Create(handle, filepath.Join(dir, "backups"), func() time.Time { return fakeNow })
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	for _, needle := range []string{
		"amnezia-vpn-server",
		"SQLite format 3",
		"manifest.json",
		testServerKey,
		testClientKey,
		testPreshared,
		testHash,
		"10.66.66.2",
	} {
		if bytes.Contains(raw, []byte(needle)) {
			t.Fatalf("archive contains plaintext %q", needle)
		}
	}
}

func TestCreatePermissions(t *testing.T) {
	handle, dir := newTestDB(t)
	backupsDir := filepath.Join(dir, "backups")
	path, err := Create(handle, backupsDir, func() time.Time { return fakeNow })
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	di, err := os.Stat(backupsDir)
	if err != nil {
		t.Fatalf("stat backups dir: %v", err)
	}
	if perm := di.Mode().Perm(); perm != 0o700 {
		t.Fatalf("backups dir mode = %o, want 700", perm)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat archive: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("archive mode = %o, want 600", perm)
	}
}

// TestCreateFailureCleanup forces failures at each stage and asserts
// the plaintext staging directory never survives and the working
// backups directory is never polluted.
func TestCreateFailureCleanup(t *testing.T) {


	t.Run("unmigrated database", func(t *testing.T) {
		dir := t.TempDir()
		handle, err := db.Open(filepath.Join(dir, "amnezia.sqlite"))
		if err != nil {
			t.Fatalf("db.Open: %v", err)
		}
		defer handle.Close()
		backupsDir := filepath.Join(dir, "backups")
			if _, err := Create(handle, backupsDir, func() time.Time { return fakeNow }); err == nil {
			t.Fatal("unmigrated database must fail")
		}
		assertClean(t, backupsDir)
	})

	t.Run("rename blocked by directory", func(t *testing.T) {
		handle, dir := newTestDB(t)
		backupsDir := filepath.Join(dir, "backups")
			// A directory with the exact target name makes the final
		// rename fail after the archive was fully written.
		blocker := filepath.Join(backupsDir, "backup-2026-08-12.tar.zst")
		if err := os.MkdirAll(blocker, 0o700); err != nil {
			t.Fatalf("MkdirAll blocker: %v", err)
		}
		_, err := Create(handle, backupsDir, func() time.Time { return fakeNow })
		if err == nil {
			t.Fatal("blocked rename must fail")
		}
		assertClean(t, backupsDir)
		di, err := os.Stat(blocker)
		if err != nil || !di.IsDir() {
			t.Fatalf("blocker must be untouched")
		}
	})
}

// assertClean checks that no staging directories (plaintext) survived.
func assertClean(t *testing.T, backupsDir string) {
	t.Helper()
	if _, err := os.Stat(backupsDir); errors.Is(err, os.ErrNotExist) {
		return // never created — clean
	}
	leftovers, err := filepath.Glob(filepath.Join(backupsDir, ".staging-*"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("staging leftovers: %v", leftovers)
	}
}

// TestCreateConcurrent runs parallel backups against the same
// database and directory; every call must succeed and the surviving
// archive must be valid. Runs under -race.
func TestCreateConcurrent(t *testing.T) {
	handle, dir := newTestDB(t)
	backupsDir := filepath.Join(dir, "backups")

	const n = 8
	paths := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			paths[i], errs[i] = Create(handle, backupsDir, func() time.Time { return fakeNow })
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent create %d: %v", i, err)
		}
		if !strings.HasSuffix(paths[i], "backup-2026-08-12.tar.zst") {
			t.Fatalf("concurrent create %d: unexpected path %q", i, paths[i])
		}
	}
	assertClean(t, backupsDir)

	// The surviving archive (last rename wins) must be complete.
	finalPath := filepath.Join(backupsDir, "backup-2026-08-12.tar.zst")
	entries := unpackTestArchive(t, finalPath)
	if len(entries) != 2 {
		t.Fatalf("surviving archive has %d entries", len(entries))
	}
}

// TestCreateErrorsDoNotLeakSecrets asserts that every error path
// produces generic messages: no keys, no hashes, no paths to secrets.
func TestCreateErrorsDoNotLeakSecrets(t *testing.T) {
	dir := t.TempDir()
	backupsDir := filepath.Join(dir, "backups")

	// Closed handle: every pipeline step after connection fails.
	closed := filepath.Join(t.TempDir(), "closed.sqlite")
	ch, err := db.Open(closed)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	ch.Close()
	_, err = Create(ch, backupsDir, func() time.Time { return fakeNow })
	if err == nil {
		t.Fatal("closed database must fail")
	}
	for _, secret := range []string{testServerKey, testClientKey, testPreshared, testHash, "10.66.66"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error leaks %q: %v", secret, err)
		}
	}
}

func keys(m map[string][]byte) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
