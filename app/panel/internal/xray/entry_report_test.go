package xray

import (
	"math"
	"strings"
	"testing"
)

func f64(v float64) *float64 { return &v }
func i64(v int64) *int64     { return &v }

func eqF(a, b *float64) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }
func eqI(a, b *int64) bool   { return (a == nil) == (b == nil) && (a == nil || *a == *b) }

// Узлу панель не доверяет вслепую (amnezia-vpn-server-q1do.1): проценты
// обрезаются до 0–100, числа — до неотрицательных, занятое не больше
// всего, строки версий — по шаблону и длине; чужое отбрасывается, а не
// попадает в шапку.
func TestParseEntryReport(t *testing.T) {
	r, err := ParseEntryReport([]byte(`{
		"cpu_percent": 12.34, "ram_percent": 150, "disk_percent": -3,
		"ram_used_bytes": 2048, "ram_total_bytes": 1024,
		"disk_used_bytes": -5, "disk_total_bytes": 1e30,
		"rx_bytes": 1000.9, "tx_bytes": 0, "interval_seconds": 60, "uptime_seconds": 3600,
		"xray_version": "26.10.21", "release": "3.1.0-rc.1",
		"unknown_future_field": {"x": 1}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		name string
		ok   bool
	}{
		{"cpu как есть", eqF(r.CPUPercent, f64(12.34))},
		{"ram > 100 → 100", eqF(r.RAMPercent, f64(100))},
		{"disk < 0 → 0", eqF(r.DiskPercent, f64(0))},
		{"занятая память не больше всей", eqI(r.RAMUsedBytes, i64(1024)) && eqI(r.RAMTotalBytes, i64(1024))},
		{"отрицательное → 0", eqI(r.DiskUsedBytes, i64(0))},
		{"огромное — до потолка", eqI(r.DiskTotalBytes, i64(maxEntryCounter))},
		{"дробное — целым", eqI(r.RxBytes, i64(1000))},
		{"ноль — ноль, не пусто", eqI(r.TxBytes, i64(0))},
		{"интервал", eqI(r.IntervalSeconds, i64(60))},
		{"аптайм", eqI(r.UptimeSeconds, i64(3600))},
		{"версия Xray", r.XrayVersion == "26.10.21"},
		{"версия выпуска", r.Release == "3.1.0-rc.1"},
	}
	for _, c := range checks {
		if !c.ok {
			t.Errorf("%s: %+v", c.name, r)
		}
	}

	// Нет полей — нет значений: старому или неполному узлу шапка
	// показывает прочерки, а не нули.
	r, err = ParseEntryReport([]byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if r.CPUPercent != nil || r.RAMUsedBytes != nil || r.UptimeSeconds != nil || r.XrayVersion != "" {
		t.Fatalf("пустой отчёт: %+v", r)
	}

	for name, v := range map[string]string{
		"пробел":         "3.1.0 beta",
		"разметка":       "<b>3</b>",
		"перевод строки": "3.1\n",
		"длинная":        strings.Repeat("1", 33),
		"с точки":        ".1",
		"кириллица":      "в3",
	} {
		r, err := ParseEntryReport([]byte(`{"xray_version": ` + quote(v) + `, "release": ` + quote(v) + `}`))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if r.XrayVersion != "" || r.Release != "" {
			t.Errorf("%s: версия %q принята", name, v)
		}
	}
	if r, _ := ParseEntryReport([]byte(`{"release": "` + strings.Repeat("1", 32) + `"}`)); r.Release == "" {
		t.Error("версия в 32 символа отброшена")
	}

	for name, body := range map[string]string{
		"не JSON":             `nope`,
		"не объект":           `[1,2]`,
		"строка вместо числа": `{"cpu_percent": "12"}`,
		"число вместо строки": `{"release": 3}`,
		"хвост после объекта": `{} {}`,
		"слишком большой":     `{"release": "` + strings.Repeat("a", maxEntryReportBytes) + `"}`,
		"вне float64":         `{"rx_bytes": 1e400}`,
	} {
		if _, err := ParseEntryReport([]byte(body)); err == nil {
			t.Errorf("%s: принят", name)
		}
	}
}

func TestSanitizeEntryReportDropsNonFinite(t *testing.T) {
	nan, inf := math.NaN(), math.Inf(1)
	r := EntryReport{CPUPercent: &nan, RAMPercent: &inf}.Sanitized()
	if r.CPUPercent != nil || r.RAMPercent != nil {
		t.Fatalf("NaN/Inf прошли: %+v", r)
	}
}

func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\n':
			b.WriteString(`\n`)
		case '"':
			b.WriteString(`\"`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// Состояние самообновления узла (amnezia-vpn-server-q1do.2) — тоже не
// вслепую: состояние из трёх слов, причина — код из списка (текст
// показывает панель), выпуск и метки времени — по шаблону.
func TestParseEntryReportUpdate(t *testing.T) {
	r, err := ParseEntryReport([]byte(`{"update": {"state": "failed", "target": "3.4.0", "reason": "install",
		"at_utc": "2026-10-28T10:00:00Z", "request": "2026-10-28T09:59:00Z", "extra": 1}}`))
	if err != nil {
		t.Fatal(err)
	}
	want := EntryUpdate{State: "failed", Target: "3.4.0", Reason: "install",
		AtUTC: "2026-10-28T10:00:00Z", Request: "2026-10-28T09:59:00Z"}
	if r.Update == nil || *r.Update != want {
		t.Fatalf("update = %+v", r.Update)
	}

	if r, err := ParseEntryReport([]byte(`{"update": {"state": "pwned"}}`)); err != nil || r.Update != nil {
		t.Errorf("состояние не из списка: %+v, %v", r.Update, err)
	}
	if _, err := ParseEntryReport([]byte(`{"update": "idle"}`)); err == nil {
		t.Error("update не объект — принят")
	}

	r, err = ParseEntryReport([]byte(`{"update": {"state": "idle", "target": "3.4.0; rm -rf /",
		"reason": "Позвоните нам", "at_utc": "вчера", "request": "<script>"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if r.Update == nil || *r.Update != (EntryUpdate{State: "idle"}) {
		t.Fatalf("мусор не отброшен: %+v", r.Update)
	}
	// Причина — только у неудачи и только из списка.
	r, _ = ParseEntryReport([]byte(`{"update": {"state": "updating", "reason": "install"}}`))
	if r.Update == nil || r.Update.Reason != "" {
		t.Fatalf("причина у updating: %+v", r.Update)
	}
	r, _ = ParseEntryReport([]byte(`{"update": {"state": "failed", "reason": "something-new"}}`))
	if r.Update == nil || r.Update.Reason != "" {
		t.Fatalf("неизвестная причина: %+v", r.Update)
	}
}

// Последний перезапуск Xray сторожем узла (amnezia-vpn-server-q1do.5):
// время — по RFC 3339, причина — код из списка, иначе пусто. Без времени
// записи нет вовсе: перезапуск «когда-то» панели сказать нечего.
func TestParseEntryReportWatchdog(t *testing.T) {
	r, err := ParseEntryReport([]byte(`{"watchdog": {"restarted_at_utc": "2026-10-29T08:00:00Z", "reason": "api", "extra": 1}}`))
	if err != nil {
		t.Fatal(err)
	}
	if r.Watchdog == nil || *r.Watchdog != (EntryWatchdog{RestartedAtUTC: "2026-10-29T08:00:00Z", Reason: "api"}) {
		t.Fatalf("watchdog = %+v", r.Watchdog)
	}
	r, _ = ParseEntryReport([]byte(`{"watchdog": {"restarted_at_utc": "2026-10-29T08:00:00Z", "reason": "Позвоните нам"}}`))
	if r.Watchdog == nil || r.Watchdog.Reason != "" {
		t.Fatalf("причина не из списка: %+v", r.Watchdog)
	}
	for _, body := range []string{`{"watchdog": {"restarted_at_utc": "вчера", "reason": "api"}}`, `{"watchdog": null}`, `{}`} {
		if r, err := ParseEntryReport([]byte(body)); err != nil || r.Watchdog != nil {
			t.Errorf("%s: %+v, %v", body, r.Watchdog, err)
		}
	}
	if _, err := ParseEntryReport([]byte(`{"watchdog": "api"}`)); err == nil {
		t.Error("watchdog не объект — принят")
	}
}

func TestEntryTargetVersion(t *testing.T) {
	for in, want := range map[string]string{
		"3.4.0": "3.4.0", "10.0.12": "10.0.12", "": "", "dev": "", "3.4.0-rc.1": "",
		"v3.4.0": "", "03.4.0": "", "3.4": "", " 3.4.0": "",
	} {
		if got := EntryTargetVersion(in); got != want {
			t.Errorf("EntryTargetVersion(%q) = %q, want %q", in, got, want)
		}
	}
}
