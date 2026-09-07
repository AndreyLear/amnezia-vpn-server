package web

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func writeStatusFile(t *testing.T, f *fixture, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(filepath.Dir(f.statusPath), name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Всё, что панель знает про обновление, она знает из файлов, которые пишет
// хост (amnezia-vpn-server-8bt5).
func TestAPIUpdateReportsWhatTheHostFound(t *testing.T) {
	f := newFixture(t)
	t.Setenv("AMNEZIA_VERSION", "2.8.2")

	writeStatusFile(t, f, "update-latest.json",
		`{"tag_name":"v2.9.0","body":"- первое\n- второе\n\namnezia-sha256: abc"}`)
	writeStatusFile(t, f, "update-check.json",
		`{"schema":"v1","checked_at_utc":"2026-09-07T08:00:00Z","result":"ok"}`)

	got := decodeAPI(t, f.get("/api/update"))
	if got["installed"] != "2.8.2" || got["latest"] != "2.9.0" {
		t.Fatalf("версии не сошлись: %v", got)
	}
	if got["available"] != true {
		t.Fatalf("available = %v, ожидалось true", got["available"])
	}
	// Контрольная сумма предназначена агенту; человеку она была бы шумом
	// посреди того, что он пришёл прочитать.
	notes, _ := got["notes"].(string)
	if notes != "- первое\n- второе" {
		t.Fatalf("notes = %q", notes)
	}
}

// Установленная версия и есть последняя — обычное состояние сервера, и
// предлагать в нём нечего.
func TestAPIUpdateOffersNothingWhenUpToDate(t *testing.T) {
	f := newFixture(t)
	t.Setenv("AMNEZIA_VERSION", "2.9.0")
	writeStatusFile(t, f, "update-latest.json", `{"tag_name":"v2.9.0","body":"x"}`)

	got := decodeAPI(t, f.get("/api/update"))
	if got["available"] != false {
		t.Fatalf("available = %v, ожидалось false", got["available"])
	}
}

// Свежий сервер, где ещё ничего не спрашивали и не обновляли: пустые поля, а
// не отказ.
func TestAPIUpdateSurvivesAServerThatNeverChecked(t *testing.T) {
	f := newFixture(t)
	rec := f.get("/api/update")
	if rec.Code != http.StatusOK {
		t.Fatalf("код %d, ожидался 200", rec.Code)
	}
	got := decodeAPI(t, rec)
	if got["latest"] != "" || got["state"] != "" || got["check_result"] != "" {
		t.Fatalf("панель выдумала то, чего нет: %v", got)
	}
}

// Итог обновления лежит в файле и дожидается: обновление перезапускает саму
// панель, и браузер мог быть закрыт всё это время.
func TestAPIUpdateCarriesTheOutcomeThroughARestart(t *testing.T) {
	f := newFixture(t)
	writeStatusFile(t, f, "update-state.json",
		`{"schema":"v1","state":"rolled-back","from":"2.8.2","to":"2.9.0","step":"откат","message":"не удалось","at_utc":"2026-09-07T08:00:00Z"}`)

	got := decodeAPI(t, f.get("/api/update"))
	if got["state"] != "rolled-back" || got["state_to"] != "2.9.0" {
		t.Fatalf("итог обновления не дошёл: %v", got)
	}
	if got["state_message"] != "не удалось" {
		t.Fatalf("state_message = %v", got["state_message"])
	}
}

// «Проверить обновления» в панели — это файл в её собственном томе, а не
// поход наружу. Наружу ходит хост.
func TestAPIUpdateCheckOnlyLeavesARequest(t *testing.T) {
	f := newFixture(t)
	rec := f.postJSON("/api/update/check", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("код %d, ожидался 200: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["ok"] != true {
		t.Fatalf("ok = %v", body["ok"])
	}
	request := filepath.Join(filepath.Dir(f.dbPath), "update-check-request")
	if _, err := os.Stat(request); err != nil {
		t.Fatalf("запрос не появился: %v", err)
	}
}
