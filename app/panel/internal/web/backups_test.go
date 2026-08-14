// M8.5 backup management UI tests: the /backups page behind auth+CSRF,
// create/download/delete flows with strict name handling, secret
// absence, and concurrency smoke under -race. The full M7.6 CSRF matrix
// covers the new POST routes through protectedPosts (csrf_test.go).
package web

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/backup"
	"github.com/klauspost/compress/zstd"
)

// setBackupsPath points AMNEZIA_BACKUPS_PATH at a fresh temp dir and
// installs a test AGE_RECIPIENT; it returns the dir and the identity
// (whose strings are secrets that must never appear in any response).
func setBackupsPath(t *testing.T) (string, *age.X25519Identity) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "backups")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir backups: %v", err)
	}
	t.Setenv("AMNEZIA_BACKUPS_PATH", dir)
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("age identity: %v", err)
	}
	t.Setenv("AGE_RECIPIENT", id.Recipient().String())
	return dir, id
}

// makeBackup runs the real M8.2 pipeline into the backups dir and
// returns the archive name.
func makeBackup(t *testing.T, f *fixture, dir string, when time.Time) string {
	t.Helper()
	if _, err := backup.Create(f.h, dir, os.Getenv("AGE_RECIPIENT"), func() time.Time { return when }); err != nil {
		t.Fatalf("backup.Create: %v", err)
	}
	return "backup-" + when.Format("2006-01-02") + ".tar.zst.age"
}

// decryptEntries is the inverse of backup.Create for tests: age → zstd
// → tar, returning entry name → content.
func decryptEntries(t *testing.T, path string, id *age.X25519Identity) map[string][]byte {
	t.Helper()
	fd, err := os.Open(path)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	defer fd.Close()
	rd, err := age.Decrypt(fd, id)
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

// TestBackupsRequireAuth: unauthenticated requests hit the M7.4
// challenge — GET redirects to /login, POST answers the fixed 401.
func TestBackupsRequireAuth(t *testing.T) {
	f := newFixture(t)
	gets := []string{
		"/backups",
		"/backups/backup-2026-08-12.tar.zst.age/download",
	}
	for _, path := range gets {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		f.server.ServeHTTP(rec, req)
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
			t.Errorf("GET %s: code %d location %q, want 303 /login",
				path, rec.Code, rec.Header().Get("Location"))
		}
	}
	posts := []string{
		"/backups/create",
		"/backups/backup-2026-08-12.tar.zst.age/delete",
	}
	for _, path := range posts {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		rec := httptest.NewRecorder()
		f.server.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized || rec.Body.String() != "Unauthorized.\n" {
			t.Errorf("POST %s: code %d body %q, want 401 fixed", path, rec.Code, rec.Body.String())
		}
	}
}

// TestBackupsPageEmptyDir: no backups directory yet — the page renders
// 200 with the empty state.
func TestBackupsPageEmptyDir(t *testing.T) {
	f := newFixture(t)
	setBackupsPath(t)
	rec := f.get("/backups")
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "No backups yet.") {
		t.Fatalf("empty state missing: %s", body)
	}
	if !strings.Contains(body, `action="/backups/create"`) {
		t.Fatalf("create form missing: %s", body)
	}
}

// TestBackupsPageListsValidOnly: only real archives (strict name +
// regular file) appear, sorted; garbage, directories and symlinks with
// matching names never do.
func TestBackupsPageListsValidOnly(t *testing.T) {
	f := newFixture(t)
	dir, _ := setBackupsPath(t)
	makeBackup(t, f, dir, time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC))
	makeBackup(t, f, dir, time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC))

	junk := []string{
		"notes.txt",
		"backup-2026-08-10.tar.age",     // wrong suffix
		"backup-2026-08-10.tar.zst.old", // missing .age
		"backup-2026-13-99.tar.zst.age", // not a date
		"backup-2026-08-10.tar.zst.age.old",
	}
	for _, n := range junk {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("junk"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "backup-2026-08-11.tar.zst.age"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "notes.txt"), filepath.Join(dir, "backup-2026-08-09.tar.zst.age")); err != nil {
		t.Fatal(err)
	}

	body := f.get("/backups").Body.String()
	for _, want := range []string{"backup-2026-08-10.tar.zst.age", "backup-2026-08-12.tar.zst.age"} {
		if !strings.Contains(body, want) {
			t.Errorf("listing missing %s", want)
		}
	}
	if strings.Index(body, "backup-2026-08-10") > strings.Index(body, "backup-2026-08-12") {
		t.Errorf("listing not sorted by name")
	}
	for _, banned := range junk {
		if strings.Contains(body, ">"+banned+"<") {
			t.Errorf("listing shows junk entry %q", banned)
		}
	}
	for _, banned := range []string{"backup-2026-08-11.tar.zst.age", "backup-2026-08-09.tar.zst.age"} {
		if strings.Contains(body, ">"+banned+"<") {
			t.Errorf("listing shows non-regular entry %q", banned)
		}
	}
}

// TestBackupCreateFlow: POST /backups/create (valid CSRF) PRG-redirects
// to /backups, the archive exists and decrypts, and the page then
// shows it.
func TestBackupCreateFlow(t *testing.T) {
	f := newFixture(t)
	dir, id := setBackupsPath(t)
	rec := csrfPOST(f, "/backups/create", f.csrf)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("create: code = %d, want 303", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/backups?msg=") || !strings.Contains(loc, "Backup+created.") {
		t.Fatalf("create: Location = %q", loc)
	}
	name := "backup-" + time.Now().UTC().Format("2006-01-02") + ".tar.zst.age"
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != name {
		t.Fatalf("backups dir after create: %v, %v", entries, err)
	}
	dec := decryptEntries(t, filepath.Join(dir, name), id)
	if len(dec) != 2 || dec["manifest.json"] == nil || dec["amnezia.sqlite"] == nil {
		t.Fatalf("archive entries = %v", dec)
	}
	body := f.get("/backups").Body.String()
	if !strings.Contains(body, name) {
		t.Fatalf("page after create missing %s", name)
	}
	if strings.Contains(body, id.Recipient().String()) {
		t.Fatal("AGE_RECIPIENT value leaked into page")
	}
}

// TestBackupCreateNothingConfigured: without AGE_RECIPIENT the create
// flow still PRG-redirects with a fixed flash and leaves no files
// behind (no staging leaks, no partial archives).
func TestBackupCreateNothingConfigured(t *testing.T) {
	f := newFixture(t)
	dir, _ := setBackupsPath(t)
	t.Setenv("AGE_RECIPIENT", "")
	rec := csrfPOST(f, "/backups/create", f.csrf)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("code = %d, want 303", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("Location"), "Backup+encryption+is+not+configured.") {
		t.Fatalf("Location = %q", rec.Header().Get("Location"))
	}
	// the pipeline never ran: the directory may not even exist, and if
	// it does it must be completely empty (no staging, no archives)
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		t.Fatalf("unexpected leftover after failed create: %s", e.Name())
	}
}

// TestBackupDownloadBytes: download returns byte-for-byte the archive
// with attachment disposition and binary content type.
func TestBackupDownloadBytes(t *testing.T) {
	f := newFixture(t)
	dir, _ := setBackupsPath(t)
	name := makeBackup(t, f, dir, time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC))
	want, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	if len(want) < 100 {
		t.Fatalf("archive suspiciously small: %d bytes", len(want))
	}
	rec := f.get("/backups/" + name + "/download")
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
	if got := rec.Body.Bytes(); !bytes.Equal(got, want) {
		t.Fatal("download bytes differ from the archive on disk")
	}
	if cd := rec.Header().Get("Content-Disposition"); cd != `attachment; filename="`+name+`"` {
		t.Fatalf("Content-Disposition = %q", cd)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Fatalf("Content-Type = %q", ct)
	}
}

// TestBackupDownloadRejectsBadNames: traversal attempts, absolute
// paths and malformed names never reach the file system. The mux
// itself clean-path-redirects (301/307 — 301 since Go 1.22) "..",
// "//" and trailing-slash shapes — those redirects are also refused
// (follow them to a 404); everything else answers the generic 404
// whose body never echoes the requested name.
func TestBackupDownloadRejectsBadNames(t *testing.T) {
	f := newFixture(t)
	setBackupsPath(t)
	hostile := []string{
		"../backup-2026-08-12.tar.zst.age",
		"..%2Fbackup-2026-08-12.tar.zst.age",
		"/etc/passwd",
		"backup-2026-08-12",              // no suffix
		"backup-2026-08-12.tar.zst.age/", // trailing slash
		"backup-2026-13-99.tar.zst.age",
		"backup-2026-08-12.tar.zst.age%00.age",
		"a/b",
	}
	for _, p := range hostile {
		rec := f.get("/backups/" + p + "/download")
		if rec.Code == http.StatusMovedPermanently || rec.Code == http.StatusTemporaryRedirect {
			target := rec.Header().Get("Location")
			if target == "" || !strings.HasPrefix(target, "/") {
				t.Fatalf("%q: 307 with bad Location %q", p, target)
			}
			rec = f.get(target)
		}
		if rec.Code != http.StatusNotFound {
			t.Errorf("%q: final code = %d, want 404", p, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "backup-2026-08-12.tar.zst.age") ||
			strings.Contains(rec.Body.String(), "/etc/passwd") {
			t.Errorf("%q: 404 body echoes the requested name", p)
		}
	}
}

// TestBackupDownloadSymlinkAndDir404: a symlink or directory that
// matches the strict name is never served.
func TestBackupDownloadSymlinkAndDir404(t *testing.T) {
	f := newFixture(t)
	dir, _ := setBackupsPath(t)
	if err := os.WriteFile(filepath.Join(dir, "target.bin"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "target.bin"), filepath.Join(dir, "backup-2026-08-12.tar.zst.age")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "backup-2026-08-11.tar.zst.age"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"backup-2026-08-12.tar.zst.age", "backup-2026-08-11.tar.zst.age"} {
		rec := f.get("/backups/" + name + "/download")
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: code = %d, want 404", name, rec.Code)
		}
	}
}

// TestBackupDeleteRequiresCSRF: a delete without a token is refused
// with 403 and the file survives.
func TestBackupDeleteRequiresCSRF(t *testing.T) {
	f := newFixture(t)
	dir, _ := setBackupsPath(t)
	name := makeBackup(t, f, dir, time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC))
	rec := csrfPOST(f, "/backups/"+name+"/delete", "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403", rec.Code)
	}
	if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
		t.Fatalf("file removed despite CSRF refusal: %v", err)
	}
}

// TestBackupDeleteWrongCSRF: a wrong token is refused with the generic
// fixed 403 (nothing about the token is echoed) and the file survives.
func TestBackupDeleteWrongCSRF(t *testing.T) {
	f := newFixture(t)
	dir, _ := setBackupsPath(t)
	name := makeBackup(t, f, dir, time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC))
	rec := csrfPOST(f, "/backups/"+name+"/delete", "wrong-token-value")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "wrong-token-value") {
		t.Fatal("403 body echoes the submitted token")
	}
	if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
		t.Fatalf("file removed despite wrong CSRF: %v", err)
	}
}

// TestBackupDeleteFlow: a valid delete PRG-redirects with a fixed
// flash and unlinks the file; a second delete answers "Backup not
// found.".
func TestBackupDeleteFlow(t *testing.T) {
	f := newFixture(t)
	dir, _ := setBackupsPath(t)
	name := makeBackup(t, f, dir, time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC))
	rec := csrfPOST(f, "/backups/"+name+"/delete", f.csrf)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("delete: code = %d, want 303", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/backups?msg=") || !strings.Contains(loc, "Backup+deleted.") {
		t.Fatalf("delete: Location = %q", loc)
	}
	if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
		t.Fatal("file still exists after delete")
	}

	rec = csrfPOST(f, "/backups/"+name+"/delete", f.csrf)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("second delete: code = %d, want 303", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("Location"), "Backup+not+found.") {
		t.Fatalf("second delete: Location = %q", rec.Header().Get("Location"))
	}
}

// TestBackupDeleteInvalidNameFixedFlash: a hostile delete answers the
// fixed flash and the name itself is never echoed anywhere.
func TestBackupDeleteInvalidNameFixedFlash(t *testing.T) {
	f := newFixture(t)
	setBackupsPath(t)
	hostile := []string{
		"backup-2026-13-99.tar.zst.age",
		"x",
	}
	for _, name := range hostile {
		rec := csrfPOST(f, "/backups/"+name+"/delete", f.csrf)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("%q: code = %d, want 303 flash", name, rec.Code)
		}
		loc := rec.Header().Get("Location")
		if strings.Contains(loc, name) {
			t.Fatalf("%q: flash echoes the submitted name: %s", name, loc)
		}
		if !strings.Contains(loc, "Invalid+backup+name.") {
			t.Fatalf("%q: Location = %q", name, loc)
		}
	}
}

// TestBackupDeleteSymlinkNeverUnlinkTarget: deleting a regular-name
// symlink answers "Backup not found." and neither the link nor its
// target is removed (os.Remove only ever runs after IsRegular).
func TestBackupDeleteSymlinkNeverUnlinkTarget(t *testing.T) {
	f := newFixture(t)
	dir, _ := setBackupsPath(t)
	hostage := filepath.Join(dir, "hostage.db")
	if err := os.WriteFile(hostage, []byte("precious"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "backup-2026-08-09.tar.zst.age")
	if err := os.Symlink(hostage, link); err != nil {
		t.Fatal(err)
	}
	rec := csrfPOST(f, "/backups/backup-2026-08-09.tar.zst.age/delete", f.csrf)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("symlink delete: code = %d, want 303", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("Location"), "Backup+not+found.") {
		t.Fatalf("symlink delete: Location = %q", rec.Header().Get("Location"))
	}
	if _, err := os.Stat(hostage); err != nil {
		t.Fatalf("symlink delete removed the target file: %v", err)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatal("symlink itself must survive (only regular files are deleted)")
	}
}

// TestBackupsNoSecrets: the recipient value, private keys and marked
// DB material never appear on /backups, in flash redirects or in
// download responses.
func TestBackupsNoSecrets(t *testing.T) {
	f := newFixture(t)
	dir, id := setBackupsPath(t)
	makeBackup(t, f, dir, time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC))
	name := "backup-2026-08-12.tar.zst.age"
	recipientStr := id.Recipient().String()
	identityStr := id.String()

	body := f.get("/backups").Body.String()
	for _, secret := range []string{recipientStr, identityStr, samplePrivateKey, samplePresharedKey,
		"private_key", "preshared_key", "staging", "AGE_RECIPIENT"} {
		if strings.Contains(body, secret) {
			t.Errorf("page leaks %q", secret)
		}
	}

	rec := csrfPOST(f, "/backups/"+name+"/delete", f.csrf)
	loc := rec.Header().Get("Location")
	for _, secret := range []string{recipientStr, identityStr, samplePrivateKey} {
		if strings.Contains(loc, secret) {
			t.Errorf("redirect leaks %q", secret)
		}
	}

	download := f.get("/backups/" + name + "/download")
	if strings.Contains(download.Body.String(), recipientStr) {
		t.Error("download leaks recipient")
	}
}

// TestBackupsConcurrent: parallel creates (same day), downloads,
// page views and deletes must not race, panic or leave staging
// leftovers behind. Every surviving archive decrypts.
func TestBackupsConcurrent(t *testing.T) {
	f := newFixture(t)
	dir, id := setBackupsPath(t)
	name := "backup-" + time.Now().UTC().Format("2006-01-02") + ".tar.zst.age"

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if rec := csrfPOST(f, "/backups/create", f.csrf); rec.Code != http.StatusSeeOther {
				t.Errorf("create: code = %d", rec.Code)
			}
		}()
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if rec := f.get("/backups"); rec.Code != http.StatusOK {
				t.Errorf("page: code = %d", rec.Code)
			}
			rec := f.get("/backups/" + name + "/download")
			if rec.Code != http.StatusOK && rec.Code != http.StatusNotFound {
				t.Errorf("download: code = %d", rec.Code)
			}
		}()
	}
	wg.Wait()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".staging-") {
			t.Fatalf("staging leak: %s", e.Name())
		}
		names = append(names, e.Name())
	}
	for _, n := range names {
		if !validBackupName(n) {
			t.Errorf("unexpected file after concurrency: %s", n)
			continue
		}
		dec := decryptEntries(t, filepath.Join(dir, n), id)
		if len(dec) != 2 {
			t.Errorf("%s: entries = %d", n, len(dec))
		}
	}

	for _, n := range names {
		wg.Add(1)
		go func(n string) {
			defer wg.Done()
			if rec := csrfPOST(f, "/backups/"+n+"/delete", f.csrf); rec.Code != http.StatusSeeOther {
				t.Errorf("delete %s: code = %d", n, rec.Code)
			}
		}(n)
	}
	wg.Wait()
	entries, err = os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("backups dir not empty after concurrent deletes: %v", entries)
	}
}

// TestBackupsPageLinkFromDashboard: the dashboard carries a Backups
// link (M8.5 UI requirement).
func TestBackupsPageLinkFromDashboard(t *testing.T) {
	f := newFixture(t)
	body := f.get("/").Body.String()
	if !strings.Contains(body, `href="/backups"`) {
		t.Fatalf("dashboard missing Backups link: %s", body)
	}
}

// TestBackupDownloadNowFlow (T-125): POST /backups/download streams a
// fresh archive as an attachment and stores nothing — no new file in
// the backups dir, no temp dirs left behind.
func TestBackupDownloadNowFlow(t *testing.T) {
	f := newFixture(t)
	dir, id := setBackupsPath(t)
	f.addClient("alice")
	before := countTempRestoreDirs()

	rec := csrfPOST(f, "/backups/download", f.csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("download: code = %d, want 200", rec.Code)
	}
	cd := rec.Header().Get("Content-Disposition")
	if !strings.HasPrefix(cd, "attachment; filename=\"backup-") || !strings.HasSuffix(cd, ".tar.zst.age\"") {
		t.Fatalf("download: Content-Disposition = %q", cd)
	}
	entries := decryptEntries(t, writeTempArchive(t, rec.Body.Bytes()), id)
	if _, ok := entries["manifest.json"]; !ok {
		t.Fatalf("downloaded archive missing manifest: %v", entries)
	}
	// nothing stored: the backups dir has no new archive.
	names, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range names {
		if strings.HasPrefix(e.Name(), "backup-") {
			t.Fatalf("download stored an archive in the backups dir: %s", e.Name())
		}
	}
	if got := countTempRestoreDirs(); got != before {
		t.Fatalf("temp dirs leaked: before %d, after %d", before, got)
	}
}

// TestBackupDownloadNowGuards (T-125): CSRF 403, pause while a restore
// is pending, and the unconfigured flash without AGE_RECIPIENT.
func TestBackupDownloadNowGuards(t *testing.T) {
	f := newFixture(t)
	dir, id := setBackupsPath(t)

	rec := csrfPOST(f, "/backups/download", "wrong-token")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("bad csrf: code = %d, want 403", rec.Code)
	}

	marker := preparedRestore(t, f, dir, id)
	rec = csrfPOST(f, "/backups/download", f.csrf)
	if !strings.Contains(rec.Header().Get("Location"), "Restore+pending.") {
		t.Fatalf("pending pause: Location = %q", rec.Header().Get("Location"))
	}

	// clear the marker (as a serve restart does), drop the recipient.
	if err := os.RemoveAll(marker); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGE_RECIPIENT", "")
	rec = csrfPOST(f, "/backups/download", f.csrf)
	if !strings.Contains(rec.Header().Get("Location"), "not+configured") {
		t.Fatalf("unconfigured: Location = %q", rec.Header().Get("Location"))
	}
}

// writeTempArchive persists raw archive bytes to a temp file and
// returns its path (inverse of the download stream).
func writeTempArchive(t *testing.T, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "dl.age")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}
