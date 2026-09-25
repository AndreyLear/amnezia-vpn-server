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
