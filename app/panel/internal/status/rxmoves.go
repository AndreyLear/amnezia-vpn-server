package status

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// RxActivity is when each peer last showed incoming traffic, as seen in the
// speed log (amnezia-vpn-server-0d2n).
//
// It is what the notification rules use to tell «every client dropped at
// once» from «clients went quiet one by one»: a peer is on the line while
// its rx counter keeps moving (tyic), and the moment it stopped is the last
// sample in which the counter grew.
type RxActivity struct {
	// LastMove maps SpeedKey(peer) to the time of the last sample whose rx
	// counter was higher than the one before. Peers that did not move in
	// the window are absent.
	LastMove map[string]time.Time
	// LastSample is the newest sample in the window; zero when there is
	// none, which means the log says nothing about now.
	LastSample time.Time
}

// ReadRxActivity reads the samples of [from, to] from the live speed log
// and, when the window starts before today, the rotated file of the day
// before. Only the tail of each file is read.
func ReadRxActivity(path string, from, to time.Time) (*RxActivity, error) {
	var buf []byte
	if from.UTC().Truncate(24 * time.Hour).Before(to.UTC().Truncate(24 * time.Hour)) {
		prev, err := tailFrom(speedDatedPath(path, from), from)
		if err != nil {
			return nil, err
		}
		buf = append(buf, prev...)
		buf = append(buf, '\n')
	}
	live, err := tailFrom(path, from)
	if err != nil {
		return nil, err
	}
	buf = append(buf, live...)

	out := &RxActivity{LastMove: map[string]time.Time{}}
	lastRx := map[string]uint64{}
	for line := range strings.SplitSeq(string(buf), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 1 {
			continue
		}
		unix, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			continue
		}
		at := time.Unix(unix, 0).UTC()
		if at.Before(from) || at.After(to) {
			continue
		}
		if at.After(out.LastSample) {
			out.LastSample = at
		}
		for _, f := range fields[1:] {
			key, counters, ok := strings.Cut(f, ":")
			if !ok {
				continue
			}
			rxField, _, _ := strings.Cut(counters, ":")
			rx, err := strconv.ParseUint(rxField, 10, 64)
			if err != nil {
				continue
			}
			// A counter lower than before is a reset (the tunnel restarted):
			// it is a new baseline, not traffic.
			if prev, seen := lastRx[key]; seen && rx > prev {
				out.LastMove[key] = at
			}
			lastRx[key] = rx
		}
	}
	return out, nil
}

func tailFrom(path string, from time.Time) ([]byte, error) {
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
	return speedTail(f, info.Size(), from)
}
