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
