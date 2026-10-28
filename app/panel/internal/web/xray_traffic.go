package web

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/status"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/xray"
)

// Опрос счётчиков Xray (amnezia-vpn-server-14y0.8). Живёт в serve, а не
// в отдельной службе: копить трафик в базе может только тот, кто в неё
// пишет, — панель.

// XrayTrafficInterval — как часто накопленный трафик VLESS пишется в
// базу. Последняя активность VLESS точна до этого шага; чаще — лишние
// записи в базу без пользы для того, кто смотрит на минуты.
const XrayTrafficInterval = 10 * time.Second

// XraySpeedInterval — как часто панель опрашивает Xray и пишет замер в
// журнал скорости VLESS (amnezia-vpn-server-14y0.9). Шаг тот же, что у
// журнала AWG (пять секунд): десятиминутное окно графика рассчитано ровно
// на 120 таких замеров, по одному на столбец.
const XraySpeedInterval = 5 * time.Second

// Ответ Xray на последний опрос — для шапки панели
// (amnezia-vpn-server-45ds). Отдельной проверки нет: «Xray отвечает на API»
// и есть «VLESS работает», а опрос и так идёт каждые XraySpeedInterval.
const (
	xrayAPIUnknown int32 = iota // опроса ещё не было или Xray не настроен
	xrayAPIUp
	xrayAPIDown
)

// collectXrayTraffic — один опрос с записью в базу.
func (s *Server) collectXrayTraffic(ctx context.Context, api xray.StatsReader, now time.Time) error {
	return s.pollXrayTraffic(ctx, api, now, true)
}

// pollXrayTraffic — один опрос: счётчики из Xray — в журнал скорости
// VLESS, а при record ещё и накопленное — в базу. Xray не настроен —
// опрашивать нечего, это не ошибка.
func (s *Server) pollXrayTraffic(ctx context.Context, api xray.StatsReader, now time.Time, record bool) error {
	if _, err := db.XraySettingsRow(s.db()); err != nil {
		if errors.Is(err, db.ErrXrayNotConfigured) {
			s.xrayAPI.Store(xrayAPIUnknown)
			return nil
		}
		return err
	}
	started, counters, err := xray.ReadTraffic(ctx, api, now)
	if err != nil {
		s.xrayAPI.Store(xrayAPIDown)
		return err
	}
	s.xrayAPI.Store(xrayAPIUp)
	// Запись — под тем же замком, что изменения и восстановление из
	// копии: восстановление подменяет базу, и опрос не должен писать в
	// уже снятую.
	s.mutex.Lock()
	defer s.mutex.Unlock()
	var recErr error
	if record {
		recErr = db.RecordVLESSCounters(s.db(), started, counters, now)
	}
	return errors.Join(recErr, s.appendVLESSSpeed(counters, now))
}

// vlessSpeedKey — ключ клиента в журнале скорости VLESS: тот же c<id>, что
// email в Xray. Публичного ключа, как у AWG, у VLESS нет, а UUID — это
// пароль подключения, ему в журнале не место.
func vlessSpeedKey(id int64) string {
	return "c" + strconv.FormatInt(id, 10)
}

// appendVLESSSpeed пишет один замер журнала скорости VLESS тем же
// форматом, ротацией и пределами объёма, что журнал AWG
// (status.AppendSample): rx — от клиента (uplink), tx — к нему (downlink).
//
// В замер попадают ровно клиенты панели с VLESS. Служебные учётки (ruentry)
// отброшены ещё в ReadTraffic: трафик через вход приходит одной учёткой, и
// по клиентам его не разложить. Клиент, которого у Xray нет в счётчиках,
// пишется нулями: Xray заводит счётчик пользователя только на первом его
// байте, и «не было ни байта» — это ноль, а не «мы не смотрели». Счётчики
// удалённых клиентов (Xray держит их до перезапуска) не пишутся.
func (s *Server) appendVLESSSpeed(counters map[int64]db.VLESSCounters, now time.Time) error {
	users, err := db.VLESSUsersExport(s.db())
	if err != nil {
		return err
	}
	st := &status.Status{GeneratedAt: now.UTC()}
	for _, u := range users {
		c := counters[u.ClientID]
		st.Peers = append(st.Peers, status.Peer{
			PublicKey: vlessSpeedKey(u.ClientID),
			RxBytes:   c.Up,
			TxBytes:   c.Down,
		})
	}
	sort.Slice(st.Peers, func(i, j int) bool { return st.Peers[i].PublicKey < st.Peers[j].PublicKey })
	return status.AppendSample(s.vlessSpeedLog(), st)
}

// WatchXrayTraffic опрашивает Xray раз в every, пока ctx не отменён; в базу
// пишет не чаще XrayTrafficInterval. В журнал — только смена состояния:
// Xray, который не запущен или ещё не перезапущен с включённой
// статистикой, не должен писать строку на каждом опросе.
func (s *Server) WatchXrayTraffic(ctx context.Context, every time.Duration) {
	api := xray.NewAPI(xray.APIAddr())
	t := time.NewTicker(every)
	defer t.Stop()
	failing := false
	var recorded time.Time
	for {
		now := time.Now().UTC()
		record := now.Sub(recorded) >= XrayTrafficInterval-every/2 || now.Before(recorded)
		callCtx, cancel := context.WithTimeout(ctx, every)
		err := s.pollXrayTraffic(callCtx, api, now, record)
		cancel()
		if err == nil && record {
			recorded = now
		}
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
