package awgconf

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

// Генерация читает базу только под межпроцессной блокировкой рядом с
// awg0.conf. Иначе CLI, прочитавший клиентов до того, как веб отключил
// одного из них, записывал файл после веба, и отключённый пир сохранял
// доступ до следующего изменения (amnezia-vpn-server-76mp.18).
//
// Второй «процесс» здесь — тест, держащий ту же блокировку.
func TestGenerateReadsUnderConfigLock(t *testing.T) {
	handle, dir := newTestDB(t)
	seedServer(t, handle, "", "")
	seedClient(t, handle, 1, "", true, "10.8.0.2/32")
	target := filepath.Join(dir, "awg0.conf")

	lock, err := os.OpenFile(target+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- Generate(handle, target) }()

	select {
	case err := <-done:
		t.Fatalf("Generate не ждал чужую блокировку (err = %v)", err)
	case <-time.After(200 * time.Millisecond):
	}
	// Пока «другой процесс» держит блокировку, клиента отключают.
	if err := db.SetClientEnabled(handle, 1, false); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("Generate: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), testKey(21)) {
		t.Fatalf("отключённый клиент остался в awg0.conf:\n%s", data)
	}
}

// Две генерации наперегонки с изменениями базы: последний записанный файл
// всегда отражает последнее закоммиченное состояние.
func TestConcurrentGenerateEndsWithLatestState(t *testing.T) {
	handle, dir := newTestDB(t)
	seedServer(t, handle, "", "")
	seedClient(t, handle, 1, "", true, "10.8.0.2/32")
	target := filepath.Join(dir, "awg0.conf")

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() { // «CLI»: перегенерирует без остановки
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if err := Generate(handle, target); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	// «Веб»: меняет клиента и перегенерирует после каждого коммита.
	for i := 0; i < 30; i++ {
		if err := db.SetClientEnabled(handle, 1, i%2 == 0); err != nil {
			t.Fatal(err)
		}
		if err := Generate(handle, target); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.SetClientEnabled(handle, 1, false); err != nil {
		t.Fatal(err)
	}
	if err := Generate(handle, target); err != nil {
		t.Fatal(err)
	}
	close(stop)
	wg.Wait()
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), testKey(21)) {
		t.Fatalf("после гонки отключённый клиент остался в awg0.conf:\n%s", data)
	}
}
