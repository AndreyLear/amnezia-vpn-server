package web

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/xray"
)

// Конфигурация узла-входа (amnezia-vpn-server-14y0.6, см.
// internal/xray/entry_sync.go).
//
// GET /api/entry/config — единственный путь панели, открытый без входа в
// неё: узел-вход раз в минуту забирает по нему список клиентов и дорогу до
// этого сервера. Пускает только секрет узла в заголовке Authorization:
// Bearer — не в адресе, потому что адреса оседают в журналах nginx и
// прокси. Секрет не попадает ни в журнал, ни в ответ.
//
// Лимиты на адрес, как у входа в панель: всех запросов — с запасом на
// синхронизацию раз в минуту и ручной перезапуск, неудачных — столько, что
// подбор упирается в стену задолго до того, как стал бы осмысленным.
const (
	entryRequestLimit  = 30
	entryRequestWindow = time.Minute
	entryFailLimit     = 10
	entryFailWindow    = 15 * time.Minute
)

// entryBearer — секрет из «Authorization: Bearer <секрет>»; пусто — такого
// заголовка нет.
func entryBearer(r *http.Request) string {
	if token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		return token
	}
	return ""
}

func (s *Server) apiEntryConfig(w http.ResponseWriter, r *http.Request) {
	key := requestLoginKey(r)
	now := time.Now()
	if sec, ok := s.entryRequests.reserve(key, now); !ok {
		setRetryAfter(w, sec)
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"ok": false, "message": "Слишком много запросов."})
		return
	}
	// Попытка занимается до проверки и возвращается при успехе — как у
	// входа в панель: параллельные запросы не проскочат лимит.
	if sec, ok := s.entryFails.reserve(key, now); !ok {
		setRetryAfter(w, sec)
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"ok": false, "message": "Слишком много запросов."})
		return
	}
	name, err := xray.AuthenticateEntry(s.db(), entryBearer(r))
	if errors.Is(err, xray.ErrEntryTokenInvalid) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		writeJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "message": "Нет доступа."})
		return
	}
	s.entryFails.release(key)
	if err != nil {
		internalFailure(w, r, s, "api entry config", err)
		return
	}
	cfg, err := xray.BuildEntryConfig(s.db(), name)
	switch {
	case errors.Is(err, db.ErrXrayNotConfigured), errors.Is(err, xray.ErrNoLinkAddress):
		// Дороги на этот сервер нет: узел остаётся на прежнем конфиге.
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "message": "Xray на сервере не настроен."})
		return
	case err != nil:
		internalFailure(w, r, s, "api entry config", err)
		return
	}
	// Время синка — по успешному ответу, а не по одной проверке секрета:
	// при 503 узел конфига не получил и остался на прежнем
	// (amnezia-vpn-server-14y0.16). Сбой записи узлу не мешает — это
	// только подпись в окне «О версиях».
	if err := db.TouchXrayEntrySync(s.db(), name, now); err != nil {
		s.cfg.Logger.Printf("api entry config: %v", err)
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, cfg)
}
