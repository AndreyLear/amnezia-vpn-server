package db

import (
	"database/sql"
	"testing"
	"time"
)

func trafficDB(t *testing.T) (*sql.DB, *ClientRecord) {
	t.Helper()
	handle, _ := openTest(t, "amnezia.sqlite")
	if err := Migrate(handle); err != nil {
		t.Fatal(err)
	}
	c, err := CreateClient(handle, "10.8.0.1/24", NewClient{Name: "a", PrivateKey: testPriv, PublicKey: testPub})
	if err != nil {
		t.Fatal(err)
	}
	return handle, c
}

func trafficOf(t *testing.T, handle *sql.DB, id int64) VLESSTraffic {
	t.Helper()
	all, err := VLESSTrafficAll(handle)
	if err != nil {
		t.Fatal(err)
	}
	return all[id]
}

// Счётчики Xray живут в памяти и обнуляются при каждом его перезапуске;
// панель копит их сама, и накопленное переживает перезапуск
// (amnezia-vpn-server-14y0.8).
func TestVLESSTrafficSurvivesXrayRestart(t *testing.T) {
	handle, c := trafficDB(t)
	start := time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)
	now := start.Add(time.Minute)
	rec := func(started time.Time, up, down uint64) {
		t.Helper()
		if err := RecordVLESSCounters(handle, started, map[int64]VLESSCounters{c.ID: {Up: up, Down: down}}, now); err != nil {
			t.Fatal(err)
		}
		now = now.Add(10 * time.Second)
	}

	// Первое знакомство: всё, что Xray насчитал с запуска, — этого клиента.
	rec(start, 100, 1000)
	if got := trafficOf(t, handle, c.ID); got.Up != 100 || got.Down != 1000 {
		t.Fatalf("первый опрос: %+v", got)
	}
	rec(start, 150, 3000)
	if got := trafficOf(t, handle, c.ID); got.Up != 150 || got.Down != 3000 {
		t.Fatalf("рост счётчика: %+v", got)
	}

	// Xray перезапустился, и к опросу счётчик успел перерасти прежний:
	// по одному сравнению «стало меньше» перезапуск не заметить, его
	// выдаёт время запуска.
	restarted := now.Add(-5 * time.Second)
	rec(restarted, 200, 4000)
	if got := trafficOf(t, handle, c.ID); got.Up != 350 || got.Down != 7000 {
		t.Fatalf("после перезапуска (счётчик вырос выше прежнего): %+v", got)
	}

	// Время запуска неточно на секунду-другую (аптайм в целых секундах,
	// задержка ответа) — это не перезапуск.
	rec(restarted.Add(2*time.Second), 260, 4000)
	if got := trafficOf(t, handle, c.ID); got.Up != 410 || got.Down != 7000 {
		t.Fatalf("дрожание времени запуска принято за перезапуск: %+v", got)
	}

	// Счётчик стал меньше — Xray перезапущен, даже если время запуска
	// почему-то совпало.
	rec(restarted, 10, 20)
	if got := trafficOf(t, handle, c.ID); got.Up != 420 || got.Down != 7020 {
		t.Fatalf("счётчик меньше прежнего: %+v", got)
	}
}

// Последняя активность — время опроса, на котором счётчик сдвинулся.
func TestVLESSTrafficLastActive(t *testing.T) {
	handle, c := trafficDB(t)
	start := time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)
	t1 := start.Add(time.Minute)
	if got := trafficOf(t, handle, c.ID); got.LastActive != nil {
		t.Fatalf("активность без единого опроса: %v", got.LastActive)
	}
	if err := RecordVLESSCounters(handle, start, map[int64]VLESSCounters{c.ID: {}}, t1); err != nil {
		t.Fatal(err)
	}
	if got := trafficOf(t, handle, c.ID); got.LastActive != nil {
		t.Fatalf("нулевой счётчик дал активность: %v", got.LastActive)
	}
	t2 := t1.Add(10 * time.Second)
	if err := RecordVLESSCounters(handle, start, map[int64]VLESSCounters{c.ID: {Down: 5}}, t2); err != nil {
		t.Fatal(err)
	}
	t3 := t2.Add(10 * time.Second)
	if err := RecordVLESSCounters(handle, start, map[int64]VLESSCounters{c.ID: {Down: 5}}, t3); err != nil {
		t.Fatal(err)
	}
	got := trafficOf(t, handle, c.ID)
	if got.LastActive == nil || !got.LastActive.Equal(t2) {
		t.Fatalf("последняя активность %v, ждали %v", got.LastActive, t2)
	}
}

// Номер удалённого клиента может достаться новому (id без AUTOINCREMENT),
// а счётчик c<id> в Xray переживает удаление пользователя. Новый клиент не
// должен унаследовать ни накопленного, ни того, что старый успел
// насчитать в Xray.
func TestVLESSTrafficNotInheritedByReusedID(t *testing.T) {
	handle, c := trafficDB(t)
	start := time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)
	now := start.Add(time.Minute)
	if err := RecordVLESSCounters(handle, start, map[int64]VLESSCounters{c.ID: {Up: 7, Down: 900}}, now); err != nil {
		t.Fatal(err)
	}
	if err := DeleteClient(handle, c.ID); err != nil {
		t.Fatal(err)
	}
	// Опрос между удалением и новым клиентом: счётчик удалённого растёт.
	now = now.Add(10 * time.Second)
	if err := RecordVLESSCounters(handle, start, map[int64]VLESSCounters{c.ID: {Up: 8, Down: 950}}, now); err != nil {
		t.Fatal(err)
	}
	n, err := CreateClient(handle, "10.8.0.1/24", NewClient{Name: "b", PrivateKey: testPriv, PublicKey: testPub2})
	if err != nil {
		t.Fatal(err)
	}
	if n.ID != c.ID {
		t.Skipf("номер не переиспользован (%d → %d)", c.ID, n.ID)
	}
	if got := trafficOf(t, handle, n.ID); got.Up != 0 || got.Down != 0 || got.LastActive != nil {
		t.Fatalf("новый клиент унаследовал трафик: %+v", got)
	}
	now = now.Add(10 * time.Second)
	if err := RecordVLESSCounters(handle, start, map[int64]VLESSCounters{n.ID: {Up: 10, Down: 1000}}, now); err != nil {
		t.Fatal(err)
	}
	if got := trafficOf(t, handle, n.ID); got.Up != 2 || got.Down != 50 {
		t.Fatalf("новому клиенту засчитано чужое: %+v", got)
	}
}

// То же, но новый клиент заведён раньше, чем панель успела опросить Xray:
// накопленное удалённого обнуляет само удаление.
func TestVLESSTrafficResetOnDelete(t *testing.T) {
	handle, c := trafficDB(t)
	start := time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)
	now := start.Add(time.Minute)
	if err := RecordVLESSCounters(handle, start, map[int64]VLESSCounters{c.ID: {Up: 7, Down: 900}}, now); err != nil {
		t.Fatal(err)
	}
	if err := DeleteClient(handle, c.ID); err != nil {
		t.Fatal(err)
	}
	n, err := CreateClient(handle, "10.8.0.1/24", NewClient{Name: "b", PrivateKey: testPriv, PublicKey: testPub2})
	if err != nil {
		t.Fatal(err)
	}
	if n.ID != c.ID {
		t.Skipf("номер не переиспользован (%d → %d)", c.ID, n.ID)
	}
	if got := trafficOf(t, handle, n.ID); got.Up != 0 || got.Down != 0 || got.LastActive != nil {
		t.Fatalf("новый клиент унаследовал трафик: %+v", got)
	}
	now = now.Add(10 * time.Second)
	if err := RecordVLESSCounters(handle, start, map[int64]VLESSCounters{n.ID: {Up: 8, Down: 950}}, now); err != nil {
		t.Fatal(err)
	}
	if got := trafficOf(t, handle, n.ID); got.Up != 1 || got.Down != 50 {
		t.Fatalf("новому клиенту засчитано чужое: %+v", got)
	}
}
