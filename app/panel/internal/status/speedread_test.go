package status

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testKey = "AAAAAAAAAAAA"

// writeLog lays down a log by hand: the reader must cope with whatever is
// on disk, including what an older version wrote.
func writeLog(t *testing.T, path string, lines ...string) {
	t.Helper()
	body := SpeedSchema + "\n"
	if len(lines) > 0 {
		body += strings.Join(lines, "\n") + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func sampleLine(at time.Time, rx, tx uint64) string {
	return fmt.Sprintf("%d %s:%d:%d", at.UTC().Unix(), testKey, rx, tx)
}

// A rate is the difference between two samples, in bits per second —
// what a person reads a chart in.
func TestReadSpeedSeriesDerivesRates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "speed.log")
	base := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	// 5 seconds, 6 250 000 bytes towards the client = 10 Mbit/s.
	writeLog(t, path,
		sampleLine(base, 0, 0),
		sampleLine(base.Add(5*time.Second), 625_000, 6_250_000),
	)

	series, err := ReadSpeedSeries(path, testKey, base, base.Add(20*time.Second), 4)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// The rate belongs to the INTERVAL [a, b], so it paints every column
	// that interval touches — here the first two of four.
	for _, i := range []int{0, 1} {
		col := series.Columns[i]
		if !col.HasData {
			t.Fatalf("column %d is empty; the interval covers it", i)
		}
		if col.DownMax != 10_000_000 {
			t.Errorf("column %d: down = %d bit/s, want 10 000 000", i, col.DownMax)
		}
		if col.UpMax != 1_000_000 {
			t.Errorf("column %d: up = %d bit/s, want 1 000 000", i, col.UpMax)
		}
	}
	// And nothing beyond it: no interval reached there.
	for _, i := range []int{2, 3} {
		if series.Columns[i].HasData {
			t.Errorf("column %d has data; no interval reached it", i)
		}
	}
}

// The whole point of the chart. Averaging twenty-five samples turns a
// one-second collapse into invisible ripple; extremes keep it.
func TestReadSpeedSeriesKeepsExtremesNotAverage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "speed.log")
	base := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	var lines []string
	var tx uint64
	// Nine fast seconds and one collapsed one, all inside one column.
	for i := range 11 {
		lines = append(lines, sampleLine(base.Add(time.Duration(i)*time.Second), 0, tx))
		if i == 4 {
			tx += 125_000 // 1 Mbit/s
		} else {
			tx += 12_500_000 // 100 Mbit/s
		}
	}
	writeLog(t, path, lines...)

	series, err := ReadSpeedSeries(path, testKey, base, base.Add(11*time.Second), 1)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	col := series.Columns[0]
	if col.DownMin != 1_000_000 {
		t.Errorf("min = %d, want the collapsed second (1 000 000) to survive the fold", col.DownMin)
	}
	if col.DownMax != 100_000_000 {
		t.Errorf("max = %d, want 100 000 000", col.DownMax)
	}
}

// A silence means nobody was looking. Differencing across it would draw a
// low flat line over a period we know nothing about.
func TestReadSpeedSeriesTreatsSilenceAsGap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "speed.log")
	base := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	writeLog(t, path,
		sampleLine(base, 0, 0),
		sampleLine(base.Add(10*time.Minute), 0, 6_000_000_000),
	)

	series, err := ReadSpeedSeries(path, testKey, base, base.Add(11*time.Minute), 11)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for i, col := range series.Columns {
		if col.HasData {
			t.Fatalf("column %d has data; ten minutes of silence must stay a gap, not become an average", i)
		}
	}
}

// Counters that went backwards mean the interface was recreated, not that
// the client sent negative traffic.
func TestReadSpeedSeriesTreatsCounterResetAsGap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "speed.log")
	base := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	writeLog(t, path,
		sampleLine(base, 1_000_000, 9_000_000),
		sampleLine(base.Add(5*time.Second), 0, 0),
		sampleLine(base.Add(10*time.Second), 625_000, 6_250_000),
	)

	series, err := ReadSpeedSeries(path, testKey, base, base.Add(15*time.Second), 3)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// Отрезок со сбросом счётчика не даёт скорости вовсе, поэтому столбец,
	// который занимает только он, остаётся пустым.
	if series.Columns[0].HasData {
		t.Errorf("the reset interval produced a rate; it must be a gap")
	}
	// А следующий за ним отрезок считается как обычно.
	if !series.Columns[2].HasData {
		t.Errorf("the interval after the reset must count again")
	}
}

// Another peer's numbers must never end up on this client's chart.
func TestReadSpeedSeriesIgnoresOtherPeers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "speed.log")
	base := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	writeLog(t, path,
		fmt.Sprintf("%d %s:0:0 BBBBBBBBBBBB:0:0", base.Unix(), testKey),
		fmt.Sprintf("%d %s:0:625000 BBBBBBBBBBBB:0:99999999999", base.Add(5*time.Second).Unix(), testKey),
	)

	series, err := ReadSpeedSeries(path, testKey, base, base.Add(10*time.Second), 1)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got := series.Columns[0].DownMax; got != 1_000_000 {
		t.Errorf("down = %d, want only this peer's 1 000 000", got)
	}
}

// Производитель пишет замер раз в пять секунд, но иногда раз в шесть, а
// столбец десятиминутного окна — ровно пять секунд. Пока красился только
// столбец, где отрезок кончился, такой отрезок перепрыгивал через столбец, и
// график показывал «замеров нет» посреди сплошной загрузки
// (amnezia-vpn-server-bwp3).
func TestReadSpeedSeriesSurvivesLateSample(t *testing.T) {
	path := filepath.Join(t.TempDir(), "speed.log")
	base := time.Date(2026, 9, 9, 1, 45, 0, 0, time.UTC)
	// Замер опоздал на секунду: шесть секунд вместо пяти.
	writeLog(t, path,
		sampleLine(base, 0, 0),
		sampleLine(base.Add(6*time.Second), 0, 60_000_000),
	)

	// Окно 20 секунд на 4 столбца — по пять секунд, как в десятиминутном.
	series, err := ReadSpeedSeries(path, testKey, base, base.Add(20*time.Second), 4)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for _, i := range []int{0, 1} {
		if !series.Columns[i].HasData {
			t.Fatalf("столбец %d пуст, хотя отрезок его накрывает: опоздание замера рисует ложную дырку", i)
		}
	}
}

// A day-long window has to reach into the file that rotated out, or
// "yesterday evening" would always be empty. This exercises the legacy
// "speed.prev.log" tier directly: a build from before dated rotation
// (amnezia-vpn-server-b0fl) wrote only that file, and an update must not
// erase whatever it had already collected.
func TestReadSpeedSeriesReachesIntoThePreviousFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "speed.log")
	base := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	writeLog(t, SpeedPrevPath(path),
		sampleLine(base, 0, 0),
		sampleLine(base.Add(5*time.Second), 0, 6_250_000),
	)
	writeLog(t, path, sampleLine(base.Add(time.Hour), 0, 6_250_000))

	series, err := ReadSpeedSeries(path, testKey, base, base.Add(2*time.Hour), 4)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !series.Columns[0].HasData {
		t.Fatalf("the rotated-out file was not read; a day window would always start empty")
	}
	if got := series.Columns[0].DownMax; got != 10_000_000 {
		t.Errorf("down = %d, want 10 000 000 from the previous file", got)
	}
}

// A window several days wide must stitch samples back together across
// every dated file it touches, not just the newest one
// (amnezia-vpn-server-b0fl): otherwise a report spanning "since last
// Tuesday" would silently start on the wrong day.
func TestReadSpeedSamplesSpansMultipleDatedDays(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "speed.log")
	day0 := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	day1 := day0.AddDate(0, 0, 1)
	day2 := day0.AddDate(0, 0, 2)

	writeLog(t, speedDatedPath(path, day0), sampleLine(day0.Add(12*time.Hour), 0, 1))
	writeLog(t, speedDatedPath(path, day1), sampleLine(day1.Add(12*time.Hour), 0, 2))
	writeLog(t, path, sampleLine(day2.Add(12*time.Hour), 0, 3))

	samples, err := readSpeedSamples(path, testKey, day0, day2.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(samples) != 3 {
		t.Fatalf("samples = %d, want 3 (one per day): %+v", len(samples), samples)
	}
	for i, want := range []uint64{1, 2, 3} {
		if samples[i].tx != want {
			t.Errorf("sample %d tx = %d, want %d (days out of order)", i, samples[i].tx, want)
		}
	}
}

// forbidFile writes one sample line to path and then strips every
// permission from it, so opening it turns into a hard error instead of a
// silent, wasteful read. A test that reads successfully around such a file
// is proof the file was never opened - the only way an unreadable file
// causes no error.
func forbidFile(t *testing.T, path string, at time.Time) {
	t.Helper()
	writeLog(t, path, sampleLine(at, 0, 0))
	if err := os.Chmod(path, 0); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
}

// A dated file whose name already places it outside [from-gap, to] must
// never be opened (amnezia-vpn-server-b0fl): with speedRetentionDays of
// files potentially on disk, a short query has to skip almost all of them
// by name alone. Both directions matter - a day older than the window and
// a day newer than it - because readSpeedDatedFiles walks newest first and
// the two bounds need different loop actions (continue past a too-new day,
// since an in-window day can still follow it; break on a too-old one).
func TestReadSpeedSeriesNeverOpensFilesOutsideTheWindow(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores file permissions")
	}

	tests := []struct {
		name string
		// build lays down fixtures under path (a fresh temp dir's
		// "speed.log") and returns the query window.
		build func(t *testing.T, path string) (from, to time.Time)
	}{
		{
			// The window reaches back from the live file; only an older
			// day sits outside it.
			name: "window reaches back from the live file",
			build: func(t *testing.T, path string) (time.Time, time.Time) {
				base := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
				writeLog(t, path, sampleLine(base, 0, 0), sampleLine(base.Add(5*time.Second), 0, 6_250_000))
				forbidFile(t, speedDatedPath(path, base.AddDate(0, 0, -20)), base.AddDate(0, 0, -20))
				return base, base.Add(10 * time.Second)
			},
		},
		{
			// The window lies entirely in the past: no live-file data is
			// in range, the wanted day is a dated file, and both older
			// and newer dated days sit outside the window.
			name: "window lies entirely in the past",
			build: func(t *testing.T, path string) (time.Time, time.Time) {
				base := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
				writeLog(t, speedDatedPath(path, base),
					sampleLine(base, 0, 0), sampleLine(base.Add(5*time.Second), 0, 6_250_000))
				forbidFile(t, speedDatedPath(path, base.AddDate(0, 0, -10)), base.AddDate(0, 0, -10))
				for i := 1; i <= 3; i++ {
					forbidFile(t, speedDatedPath(path, base.AddDate(0, 0, i)), base.AddDate(0, 0, i))
				}
				return base, base.Add(10 * time.Second)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "speed.log")
			from, to := tt.build(t, path)

			series, err := ReadSpeedSeries(path, testKey, from, to, 1)
			if err != nil {
				t.Fatalf("read: %v, want out-of-window files never to be opened", err)
			}
			if !series.Columns[0].HasData {
				t.Fatalf("column has no data; the in-window day was not read")
			}
			if got := series.Columns[0].DownMax; got != 10_000_000 {
				t.Errorf("down = %d, want 10 000 000 from the in-window day", got)
			}
		})
	}
}

// A server that has not written history yet is the normal state right
// after an update; that must read as "nothing yet", not as a failure.
func TestReadSpeedSeriesMissingFileIsEmpty(t *testing.T) {
	base := time.Now().UTC()
	series, err := ReadSpeedSeries(filepath.Join(t.TempDir(), "speed.log"), testKey,
		base.Add(-time.Hour), base, 5)
	if err != nil {
		t.Fatalf("missing file: %v", err)
	}
	for i, col := range series.Columns {
		if col.HasData {
			t.Fatalf("column %d has data from a file that does not exist", i)
		}
	}
}

// One unreadable line must not cost the rest of the day.
func TestReadSpeedSeriesSkipsUnreadableLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "speed.log")
	base := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	writeLog(t, path,
		sampleLine(base, 0, 0),
		"половина строки после обрыва",
		"#speed v9 из будущего",
		sampleLine(base.Add(5*time.Second), 0, 6_250_000),
	)

	series, err := ReadSpeedSeries(path, testKey, base, base.Add(10*time.Second), 1)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got := series.Columns[0].DownMax; got != 10_000_000 {
		t.Errorf("down = %d, want the good pair to still produce a rate", got)
	}
}

// The tail is read backwards in chunks; a window that reaches past one
// chunk must still find its samples.
func TestReadSpeedSeriesReadsBeyondOneChunk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "speed.log")
	base := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	var lines []string
	var tx uint64
	pad := strings.Repeat("PADPADPADPAD", 40)
	for i := range 4000 {
		at := base.Add(time.Duration(i) * 5 * time.Second)
		lines = append(lines, sampleLine(at, 0, tx)+" "+pad+":0:0")
		tx += 6_250_000
	}
	writeLog(t, path, lines...)
	if info, err := os.Stat(path); err != nil || info.Size() <= 256<<10 {
		t.Fatalf("file is %v bytes, want more than one chunk", info.Size())
	}

	// The very first samples sit at the far end of the file.
	series, err := ReadSpeedSeries(path, testKey, base, base.Add(time.Minute), 12)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !series.Columns[1].HasData {
		t.Fatalf("nothing found at the start of a multi-chunk file")
	}
	if got := series.Columns[1].DownMax; got != 10_000_000 {
		t.Errorf("down = %d, want 10 000 000", got)
	}
}

// sampleLineV2 renders a line in the format that carries the handshake age
// (amnezia-vpn-server-3wbe). sampleLine above stays on the old format on
// purpose: the reader must keep understanding it, and the tests that use
// it are what prove it.
func sampleLineV2(at time.Time, rx, tx uint64, handshake string) string {
	return fmt.Sprintf("%d %s:%d:%d:%s", at.UTC().Unix(), testKey, rx, tx, handshake)
}

// Обновление приходит посреди суток, а ротация — по суткам, поэтому один
// файл штатно держит записи обоих форматов. Потерять из-за этого утро —
// значит потерять ровно ту историю, ради которой всё и заведено
// (amnezia-vpn-server-3wbe).
func TestReadSpeedSeriesReadsBothFormatsInOneFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "speed.log")
	from := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	to := from.Add(20 * time.Second)
	writeLog(t, path,
		sampleLine(from, 0, 0),
		sampleLine(from.Add(5*time.Second), 1_250_000, 1_250_000),
		sampleLineV2(from.Add(10*time.Second), 2_500_000, 2_500_000, "7"),
		sampleLineV2(from.Add(15*time.Second), 3_750_000, 3_750_000, "12"),
	)

	series, err := ReadSpeedSeries(path, testKey, from, to, 4)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for i, col := range series.Columns {
		if !col.HasData {
			t.Fatalf("столбец %d без данных — строка одного из форматов потеряна: %+v", i, series.Columns)
		}
	}
	// Первый столбец красит только пара из строк старого формата, и признака
	// живости он не несёт — это не «был на связи», а «неизвестно».
	if series.Columns[0].HasLiveness {
		t.Errorf("столбец из записей старого формата заявил, что знает про связь")
	}
	if !series.Columns[3].HasLiveness || !series.Columns[3].Online {
		t.Errorf("столбец из записей нового формата: HasLiveness=%v Online=%v, ждали true/true",
			series.Columns[3].HasLiveness, series.Columns[3].Online)
	}
}

// Главное различение: одинаковые нули в трафике — у плеера, добирающего
// буфер, и у оборвавшегося туннеля. Отличает их только возраст
// рукопожатия (amnezia-vpn-server-3wbe).
func TestReadSpeedSeriesTellsBufferingFromOutage(t *testing.T) {
	from := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)

	for _, tc := range []struct {
		name       string
		handshake  string
		wantOnline bool
	}{
		{"плеер добирает буфер — рукопожатие свежее", "40", true},
		{"на границе срока — ещё на связи", "180", true},
		{"рукопожатие состарилось — связи не было", "600", false},
		{"рукопожатия не было вовсе", SpeedNoHandshake, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "speed.log")
			// Трафика в обоих случаях нет: счётчики не двигаются.
			writeLog(t, path,
				sampleLineV2(from, 1000, 2000, tc.handshake),
				sampleLineV2(from.Add(5*time.Second), 1000, 2000, tc.handshake),
			)

			series, err := ReadSpeedSeries(path, testKey, from, from.Add(10*time.Second), 2)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			col := series.Columns[1]
			if !col.HasData {
				t.Fatalf("столбец без данных: %+v", col)
			}
			if col.DownMax != 0 || col.UpMax != 0 {
				t.Fatalf("трафик не нулевой, случай не тот: %+v", col)
			}
			if tc.handshake == SpeedNoHandshake {
				if col.HasLiveness {
					t.Errorf("без рукопожатия признак живости не может быть известен")
				}
				return
			}
			if !col.HasLiveness {
				t.Fatalf("признак живости потерян: %+v", col)
			}
			if col.Online != tc.wantOnline {
				t.Errorf("Online = %v, ждали %v при возрасте рукопожатия %s", col.Online, tc.wantOnline, tc.handshake)
			}
		})
	}
}

// В суточном окне столбец — это минуты, и обрыв внутри него ровно то, что
// ищут. Один здоровый замер не должен закрашивать его «всё хорошо»
// (amnezia-vpn-server-3wbe).
func TestReadSpeedSeriesColumnIsOnlineOnlyIfEverySampleWas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "speed.log")
	from := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	writeLog(t, path,
		sampleLineV2(from, 0, 0, "5"),
		sampleLineV2(from.Add(5*time.Second), 100, 100, "10"),
		sampleLineV2(from.Add(10*time.Second), 200, 200, "900"),
		sampleLineV2(from.Add(15*time.Second), 300, 300, "15"),
	)

	// Одно окно — один столбец: все замеры попадают в него.
	series, err := ReadSpeedSeries(path, testKey, from, from.Add(20*time.Second), 1)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	col := series.Columns[0]
	if !col.HasLiveness {
		t.Fatalf("признак живости потерян: %+v", col)
	}
	if col.Online {
		t.Error("столбец назван «на связи», хотя внутри него связь пропадала")
	}
}
