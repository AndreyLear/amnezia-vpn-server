package hostmetrics

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Запросы только читают последнее значение: сколько бы вкладок ни спрашивало
// между замерами, окно не сдвигается и число не меняется
// (amnezia-vpn-server-76mp.23).
func TestCPUMeterReadsDoNotMoveTheWindow(t *testing.T) {
	dir := t.TempDir()
	writeProc(t, dir, statSample1, meminfoOK)
	m := NewCPUMeter(dir, time.Hour)
	m.sample()
	assertNil(t, "CPU после первого замера", m.Percent())
	writeProc(t, dir, statSample2, meminfoOK)
	m.sample()
	assertPct(t, "CPU", m.Percent(), wantCPU, 0.01)
	writeProc(t, dir, "cpu  2001 400 600 4500 200 100 100 100\n", meminfoOK)
	for i := 0; i < 5; i++ {
		assertPct(t, "CPU между замерами", m.Percent(), wantCPU, 0.01)
	}
}

// Замер без прошедшего времени (счётчики не сдвинулись) не стирает число.
func TestCPUMeterKeepsFigureOnEmptyDelta(t *testing.T) {
	dir := t.TempDir()
	writeProc(t, dir, statSample1, meminfoOK)
	m := NewCPUMeter(dir, time.Hour)
	m.sample()
	writeProc(t, dir, statSample2, meminfoOK)
	m.sample()
	m.sample()
	assertPct(t, "CPU", m.Percent(), wantCPU, 0.01)
}

// Фоновый замер идёт сам, с заданным периодом.
func TestCPUMeterRunSamplesInBackground(t *testing.T) {
	dir := t.TempDir()
	writeProc(t, dir, statSample1, meminfoOK)
	m := NewCPUMeter(dir, 10*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	time.Sleep(30 * time.Millisecond)
	writeProc(t, dir, statSample2, meminfoOK)
	deadline := time.Now().Add(5 * time.Second)
	for m.Percent() == nil && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	assertPct(t, "CPU", m.Percent(), wantCPU, 0.01)
	cancel()
	<-done
}

// iowait в /proc/stat может уменьшаться; это не сброс счётчиков, и число
// остаётся (amnezia-vpn-server-76mp.23). busy +100, idle +10, iowait −100 →
// 100 / (100 + 10).
func TestReadCPUIowaitGoingBackIsNotAReset(t *testing.T) {
	dir := t.TempDir()
	disk := t.TempDir()
	writeProc(t, dir, "cpu  1000 0 0 4000 500 0 0 0\n", meminfoOK)
	_, prev := Read(dir, disk, CPUSample{})
	writeProc(t, dir, "cpu  1100 0 0 4010 400 0 0 0\n", meminfoOK)
	snap, _ := Read(dir, disk, prev)
	assertPct(t, "CPU", snap.CPU, 100.0*100.0/110.0, 0.01)
}

// Неудачное чтение /proc/stat (пустой файл, сбой открытия) гасит число, но
// не выбрасывает прежний отсчёт: иначе следующий удачный замер снова стал
// бы «первым», а при неподвижных счётчиках не вернулось бы и число
// (amnezia-vpn-server-76mp.23).
func TestCPUMeterFailedReadKeepsTheReference(t *testing.T) {
	dir := t.TempDir()
	writeProc(t, dir, statSample1, meminfoOK)
	m := NewCPUMeter(dir, time.Hour)
	m.sample()
	// Пустой файл — то, что читатель видит посреди неатомарной перезаписи.
	if err := os.WriteFile(filepath.Join(dir, "stat"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	m.sample()
	assertNil(t, "CPU при нечитаемом /proc/stat", m.Percent())
	writeProc(t, dir, statSample2, meminfoOK)
	m.sample()
	assertPct(t, "CPU после восстановления чтения", m.Percent(), wantCPU, 0.01)
	m.sample()
	assertPct(t, "CPU при неподвижных счётчиках", m.Percent(), wantCPU, 0.01)
}

// writeProcAtomic пишет как настоящий писатель — через tmp + rename: фоновый
// замер, читающий файл посреди записи, не должен видеть обрезанный файл.
func writeProcAtomic(t *testing.T, dir, stat, meminfo string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"stat": stat, "meminfo": meminfo} {
		tmp := filepath.Join(dir, name+".tmp")
		if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
}

// SharedCPUMeter раньше запускала фоновый цикл через "go m.Run(ctx)", и
// первый замер случался тогда, когда планировщик доберётся до этой горутины,
// а не до возврата из SharedCPUMeter. Вызывающий код, который сразу же
// подменял /proc/stat вторым отсчётом (как это делает
// TestHostStatsCPUFirstNullThenPercent), мог выиграть эту гонку: «первый»
// замер горутины читал уже второй отсчёт и брал его за опору, а раз файл
// после этого не менялся, каждый следующий тик сравнивал его сам с собой —
// cpu_percent так и оставался nil (amnezia-vpn-server-azsm). Тест проверяет
// исправление напрямую: опорный отсчёт должен быть на месте сразу же по
// возврату из SharedCPUMeter, до того как эта горутина успеет сделать
// что-то, что дало бы фоновой горутине шанс выполниться первой.
func TestSharedCPUMeterFirstSampleIsSynchronous(t *testing.T) {
	dir := t.TempDir()
	writeProcAtomic(t, dir, statSample1, meminfoOK)
	want, ok := parseStat(filepath.Join(dir, "stat"))
	if !ok {
		t.Fatal("parseStat(statSample1) failed")
	}

	// Период достаточно большой, чтобы тикер фонового цикла не сработал за
	// время теста: единственный замер, который мог случиться к моменту
	// возврата из SharedCPUMeter, — синхронный.
	m := SharedCPUMeter(dir, time.Hour)

	m.mu.Lock()
	prev := m.prev
	pct := m.pct
	m.mu.Unlock()
	if prev != want {
		t.Fatalf("опорный отсчёт сразу после SharedCPUMeter = %+v, хотим %+v (первое чтение было не синхронным)", prev, want)
	}
	if pct != nil {
		t.Fatalf("cpu_percent после самого первого замера = %v, хотим nil", *pct)
	}

	// Подмена /proc/stat теперь уже не гонка: опора — уже statSample1, и
	// ручной прогон следующего тика цикла (тот же пакет, публичного хука
	// нет) должен считать вперёд от неё — как и ожидает от общего замерщика
	// TestHostStatsCPUFirstNullThenPercent.
	writeProcAtomic(t, dir, statSample2, meminfoOK)
	m.sample()
	assertPct(t, "CPU", m.Percent(), wantCPU, 0.01)
}
