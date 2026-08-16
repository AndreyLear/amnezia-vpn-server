// M8.8 apply tests: the restart half of the restore pipeline — a
// pending marker produced by Restore is applied at panel-init time via
// ApplyPending, atomically, with crash/concurrency safety.
package backup

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/auth"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

// sqliteFileWithClient returns the bytes of a migrated database with a
// single client row (the archived state: alice present, bob absent).
func sqliteFileWithClient(t *testing.T, name, key string) []byte {
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
	if _, err := handle.Exec(
		`INSERT INTO clients (name, private_key, public_key, preshared_key, address, enabled, created_at, updated_at)
		 VALUES (?, ?, 'x-archive-client-public-key-0000000000000001',
		         'x-archive-preshared-key-0000000000000001', '10.66.66.10/32',
		         1, '2026-08-01T00:00:00Z', '2026-08-01T00:00:00Z')`,
		name, key,
	); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func sqliteFileWithClientAndAuth(t *testing.T, clientName, clientKey, username, passwordHash, totpSecret, totpMode string) []byte {
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
	if _, err := handle.Exec(
		`INSERT INTO clients (name, private_key, public_key, preshared_key, address, enabled, created_at, updated_at)
		 VALUES (?, ?, 'x-archive-client-public-key-0000000000000001',
		         'x-archive-preshared-key-0000000000000001', '10.66.66.10/32',
		         1, '2026-08-01T00:00:00Z', '2026-08-01T00:00:00Z')`,
		clientName, clientKey,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := handle.Exec(
		`INSERT INTO auth (username, password_hash, totp_secret, totp_mode) VALUES (?, ?, ?, ?)`,
		username, passwordHash, totpSecret, totpMode,
	); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func archiveWithAliceAndAuth(t *testing.T, username, passwordHash, totpSecret, totpMode string) string {
	t.Helper()
	return buildArchive(t, []archiveEntry{
		{name: manifestFilename, typ: tar.TypeReg, data: []byte(validManifestJSON())},
		{name: snapshotFilename, typ: tar.TypeReg, data: sqliteFileWithClientAndAuth(t, "alice", "x-archive-client-private-key-alice", username, passwordHash, totpSecret, totpMode)},
	})
}

func (c *restoreCtx) seedAuth(username, passwordHash, totpSecret, totpMode string) {
	c.t.Helper()
	h := c.reopen()
	if _, err := h.Exec(
		`INSERT INTO auth (username, password_hash, totp_secret, totp_mode) VALUES (?, ?, ?, ?)`,
		username, passwordHash, totpSecret, totpMode,
	); err != nil {
		c.t.Fatal(err)
	}
}

// archiveWithAlice is the crafted restore source: manifest + a
// migrated database whose only client is alice.
func archiveWithAlice(t *testing.T) string {
	t.Helper()
	return buildArchive(t, []archiveEntry{
		{name: manifestFilename, typ: tar.TypeReg, data: []byte(validManifestJSON())},
		{name: snapshotFilename, typ: tar.TypeReg, data: sqliteFileWithClient(t, "alice", "x-archive-client-private-key-alice")},
	})
}

// applyCtx runs a restore to create the pending marker, then lets each
// test drive ApplyPending. Live state: alice + bob; archived state:
// alice only.
func applyCtx(t *testing.T) *restoreCtx {
	t.Helper()
	c := newRestoreCtx(t)
	c.seedClient("alice", "x-test-client-private-key-alice")
	c.seedClient("bob", "x-test-client-private-key-bob")
	res, err := c.doRestore(archiveWithAlice(t))
	if err != nil {
		t.Fatal(err)
	}
	if res.PendingDB == "" {
		t.Fatal("restore did not produce a pending marker")
	}
	return c
}

func TestApplyPendingNoMarker(t *testing.T) {
	c := newRestoreCtx(t)
	c.seedClient("alice", "x-test-client-private-key-alice")
	before, _ := c.liveState()

	applied, err := ApplyPending(c.dbPath)
	if err != nil {
		t.Fatalf("ApplyPending: %v", err)
	}
	if applied {
		t.Fatal("applied with no marker")
	}
	c.assertUntouched(before)
	if _, err := os.Lstat(c.dbPath + preRestoreSuffix); !os.IsNotExist(err) {
		t.Fatalf("unexpected pre-restore copy: %v", err)
	}
}

func TestApplyPendingHappyPath(t *testing.T) {
	c := applyCtx(t)
	before, _ := c.liveState()
	pendingDir, ok, err := PendingPath(c.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("no marker")
	}
	want, err := os.ReadFile(filepath.Join(pendingDir, pendingDBName))
	if err != nil {
		t.Fatal(err)
	}

	applied, err := ApplyPending(c.dbPath)
	if err != nil {
		t.Fatalf("ApplyPending: %v", err)
	}
	if !applied {
		t.Fatal("not applied")
	}

	// Marker is gone; the web UI unblocks.
	c.assertNoMarker()

	// The live database is now byte-for-byte the pending image (the
	// archived alice-only state), not the live alice+bob state.
	after, err := os.ReadFile(c.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, want) {
		t.Fatal("live database is not the restored image")
	}
	if bytes.Equal(after, before) {
		t.Fatal("live database was not swapped")
	}
	h := c.reopen()
	var alice, bob int
	if err := h.QueryRow(
		`SELECT COUNT(*) FROM clients WHERE name = 'alice'`,
	).Scan(&alice); err != nil {
		t.Fatal(err)
	}
	if err := h.QueryRow(
		`SELECT COUNT(*) FROM clients WHERE name = 'bob'`,
	).Scan(&bob); err != nil {
		t.Fatal(err)
	}
	if alice != 1 || bob != 0 {
		t.Fatalf("restored state wrong: alice=%d bob=%d", alice, bob)
	}

	// The retired live database is kept as an in-place recovery copy.
	retired, err := os.ReadFile(c.dbPath + preRestoreSuffix)
	if err != nil {
		t.Fatalf("pre-restore copy missing: %v", err)
	}
	if !bytes.Equal(retired, before) {
		t.Fatal("pre-restore copy does not match the retired live database")
	}

	// Idempotence: a second apply is a no-op.
	again, err := ApplyPending(c.dbPath)
	if err != nil {
		t.Fatalf("second ApplyPending: %v", err)
	}
	if again {
		t.Fatal("second apply reported applied")
	}
}

// TestApplyPendingCrashAfterInstall simulates a crash after the swap:
// the image is already installed at live and the retired copy exists,
// but the marker was never cleaned up. The next apply RESUMES forward:
// it finishes the cleanup and reports the restore as applied.
func TestApplyPendingCrashAfterInstall(t *testing.T) {
	c := applyCtx(t)
	pendingDir, ok, err := PendingPath(c.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("no marker")
	}
	image := filepath.Join(pendingDir, pendingDBName)
	before, _ := c.liveState()
	want, err := os.ReadFile(image)
	if err != nil {
		t.Fatal(err)
	}

	// Crash window: image consumed, live already swapped, marker left.
	if err := os.Rename(c.dbPath, c.dbPath+preRestoreSuffix); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(image, c.dbPath); err != nil {
		t.Fatal(err)
	}

	applied, err := ApplyPending(c.dbPath)
	if err != nil {
		t.Fatalf("ApplyPending: %v", err)
	}
	if !applied {
		t.Fatal("crashed apply was not resumed")
	}
	after, err := os.ReadFile(c.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, want) {
		t.Fatal("live database is not the restored image")
	}
	retired, err := os.ReadFile(c.dbPath + preRestoreSuffix)
	if err != nil {
		t.Fatalf("retired copy missing: %v", err)
	}
	if !bytes.Equal(retired, before) {
		t.Fatal("retired copy does not match the pre-restore database")
	}
	c.assertNoMarker()
}

// TestApplyPendingCrashBeforeRetire simulates a crash right after the
// claim: the applying directory exists with the image, live intact.
// The next apply resumes the full swap.
func TestApplyPendingCrashBeforeRetire(t *testing.T) {
	c := applyCtx(t)
	pendingDir, ok, err := PendingPath(c.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("no marker")
	}
	if err := os.Rename(pendingDir, pendingDir+applyingSuffix); err != nil {
		t.Fatal(err)
	}
	before, _ := c.liveState()

	applied, err := ApplyPending(c.dbPath)
	if err != nil {
		t.Fatalf("ApplyPending: %v", err)
	}
	if !applied {
		t.Fatal("crashed apply was not resumed")
	}

	// Live was swapped to the archived alice-only state, the retired
	// copy is the pre-restore alice+bob state, marker fully cleaned.
	after, err := os.ReadFile(c.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(after, before) {
		t.Fatal("live database was not swapped")
	}
	h := c.reopen()
	var alice, bob int
	if err := h.QueryRow(`SELECT COUNT(*) FROM clients WHERE name = 'alice'`).Scan(&alice); err != nil {
		t.Fatal(err)
	}
	if err := h.QueryRow(`SELECT COUNT(*) FROM clients WHERE name = 'bob'`).Scan(&bob); err != nil {
		t.Fatal(err)
	}
	if alice != 1 || bob != 0 {
		t.Fatalf("restored state wrong: alice=%d bob=%d", alice, bob)
	}
	if _, err := os.Lstat(c.dbPath + preRestoreSuffix); err != nil {
		t.Fatalf("retired copy missing: %v", err)
	}
	c.assertNoMarker()
}

// TestApplyPendingNoLiveDatabase covers a fresh data directory that
// has a pending restore but no live database yet (restore before first
// start): the image simply becomes the live database.
func TestApplyPendingNoLiveDatabase(t *testing.T) {
	c := newRestoreCtx(t)
	archive := buildArchive(t, validEntries(t, db.SchemaVersion))
	if _, err := c.doRestore(archive); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(c.dbPath); err != nil {
		t.Fatal(err)
	}

	applied, err := ApplyPending(c.dbPath)
	if err != nil {
		t.Fatalf("ApplyPending: %v", err)
	}
	if !applied {
		t.Fatal("not applied")
	}
	c.assertNoMarker()
	h := c.reopen()
	var n int
	if err := h.QueryRow(`SELECT COUNT(*) FROM clients`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("unexpected clients in restored database: %d", n)
	}
}

// TestApplyPendingMarkerWithoutImage rejects an empty marker directory
// that has neither the image nor a retired copy (foreign state).
func TestApplyPendingMarkerWithoutImage(t *testing.T) {
	c := applyCtx(t)
	pendingDir, ok, err := PendingPath(c.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("no marker")
	}
	// Consume the image without leaving a retired copy.
	if err := os.Remove(filepath.Join(pendingDir, pendingDBName)); err != nil {
		t.Fatal(err)
	}
	before, _ := c.liveState()

	applied, err := ApplyPending(c.dbPath)
	if err == nil {
		t.Fatal("empty marker did not fail")
	}
	if applied {
		t.Fatal("empty marker reported applied")
	}
	c.assertUntouched(before)
	// The marker is kept for inspection.
	if _, ok, err := PendingPath(c.dbPath); err != nil || !ok {
		t.Fatalf("marker removed despite inconsistency (ok=%v err=%v)", ok, err)
	}
}

// TestApplyPendingRejectsSymlinkImage: a non-regular restored image is
// refused before anything is touched.
func TestApplyPendingRejectsSymlinkImage(t *testing.T) {
	c := applyCtx(t)
	pendingDir, ok, err := PendingPath(c.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("no marker")
	}
	image := filepath.Join(pendingDir, pendingDBName)
	if err := os.Remove(image); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(c.dbPath, image); err != nil {
		t.Fatal(err)
	}
	before, _ := c.liveState()

	applied, err := ApplyPending(c.dbPath)
	if err == nil {
		t.Fatal("symlink image did not fail")
	}
	if applied {
		t.Fatal("symlink image reported applied")
	}
	c.assertUntouched(before)
	if _, ok, err := PendingPath(c.dbPath); err != nil || !ok {
		t.Fatalf("marker removed despite rejection (ok=%v err=%v)", ok, err)
	}
}

// TestApplyPendingConcurrent races two applies: exactly one may win,
// the loser errors or no-ops, and the final state is a consistent
// applied restore.
func TestApplyPendingConcurrent(t *testing.T) {
	for i := 0; i < 20; i++ {
		c := applyCtx(t)
		want, _ := c.liveState()
		pendingDir, ok, err := PendingPath(c.dbPath)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Fatal("no marker")
		}
		image, err := os.ReadFile(filepath.Join(pendingDir, pendingDBName))
		if err != nil {
			t.Fatal(err)
		}

		var wg sync.WaitGroup
		results := make([]bool, 2)
		errs := make([]error, 2)
		start := make(chan struct{})
		for i := range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				applied, err := ApplyPending(c.dbPath)
				results[i] = applied
				errs[i] = err
			}()
		}
		close(start)
		wg.Wait()

		// Exactly one winner; the loser either saw the marker gone
		// (no-op) or raced into the swap (error).
		winners := 0
		for _, a := range results {
			if a {
				winners++
			}
		}
		if winners != 1 {
			t.Fatalf("winners=%d (results=%v errs=%v)", winners, results, errs)
		}

		// Final state: live is the archived image, no marker, the
		// retired copy is the pre-restore live database.
		c.assertNoMarker()
		after, err := os.ReadFile(c.dbPath)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(after, image) {
			t.Fatal("concurrent apply left the wrong live database")
		}
		retired, err := os.ReadFile(c.dbPath + preRestoreSuffix)
		if err != nil {
			t.Fatalf("pre-restore copy missing: %v", err)
		}
		if !bytes.Equal(retired, want) {
			t.Fatal("pre-restore copy does not match the retired live database")
		}
	}
}

func archiveWithClient(t *testing.T, name, key string) string {
	t.Helper()
	return buildArchive(t, []archiveEntry{
		{name: manifestFilename, typ: tar.TypeReg, data: []byte(validManifestJSON())},
		{name: snapshotFilename, typ: tar.TypeReg, data: sqliteFileWithClient(t, name, key)},
	})
}

// TestApplyPendingSequentialRefreshesPreRestore (T-141): a second
// restore+apply must replace .pre-restore with the immediately previous
// live database, not leave the older generation and destroy the current
// live file.
func TestApplyPendingSequentialRefreshesPreRestore(t *testing.T) {
	c := applyCtx(t)
	firstLive, _ := c.liveState()

	applied, err := ApplyPending(c.dbPath)
	if err != nil {
		t.Fatalf("first ApplyPending: %v", err)
	}
	if !applied {
		t.Fatal("first apply not applied")
	}
	firstRetired, err := os.ReadFile(c.dbPath + preRestoreSuffix)
	if err != nil {
		t.Fatalf("first pre-restore missing: %v", err)
	}
	if !bytes.Equal(firstRetired, firstLive) {
		t.Fatal("first pre-restore is not the original live database")
	}

	c.seedClient("carol", "x-test-client-private-key-carol")
	secondLive, _ := c.liveState()
	if bytes.Equal(secondLive, firstLive) {
		t.Fatal("live database was not mutated between applies")
	}

	if _, err := c.doRestore(archiveWithClient(t, "charlie", "x-archive-client-private-key-charlie")); err != nil {
		t.Fatal(err)
	}
	applied, err = ApplyPending(c.dbPath)
	if err != nil {
		t.Fatalf("second ApplyPending: %v", err)
	}
	if !applied {
		t.Fatal("second apply not applied")
	}

	retired, err := os.ReadFile(c.dbPath + preRestoreSuffix)
	if err != nil {
		t.Fatalf("second pre-restore missing: %v", err)
	}
	if !bytes.Equal(retired, secondLive) {
		t.Fatal("pre-restore is not the immediately previous live database")
	}
	if bytes.Equal(retired, firstRetired) {
		t.Fatal("pre-restore was left as the older generation")
	}

	h := c.reopen()
	var charlie, carol int
	if err := h.QueryRow(`SELECT COUNT(*) FROM clients WHERE name = 'charlie'`).Scan(&charlie); err != nil {
		t.Fatal(err)
	}
	if err := h.QueryRow(`SELECT COUNT(*) FROM clients WHERE name = 'carol'`).Scan(&carol); err != nil {
		t.Fatal(err)
	}
	if charlie != 1 || carol != 0 {
		t.Fatalf("second restored state wrong: charlie=%d carol=%d", charlie, carol)
	}
}

// TestRevertApplyRestoresPendingAndLive (T-141): after a successful
// file swap, RevertApply puts the restored image back behind a pending
// marker and reinstates the retired live database.
func TestRevertApplyRestoresPendingAndLive(t *testing.T) {
	c := applyCtx(t)
	before, _ := c.liveState()
	pendingDir, ok, err := PendingPath(c.dbPath)
	if err != nil || !ok {
		t.Fatalf("no marker: ok=%v err=%v", ok, err)
	}
	wantImage, err := os.ReadFile(filepath.Join(pendingDir, pendingDBName))
	if err != nil {
		t.Fatal(err)
	}

	applied, err := ApplyPending(c.dbPath)
	if err != nil {
		t.Fatalf("ApplyPending: %v", err)
	}
	if !applied {
		t.Fatal("not applied")
	}

	if err := RevertApply(c.dbPath); err != nil {
		t.Fatalf("RevertApply: %v", err)
	}

	after, err := os.ReadFile(c.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatal("live database was not restored from .pre-restore")
	}
	pendingDir, ok, err = PendingPath(c.dbPath)
	if err != nil || !ok {
		t.Fatalf("pending marker missing after revert: ok=%v err=%v", ok, err)
	}
	gotImage, err := os.ReadFile(filepath.Join(pendingDir, pendingDBName))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotImage, wantImage) {
		t.Fatal("pending image is not the restored database")
	}
	if _, err := os.Lstat(c.dbPath + preRestoreSuffix); !os.IsNotExist(err) {
		t.Fatalf("retired copy survived revert: %v", err)
	}
}

// TestApplyPendingKeepsLiveAuth (T-155): restoring an archive whose
// auth row is a different user must not replace the destination login.
func TestApplyPendingKeepsLiveAuth(t *testing.T) {
	const livePass = "live-admin-password-A"
	const archivePass = "archive-admin-password-B"
	liveHash, err := auth.HashPassword(livePass)
	if err != nil {
		t.Fatal(err)
	}
	archiveHash, err := auth.HashPassword(archivePass)
	if err != nil {
		t.Fatal(err)
	}

	c := newRestoreCtx(t)
	c.seedAuth("admin-a", liveHash, "JBSWY3DPEHPK3PXP", "2fa")
	c.seedClient("bob", "x-test-client-private-key-bob")
	if _, err := c.doRestore(archiveWithAliceAndAuth(t, "admin-b", archiveHash, "KRSXG5CTMVRXEZLU", "")); err != nil {
		t.Fatal(err)
	}

	applied, err := ApplyPending(c.dbPath)
	if err != nil {
		t.Fatalf("ApplyPending: %v", err)
	}
	if !applied {
		t.Fatal("not applied")
	}

	h := c.reopen()
	live, err := db.AuthUserByUsername(h, "admin-a")
	if err != nil {
		t.Fatalf("live admin A missing after restore: %v", err)
	}
	if !auth.VerifyPassword(livePass, live.PasswordHash) {
		t.Fatal("live admin A password no longer verifies")
	}
	if live.TOTPSecret != "JBSWY3DPEHPK3PXP" || live.TOTPMode != "2fa" {
		t.Fatalf("live TOTP not kept: secret=%q mode=%q", live.TOTPSecret, live.TOTPMode)
	}
	if _, err := db.AuthUserByUsername(h, "admin-b"); err == nil {
		t.Fatal("archive admin B became the live login")
	}
	var alice, bob int
	if err := h.QueryRow(`SELECT COUNT(*) FROM clients WHERE name = 'alice'`).Scan(&alice); err != nil {
		t.Fatal(err)
	}
	if err := h.QueryRow(`SELECT COUNT(*) FROM clients WHERE name = 'bob'`).Scan(&bob); err != nil {
		t.Fatal(err)
	}
	if alice != 1 || bob != 0 {
		t.Fatalf("restored clients wrong: alice=%d bob=%d", alice, bob)
	}
}

// TestApplyPendingNoLiveAuthKeepsArchiveAuth (T-155): restore before
// the first panel user keeps the archive login.
func TestApplyPendingNoLiveAuthKeepsArchiveAuth(t *testing.T) {
	const archivePass = "archive-admin-password-B"
	archiveHash, err := auth.HashPassword(archivePass)
	if err != nil {
		t.Fatal(err)
	}
	c := newRestoreCtx(t)
	if _, err := c.doRestore(archiveWithAliceAndAuth(t, "admin-b", archiveHash, "", "")); err != nil {
		t.Fatal(err)
	}

	applied, err := ApplyPending(c.dbPath)
	if err != nil {
		t.Fatalf("ApplyPending: %v", err)
	}
	if !applied {
		t.Fatal("not applied")
	}

	h := c.reopen()
	u, err := db.AuthUserByUsername(h, "admin-b")
	if err != nil {
		t.Fatalf("archive admin missing: %v", err)
	}
	if !auth.VerifyPassword(archivePass, u.PasswordHash) {
		t.Fatal("archive admin password does not verify")
	}
}
