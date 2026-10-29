package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/xray"
)

// entryPost — отчёт узла: без сессии панели, с секретом в заголовке.
func (f *fixture) entryPost(ip, authorization, body string) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/entry/report", strings.NewReader(body))
	req.Header.Set("X-Real-IP", ip)
	req.Header.Set("Content-Type", "application/json")
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, req)
	return rec
}

type hostEntryView struct {
	Name           string   `json:"name"`
	State          string   `json:"state"`
	Reporting      bool     `json:"reporting"`
	Seen           bool     `json:"seen"`
	CPUPercent     *float64 `json:"cpu_percent"`
	RAMPercent     *float64 `json:"ram_percent"`
	DiskPercent    *float64 `json:"disk_percent"`
	RAMUsedBytes   *int64   `json:"ram_used_bytes"`
	RAMTotalBytes  *int64   `json:"ram_total_bytes"`
	DiskUsedBytes  *int64   `json:"disk_used_bytes"`
	DiskTotalBytes *int64   `json:"disk_total_bytes"`
	RxBytes        *int64   `json:"rx_bytes"`
	TxBytes        *int64   `json:"tx_bytes"`
}

func hostEntries(t *testing.T, f *fixture) []hostEntryView {
	t.Helper()
	rec := f.get("/api/stats/host")
	if rec.Code != http.StatusOK {
		t.Fatalf("stats/host: код %d", rec.Code)
	}
	var got struct {
		Entries []hostEntryView `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got.Entries
}

const goodReport = `{"cpu_percent": 150, "ram_percent": 30.5, "disk_percent": 12,
	"ram_used_bytes": 300, "ram_total_bytes": 1000, "disk_used_bytes": 2254857830, "disk_total_bytes": 26843545600,
	"rx_bytes": 1200, "tx_bytes": -1, "interval_seconds": 60, "uptime_seconds": 86400,
	"xray_version": "26.10.21", "release": "3.2.0"}`

// Узел после синка присылает свои показатели (amnezia-vpn-server-q1do.1):
// тот же секрет и те же правила, что у GET /api/entry/config; значения
// панель обрезает, шапка получает их в /api/stats/host, окно «О версиях» —
// версии в /api/versions.
func TestAPIEntryReport(t *testing.T) {
	f, logs, _ := vlessFixture(t)
	token, err := xray.RotateEntryToken(f.h, "ru")
	if err != nil {
		t.Fatal(err)
	}

	// Узел только заведён: ни синка, ни отчёта — строка есть, без связи.
	if got := hostEntries(t, f); len(got) != 1 || got[0].Name != "ru" || got[0].State != "down" ||
		got[0].Seen || got[0].Reporting || got[0].CPUPercent != nil {
		t.Fatalf("новый узел: %+v", got)
	}

	// Старый узел: синхронизируется, отчётов не шлёт — на связи, но без
	// показателей.
	if rec := f.entryGet("198.51.100.7", "Bearer "+token); rec.Code != http.StatusOK {
		t.Fatalf("синк: код %d", rec.Code)
	}
	if got := hostEntries(t, f); got[0].State != "up" || !got[0].Seen || got[0].Reporting || got[0].CPUPercent != nil {
		t.Fatalf("старый узел: %+v", got)
	}

	rec := f.entryPost("198.51.100.7", "Bearer "+token, goodReport)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("отчёт: код %d: %s", rec.Code, rec.Body.String())
	}
	got := hostEntries(t, f)[0]
	if got.State != "up" || !got.Reporting || got.CPUPercent == nil || *got.CPUPercent != 100 ||
		got.RAMPercent == nil || *got.RAMPercent != 30.5 || got.DiskUsedBytes == nil || *got.DiskUsedBytes != 2254857830 ||
		got.RxBytes == nil || *got.RxBytes != 1200 || got.TxBytes == nil || *got.TxBytes != 0 {
		t.Fatalf("после отчёта: %+v", got)
	}

	var versions struct {
		Entries []struct {
			Name          string `json:"name"`
			Xray          string `json:"xray"`
			Release       string `json:"release"`
			UptimeSeconds *int64 `json:"uptime_seconds"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(f.get("/api/versions").Body.Bytes(), &versions); err != nil {
		t.Fatal(err)
	}
	if v := versions.Entries; len(v) != 1 || v[0].Xray != "26.10.21" || v[0].Release != "3.2.0" ||
		v[0].UptimeSeconds == nil || *v[0].UptimeSeconds != 86400 {
		t.Fatalf("versions: %+v", v)
	}

	// Битый отчёт не принимается и прежний не затирает.
	for name, body := range map[string]string{
		"не JSON":             "nope",
		"строка вместо числа": `{"cpu_percent": "1"}`,
		"слишком большой":     `{"release": "` + strings.Repeat("a", 8<<10) + `"}`,
	} {
		if rec := f.entryPost("198.51.100.7", "Bearer "+token, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: код %d", name, rec.Code)
		}
	}
	if got := hostEntries(t, f)[0]; got.CPUPercent == nil || *got.CPUPercent != 100 {
		t.Fatalf("битый отчёт затёр прежний: %+v", got)
	}

	for name, h := range map[string]string{
		"без заголовка": "",
		"чужой секрет":  "Bearer " + strings.Repeat("A", len(token)),
		"другая схема":  "Basic " + token,
	} {
		rec := f.entryPost("198.51.100.8", h, `{"cpu_percent": 1}`)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: код %d", name, rec.Code)
		}
	}
	if got := hostEntries(t, f)[0]; *got.CPUPercent != 100 {
		t.Fatalf("отчёт по чужому секрету принят: %+v", got)
	}
	if strings.Contains(logs.String(), token) {
		t.Fatal("секрет узла попал в журнал")
	}

	// Узел молчит дольше 5 минут: без связи, старые числа не показываются.
	old := time.Now().UTC().Add(-6 * time.Minute).Format(time.RFC3339)
	if _, err := f.h.Exec(`UPDATE xray_entries SET last_sync_utc = ?, report_utc = ?`, old, old); err != nil {
		t.Fatal(err)
	}
	if got := hostEntries(t, f)[0]; got.State != "down" || !got.Seen || got.Reporting || got.CPUPercent != nil {
		t.Fatalf("молчащий узел: %+v", got)
	}

	// Испорченная строка в базе — прочерки, а не сбой шапки.
	now := time.Now().UTC()
	if err := db.SaveXrayEntryReport(f.h, "ru", `{"cpu_percent": "x"`, now); err != nil {
		t.Fatal(err)
	}
	if got := hostEntries(t, f)[0]; got.State != "up" || got.CPUPercent != nil {
		t.Fatalf("испорченный отчёт: %+v", got)
	}
}

// Последний перезапуск Xray сторожем узла (amnezia-vpn-server-q1do.5) —
// в «О версиях» у каскада. Это след, а не показатель: виден и по старому
// отчёту, иначе через пять минут без связи пропал бы именно тогда, когда
// нужен.
func TestAPIEntryReportWatchdog(t *testing.T) {
	f, _, _ := vlessFixture(t)
	token, err := xray.RotateEntryToken(f.h, "ru")
	if err != nil {
		t.Fatal(err)
	}
	type watchdogView struct {
		RestartedAtUTC string `json:"restarted_at_utc"`
		Reason         string `json:"reason"`
	}
	versions := func() *watchdogView {
		t.Helper()
		var got struct {
			Entries []struct {
				Watchdog *watchdogView `json:"watchdog"`
			} `json:"entries"`
		}
		if err := json.Unmarshal(f.get("/api/versions").Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Entries) != 1 {
			t.Fatalf("entries: %+v", got.Entries)
		}
		return got.Entries[0].Watchdog
	}

	if rec := f.entryPost("198.51.100.7", "Bearer "+token, goodReport); rec.Code != http.StatusNoContent {
		t.Fatalf("отчёт: код %d", rec.Code)
	}
	if w := versions(); w != nil {
		t.Fatalf("перезапусков не было, а строка есть: %+v", w)
	}

	body := strings.TrimSuffix(goodReport, "}") + `, "watchdog": {"restarted_at_utc": "2026-10-29T08:00:00Z", "reason": "port"}}`
	if rec := f.entryPost("198.51.100.7", "Bearer "+token, body); rec.Code != http.StatusNoContent {
		t.Fatalf("отчёт со сторожем: код %d", rec.Code)
	}
	if w := versions(); w == nil || *w != (watchdogView{RestartedAtUTC: "2026-10-29T08:00:00Z", Reason: "port"}) {
		t.Fatalf("versions: %+v", w)
	}

	old := time.Now().UTC().Add(-6 * time.Minute).Format(time.RFC3339)
	if _, err := f.h.Exec(`UPDATE xray_entries SET last_sync_utc = ?, report_utc = ?`, old, old); err != nil {
		t.Fatal(err)
	}
	if w := versions(); w == nil || w.RestartedAtUTC != "2026-10-29T08:00:00Z" {
		t.Fatalf("узел без связи: след перезапуска пропал: %+v", w)
	}
}

// Отчёт и конфиг делят одни лимиты на адрес: секрет, кому бы он ни
// достался, не даёт запросов больше, чем было до отчётов, а подбор
// секрета через отчёт упирается в ту же стену неудач.
func TestAPIEntryReportSharesRateLimit(t *testing.T) {
	f, _, _ := vlessFixture(t)
	token, err := xray.RotateEntryToken(f.h, "ru")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < entryRequestLimit-1; i++ {
		if rec := f.entryGet("203.0.113.7", "Bearer "+token); rec.Code != http.StatusOK {
			t.Fatalf("запрос #%d: код %d", i+1, rec.Code)
		}
	}
	if rec := f.entryPost("203.0.113.7", "Bearer "+token, `{}`); rec.Code != http.StatusNoContent {
		t.Fatalf("последний разрешённый запрос: код %d", rec.Code)
	}
	if rec := f.entryPost("203.0.113.7", "Bearer "+token, `{}`); rec.Code != http.StatusTooManyRequests ||
		rec.Header().Get("Retry-After") == "" {
		t.Fatalf("сверх общего лимита: код %d", rec.Code)
	}

	bad := "Bearer " + strings.Repeat("B", len(token))
	for i := 0; i < entryFailLimit; i++ {
		if rec := f.entryPost("203.0.113.5", bad, `{}`); rec.Code != http.StatusUnauthorized {
			t.Fatalf("неудача #%d: код %d", i+1, rec.Code)
		}
	}
	if rec := f.entryGet("203.0.113.5", "Bearer "+token); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("конфиг после %d неудач отчёта: код %d", entryFailLimit, rec.Code)
	}
}
