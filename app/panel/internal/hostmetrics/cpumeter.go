package hostmetrics

import (
	"context"
	"path/filepath"
	"sync"
	"time"
)

// CPUPeriod is the window one CPU figure covers. The dashboard asks every
// five seconds; a shorter window keeps the figure it gets fresh, and two
// seconds of /proc/stat ticks (10 ms each) still give half-percent steps.
const CPUPeriod = 2 * time.Second

// CPUMeter measures CPU load in the background over fixed windows, and
// readers only take the last figure (amnezia-vpn-server-76mp.23).
//
// Before, every request moved one shared reference sample: two open tabs
// measured intervals of tens of milliseconds, where /proc/stat granularity
// makes the load jump between 0 and 100, and the first request after a
// break showed the average over hours.
type CPUMeter struct {
	statPath string
	period   time.Duration

	mu   sync.Mutex
	prev CPUSample
	pct  *float64
}

// NewCPUMeter measures procDir/stat every period once Run is started.
func NewCPUMeter(procDir string, period time.Duration) *CPUMeter {
	return &CPUMeter{statPath: filepath.Join(procDir, "stat"), period: period}
}

// Run samples right away and then every period until ctx is done.
func (m *CPUMeter) Run(ctx context.Context) {
	m.sample()
	t := time.NewTicker(m.period)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.sample()
		}
	}
}

func (m *CPUMeter) sample() {
	next, ok := parseStat(m.statPath)
	m.mu.Lock()
	defer m.mu.Unlock()
	if !ok {
		// The figure goes, the reference stays. Dropping it made the next
		// good read a "first" sample again, and with counters that did not
		// move after it the figure never came back (amnezia-vpn-server-76mp.23).
		m.pct = nil
		return
	}
	if next == m.prev {
		// No time passed as far as the kernel counts it: the last figure
		// still stands.
		return
	}
	m.pct = cpuPercent(m.prev, next)
	m.prev = next
}

// Percent is the load over the last window; nil until two samples were
// taken or while /proc/stat cannot be read.
func (m *CPUMeter) Percent() *float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pct == nil {
		return nil
	}
	v := *m.pct
	return &v
}

var (
	sharedMu     sync.Mutex
	sharedMeters = map[string]*CPUMeter{}
)

// SharedCPUMeter is the process-wide meter for procDir, started on first
// use and running for the life of the process: there is one host to
// measure, however many requests ask about it. period applies only to the
// call that starts it.
func SharedCPUMeter(procDir string, period time.Duration) *CPUMeter {
	sharedMu.Lock()
	defer sharedMu.Unlock()
	if m, ok := sharedMeters[procDir]; ok {
		return m
	}
	m := NewCPUMeter(procDir, period)
	sharedMeters[procDir] = m
	go m.Run(context.Background())
	return m
}
