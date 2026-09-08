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

	series, err := ReadSpeedSeries(path, testKey, base, base.Add(10*time.Second), 2)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// The rate lands in the column its interval ENDED in: the interval
	// [base, base+5s] belongs to the second half of a ten-second window.
	col := series.Columns[1]
	if !col.HasData {
		t.Fatalf("the rate did not land in the column its interval ended in")
	}
	if col.DownMax != 10_000_000 {
		t.Errorf("down = %d bit/s, want 10 000 000", col.DownMax)
	}
	if col.UpMax != 1_000_000 {
		t.Errorf("up = %d bit/s, want 1 000 000", col.UpMax)
	}
	if series.Columns[0].HasData {
		t.Errorf("first column has data; nothing had been differenced yet when it started")
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
	if series.Columns[1].HasData {
		t.Errorf("the reset interval produced a rate; it must be a gap")
	}
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

// A day-long window has to reach into the file that rotated out, or
// "yesterday evening" would always be empty.
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
