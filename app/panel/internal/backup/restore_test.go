package backup

// M8.4 restore preparation tests:
//   - valid restore: pending image, safety backup, marker, untouched DB
//   - wrong identity / corrupted .age / corrupted tar-zstd body
//   - missing manifest.json / missing amnezia.sqlite / extra file /
//     duplicate entry
//   - path traversal, absolute paths, nested names, symlinks, hard
//     links, directories
//   - invalid manifest fields, disagreement between manifest and stored
//     schema_version
//   - corrupted SQLite image (integrity_check)
//   - every failure leaves the live database byte-identical and no
//     marker behind (and a later valid restore still succeeds)
//   - safety-backup failure leaves the state untouched
//   - a pending restore blocks a second one (Q14)
//   - concurrent restores: exactly one wins, no corruption (Q14)
//   - the identity never lands on disk; errors never leak secrets

import (
	"archive/tar"
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
	"github.com/klauspost/compress/zstd"
)

const validManifestJSON = `{"format":1,"application":"amnezia-vpn-server","application_version":"2.0.0","schema_version":3,"created_at":"2026-08-12T10:30:00Z"}` + "\n"

// archiveEntry is a raw tar entry for buildArchive.
type archiveEntry struct {
	name string
	typ  byte
	link string // symlink/hardlink target
	data []byte
}

// buildArchive produces a .tar.zst.age file with exactly the given
// entries (any names/types/links — used to craft hostile archives).
func buildArchive(t *testing.T, id *age.X25519Identity, entries []archiveEntry) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "crafted.tar.zst.age")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := age.Encrypt(f, id.Recipient())
	if err != nil {
		t.Fatal(err)
	}
	zw, err := zstd.NewWriter(enc)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(zw)
	for _, e := range entries {
		hdr := &tar.Header{
			Name:     e.name,
			Typeflag: e.typ,
			Mode:     0o600,
			ModTime:  time.Unix(0, 0).UTC(),
			Linkname: e.link,
		}
		if e.typ == tar.TypeReg {
			hdr.Size = int64(len(e.data))
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if e.typ == tar.TypeReg {
			if _, err := tw.Write(e.data); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// validEntries returns manifest.json + a clean migrated amnezia.sqlite
// (stored schema_version set to wantStored; "" for tampered none).
func validEntries(t *testing.T, wantStored string) []archiveEntry {
	t.Helper()
	return []archiveEntry{
		{name: manifestFilename, typ: tar.TypeReg, data: []byte(validManifestJSON)},
		{name: snapshotFilename, typ: tar.TypeReg, data: sqliteFileBytes(t, wantStored)},
	}
}

// sqliteFileBytes returns the raw bytes of a migrated database whose
// stored schema_version is set to schemaVersion ("3" normally; other
// values craft the manifest/stored disagreement test).
func sqliteFileBytes(t *testing.T, schemaVersion string) []byte {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "src.sqlite")
	handle, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	if err := db.Migrate(handle); err != nil {
		t.Fatal(err)
	}
	if schemaVersion != "" {
		if _, err := handle.Exec(
			`UPDATE schema_meta SET value = ? WHERE key = 'schema_version'`,
			schemaVersion,
		); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// restoreCtx is the shared fixture: a seeded live database, its path,
// the backups directory and the age identity/recipient pair.
type restoreCtx struct {
	t           *testing.T
	dbPath      string
	backupsDir  string
	id          *age.X25519Identity
	recipient   string
	seedCounter int
}

func newRestoreCtx(t *testing.T) *restoreCtx {
	t.Helper()
	dir := t.TempDir()
	handle, err := db.Open(filepath.Join(dir, "amnezia.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { handle.Close() })
	if err := db.Migrate(handle); err != nil {
		t.Fatal(err)
	}
	if _, err := handle.Exec(
		`INSERT INTO server (id, private_key, public_key, address, listen_port, dns, awg_params, created_at, updated_at)
		 VALUES (1, ?, ?, '10.66.66.1/24', 51820, '1.1.1.1', '{}', '2026-08-01T00:00:00Z', '2026-08-01T00:00:00Z')`,
		testServerKey, "x-test-server-public-key-00000000000000000000",
	); err != nil {
		t.Fatal(err)
	}
	id, recipient := newRecipient(t)
	return &restoreCtx{
		t:          t,
		dbPath:     filepath.Join(dir, "amnezia.sqlite"),
		backupsDir: filepath.Join(dir, "backups"),
		id:         id,
		recipient:  recipient,
	}
}

func (c *restoreCtx) reopen() *sql.DB {
	c.t.Helper()
	h, err := db.Open(c.dbPath)
	if err != nil {
		c.t.Fatal(err)
	}
	c.t.Cleanup(func() { h.Close() })
	return h
}

// seedClient inserts a client row into the live database (used to make
// the live state differ from the archived one). Each call uses a fresh
// address so repeated seeding never collides.
func (c *restoreCtx) seedClient(name, key string) {
	c.t.Helper()
	h := c.reopen()
	c.seedCounter++
	if _, err := h.Exec(
		`INSERT INTO clients (name, private_key, public_key, preshared_key, address, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, 1, '2026-08-01T00:00:00Z', '2026-08-01T00:00:00Z')`,
		name, key,
		fmt.Sprintf("x-test-client-public-key-%016d", c.seedCounter),
		fmt.Sprintf("x-test-preshared-key-%016d", c.seedCounter),
		fmt.Sprintf("10.66.66.%d/32", 2+c.seedCounter),
	); err != nil {
		c.t.Fatal(err)
	}
}

// doRestore runs the pipeline with the fixture identity.
func (c *restoreCtx) doRestore(srcPath string) (RestoreResult, error) {
	c.t.Helper()
	h := c.reopen()
	return Restore(h, c.dbPath, srcPath, c.backupsDir, c.recipient, []age.Identity{c.id}, func() time.Time { return fakeNow })
}

func (c *restoreCtx) assertUntouched(before []byte) {
	c.t.Helper()
	after, err := os.ReadFile(c.dbPath)
	if err != nil {
		c.t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		c.t.Fatal("live database was modified")
	}
}

func (c *restoreCtx) assertNoMarker() {
	c.t.Helper()
	_, ok, err := PendingPath(c.dbPath)
	if err != nil {
		c.t.Fatal(err)
	}
	if ok {
		c.t.Fatal("pending marker present after a failed restore")
	}
}

// assertNoNewBackups checks that a failed restore added nothing to the
// backups directory (no safety backup, no leftovers).
func (c *restoreCtx) assertNoNewBackups(before []string) {
	c.t.Helper()
	after, err := os.ReadDir(c.backupsDir)
	if !errors.Is(err, os.ErrNotExist) && err != nil {
		c.t.Fatal(err)
	}
	names := map[string]bool{}
	for _, e := range after {
		names[e.Name()] = true
	}
	for _, b := range before {
		delete(names, b)
	}
	if len(names) != 0 {
		c.t.Fatalf("failed restore left new files behind: %v", keysOf(names))
	}
}

func namesOf(es []os.DirEntry) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}

func keysOf(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

// liveState returns the byte state of the live DB and the current set
// of backup file names.
func (c *restoreCtx) liveState() ([]byte, []string) {
	c.t.Helper()
	before, err := os.ReadFile(c.dbPath)
	if err != nil {
		c.t.Fatal(err)
	}
	entries, err := os.ReadDir(c.backupsDir)
	if !errors.Is(err, os.ErrNotExist) && err != nil {
		c.t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return before, names
}

// checkValidPending asserts the prepared state of a successful restore
// and that the live database still equals before.
func (c *restoreCtx) checkValidPending(res RestoreResult, before []byte, wantClient string, wantNotClient string) {
	c.t.Helper()
	if res.Archive == "" || res.PendingDB == "" || res.SafetyBackup == "" {
		c.t.Fatalf("incomplete result: %+v", res)
	}
	c.assertUntouched(before)

	marker, ok, err := PendingPath(c.dbPath)
	if err != nil || !ok {
		c.t.Fatalf("no pending marker: %v", err)
	}
	if filepath.Base(marker) != pendingDirName {
		c.t.Fatalf("marker path = %q", marker)
	}
	if !strings.HasPrefix(filepath.Base(res.SafetyBackup), "safety-backup-") {
		c.t.Fatalf("safety backup name = %q", filepath.Base(res.SafetyBackup))
	}
	if _, err := os.Stat(res.PendingDB); err != nil {
		c.t.Fatalf("pending image missing: %v", err)
	}
	if fi, err := os.Stat(marker); err != nil || fi.Mode().Perm() != 0o700 {
		c.t.Fatalf("marker dir perms: %v %o", err, fi.Mode().Perm())
	}

	pending, err := sql.Open("sqlite", res.PendingDB)
	if err != nil {
		c.t.Fatal(err)
	}
	defer pending.Close()
	var got string
	if err := pending.QueryRow(`SELECT private_key FROM clients WHERE name = ?`, wantClient).Scan(&got); err != nil {
		c.t.Fatalf("pending image missing %s: %v", wantClient, err)
	}
	if wantNotClient != "" {
		var n int
		if err := pending.QueryRow(`SELECT COUNT(*) FROM clients WHERE name = ?`, wantNotClient).Scan(&n); err != nil || n != 0 {
			c.t.Fatalf("pending image contains %s: n=%d err=%v", wantNotClient, n, err)
		}
	}

	// the safety backup decrypts with the same identity and contains
	// the *new* live state
	entries := decryptArchive(c.t, res.SafetyBackup, c.id)
	if len(entries) != 2 {
		c.t.Fatalf("safety backup entries = %d", len(entries))
	}
	snapPath := filepath.Join(c.t.TempDir(), "safety.sqlite")
	if err := os.WriteFile(snapPath, entries[snapshotFilename], 0o600); err != nil {
		c.t.Fatal(err)
	}
	snap, err := sql.Open("sqlite", snapPath)
	if err != nil {
		c.t.Fatal(err)
	}
	defer snap.Close()
	if wantNotClient != "" {
		var n int
		if err := snap.QueryRow(`SELECT COUNT(*) FROM clients WHERE name = ?`, wantNotClient).Scan(&n); err != nil || n != 1 {
			c.t.Fatalf("safety backup missing %s: n=%d err=%v", wantNotClient, n, err)
		}
	}
}

// TestRestoreValid is the happy path: an archive of the initial state
// is prepared for restore after the live database has moved on.
func TestRestoreValid(t *testing.T) {
	c := newRestoreCtx(t)
	c.seedClient("alice", testClientKey)
	archive, err := Create(c.reopen(), c.backupsDir, c.recipient, func() time.Time { return fakeNow })
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	c.seedClient("bob", "x-test-bob-private-key-000000000000000000000")
	before, _ := c.liveState()

	res, err := c.doRestore(archive)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	c.checkValidPending(res, before, "alice", "bob")
	// the surviving state tells the operator what to do next
	if _, ok, err := PendingPath(c.dbPath); err != nil || !ok {
		t.Fatalf("marker after valid restore: %v", err)
	}
}

// TestRestoreWrongIdentity: a different identity cannot decrypt.
func TestRestoreWrongIdentity(t *testing.T) {
	c := newRestoreCtx(t)
	c.seedClient("alice", testClientKey)
	archive, err := Create(c.reopen(), c.backupsDir, c.recipient, func() time.Time { return fakeNow })
	if err != nil {
		t.Fatal(err)
	}
	other, _ := newRecipient(t)
	before, names := c.liveState()
	h := c.reopen()
	_, err = Restore(h, c.dbPath, archive, c.backupsDir, c.recipient, []age.Identity{other}, func() time.Time { return fakeNow })
	if err == nil {
		t.Fatal("wrong identity must fail")
	}
	c.assertUntouched(before)
	c.assertNoMarker()
	c.assertNoNewBackups(names)
}

// corruptFile flips the byte at position i (mod len) in place.
func corruptFile(t *testing.T, path string, i int) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data[i%len(data)] ^= 0xff
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestRestoreCorruptedAgeFile: a flipped byte inside the .age
// container must fail without touching anything.
func TestRestoreCorruptedAgeFile(t *testing.T) {
	c := newRestoreCtx(t)
	archive := buildArchive(t, c.id, validEntries(t, "3"))
	corruptFile(t, archive, 7)
	before, names := c.liveState()
	if _, err := c.doRestore(archive); err == nil {
		t.Fatal("corrupted .age must fail")
	}
	c.assertUntouched(before)
	c.assertNoMarker()
	c.assertNoNewBackups(names)
}

// TestRestoreCorruptedBody: the .age container decrypts fine, but the
// zstd/tar payload inside is corrupted.
func TestRestoreCorruptedBody(t *testing.T) {
	c := newRestoreCtx(t)
	archive := buildArchive(t, c.id, validEntries(t, "3"))
	f, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := age.Decrypt(f, c.id)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(plain)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	data[700] ^= 0xff
	crafted := filepath.Join(t.TempDir(), "corrupt-body.tar.zst.age")
	out, err := os.OpenFile(crafted, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := age.Encrypt(out, c.id.Recipient())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enc.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}

	before, names := c.liveState()
	if _, err := c.doRestore(crafted); err == nil {
		t.Fatal("corrupted zstd/tar body must fail")
	}
	c.assertUntouched(before)
	c.assertNoMarker()
	c.assertNoNewBackups(names)
}

// TestRestoreMissingManifest: archive with only the database.
func TestRestoreMissingManifest(t *testing.T) {
	c := newRestoreCtx(t)
	archive := buildArchive(t, c.id, []archiveEntry{
		{name: snapshotFilename, typ: tar.TypeReg, data: sqliteFileBytes(t, "3")},
	})
	before, names := c.liveState()
	_, err := c.doRestore(archive)
	if err == nil || !strings.Contains(err.Error(), "manifest.json") {
		t.Fatalf("err = %v, want missing-manifest", err)
	}
	c.assertUntouched(before)
	c.assertNoMarker()
	c.assertNoNewBackups(names)
}

// TestRestoreMissingSQLite: archive with only the manifest.
func TestRestoreMissingSQLite(t *testing.T) {
	c := newRestoreCtx(t)
	archive := buildArchive(t, c.id, []archiveEntry{
		{name: manifestFilename, typ: tar.TypeReg, data: []byte(validManifestJSON)},
	})
	before, names := c.liveState()
	_, err := c.doRestore(archive)
	if err == nil || !strings.Contains(err.Error(), "amnezia.sqlite") {
		t.Fatalf("err = %v, want missing-sqlite", err)
	}
	c.assertUntouched(before)
	c.assertNoMarker()
	c.assertNoNewBackups(names)
}

// TestRestoreExtraFile: a third entry is rejected (Q15).
func TestRestoreExtraFile(t *testing.T) {
	c := newRestoreCtx(t)
	archive := buildArchive(t, c.id, append(validEntries(t, "3"),
		archiveEntry{name: "evil.txt", typ: tar.TypeReg, data: []byte("x")}))
	before, names := c.liveState()
	_, err := c.doRestore(archive)
	if err == nil || !strings.Contains(err.Error(), "unexpected entry") {
		t.Fatalf("err = %v, want unexpected-entry", err)
	}
	c.assertUntouched(before)
	c.assertNoMarker()
	c.assertNoNewBackups(names)
}

// TestRestoreDuplicate: a second manifest.json is rejected.
func TestRestoreDuplicate(t *testing.T) {
	c := newRestoreCtx(t)
	archive := buildArchive(t, c.id, []archiveEntry{
		{name: manifestFilename, typ: tar.TypeReg, data: []byte(validManifestJSON)},
		{name: manifestFilename, typ: tar.TypeReg, data: []byte(validManifestJSON)},
		{name: snapshotFilename, typ: tar.TypeReg, data: sqliteFileBytes(t, "3")},
	})
	before, names := c.liveState()
	_, err := c.doRestore(archive)
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("err = %v, want duplicate", err)
	}
	c.assertUntouched(before)
	c.assertNoMarker()
	c.assertNoNewBackups(names)
}

// TestRestoreTraversalAndSpecial: hostile entry names and entry types.
func TestRestoreTraversalAndSpecial(t *testing.T) {
	hostile := []archiveEntry{
		{name: "../evil", typ: tar.TypeReg, data: []byte("x")},
		{name: "/tmp/evil", typ: tar.TypeReg, data: []byte("x")},
		{name: "a/b/evil", typ: tar.TypeReg, data: []byte("x")},
		{name: "evil", typ: tar.TypeReg, data: []byte("x")},
		{name: manifestFilename, typ: tar.TypeSymlink, link: "/etc/passwd"},
		{name: snapshotFilename, typ: tar.TypeSymlink, link: "/etc/passwd"},
		{name: manifestFilename, typ: tar.TypeReg, data: []byte("x")}, // duplicate shape
		{name: manifestFilename + "/", typ: tar.TypeDir},
		{name: snapshotFilename + "_", typ: tar.TypeReg, data: []byte("x")},
	}
	for _, e := range hostile {
		c := newRestoreCtx(t)
		archive := buildArchive(t, c.id, []archiveEntry{e})
		before, names := c.liveState()
		if _, err := c.doRestore(archive); err == nil {
			t.Fatalf("entry %q type %d must be rejected", e.name, e.typ)
		}
		c.assertUntouched(before)
		c.assertNoMarker()
		c.assertNoNewBackups(names)
	}
}

// TestRestoreInvalidManifest: broken contract fields.
func TestRestoreInvalidManifest(t *testing.T) {
	bad := []string{
		`{"format":2,"application":"amnezia-vpn-server","application_version":"2.0.0","schema_version":3,"created_at":"2026-08-12T10:30:00Z"}` + "\n",
		`{"format":1,"application":"other","application_version":"2.0.0","schema_version":3,"created_at":"2026-08-12T10:30:00Z"}` + "\n",
		`{"format":1,"application":"amnezia-vpn-server","application_version":"9.9.9","schema_version":3,"created_at":"2026-08-12T10:30:00Z"}` + "\n",
		`{"format":1,"application":"amnezia-vpn-server","application_version":"2.0.0","schema_version":3,"created_at":"not-a-time"}` + "\n",
		`{`,
	}
	for _, m := range bad {
		c := newRestoreCtx(t)
		archive := buildArchive(t, c.id, []archiveEntry{
			{name: manifestFilename, typ: tar.TypeReg, data: []byte(m)},
			{name: snapshotFilename, typ: tar.TypeReg, data: sqliteFileBytes(t, "3")},
		})
		before, names := c.liveState()
		if _, err := c.doRestore(archive); err == nil {
			t.Fatalf("manifest %q must be rejected", m)
		}
		c.assertUntouched(before)
		c.assertNoMarker()
		c.assertNoNewBackups(names)
	}
}

// TestRestoreWrongSchemaVersion: the manifest declares 77 (already
// rejected by the contract) and the stored version disagrees with the
// manifest (Q6 exact-match).
func TestRestoreWrongSchemaVersion(t *testing.T) {
	c := newRestoreCtx(t)
	// manifest 77 → contract rejection before any state change
	badManifest := `{"format":1,"application":"amnezia-vpn-server","application_version":"2.0.0","schema_version":77,"created_at":"2026-08-12T10:30:00Z"}` + "\n"
	archive := buildArchive(t, c.id, []archiveEntry{
		{name: manifestFilename, typ: tar.TypeReg, data: []byte(badManifest)},
		{name: snapshotFilename, typ: tar.TypeReg, data: sqliteFileBytes(t, "3")},
	})
	before, names := c.liveState()
	if _, err := c.doRestore(archive); err == nil {
		t.Fatal("schema_version 77 must be rejected")
	}
	c.assertUntouched(before)
	c.assertNoMarker()
	c.assertNoNewBackups(names)

	// manifest 3 but the image stores 2 → stored/manifest disagreement
	c2 := newRestoreCtx(t)
	archive2 := buildArchive(t, c2.id, validEntries(t, "2"))
	before2, names2 := c2.liveState()
	if _, err := c2.doRestore(archive2); err == nil {
		t.Fatal("stored schema_version 2 with manifest 3 must be rejected")
	}
	c2.assertUntouched(before2)
	c2.assertNoMarker()
	c2.assertNoNewBackups(names2)
}

// TestRestoreCorruptedSQLite: the image inside a valid archive is
// damaged (truncated mid-page), integrity_check must refuse it.
func TestRestoreCorruptedSQLite(t *testing.T) {
	c := newRestoreCtx(t)
	data := sqliteFileBytes(t, "3")
	data = data[:len(data)-777] // cut whole pages + a partial one
	archive := buildArchive(t, c.id, []archiveEntry{
		{name: manifestFilename, typ: tar.TypeReg, data: []byte(validManifestJSON)},
		{name: snapshotFilename, typ: tar.TypeReg, data: data},
	})
	before, names := c.liveState()
	_, err := c.doRestore(archive)
	if err == nil || !strings.Contains(err.Error(), "integrity") {
		t.Fatalf("err = %v, want integrity failure", err)
	}
	c.assertUntouched(before)
	c.assertNoMarker()
	c.assertNoNewBackups(names)
}

// TestRestoreSafetyBackupFailure: the safety backup cannot be written
// (backups path is blocked by a regular file) — everything before it
// succeeded, but no state may change.
func TestRestoreSafetyBackupFailure(t *testing.T) {
	c := newRestoreCtx(t)
	c.seedClient("alice", testClientKey)
	archive := buildArchive(t, c.id, validEntries(t, "3"))
	before, _ := c.liveState()
	blocker := filepath.Join(c.t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := c.reopen()
	_, err := Restore(h, c.dbPath, archive, blocker, c.recipient, []age.Identity{c.id}, func() time.Time { return fakeNow })
	if err == nil {
		t.Fatal("blocked safety backup must fail")
	}
	c.assertUntouched(before)
	c.assertNoMarker()
	c.assertNoNewBackups(nil)
}

// TestRestoreFailedLeavesCleanState: after any failure the marker is
// gone and the next valid restore works.
func TestRestoreFailedLeavesCleanState(t *testing.T) {
	c := newRestoreCtx(t)
	c.seedClient("alice", testClientKey)
	good := buildArchive(t, c.id, validEntries(t, "3"))
	bad := buildArchive(t, c.id, validEntries(t, "3"))
	corruptFile(t, bad, 3)
	if _, err := c.doRestore(bad); err == nil {
		t.Fatal("bad archive must fail")
	}
	c.assertNoMarker()
	res, err := c.doRestore(good)
	if err != nil {
		t.Fatalf("restore after failure: %v", err)
	}
	if res.PendingDB == "" || res.SafetyBackup == "" {
		t.Fatalf("incomplete result: %+v", res)
	}
	_, isPending, err := PendingPath(c.dbPath)
	if err != nil || !isPending {
		t.Fatalf("marker missing after valid restore: %v", err)
	}
}

// TestRestorePendingBlocksSecond: while a restore is pending, a second
// restore is refused (Q14).
func TestRestorePendingBlocksSecond(t *testing.T) {
	c := newRestoreCtx(t)
	c.seedClient("alice", testClientKey)
	archive, err := Create(c.reopen(), c.backupsDir, c.recipient, func() time.Time { return fakeNow })
	if err != nil {
		t.Fatal(err)
	}
	before, _ := c.liveState()
	if _, err := c.doRestore(archive); err != nil {
		t.Fatalf("first restore: %v", err)
	}
	_, names := c.liveState()
	_, err = c.doRestore(archive)
	if !errors.Is(err, ErrRestorePending) {
		t.Fatalf("second restore err = %v, want ErrRestorePending", err)
	}
	c.assertUntouched(before)
	c.assertNoNewBackups(names)
	_, isPending, err := PendingPath(c.dbPath)
	if err != nil || !isPending {
		t.Fatalf("marker lost on refused restore: %v", err)
	}
}

// TestRestoreConcurrent: two parallel restores of the same archive —
// the pending directory is the lock, so exactly one wins and the loser
// gets ErrRestorePending; no corruption either way (Q14).
func TestRestoreConcurrent(t *testing.T) {
	c := newRestoreCtx(t)
	c.seedClient("alice", testClientKey)
	archive, err := Create(c.reopen(), c.backupsDir, c.recipient, func() time.Time { return fakeNow })
	if err != nil {
		t.Fatal(err)
	}
	before, _ := c.liveState()
	var wg sync.WaitGroup
	results := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, results[i] = c.doRestore(archive)
		}(i)
	}
	wg.Wait()
	ok, pending := 0, 0
	for _, err := range results {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrRestorePending):
			pending++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if ok != 1 || pending != 1 {
		t.Fatalf("results: ok=%d pending=%d, want 1/1", ok, pending)
	}
	c.assertUntouched(before)
	_, isPending, err := PendingPath(c.dbPath)
	if err != nil || !isPending {
		t.Fatalf("no marker after concurrent restore: %v", err)
	}
	// exactly one safety backup (the pre-existing archive file is not
	// one), always decryptable
	entries, err := os.ReadDir(c.backupsDir)
	if err != nil {
		t.Fatal(err)
	}
	var safety string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "safety-backup-") {
			if safety != "" {
				t.Fatalf("multiple safety backups: %v", namesOf(entries))
			}
			safety = e.Name()
		}
	}
	if safety == "" {
		t.Fatalf("no safety backup among %v", namesOf(entries))
	}
	if got := decryptArchive(t, filepath.Join(c.backupsDir, safety), c.id); len(got) != 2 {
		t.Fatalf("safety backup entries = %d", len(got))
	}
}

// TestRestoreIdentityNotOnDisk: after a successful restore the
// identity string appears nowhere under the fixture directory.
func TestRestoreIdentityNotOnDisk(t *testing.T) {
	c := newRestoreCtx(t)
	c.seedClient("alice", testClientKey)
	archive := buildArchive(t, c.id, validEntries(t, "3"))
	if _, err := c.doRestore(archive); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	secret := c.id.String()
	var found []string
	err := filepath.Walk(filepath.Dir(c.dbPath), func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		if bytes.Contains(data, []byte(secret)) {
			found = append(found, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Fatalf("identity leaked onto disk: %v", found)
	}
}

// TestRestoreErrorsDoNotLeakSecrets: failing paths must not echo key
// material, hashes or the identity string.
func TestRestoreErrorsDoNotLeakSecrets(t *testing.T) {
	markedPriv := testClientKey
	c := newRestoreCtx(t)
	c.seedClient("alice", markedPriv+"-broken")
	archive2 := buildArchive(t, c.id, validEntries(t, "2")) // schema mismatch
	corrupt := buildArchive(t, c.id, validEntries(t, "3"))
	corruptFile(t, corrupt, 5)
	identityStr := c.id.String()

	for _, src := range []string{archive2, corrupt} {
		_, err := c.doRestore(src)
		if err == nil {
			t.Fatalf("%s must fail", src)
		}
		for _, secret := range []string{markedPriv, identityStr, "$argon2id$", "x-test-preshared-key"} {
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("error leaks %q: %v", secret, err)
			}
		}
	}
}

// TestRestoreRejectsSpecialTypes: every non-regular tar entry type is
// refused with a diagnostic naming the type (the message never echoes
// file content).
func TestRestoreRejectsSpecialTypes(t *testing.T) {
	cases := []struct {
		typ  byte
		link string
		diag string
	}{
		{tar.TypeDir, "", "a directory"},
		{tar.TypeSymlink, "/etc/passwd", "a symlink"},
		{tar.TypeLink, "manifest.json", "a hard link"},
		{tar.TypeChar, "", "a character device"},
		{tar.TypeBlock, "", "a block device"},
		{tar.TypeFifo, "", `type "6"`},
	}
	for _, tc := range cases {
		c := newRestoreCtx(t)
		archive := buildArchive(t, c.id, []archiveEntry{
			{name: manifestFilename, typ: tc.typ, link: tc.link},
		})
		before, names := c.liveState()
		_, err := c.doRestore(archive)
		if err == nil {
			t.Fatalf("type %d must be rejected", tc.typ)
		}
		if !strings.Contains(err.Error(), tc.diag) {
			t.Fatalf("type %d: error %q missing %q", tc.typ, err, tc.diag)
		}
		c.assertUntouched(before)
		c.assertNoMarker()
		c.assertNoNewBackups(names)
	}
}
