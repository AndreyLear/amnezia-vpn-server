package web

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/auth"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/backup"
)

func postAPIRestore(t *testing.T, f *fixture, token string, headerCSRF bool, files map[string][]byte) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if token != "" && !headerCSRF {
		if err := mw.WriteField(auth.CSRFFieldName, token); err != nil {
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
	req := httptest.NewRequest(http.MethodPost, "/api/backups/restore", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if headerCSRF && token != "" {
		req.Header.Set(auth.CSRFHeaderName, token)
	}
	rec := httptest.NewRecorder()
	f.serve(rec, req)
	return rec
}

func TestAPIBackupDownloadAttachment(t *testing.T) {
	f := newFixture(t)
	setBackupsPath(t)
	f.addClient("alice")

	req := httptest.NewRequest(http.MethodPost, "/api/backups/download", nil)
	req.Header.Set(auth.CSRFHeaderName, f.csrf)
	rec := httptest.NewRecorder()
	f.serve(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	cd := rec.Header().Get("Content-Disposition")
	if !strings.HasPrefix(cd, "attachment; filename=\"backup-") || !strings.HasSuffix(cd, ".tar.zst\"") {
		t.Fatalf("Content-Disposition = %q", cd)
	}
	entries := unpackArchive(t, writeTempArchive(t, rec.Body.Bytes()))
	if _, ok := entries["manifest.json"]; !ok {
		t.Fatalf("archive missing manifest: %v", entries)
	}
}

func TestAPIRestoreMissingFileJSON(t *testing.T) {
	f := newFixture(t)
	setBackupsPath(t)
	rec := postAPIRestore(t, f, f.csrf, true, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	got := decodeAPI(t, rec)
	if got["ok"] != false {
		t.Fatalf("ok = %v", got["ok"])
	}
	if got["message"] != flashRestoreMissingFile {
		t.Fatalf("message = %q, want %q", got["message"], flashRestoreMissingFile)
	}
}

func TestAPIRestoreRejectsNonArchiveName(t *testing.T) {
	f := newFixture(t)
	setBackupsPath(t)
	for _, name := range []string{"notes.txt", "foo.zip"} {
		rec := postAPIRestore(t, f, f.csrf, true, map[string][]byte{name: []byte("x")})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: code = %d, want 400; body=%s", name, rec.Code, rec.Body.String())
		}
		got := decodeAPI(t, rec)
		if got["ok"] != false {
			t.Fatalf("%s: ok = %v", name, got["ok"])
		}
		if got["message"] != flashRestoreInvalidFileName {
			t.Fatalf("%s: message = %q, want %q", name, got["message"], flashRestoreInvalidFileName)
		}
		if _, ok, err := backup.PendingPath(f.dbPath); err != nil || ok {
			t.Fatalf("%s: pending marker after reject: ok=%v err=%v", name, ok, err)
		}
	}
}

func TestAPIRestorePendingJSON409(t *testing.T) {
	f := newFixture(t)
	dir := setBackupsPath(t)
	preparedRestore(t, f, dir)
	rec := postAPIRestore(t, f, f.csrf, true, map[string][]byte{"backup-2026-08-12.tar.zst": []byte("x")})
	if rec.Code != http.StatusConflict {
		t.Fatalf("code = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	got := decodeAPI(t, rec)
	if got["message"] != flashRestorePending {
		t.Fatalf("message = %q, want %q", got["message"], flashRestorePending)
	}
	if _, ok, err := backup.PendingPath(f.dbPath); err != nil || !ok {
		t.Fatalf("pending marker lost: ok=%v err=%v", ok, err)
	}
}

func TestAPIRestoreCSRFHeaderOrPart(t *testing.T) {
	f := newFixture(t)
	dir := setBackupsPath(t)
	name := makeBackup(t, f, dir, time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC))
	archive, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}

	missing := postAPIRestore(t, f, "", false, map[string][]byte{name: archive})
	if missing.Code != http.StatusForbidden {
		t.Fatalf("no csrf: code = %d, want 403; body=%s", missing.Code, missing.Body.String())
	}
	got := decodeAPI(t, missing)
	if got["ok"] != false || got["message"] != "Forbidden." {
		t.Fatalf("no csrf body = %v", got)
	}

	viaHeader := postAPIRestore(t, f, f.csrf, true, map[string][]byte{name: archive})
	if viaHeader.Code == http.StatusForbidden {
		t.Fatalf("header csrf rejected: %s", viaHeader.Body.String())
	}

	f2 := newFixture(t)
	dir2 := setBackupsPath(t)
	name2 := makeBackup(t, f2, dir2, time.Date(2026, 8, 13, 10, 0, 0, 0, time.UTC))
	archive2, err := os.ReadFile(filepath.Join(dir2, name2))
	if err != nil {
		t.Fatal(err)
	}
	viaPart := postAPIRestore(t, f2, f2.csrf, false, map[string][]byte{name2: archive2})
	if viaPart.Code == http.StatusForbidden {
		t.Fatalf("part csrf rejected: %s", viaPart.Body.String())
	}
}

func TestAPIRestoreOversized413(t *testing.T) {
	f := newFixture(t)
	setBackupsPath(t)
	blob := bytes.Repeat([]byte("z"), MaxRestoreBodyBytes+1<<20)
	rec := postAPIRestore(t, f, f.csrf, true, map[string][]byte{"backup-2026-08-12.tar.zst": blob})
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("code = %d, want 413; body=%s", rec.Code, rec.Body.String())
	}
	got := decodeAPI(t, rec)
	if got["ok"] != false || got["message"] != "Тело запроса слишком большое." {
		t.Fatalf("413 JSON = %v", got)
	}
}

func TestAPIBackupJSONOmitsSecrets(t *testing.T) {
	f := newFixture(t)
	setBackupsPath(t)
	_, priv, psk := f.addClient("keys-check")
	req := httptest.NewRequest(http.MethodPost, "/api/backups/download", nil)
	req.Header.Set(auth.CSRFHeaderName, f.csrf)
	rec := httptest.NewRecorder()
	f.serve(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("download code = %d", rec.Code)
	}
	if strings.Contains(rec.Header().Get("Content-Disposition"), priv) {
		t.Fatal("disposition leaked private key")
	}

	fail := postAPIRestore(t, f, f.csrf, true, nil)
	body := fail.Body.String()
	if strings.Contains(body, priv) || strings.Contains(body, psk) || strings.Contains(body, samplePrivateKey) {
		t.Fatalf("error JSON leaked secrets: %s", body)
	}
}
