package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// Same /proc/stat samples as hostmetrics_test.go:
// sample1 total=5750 idle_all=4100; sample2 total=8000 idle_all=4700
// → CPU = (1-600/2250)*100 ≈ 73.333...
const (
	hostStatsStat1 = "cpu  1000 200 300 4000 100 50 50 50 extra ignored\ncpu0 1 0 0 0\n"
	hostStatsStat2 = "cpu  2000 400 600 4500 200 100 100 100\ncpu0 2 0 0 0\n"
	hostStatsMem   = "MemTotal:       8000000 kB\nMemFree:        1000000 kB\nMemAvailable:   2000000 kB\n"
	hostStatsCPU   = 100.0 * (1.0 - 600.0/2250.0)
)

func writeHostStatsProc(t *testing.T, dir, stat, meminfo string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(stat), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meminfo"), []byte(meminfo), 0o644); err != nil {
		t.Fatal(err)
	}
}

func decodeHostStatsMap(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("host stats json: %v body=%s", err, rec.Body.String())
	}
	return got
}

func assertHostStatsKeys(t *testing.T, got map[string]any) {
	t.Helper()
	for _, key := range []string{
		"cpu_percent", "ram_percent", "disk_percent",
		"ram_used_bytes", "ram_total_bytes",
		"disk_used_bytes", "disk_total_bytes",
	} {
		v, ok := got[key]
		if !ok {
			t.Fatalf("missing key %s: %v", key, got)
		}
		if v == nil {
			continue
		}
		if _, ok := v.(float64); !ok {
			t.Fatalf("%s = %T %v, want number or null", key, v, v)
		}
	}
}

func TestHostStatsUnauthorized(t *testing.T) {
	f := newFixture(t)
	req := httptest.NewRequest(http.MethodGet, "/api/stats/host", nil)
	hostRec := httptest.NewRecorder()
	f.server.ServeHTTP(hostRec, req)

	meReq := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	meRec := httptest.NewRecorder()
	f.server.ServeHTTP(meRec, meReq)

	assertAPIUnauthorized(t, hostRec)
	assertAPIUnauthorized(t, meRec)
	if hostRec.Body.String() != meRec.Body.String() {
		t.Fatalf("401 body mismatch: host=%s me=%s", hostRec.Body.String(), meRec.Body.String())
	}
	got := decodeAPI(t, hostRec)
	if got["ok"] != false || got["message"] != "Unauthorized." {
		t.Fatalf("401 json = %v", got)
	}
}

func TestHostStatsAuthenticatedKeysPresent(t *testing.T) {
	f := newFixture(t)
	rec := f.get("/api/stats/host")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/stats/host code = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	got := decodeHostStatsMap(t, rec)
	assertHostStatsKeys(t, got)
}

func TestHostStatsCPUFirstNullThenPercent(t *testing.T) {
	f := newFixture(t)
	proc := t.TempDir()
	writeHostStatsProc(t, proc, hostStatsStat1, hostStatsMem)
	f.server.cfg.HostProcDir = proc
	f.server.cfg.HostDiskPath = t.TempDir()

	first := f.get("/api/stats/host")
	if first.Code != http.StatusOK {
		t.Fatalf("first GET code = %d, want 200; body=%s", first.Code, first.Body.String())
	}
	got := decodeHostStatsMap(t, first)
	assertHostStatsKeys(t, got)
	if got["cpu_percent"] != nil {
		t.Fatalf("first cpu_percent = %v, want null", got["cpu_percent"])
	}
	ram, ok := got["ram_percent"].(float64)
	if !ok {
		t.Fatalf("ram_percent = %v, want 75", got["ram_percent"])
	}
	if ram < 74.99 || ram > 75.01 {
		t.Fatalf("ram_percent = %v, want 75", ram)
	}
	wantUsed := float64((8000000 - 2000000) * 1024)
	wantTotal := float64(8000000 * 1024)
	if got["ram_used_bytes"] != wantUsed {
		t.Fatalf("ram_used_bytes = %v, want %v", got["ram_used_bytes"], wantUsed)
	}
	if got["ram_total_bytes"] != wantTotal {
		t.Fatalf("ram_total_bytes = %v, want %v", got["ram_total_bytes"], wantTotal)
	}

	writeHostStatsProc(t, proc, hostStatsStat2, hostStatsMem)
	second := f.get("/api/stats/host")
	if second.Code != http.StatusOK {
		t.Fatalf("second GET code = %d, want 200; body=%s", second.Code, second.Body.String())
	}
	got = decodeHostStatsMap(t, second)
	assertHostStatsKeys(t, got)
	cpu, ok := got["cpu_percent"].(float64)
	if !ok {
		t.Fatalf("second cpu_percent = %v, want ~73.333", got["cpu_percent"])
	}
	if cpu < hostStatsCPU-0.01 || cpu > hostStatsCPU+0.01 {
		t.Fatalf("second cpu_percent = %v, want %v", cpu, hostStatsCPU)
	}
}

func TestHostStatsMissingProcAndDiskNullThenClientsOK(t *testing.T) {
	f := newFixture(t)
	f.server.cfg.HostProcDir = filepath.Join(t.TempDir(), "no-such-proc")
	f.server.cfg.HostDiskPath = filepath.Join(t.TempDir(), "missing-mount")

	rec := f.get("/api/stats/host")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/stats/host code = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	got := decodeHostStatsMap(t, rec)
	assertHostStatsKeys(t, got)
	for _, key := range []string{
		"cpu_percent", "ram_percent", "disk_percent",
		"ram_used_bytes", "ram_total_bytes",
		"disk_used_bytes", "disk_total_bytes",
	} {
		if got[key] != nil {
			t.Fatalf("%s = %v, want null", key, got[key])
		}
	}

	clients := f.get("/api/clients")
	if clients.Code != http.StatusOK {
		t.Fatalf("GET /api/clients after host-stats miss: code = %d, want 200; body=%s", clients.Code, clients.Body.String())
	}
}
