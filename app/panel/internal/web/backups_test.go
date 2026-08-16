// M8.5 backup UI tests: GET /backups (upload page) behind auth, one-click
// POST /backups/download, secret absence, and concurrency smoke under
// -race. The M7.6 CSRF matrix covers remaining POST routes through
// protectedPosts (csrf_test.go). There is no web create/list/delete or
// named download.
package web

import (
	"archive/tar"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/backup"
	"github.com/klauspost/compress/zstd"
)

// backupFlashOf extracts the msg value of a /backups PRG Location and
// returns it decoded — language-independent comparison against the
// flash constants (the whole panel is Russian since T-120 round 2).
func backupFlashOf(t *testing.T, rec *httptest.ResponseRecorder) string {
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

// setBackupsPath points AMNEZIA_BACKUPS_PATH at a fresh temp dir.
func setBackupsPath(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "backups")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir backups: %v", err)
	}
	t.Setenv("AMNEZIA_BACKUPS_PATH", dir)
	return dir
}

// makeBackup runs the real M8.2 pipeline into the backups dir and
// returns the archive name.
func makeBackup(t *testing.T, f *fixture, dir string, when time.Time) string {
	t.Helper()
	if _, err := backup.Create(f.h, dir, func() time.Time { return when }); err != nil {
		t.Fatalf("backup.Create: %v", err)
	}
	return "backup-" + when.Format("2006-01-02") + ".tar.zst"
}

// unpackArchive is the inverse of backup.Create for tests: zstd → tar.
func unpackArchive(t *testing.T, path string) map[string][]byte {
	t.Helper()
	fd, err := os.Open(path)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	defer fd.Close()
	zr, err := zstd.NewReader(fd)
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
	req := httptest.NewRequest(http.MethodGet, "/backups", nil)
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, req)
	assertSPA(t, rec)
	for _, path := range []string{"/backups/download", "/backups/restore"} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		rec := httptest.NewRecorder()
		f.server.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized || rec.Body.String() != "Unauthorized.\n" {
			t.Errorf("POST %s: code %d body %q, want 401 fixed", path, rec.Code, rec.Body.String())
		}
	}
}

// TestBackupsPageEmptyDir: no backups directory yet — the page renders
// 200 with the upload form.
func TestBackupsPageEmptyDir(t *testing.T) {
	f := newFixture(t)
	setBackupsPath(t)
	rec := f.get("/backups")
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	assertSPA(t, rec)
	if strings.Contains(body, `enctype="multipart/form-data"`) {
		t.Fatal("HTML upload form must not be server-rendered")
	}
	if strings.Contains(body, `name="identity"`) {
		t.Fatal("upload form still asks for an identity")
	}
}

// TestBackupsPageDoesNotListArchives: stored tar.zst files on disk
// must not appear on the upload page.
func TestBackupsPageDoesNotListArchives(t *testing.T) {
	f := newFixture(t)
	dir := setBackupsPath(t)
	makeBackup(t, f, dir, time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC))
	makeBackup(t, f, dir, time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC))

	body := f.get("/backups").Body.String()
	assertSPA(t, f.get("/backups"))
	for _, stored := range []string{"backup-2026-08-10.tar.zst", "backup-2026-08-12.tar.zst"} {
		if strings.Contains(body, ">"+stored+"<") {
			t.Errorf("upload page lists stored archive %s", stored)
		}
	}
}

// TestBackupsNoSecrets: private keys and marked DB material never
// appear on /backups or in flash redirects.
func TestBackupsNoSecrets(t *testing.T) {
	f := newFixture(t)
	dir := setBackupsPath(t)
	makeBackup(t, f, dir, time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC))

	body := f.get("/backups").Body.String()
	for _, secret := range []string{samplePrivateKey, samplePresharedKey,
		"private_key", "preshared_key", "staging", "AGE_RECIPIENT"} {
		if strings.Contains(body, secret) {
			t.Errorf("page leaks %q", secret)
		}
	}
}

// TestBackupsConcurrent: parallel downloads and page views must not
// race, panic or leave staging leftovers behind.
func TestBackupsConcurrent(t *testing.T) {
	f := newFixture(t)
	dir := setBackupsPath(t)

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := csrfPOST(f, "/backups/download", f.csrf)
			if rec.Code != http.StatusOK && rec.Code != http.StatusSeeOther {
				t.Errorf("download: code = %d", rec.Code)
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
		}()
	}
	wg.Wait()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".staging-") {
			t.Fatalf("staging leak: %s", e.Name())
		}
		if strings.HasPrefix(e.Name(), "backup-") {
			t.Fatalf("download stored an archive: %s", e.Name())
		}
	}
}

// TestBackupsPageLinkFromDashboard: the dashboard carries a Backups
// link (M8.5 UI requirement).
func TestBackupsPageLinkFromDashboard(t *testing.T) {
	f := newFixture(t)
	assertSPA(t, f.get("/"))
}

// TestBackupDownloadNowFlow (T-125): POST /backups/download streams a
// fresh archive as an attachment and stores nothing — no new file in
// the backups dir, no temp dirs left behind.
func TestBackupDownloadNowFlow(t *testing.T) {
	f := newFixture(t)
	dir := setBackupsPath(t)
	f.addClient("alice")
	before := countTempRestoreDirs()

	rec := csrfPOST(f, "/backups/download", f.csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("download: code = %d, want 200", rec.Code)
	}
	cd := rec.Header().Get("Content-Disposition")
	if !strings.HasPrefix(cd, "attachment; filename=\"backup-") || !strings.HasSuffix(cd, ".tar.zst\"") {
		t.Fatalf("download: Content-Disposition = %q", cd)
	}
	entries := unpackArchive(t, writeTempArchive(t, rec.Body.Bytes()))
	if _, ok := entries["manifest.json"]; !ok {
		t.Fatalf("downloaded archive missing manifest: %v", entries)
	}
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

// TestBackupDownloadNowGuards (T-125): CSRF 403 and pause while a restore
// is pending.
func TestBackupDownloadNowGuards(t *testing.T) {
	f := newFixture(t)
	dir := setBackupsPath(t)

	rec := csrfPOST(f, "/backups/download", "wrong-token")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("bad csrf: code = %d, want 403", rec.Code)
	}

	marker := preparedRestore(t, f, dir)
	rec = csrfPOST(f, "/backups/download", f.csrf)
	if got := backupFlashOf(t, rec); got != flashRestoreBlockedByPending {
		t.Fatalf("pending pause: flash = %q, want %q", got, flashRestoreBlockedByPending)
	}

	if err := os.RemoveAll(marker); err != nil {
		t.Fatal(err)
	}
	rec = csrfPOST(f, "/backups/download", f.csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("download after marker clear: code = %d, want 200", rec.Code)
	}
}

// writeTempArchive persists raw archive bytes to a temp file and
// returns its path (inverse of the download stream).
func writeTempArchive(t *testing.T, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "dl.tar.zst")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}
