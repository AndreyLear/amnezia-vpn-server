package status

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
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

// SpeedSchema is the first line of every history file. A reader must
// skip lines it does not understand: after an update the previous
// version's file still lies next to the new one.
const SpeedSchema = "#speed v1"

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

// SpeedKey shortens a peer's public key to its log identity.
func SpeedKey(publicKey string) string {
	if len(publicKey) <= SpeedKeyLen {
		return publicKey
	}
	return publicKey[:SpeedKeyLen]
}

// SpeedPrevPath names the rotated-out file that sits beside the current
// one. Two files are what makes "at least 24 hours" true at any moment:
// right after midnight the current one holds a minute and the previous
// one holds the whole day before it.
func SpeedPrevPath(path string) string {
	dir, base := filepath.Dir(path), filepath.Base(path)
	ext := filepath.Ext(base)
	return filepath.Join(dir, strings.TrimSuffix(base, ext)+".prev"+ext)
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

// speedLine renders "<unix> <key>:<rx>:<tx> …". Peers keep the order
// Parse gave them (sorted by public key), so a line is deterministic for
// a given dump.
func speedLine(st *Status) string {
	var b strings.Builder
	b.WriteString(strconv.FormatInt(st.GeneratedAt.UTC().Unix(), 10))
	for _, p := range st.Peers {
		b.WriteByte(' ')
		b.WriteString(SpeedKey(p.PublicKey))
		b.WriteByte(':')
		b.WriteString(strconv.FormatUint(p.RxBytes, 10))
		b.WriteByte(':')
		b.WriteString(strconv.FormatUint(p.TxBytes, 10))
	}
	b.WriteByte('\n')
	return b.String()
}

// rotateSpeed moves the current file aside when the sample belongs to a
// later UTC day than the file's last line, or when the backstop size is
// reached. The day is read from the file itself rather than from its
// modification time: a restored copy or a touched file must not be able
// to throw away a day of history.
func rotateSpeed(path string, at time.Time) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("status: stat speed log: %w", err)
	}
	rotate := info.Size() >= speedMaxBytes
	if !rotate {
		last, ok, err := lastSpeedTime(path, info.Size())
		if err != nil {
			return err
		}
		rotate = ok && !sameUTCDay(last, at)
	}
	if !rotate {
		return nil
	}
	if err := os.Rename(path, SpeedPrevPath(path)); err != nil {
		return fmt.Errorf("status: rotate speed log: %w", err)
	}
	return nil
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
