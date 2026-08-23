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
type CPUSample struct {
	Idle  uint64
	Total uint64
}

// Read fills CPU/RAM/Disk. Missing/unreadable sources leave that field nil.
// procDir is typically "/host/proc" in Docker or a testdir with stat+meminfo.
// diskPath is typically "/data"; tests pass t.TempDir().
// prev is the previous CPUSample from the last Read (zero on first call).
func Read(procDir, diskPath string, prev CPUSample) (Snapshot, CPUSample) {
	cpu, next := readCPU(filepath.Join(procDir, "stat"), prev)
	ramPct, ramUsed, ramTotal := readRAM(filepath.Join(procDir, "meminfo"))
	diskPct, diskUsed, diskTotal := readDisk(diskPath)
	return Snapshot{
		CPU:            cpu,
		RAM:            ramPct,
		Disk:           diskPct,
		RAMUsedBytes:   ramUsed,
		RAMTotalBytes:  ramTotal,
		DiskUsedBytes:  diskUsed,
		DiskTotalBytes: diskTotal,
	}, next
}

func readCPU(statPath string, prev CPUSample) (*float64, CPUSample) {
	total, idle, ok := parseStat(statPath)
	if !ok {
		return nil, CPUSample{}
	}
	next := CPUSample{Idle: idle, Total: total}
	if prev.Total == 0 {
		return nil, next
	}
	if total < prev.Total || idle < prev.Idle {
		return nil, next
	}
	deltaTotal := total - prev.Total
	if deltaTotal == 0 {
		return nil, next
	}
	deltaIdle := idle - prev.Idle
	pct := (1 - float64(deltaIdle)/float64(deltaTotal)) * 100
	return clamp(pct), next
}

func parseStat(path string) (total, idle uint64, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, false
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
			return 0, 0, false
		}
		for _, v := range vals {
			total += v
		}
		idle = vals[3]
		if len(vals) > 4 {
			idle += vals[4] // iowait
		}
		return total, idle, true
	}
	return 0, 0, false
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
