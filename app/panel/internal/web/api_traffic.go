package web

import (
	"errors"
	"io/fs"
	"net/http"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/status"
)

// Трафик клиента по дням (amnezia-vpn-server-gzmb).
//
// Сервер отдаёт часовые корзины UTC, а в дни их складывает браузер: день
// начинается в местную полночь того, кто смотрит, а пояса браузера сервер
// не знает. Корзин за тридцать дней не больше 720 (час без трафика строки
// не имеет), так что сворачивать их здесь, как журнал скорости, незачем.

// trafficDefaultSpan — период, если from не сказали: тридцать дней, как
// на графике.
const trafficDefaultSpan = 30 * 24 * time.Hour

// trafficMaxSpan — дальше хранения смотреть нечего; лишние сутки — на
// местную полночь, которая может лежать до полуночи UTC.
const trafficMaxSpan = db.ClientTrafficRetention + 24*time.Hour

// trafficJSON — корзины по возрастанию часа. Названия от лица клиента,
// как у графика скорости: down — к клиенту, up — от него.
type trafficJSON struct {
	FromUTC string            `json:"from_utc"`
	ToUTC   string            `json:"to_utc"`
	Hours   []trafficHourJSON `json:"hours"`
}

type trafficHourJSON struct {
	HourUTC   string `json:"hour_utc"`
	AWGDown   uint64 `json:"awg_down"`
	AWGUp     uint64 `json:"awg_up"`
	VLESSDown uint64 `json:"vless_down"`
	VLESSUp   uint64 `json:"vless_up"`
}

func (s *Server) apiClientTraffic(w http.ResponseWriter, r *http.Request) {
	id, err := parseClientID(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "message": flashNotFound})
		return
	}
	if _, err := db.ClientByID(s.db(), id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "message": flashNotFound})
		return
	}
	now := time.Now().UTC()
	// Корзина текущего часа ещё копится — она входит в ответ целиком.
	to := now.Truncate(time.Hour).Add(time.Hour)
	from := now.Add(-trafficDefaultSpan)
	if raw := r.URL.Query().Get("from"); raw != "" {
		if at, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			from = at
		}
	}
	if from.Before(now.Add(-trafficMaxSpan)) {
		from = now.Add(-trafficMaxSpan)
	}
	if from.After(now) {
		from = now
	}
	// Корзина начинается в начале часа: час, в котором лежит from,
	// нужен целиком.
	from = from.UTC().Truncate(time.Hour)
	hours, err := db.ClientTrafficHours(s.db(), id, from, to)
	if err != nil {
		internalFailure(w, r, s, "api client traffic", err)
		return
	}
	out := trafficJSON{
		FromUTC: from.Format(time.RFC3339),
		ToUTC:   to.Format(time.RFC3339),
		Hours:   make([]trafficHourJSON, 0, len(hours)),
	}
	for _, h := range hours {
		out.Hours = append(out.Hours, trafficHourJSON{
			HourUTC:   h.Hour.Format(time.RFC3339),
			AWGDown:   h.AWGTx,
			AWGUp:     h.AWGRx,
			VLESSDown: h.VLESSTx,
			VLESSUp:   h.VLESSRx,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// recordAWGTraffic переносит в историю по часам один снимок status.json
// (amnezia-vpn-server-gzmb). Его пишет контейнер awg, панель только
// читает — и копить историю может только она, потому что пишет в базу
// только она. Нет файла — awg не запущен, писать нечего, но уборку
// старой истории это не отменяет.
func (s *Server) recordAWGTraffic(now time.Time) error {
	st, err := status.ReadStatus(s.cfg.StatusPath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	peers := map[string]db.AWGCounters{}
	if st != nil {
		for _, p := range st.Peers {
			peers[p.PublicKey] = db.AWGCounters{Rx: p.RxBytes, Tx: p.TxBytes}
		}
	}
	// Под тем же замком, что изменения и восстановление из копии: см.
	// pollXrayTraffic.
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return db.RecordAWGCounters(s.db(), peers, now)
}
