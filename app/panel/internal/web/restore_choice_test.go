package web

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/auth"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/backup"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

// postAPIRestoreWith submits an archive to the JSON API with extra form
// fields (the endpoint choice).
func postAPIRestoreWith(t *testing.T, f *fixture, fields map[string]string, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField(auth.CSRFFieldName, f.csrf); err != nil {
		t.Fatal(err)
	}
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	pw, err := mw.CreateFormFile("backup", "backup-2026-08-12.tar.zst")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/backups/restore", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: f.sid})
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, req)
	return rec
}

// archiveFromOtherHost captures a backup carrying the given endpoint and
// MTU, then moves the live database to the values a freshly installed
// server would have — the migration situation.
func archiveFromOtherHost(t *testing.T, f *fixture, dir, oldEndpoint, oldMTU, newEndpoint, newMTU string) []byte {
	t.Helper()
	setSetting(t, f, "endpoint", oldEndpoint)
	if oldMTU != "" {
		setSetting(t, f, "mtu", oldMTU)
	}
	name := makeBackup(t, f, dir, time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC))
	setSetting(t, f, "endpoint", newEndpoint)
	setSetting(t, f, "mtu", newMTU)
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	return data
}

func setSetting(t *testing.T, f *fixture, key, value string) {
	t.Helper()
	if err := db.SetSetting(f.h, key, value); err != nil {
		t.Fatalf("SetSetting %s: %v", key, err)
	}
}

func getSetting(t *testing.T, f *fixture, key string) string {
	t.Helper()
	// The server reopens its handle after applying a restore, so the test
	// must read through the server rather than the fixture's original one.
	v, _, err := db.GetSetting(f.server.db(), key)
	if err != nil {
		t.Fatalf("GetSetting %s: %v", key, err)
	}
	return v
}

func decodeRestoreJSON(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	return out
}

// A backup from another server carries that server's address. Applying it
// silently would point every client at a machine the operator just left, so
// the upload stops and asks — without changing anything.
func TestRestoreAsksWhenArchiveCarriesAnotherAddress(t *testing.T) {
	f := newFixture(t)
	dir := setBackupsPath(t)
	data := archiveFromOtherHost(t, f, dir, "old.example.com:443", "1380", "2.26.93.192:443", "1416")

	rec := postAPIRestoreWith(t, f, nil, data)
	if rec.Code != http.StatusConflict {
		t.Fatalf("code = %d, want 409 asking for a choice (body %s)", rec.Code, rec.Body.String())
	}
	body := decodeRestoreJSON(t, rec)
	if body["needs_choice"] != true {
		t.Fatalf("needs_choice missing from %v", body)
	}
	if body["archive_endpoint"] != "old.example.com:443" || body["server_endpoint"] != "2.26.93.192:443" {
		t.Fatalf("both addresses must be reported, got %v", body)
	}
	if body["archive_mtu"] != "1380" || body["server_mtu"] != "1416" {
		t.Fatalf("both MTUs must be reported, got %v", body)
	}
	// Nothing may have been applied while the question is open.
	if got := getSetting(t, f, "endpoint"); got != "2.26.93.192:443" {
		t.Fatalf("endpoint changed to %q while asking", got)
	}
	if _, ok, err := backup.PendingPath(f.dbPath); err != nil || ok {
		t.Fatalf("a pending restore must not be created while asking (ok=%v err=%v)", ok, err)
	}
}

// Keeping the archive's address is the domain migration: DNS is repointed
// and existing client configs keep working.
func TestRestoreKeepsArchiveAddressWhenChosen(t *testing.T) {
	f := newFixture(t)
	dir := setBackupsPath(t)
	data := archiveFromOtherHost(t, f, dir, "old.example.com:443", "1380", "2.26.93.192:443", "1416")

	rec := postAPIRestoreWith(t, f, map[string]string{"endpoint": "archive"}, data)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if got := getSetting(t, f, "endpoint"); got != "old.example.com:443" {
		t.Fatalf("endpoint = %q, want the archived address", got)
	}
}

// Choosing this server keeps the address clients will actually reach, and
// with it the MTU measured for this uplink.
func TestRestoreKeepsServerAddressWhenChosen(t *testing.T) {
	f := newFixture(t)
	dir := setBackupsPath(t)
	data := archiveFromOtherHost(t, f, dir, "old.example.com:443", "1380", "2.26.93.192:443", "1416")

	rec := postAPIRestoreWith(t, f, map[string]string{"endpoint": "server"}, data)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if got := getSetting(t, f, "endpoint"); got != "2.26.93.192:443" {
		t.Fatalf("endpoint = %q, want this server's address", got)
	}
	if got := getSetting(t, f, "mtu"); got != "1416" {
		t.Fatalf("mtu = %q, want the value measured for this uplink", got)
	}
}

// The MTU belongs to this host's uplink, not to the backup: it is measured
// during install and must survive a restore whichever address the operator
// keeps. An archive taken before the setting existed carries none at all,
// and letting that win puts the tunnel back on a guessed default.
func TestRestoreKeepsMeasuredMTUWithArchiveAddress(t *testing.T) {
	f := newFixture(t)
	dir := setBackupsPath(t)
	data := archiveFromOtherHost(t, f, dir, "old.example.com:443", "", "2.26.93.192:443", "1416")

	rec := postAPIRestoreWith(t, f, map[string]string{"endpoint": "archive"}, data)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if got := getSetting(t, f, "endpoint"); got != "old.example.com:443" {
		t.Fatalf("endpoint = %q, want the archived address", got)
	}
	if got := getSetting(t, f, "mtu"); got != "1416" {
		t.Fatalf("mtu = %q, want this host's measured 1416", got)
	}
}

// Restoring onto the same server is the common case and must stay a single
// step: identical values raise no question.
func TestRestoreDoesNotAskWhenAddressMatches(t *testing.T) {
	f := newFixture(t)
	dir := setBackupsPath(t)
	setSetting(t, f, "endpoint", "vpn.example.com:443")
	setSetting(t, f, "mtu", "1400")
	name := makeBackup(t, f, dir, time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC))
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}

	rec := postAPIRestoreWith(t, f, nil, data)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200 without any question (body %s)", rec.Code, rec.Body.String())
	}
}
