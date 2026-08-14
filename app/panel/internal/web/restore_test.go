// M8.6 restore UI tests: GET/POST /backups/restore behind auth,
// multipart upload with own CSRF check, generic fixed failures that
// never echo input, the pending state (marker + banner + blocked
// create/delete/restore), and the full pipeline round-trip.
package web

import (
	"bytes"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/auth"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/backup"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

// restoreFlashOf extracts the decoded msg value of a /backups PRG
// Location — language-independent comparison against the flash
// constants (the whole panel is Russian since T-120 round 2).
func restoreFlashOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/backups?msg=") {
		t.Fatalf("Location = %q, want /backups?msg=...", loc)
	}
	msg, err := url.QueryUnescape(loc[len("/backups?msg="):])
	if err != nil {
		t.Fatalf("unescape msg: %v", err)
	}
	return msg
}

// postRestoreUpload builds a multipart body from the given fields (nil
// parts are omitted) and submits POST /backups/restore.
func postRestoreUpload(t *testing.T, f *fixture, fields map[string]string, files map[string][]byte) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	for name, data := range files {
		pw, err := mw.CreateFormFile("backup", name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/backups/restore", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: f.sid})
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, req)
	return rec
}

// restoreFields is the minimal field set every happy-path upload needs.
func restoreFields(f *fixture, identity string) map[string]string {
	return map[string]string{"_csrf": f.csrf, "identity": identity}
}

// preparedRestore drives a full happy-path restore through the
// pipeline DIRECTLY (the CLI path — not the web upload, which now
// applies in-process, T-125) and returns the marker path. It is used
// by the pending-state tests.
func preparedRestore(t *testing.T, f *fixture, dir string, id *age.X25519Identity) string {
	t.Helper()
	name := makeBackup(t, f, dir, time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC))
	archive := filepath.Join(dir, name)
	if _, err := backup.Restore(f.h, f.dbPath, archive, dir, os.Getenv("AGE_RECIPIENT"), []age.Identity{id}, nil); err != nil {
		t.Fatalf("restore prepare: %v", err)
	}
	marker, ok, err := backup.PendingPath(f.dbPath)
	if err != nil || !ok {
		t.Fatalf("marker after restore: ok=%v err=%v", ok, err)
	}
	return marker
}

// TestRestoreRequireAuth: unauthenticated requests hit the M7.4
// challenge — GET redirects to /login, POST answers the fixed 401.
func TestRestoreRequireAuth(t *testing.T) {
	f := newFixture(t)
	req := httptest.NewRequest(http.MethodGet, "/backups/restore", nil)
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Fatalf("GET restore: code %d location %q, want 303 /login", rec.Code, rec.Header().Get("Location"))
	}
	req = httptest.NewRequest(http.MethodPost, "/backups/restore", nil)
	rec = httptest.NewRecorder()
	f.server.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized || rec.Body.String() != "Unauthorized.\n" {
		t.Fatalf("POST restore: code %d body %q, want 401 fixed", rec.Code, rec.Body.String())
	}
}

// TestRestorePageRendersForm: the GET page carries the multipart form
// (fields backup + identity), the CSRF token and no secrets.
func TestRestorePageRendersForm(t *testing.T) {
	f := newFixture(t)
	dir, id := setBackupsPath(t)
	makeBackup(t, f, dir, time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC))
	body := f.get("/backups/restore").Body.String()
	for _, want := range []string{
		`enctype="multipart/form-data"`,
		`name="backup"`,
		`name="identity"`,
		`/backups/restore`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("restore page missing %q", want)
		}
	}
	if !strings.Contains(body, `name="_csrf" value="`+f.csrf+`"`) {
		t.Error("restore page missing CSRF token")
	}
	if strings.Contains(body, id.String()) || strings.Contains(body, id.Recipient().String()) {
		t.Error("restore page leaks identity material")
	}
}

// TestRestoreUploadMissingIdentity: an upload without the identity
// field is refused with the fixed flash and nothing is written
// (marker, safety backup, staging).
func TestRestoreUploadMissingIdentity(t *testing.T) {
	f := newFixture(t)
	dir, _ := setBackupsPath(t)
	name := makeBackup(t, f, dir, time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC))
	archive, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	fields := restoreFields(f, "")
	delete(fields, "identity")
	rec := postRestoreUpload(t, f, fields, map[string][]byte{name: archive})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("code %d Location %q, want %q flash", rec.Code, rec.Header().Get("Location"), flashRestoreMissingIdentity)
	}
	if got := restoreFlashOf(t, rec); got != flashRestoreMissingIdentity {
		t.Fatalf("flash = %q, want %q", got, flashRestoreMissingIdentity)
	}
	if _, ok, err := backup.PendingPath(f.dbPath); err != nil || ok {
		t.Fatalf("marker after rejected upload: ok=%v err=%v", ok, err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("backups dir after rejected upload = %v, want only the archive", entries)
	}
}

// TestRestoreUploadInvalidIdentity: garbage in the identity field is
// refused with the fixed flash, and no identity string ever appears in
// the response.
func TestRestoreUploadInvalidIdentity(t *testing.T) {
	f := newFixture(t)
	dir, _ := setBackupsPath(t)
	name := makeBackup(t, f, dir, time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC))
	archive, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	bad := "not-an-age-identity"
	rec := postRestoreUpload(t, f, restoreFields(f, bad), map[string][]byte{name: archive})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("code %d Location %q, want %q flash", rec.Code, rec.Header().Get("Location"), flashRestoreInvalidIdentity)
	}
	if got := restoreFlashOf(t, rec); got != flashRestoreInvalidIdentity {
		t.Fatalf("flash = %q, want %q", got, flashRestoreInvalidIdentity)
	}
	if strings.Contains(rec.Header().Get("Location"), bad) {
		t.Fatal("flash echoes the submitted identity")
	}
	if _, ok, err := backup.PendingPath(f.dbPath); err != nil || ok {
		t.Fatalf("marker after rejected upload: ok=%v err=%v", ok, err)
	}
}

// TestRestoreUploadMissingFile: no file part at all — refused with the
// fixed flash.
func TestRestoreUploadMissingFile(t *testing.T) {
	f := newFixture(t)
	dir, id := setBackupsPath(t)
	rec := postRestoreUpload(t, f, restoreFields(f, id.String()), nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("code %d Location %q, want %q flash", rec.Code, rec.Header().Get("Location"), flashRestoreMissingFile)
	}
	if got := restoreFlashOf(t, rec); got != flashRestoreMissingFile {
		t.Fatalf("flash = %q, want %q", got, flashRestoreMissingFile)
	}
	if _, ok, err := backup.PendingPath(f.dbPath); err != nil || ok {
		t.Fatalf("marker after rejected upload: ok=%v err=%v", ok, err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("backups dir after rejected upload = %v, want empty", entries)
	}
}

// TestRestoreUploadInvalidFileName: file names that stay hostile after
// the stdlib's own base-name normalization (multipart.Part.FileName
// strips directories) are refused up front with the fixed flash.
// Traversal-shaped names that the stdlib normalizes are accepted and
// still cannot escape: the upload is always written to a private 0700
// temp path that never uses the client-supplied name.
func TestRestoreUploadInvalidFileName(t *testing.T) {
	f := newFixture(t)
	dir, id := setBackupsPath(t)
	hostile := []string{"..", `\`}
	for _, fn := range hostile {
		rec := postRestoreUpload(t, f, restoreFields(f, id.String()), map[string][]byte{fn: []byte("x")})
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("%q: code %d Location %q", fn, rec.Code, rec.Header().Get("Location"))
		}
		if got := restoreFlashOf(t, rec); got != flashRestoreInvalidFileName {
			t.Fatalf("%q: flash = %q, want %q", fn, got, flashRestoreInvalidFileName)
		}
	}
	// normalized traversal names reach the pipeline with a meaningless
	// upload — the fixed failure flash, nothing written
	for _, fn := range []string{"../../etc/passwd.age", "a/b.age", "/etc/passwd.age"} {
		rec := postRestoreUpload(t, f, restoreFields(f, id.String()), map[string][]byte{fn: []byte("x")})
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("%q: code %d Location %q", fn, rec.Code, rec.Header().Get("Location"))
		}
		if got := restoreFlashOf(t, rec); got != flashRestoreFailed {
			t.Fatalf("%q: flash = %q, want %q", fn, got, flashRestoreFailed)
		}
	}
	if _, ok, err := backup.PendingPath(f.dbPath); err != nil || ok {
		t.Fatalf("marker after rejected upload: ok=%v err=%v", ok, err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("backups dir after rejected upload = %v, want empty", entries)
	}
}

// TestRestoreUploadBadCSRF: the multipart POST validates the token
// itself (RequireCSRF cannot parse multipart); a wrong or missing
// token answers the same fixed 403 as every other mutation and the
// token value is never echoed.
func TestRestoreUploadBadCSRF(t *testing.T) {
	f := newFixture(t)
	dir, id := setBackupsPath(t)
	name := makeBackup(t, f, dir, time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC))
	archive, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"", "wrong-token-value"} {
		rec := postRestoreUpload(t, f, map[string]string{"_csrf": token, "identity": id.String()},
			map[string][]byte{name: archive})
		if rec.Code != http.StatusForbidden || rec.Body.String() != "Forbidden.\n" {
			t.Fatalf("token %q: code %d body %q, want 403 fixed", token, rec.Code, rec.Body.String())
		}
	}
	if _, ok, err := backup.PendingPath(f.dbPath); err != nil || ok {
		t.Fatalf("marker after refused upload: ok=%v err=%v", ok, err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("backups dir after refused upload = %v, want only the archive", entries)
	}
}

// TestRestoreOversizedUpload: a body beyond MaxRestoreBodyBytes
// answers the fixed 413 and nothing is written.
func TestRestoreOversizedUpload(t *testing.T) {
	f := newFixture(t)
	dir, id := setBackupsPath(t)
	blob := bytes.Repeat([]byte("z"), MaxRestoreBodyBytes+1<<20)
	rec := postRestoreUpload(t, f, restoreFields(f, id.String()), map[string][]byte{"backup-2026-08-12.tar.zst.age": blob})
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("code = %d, want 413", rec.Code)
	}
	if _, ok, err := backup.PendingPath(f.dbPath); err != nil || ok {
		t.Fatalf("marker after oversized upload: ok=%v err=%v", ok, err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("backups dir after oversized upload = %v, want empty", entries)
	}
}

// TestRestoreWrongIdentity: an archive encrypted for another identity
// fails inside the pipeline — generic flash, no marker, no safety
// backup, no staging leftovers, and the DB is untouched.
func TestRestoreWrongIdentity(t *testing.T) {
	f := newFixture(t)
	dir, _ := setBackupsPath(t)
	other, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	clientsBefore := clientCount(f)
	name := makeBackup(t, f, dir, time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC))
	archive, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	rec := postRestoreUpload(t, f, restoreFields(f, other.String()), map[string][]byte{name: archive})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("code %d Location %q, want %q flash", rec.Code, rec.Header().Get("Location"), flashRestoreFailed)
	}
	if got := restoreFlashOf(t, rec); got != flashRestoreFailed {
		t.Fatalf("flash = %q, want %q", got, flashRestoreFailed)
	}
	if _, ok, err := backup.PendingPath(f.dbPath); err != nil || ok {
		t.Fatalf("marker after failed restore: ok=%v err=%v", ok, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != name {
		t.Fatalf("backups dir after failed restore = %v, want only the original archive", entries)
	}
	if got := clientCount(f); got != clientsBefore {
		t.Fatalf("clients after failed restore = %d, want %d (untouched)", got, clientsBefore)
	}
}

// TestRestoreFullFlow: a valid archive with the matching identity runs
// the whole pipeline — pending marker next to the DB, safety backup in
// the backups dir, the panel still live (never restarted), the pending
// banner on both pages, and every mutation blocked.
func TestRestoreFullFlow(t *testing.T) {
	f := newFixture(t)
	dir, id := setBackupsPath(t)
	clientsBefore := clientCount(f)
	marker := preparedRestore(t, f, dir, id)

	// the pending marker is the exclusive 0700 directory next to the
	// DB holding the unpacked restore image (Q7a)
	st, err := os.Stat(marker)
	if err != nil || !st.IsDir() {
		t.Fatalf("marker: %v is not a directory", marker)
	}
	if st.Mode().Perm() != 0o700 {
		t.Fatalf("marker mode = %o, want 0700", st.Mode().Perm())
	}
	markerEntries, err := os.ReadDir(marker)
	if err != nil || len(markerEntries) == 0 {
		t.Fatalf("pending dir contents: %v, err %v", markerEntries, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var safety bool
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "safety-backup-") {
			safety = true
		}
	}
	if !safety {
		t.Fatalf("no safety backup after restore: %v", entries)
	}
	// the live DB is never replaced by the web layer: the marker
	// proves the pipeline stopped at "restart required".
	if got := clientCount(f); got != clientsBefore {
		t.Fatalf("clients = %d, want %d", got, clientsBefore)
	}

	body := f.get("/backups").Body.String()
	if !strings.Contains(body, "Восстановление подготовлено и ожидает перезапуска") {
		t.Fatal("pending banner missing on /backups")
	}
	body = f.get("/backups/restore").Body.String()
	if !strings.Contains(body, "Восстановление подготовлено и ожидает перезапуска") {
		t.Fatal("pending banner missing on /backups/restore")
	}

	// create/delete/restore are all blocked while pending
	rec := csrfPOST(f, "/backups/create", f.csrf)
	if got := restoreFlashOf(t, rec); got != flashRestoreBlockedByPending {
		t.Fatalf("create while pending: flash = %q, want %q", got, flashRestoreBlockedByPending)
	}
	rec = csrfPOST(f, "/backups/backup-2026-08-12.tar.zst.age/delete", f.csrf)
	if got := restoreFlashOf(t, rec); got != flashRestoreBlockedByPending {
		t.Fatalf("delete while pending: flash = %q, want %q", got, flashRestoreBlockedByPending)
	}
	rec = postRestoreUpload(t, f, restoreFields(f, id.String()), map[string][]byte{"backup-2026-08-12.tar.zst.age": []byte("x")})
	if got := restoreFlashOf(t, rec); got != flashRestoreBlockedByPending {
		t.Fatalf("second restore while pending: flash = %q, want %q", got, flashRestoreBlockedByPending)
	}
	// the marker survived all refusals
	if _, ok, err := backup.PendingPath(f.dbPath); err != nil || !ok {
		t.Fatalf("marker lost: ok=%v err=%v", ok, err)
	}
}

// TestRestorePreparedStateSurvivesRestart: dropping the prepared
// marker (as a serve restart does via backup.ClearPending) clears the
// banner and unblocks mutations (M8.4 contract).
func TestRestorePreparedStateSurvivesRestart(t *testing.T) {
	f := newFixture(t)
	dir, id := setBackupsPath(t)
	marker := preparedRestore(t, f, dir, id)
	if err := os.RemoveAll(marker); err != nil {
		t.Fatal(err)
	}
	body := f.get("/backups").Body.String()
	if strings.Contains(body, "Восстановление подготовлено и ожидает перезапуска") {
		t.Fatal("pending banner still shown after marker removal")
	}
	rec := csrfPOST(f, "/backups/create", f.csrf)
	if got := restoreFlashOf(t, rec); got != flashBackupCreated {
		t.Fatalf("create after marker removal: flash = %q, want %q", got, flashBackupCreated)
	}
}

// TestRestoreNoIdentityLeakInResponses: the identity submitted to the
// upload form never appears in any response (flash redirect, page,
// 403), in temp files or in the backups directory.
func TestRestoreNoIdentityLeakInResponses(t *testing.T) {
	f := newFixture(t)
	dir, id := setBackupsPath(t)
	name := makeBackup(t, f, dir, time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC))
	archive, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}

	rec := postRestoreUpload(t, f, restoreFields(f, id.String()), map[string][]byte{name: archive})
	if strings.Contains(rec.Header().Get("Location"), id.String()) {
		t.Fatal("flash redirect leaks the identity")
	}
	rec = postRestoreUpload(t, f, restoreFields(f, id.String()+"\n"), map[string][]byte{name: archive})
	if strings.Contains(rec.Header().Get("Location"), id.String()) {
		t.Fatal("flash redirect leaks the padded identity")
	}

	// T-125: both uploads applied in-process — the pending marker is
	// consumed, so nothing waits for a restart and nothing on disk
	// holds the identity.
	if _, ok, err := backup.PendingPath(f.dbPath); err != nil || ok {
		t.Fatalf("marker after applied restores: ok=%v err=%v", ok, err)
	}
	// no file anywhere holds the identity (temp uploads and staging are
	// cleaned; the pending dir, marker and safety backup are pipeline
	// outputs)
	found, err := scanForString(f.dbPath, dir, id.String())
	if err != nil {
		t.Fatal(err)
	}
	if found != "" {
		t.Fatalf("identity material found in %s", found)
	}
}

// scanForString looks for s inside the pending marker tree next to
// dbPath and every file under backupsDir; returns the first path that
// contains it.
func scanForString(dbPath, backupsDir, s string) (string, error) {
	var paths []string
	marker, ok, err := backup.PendingPath(dbPath)
	if err != nil {
		return "", err
	}
	if ok {
		entries, err := os.ReadDir(marker)
		if err != nil {
			return "", err
		}
		for _, e := range entries {
			paths = append(paths, filepath.Join(marker, e.Name()))
		}
	}
	entries, err := os.ReadDir(backupsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	for _, e := range entries {
		paths = append(paths, filepath.Join(backupsDir, e.Name()))
	}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			return "", err
		}
		if bytes.Contains(data, []byte(s)) {
			return p, nil
		}
	}
	return "", nil
}

// clientCount queries the fixture DB directly.
func clientCount(f *fixture) int {
	f.t.Helper()
	var n int
	if err := f.h.QueryRow("SELECT COUNT(*) FROM clients").Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

// TestRestoreUploadBiggerThanFormLimit: the 64 KiB M6 form limit must
// not reject legitimate archives — the restore route carries its own
// larger limit.
func TestRestoreUploadBiggerThanFormLimit(t *testing.T) {
	f := newFixture(t)
	dir, id := setBackupsPath(t)
	name := makeBackup(t, f, dir, time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC))
	archive, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	if len(archive) <= MaxBodyBytes {
		t.Skipf("archive (%d bytes) not bigger than the form limit", len(archive))
	}
	rec := postRestoreUpload(t, f, restoreFields(f, id.String()), map[string][]byte{name: archive})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("code = %d, want 303", rec.Code)
	}
	// T-125: a successful upload applies in-process — the archive
	// snapshots an empty client table, so the applied flash reports 0.
	if got := restoreFlashOf(t, rec); got != fmt.Sprintf(flashRestoreApplied, 0) {
		t.Fatalf("flash = %q, want %q", got, fmt.Sprintf(flashRestoreApplied, 0))
	}
}

// TestRestorePageNoTempLeaks: rejected uploads leave no panel-restore-*
// temp directories behind.
func TestRestorePageNoTempLeaks(t *testing.T) {
	f := newFixture(t)
	dir, id := setBackupsPath(t)
	name := makeBackup(t, f, dir, time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC))
	archive, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	before := countTempRestoreDirs()
	postRestoreUpload(t, f, restoreFields(f, "garbage-identity"), map[string][]byte{name: archive})
	postRestoreUpload(t, f, restoreFields(f, id.String()), nil)
	rec := postRestoreUpload(t, f, restoreFields(f, id.String()), map[string][]byte{name: archive})
	// T-125: the happy-path upload applies in-process — no pending
	// marker is left behind, so nothing waits for a restart. The
	// archive snapshots an empty client table (0 active clients).
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("final restore: code %d Location %q", rec.Code, rec.Header().Get("Location"))
	}
	if got := restoreFlashOf(t, rec); got != fmt.Sprintf(flashRestoreApplied, 0) {
		t.Fatalf("final restore: flash = %q, want %q", got, fmt.Sprintf(flashRestoreApplied, 0))
	}
	if _, ok, err := backup.PendingPath(f.dbPath); err != nil || ok {
		t.Fatalf("pending marker after applied restore: ok=%v err=%v", ok, err)
	}
	if got := countTempRestoreDirs(); got != before {
		t.Fatalf("temp dirs leaked: before %d, after %d", before, got)
	}
}

func countTempRestoreDirs() int {
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		return -1
	}
	var n int
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "panel-restore-") {
			n++
		}
	}
	return n
}

// TestRestorePageReachableFromBackups: the /backups page links to the
// restore page (M8.6 UI requirement).
func TestRestorePageReachableFromBackups(t *testing.T) {
	f := newFixture(t)
	body := f.get("/backups").Body.String()
	if !strings.Contains(body, `href="/backups/restore"`) {
		t.Fatalf("backups page missing restore link: %s", body)
	}
}

// TestRestoreUploadAppliesInProcess (T-125): uploading an archive
// through the panel applies it immediately — the live handle is
// swapped, awg0.conf is regenerated from the restored state, and no
// restart is needed (no pending marker left).
func TestRestoreUploadAppliesInProcess(t *testing.T) {
	f := newFixture(t)
	dir, id := setBackupsPath(t)

	// live state: alice only; archive = snapshot of alice.
	_, _, pub := f.addClient("alice")
	name := makeBackup(t, f, dir, time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC))
	archive, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	// mutate the live state after the snapshot: bob joins.
	f.addClient("bob")

	rec := postRestoreUpload(t, f, restoreFields(f, id.String()), map[string][]byte{name: archive})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("upload: code = %d, want 303", rec.Code)
	}
	if got := restoreFlashOf(t, rec); got != fmt.Sprintf(flashRestoreApplied, 1) {
		t.Fatalf("upload: flash = %q, want %q", got, fmt.Sprintf(flashRestoreApplied, 1))
	}
	// applied in-process: no pending marker, the live handle sees the
	// restored state (bob gone), awg0.conf regenerated (alice only).
	if _, ok, err := backup.PendingPath(f.dbPath); err != nil || ok {
		t.Fatalf("pending marker after in-process apply: ok=%v err=%v", ok, err)
	}
	live := f.server.db()
	var n int
	if err := live.QueryRow("SELECT COUNT(*) FROM clients").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("clients after applied restore = %d, want 1", n)
	}
	cfg, err := os.ReadFile(f.confPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cfg), pub) {
		t.Fatal("awg0.conf missing the restored client peer")
	}
	if strings.Contains(string(cfg), "x-test-bob") {
		t.Fatal("awg0.conf contains a post-snapshot client")
	}
	if _, ok := f.server.cfg.Sessions.Get(f.sid); ok {
		t.Fatal("pre-restore session must be invalid after apply")
	}
	dash := f.get("/")
	if dash.Code != http.StatusSeeOther || dash.Header().Get("Location") != "/login" {
		t.Fatalf("GET / after restore: code=%d loc=%q, want 303 /login", dash.Code, dash.Header().Get("Location"))
	}
}

// withRestoreFault injects an error at the named in-process apply step
// for the duration of fn (T-141).
func withRestoreFault(t *testing.T, step string, fn func()) {
	t.Helper()
	prev := testFault
	testFault = func(s string) error {
		if s == step {
			return errors.New("injected: " + step)
		}
		return nil
	}
	defer func() { testFault = prev }()
	fn()
}

// restoreApplyFailClosedFixture snapshots alice, then adds bob so a
// failed apply can prove disk, memory and awg0.conf stayed on bob.
func restoreApplyFailClosedFixture(t *testing.T) (*fixture, *age.X25519Identity, string, []byte, string, string) {
	t.Helper()
	f := newFixture(t)
	dir, id := setBackupsPath(t)
	alice, _, _ := f.addClient("alice")
	name := makeBackup(t, f, dir, time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC))
	archive, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	bob, _, _ := f.addClient("bob")
	if err := os.WriteFile(f.confPath, []byte("[Peer]\nPublicKey = "+bob.PublicKey+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return f, id, name, archive, alice.PublicKey, bob.PublicKey
}

func assertRestoreApplyFailClosed(t *testing.T, f *fixture, bobPub string) {
	t.Helper()
	if _, ok, err := backup.PendingPath(f.dbPath); err != nil || !ok {
		t.Fatalf("pending marker missing after failed apply: ok=%v err=%v", ok, err)
	}
	live := f.server.db()
	var n int
	if err := live.QueryRow("SELECT COUNT(*) FROM clients").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("memory handle clients = %d, want 2 (alice+bob)", n)
	}
	disk, err := db.Open(f.dbPath)
	if err != nil {
		t.Fatalf("open live database: %v", err)
	}
	defer disk.Close()
	if err := disk.QueryRow("SELECT COUNT(*) FROM clients").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("disk clients = %d, want 2 (alice+bob); disk diverged from memory", n)
	}
	cfg, err := os.ReadFile(f.confPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cfg), bobPub) {
		t.Fatal("awg0.conf lost the pre-apply peer after a failed apply")
	}
	if _, ok := f.server.cfg.Sessions.Get(f.sid); !ok {
		t.Fatal("failed apply must not wipe sessions")
	}
}

// TestRestoreApplyOpenFailKeepsPending (T-141): Open failing after
// ApplyPending must restore the pending marker and leave disk equal to
// the still-live handle — not a restored file with a restart-required flash.
func TestRestoreApplyOpenFailKeepsPending(t *testing.T) {
	f, id, name, archive, _, bobPub := restoreApplyFailClosedFixture(t)
	withRestoreFault(t, "restore.apply.open", func() {
		rec := postRestoreUpload(t, f, restoreFields(f, id.String()), map[string][]byte{name: archive})
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("code = %d, want 303", rec.Code)
		}
		if got := restoreFlashOf(t, rec); got != flashRestoreApplyFailed {
			t.Fatalf("flash = %q, want %q", got, flashRestoreApplyFailed)
		}
	})
	assertRestoreApplyFailClosed(t, f, bobPub)
}

// TestRestoreApplyMigrateFailKeepsPending (T-141): same fail-closed
// contract when Migrate fails after the file swap.
func TestRestoreApplyMigrateFailKeepsPending(t *testing.T) {
	f, id, name, archive, _, bobPub := restoreApplyFailClosedFixture(t)
	withRestoreFault(t, "restore.apply.migrate", func() {
		rec := postRestoreUpload(t, f, restoreFields(f, id.String()), map[string][]byte{name: archive})
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("code = %d, want 303", rec.Code)
		}
		if got := restoreFlashOf(t, rec); got != flashRestoreApplyFailed {
			t.Fatalf("flash = %q, want %q", got, flashRestoreApplyFailed)
		}
	})
	assertRestoreApplyFailClosed(t, f, bobPub)
}

// TestRestoreApplyGenerateFailDoesNotSwap (T-141): Generate failure
// must not leave SQLite on the restored peers while awg0.conf still
// describes the old ones.
func TestRestoreApplyGenerateFailDoesNotSwap(t *testing.T) {
	f, id, name, archive, alicePub, bobPub := restoreApplyFailClosedFixture(t)
	withRestoreFault(t, "restore.apply.generate", func() {
		rec := postRestoreUpload(t, f, restoreFields(f, id.String()), map[string][]byte{name: archive})
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("code = %d, want 303", rec.Code)
		}
		if got := restoreFlashOf(t, rec); got != flashRestoreApplyFailed {
			t.Fatalf("flash = %q, want %q", got, flashRestoreApplyFailed)
		}
	})
	assertRestoreApplyFailClosed(t, f, bobPub)
	cfg, err := os.ReadFile(f.confPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cfg), alicePub) && !strings.Contains(string(cfg), bobPub) {
		t.Fatal("awg0.conf was rewritten from the restored database despite Generate failure")
	}
}

// TestRestoreApplyInFlightHandleNotClosed (T-127): db() copies *sql.DB
// under RLock then unlocks before the query. After swap the old handle
// must stay usable so a concurrent dashboard Load cannot see
// "sql: database is closed".
func TestRestoreApplyInFlightHandleNotClosed(t *testing.T) {
	f := newFixture(t)
	dir, id := setBackupsPath(t)
	f.addClient("alice")
	name := makeBackup(t, f, dir, time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC))
	archive, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	f.addClient("bob")

	old := f.server.db()
	var stop atomic.Bool
	var http500 atomic.Int64
	errCh := make(chan error, 1)

	go func() {
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			if f.server.db() != old {
				var n int
				errCh <- old.QueryRow("SELECT COUNT(*) FROM clients").Scan(&n)
				return
			}
			time.Sleep(time.Millisecond)
		}
		errCh <- errors.New("swap never observed")
	}()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				rec := f.get("/")
				if rec.Code == http.StatusInternalServerError {
					http500.Add(1)
				}
			}
		}()
	}

	rec := postRestoreUpload(t, f, restoreFields(f, id.String()), map[string][]byte{name: archive})
	stop.Store(true)
	wg.Wait()

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("upload: code = %d, want 303", rec.Code)
	}
	if got := restoreFlashOf(t, rec); got != fmt.Sprintf(flashRestoreApplied, 1) {
		t.Fatalf("upload: flash = %q, want %q", got, fmt.Sprintf(flashRestoreApplied, 1))
	}

	select {
	case qerr := <-errCh:
		if qerr != nil {
			t.Fatalf("in-flight query on pre-swap handle: %v", qerr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for in-flight query after swap")
	}
	if n := http500.Load(); n > 0 {
		t.Fatalf("GET / returned 500 during restore-apply (%d times)", n)
	}
}
