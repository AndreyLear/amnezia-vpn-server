package cli

// M8.3 CLI backup tests:
//   - backup create produces a valid .tar.zst.age (decryptable with the
//     matching identity, exactly the M8 entries)
//   - a second create the same day succeeds and replaces the archive
//     atomically (M8.2 same-day overwrite, Q8 open)
//   - backup list prints only real backups, sorted; empty/missing
//     directory is an empty list (exit 0)
//   - suspicious entries (bad dates, wrong shapes, directories,
//     symlinks pointing outside, nested paths) never appear in the
//     output and are never followed
//   - usage errors exit 2, runtime errors exit 1
//   - stdout/stderr never carry secrets (keys, hash, AGE_RECIPIENT)

import (
	"archive/tar"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/backup"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
	"github.com/klauspost/compress/zstd"
)

// setBackupsPath points the backups directory at the test temp dir.
func setBackupsPath(t *testing.T, c *ctx) string {
	t.Helper()
	dir := filepath.Join(c.dir, "backups")
	t.Setenv("AMNEZIA_BACKUPS_PATH", dir)
	return dir
}

// setRecipient installs a fresh test identity as AGE_RECIPIENT and
// returns it (identity is test-only, never produced in production).
func setRecipient(t *testing.T) *age.X25519Identity {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("GenerateX25519Identity: %v", err)
	}
	t.Setenv("AGE_RECIPIENT", id.Recipient().String())
	return id
}

// decryptEntries is the inverse of backup.Create for tests: age →
// zstd → tar, returning entry name → content.
func decryptEntries(t *testing.T, path string, id *age.X25519Identity) map[string][]byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	defer f.Close()
	rd, err := age.Decrypt(f, id)
	if err != nil {
		t.Fatalf("age.Decrypt: %v", err)
	}
	zr, err := zstd.NewReader(rd)
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

// assertSecretFree fails when secret material appears in the given
// output streams. recipient is the value of AGE_RECIPIENT, which must
// never be echoed.
func assertSecretFree(t *testing.T, streams string, secret string, recipient string) {
	t.Helper()
	if secret != "" && strings.Contains(streams, secret) {
		t.Fatalf("output leaks secret %q", secret)
	}
	if recipient != "" && strings.Contains(streams, recipient) {
		t.Fatal("output leaks the AGE_RECIPIENT value")
	}
}

func TestBackupCreateValidArchive(t *testing.T) {
	c := newCtx(t)
	backups := setBackupsPath(t, c)
	id := setRecipient(t)
	// seed a client with marked keys so the snapshot provably contains
	// live data
	c.seedServer("", "")
	_, pub, priv, _ := c.seedClient("alice")

	out := c.mustRun("backup", "create")
	name := time.Now().UTC().Format("2006-01-02") + ".tar.zst.age"
	want := "panel backup create: " + filepath.Join(backups, "backup-"+name) + "\n"
	if out != want {
		t.Fatalf("stdout = %q, want %q", out, want)
	}
	archive := filepath.Join(backups, "backup-"+name)
	fi, err := os.Stat(archive)
	if err != nil {
		t.Fatalf("archive missing: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("archive mode = %o, want 600", perm)
	}

	entries := decryptEntries(t, archive, id)
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want exactly 2", len(entries))
	}
	var m backup.Manifest
	if err := json.Unmarshal(entries["manifest.json"], &m); err != nil {
		t.Fatalf("manifest JSON: %v", err)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("manifest invalid: %v", err)
	}
	snapPath := filepath.Join(t.TempDir(), "snap.sqlite")
	if err := os.WriteFile(snapPath, entries["amnezia.sqlite"], 0o600); err != nil {
		t.Fatal(err)
	}
	snap, err := sql.Open("sqlite", snapPath)
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	defer snap.Close()
	var gotPub string
	if err := snap.QueryRow(`SELECT public_key FROM clients WHERE name = 'alice'`).Scan(&gotPub); err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if gotPub != pub {
		t.Fatalf("snapshot public_key = %q, want %q", gotPub, pub)
	}
	if priv == "" || pub == "" {
		t.Fatal("seed failed")
	}
}

func TestBackupCreateSecondRunOverwrites(t *testing.T) {
	c := newCtx(t)
	backups := setBackupsPath(t, c)
	id := setRecipient(t)
	c.seedServer("", "")

	if out := c.mustRun("backup", "create"); !strings.HasPrefix(out, "panel backup create: ") {
		t.Fatalf("first stdout = %q", out)
	}
	if out := c.mustRun("backup", "create"); !strings.HasPrefix(out, "panel backup create: ") {
		t.Fatalf("second stdout = %q", out)
	}
	entries, err := os.ReadDir(backups)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("backups dir has %d entries, want exactly 1 (same-day overwrite)", len(entries))
	}
	// the surviving archive is complete and valid
	if got := decryptEntries(t, filepath.Join(backups, entries[0].Name()), id); len(got) != 2 {
		t.Fatalf("surviving archive has %d entries", len(got))
	}
}

func TestBackupListShowsOnlyBackupsSorted(t *testing.T) {
	c := newCtx(t)
	backups := setBackupsPath(t, c)
	if err := os.MkdirAll(backups, 0o700); err != nil {
		t.Fatal(err)
	}
	// three archives on different days (raw copies: list never reads
	// content) plus one odd-shaped file
	for _, name := range []string{
		"backup-2026-08-11.tar.zst.age",
		"backup-2026-08-12.tar.zst.age",
		"backup-2026-08-13.tar.zst.age",
		"backup-2026-08-11.tar.zst",
	} {
		if err := os.WriteFile(filepath.Join(backups, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	out := c.mustRun("backup", "list")
	want := []string{
		"backup-2026-08-11.tar.zst.age",
		"backup-2026-08-12.tar.zst.age",
		"backup-2026-08-13.tar.zst.age",
	}
	sort.Strings(want)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != len(want) {
		t.Fatalf("lines = %q, want %v", lines, want)
	}
	for i, w := range want {
		if lines[i] != w {
			t.Fatalf("line %d = %q, want %q", i, lines[i], w)
		}
		if strings.Contains(lines[i], "/") || strings.Contains(lines[i], "..") {
			t.Fatalf("line %d escapes the backups directory: %q", i, lines[i])
		}
	}
}

func TestBackupListMissingAndEmptyDir(t *testing.T) {
	c := newCtx(t)
	setBackupsPath(t, c)
	if out := c.mustRun("backup", "list"); out != "" {
		t.Fatalf("missing dir: stdout = %q, want empty", out)
	}
	if err := os.MkdirAll(filepath.Join(c.dir, "backups"), 0o700); err != nil {
		t.Fatal(err)
	}
	if out := c.mustRun("backup", "list"); out != "" {
		t.Fatalf("empty dir: stdout = %q, want empty", out)
	}
}

func TestBackupListIgnoresSuspiciousEntries(t *testing.T) {
	c := newCtx(t)
	backups := setBackupsPath(t, c)
	if err := os.MkdirAll(backups, 0o700); err != nil {
		t.Fatal(err)
	}
	// the only real backup
	if err := os.WriteFile(
		filepath.Join(backups, "backup-2026-08-12.tar.zst.age"),
		[]byte("real"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	// an outside sentinel a malicious symlink could point at; its
	// content must never surface anywhere
	outside := filepath.Join(c.dir, "outside-secret.txt")
	if err := os.WriteFile(outside, []byte("TOP-SECRET-CONTENT"), 0o600); err != nil {
		t.Fatal(err)
	}
	entries := []string{
		"backup-2026-13-99.tar.zst.age",   // impossible date
		"backup-evil.tar.zst.age",         // wrong date shape
		"evil.tar.zst.age",                // wrong prefix
		"backup-2026-08-12.tar.zst.age.x", // wrong suffix
	}
	for _, name := range entries {
		if err := os.WriteFile(filepath.Join(backups, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// ".." and names with separators cannot exist as single directory
	// entries; the directory entry below simulates the deepest such
	// attack: an entry carrying a path that must never be printed
	// a directory and a nested file with a valid-looking name
	if err := os.MkdirAll(filepath.Join(backups, "backup-2026-08-10.tar.zst.age"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(backups, "backup-2026-08-10.tar.zst.age", "inner"),
		[]byte("x"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	// a symlink with a valid name pointing outside
	if err := os.Symlink(outside, filepath.Join(backups, "backup-2026-08-09.tar.zst.age")); err != nil {
		t.Fatal(err)
	}

	code, out, errb := c.run("backup", "list")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errb)
	}
	if out != "backup-2026-08-12.tar.zst.age\n" {
		t.Fatalf("stdout = %q, want only the real backup", out)
	}
	if strings.Contains(out, "TOP-SECRET-CONTENT") {
		t.Fatal("list leaked the outside sentinel content")
	}
	if strings.Contains(out, "/") || strings.Contains(out, "..") {
		t.Fatalf("list output escapes backups/: %q", out)
	}
	// the sentinel was never followed or modified
	if data, err := os.ReadFile(outside); err != nil || string(data) != "TOP-SECRET-CONTENT" {
		t.Fatal("outside sentinel was touched")
	}
}

func TestBackupUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"backup"},
		{"backup", "frobnicate"},
		{"backup", "create", "extra"},
		{"backup", "create", "--flag"},
		{"backup", "list", "extra"},
		{"backup", "list", "--all"},
	} {
		c := newCtx(t)
		code, out, errb := c.run(args...)
		if code != 2 {
			t.Fatalf("%v: exit = %d, want 2", args, code)
		}
		if out != "" {
			t.Fatalf("%v: stdout = %q, want empty", args, out)
		}
		if !strings.Contains(errb, "panel backup") {
			t.Fatalf("%v: stderr = %q", args, errb)
		}
	}
}

func TestBackupCreateMissingRecipient(t *testing.T) {
	c := newCtx(t)
	setBackupsPath(t, c)
	t.Setenv("AGE_RECIPIENT", "")
	code, out, errb := c.run("backup", "create")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if out != "" {
		t.Fatalf("stdout = %q, want empty", out)
	}
	if !strings.Contains(errb, "AGE_RECIPIENT") {
		t.Fatalf("stderr = %q, want AGE_RECIPIENT diagnostic", errb)
	}
}

func TestBackupCreateInvalidRecipient(t *testing.T) {
	c := newCtx(t)
	setBackupsPath(t, c)
	t.Setenv("AGE_RECIPIENT", "not-an-age-key")
	code, out, errb := c.run("backup", "create")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if out != "" {
		t.Fatalf("stdout = %q, want empty", out)
	}
	if !strings.Contains(errb, "panel backup create:") || !strings.Contains(errb, "recipient") {
		t.Fatalf("stderr = %q", errb)
	}
}

func TestBackupCreateClosedDatabaseFails(t *testing.T) {
	c := newCtx(t)
	setBackupsPath(t, c)
	setRecipient(t)
	// a path that can never be a database: parent is a regular file
	blocker := filepath.Join(c.dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AMNEZIA_DB_PATH", filepath.Join(blocker, "db.sqlite"))
	code, out, errb := c.run("backup", "create")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if out != "" {
		t.Fatalf("stdout = %q, want empty", out)
	}
	if !strings.Contains(errb, "panel backup create:") {
		t.Fatalf("stderr = %q", errb)
	}
}

// TestBackupSecretFreeStreams seeds the database with marked secrets
// and asserts no stream of create or list ever echoes them, the
// password hash, or the recipient value.
func TestBackupSecretFreeStreams(t *testing.T) {
	markedPriv := formKey(0xAB)
	markedPSK := formKey(0xCD)
	markedHash := "$argon2id$v=19$m=65536,t=1,p=4$" + formKey(0xEF)
	c := newCtx(t)
	backups := setBackupsPath(t, c)
	id := setRecipient(t)
	c.seedServer("", "")
	h := c.openDB()
	defer h.Close()
	if _, err := h.Exec(
		`INSERT INTO clients (name, private_key, public_key, preshared_key, address, enabled, created_at, updated_at)
		 VALUES ('marked', ?, ?, ?, '10.8.0.2/32', 1, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		markedPriv, formKey(0x11), markedPSK,
	); err != nil {
		t.Fatalf("insert client: %v", err)
	}
	if _, err := h.Exec(
		`INSERT INTO auth (username, password_hash, totp_secret) VALUES ('marked', ?, NULL)`,
		markedHash,
	); err != nil {
		t.Fatalf("insert auth: %v", err)
	}

	code, out, errb := c.run("backup", "create")
	if code != 0 {
		t.Fatalf("create: exit = %d, stderr = %s", code, errb)
	}
	assertSecretFree(t, out+errb, markedPriv, id.Recipient().String())
	assertSecretFree(t, out+errb, markedPSK, "")
	assertSecretFree(t, out+errb, markedHash, "")
	if !strings.HasPrefix(out, "panel backup create: "+backups) {
		t.Fatalf("create stdout = %q", out)
	}

	code, out, errb = c.run("backup", "list")
	if code != 0 {
		t.Fatalf("list: exit = %d, stderr = %s", code, errb)
	}
	assertSecretFree(t, out+errb, markedPriv, id.Recipient().String())
	assertSecretFree(t, out+errb, markedPSK, "")
	assertSecretFree(t, out+errb, markedHash, "")

	// the archive file itself must not carry plaintext markers either
	entries, err := os.ReadDir(backups)
	if err != nil || len(entries) != 1 {
		t.Fatalf("backups dir: %v, %v", entries, err)
	}
	raw, err := os.ReadFile(filepath.Join(backups, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{markedPriv, markedPSK, markedHash, "SQLite format 3"} {
		if strings.Contains(string(raw), needle) {
			t.Fatalf("archive contains plaintext %q", needle)
		}
	}
}

// TestBackupListDoesNotOpenDatabase pins that `backup list` needs no
// database at all: with an impossible AMNEZIA_DB_PATH the listing still
// works.
func TestBackupListDoesNotOpenDatabase(t *testing.T) {
	c := newCtx(t)
	backups := setBackupsPath(t, c)
	if err := os.MkdirAll(backups, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backups, "backup-2026-08-12.tar.zst.age"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AMNEZIA_DB_PATH", filepath.Join(c.dir, "blocked", "x.sqlite"))
	if out := c.mustRun("backup", "list"); out != "backup-2026-08-12.tar.zst.age\n" {
		t.Fatalf("stdout = %q", out)
	}
}

// TestBackupCreatePreservesDatabase pins that create is read-only for
// the live database: schema_version and rows are untouched.
func TestBackupCreatePreservesDatabase(t *testing.T) {
	c := newCtx(t)
	setBackupsPath(t, c)
	setRecipient(t)
	c.seedServer("", "")
	c.seedClient("alice")
	before, err := os.ReadFile(c.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	c.mustRun("backup", "create")
	after, err := os.ReadFile(c.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("backup create modified the live database")
	}
	rec := c.clientByID(1)
	if rec.Name != "alice" {
		t.Fatalf("client after backup = %+v", rec)
	}
}

func TestBackupListNoDatabasePathExists(t *testing.T) {
	c := newCtx(t)
	setBackupsPath(t, c)
	if _, err := db.Open(c.dbPath); err != nil {
		t.Fatal(err)
	}
	if out := c.mustRun("backup", "list"); out != "" {
		t.Fatalf("stdout = %q, want empty", out)
	}
}
