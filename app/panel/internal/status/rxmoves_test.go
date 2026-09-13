package status

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Последнее движение rx берётся из последнего сэмпла, где счётчик вырос;
// сброс счётчика — новая точка отсчёта, а не трафик
// (amnezia-vpn-server-0d2n).
func TestReadRxActivity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "speed.log")
	base := time.Date(2026, 9, 13, 3, 0, 0, 0, time.UTC)
	line := func(off int, fields ...string) string {
		return strings.Join(append([]string{unixStr(base.Add(time.Duration(off) * time.Second).Unix())}, fields...), " ")
	}
	body := strings.Join([]string{
		line(0, "a:100:0:1", "b:500:0:1", "c:10:0:-"),
		line(5, "a:150:0:1", "b:500:0:1", "c:10:0:-"),
		line(10, "a:150:0:1", "b:600:0:1", "c:10:0:-"),
		line(15, "a:150:0:1", "b:5:0:1", "c:10:0:-"), // b сброшен: не движение
		"torn line",
		line(20, "a:150:0:1", "b:5:0:1"),
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadRxActivity(path, base, base.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !got.LastMove["a"].Equal(base.Add(5 * time.Second)) {
		t.Errorf("a: %v", got.LastMove["a"])
	}
	if !got.LastMove["b"].Equal(base.Add(10 * time.Second)) {
		t.Errorf("b: %v — сброс счётчика принят за движение", got.LastMove["b"])
	}
	if _, ok := got.LastMove["c"]; ok {
		t.Error("c не двигался, а попал в активность")
	}
	if !got.LastSample.Equal(base.Add(20 * time.Second)) {
		t.Errorf("последний сэмпл %v", got.LastSample)
	}

	// Окно, начатое до полуночи, дочитывает файл прошлого дня.
	midnight := time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)
	prev := speedDatedPath(path, base)
	if err := os.WriteFile(prev, []byte(unixStr(midnight.Add(-10*time.Second).Unix())+" d:1:0:1\n"+unixStr(midnight.Add(-5*time.Second).Unix())+" d:2:0:1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(unixStr(midnight.Add(5*time.Second).Unix())+" d:2:0:1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = ReadRxActivity(path, midnight.Add(-time.Minute), midnight.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !got.LastMove["d"].Equal(midnight.Add(-5 * time.Second)) {
		t.Errorf("через полночь: d = %v", got.LastMove["d"])
	}

	if got, err := ReadRxActivity(filepath.Join(dir, "none.log"), base, base); err != nil || len(got.LastMove) != 0 || !got.LastSample.IsZero() {
		t.Errorf("нет файла: %+v, %v", got, err)
	}
}

func unixStr(n int64) string { return strconv.FormatInt(n, 10) }
