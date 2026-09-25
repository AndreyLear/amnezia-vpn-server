package hostmetrics

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// Snapshot is a live host load sample. Nil fields mean that source was
// missing or unreadable; Read never fails the whole snapshot.
type Snapshot struct {
	CPU            *float64 // 0-100
	RAM            *float64
	Disk           *float64
	RAMUsedBytes   *int64
	RAMTotalBytes  *int64
	DiskUsedBytes  *int64
	DiskTotalBytes *int64
}

// CPUSample is the previous /proc/stat aggregate used for a CPU percent delta.
// Idle and IOWait are kept apart because iowait is not monotonic — the kernel
// may report it lower than before — so it cannot take part in the check for
// a counter reset (amnezia-vpn-server-76mp.23).
type CPUSample struct {
	Idle   uint64
	IOWait uint64
	Total  uint64
}

// busy is every counter that only grows: all but idle and iowait.
func (c CPUSample) busy() uint64 { return c.Total - c.Idle - c.IOWait }

// Read fills CPU/RAM/Disk. Missing/unreadable sources leave that field nil.
// procDir is typically "/host/proc" in Docker or a testdir with stat+meminfo.
// diskPath is typically "/data"; tests pass t.TempDir().
// prev is the previous CPUSample from the last Read (zero on first call).
func Read(procDir, diskPath string, prev CPUSample) (Snapshot, CPUSample) {
	snap := ReadMemDisk(procDir, diskPath)
	snap.CPU, prev = readCPU(filepath.Join(procDir, "stat"), prev)
	return snap, prev
}

// ReadMemDisk fills RAM and Disk only. The panel takes CPU from a CPUMeter:
// a load is a rate over an interval, and the interval must not depend on
// how often the page asks (amnezia-vpn-server-76mp.23).
func ReadMemDisk(procDir, diskPath string) Snapshot {
	ramPct, ramUsed, ramTotal := readRAM(filepath.Join(procDir, "meminfo"))
	diskPct, diskUsed, diskTotal := readDisk(diskPath)
	return Snapshot{
		RAM:            ramPct,
		Disk:           diskPct,
		RAMUsedBytes:   ramUsed,
		RAMTotalBytes:  ramTotal,
		DiskUsedBytes:  diskUsed,
		DiskTotalBytes: diskTotal,
	}
}

func readCPU(statPath string, prev CPUSample) (*float64, CPUSample) {
	next, ok := parseStat(statPath)
	if !ok {
		return nil, CPUSample{}
	}
	return cpuPercent(prev, next), next
}

// cpuPercent is the load between two samples; nil when there is no earlier
// sample, the counters were reset, or no time passed between them.
func cpuPercent(prev, next CPUSample) *float64 {
	if prev.Total == 0 || next.busy() < prev.busy() {
		return nil
	}
	busy := next.busy() - prev.busy()
	idle := grown(prev.Idle, next.Idle) + grown(prev.IOWait, next.IOWait)
	if busy+idle == 0 {
		return nil
	}
	return clamp(float64(busy) / float64(busy+idle) * 100)
}

func grown(prev, next uint64) uint64 {
	if next < prev {
		return 0
	}
	return next - prev
}

func parseStat(path string) (sample CPUSample, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return CPUSample{}, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 || fields[0] != "cpu" {
			continue
		}
		var vals []uint64
		for _, f := range fields[1:] {
			v, err := strconv.ParseUint(f, 10, 64)
			if err != nil {
				break
			}
			vals = append(vals, v)
			if len(vals) == 8 {
				break
			}
		}
		if len(vals) < 4 {
			return CPUSample{}, false
		}
		for _, v := range vals {
			sample.Total += v
		}
		sample.Idle = vals[3]
		if len(vals) > 4 {
			sample.IOWait = vals[4]
		}
		return sample, true
	}
	return CPUSample{}, false
}

func readRAM(meminfoPath string) (pct *float64, used, totalBytes *int64) {
	f, err := os.Open(meminfoPath)
	if err != nil {
		return nil, nil, nil
	}
	defer f.Close()
	var total, avail uint64
	var haveTotal, haveAvail bool
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		key := strings.TrimSuffix(fields[0], ":")
		v, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch key {
		case "MemTotal":
			total, haveTotal = v, true
		case "MemAvailable":
			avail, haveAvail = v, true
		}
	}
	if !haveTotal || !haveAvail || total == 0 {
		return nil, nil, nil
	}
	totalB := int64(total) * 1024
	usedB := int64(total-avail) * 1024
	return clamp((1 - float64(avail)/float64(total)) * 100), &usedB, &totalB
}

func readDisk(diskPath string) (pct *float64, used, totalBytes *int64) {
	if diskPath == "" {
		diskPath = "/data"
	}
	var st unix.Statfs_t
	if err := unix.Statfs(diskPath, &st); err != nil {
		return nil, nil, nil
	}
	return diskUsage(int64(st.Blocks), int64(st.Bavail), int64(st.Bsize))
}

// diskUsage turns a statfs result into the reported figures. Split out from
// readDisk so the arithmetic can be checked against fixed numbers: taking a
// second statfs of a live filesystem to compare against drifts between the
// two calls, which made the test fail at random.
//
// "Used" is total minus what is available to this user, not minus free
// blocks: the reserved blocks a filesystem keeps back are space the panel
// cannot offer either.
func diskUsage(blocks, bavail, bsize int64) (pct *float64, used, totalBytes *int64) {
	totalB := blocks * bsize
	if totalB == 0 {
		return nil, nil, nil
	}
	availB := bavail * bsize
	usedB := totalB - availB
	return clamp((1 - float64(availB)/float64(totalB)) * 100), &usedB, &totalB
}

func clamp(v float64) *float64 {
	if v < 0 {
		v = 0
	}
	if v > 100 {
		v = 100
	}
	return &v
}
