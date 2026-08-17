package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestAPIHostStatsUnauthorized(t *testing.T) {
	f := newFixture(t)
	req := httptest.NewRequest(http.MethodGet, "/api/stats/host", nil)
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, req)
	assertAPIUnauthorized(t, rec)
}

func TestAPIHostStatsAuthenticatedJSON(t *testing.T) {
	f := newFixture(t)
	rec := f.get("/api/stats/host")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/stats/host code = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("content-type = %q, want application/json; body=%s", ct, rec.Body.String())
	}
	got := decodeHostStats(t, rec)
	for _, key := range []string{"cpu_percent", "ram_percent", "disk_percent"} {
		if _, ok := got[key]; !ok {
			t.Fatalf("missing key %s: %v", key, got)
		}
	}
	if len(got) != 3 {
		t.Fatalf("extra fields: %v", got)
	}
}

func TestAPIHostStatsMissingProcDir(t *testing.T) {
	f := newFixture(t)
	f.server.cfg.HostProc = filepath.Join(t.TempDir(), "no-such-proc")
	f.server.cfg.DiskPath = t.TempDir()
	rec := f.get("/api/stats/host")
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	got := decodeHostStats(t, rec)
	if got["cpu_percent"] != nil {
		t.Fatalf("cpu_percent = %v, want null", got["cpu_percent"])
	}
	if got["ram_percent"] != nil {
		t.Fatalf("ram_percent = %v, want null", got["ram_percent"])
	}
	if _, ok := got["disk_percent"]; !ok {
		t.Fatalf("disk_percent missing: %v", got)
	}
}

func TestAPIClientsOKWhenHostProcGarbage(t *testing.T) {
	f := newFixture(t)
	f.server.cfg.HostProc = "/definitely/not/a/proc"
	rec := f.get("/api/clients")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/clients code = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

func decodeHostStats(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("host stats json: %v body=%s", err, rec.Body.String())
	}
	return got
}
