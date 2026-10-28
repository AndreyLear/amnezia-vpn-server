package db

import (
	"database/sql"
	"testing"
	"time"
)

// hoursOf — часовые корзины клиента за всё время, по часу.
func hoursOf(t *testing.T, handle *sql.DB, id int64) map[time.Time]TrafficHour {
	t.Helper()
	all, err := ClientTrafficHours(handle, id, time.Unix(0, 0), time.Now().Add(10*365*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	out := map[time.Time]TrafficHour{}
	for _, h := range all {
		out[h.Hour] = h
	}
	return out
}

func recordAWG(t *testing.T, handle *sql.DB, pub string, rx, tx uint64, now time.Time) {
	t.Helper()
	if err := RecordAWGCounters(handle, map[string]AWGCounters{pub: {Rx: rx, Tx: tx}}, now); err != nil {
		t.Fatal(err)
	}
}

// Счётчики AWG в status.json живут, пока жив интерфейс: перезапуск
// контейнера awg и отключение клиента (его пир убирается из конфига)
// начинают их с нуля. В часовую корзину идёт прирост, и ни сброс, ни
// первое знакомство не дают отрицательного или чужого трафика
// (amnezia-vpn-server-gzmb).
func TestAWGTrafficHourlyDeltas(t *testing.T) {
	handle, c := trafficDB(t)
	h10 := time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)
	h11 := h10.Add(time.Hour)

	// Первое знакомство — только точка отсчёта: в счётчике всё, что
	// клиент накачал с запуска awg, может быть, за неделю до обновления,
	// и класть это в один час нельзя. История начинается с обновления.
	recordAWG(t, handle, c.PublicKey, 5000, 90000, h10.Add(5*time.Minute))
	if got := hoursOf(t, handle, c.ID); len(got) != 0 {
		t.Fatalf("первое знакомство попало в историю: %+v", got)
	}

	recordAWG(t, handle, c.PublicKey, 5100, 91000, h10.Add(6*time.Minute))
	recordAWG(t, handle, c.PublicKey, 5300, 94000, h10.Add(59*time.Minute))
	// Следующий час — своя корзина.
	recordAWG(t, handle, c.PublicKey, 5400, 95000, h11.Add(time.Minute))
	// awg перезапущен: счётчик меньше прежнего — всё новое значение
	// набежало заново, отрицательной дельты нет.
	recordAWG(t, handle, c.PublicKey, 70, 800, h11.Add(2*time.Minute))
	// Без изменений — ни строки, ни нуля поверх.
	recordAWG(t, handle, c.PublicKey, 70, 800, h11.Add(3*time.Minute))

	got := hoursOf(t, handle, c.ID)
	if len(got) != 2 {
		t.Fatalf("корзин %d, нужно 2: %+v", len(got), got)
	}
	if h := got[h10]; h.AWGRx != 300 || h.AWGTx != 4000 || h.VLESSRx != 0 || h.VLESSTx != 0 {
		t.Fatalf("час 10: %+v", h)
	}
	if h := got[h11]; h.AWGRx != 100+70 || h.AWGTx != 1000+800 {
		t.Fatalf("час 11 (рост и сброс): %+v", h)
	}
}

// Пира клиента нет в status.json (клиент отключён, awg остановлен) —
// точка отсчёта не трогается; пир с другим ключом — другой пир, его
// счётчик начинается с нуля и целиком идёт в прирост.
func TestAWGTrafficMissingPeerAndNewKey(t *testing.T) {
	handle, c := trafficDB(t)
	h := time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)
	recordAWG(t, handle, c.PublicKey, 1000, 1000, h)
	if err := RecordAWGCounters(handle, nil, h.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	recordAWG(t, handle, c.PublicKey, 1500, 1200, h.Add(2*time.Minute))
	if got := hoursOf(t, handle, c.ID)[h]; got.AWGRx != 500 || got.AWGTx != 200 {
		t.Fatalf("после пропуска пира: %+v", got)
	}

	// Ключ клиента сменился в базе: прежняя точка отсчёта к новому пиру
	// не относится.
	if _, err := handle.Exec(`UPDATE clients SET public_key = 'bmV3a2V5' WHERE id = ?`, c.ID); err != nil {
		t.Fatal(err)
	}
	recordAWG(t, handle, "bmV3a2V5", 3000, 4000, h.Add(3*time.Minute))
	if got := hoursOf(t, handle, c.ID)[h]; got.AWGRx != 3500 || got.AWGTx != 4200 {
		t.Fatalf("новый ключ: %+v", got)
	}
}

// Трафик VLESS идёт в часовые корзины теми же приростами, что копятся в
// «Скачал/Отдал»: перезапуск Xray узнаётся так же, по времени запуска
// (amnezia-vpn-server-gzmb).
func TestVLESSTrafficHourly(t *testing.T) {
	handle, c := trafficDB(t)
	start := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	h10 := time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)
	rec := func(started time.Time, up, down uint64, now time.Time) {
		t.Helper()
		if err := RecordVLESSCounters(handle, started, map[int64]VLESSCounters{c.ID: {Up: up, Down: down}}, now); err != nil {
			t.Fatal(err)
		}
	}
	rec(start, 100, 1000, h10.Add(time.Minute))
	rec(start, 150, 3000, h10.Add(2*time.Minute))
	// Перезапуск Xray, счётчик к опросу перерос прежний.
	restarted := h10.Add(time.Hour + time.Minute)
	rec(restarted, 200, 4000, restarted.Add(5*time.Second))

	got := hoursOf(t, handle, c.ID)
	if h := got[h10]; h.VLESSRx != 150 || h.VLESSTx != 3000 || h.AWGRx != 0 {
		t.Fatalf("час 10: %+v", h)
	}
	if h := got[h10.Add(time.Hour)]; h.VLESSRx != 200 || h.VLESSTx != 4000 {
		t.Fatalf("час 11 после перезапуска: %+v", h)
	}
}

// Оба протокола в один час — одна корзина ответа, каждый в своих полях.
func TestTrafficHoursMergeProtocols(t *testing.T) {
	handle, c := trafficDB(t)
	h := time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)
	recordAWG(t, handle, c.PublicKey, 0, 0, h)
	recordAWG(t, handle, c.PublicKey, 10, 20, h.Add(time.Minute))
	if err := RecordVLESSCounters(handle, h, map[int64]VLESSCounters{c.ID: {Up: 30, Down: 40}}, h.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	all, err := ClientTrafficHours(handle, c.ID, h, h.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0] != (TrafficHour{Hour: h, AWGRx: 10, AWGTx: 20, VLESSRx: 30, VLESSTx: 40}) {
		t.Fatalf("слияние протоколов: %+v", all)
	}
	// Граница периода — по началу часа: час до from не входит.
	none, err := ClientTrafficHours(handle, c.ID, h.Add(time.Hour), h.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("час вне периода: %+v", none)
	}
}

// История хранится 365 дней: старше — удаляется на ближайшей записи,
// так же как журнал панели подрезается на записи (amnezia-vpn-server-gzmb).
func TestTrafficHistoryKeeps365Days(t *testing.T) {
	handle, c := trafficDB(t)
	old := time.Date(2025, 10, 1, 10, 0, 0, 0, time.UTC)
	recordAWG(t, handle, c.PublicKey, 0, 0, old)
	recordAWG(t, handle, c.PublicKey, 1, 1, old.Add(time.Minute))
	edge := time.Date(2025, 10, 28, 12, 0, 0, 0, time.UTC)
	recordAWG(t, handle, c.PublicKey, 2, 2, edge.Add(time.Minute))
	now := time.Date(2026, 10, 28, 12, 30, 0, 0, time.UTC)
	recordAWG(t, handle, c.PublicKey, 3, 3, now)

	got := hoursOf(t, handle, c.ID)
	if _, ok := got[old]; ok {
		t.Fatalf("корзина старше 365 дней не удалена: %+v", got)
	}
	if _, ok := got[edge]; !ok {
		t.Fatalf("корзина ровно 365 дней назад удалена раньше срока: %+v", got)
	}
	if _, ok := got[now.Truncate(time.Hour)]; !ok {
		t.Fatalf("свежая корзина пропала: %+v", got)
	}
}

// Удаление клиента удаляет его историю и точку отсчёта AWG: номер
// достаётся следующему клиенту, и чужие дни на его графике хуже пустоты.
func TestDeleteClientDropsTrafficHistory(t *testing.T) {
	handle, c := trafficDB(t)
	h := time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)
	recordAWG(t, handle, c.PublicKey, 0, 0, h)
	recordAWG(t, handle, c.PublicKey, 10, 10, h.Add(time.Minute))
	if err := RecordVLESSCounters(handle, h, map[int64]VLESSCounters{c.ID: {Up: 5, Down: 5}}, h); err != nil {
		t.Fatal(err)
	}
	if err := DeleteClient(handle, c.ID); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"client_traffic_hourly", "awg_traffic"} {
		var n int
		if err := handle.QueryRow(`SELECT count(*) FROM `+table+` WHERE client_id = ?`, c.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("%s: после удаления клиента осталось строк: %d", table, n)
		}
	}
}

// Таблицы аддитивны: повторная миграция их не трогает.
func TestTrafficTablesMigrateIdempotent(t *testing.T) {
	handle, c := trafficDB(t)
	h := time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)
	recordAWG(t, handle, c.PublicKey, 0, 0, h)
	recordAWG(t, handle, c.PublicKey, 7, 9, h.Add(time.Minute))
	if err := Migrate(handle); err != nil {
		t.Fatal(err)
	}
	if got := hoursOf(t, handle, c.ID)[h]; got.AWGRx != 7 || got.AWGTx != 9 {
		t.Fatalf("после повторной миграции: %+v", got)
	}
}
