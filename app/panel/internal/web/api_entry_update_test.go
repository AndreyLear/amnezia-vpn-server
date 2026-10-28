package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/xray"
)

// Самообновление каскада (amnezia-vpn-server-q1do.2): панель называет
// узлу в ответе синка только целевой выпуск — свою версию — и метку
// запроса владельца; адресов и сумм в ответе нет.

type entryConfigView struct {
	TargetVersion *string `json:"target_version"`
	UpdateRequest *string `json:"update_request"`
	Raw           map[string]json.RawMessage
}

func entryConfigOf(t *testing.T, f *fixture, token string) entryConfigView {
	t.Helper()
	rec := f.entryGet("198.51.100.7", "Bearer "+token)
	if rec.Code != http.StatusOK {
		t.Fatalf("синк: код %d", rec.Code)
	}
	var v entryConfigView
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &v.Raw); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestAPIEntryConfigTargetVersion(t *testing.T) {
	f, _, _ := vlessFixture(t)
	token, err := xray.RotateEntryToken(f.h, "ru")
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("AMNEZIA_VERSION", "3.4.0")
	got := entryConfigOf(t, f, token)
	if got.TargetVersion == nil || *got.TargetVersion != "3.4.0" {
		t.Fatalf("целевой выпуск — версия панели: %+v", got.TargetVersion)
	}
	if got.UpdateRequest != nil {
		t.Fatalf("без запроса владельца метки нет: %q", *got.UpdateRequest)
	}
	// Только номер: ни адреса, ни суммы узел от панели не получает.
	for key := range got.Raw {
		switch key {
		case "entry", "clients", "upstream", "target_version", "update_request":
		default:
			t.Errorf("лишнее поле в ответе узлу: %s", key)
		}
	}

	// Версия панели не номер выпуска — целевого выпуска нет вовсе.
	for _, v := range []string{"", "dev", "3.4.0-rc.1", "v3.4.0", "3.4"} {
		t.Setenv("AMNEZIA_VERSION", v)
		if got := entryConfigOf(t, f, token); got.TargetVersion != nil {
			t.Errorf("AMNEZIA_VERSION=%q: target_version = %q", v, *got.TargetVersion)
		}
	}
}

// entryReport — отчёт узла с выпуском и состоянием обновления.
func entryReport(release, update string) string {
	body := `{"release": "` + release + `"`
	if update != "" {
		body += `, "update": ` + update
	}
	return body + "}"
}

type entryUpdateView struct {
	Supported bool   `json:"supported"`
	State     string `json:"state"`
	Target    string `json:"target"`
	Reason    string `json:"reason"`
	Release   string `json:"release"`
	Panel     string `json:"panel"`
	Available bool   `json:"available"`
	Requested bool   `json:"requested"`
}

func hostEntryUpdate(t *testing.T, f *fixture) entryUpdateView {
	t.Helper()
	var got struct {
		Entries []struct {
			Update entryUpdateView `json:"update"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(f.get("/api/stats/host").Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Entries) != 1 {
		t.Fatalf("узлов: %d", len(got.Entries))
	}
	return got.Entries[0].Update
}

func (f *fixture) postNoCSRF(path string) *httptest.ResponseRecorder {
	f.t.Helper()
	req := apiJSON(f.t, http.MethodPost, path, map[string]any{})
	rec := httptest.NewRecorder()
	f.serve(rec, req)
	return rec
}

var requestStamp = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`)

// Владелец просит обновить каскад кнопкой: запрос попадает в ответ синка,
// в журнал — кто и до какой версии; узел, забравший запрос, сообщает это
// в отчёте, и панель перестаёт показывать его ожидающим.
func TestAPIEntryUpdateRequest(t *testing.T) {
	f, _, _ := vlessFixture(t)
	token, err := xray.RotateEntryToken(f.h, "ru")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("AMNEZIA_VERSION", "3.4.0")
	report := func(body string) {
		t.Helper()
		if rec := f.entryPost("198.51.100.7", "Bearer "+token, body); rec.Code != http.StatusNoContent {
			t.Fatalf("отчёт: код %d: %s", rec.Code, rec.Body.String())
		}
	}

	// Старый узел: отчёт без update — сам обновляться не умеет.
	report(entryReport("3.3.0", ""))
	if got := hostEntryUpdate(t, f); got.Supported || !got.Available || got.Release != "3.3.0" || got.Panel != "3.4.0" {
		t.Fatalf("старый узел: %+v", got)
	}
	if rec := f.postBody("/api/entries/ru/update", "{}"); rec.Code != http.StatusConflict {
		t.Fatalf("старый узел: код %d, ждали 409", rec.Code)
	}

	report(entryReport("3.3.0", `{"state": "failed", "target": "3.4.0", "reason": "install", "at_utc": "2026-10-28T10:00:00Z", "request": ""}`))
	if got := hostEntryUpdate(t, f); !got.Supported || !got.Available || got.State != "failed" ||
		got.Reason != "install" || got.Target != "3.4.0" || got.Requested {
		t.Fatalf("неудача на узле: %+v", got)
	}

	// Без CSRF — отказ, запроса нет.
	if rec := f.postNoCSRF("/api/entries/ru/update"); rec.Code != http.StatusForbidden {
		t.Fatalf("без CSRF: код %d", rec.Code)
	}
	if got := entryConfigOf(t, f, token); got.UpdateRequest != nil {
		t.Fatal("запрос без CSRF всё же записан")
	}

	if rec := f.postBody("/api/entries/nope/update", "{}"); rec.Code != http.StatusNotFound {
		t.Fatalf("нет такого каскада: код %d", rec.Code)
	}

	rec := f.postBody("/api/entries/ru/update", "{}")
	if rec.Code != http.StatusOK {
		t.Fatalf("запрос: код %d: %s", rec.Code, rec.Body.String())
	}
	cfg := entryConfigOf(t, f, token)
	if cfg.UpdateRequest == nil || !requestStamp.MatchString(*cfg.UpdateRequest) {
		t.Fatalf("метка запроса в ответе синка: %v", cfg.UpdateRequest)
	}
	if got := hostEntryUpdate(t, f); !got.Requested {
		t.Fatalf("запрос ждёт узла: %+v", got)
	}
	entries, err := db.AuditTail(f.h, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 || entries[0].Action != "entry.update" || entries[0].Subject != "ru" ||
		entries[0].Detail != "До версии 3.4.0" || entries[0].Actor == "" {
		t.Fatalf("журнал: %+v", entries)
	}

	// Узел забрал запрос и обновляется.
	report(entryReport("3.3.0", `{"state": "updating", "target": "3.4.0", "reason": "", "at_utc": "2026-10-28T10:05:00Z", "request": "`+*cfg.UpdateRequest+`"}`))
	if got := hostEntryUpdate(t, f); got.Requested || got.State != "updating" || got.Reason != "" {
		t.Fatalf("узел обновляется: %+v", got)
	}
	if rec := f.postBody("/api/entries/ru/update", "{}"); rec.Code != http.StatusConflict {
		t.Fatalf("обновление уже идёт: код %d", rec.Code)
	}

	// Узел на версии панели — обновлять нечего.
	report(entryReport("3.4.0", `{"state": "idle", "target": "3.4.0", "reason": "", "at_utc": "2026-10-28T10:06:00Z", "request": "`+*cfg.UpdateRequest+`"}`))
	if got := hostEntryUpdate(t, f); got.Available || got.Requested {
		t.Fatalf("узел догнал панель: %+v", got)
	}
	if rec := f.postBody("/api/entries/ru/update", "{}"); rec.Code != http.StatusConflict {
		t.Fatalf("уже на версии панели: код %d", rec.Code)
	}

	// Версия панели неизвестна — и просить не о чем.
	report(entryReport("3.3.0", `{"state": "idle", "target": "", "reason": "", "at_utc": "", "request": ""}`))
	t.Setenv("AMNEZIA_VERSION", "")
	if rec := f.postBody("/api/entries/ru/update", "{}"); rec.Code != http.StatusBadRequest {
		t.Fatalf("версия панели неизвестна: код %d", rec.Code)
	}

	// То же состояние — в окне «О версиях».
	t.Setenv("AMNEZIA_VERSION", "3.4.0")
	var versions struct {
		Entries []struct {
			Update entryUpdateView `json:"update"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(f.get("/api/versions").Body.Bytes(), &versions); err != nil {
		t.Fatal(err)
	}
	if v := versions.Entries; len(v) != 1 || !v[0].Update.Supported || !v[0].Update.Available || v[0].Update.State != "idle" {
		t.Fatalf("versions: %+v", v)
	}
}
