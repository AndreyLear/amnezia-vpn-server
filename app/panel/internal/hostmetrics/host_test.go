package hostmetrics

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// testdataDir is the hostmetrics testdata folder next to this file, resolved
// via runtime.Caller so go test works from app/panel (CI cwd), not only from
// the package directory.
func testdataDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Join(filepath.Dir(file), "testdata")
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("testdata not found at %s: %v", dir, err)
	}
	return dir
}

func fixtureProc(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(testdataDir(t), name)
	stat, err := os.ReadFile(filepath.Join(dir, "stat"))
	if err != nil {
		t.Fatalf("%s/stat: %v", name, err)
	}
	first := strings.Fields(strings.Split(string(stat), "\n")[0])
	if len(first) < 9 || first[0] != "cpu" {
		t.Fatalf("%s/stat first line must start with cpu and have 8 counters, got %q", name, first)
	}
	mem, err := os.ReadFile(filepath.Join(dir, "meminfo"))
	if err != nil {
		t.Fatalf("%s/meminfo: %v", name, err)
	}
	body := string(mem)
	if !strings.Contains(body, "MemTotal:") || !strings.Contains(body, "MemAvailable:") {
		t.Fatalf("%s/meminfo missing MemTotal or MemAvailable:\n%s", name, body)
	}
	return dir
}

func TestRead_proc1ThenProc2_secondCPUNonNil(t *testing.T) {
	proc1 := fixtureProc(t, "proc1")
	proc2 := fixtureProc(t, "proc2")
	disk := t.TempDir()

	first, prev := Read(proc1, disk, CPUSample{})
	if first.CPU != nil {
		t.Fatalf("first CPU = %v, want nil (no delta yet)", *first.CPU)
	}
	if prev.Total == 0 {
		t.Fatal("first CPUSample.Total = 0")
	}

	second, _ := Read(proc2, disk, prev)
	if second.CPU == nil {
		t.Fatal("second CPU = nil, want non-nil delta from proc1 → proc2")
	}
	if *second.CPU < 0 || *second.CPU > 100 {
		t.Fatalf("second CPU = %v, want clamped [0, 100]", *second.CPU)
	}
}

func TestRead_fixtureRAM_inOpenClosedUnitInterval(t *testing.T) {
	disk := t.TempDir()
	snap, _ := Read(fixtureProc(t, "proc1"), disk, CPUSample{})
	if snap.RAM == nil {
		t.Fatal("RAM = nil, want percent from fixture meminfo")
	}
	if *snap.RAM <= 0 || *snap.RAM > 100 {
		t.Fatalf("RAM = %v, want (0, 100]", *snap.RAM)
	}
}

func TestRead_missingProcDir_nilCPURAM_diskTempNonNil(t *testing.T) {
	disk := t.TempDir()
	snap, _ := Read(filepath.Join(t.TempDir(), "no-such-proc"), disk, CPUSample{})
	if snap.CPU != nil {
		t.Fatalf("CPU = %v, want nil", *snap.CPU)
	}
	if snap.RAM != nil {
		t.Fatalf("RAM = %v, want nil", *snap.RAM)
	}
	if snap.Disk == nil {
		t.Fatal("Disk = nil, want Statfs of t.TempDir()")
	}
	if *snap.Disk < 0 || *snap.Disk > 100 {
		t.Fatalf("Disk = %v, want [0, 100]", *snap.Disk)
	}
}

func TestRead_emptyDiskPath_usesDataAndDoesNotPanic(t *testing.T) {
	defer func() {
		if rec := recover(); rec != nil {
			t.Fatalf("Read(\"\", \"\") panicked: %v", rec)
		}
	}()
	snap, _ := Read("", "", CPUSample{})
	if snap.Disk != nil && (*snap.Disk < 0 || *snap.Disk > 100) {
		t.Fatalf("Disk = %v, want nil (no /data) or clamped [0, 100]", *snap.Disk)
	}
}
