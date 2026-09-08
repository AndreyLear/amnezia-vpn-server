package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/auth"
	"testing"
)

// postBody отправляет мутацию так же, как это делает панель: телом JSON.
func (f *fixture) postBody(path, body string) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Requested-With", "fetch")
	req.Header.Set(auth.CSRFHeaderName, f.csrf)
	rec := httptest.NewRecorder()
	f.serve(rec, req)
	return rec
}

// patchBody отправляет PATCH так же, как это делает панель: телом JSON.
func (f *fixture) patchBody(path, body string) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(http.MethodPatch, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Requested-With", "fetch")
	req.Header.Set(auth.CSRFHeaderName, f.csrf)
	rec := httptest.NewRecorder()
	f.serve(rec, req)
	return rec
}

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

// Кнопка «Обновить»: панель кладёт запрос в свой том, а делает обновление
// хост (amnezia-vpn-server-tjoq).
func TestAPIUpdateStartLeavesARequestForTheHost(t *testing.T) {
	f := newFixture(t)
	t.Setenv("AMNEZIA_VERSION", "2.8.2")
	writeStatusFile(t, f, "update-latest.json", `{"tag_name":"v2.9.0","body":"x"}`)

	rec := f.postJSON("/api/update/start", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("код %d: %s", rec.Code, rec.Body.String())
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(f.dbPath), "update-request.json"))
	if err != nil {
		t.Fatalf("запрос не появился: %v", err)
	}
	// Версию в запрос кладёт панель из того, что нашёл хост, — не из того,
	// что прислал браузер.
	if !strings.Contains(string(data), `"version":"2.9.0"`) {
		t.Fatalf("запрос = %s", data)
	}
}

// Просить нечего — и человек должен услышать это, а не смотреть в тишину.
func TestAPIUpdateStartRefusesWhenThereIsNothingToTake(t *testing.T) {
	f := newFixture(t)
	t.Setenv("AMNEZIA_VERSION", "2.9.0")
	writeStatusFile(t, f, "update-latest.json", `{"tag_name":"v2.9.0","body":"x"}`)

	rec := f.postJSON("/api/update/start", nil)
	if rec.Code == http.StatusOK {
		t.Fatalf("панель попросила обновиться на ту же версию")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(f.dbPath), "update-request.json")); err == nil {
		t.Fatalf("запрос всё-таки лёг")
	}
}

// Два установщика в одном каталоге подерутся, и разбирать это придётся
// руками на живом сервере.
func TestAPIUpdateStartRefusesWhileOneIsRunning(t *testing.T) {
	f := newFixture(t)
	t.Setenv("AMNEZIA_VERSION", "2.8.2")
	writeStatusFile(t, f, "update-latest.json", `{"tag_name":"v2.9.0","body":"x"}`)
	writeStatusFile(t, f, "update-state.json", `{"state":"running","to":"2.9.0"}`)

	rec := f.postJSON("/api/update/start", nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("код %d, ожидался 409: %s", rec.Code, rec.Body.String())
	}
}

// Закрытая крестиком полоса переживает и перезагрузку страницы, и другой
// браузер: владелец один и тот же на компьютере и на телефоне.
func TestDismissedBannerIsRememberedOnTheServer(t *testing.T) {
	f := newFixture(t)
	t.Setenv("AMNEZIA_VERSION", "2.8.2")
	writeStatusFile(t, f, "update-latest.json", `{"tag_name":"v2.9.0","body":"x"}`)

	if rec := f.postBody("/api/update/dismiss", `{"version":"2.9.0"}`); rec.Code != http.StatusOK {
		t.Fatalf("код %d: %s", rec.Code, rec.Body.String())
	}

	got := decodeAPI(t, f.get("/api/update"))
	if got["dismissed"] != "2.9.0" {
		t.Fatalf("dismissed = %v", got["dismissed"])
	}
	// Полосу закрыли, но взять выпуск по-прежнему есть что: значок-
	// напоминание живёт отдельно от полосы.
	if got["available"] != true {
		t.Fatalf("закрытие полосы погасило само предложение: %v", got)
	}
}

// Мутация без токена должна быть отвергнута — иначе пропуск повторится
// (amnezia-vpn-server-f0xm).
//
// SameSite=Lax роняет чужие межсайтовые POST и потому прикрывает такой
// пропуск в браузере, но заведён он вторым слоем, а не единственным. Самая
// дорогая операция продукта — «обнови весь сервер», выполняется от root на
// хосте — последнее, что стоит оставлять на одном слое.
func TestUpdateRoutesRefuseAMutationWithoutCSRF(t *testing.T) {
	f := newFixture(t)
	t.Setenv("AMNEZIA_VERSION", "2.8.2")
	writeStatusFile(t, f, "update-latest.json", `{"tag_name":"v2.9.0","body":"x"}`)

	for _, path := range []string{
		"/api/update/check",
		"/api/update/start",
		"/api/update/dismiss",
	} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"version":"2.9.0"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("X-Requested-With", "fetch")
		// Токена нет намеренно: сессия при этом живая.
		rec := httptest.NewRecorder()
		f.serve(rec, req)
		if rec.Code == http.StatusOK {
			t.Errorf("%s принял мутацию без токена (код %d)", path, rec.Code)
		}
	}

	// И ничего не сделал по дороге: запросов на хост не появилось.
	for _, name := range []string{"update-check-request", "update-request.json"} {
		if _, err := os.Stat(filepath.Join(filepath.Dir(f.dbPath), name)); err == nil {
			t.Fatalf("запрос %s лёг, хотя токена не было", name)
		}
	}
}

// Итог обновления должен найти человека сам — в том числе после перезапуска
// панели, который делает само обновление (amnezia-vpn-server-tjoq).
func TestOutcomeIsRememberedOnceItHasBeenShown(t *testing.T) {
	f := newFixture(t)
	t.Setenv("AMNEZIA_VERSION", "2.9.0")
	writeStatusFile(t, f, "update-state.json",
		`{"schema":"v1","state":"ok","from":"2.8.2","to":"2.9.0","step":"готово","message":"готово","at_utc":"2026-09-08T10:00:00Z"}`)

	got := decodeAPI(t, f.get("/api/update"))
	if got["outcome_seen"] != "" {
		t.Fatalf("итог помечен показанным до того, как его показали: %v", got)
	}

	if rec := f.postBody("/api/update/dismiss", `{"outcome":"2026-09-08T10:00:00Z"}`); rec.Code != http.StatusOK {
		t.Fatalf("код %d: %s", rec.Code, rec.Body.String())
	}
	got = decodeAPI(t, f.get("/api/update"))
	if got["outcome_seen"] != "2026-09-08T10:00:00Z" {
		t.Fatalf("outcome_seen = %v", got["outcome_seen"])
	}
	// Закрытая полоса и показанный итог живут порознь: закрыть одно не
	// значит убрать другое.
	if got["dismissed"] != "" {
		t.Fatalf("подтверждение итога заодно закрыло полосу: %v", got)
	}
}

// Запрос к хосту пишется так же, как остальные производные файлы: целиком
// или никак. Хост следит за появлением файла, и половина запроса выглядела
// бы для него целым (amnezia-vpn-server-i4m6).
func TestUpdateRequestIsWrittenWholeAndPrivate(t *testing.T) {
	f := newFixture(t)
	t.Setenv("AMNEZIA_VERSION", "2.8.2")
	writeStatusFile(t, f, "update-latest.json", `[{"tag_name":"v2.9.0","body":"x"}]`)

	if rec := f.postJSON("/api/update/start", nil); rec.Code != http.StatusOK {
		t.Fatalf("код %d: %s", rec.Code, rec.Body.String())
	}
	path := filepath.Join(filepath.Dir(f.dbPath), "update-request.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// 0600: запрос лежит в томе панели, и лишним читателям там делать
	// нечего.
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("права %o, ожидалось 600", mode)
	}
	// Никаких временных огрызков рядом: их подобрал бы хост, следящий за
	// каталогом.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Fatalf("рядом остался временный файл %s", e.Name())
		}
	}
}
