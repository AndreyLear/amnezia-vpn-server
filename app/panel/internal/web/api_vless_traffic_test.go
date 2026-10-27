package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/xray"
)

type fakeXrayStats struct {
	traffic map[string]xray.UserCounters
	uptime  time.Duration
	err     error
}

func (f *fakeXrayStats) UserTraffic(context.Context) (map[string]xray.UserCounters, error) {
	return f.traffic, f.err
}

func (f *fakeXrayStats) Uptime(context.Context) (time.Duration, error) { return f.uptime, f.err }

// Трафик VLESS идёт в карточку клиента отдельно от AWG: опрос Xray копится
// в базе, список и карточка отдают накопленное и последнюю активность
// (amnezia-vpn-server-14y0.8).
func TestClientJSONCarriesVLESSTraffic(t *testing.T) {
	f, _, c := vlessFixture(t)
	other, _, _ := f.addClient("mama")
	stats := &fakeXrayStats{uptime: time.Hour, traffic: map[string]xray.UserCounters{
		fmt.Sprintf("c%d", c.ID): {Up: 300, Down: 4000},
		"ruentry":                {Up: 1 << 30, Down: 1 << 30},
	}}
	now := time.Now().UTC()
	if err := f.server.collectXrayTraffic(context.Background(), stats, now); err != nil {
		t.Fatal(err)
	}

	var list []clientJSON
	rec := f.get("/api/clients")
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	byID := map[int64]clientJSON{}
	for _, it := range list {
		byID[it.ID] = it
	}
	got := byID[c.ID]
	if got.VLESSUpBytes != 300 || got.VLESSDownBytes != 4000 || got.VLESSLastActiveUTC == nil ||
		!got.VLESSLastActiveUTC.Equal(now) || !got.VLESSOnline {
		t.Fatalf("клиент с трафиком VLESS: %+v", got)
	}
	if o := byID[other.ID]; o.VLESSUpBytes != 0 || o.VLESSLastActiveUTC != nil || o.VLESSOnline {
		t.Fatalf("клиенту без VLESS приписан трафик: %+v", o)
	}
	// Трафик служебной учётки входа никому из клиентов не достаётся.
	for _, it := range list {
		if it.VLESSUpBytes >= 1<<30 {
			t.Fatalf("трафик ruentry приписан клиенту %d", it.ID)
		}
	}

	var one clientJSON
	rec = f.get(fmt.Sprintf("/api/clients/%d", c.ID))
	if err := json.Unmarshal(rec.Body.Bytes(), &one); err != nil {
		t.Fatal(err)
	}
	if one.VLESSDownBytes != 4000 {
		t.Fatalf("карточка клиента: %+v", one)
	}
	if !strings.Contains(rec.Body.String(), `"vless_last_active_utc"`) {
		t.Fatalf("в ответе нет vless_last_active_utc: %s", rec.Body.String())
	}

	// Активность час назад — не «на связи»: счётчик с тех пор не
	// двигался.
	stale := &fakeXrayStats{uptime: 2 * time.Hour, traffic: map[string]xray.UserCounters{
		fmt.Sprintf("c%d", other.ID): {Up: 1},
	}}
	if err := f.server.collectXrayTraffic(context.Background(), stale, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	stale.uptime += time.Hour // тот же запуск Xray, час спустя
	if err := f.server.collectXrayTraffic(context.Background(), stale, now); err != nil {
		t.Fatal(err)
	}
	rec = f.get(fmt.Sprintf("/api/clients/%d", other.ID))
	one = clientJSON{}
	if err := json.Unmarshal(rec.Body.Bytes(), &one); err != nil {
		t.Fatal(err)
	}
	if one.VLESSOnline || one.VLESSLastActiveUTC == nil || one.VLESSUpBytes != 1 {
		t.Fatalf("клиент без движения час — на связи: %+v", one)
	}
}

// Xray не настроен — опрашивать нечего, и это не ошибка.
func TestCollectXrayTrafficWithoutXray(t *testing.T) {
	f := newFixture(t)
	stats := &fakeXrayStats{err: errors.New("must not be called")}
	if err := f.server.collectXrayTraffic(context.Background(), stats, time.Now()); err != nil {
		t.Fatalf("без Xray: %v", err)
	}
	if code := f.get("/api/clients").Code; code != http.StatusOK {
		t.Fatalf("список: %d", code)
	}
}

// Xray недоступен — ошибка опроса, база не тронута.
func TestCollectXrayTrafficAPIError(t *testing.T) {
	f, _, c := vlessFixture(t)
	stats := &fakeXrayStats{err: errors.New("xray down")}
	if err := f.server.collectXrayTraffic(context.Background(), stats, time.Now()); err == nil {
		t.Fatal("ошибка API проглочена")
	}
	all, err := db.VLESSTrafficAll(f.h)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := all[c.ID]; ok {
		t.Fatalf("после неудачного опроса в базе строка: %+v", all)
	}
}
