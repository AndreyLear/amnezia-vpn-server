package status

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sampleStatus(at time.Time, peers ...Peer) *Status {
	return &Status{
		Schema:      "v1",
		GeneratedAt: at,
		Interface:   &Interface{Iface: "awg0", HasInterface: true},
		Peers:       peers,
	}
}

func peer(key string, rx, tx uint64) Peer {
	return Peer{PublicKey: key, RxBytes: rx, TxBytes: tx}
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

// A tick must land as one line carrying the cumulative counters, so a
// reader can difference two lines into a rate.
func TestAppendSampleWritesCumulativeCounters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "speed.log")
	at := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	key := "AAAAAAAAAAAABBBBBBBBBBBBCCCCCCCCCCCCDDDDDDDD"

	if err := AppendSample(path, sampleStatus(at, peer(key, 100, 200))); err != nil {
		t.Fatalf("first sample: %v", err)
	}
	if err := AppendSample(path, sampleStatus(at.Add(5*time.Second), peer(key, 700, 260))); err != nil {
		t.Fatalf("second sample: %v", err)
	}

	lines := readLines(t, path)
	if len(lines) != 3 {
		t.Fatalf("lines = %d, want 3 (schema + two ticks): %q", len(lines), lines)
	}
	if lines[0] != SpeedSchema {
		t.Errorf("first line = %q, want the schema marker %q", lines[0], SpeedSchema)
	}
	want := fmt.Sprintf("%d AAAAAAAAAAAA:100:200:-", at.Unix())
	if lines[1] != want {
		t.Errorf("first tick = %q, want %q", lines[1], want)
	}
	if got, want := lines[2], fmt.Sprintf("%d AAAAAAAAAAAA:700:260:-", at.Add(5*time.Second).Unix()); got != want {
		t.Errorf("second tick = %q, want %q; counters must stay cumulative, not become deltas", got, want)
	}
}

// The full 44-character key on every line would be most of the file, and
// it is already in status.json.
func TestAppendSampleShortensKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "speed.log")
	key := "abcdefghijkl/+34567890123456789012345678901="
	if err := AppendSample(path, sampleStatus(time.Now(), peer(key, 1, 2))); err != nil {
		t.Fatalf("append: %v", err)
	}
	body := strings.Join(readLines(t, path), "\n")
	if strings.Contains(body, key) {
		t.Errorf("log carries the full key; want only its first %d characters", SpeedKeyLen)
	}
	if !strings.Contains(body, "abcdefghijkl:1:2") {
		t.Errorf("log = %q, want the shortened key", body)
	}
}

// A tick without peers is exactly the case a reader must show as a gap.
// A line claiming zero bytes would be a diagnosis nobody made.
func TestAppendSampleSkipsEmptyTick(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "speed.log")
	now := time.Now()

	if err := AppendSample(path, sampleStatus(now)); err != nil {
		t.Fatalf("empty status: %v", err)
	}
	if err := AppendSample(path, &Status{GeneratedAt: now}); err != nil {
		t.Fatalf("no interface: %v", err)
	}
	if err := AppendSample(path, nil); err != nil {
		t.Fatalf("nil status: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("stat = %v, want the file never to be created", err)
	}
}

// An empty path is how a caller says "do not record".
func TestAppendSampleWithoutPathIsNoOp(t *testing.T) {
	if err := AppendSample("", sampleStatus(time.Now(), peer("k", 1, 2))); err != nil {
		t.Fatalf("append: %v", err)
	}
}

// Rotation keeps "at least 24 hours" true: the day that just ended moves
// aside whole, and the new file starts empty.
func TestAppendSampleRotatesOnUTCDay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "speed.log")
	key := "AAAAAAAAAAAAxxxx"
	late := time.Date(2026, 9, 8, 23, 59, 55, 0, time.UTC)

	if err := AppendSample(path, sampleStatus(late, peer(key, 10, 20))); err != nil {
		t.Fatalf("day one: %v", err)
	}
	if err := AppendSample(path, sampleStatus(late.Add(10*time.Second), peer(key, 30, 40))); err != nil {
		t.Fatalf("day two: %v", err)
	}

	current := readLines(t, path)
	if len(current) != 2 || !strings.HasSuffix(current[1], ":30:40:-") {
		t.Fatalf("current file = %q, want the schema and only the new day", current)
	}
	rotated := readLines(t, speedDatedPath(path, time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)))
	if len(rotated) != 2 || !strings.HasSuffix(rotated[1], ":10:20:-") {
		t.Fatalf("rotated file = %q, want the whole day that ended", rotated)
	}
}

// Same day, later hour: nothing moves. Rotating per tick would leave one
// line of history.
func TestAppendSampleKeepsFileWithinTheDay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "speed.log")
	at := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	for i := range 3 {
		if err := AppendSample(path, sampleStatus(at.Add(time.Duration(i)*time.Hour), peer("k", uint64(i), 0))); err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}
	}
	if lines := readLines(t, path); len(lines) != 4 {
		t.Fatalf("lines = %d, want the schema and three ticks", len(lines))
	}
	if _, err := os.Stat(SpeedPrevPath(path)); !os.IsNotExist(err) {
		t.Errorf("previous file exists; nothing should have rotated within one day")
	}
}

// The day is read from the file, not from its modification time: a
// restored copy or a touched file must not throw away a day of history.
func TestAppendSampleIgnoresModificationTime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "speed.log")
	at := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	if err := AppendSample(path, sampleStatus(at, peer("k", 1, 1))); err != nil {
		t.Fatalf("first: %v", err)
	}
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	if err := AppendSample(path, sampleStatus(at.Add(time.Minute), peer("k", 2, 2))); err != nil {
		t.Fatalf("second: %v", err)
	}
	if _, err := os.Stat(SpeedPrevPath(path)); !os.IsNotExist(err) {
		t.Errorf("rotated on modification time; the day must come from the file's own last line")
	}
}

// A torn append or a line from another version must not stop the next
// tick from being recorded.
func TestAppendSampleSurvivesUnreadableLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "speed.log")
	if err := os.WriteFile(path, []byte(SpeedSchema+"\nrubbish\n#speed v9 something\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	at := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	if err := AppendSample(path, sampleStatus(at, peer("k", 5, 6))); err != nil {
		t.Fatalf("append: %v", err)
	}
	lines := readLines(t, path)
	if got := lines[len(lines)-1]; !strings.HasSuffix(got, ":5:6:-") {
		t.Errorf("last line = %q, want the new tick", got)
	}
}

// Secrets never reach the log: the model has no field for them, and this
// pins that the rendered line carries nothing but public keys and counters.
func TestSpeedLineCarriesNoSecrets(t *testing.T) {
	st := sampleStatus(time.Now(), peer("PUBLICKEYAAAA", 1, 2))
	st.Interface.PublicKey = "INTERFACEPUBLIC"
	line := speedLine(st)
	for _, forbidden := range []string{"INTERFACEPUBLIC", "private", "preshared"} {
		if strings.Contains(line, forbidden) {
			t.Errorf("line %q carries %q", line, forbidden)
		}
	}
}

func TestSpeedPrevPath(t *testing.T) {
	if got := SpeedPrevPath("/status/speed.log"); got != "/status/speed.prev.log" {
		t.Errorf("SpeedPrevPath = %q, want /status/speed.prev.log", got)
	}
}

// The last line is found by reading the tail, not the whole file: this
// runs every tick, and re-reading a day of history to learn one
// timestamp would cost megabytes of reads every five seconds. The saving
// must not cost correctness on a file longer than the tail.
func TestRotationSeesTheLastLineOfALongFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "speed.log")
	day := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)

	var b strings.Builder
	b.WriteString(SpeedSchema + "\n")
	for i := range 4000 {
		b.WriteString(speedLine(sampleStatus(day.Add(time.Duration(i)*time.Second),
			peer("PADPADPADPADpad", uint64(i), uint64(i)))))
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if info, err := os.Stat(path); err != nil || info.Size() <= speedTailBytes {
		t.Fatalf("seed file is %v bytes, want more than the tail (%d)", info.Size(), speedTailBytes)
	}

	// Same day as the file's last line: nothing may rotate.
	if err := AppendSample(path, sampleStatus(day.Add(5*time.Hour), peer("k", 1, 1))); err != nil {
		t.Fatalf("same day: %v", err)
	}
	if _, err := os.Stat(SpeedPrevPath(path)); !os.IsNotExist(err) {
		t.Fatalf("rotated within the day; the tail read must have found the last line")
	}

	// Next day: it must.
	if err := AppendSample(path, sampleStatus(day.Add(30*time.Hour), peer("k", 2, 2))); err != nil {
		t.Fatalf("next day: %v", err)
	}
	if _, err := os.Stat(speedDatedPath(path, day)); err != nil {
		t.Fatalf("did not rotate on the next day: %v", err)
	}
	if lines := readLines(t, path); len(lines) != 2 || !strings.HasSuffix(lines[1], ":2:2:-") {
		t.Fatalf("new file = %q, want the schema and only the new day", lines)
	}
}

// A file is named for the day its own content covers, taken apart from
// the path it rotates out of.
func TestSpeedDatedPath(t *testing.T) {
	day := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	if got, want := speedDatedPath("/status/speed.log", day), "/status/speed-2026-09-10.log"; got != want {
		t.Errorf("speedDatedPath = %q, want %q", got, want)
	}
}

// Rotating aside when the size backstop fires twice on the same
// frozen-clock day must not silently discard whatever the day already
// collected.
func TestRotateFileToAppendsOnCollision(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "speed.log")
	dest := filepath.Join(dir, "speed-2026-09-10.log")
	if err := os.WriteFile(dest, []byte("first\n"), 0o600); err != nil {
		t.Fatalf("seed dest: %v", err)
	}
	if err := os.WriteFile(src, []byte("second\n"), 0o600); err != nil {
		t.Fatalf("seed src: %v", err)
	}

	if err := rotateFileTo(src, dest); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("source file still exists after rotation")
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if want := "first\nsecond\n"; string(got) != want {
		t.Errorf("dest = %q, want %q (append, not overwrite)", got, want)
	}
}

// speedPruneDecision is the pure budget arithmetic behind pruneSpeedHistory
// (amnezia-vpn-server-b0fl); testing it directly avoids writing hundreds of
// megabytes of fixture files to cross speedMaxTotalBytes.
func TestSpeedPruneDecisionByAge(t *testing.T) {
	now := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		daysAgo     int
		wantRemoved bool
	}{
		{"well inside retention", 1, false},
		{"just inside retention", speedRetentionDays - 1, false},
		{"exactly at the boundary is kept", speedRetentionDays, false},
		{"one day past the boundary", speedRetentionDays + 1, true},
		{"long expired", speedRetentionDays + 30, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			day := now.AddDate(0, 0, -tt.daysAgo)
			f := speedDatedFile{path: "f.log", day: day, size: 1024}
			keep, remove := speedPruneDecision([]speedDatedFile{f}, now)
			removed := len(remove) == 1
			if removed != tt.wantRemoved {
				t.Errorf("removed = %v, want %v (keep=%v remove=%v)", removed, tt.wantRemoved, keep, remove)
			}
			if removed && len(keep) != 0 {
				t.Errorf("keep = %v, want empty", keep)
			}
			if !removed && len(keep) != 1 {
				t.Errorf("keep = %v, want the one file", keep)
			}
		})
	}
}

// When the total exceeds speedMaxTotalBytes, the oldest surviving days go
// first - the same "protect the newest history" choice speedMaxBytes makes
// for one file, applied across the whole directory.
func TestSpeedPruneDecisionByTotalSize(t *testing.T) {
	now := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	// All three are comfortably inside the age window; only the combined
	// size should drive removal.
	half := int64(speedMaxTotalBytes / 2)
	files := []speedDatedFile{
		{path: "day1.log", day: now.AddDate(0, 0, -3), size: half},
		{path: "day2.log", day: now.AddDate(0, 0, -2), size: half},
		{path: "day3.log", day: now.AddDate(0, 0, -1), size: half},
	}

	keep, remove := speedPruneDecision(files, now)
	if len(remove) != 1 || remove[0].path != "day1.log" {
		t.Fatalf("remove = %v, want only the oldest day (day1.log)", remove)
	}
	if len(keep) != 2 || keep[0].path != "day2.log" || keep[1].path != "day3.log" {
		t.Fatalf("keep = %v, want the two newest days", keep)
	}
	var total int64
	for _, f := range keep {
		total += f.size
	}
	if total > speedMaxTotalBytes {
		t.Errorf("kept total = %d, still over the budget %d", total, speedMaxTotalBytes)
	}
}

// pruneSpeedHistory is speedPruneDecision wired to real files: this pins
// that the files it decides to remove actually disappear from disk, and
// the ones it keeps do not.
func TestPruneSpeedHistoryRemovesDecidedFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "speed.log")
	now := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)

	expired := speedDatedPath(path, now.AddDate(0, 0, -(speedRetentionDays+1)))
	recent := speedDatedPath(path, now.AddDate(0, 0, -1))
	writeLog(t, expired, sampleLine(now.AddDate(0, 0, -(speedRetentionDays+1)), 1, 1))
	writeLog(t, recent, sampleLine(now.AddDate(0, 0, -1), 1, 1))

	if err := pruneSpeedHistory(path, now); err != nil {
		t.Fatalf("prune: %v", err)
	}
	if _, err := os.Stat(expired); !os.IsNotExist(err) {
		t.Errorf("expired file survived pruning")
	}
	if _, err := os.Stat(recent); err != nil {
		t.Errorf("recent file was removed: %v", err)
	}
}

// A "speed.prev.log" a pre-b0fl build left behind is retired once its own
// last line ages out of the retention window - the same rule a dated file
// this old would be held to.
func TestPruneLegacySpeedPrevAgesOut(t *testing.T) {
	tests := []struct {
		name        string
		lastSample  time.Time
		wantDeleted bool
	}{
		{"recent", time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC), false},
		{"expired", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), true},
	}
	now := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "speed.log")
			writeLog(t, SpeedPrevPath(path), speedLine(sampleStatus(tt.lastSample, peer("k", 1, 1))))

			if err := pruneLegacySpeedPrev(path, now); err != nil {
				t.Fatalf("prune: %v", err)
			}
			_, err := os.Stat(SpeedPrevPath(path))
			deleted := os.IsNotExist(err)
			if deleted != tt.wantDeleted {
				t.Errorf("deleted = %v, want %v", deleted, tt.wantDeleted)
			}
		})
	}
}

// Content that cannot be dated must not be deleted on a guess - the same
// caution rotation takes about mtime.
func TestPruneLegacySpeedPrevLeavesUnreadableFileAlone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "speed.log")
	if err := os.WriteFile(SpeedPrevPath(path), []byte(SpeedSchema+"\nrubbish\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := pruneLegacySpeedPrev(path, time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("prune: %v", err)
	}
	if _, err := os.Stat(SpeedPrevPath(path)); err != nil {
		t.Errorf("unreadable legacy file was deleted: %v", err)
	}
}

// The handshake age is what tells a buffering pause from a broken tunnel,
// so it has to reach the file — traffic counters alone cannot
// (amnezia-vpn-server-3wbe).
func TestSpeedLineCarriesHandshakeAge(t *testing.T) {
	at := time.Date(2026, 9, 11, 8, 9, 0, 0, time.UTC)
	shook := at.Add(-42 * time.Second)
	ahead := at.Add(4 * time.Second)

	for _, tc := range []struct {
		name      string
		handshake *time.Time
		want      string
	}{
		{"возраст в секундах", &shook, "42"},
		{"рукопожатия не было", nil, SpeedNoHandshake},
		{"замер в тот же миг", &at, "0"},
		{"часы ушли вперёд — не отрицательное", &ahead, "0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := peer("AAAAAAAAAAAAxxxx", 100, 200)
			p.LastHandshakeUTC = tc.handshake
			line := speedLine(sampleStatus(at, p))

			want := fmt.Sprintf("%d AAAAAAAAAAAA:100:200:%s\n", at.Unix(), tc.want)
			if line != want {
				t.Errorf("line = %q, want %q", line, want)
			}
		})
	}
}

// «Рукопожатия никогда не было» и «рукопожатие сию секунду» — разные
// вещи, и ноль не может означать оба (amnezia-vpn-server-3wbe).
func TestSpeedNoHandshakeIsNotZero(t *testing.T) {
	if SpeedNoHandshake == "0" {
		t.Fatal("признак «рукопожатия не было» совпал с нулевым возрастом")
	}
}
