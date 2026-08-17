package hostmetrics

import (
	"os"
	"path/filepath"
	"testing"
)

// Crafted /proc/stat samples. First 8 counters: user nice system idle
// iowait irq softirq steal.
//
// sample1 total=5750 idle_all=4100
// sample2 total=8000 idle_all=4700
// deltaTotal=2250 deltaIdle=600 → CPU = (1-600/2250)*100 = 73.333...%
const (
	statSample1 = "cpu  1000 200 300 4000 100 50 50 50 extra ignored\ncpu0 1 0 0 0\n"
	statSample2 = "cpu  2000 400 600 4500 200 100 100 100\ncpu0 2 0 0 0\n"
	meminfoOK   = "MemTotal:       8000000 kB\nMemFree:        1000000 kB\nMemAvailable:   2000000 kB\n"
	wantCPU     = 100.0 * (1.0 - 600.0/2250.0) // 73.333...
	wantRAM     = 75.0                         // 1 - 2000000/8000000
)

func writeProc(t *testing.T, dir, stat, meminfo string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if stat != "" {
		if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(stat), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if meminfo != "" {
		if err := os.WriteFile(filepath.Join(dir, "meminfo"), []byte(meminfo), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func assertNil(t *testing.T, name string, p *float64) {
	t.Helper()
	if p != nil {
		t.Fatalf("%s = %v, want nil", name, *p)
	}
}

func assertPct(t *testing.T, name string, p *float64, want, eps float64) {
	t.Helper()
	if p == nil {
		t.Fatalf("%s is nil, want %v", name, want)
	}
	got := *p
	if got < want-eps || got > want+eps {
		t.Fatalf("%s = %v, want %v ± %v", name, got, want, eps)
	}
	if got < 0 || got > 100 {
		t.Fatalf("%s = %v, want clamped to [0, 100]", name, got)
	}
}

func TestReadCPUDeltaKnownPercent(t *testing.T) {
	dir := t.TempDir()
	disk := t.TempDir()
	writeProc(t, dir, statSample1, meminfoOK)

	first, prev := Read(dir, disk, CPUSample{})
	assertNil(t, "first CPU", first.CPU)
	assertPct(t, "RAM", first.RAM, wantRAM, 0.01)

	writeProc(t, dir, statSample2, meminfoOK)
	second, _ := Read(dir, disk, prev)
	assertPct(t, "CPU", second.CPU, wantCPU, 0.01)
	assertPct(t, "RAM", second.RAM, wantRAM, 0.01)
}

func TestReadMissingProcDirCPUAndRAMNilDiskOK(t *testing.T) {
	disk := t.TempDir()
	snap, _ := Read(filepath.Join(t.TempDir(), "no-such-proc"), disk, CPUSample{})
	assertNil(t, "CPU", snap.CPU)
	assertNil(t, "RAM", snap.RAM)
	if snap.Disk == nil {
		t.Fatal("Disk is nil, want percent for temp dir")
	}
	if *snap.Disk < 0 || *snap.Disk > 100 {
		t.Fatalf("Disk = %v, want [0, 100]", *snap.Disk)
	}
}

func TestReadMissingMeminfoRAMNilCPUWorks(t *testing.T) {
	dir := t.TempDir()
	disk := t.TempDir()
	writeProc(t, dir, statSample1, "")
	_, prev := Read(dir, disk, CPUSample{})
	writeProc(t, dir, statSample2, "")
	snap, _ := Read(dir, disk, prev)
	assertNil(t, "RAM", snap.RAM)
	assertPct(t, "CPU", snap.CPU, wantCPU, 0.01)
}

func TestReadFirstCPUNilSecondSet(t *testing.T) {
	dir := t.TempDir()
	disk := t.TempDir()
	writeProc(t, dir, statSample1, meminfoOK)
	first, prev := Read(dir, disk, CPUSample{})
	assertNil(t, "first CPU", first.CPU)
	if prev.Total == 0 {
		t.Fatal("first Read CPUSample.Total is 0, want parsed total for next poll")
	}
	writeProc(t, dir, statSample2, meminfoOK)
	second, _ := Read(dir, disk, prev)
	if second.CPU == nil {
		t.Fatal("second CPU is nil, want set after two samples")
	}
}

func TestReadDiskTempDirInRange(t *testing.T) {
	dir := t.TempDir()
	disk := t.TempDir()
	writeProc(t, dir, statSample1, meminfoOK)
	snap, _ := Read(dir, disk, CPUSample{})
	if snap.Disk == nil {
		t.Fatal("Disk is nil, want *float64 in [0, 100]")
	}
	if *snap.Disk < 0 || *snap.Disk > 100 {
		t.Fatalf("Disk = %v, want [0, 100]", *snap.Disk)
	}
}

func TestReadInvalidStatCPUNil(t *testing.T) {
	disk := t.TempDir()
	cases := []struct {
		name string
		stat string
	}{
		{"empty", ""},
		{"no cpu line", "intr 1\nctxt 2\n"},
		{"cpu without numbers", "cpu\n"},
		{"non-numeric", "cpu  not-a-number\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeProc(t, dir, tc.stat, meminfoOK)
			snap, sample := Read(dir, disk, CPUSample{Idle: 1, Total: 100})
			assertNil(t, "CPU", snap.CPU)
			if sample.Total != 0 || sample.Idle != 0 {
				t.Fatalf("CPUSample = %+v, want zero when stat is invalid", sample)
			}
			assertPct(t, "RAM", snap.RAM, wantRAM, 0.01)
		})
	}
}

func TestReadZeroMemTotalRAMNil(t *testing.T) {
	dir := t.TempDir()
	disk := t.TempDir()
	writeProc(t, dir, statSample1, "MemTotal: 0 kB\nMemAvailable: 1 kB\n")
	snap, _ := Read(dir, disk, CPUSample{})
	assertNil(t, "RAM", snap.RAM)
}

func TestReadBadDiskPathDiskNil(t *testing.T) {
	dir := t.TempDir()
	writeProc(t, dir, statSample1, meminfoOK)
	snap, _ := Read(dir, filepath.Join(t.TempDir(), "missing-mount"), CPUSample{})
	assertNil(t, "Disk", snap.Disk)
}
