package web

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/status"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/xray"
)

// vlessSpeed — ряды скорости VLESS клиента из API (amnezia-vpn-server-14y0.9).
func vlessSpeed(t *testing.T, f *fixture, id int64, columns int) map[string]any {
	t.Helper()
	return decodeAPI(t, f.get(fmt.Sprintf("/api/clients/%d/speed?protocol=vless&window=10min&columns=%d", id, columns)))
}

func filledValues(series []any) []float64 {
	var out []float64
	for _, v := range series {
		if v != nil {
			out = append(out, v.(float64))
		}
	}
	return out
}

// Опрос Xray копит ряды скорости VLESS тем же журналом, что у AWG, и
// график клиента их показывает (amnezia-vpn-server-14y0.9).
func TestVLESSSpeedFromXrayCounters(t *testing.T) {
	f, _, c := vlessFixture(t)
	other, _, _ := f.addClient("mama")
	now := time.Now().UTC().Truncate(time.Second)
	// Замеры ниже — за секунды до «сейчас», а клиенты созданы только что:
	// история до создания клиента скрывается (см. следующий тест).
	if _, err := f.h.Exec(`UPDATE clients SET created_at = ?`, now.Add(-time.Hour).Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	email := fmt.Sprintf("c%d", c.ID)
	stats := &fakeXrayStats{uptime: time.Hour, traffic: map[string]xray.UserCounters{
		email:     {Up: 1000, Down: 10_000},
		"ruentry": {Up: 1 << 20, Down: 1 << 20},
	}}
	if err := f.server.collectXrayTraffic(context.Background(), stats, now.Add(-10*time.Second)); err != nil {
		t.Fatal(err)
	}
	// Пять секунд спустя: 6 250 000 байт к клиенту = 10 Мбит/с.
	stats.uptime += 5 * time.Second
	stats.traffic = map[string]xray.UserCounters{
		email:     {Up: 1000 + 625_000, Down: 10_000 + 6_250_000},
		"ruentry": {Up: 1 << 30, Down: 1 << 30},
	}
	if err := f.server.collectXrayTraffic(context.Background(), stats, now.Add(-5*time.Second)); err != nil {
		t.Fatal(err)
	}

	got := vlessSpeed(t, f, c.ID, 120)
	down := filledValues(got["down_max_bps"].([]any))
	if len(down) == 0 {
		t.Fatalf("у VLESS нет ни одного столбца скорости: %v", got)
	}
	for _, v := range down {
		if v != 10_000_000 {
			t.Fatalf("скорость к клиенту = %v бит/с, ожидалось 10 000 000", v)
		}
	}
	for _, v := range filledValues(got["up_max_bps"].([]any)) {
		if v != 1_000_000 {
			t.Fatalf("скорость от клиента = %v бит/с, ожидалось 1 000 000", v)
		}
	}
	// «На связи» по VLESS не определить: у XHTTP нет keepalive, и тишина
	// счётчика — это простой, а не обрыв. Признак не отдаётся вовсе.
	for i, v := range got["online"].([]any) {
		if v != nil {
			t.Fatalf("online[%d] = %v для VLESS, ожидался null", i, v)
		}
	}

	// Клиент без своего счётчика в Xray (ещё не подключался) — нули, а не
	// разрыв: Xray заводит счётчик при первом байте.
	idle := vlessSpeed(t, f, other.ID, 120)
	zeros := filledValues(idle["down_max_bps"].([]any))
	if len(zeros) == 0 {
		t.Fatalf("у клиента без счётчика разрыв вместо нулей: %v", idle)
	}
	for _, v := range zeros {
		if v != 0 {
			t.Fatalf("скорость клиента без счётчика = %v", v)
		}
	}

	// Служебная учётка входа в журнал не попадает.
	body, err := os.ReadFile(filepath.Join(filepath.Dir(f.server.cfg.DBPath), "vless-speed.log"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "ruentry") {
		t.Fatalf("трафик ruentry в журнале скорости VLESS:\n%s", body)
	}

	// AWG-график клиента про VLESS не знает.
	awg := decodeAPI(t, f.get(fmt.Sprintf("/api/clients/%d/speed?window=10min&columns=120", c.ID)))
	if n := len(filledValues(awg["down_max_bps"].([]any))); n != 0 {
		t.Fatalf("в график AWG попала скорость VLESS: %d столбцов", n)
	}
}

// Номер удалённого клиента достаётся новому (id без AUTOINCREMENT), а
// журнал ключуется номером: прежняя история не должна оказаться на
// графике нового клиента.
func TestVLESSSpeedHidesHistoryBeforeClientCreated(t *testing.T) {
	f, _, c := vlessFixture(t)
	created, err := time.Parse(time.RFC3339, c.CreatedAt)
	if err != nil {
		t.Fatal(err)
	}
	key := fmt.Sprintf("c%d", c.ID)
	before := created.Add(-5 * time.Minute)
	body := status.SpeedSchema + "\n" +
		fmt.Sprintf("%d %s:0:0:-\n", before.Unix(), key) +
		fmt.Sprintf("%d %s:625000:6250000:-\n", before.Add(5*time.Second).Unix(), key)
	path := filepath.Join(filepath.Dir(f.server.cfg.DBPath), "vless-speed.log")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got := vlessSpeed(t, f, c.ID, 120)
	if n := len(filledValues(got["down_max_bps"].([]any))); n != 0 {
		t.Fatalf("на графике нового клиента %d столбцов истории прежнего владельца номера", n)
	}
}

// Опрос без записи в базу (так часто, как пишет журнал скорости) копит
// только журнал: накопленный трафик в базе пишется реже.
func TestPollXrayTrafficWithoutRecordKeepsDBUntouched(t *testing.T) {
	f, _, c := vlessFixture(t)
	stats := &fakeXrayStats{uptime: time.Hour, traffic: map[string]xray.UserCounters{
		fmt.Sprintf("c%d", c.ID): {Up: 1, Down: 2},
	}}
	if err := f.server.pollXrayTraffic(context.Background(), stats, time.Now().UTC(), false); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := f.h.QueryRow(`SELECT COUNT(*) FROM vless_traffic`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("опрос без записи тронул базу: %d строк", n)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(f.server.cfg.DBPath), "vless-speed.log")); err != nil {
		t.Fatalf("журнал скорости VLESS не записан: %v", err)
	}
}
