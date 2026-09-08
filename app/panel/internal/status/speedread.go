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
// when the window reaches further back than it goes, from the rotated-out
// one as well.
func readSpeedSamples(path, key string, from, to time.Time) ([]speedSample, error) {
	// One sample before the window is deliberately kept: the first rate
	// inside the window is the difference against it, and without it the
	// chart would begin with an unexplained gap.
	reach := from.Add(-SpeedMaxGap)
	current, err := readSpeedFile(path, key, reach, to)
	if err != nil {
		return nil, err
	}
	if len(current) == 0 || current[0].at.After(reach) {
		prev, err := readSpeedFile(SpeedPrevPath(path), key, reach, to)
		if err != nil {
			return nil, err
		}
		current = append(prev, current...)
	}
	sort.Slice(current, func(i, j int) bool { return current[i].at.Before(current[j].at) })
	return current, nil
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
		rxs, txs, ok := strings.Cut(counters, ":")
		if !ok {
			return speedSample{}, false
		}
		rx, err := strconv.ParseUint(rxs, 10, 64)
		if err != nil {
			return speedSample{}, false
		}
		tx, err := strconv.ParseUint(txs, 10, 64)
		if err != nil {
			return speedSample{}, false
		}
		return speedSample{at: time.Unix(unix, 0).UTC(), rx: rx, tx: tx}, true
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
	for i := 1; i < len(samples); i++ {
		a, b := samples[i-1], samples[i]
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
		first := columnAt(a.at, from, width, columns)
		last := columnAt(b.at, from, width, columns)
		for idx := first; idx <= last; idx++ {
			col := &out.Columns[idx]
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
