package cli

// M8.4 CLI restore tests:
//   - `panel restore <backup> --identity-stdin` prepares a restore:
//     the live database is untouched, the pending marker
//     (.restore-pending/amnezia.sqlite, 0600) holds the archived state,
//     and a unique safety backup appears in the backups directory
//   - usage errors exit 2 (missing/extra args, invalid names, absolute
//     paths, missing --identity-stdin, unknown flags)
//   - operational failures exit 1 (missing file, symlink, bad identity
//     on stdin, wrong identity, second restore while pending)
//   - identity, AGE_RECIPIENT and DB key material never reach stdout or
//     stderr, on success or failure

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

// seedRestoreState creates a server + one client, produces a real
// backup archive through the CLI, then mutates the live database by
// adding a second client. Returns the archive name, the identity that
// unlocks it, and the pending directory expected next to the db.
func seedRestoreState(t *testing.T) (c *ctx, backups string, name string, id *age.X25519Identity, idStr string, pendingDir string) {
	t.Helper()
	c = newCtx(t)
	backups = setBackupsPath(t, c)
	id = setRecipient(t)
	idStr = id.String()
	c.seedServer("", "")
	c.seedClient("alice")
	out := c.mustRun("backup", "create")
	name = filepath.Base(strings.TrimSuffix(strings.TrimSpace(out), "\n"))
	pendingDir = filepath.Join(filepath.Dir(c.dbPath), ".restore-pending")
	// mutate the live database after the snapshot
	c.mustRun("client", "add", "bob")
	return c, backups, name, id, idStr, pendingDir
}

// clientCount returns the number of client rows in the given database.
func clientCount(t *testing.T, path string) int {
	t.Helper()
	h, err := db.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer h.Close()
	if err := db.Migrate(h); err != nil {
		t.Fatalf("migrate %s: %v", path, err)
	}
	recs, err := db.ClientsAll(h)
	if err != nil {
		t.Fatalf("clients in %s: %v", path, err)
	}
	return len(recs)
}

func TestRestoreHappyPath(t *testing.T) {
	c, backups, name, id, idStr, pendingDir := seedRestoreState(t)

	code, out, errb := runInput(strings.NewReader(idStr+"\n"), "restore", name, "--identity-stdin")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errb)
	}
	if errb != "" {
		t.Fatalf("stderr = %q, want empty", errb)
	}
	for _, want := range []string{
		"panel restore: backup " + name + " is prepared and pending",
		"panel restore: safety backup: ",
		"panel restore: restart required (pending: " + pendingDir + ")",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout missing %q:\n%s", want, out)
		}
	}
	assertSecretFree(t, out+errb, idStr, id.Recipient().String())

	// the live database is untouched (both clients, including bob)
	if got := clientCount(t, c.dbPath); got != 2 {
		t.Fatalf("live db clients = %d, want 2", got)
	}

	// the pending image is the archived state (alice only), 0600
	if got := clientCount(t, filepath.Join(pendingDir, "amnezia.sqlite")); got != 1 {
		t.Fatalf("pending db clients = %d, want 1", got)
	}
	fi, err := os.Stat(filepath.Join(pendingDir, "amnezia.sqlite"))
	if err != nil {
		t.Fatalf("pending db: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("pending db perms = %o, want 0600", fi.Mode().Perm())
	}

	// a unique, decryptable safety backup appeared
	entries, err := os.ReadDir(backups)
	if err != nil {
		t.Fatal(err)
	}
	var safety string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "safety-backup-") {
			if safety != "" {
				t.Fatalf("multiple safety backups: %v", e.Name())
			}
			safety = e.Name()
		}
	}
	if safety == "" {
		t.Fatalf("no safety backup in %v", entries)
	}
	if got := decryptEntries(t, filepath.Join(backups, safety), id); len(got) != 2 {
		t.Fatalf("safety backup entries = %d, want 2", len(got))
	}
}

func TestRestoreUsageErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"no name", []string{"restore"}},
		{"no name with flag", []string{"restore", "--identity-stdin"}},
		{"extra positional", []string{"restore", "backup-2026-08-12.tar.zst.age", "x", "--identity-stdin"}},
		{"missing flag", []string{"restore", "backup-2026-08-12.tar.zst.age"}},
		{"bad date", []string{"restore", "backup-2026-13-99.tar.zst.age", "--identity-stdin"}},
		{"wrong shape", []string{"restore", "backup-2026-08-12.tar.age", "--identity-stdin"}},
		{"directory name", []string{"restore", "backup-2026-08-12", "--identity-stdin"}},
		{"absolute path", []string{"restore", "/etc/passwd", "--identity-stdin"}},
		{"traversal", []string{"restore", "../backup-2026-08-12.tar.zst.age", "--identity-stdin"}},
		{"unknown flag", []string{"restore", "backup-2026-08-12.tar.zst.age", "--identity-stdin", "--bogus"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			newCtx(t)
			code, out, _ := runInput(strings.NewReader("AGE-SECRET-KEY-1"), tc.args...)
			if code != 2 {
				t.Fatalf("%v: exit = %d, want 2", tc.args, code)
			}
			if out != "" {
				t.Fatalf("%v: stdout = %q, want empty", tc.args, out)
			}
		})
	}
}

func TestRestoreMissingFile(t *testing.T) {
	c := newCtx(t)
	setBackupsPath(t, c)
	setRecipient(t)
	code, out, errb := runInput(strings.NewReader("AGE-SECRET-KEY-1"),
		"restore", "backup-2026-08-12.tar.zst.age", "--identity-stdin")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if out != "" {
		t.Fatalf("stdout = %q, want empty", out)
	}
	if !strings.Contains(errb, "backup file") {
		t.Fatalf("stderr = %q, want backup file diagnostic", errb)
	}
}

func TestRestoreSymlinkRejected(t *testing.T) {
	c, backups, name, _, _, pendingDir := seedRestoreState(t)
	real := filepath.Join(c.dir, "elsewhere.tar.zst.age")
	if err := os.WriteFile(real, []byte("not an archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	// replace the freshly created archive with a symlink pointing
	// outside the backups directory
	if err := os.Remove(filepath.Join(backups, name)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(backups, name)); err != nil {
		t.Fatal(err)
	}
	// restore must refuse the symlink without even opening it
	code, _, errb := runInput(strings.NewReader("AGE-SECRET-KEY-1"),
		"restore", name, "--identity-stdin")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(errb, "not a regular file") {
		t.Fatalf("stderr = %q", errb)
	}
	if _, err := os.Stat(pendingDir); err == nil {
		t.Fatal("marker created for a refused archive")
	}
}

func TestRestoreWrongIdentity(t *testing.T) {
	c, backups, name, _, idStr, pendingDir := seedRestoreState(t)
	other, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	code, _, errb := runInput(strings.NewReader(other.String()+"\n"),
		"restore", name, "--identity-stdin")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(errb, "identity") {
		t.Fatalf("stderr = %q, want identity diagnostic", errb)
	}
	if got := clientCount(t, c.dbPath); got != 2 {
		t.Fatalf("live db clients = %d, want 2", got)
	}
	if _, err := os.Stat(pendingDir); err == nil {
		t.Fatal("marker created on wrong identity")
	}
	entries, err := os.ReadDir(backups)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("new files appeared after failed restore: %v", entries)
	}
	assertSecretFree(t, errb, idStr, c.mustEnvRecipient())
}

// mustEnvRecipient returns the test AGE_RECIPIENT value (used only for
// leak assertions).
func (c *ctx) mustEnvRecipient() string {
	c.t.Helper()
	return os.Getenv("AGE_RECIPIENT")
}

func TestRestoreInvalidIdentityInput(t *testing.T) {
	_, _, name, _, _, pendingDir := seedRestoreState(t)
	for _, tc := range []struct {
		name  string
		stdin string
		want  string
	}{
		{"garbage", "not-a-secret-key\n", "invalid identity"},
		{"empty", "", "no identity"},
		{"whitespace", "   \n", "no identity"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, _, errb := runInput(strings.NewReader(tc.stdin),
				"restore", name, "--identity-stdin")
			if code != 1 {
				t.Fatalf("exit = %d, want 1", code)
			}
			if !strings.Contains(errb, tc.want) {
				t.Fatalf("stderr = %q, want %q", errb, tc.want)
			}
			if _, err := os.Stat(pendingDir); err == nil {
				t.Fatal("marker created despite bad identity")
			}
		})
	}
}

func TestRestorePendingBlocksSecondRun(t *testing.T) {
	_, _, name, _, idStr, pendingDir := seedRestoreState(t)
	if code, _, errb := runInput(strings.NewReader(idStr+"\n"), "restore", name, "--identity-stdin"); code != 0 {
		t.Fatalf("first restore: exit %d, stderr %s", code, errb)
	}
	code, out, errb := runInput(strings.NewReader(idStr+"\n"), "restore", name, "--identity-stdin")
	if code != 1 {
		t.Fatalf("second restore: exit = %d, want 1", code)
	}
	if out != "" {
		t.Fatalf("second restore stdout = %q, want empty", out)
	}
	if !strings.Contains(errb, "pending") {
		t.Fatalf("second restore stderr = %q, want pending diagnostic", errb)
	}
	if _, err := os.Stat(pendingDir); err != nil {
		t.Fatal("marker lost after refused restore")
	}
}

func TestRestoreSecretsNeverLeak(t *testing.T) {
	markedPriv := formKey(0xAB)
	c, _, name, id, idStr, _ := seedRestoreState(t)
	// a marked key in the live database must never surface either
	h, err := db.Open(c.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Exec(
		`INSERT INTO clients (name, private_key, public_key, preshared_key, address, enabled, created_at, updated_at)
		 VALUES ('marked', ?, 'x-marked-public', 'x-marked-psk', '10.8.0.9/32', 1, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		markedPriv,
	); err != nil {
		t.Fatalf("insert marked client: %v", err)
	}
	h.Close()

	// success path
	code, out, errb := runInput(strings.NewReader(idStr+"\n"), "restore", name, "--identity-stdin")
	if code != 0 {
		t.Fatalf("restore: exit %d, stderr %s", code, errb)
	}
	streams := out + errb
	for _, secret := range []string{idStr, id.Recipient().String(), markedPriv, "$argon2id$"} {
		if strings.Contains(streams, secret) {
			t.Fatalf("success path leaks %q", secret)
		}
	}

	// failure path: wrong identity on stdin
	other, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	code, out, errb = runInput(strings.NewReader(other.String()+"\n"), "restore", name, "--identity-stdin")
	if code != 1 {
		t.Fatalf("wrong identity: exit = %d", code)
	}
	streams = out + errb
	for _, secret := range []string{idStr, id.Recipient().String(), markedPriv, "$argon2id$", other.String()} {
		if strings.Contains(streams, secret) {
			t.Fatalf("failure path leaks %q", secret)
		}
	}
}
