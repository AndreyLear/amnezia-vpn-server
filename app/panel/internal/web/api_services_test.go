package web

import (
	"net/http"
	"testing"
)

// Отказ, который чинится сам, невидим, пока о нём негде прочитать
// (amnezia-vpn-server-eq82).
func TestAPIServicesReportsWhatTheWatchdogSaw(t *testing.T) {
	f := newFixture(t)
	writeStatusFile(t, f, "services.json", `{
		"schema":"v1","checked_at_utc":"2026-09-07T12:00:00Z",
		"services":[
			{"name":"dns","state":"ok","reason":"","fails":0,
			 "restarted_at_utc":"2026-09-07T11:40:00Z","restart_reason":"не отвечает на 10.8.0.1"},
			{"name":"awg","state":"ok","reason":"","fails":0,
			 "restarted_at_utc":"","restart_reason":""}
		]}`)

	got := decodeAPI(t, f.get("/api/services"))
	if got["checked_at_utc"] != "2026-09-07T12:00:00Z" {
		t.Fatalf("время проверки не дошло: %v", got)
	}
	list, _ := got["services"].([]any)
	if len(list) != 2 {
		t.Fatalf("служб = %d, ожидалось 2: %v", len(list), got)
	}
	dns, _ := list[0].(map[string]any)
	// Сейчас всё исправно, но след перезапуска обязан остаться: иначе
	// владелец так и не узнает, что сервер сам себя чинил.
	if dns["state"] != "ok" {
		t.Fatalf("резолвер = %v, ожидалось ok", dns["state"])
	}
	if dns["restart_reason"] != "не отвечает на 10.8.0.1" {
		t.Fatalf("причина перезапуска потеряна: %v", dns)
	}
}

// Сервер без сторожа не должен выглядеть как сервер со сломанными службами.
func TestAPIServicesSaysWhetherTheWatchdogIsThere(t *testing.T) {
	f := newFixture(t)
	writeStatusFile(t, f, "deployment.json", `{"schema":"v1","watchdog":false}`)

	got := decodeAPI(t, f.get("/api/services"))
	if got["watchdog"] != false {
		t.Fatalf("watchdog = %v, ожидалось false", got["watchdog"])
	}
	if got["checked_at_utc"] != "" {
		t.Fatalf("панель выдумала проверку, которой не было: %v", got)
	}
}

// Первая минута после установки: сторож ещё не отработал, и это не отказ.
func TestAPIServicesSurvivesABrandNewServer(t *testing.T) {
	f := newFixture(t)
	rec := f.get("/api/services")
	if rec.Code != http.StatusOK {
		t.Fatalf("код %d, ожидался 200", rec.Code)
	}
	got := decodeAPI(t, rec)
	list, ok := got["services"].([]any)
	if !ok || len(list) != 0 {
		t.Fatalf("services = %v, ожидался пустой список", got["services"])
	}
	// «Не знаем» и «выключен» — разные ответы, и путать их нельзя.
	if got["watchdog"] != nil {
		t.Fatalf("watchdog = %v, ожидалось null", got["watchdog"])
	}
}
