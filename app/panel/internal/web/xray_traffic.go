package web

import (
	"context"
	"errors"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/xray"
)

// Опрос счётчиков Xray (amnezia-vpn-server-14y0.8). Живёт в serve, а не
// в отдельной службе: копить трафик в базе может только тот, кто в неё
// пишет, — панель.

// XrayTrafficInterval — как часто панель опрашивает Xray. Последняя
// активность VLESS точна до этого шага; чаще — лишние записи в базу без
// пользы для того, кто смотрит на минуты.
const XrayTrafficInterval = 10 * time.Second

// collectXrayTraffic — один опрос: счётчики из Xray, накопленное — в
// базу. Xray не настроен — опрашивать нечего, это не ошибка.
func (s *Server) collectXrayTraffic(ctx context.Context, api xray.StatsReader, now time.Time) error {
	if _, err := db.XraySettingsRow(s.db()); err != nil {
		if errors.Is(err, db.ErrXrayNotConfigured) {
			return nil
		}
		return err
	}
	started, counters, err := xray.ReadTraffic(ctx, api, now)
	if err != nil {
		return err
	}
	// Запись — под тем же замком, что изменения и восстановление из
	// копии: восстановление подменяет базу, и опрос не должен писать в
	// уже снятую.
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return db.RecordVLESSCounters(s.db(), started, counters, now)
}

// WatchXrayTraffic опрашивает Xray раз в every, пока ctx не отменён. В
// журнал — только смена состояния: Xray, который не запущен или ещё не
// перезапущен с включённой статистикой, не должен писать строку каждые
// десять секунд.
func (s *Server) WatchXrayTraffic(ctx context.Context, every time.Duration) {
	api := xray.NewAPI(xray.APIAddr())
	t := time.NewTicker(every)
	defer t.Stop()
	failing := false
	for {
		callCtx, cancel := context.WithTimeout(ctx, every)
		err := s.collectXrayTraffic(callCtx, api, time.Now().UTC())
		cancel()
		switch {
		case err != nil && !failing && ctx.Err() == nil:
			s.cfg.Logger.Printf("xray: traffic stats unavailable (restart xray if its config was just updated): %v", err)
			failing = true
		case err == nil && failing:
			s.cfg.Logger.Printf("xray: traffic stats available again")
			failing = false
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
