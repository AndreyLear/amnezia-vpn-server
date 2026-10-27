package xray

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// Байты запроса — как proto.Marshal(QueryStatsRequest{Pattern: "user>>>"})
// из app/stats/command/command.proto: reset=false в proto3 не пишется.
func TestQueryStatsEncoding(t *testing.T) {
	const want = "0a07757365723e3e3e"
	if got := hex.EncodeToString(queryStatsRequest(userStatsPattern)); got != want {
		t.Fatalf("QueryStatsRequest: got %s want %s", got, want)
	}
}

// stat — Stat{name, value} в проводке protobuf, собранный вручную, чтобы
// не проверять разбор тем же кодом, что и кодирование.
func stat(name string, value byte) []byte {
	b := []byte{0x0a, byte(len(name))}
	b = append(b, name...)
	return append(b, 0x10, value)
}

func statsResponse(stats ...[]byte) []byte {
	var b []byte
	for _, s := range stats {
		b = append(b, 0x0a, byte(len(s)))
		b = append(b, s...)
	}
	return b
}

func grpcOK(w http.ResponseWriter, msg []byte) {
	w.Header().Set("Content-Type", "application/grpc")
	w.Header().Set("Trailer", "Grpc-Status, Grpc-Message")
	w.WriteHeader(http.StatusOK)
	frame := []byte{0, 0, 0, 0, byte(len(msg))}
	w.Write(append(frame, msg...))
	w.Header().Set("Grpc-Status", "0")
}

// Счётчики пользователей разбираются по email и направлению; чужие имена
// (служебная учётка, входы, мусор) не мешают.
func TestUserTrafficOverAPI(t *testing.T) {
	var gotPath string
	var gotBody []byte
	addr := fakeGRPCAt(t, "", func(w http.ResponseWriter, r *http.Request, body []byte) {
		gotPath, gotBody = r.URL.Path, body
		grpcOK(w, statsResponse(
			stat("user>>>c1>>>traffic>>>uplink", 100),
			stat("user>>>c1>>>traffic>>>downlink", 120),
			stat("user>>>c10>>>traffic>>>downlink", 7),
			stat("user>>>ruentry>>>traffic>>>uplink", 9),
			stat("user>>>c2>>>online", 1),
		))
	})
	got, err := NewAPI(addr).UserTraffic(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != queryStatsPath || string(gotBody) != string(queryStatsRequest(userStatsPattern)) {
		t.Fatalf("запрос %s %x", gotPath, gotBody)
	}
	want := map[string]UserCounters{
		"c1":      {Up: 100, Down: 120},
		"c10":     {Down: 7},
		"ruentry": {Up: 9},
	}
	if a, b := mustJSON(got), mustJSON(want); a != b {
		t.Fatalf("счётчики %s, ждали %s", a, b)
	}
}

// Время работы Xray — поле 10 SysStatsResponse; остальные поля
// пропускаются.
func TestUptimeOverAPI(t *testing.T) {
	addr := fakeGRPCAt(t, sysStatsPath, func(w http.ResponseWriter, _ *http.Request, body []byte) {
		if len(body) != 0 {
			t.Errorf("SysStatsRequest не пустой: %x", body)
		}
		// NumGoroutine=42, Alloc=300, Uptime=3600.
		grpcOK(w, []byte{0x08, 42, 0x18, 0xac, 0x02, 0x50, 0x90, 0x1c})
	})
	got, err := NewAPI(addr).Uptime(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != time.Hour {
		t.Fatalf("аптайм %v", got)
	}
}

// Битый ответ — ошибка, а не нули: нули панель приняла бы за перезапуск
// Xray.
func TestStatsMalformedResponse(t *testing.T) {
	addr := fakeGRPCAt(t, "", func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		grpcOK(w, []byte{0x0a, 0x20, 0x01})
	})
	if _, err := NewAPI(addr).UserTraffic(context.Background()); err == nil {
		t.Fatal("битый QueryStatsResponse принят")
	}
	if _, err := NewAPI(addr).Uptime(context.Background()); err == nil {
		t.Fatal("битый SysStatsResponse принят")
	}
}

type fakeStats struct {
	traffic map[string]UserCounters
	uptime  time.Duration
	err     error
}

func (f *fakeStats) UserTraffic(context.Context) (map[string]UserCounters, error) {
	return f.traffic, f.err
}

func (f *fakeStats) Uptime(context.Context) (time.Duration, error) { return f.uptime, f.err }

// ReadTraffic оставляет только клиентов панели (c<номер>) и выдаёт время
// запуска Xray.
func TestReadTraffic(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	started, counters, err := ReadTraffic(context.Background(), &fakeStats{
		uptime: 90 * time.Second,
		traffic: map[string]UserCounters{
			"c1": {Up: 1, Down: 2}, "c22": {Down: 5}, "ruentry": {Up: 9}, "c": {Up: 1}, "cx": {Up: 1}, "c-3": {Up: 1},
		},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if !started.Equal(now.Add(-90 * time.Second)) {
		t.Fatalf("время запуска %v", started)
	}
	if len(counters) != 2 || counters[1].Up != 1 || counters[1].Down != 2 || counters[22].Down != 5 {
		t.Fatalf("счётчики %+v", counters)
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
