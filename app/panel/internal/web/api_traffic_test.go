package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/status"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/xray"
)

type trafficTestJSON struct {
	FromUTC string `json:"from_utc"`
	ToUTC   string `json:"to_utc"`
	Hours   []struct {
		HourUTC   string `json:"hour_utc"`
		AWGDown   uint64 `json:"awg_down"`
		AWGUp     uint64 `json:"awg_up"`
		VLESSDown uint64 `json:"vless_down"`
		VLESSUp   uint64 `json:"vless_up"`
	} `json:"hours"`
}

func decodeTraffic(t *testing.T, f *fixture, path string) trafficTestJSON {
	t.Helper()
	rec := f.get(path)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: code %d body %s", path, rec.Code, rec.Body.String())
	}
	var out trafficTestJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func peerStatus(at time.Time, key string, rx, tx uint64) *status.Status {
	return &status.Status{
		Schema: status.SchemaVersion, GeneratedAt: at,
		Interface: &status.Interface{Iface: "awg0", HasInterface: true},
		Peers:     []status.Peer{{PublicKey: key, RxBytes: rx, TxBytes: tx}},
	}
}

// Трафик AWG по часам панель берёт из того же status.json, что даёт
// «Скачал/Отдал» в карточке, а API отдаёт часовые корзины UTC: в дни
// местного пояса их складывает браузер (amnezia-vpn-server-gzmb).
func TestAPIClientTrafficHourly(t *testing.T) {
	f, _, c := vlessFixture(t)
	now := time.Now().UTC()
	f.setStatus(peerStatus(now, c.PublicKey, 1000, 50_000))
	if err := f.server.recordAWGTraffic(now); err != nil {
		t.Fatal(err)
	}
	f.setStatus(peerStatus(now, c.PublicKey, 1300, 90_000))
	if err := f.server.recordAWGTraffic(now); err != nil {
		t.Fatal(err)
	}
	stats := &fakeXrayStats{uptime: time.Hour, traffic: map[string]xray.UserCounters{
		fmt.Sprintf("c%d", c.ID): {Up: 70, Down: 800},
	}}
	if err := f.server.collectXrayTraffic(context.Background(), stats, now); err != nil {
		t.Fatal(err)
	}

	from := now.Add(-30 * 24 * time.Hour)
	got := decodeTraffic(t, f, fmt.Sprintf("/api/clients/%d/traffic?from=%s", c.ID, url.QueryEscape(from.Format(time.RFC3339Nano))))
	if len(got.Hours) != 1 {
		t.Fatalf("корзины: %+v", got)
	}
	h := got.Hours[0]
	if h.HourUTC != now.Truncate(time.Hour).Format(time.RFC3339) {
		t.Fatalf("час корзины %q, нужно начало часа UTC", h.HourUTC)
	}
	// Названия — от лица клиента, как у графика скорости: down — к
	// клиенту (tx в status.json), up — от него.
	if h.AWGDown != 40_000 || h.AWGUp != 300 || h.VLESSDown != 800 || h.VLESSUp != 70 {
		t.Fatalf("корзина: %+v", h)
	}
	if got.FromUTC != from.Truncate(time.Hour).Format(time.RFC3339) {
		t.Fatalf("from_utc = %q", got.FromUTC)
	}
}

// Период ограничен сроком хранения, без from — тридцать дней; чужой
// номер — «нет такого», как у графика скорости.
func TestAPIClientTrafficPeriodBounds(t *testing.T) {
	f := newFixture(t)
	c, _, _ := f.addClient("router")
	now := time.Now().UTC()

	def := decodeTraffic(t, f, fmt.Sprintf("/api/clients/%d/traffic", c.ID))
	assertHoursAgo(t, "без from", def.FromUTC, now, 30*24)

	far := decodeTraffic(t, f, fmt.Sprintf("/api/clients/%d/traffic?from=2000-01-01T00:00:00Z", c.ID))
	assertHoursAgo(t, "from за пределом хранения", far.FromUTC, now, 366*24)

	bad := decodeTraffic(t, f, fmt.Sprintf("/api/clients/%d/traffic?from=yesterday", c.ID))
	assertHoursAgo(t, "непонятный from", bad.FromUTC, now, 30*24)
	if bad.Hours == nil {
		t.Fatal("пустая история должна быть пустым списком, а не null")
	}

	if rec := f.get("/api/clients/9999/traffic"); rec.Code != http.StatusNotFound {
		t.Fatalf("чужой номер: %d", rec.Code)
	}
}

func assertHoursAgo(t *testing.T, what, fromUTC string, now time.Time, hours int) {
	t.Helper()
	got, err := time.Parse(time.RFC3339, fromUTC)
	if err != nil {
		t.Fatalf("%s: from_utc %q: %v", what, fromUTC, err)
	}
	want := now.Truncate(time.Hour).Add(-time.Duration(hours) * time.Hour)
	if d := got.Sub(want); d < -time.Hour || d > time.Hour {
		t.Fatalf("%s: from_utc %s, нужно около %s", what, got, want)
	}
}

// Без входа — 401, как у остального /api.
func TestAPIClientTrafficRequiresAuth(t *testing.T) {
	f := newFixture(t)
	c, _, _ := f.addClient("router")
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/clients/%d/traffic", c.ID), nil)
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, req)
	assertAPIUnauthorized(t, rec)
}

// status.json нет (awg не запущен) — это не ошибка опроса: писать нечего.
func TestRecordAWGTrafficWithoutStatus(t *testing.T) {
	f := newFixture(t)
	if err := f.server.recordAWGTraffic(time.Now()); err != nil {
		t.Fatalf("без status.json: %v", err)
	}
}

// Трафик AWG пишет тот же цикл, что опрашивает Xray: отдельного фонового
// процесса под историю нет, и Xray для неё не нужен (amnezia-vpn-server-gzmb).
func TestWatchTrafficRecordsAWG(t *testing.T) {
	f := newFixture(t)
	c, _, _ := f.addClient("router")
	now := time.Now().UTC()
	if err := db.RecordAWGCounters(f.h, map[string]db.AWGCounters{c.PublicKey: {Rx: 10, Tx: 100}}, now); err != nil {
		t.Fatal(err)
	}
	f.setStatus(peerStatus(now, c.PublicKey, 15, 600))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.server.WatchTraffic(ctx, 20*time.Millisecond)
	}()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		hours, err := db.ClientTrafficHours(f.h, c.ID, now.Add(-time.Hour), now.Add(2*time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		if len(hours) == 1 && hours[0].AWGRx == 5 && hours[0].AWGTx == 500 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("цикл опроса не записал трафик AWG")
}
