package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/status"
)

// writeSpeedLog lays down a history file next to status.json, the way the
// awg container does (amnezia-vpn-server-aa9u).
func writeSpeedLog(t *testing.T, f *fixture, lines ...string) {
	t.Helper()
	body := status.SpeedSchema + "\n" + strings.Join(lines, "\n") + "\n"
	writeStatusFile(t, f, "speed.log", body)
}

func speedLogLine(at time.Time, key string, rx, tx uint64) string {
	return fmt.Sprintf("%d %s:%d:%d", at.UTC().Unix(), status.SpeedKey(key), rx, tx)
}

// The panel folds the history for the SPA; nulls are gaps, not zeroes.
func TestAPIClientSpeedFoldsHistory(t *testing.T) {
	f := newFixture(t)
	c, _, _ := f.addClient("router")
	now := time.Now().UTC().Truncate(time.Second)
	// 6 250 000 bytes in five seconds = 10 Mbit/s towards the client.
	writeSpeedLog(t, f,
		speedLogLine(now.Add(-20*time.Second), c.PublicKey, 0, 0),
		speedLogLine(now.Add(-15*time.Second), c.PublicKey, 625_000, 6_250_000),
	)

	got := decodeAPI(t, f.get(fmt.Sprintf("/api/clients/%d/speed?window=10min&columns=60", c.ID)))
	if got["window"] != "10min" {
		t.Fatalf("window = %v", got["window"])
	}
	down, _ := got["down_max_bps"].([]any)
	if len(down) != 60 {
		t.Fatalf("столбцов = %d, ожидалось 60", len(down))
	}
	var seen float64
	filled := 0
	for _, v := range down {
		if v == nil {
			continue
		}
		filled++
		seen, _ = v.(float64)
	}
	if filled != 1 {
		t.Fatalf("заполненных столбцов = %d, ожидался ровно один: %v", filled, down)
	}
	if seen != 10_000_000 {
		t.Errorf("скорость = %v бит/с, ожидалось 10 000 000", seen)
	}
	up, _ := got["up_max_bps"].([]any)
	for _, v := range up {
		if v == nil {
			continue
		}
		if v.(float64) != 1_000_000 {
			t.Errorf("отдача = %v бит/с, ожидалось 1 000 000", v)
		}
	}
}

// Разрыв обязан приходить как null. Ноль означает «клиент ничего не
// получал» и является диагнозом; молчание диагнозом не является.
func TestAPIClientSpeedSendsGapsAsNull(t *testing.T) {
	f := newFixture(t)
	c, _, _ := f.addClient("router")
	writeSpeedLog(t, f, speedLogLine(time.Now().UTC().Add(-30*time.Minute), c.PublicKey, 0, 0))

	rec := f.get(fmt.Sprintf("/api/clients/%d/speed?columns=10", c.ID))
	if !strings.Contains(rec.Body.String(), "null") {
		t.Fatalf("в ответе нет null: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), ",0,") {
		t.Errorf("разрыв пришёл нулём: %s", rec.Body.String())
	}
}

// Сервер, который ещё ничего не записал, — обычное состояние сразу после
// обновления, а не отказ.
func TestAPIClientSpeedWithoutHistory(t *testing.T) {
	f := newFixture(t)
	c, _, _ := f.addClient("router")

	rec := f.get(fmt.Sprintf("/api/clients/%d/speed", c.ID))
	if rec.Code != http.StatusOK {
		t.Fatalf("код %d, ожидался 200: %s", rec.Code, rec.Body.String())
	}
	got := decodeAPI(t, rec)
	down, _ := got["down_max_bps"].([]any)
	if len(down) == 0 {
		t.Fatalf("столбцов нет вовсе: %v", got)
	}
	for _, v := range down {
		if v != nil {
			t.Fatalf("панель выдумала данные при пустой истории: %v", down)
		}
	}
}

// Сутки берутся из двух файлов; короткое окно — только из текущего.
func TestAPIClientSpeedDayWindow(t *testing.T) {
	f := newFixture(t)
	c, _, _ := f.addClient("router")
	now := time.Now().UTC().Truncate(time.Second)
	old := now.Add(-5 * time.Hour)
	writeStatusFile(t, f, "speed.prev.log", status.SpeedSchema+"\n"+
		speedLogLine(old, c.PublicKey, 0, 0)+"\n"+
		speedLogLine(old.Add(5*time.Second), c.PublicKey, 0, 6_250_000)+"\n")
	writeSpeedLog(t, f, speedLogLine(now.Add(-time.Minute), c.PublicKey, 0, 6_250_000))

	day := decodeAPI(t, f.get(fmt.Sprintf("/api/clients/%d/speed?window=day&columns=24", c.ID)))
	filled := 0
	for _, v := range day["down_max_bps"].([]any) {
		if v != nil {
			filled++
		}
	}
	if filled == 0 {
		t.Fatalf("сутки не достали до вчерашнего файла: %v", day["down_max_bps"])
	}

	short := decodeAPI(t, f.get(fmt.Sprintf("/api/clients/%d/speed?window=10min&columns=24", c.ID)))
	for _, v := range short["down_max_bps"].([]any) {
		if v != nil {
			t.Fatalf("короткое окно дотянулось до данных пятичасовой давности: %v", short["down_max_bps"])
		}
	}
}

// Короткое окно — ровно десять минут (amnezia-vpn-server-teos). Час, что
// стоял здесь раньше, вмещал 720 замеров, и на любой разумной ширине
// графика они сворачивались по несколько в столбец: мелочь, ради которой
// график открывают, усреднялась. Десять минут при такте записи в пять
// секунд — это 120 замеров, то есть замер на столбец и никакой свёртки.
func TestAPIClientSpeed10MinWindowSpansTenMinutes(t *testing.T) {
	f := newFixture(t)
	c, _, _ := f.addClient("router")
	now := time.Now().UTC().Truncate(time.Second)
	// Замеры двадцатиминутной давности за окно уже не попадают, а
	// свежие — попадают.
	writeSpeedLog(t, f,
		speedLogLine(now.Add(-20*time.Minute), c.PublicKey, 0, 0),
		speedLogLine(now.Add(-20*time.Minute+5*time.Second), c.PublicKey, 625_000, 6_250_000),
		speedLogLine(now.Add(-15*time.Second), c.PublicKey, 0, 0),
		speedLogLine(now.Add(-10*time.Second), c.PublicKey, 625_000, 6_250_000),
	)

	got := decodeAPI(t, f.get(fmt.Sprintf("/api/clients/%d/speed?window=10min&columns=120", c.ID)))
	from, err := time.Parse(time.RFC3339, got["from_utc"].(string))
	if err != nil {
		t.Fatalf("from_utc = %v: %v", got["from_utc"], err)
	}
	to, err := time.Parse(time.RFC3339, got["to_utc"].(string))
	if err != nil {
		t.Fatalf("to_utc = %v: %v", got["to_utc"], err)
	}
	if span := to.Sub(from); span != 10*time.Minute {
		t.Fatalf("окно = %v, ожидалось 10m0s", span)
	}
	// И оно действительно обрезает историю по этой границе, а не только
	// подписывает края: двадцатиминутной давности в ответе быть не может.
	down := got["down_max_bps"].([]any)
	filled := 0
	for _, v := range down {
		if v != nil {
			filled++
		}
	}
	if filled != 1 {
		t.Fatalf("заполненных столбцов = %d, ожидался ровно один свежий: %v", filled, down)
	}
}

// Чужой клиент и сломанный id отвечают одинаково коротко.
func TestAPIClientSpeedUnknownClient(t *testing.T) {
	f := newFixture(t)
	for _, path := range []string{"/api/clients/999/speed", "/api/clients/abc/speed"} {
		rec := f.get(path)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: код %d, ожидался 404", path, rec.Code)
		}
	}
}

// Маршрут закрыт сессией, как и всё остальное в /api.
func TestAPIClientSpeedRequiresSession(t *testing.T) {
	f := newFixture(t)
	c, _, _ := f.addClient("router")
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/clients/%d/speed", c.ID), nil)
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatalf("ответ без сессии: %d %s", rec.Code, rec.Body.String())
	}
}

// Ключей в ответе быть не может ни при каких данных: в истории лежит
// только укороченный публичный ключ, и наружу не уходит даже он.
func TestAPIClientSpeedLeaksNoKeys(t *testing.T) {
	f := newFixture(t)
	c, priv, psk := f.addClient("router")
	now := time.Now().UTC()
	writeSpeedLog(t, f,
		speedLogLine(now.Add(-10*time.Second), c.PublicKey, 0, 0),
		speedLogLine(now.Add(-5*time.Second), c.PublicKey, 1, 2),
	)

	body := f.get(fmt.Sprintf("/api/clients/%d/speed", c.ID)).Body.String()
	for name, secret := range map[string]string{"приватный": priv, "общий": psk, "публичный": c.PublicKey} {
		if strings.Contains(body, secret) {
			t.Errorf("%s ключ утёк в ответ", name)
		}
	}
	if strings.Contains(body, status.SpeedKey(c.PublicKey)) {
		t.Errorf("укороченный ключ утёк в ответ")
	}
}

// Ширину просит SPA, но потолок ставит сервер: считает-то он.
func TestAPIClientSpeedCapsColumns(t *testing.T) {
	f := newFixture(t)
	c, _, _ := f.addClient("router")
	got := decodeAPI(t, f.get(fmt.Sprintf("/api/clients/%d/speed?columns=100000", c.ID)))
	if n := len(got["down_max_bps"].([]any)); n != 2000 {
		t.Fatalf("столбцов = %d, ожидался потолок 2000", n)
	}
}

func TestSpeedLogFileName(t *testing.T) {
	f := newFixture(t)
	want := filepath.Join(filepath.Dir(f.statusPath), "speed.log")
	if got := filepath.Join(f.server.statusDir(), "speed.log"); got != want {
		t.Fatalf("путь = %q, ожидался %q", got, want)
	}
}
