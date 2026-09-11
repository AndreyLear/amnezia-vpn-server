package status

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Speed history (amnezia-vpn-server-aa9u, docs/adr/0002).
//
// status.json is rewritten every tick and keeps no past, so nothing in
// the product could say what a client's speed was ten minutes ago. This
// log is that memory: one line per tick, appended next to status.json by
// the same producer.
//
// What a line holds is the *cumulative* byte counters, not a speed. That
// is the whole trick: the producer then needs no memory of the previous
// tick, the reader derives a rate by differencing two adjacent lines,
// and a counter reset (the interface was recreated) shows up as a
// negative difference the reader must render as a gap rather than as
// zero — "we did not look" is not "the client got nothing".
//
// The file is appended to, never rewritten: rewriting 86 400 records
// 17 280 times a day would cost tens of gigabytes of disk writes a day
// for a few megabytes of data. A torn append therefore costs the last
// line, which is not worth protecting.
//
// Rotation used to move the whole previous day into a single
// "speed.prev.log", which put a hard ceiling of ~48 hours on how far back
// anyone could look. The owner chases failures that show up every few
// days, not every day, so that ceiling was below the thing this log
// exists to catch. Rotation now keeps one dated file per day
// (speedDatedPath) and prunes them by age and by total size
// (pruneSpeedHistory), which is what turns "at least 24 hours" into "at
// least speedRetentionDays days" (amnezia-vpn-server-b0fl). A
// "speed.prev.log" left behind by an older build is still read - see
// pruneLegacySpeedPrev - but rotation never writes to it again.

// SpeedSchema is the first line of every history file. A reader must
// skip lines it does not understand: after an update the previous
// version's file still lies next to the new one.
//
// v2 added a fourth field per peer, the handshake age
// (amnezia-vpn-server-3wbe). v1 lines stay readable — see parseSpeedLine —
// because rotation happens on the UTC day while an update happens whenever
// the operator presses the button, so one file routinely holds both.
const SpeedSchema = "#speed v2"

// SpeedNoHandshake is what the handshake field holds for a peer that has
// never completed one. It is deliberately not "0": zero is a perfectly
// good age meaning "a handshake just now", and a peer that has never been
// seen is not the same thing as one seen this second
// (amnezia-vpn-server-3wbe).
const SpeedNoHandshake = "-"

// SpeedKeyLen is how much of a peer's public key identifies it in the
// log. The full 44-character key repeated on every line would be most of
// the file; 12 base64 characters are 72 bits, which cannot collide among
// the peers of one server. The key is public — status.json already
// carries it in full.
const SpeedKeyLen = 12

// speedMaxBytes caps a single file. Rotation is normally driven by the
// UTC day, and this is only the backstop for a clock that never reaches
// the next day: without it a frozen clock would grow the file until the
// disk filled.
const speedMaxBytes = 64 << 20

// speedRetentionDays is how long a rotated-out day is kept before
// pruneSpeedHistory deletes it by age (amnezia-vpn-server-b0fl). The
// single "yesterday" file this replaced covered under 48 hours; the
// failures the owner is chasing happen every few days, not every day, so
// a look-back window that short is useless for exactly the case this log
// exists to catch. 14 days is a full two weeks of margin past "once every
// few days", and it is cheap: one active client's day of samples is
// roughly 1.3 MB, so one client's whole history stays under 20 MB.
const speedRetentionDays = 14

// speedMaxTotalBytes caps the combined size of every dated history file -
// the same backstop role speedMaxBytes plays for one file, but for the
// whole directory, in case something writes far more than the nominal
// rate assumes (many peers, a chattier client, a bug). A 30-client server
// writes roughly 8 MB/day, so speedRetentionDays of nominal traffic is
// about 112 MB; 300 MiB leaves better than 2.5x that margin before the
// oldest day starts being deleted early, while still being a small slice
// of a typical VPS disk.
const speedMaxTotalBytes = 300 << 20

// SpeedKey shortens a peer's public key to its log identity.
func SpeedKey(publicKey string) string {
	if len(publicKey) <= SpeedKeyLen {
		return publicKey
	}
	return publicKey[:SpeedKeyLen]
}

// SpeedPrevPath names the legacy single rotated-out file a pre-b0fl build
// wrote. Rotation no longer creates or writes to it - speedDatedPath is
// where a day goes now - but the reader still falls back to it
// (readSpeedSamples) so an update does not erase whatever that build had
// already collected, and pruneLegacySpeedPrev retires it once its own
// content ages out of the retention window.
func SpeedPrevPath(path string) string {
	dir, base := filepath.Dir(path), filepath.Base(path)
	ext := filepath.Ext(base)
	return filepath.Join(dir, strings.TrimSuffix(base, ext)+".prev"+ext)
}

// speedDatedPath names the file one UTC day rotates into, e.g.
// "speed-2026-09-10.log" beside "speed.log" (amnezia-vpn-server-b0fl). The
// date is a parameter rather than "today" because rotation names a file
// for the day whose data it holds - read from that day's own last line,
// per rotateSpeed - not for whenever rotation happened to run.
func speedDatedPath(path string, day time.Time) string {
	dir, base := filepath.Dir(path), filepath.Base(path)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	return filepath.Join(dir, fmt.Sprintf("%s-%s%s", stem, day.UTC().Format("2006-01-02"), ext))
}

// speedDatedFile is one entry found by listSpeedDatedFiles.
type speedDatedFile struct {
	path string
	day  time.Time
	size int64
}

// listSpeedDatedFiles finds every file speedDatedPath could have produced
// for this log and reads its day back out of the name. The name, not the
// content, is the source of truth here on purpose: both pruning and
// windowed reading need to decide whether a file is even worth opening,
// and opening every candidate just to decide that would defeat the point.
func listSpeedDatedFiles(path string) ([]speedDatedFile, error) {
	dir, base := filepath.Dir(path), filepath.Base(path)
	ext := filepath.Ext(base)
	prefix := strings.TrimSuffix(base, ext) + "-"

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("status: list speed log directory: %w", err)
	}
	var out []speedDatedFile
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ext) {
			continue
		}
		datePart := strings.TrimSuffix(strings.TrimPrefix(name, prefix), ext)
		day, err := time.Parse("2006-01-02", datePart)
		if err != nil {
			// Not one of ours - e.g. another log sharing a stem prefix.
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, speedDatedFile{path: filepath.Join(dir, name), day: day, size: info.Size()})
	}
	return out, nil
}

// rotateFileTo moves src to dest, the normal case, but appends instead of
// overwriting when dest already exists. That only happens when the size
// backstop (speedMaxBytes) fires more than once for the same UTC day - a
// frozen clock holding a day open across several rotations - and
// os.Rename onto an existing file would silently discard whatever that
// day had already collected.
func rotateFileTo(src, dest string) error {
	if _, err := os.Stat(dest); err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("status: stat rotated speed log: %w", err)
		}
		if err := os.Rename(src, dest); err != nil {
			return fmt.Errorf("status: rotate speed log: %w", err)
		}
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("status: open speed log: %w", err)
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("status: open rotated speed log: %w", err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return fmt.Errorf("status: append rotated speed log: %w", err)
	}
	// Closed explicitly, not deferred: a write can fail on flush, and that
	// error must reach the caller instead of being silently dropped by a
	// deferred Close whose return value nobody looked at.
	if err := out.Close(); err != nil {
		return fmt.Errorf("status: close rotated speed log: %w", err)
	}
	if err := os.Remove(src); err != nil {
		return fmt.Errorf("status: remove rotated speed log: %w", err)
	}
	return nil
}

// speedPruneDecision splits dated files into what survives and what gets
// removed, first by age and then, only among what survived that cut, by
// total size (oldest survivor first). It does no I/O on purpose: keeping
// the budget arithmetic separate from file removal makes it exercisable
// without writing hundreds of megabytes of fixture files just to cross
// speedMaxTotalBytes (amnezia-vpn-server-b0fl).
//
// Two independent limits apply because they guard against different
// failures: speedRetentionDays bounds how far back a report can be found
// even when disk is plentiful, and speedMaxTotalBytes bounds the
// directory even when something writes far more than the nominal rate
// those days assume.
func speedPruneDecision(files []speedDatedFile, now time.Time) (keep, remove []speedDatedFile) {
	sorted := make([]speedDatedFile, len(files))
	copy(sorted, files)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].day.Before(sorted[j].day) })

	cutoff := now.UTC().Truncate(24*time.Hour).AddDate(0, 0, -speedRetentionDays)
	var kept []speedDatedFile
	for _, f := range sorted {
		if f.day.Before(cutoff) {
			remove = append(remove, f)
			continue
		}
		kept = append(kept, f)
	}

	var total int64
	for _, f := range kept {
		total += f.size
	}
	// kept is oldest-first, so trimming from the front drops the oldest
	// survivors first when the size backstop fires.
	i := 0
	for total > speedMaxTotalBytes && i < len(kept) {
		remove = append(remove, kept[i])
		total -= kept[i].size
		i++
	}
	return kept[i:], remove
}

// pruneSpeedHistory drops rotated-out days that speedPruneDecision says are
// no longer wanted. It runs once per rotation - once a day in the normal
// case - so unlike AppendSample, which runs every tick, it never needs to
// be cheap.
func pruneSpeedHistory(path string, now time.Time) error {
	files, err := listSpeedDatedFiles(path)
	if err != nil {
		return err
	}
	_, remove := speedPruneDecision(files, now)
	for _, f := range remove {
		if err := os.Remove(f.path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("status: prune speed log: %w", err)
		}
	}
	return pruneLegacySpeedPrev(path, now)
}

// pruneLegacySpeedPrev removes the single-file "speed.prev.log" a pre-b0fl
// build left behind, once it has aged out. Rotation never writes to it
// again, so nothing keeps its content current; readSpeedSamples still
// reads it as a last-resort source until its own last line - read the
// same way rotation reads any file's day - falls out of the retention
// window, at which point it is exactly as stale as a dated file this old
// would be, and is deleted the same way.
func pruneLegacySpeedPrev(path string, now time.Time) error {
	prev := SpeedPrevPath(path)
	info, err := os.Stat(prev)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("status: stat legacy speed log: %w", err)
	}
	last, ok, err := lastSpeedTime(prev, info.Size())
	if err != nil {
		return err
	}
	if !ok {
		// The content cannot be dated. Leave it: deleting on a guess would
		// risk throwing away history nobody can prove is stale, the same
		// caution rotation takes with mtime.
		return nil
	}
	cutoff := now.UTC().Truncate(24*time.Hour).AddDate(0, 0, -speedRetentionDays)
	if last.UTC().Before(cutoff) {
		if err := os.Remove(prev); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("status: remove legacy speed log: %w", err)
		}
	}
	return nil
}

// AppendSample records one tick.
//
// A tick with no peers writes nothing at all, and that is deliberate:
// the interface being down is exactly the case a reader must show as a
// gap, and a line claiming zero bytes would be a diagnosis we did not
// make.
func AppendSample(path string, st *Status) error {
	if path == "" || st == nil || len(st.Peers) == 0 {
		return nil
	}
	if err := rotateSpeed(path, st.GeneratedAt); err != nil {
		return err
	}
	line := speedLine(st)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("status: open speed log: %w", err)
	}
	defer f.Close()
	if info, err := f.Stat(); err == nil && info.Size() == 0 {
		line = SpeedSchema + "\n" + line
	}
	if _, err := f.WriteString(line); err != nil {
		return fmt.Errorf("status: append speed log: %w", err)
	}
	return nil
}

// speedLine renders "<unix> <key>:<rx>:<tx>:<handshake age> …". Peers keep
// the order Parse gave them (sorted by public key), so a line is
// deterministic for a given dump.
func speedLine(st *Status) string {
	at := st.GeneratedAt.UTC()
	var b strings.Builder
	b.WriteString(strconv.FormatInt(at.Unix(), 10))
	for _, p := range st.Peers {
		b.WriteByte(' ')
		b.WriteString(SpeedKey(p.PublicKey))
		b.WriteByte(':')
		b.WriteString(strconv.FormatUint(p.RxBytes, 10))
		b.WriteByte(':')
		b.WriteString(strconv.FormatUint(p.TxBytes, 10))
		b.WriteByte(':')
		b.WriteString(speedHandshakeField(p.LastHandshakeUTC, at))
	}
	b.WriteByte('\n')
	return b.String()
}

// speedHandshakeField renders how old the peer's last handshake was at the
// moment of this sample (amnezia-vpn-server-3wbe).
//
// An age relative to the line's own timestamp, not the absolute handshake
// time, for two reasons. It is what a reader actually wants, so nobody has
// to subtract anything later; and it is three or four characters instead of
// ten, which matters when the field repeats for every peer of every
// five-second tick — the absolute form would have added most of a megabyte
// a day to a file that is currently 1.3 MB.
//
// A negative age means the clock moved backwards between the handshake and
// the sample. There is no honest reading of "the handshake happens in four
// seconds", so it is clamped to zero rather than written out and left for
// the reader to puzzle over.
func speedHandshakeField(handshake *time.Time, at time.Time) string {
	if handshake == nil {
		return SpeedNoHandshake
	}
	age := int64(at.Sub(handshake.UTC()) / time.Second)
	if age < 0 {
		age = 0
	}
	return strconv.FormatInt(age, 10)
}

// rotateSpeed moves the current file aside when the sample belongs to a
// later UTC day than the file's last line, or when the backstop size is
// reached. The day is read from the file itself rather than from its
// modification time: a restored copy or a touched file must not be able
// to throw away a day of history. That same day also names the file it
// rotates into (speedDatedPath), so pruning later can tell how old a file
// is without opening it.
func rotateSpeed(path string, at time.Time) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("status: stat speed log: %w", err)
	}
	// Read unconditionally, not only for the day-change check: naming the
	// rotated file needs it too, including on a size-backstop rotation.
	last, ok, err := lastSpeedTime(path, info.Size())
	if err != nil {
		return err
	}
	rotate := info.Size() >= speedMaxBytes
	if !rotate {
		rotate = ok && !sameUTCDay(last, at)
	}
	if !rotate {
		return nil
	}
	// The incoming sample's own day is the fallback when the file's last
	// line could not be dated (only reachable via the size backstop, since
	// the day-change check above already requires ok): some date is needed
	// to rotate into, and "now" is the closest available guess.
	day := at
	if ok {
		day = last
	}
	if err := rotateFileTo(path, speedDatedPath(path, day)); err != nil {
		return err
	}
	return pruneSpeedHistory(path, at)
}

func sameUTCDay(a, b time.Time) bool {
	au, bu := a.UTC(), b.UTC()
	return au.Year() == bu.Year() && au.YearDay() == bu.YearDay()
}

// speedTailBytes is how much of the end of the log is read to find the
// last line. Only the tail is read because this runs on every tick:
// scanning the whole file would mean re-reading a day of history every
// five seconds, megabytes at a time, to learn one timestamp.
const speedTailBytes = 64 << 10

// lastSpeedTime returns the timestamp of the last usable line. Lines that
// cannot be parsed — a torn append, or a format from another version —
// are skipped rather than treated as an error: the log is an
// observation, and one unreadable line must not stop the next one from
// being written.
//
// A false second return means "the file says nothing about when it was
// written". Rotation then does not happen, which is the safe way round:
// keeping a day too long costs disk, throwing one away costs the history
// somebody is about to ask for.
func lastSpeedTime(path string, size int64) (time.Time, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("status: read speed log: %w", err)
	}
	defer f.Close()
	if size > speedTailBytes {
		if _, err := f.Seek(size-speedTailBytes, io.SeekStart); err != nil {
			return time.Time{}, false, fmt.Errorf("status: seek speed log: %w", err)
		}
	}
	tail, err := io.ReadAll(f)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("status: read speed log: %w", err)
	}
	// The first line of the tail is very likely cut in half by the seek;
	// scanning backwards, it is simply the last candidate and gets used
	// only if every later line was unreadable too — and then it fails to
	// parse just like any other rubbish.
	lines := strings.Split(string(tail), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		field, _, found := strings.Cut(strings.TrimSpace(lines[i]), " ")
		if !found {
			continue
		}
		unix, err := strconv.ParseInt(field, 10, 64)
		if err != nil {
			continue
		}
		return time.Unix(unix, 0).UTC(), true, nil
	}
	return time.Time{}, false, nil
}
