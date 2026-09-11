package status

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Reading the speed history (amnezia-vpn-server-8lnv).
//
// The log holds cumulative counters, so a rate is the difference between
// two adjacent samples. Two things must survive that arithmetic:
//
//   - a counter that went backwards means the interface was recreated,
//     not that the client sent negative traffic;
//   - a long silence between samples means nobody was looking, not that
//     the client was idle. Differencing across it would draw a low, flat
//     line over a period we know nothing about — a lie in the shape of
//     data.
//
// Both become gaps, and a gap is rendered as a break rather than as
// zero.

// SpeedMaxGap is how far apart two samples may be and still yield a
// rate. The producer writes every five seconds; a few missed ticks are
// ordinary scheduling, half a minute is an outage.
const SpeedMaxGap = 30 * time.Second

// SpeedAliveMaxAge is how old the last handshake may be before a sample
// counts as "the client was not reachable" (amnezia-vpn-server-3wbe).
//
// AmneziaWG renews a handshake about every two minutes while a peer has
// traffic to send. Three minutes is that interval plus room for a renewal
// that runs late, so a peer still talking is never called offline.
//
// This is the COARSE signal, and only the fallback: three minutes is the
// best it can do, because over twenty seconds a handshake ages by twenty
// seconds whether the tunnel works or not. SpeedOfflineSilence below is
// the sharper one and is preferred wherever it can be measured
// (amnezia-vpn-server-tyic).
const SpeedAliveMaxAge = 3 * time.Minute

// SpeedOfflineSilence is how long the server may hear NOTHING from a peer
// before the peer counts as unreachable (amnezia-vpn-server-tyic).
//
// A client that is connected but idle is not silent: the config this panel
// generates carries PersistentKeepalive = 25 (internal/awgconf/clientconf.go),
// so an idle client sends a 32-byte keepalive every twenty-five seconds and
// the server's rx counter keeps ticking. A broken tunnel delivers nothing at
// all. So "the rx counter has not moved" separates the two where traffic
// volume cannot: a player refilling its buffer downloads nothing but keeps
// answering, and a dead tunnel does neither.
//
// Sixty seconds is measured, not guessed. Over two days of one real client —
// 26 908 intervals between successive arrivals of bytes from it — the
// longest silence was 46 seconds (distribution: 5 s ×22113, 6 s ×1413,
// 10 s ×1150, 15 s ×1313, 20 s ×253, 25 s ×76, then a thin tail to 46 s).
// Sixty leaves room above that worst case without waiting out the three
// minutes the handshake would need.
//
// The assumption it rests on: keepalive is in the client's config. This
// panel writes it, but a hand-edited config without it would make an idle
// client look unreachable. That is why the handshake age stays as a second
// opinion rather than being deleted.
const SpeedOfflineSilence = 60 * time.Second

// SpeedColumn is one column of the chart: the extremes of what happened
// inside it. Extremes, not an average, because averaging is what hides
// the dips this whole history exists to show — a one-second collapse
// among twenty-five samples averages away to nothing.
//
// Down is traffic towards the client (the server's tx counter), Up is
// traffic from it (rx). The names follow the client's point of view
// because that is whose card the chart appears on; getting this backwards
// would swap the main line with the one that sits at the floor.
type SpeedColumn struct {
	AtUTC   time.Time
	HasData bool
	DownMin uint64
	DownMax uint64
	UpMin   uint64
	UpMax   uint64
	// HasLiveness and Online answer a different question from HasData, and
	// confusing the two would be the whole bug back again
	// (amnezia-vpn-server-3wbe). HasData means "we looked"; Online means
	// "the client was actually reachable while we looked". A video player
	// refilling its buffer produces zero bytes for twenty seconds and is
	// perfectly online; a broken tunnel produces the same zero bytes and is
	// not. Traffic alone cannot tell them apart — see SpeedAliveMaxAge.
	//
	// HasLiveness is false only where neither signal is available: no rx
	// movement seen yet (the very start of a window) and no handshake age
	// on the record. Such a column must be drawn as neither online nor
	// offline — nothing is known about it either way.
	HasLiveness bool
	Online      bool
}

// SpeedSeries is a client's history folded to a fixed number of columns.
type SpeedSeries struct {
	FromUTC time.Time
	ToUTC   time.Time
	Columns []SpeedColumn
}

// speedSample is one parsed line, for one peer.
type speedSample struct {
	at time.Time
	rx uint64
	tx uint64
	// handshakeAge is how old the peer's handshake was at that moment, and
	// hasHandshake says whether the line carried the field at all — a v1
	// line does not, and that absence must not read as "age zero"
	// (amnezia-vpn-server-3wbe).
	handshakeAge time.Duration
	hasHandshake bool
}

// ReadSpeedSeries folds the history of one peer between from and to into
// columns equal-width columns.
//
// A missing file is not an error: a server that has not written history
// yet is the normal state right after an update, and the caller should
// show "nothing yet" rather than a failure.
func ReadSpeedSeries(path, key string, from, to time.Time, columns int) (*SpeedSeries, error) {
	if columns < 1 {
		columns = 1
	}
	if !to.After(from) {
		return nil, fmt.Errorf("status: speed window ends before it starts")
	}
	samples, err := readSpeedSamples(path, key, from, to)
	if err != nil {
		return nil, err
	}
	return foldSpeed(samples, from, to, columns), nil
}

// readSpeedSamples gathers this peer's samples from the current file and,
// when the window reaches further back than that file goes, walks the
// rotated-out days (newest first, readSpeedDatedFiles) and finally, as a
// last resort, the legacy single "speed.prev.log" a pre-b0fl build may
// have left behind (amnezia-vpn-server-b0fl). Each tier is skipped once
// the one before it already reaches back to the window: a query for the
// last ten minutes must not walk fourteen days of files to answer it.
func readSpeedSamples(path, key string, from, to time.Time) ([]speedSample, error) {
	// One sample before the window is deliberately kept: the first rate
	// inside the window is the difference against it, and without it the
	// chart would begin with an unexplained gap.
	reach := from.Add(-SpeedMaxGap)
	all, err := readSpeedFile(path, key, reach, to)
	if err != nil {
		return nil, err
	}
	if !speedReaches(all, reach) {
		dated, err := readSpeedDatedFiles(path, key, reach, to)
		if err != nil {
			return nil, err
		}
		all = append(dated, all...)
	}
	if !speedReaches(all, reach) {
		legacy, err := readSpeedFile(SpeedPrevPath(path), key, reach, to)
		if err != nil {
			return nil, err
		}
		all = append(legacy, all...)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].at.Before(all[j].at) })
	return all, nil
}

// speedReaches reports whether samples already cover back to reach, so a
// caller knows whether an older, more expensive tier is worth opening at
// all.
func speedReaches(samples []speedSample, reach time.Time) bool {
	return len(samples) > 0 && !samples[0].at.After(reach)
}

// readSpeedDatedFiles walks the rotated-out days newest first, stopping as
// soon as the window is covered or the days run out. A day whose name
// already places it outside [reach, to] is never opened
// (amnezia-vpn-server-b0fl): with speedRetentionDays of files on disk, a
// short window - including one that lies entirely in the past, e.g. "what
// happened the day before yesterday" - has to be able to ignore almost all
// of them by name alone. The two bounds need different loop actions: a day
// newer than the window is skipped with continue (older days further down
// the newest-first order can still be inside it), while a day older than
// the window ends the search outright.
func readSpeedDatedFiles(path, key string, reach, to time.Time) ([]speedSample, error) {
	files, err := listSpeedDatedFiles(path)
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].day.After(files[j].day) })
	reachDay := reach.UTC().Truncate(24 * time.Hour)
	toDay := to.UTC().Truncate(24 * time.Hour)

	var out []speedSample
	for _, f := range files {
		if f.day.After(toDay) {
			continue
		}
		if f.day.Before(reachDay) {
			break
		}
		samples, err := readSpeedFile(f.path, key, reach, to)
		if err != nil {
			return nil, err
		}
		out = append(samples, out...)
		if speedReaches(out, reach) {
			break
		}
	}
	return out, nil
}

// readSpeedFile parses one file. Only its tail is read: this endpoint is
// polled every few seconds, and re-reading a day of history to draw one
// hour would be most of the work for none of the answer.
func readSpeedFile(path, key string, from, to time.Time) ([]speedSample, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("status: open speed log: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("status: stat speed log: %w", err)
	}
	buf, err := speedTail(f, info.Size(), from)
	if err != nil {
		return nil, err
	}
	var out []speedSample
	for line := range strings.SplitSeq(string(buf), "\n") {
		s, ok := parseSpeedLine(line, key)
		if !ok || s.at.Before(from) || s.at.After(to) {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}

// speedTail reads backwards in growing chunks until the first whole line
// it holds is older than from, or until the file is exhausted.
func speedTail(f *os.File, size int64, from time.Time) ([]byte, error) {
	chunk := int64(256 << 10)
	for {
		offset := size - chunk
		if offset < 0 {
			offset = 0
		}
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return nil, fmt.Errorf("status: seek speed log: %w", err)
		}
		buf, err := io.ReadAll(f)
		if err != nil {
			return nil, fmt.Errorf("status: read speed log: %w", err)
		}
		if offset > 0 {
			// The chunk almost certainly starts mid-line; that half-line
			// would parse as rubbish or, worse, as a plausible number.
			if cut := strings.IndexByte(string(buf), '\n'); cut >= 0 {
				buf = buf[cut+1:]
			} else {
				buf = nil
			}
		}
		if offset == 0 || speedReachesBack(buf, from) {
			return buf, nil
		}
		chunk *= 4
	}
}

// speedReachesBack reports whether the buffer's first usable timestamp is
// old enough that nothing before it is wanted.
func speedReachesBack(buf []byte, from time.Time) bool {
	for line := range strings.SplitSeq(string(buf), "\n") {
		field, _, found := strings.Cut(strings.TrimSpace(line), " ")
		if !found {
			continue
		}
		unix, err := strconv.ParseInt(field, 10, 64)
		if err != nil {
			continue
		}
		return !time.Unix(unix, 0).UTC().After(from)
	}
	return false
}

// parseSpeedLine picks one peer out of a line. Lines it cannot read — a
// torn append, a format from another version — are skipped: the log is an
// observation, and one bad line must not cost the rest of the day.
func parseSpeedLine(line, key string) (speedSample, bool) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return speedSample{}, false
	}
	unix, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return speedSample{}, false
	}
	for _, f := range fields[1:] {
		id, counters, ok := strings.Cut(f, ":")
		if !ok || id != key {
			continue
		}
		// Two parts is a v1 line, three a v2 one. Both are expected side by
		// side — rotation happens on the UTC day, an update whenever the
		// operator presses the button, so one file routinely holds a v1
		// morning and a v2 afternoon (amnezia-vpn-server-3wbe).
		parts := strings.Split(counters, ":")
		if len(parts) < 2 || len(parts) > 3 {
			return speedSample{}, false
		}
		rx, err := strconv.ParseUint(parts[0], 10, 64)
		if err != nil {
			return speedSample{}, false
		}
		tx, err := strconv.ParseUint(parts[1], 10, 64)
		if err != nil {
			return speedSample{}, false
		}
		s := speedSample{at: time.Unix(unix, 0).UTC(), rx: rx, tx: tx}
		if len(parts) == 3 && parts[2] != SpeedNoHandshake {
			age, err := strconv.ParseInt(parts[2], 10, 64)
			if err != nil {
				return speedSample{}, false
			}
			s.handshakeAge = time.Duration(age) * time.Second
			s.hasHandshake = true
		}
		return s, true
	}
	return speedSample{}, false
}

// foldSpeed differences adjacent samples into rates and drops each rate
// into the column its interval ended in.
func foldSpeed(samples []speedSample, from, to time.Time, columns int) *SpeedSeries {
	out := &SpeedSeries{FromUTC: from, ToUTC: to, Columns: make([]SpeedColumn, columns)}
	span := to.Sub(from)
	width := span / time.Duration(columns)
	for i := range out.Columns {
		out.Columns[i].AtUTC = from.Add(time.Duration(i) * width)
	}
	// lastRxMove is when the client was last heard from. Updated before the
	// skips below, because a sample being unusable as a RATE (too far apart,
	// counters reset) does not make it unusable as proof the client spoke
	// (amnezia-vpn-server-tyic).
	var lastRxMove time.Time
	// Whether the rx signal is usable at all in this window decides which
	// signal is in charge, and it has to be known BEFORE the first interval
	// is judged — hence a separate scan.
	//
	// The two signals must not be mixed per-interval. The handshake ages
	// even on a perfectly healthy idle client, because AmneziaWG renews a
	// handshake only when there is data to send and a keepalive is not data:
	// fifteen idle minutes leave a 900-second-old handshake with keepalives
	// arriving the whole time. Letting the handshake veto that would call
	// the commonest state — connected, idle — an outage.
	anyRxMove := false
	for i := 1; i < len(samples); i++ {
		if samples[i].rx > samples[i-1].rx {
			anyRxMove = true
			break
		}
	}
	for i := 1; i < len(samples); i++ {
		a, b := samples[i-1], samples[i]
		if b.rx > a.rx {
			// The bytes arrived somewhere in (a, b]; b is the conservative
			// reading — it never claims the client spoke earlier than it did.
			lastRxMove = b.at
		}
		dt := b.at.Sub(a.at)
		if dt <= 0 || dt > SpeedMaxGap {
			continue
		}
		if b.rx < a.rx || b.tx < a.tx {
			// The counters were reset with the interface. Nothing is
			// known about this interval, and a huge or zero rate would
			// both be inventions.
			continue
		}
		if b.at.Before(from) || b.at.After(to) {
			continue
		}
		down := bitsPerSecond(b.tx-a.tx, dt)
		up := bitsPerSecond(b.rx-a.rx, dt)
		// Скорость принадлежит ОТРЕЗКУ [a, b], а не мгновению, поэтому
		// красится каждый столбец, которого этот отрезок касается.
		//
		// Пока красился только столбец, где отрезок кончился, дрожание такта
		// рисовало ложные дырки: производитель пишет замер раз в пять секунд,
		// но иногда раз в шесть, а столбец десятиминутного окна — ровно пять
		// секунд. Шестисекундный отрезок перепрыгивал через столбец, и тот
		// оставался пустым. График показывал «замеров нет» посреди сплошной
		// загрузки — враньё в форме данных, ровно то, от чего разрывы и
		// заведены (amnezia-vpn-server-bwp3).
		//
		// Настоящие разрывы это не трогает: отрезок длиннее SpeedMaxGap сюда
		// не доходит вовсе, он отсеян выше.
		// Liveness is taken from the interval's END: what was true once this
		// interval had happened (amnezia-vpn-server-3wbe).
		//
		// Silence in the rx counter is the sharper signal and wins whenever
		// it can be measured; the handshake age is the fallback for the very
		// start of a window, where no rx movement has been seen yet, and for
		// records from before the age was written at all
		// (amnezia-vpn-server-tyic).
		// known says whether this interval has any opinion to offer at all.
		// Before the first rx movement of a window there is none: the client
		// may have been quiet for one second or one hour, and the handshake
		// cannot break the tie (see anyRxMove above).
		var alive, known bool
		switch {
		case anyRxMove && !lastRxMove.IsZero():
			alive, known = b.at.Sub(lastRxMove) <= SpeedOfflineSilence, true
		case !anyRxMove && b.hasHandshake:
			// Nothing was ever heard in this window, so the handshake is all
			// there is — and here it can only accuse, never acquit wrongly:
			// a client silent for the whole window really is unreachable if
			// its handshake is stale too.
			alive, known = b.handshakeAge <= SpeedAliveMaxAge, true
		}
		first := columnAt(a.at, from, width, columns)
		last := columnAt(b.at, from, width, columns)
		for idx := first; idx <= last; idx++ {
			col := &out.Columns[idx]
			// Either signal is enough to have an opinion. Notably the rx
			// one works on v1 records too, which carry no handshake — so
			// history written before the age existed is not blind after all
			// (amnezia-vpn-server-tyic).
			if known {
				if !col.HasLiveness {
					col.HasLiveness = true
					col.Online = alive
				} else {
					// A column is called online only if EVERY sample in it
					// was. In the day window a column spans minutes, and an
					// outage inside it is exactly what the operator opened
					// the chart to find — letting one healthy sample paint
					// over it would hide the answer.
					col.Online = col.Online && alive
				}
			}
			if !col.HasData {
				col.HasData = true
				col.DownMin, col.DownMax = down, down
				col.UpMin, col.UpMax = up, up
				continue
			}
			col.DownMin = min(col.DownMin, down)
			col.DownMax = max(col.DownMax, down)
			col.UpMin = min(col.UpMin, up)
			col.UpMax = max(col.UpMax, up)
		}
	}
	return out
}

// columnAt — номер столбца, в который попадает момент, с зажимом в границы
// окна: у первого отрезка начало может лежать до окна, и это нормально.
func columnAt(at, from time.Time, width time.Duration, columns int) int {
	idx := int(at.Sub(from) / width)
	if idx < 0 {
		return 0
	}
	if idx >= columns {
		return columns - 1
	}
	return idx
}

// bitsPerSecond is what a person reads a chart in; bytes would make every
// reader do the same multiplication.
func bitsPerSecond(bytes uint64, over time.Duration) uint64 {
	if over <= 0 {
		return 0
	}
	return uint64(float64(bytes) * 8 / over.Seconds())
}
