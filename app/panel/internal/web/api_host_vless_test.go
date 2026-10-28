package web

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

// Состояние Xray в шапке панели рядом с туннелем AWG
// (amnezia-vpn-server-45ds): «работает» — Xray ответил на последний опрос
// счётчиков, «не работает» — не ответил, «none» — VLESS на сервере не
// настроен и шапке показывать нечего.
func hostVLESSState(t *testing.T, f *fixture) any {
	t.Helper()
	rec := f.get("/api/stats/host")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/stats/host code = %d; body=%s", rec.Code, rec.Body.String())
	}
	got := decodeHostStatsMap(t, rec)
	v, ok := got["vless_state"]
	if !ok {
		t.Fatalf("нет vless_state: %v", got)
	}
	return v
}

func TestHostStatsVLESSNotConfigured(t *testing.T) {
	f := newFixture(t)
	if got := hostVLESSState(t, f); got != "none" {
		t.Fatalf("без Xray vless_state = %v, want none", got)
	}
	// Опрос без Xray ничего не меняет: шапке по-прежнему нечего показывать.
	if err := f.server.collectXrayTraffic(context.Background(), &fakeXrayStats{err: errors.New("must not be called")}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := hostVLESSState(t, f); got != "none" {
		t.Fatalf("после опроса без Xray vless_state = %v, want none", got)
	}
}

func TestHostStatsVLESSFollowsPoll(t *testing.T) {
	f, _, _ := vlessFixture(t)
	// Настроен, но опроса ещё не было — состояние неизвестно, как у
	// туннеля без status.json.
	if got := hostVLESSState(t, f); got != "na" {
		t.Fatalf("до первого опроса vless_state = %v, want na", got)
	}

	ok := &fakeXrayStats{uptime: time.Hour}
	if err := f.server.collectXrayTraffic(context.Background(), ok, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if got := hostVLESSState(t, f); got != "up" {
		t.Fatalf("Xray ответил, vless_state = %v, want up", got)
	}

	down := &fakeXrayStats{err: errors.New("xray down")}
	if err := f.server.collectXrayTraffic(context.Background(), down, time.Now().UTC()); err == nil {
		t.Fatal("ошибка API проглочена")
	}
	if got := hostVLESSState(t, f); got != "down" {
		t.Fatalf("Xray не ответил, vless_state = %v, want down", got)
	}

	if err := f.server.collectXrayTraffic(context.Background(), ok, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if got := hostVLESSState(t, f); got != "up" {
		t.Fatalf("Xray снова ответил, vless_state = %v, want up", got)
	}
}
