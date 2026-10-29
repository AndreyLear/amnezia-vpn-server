package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/auth"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/xray"
)

// Имена серверов, которые владелец задаёт в панели
// (amnezia-vpn-server-q1do.4): шапка и «О версиях» их показывают, пустое
// имя — подпись по умолчанию, каждое изменение — в журнале, а имя каскада
// не задевает его синк.

func (f *fixture) putBody(path, body string, csrf bool) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(http.MethodPut, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Requested-With", "fetch")
	if csrf {
		req.Header.Set(auth.CSRFHeaderName, f.csrf)
	}
	rec := httptest.NewRecorder()
	f.serve(rec, req)
	return rec
}

type namesView struct {
	ServerName string `json:"server_name"`
	Entries    []struct {
		Name  string `json:"name"`
		Title string `json:"title"`
	} `json:"entries"`
}

func namesOf(t *testing.T, f *fixture, path string) namesView {
	t.Helper()
	rec := f.get(path)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: код %d", path, rec.Code)
	}
	var v namesView
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func messageOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var v struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &v)
	return v.Message
}

func newestAudit(t *testing.T, f *fixture) db.AuditRecord {
	t.Helper()
	entries, err := db.AuditTail(f.h, 1)
	if err != nil || len(entries) == 0 {
		t.Fatalf("журнал: %+v, %v", entries, err)
	}
	return entries[0]
}

func TestAPIServerName(t *testing.T) {
	f, _, _ := vlessFixture(t)
	for _, path := range []string{"/api/stats/host", "/api/versions"} {
		if got := namesOf(t, f, path); got.ServerName != "" {
			t.Fatalf("%s: по умолчанию имени нет: %q", path, got.ServerName)
		}
	}

	if rec := f.putBody("/api/server/name", `{"name":"Москва"}`, false); rec.Code != http.StatusForbidden {
		t.Fatalf("без CSRF: код %d", rec.Code)
	}
	rec := f.putBody("/api/server/name", `{"name":"  Москва <b>  "}`, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("код %d: %s", rec.Code, rec.Body.String())
	}
	for _, path := range []string{"/api/stats/host", "/api/versions"} {
		if got := namesOf(t, f, path); got.ServerName != "Москва <b>" {
			t.Fatalf("%s: имя %q", path, got.ServerName)
		}
	}
	if a := newestAudit(t, f); a.Action != "server.rename" || a.Detail != "без имени → «Москва <b>»" || a.Actor == "" {
		t.Fatalf("журнал: %+v", a)
	}

	for body, want := range map[string]string{
		`{"name":"` + strings.Repeat("я", 33) + `"}`: "Имя длиннее 32 символов",
		`{"name":"а\nб"}`:                            "В имени есть недопустимые символы",
	} {
		rec := f.putBody("/api/server/name", body, true)
		if rec.Code != http.StatusBadRequest || messageOf(t, rec) != want {
			t.Fatalf("%s: код %d, %q", body, rec.Code, messageOf(t, rec))
		}
	}
	if rec := f.putBody("/api/server/name", `nope`, true); rec.Code != http.StatusBadRequest {
		t.Fatalf("не JSON: код %d", rec.Code)
	}

	// Пустое — сброс.
	if rec := f.putBody("/api/server/name", `{"name":" "}`, true); rec.Code != http.StatusOK {
		t.Fatalf("сброс: код %d", rec.Code)
	}
	if got := namesOf(t, f, "/api/stats/host"); got.ServerName != "" {
		t.Fatalf("после сброса: %q", got.ServerName)
	}
	if a := newestAudit(t, f); a.Action != "server.rename" || a.Detail != "«Москва <b>» → без имени" {
		t.Fatalf("журнал сброса: %+v", a)
	}
	// То же имя ещё раз — ни записи, ни ошибки.
	if rec := f.putBody("/api/server/name", `{"name":""}`, true); rec.Code != http.StatusOK {
		t.Fatalf("повтор: код %d", rec.Code)
	}
	if a := newestAudit(t, f); a.Detail != "«Москва <b>» → без имени" {
		t.Fatalf("повтор попал в журнал: %+v", a)
	}
}

func TestAPIEntryTitle(t *testing.T) {
	f, _, c := vlessFixture(t)
	token, err := xray.RotateEntryToken(f.h, "ru")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SaveXrayEntry(f.h, db.XrayEntry{
		Name: "de-1", LinkHost: "de.example.com", LinkPort: 443, SNI: "de.example.com",
		PublicKey: "WQcVePR5eFDpkuq8PefEalET53I_3Zb62YjqMLuHMx4", ShortID: "a1b2", Path: "/de", LinkPrefix: "DE ",
	}); err != nil {
		t.Fatal(err)
	}

	if rec := f.putBody("/api/entries/ru/title", `{"title":"Москва"}`, false); rec.Code != http.StatusForbidden {
		t.Fatalf("без CSRF: код %d", rec.Code)
	}
	rec := f.putBody("/api/entries/ru/title", `{"title":" Москва "}`, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("код %d: %s", rec.Code, rec.Body.String())
	}
	for _, path := range []string{"/api/stats/host", "/api/versions"} {
		got := namesOf(t, f, path)
		if len(got.Entries) != 2 || got.Entries[0].Name != "de-1" || got.Entries[0].Title != "" ||
			got.Entries[1].Name != "ru" || got.Entries[1].Title != "Москва" {
			t.Fatalf("%s: %+v", path, got.Entries)
		}
	}
	if a := newestAudit(t, f); a.Action != "entry.rename" || a.Subject != "ru" || a.Detail != "без имени → «Москва»" {
		t.Fatalf("журнал: %+v", a)
	}

	// Окно клиента подписывает маршрут именем каскада.
	var vless struct {
		Routes []struct {
			Entry string `json:"entry"`
			Title string `json:"title"`
		} `json:"routes"`
	}
	if err := json.Unmarshal(f.get("/api/clients/"+strconv.FormatInt(c.ID, 10)+"/vless").Body.Bytes(), &vless); err != nil {
		t.Fatal(err)
	}
	titles := map[string]string{}
	for _, r := range vless.Routes {
		titles[r.Entry] = r.Title
	}
	if titles["ru"] != "Москва" || titles["de-1"] != "" {
		t.Fatalf("маршруты: %+v", vless.Routes)
	}

	// Синк не порвался: тот же секрет, тот же узел, отчёт принимается.
	if rec := f.entryGet("198.51.100.7", "Bearer "+token); rec.Code != http.StatusOK {
		t.Fatalf("синк после имени: код %d", rec.Code)
	}
	if rec := f.entryPost("198.51.100.7", "Bearer "+token, `{"release":"3.3.0"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("отчёт после имени: код %d", rec.Code)
	}
	if rec := f.postBody("/api/entries/ru/update", "{}"); rec.Code == http.StatusNotFound {
		t.Fatal("ручка обновления потеряла каскад")
	}

	for _, tc := range []struct {
		path, body string
		code       int
		msg        string
	}{
		{"/api/entries/de-1/title", `{"title":"МОСКВА"}`, http.StatusConflict, "Такое имя уже у другого каскада"},
		{"/api/entries/de-1/title", `{"title":"каскад RU"}`, http.StatusConflict, "Такое имя уже у другого каскада"},
		{"/api/entries/de-1/title", `{"title":"` + strings.Repeat("ж", 33) + `"}`, http.StatusBadRequest, "Имя длиннее 32 символов"},
		{"/api/entries/de-1/title", `{"title":"а‮б"}`, http.StatusBadRequest, "В имени есть недопустимые символы"},
		{"/api/entries/nope/title", `{"title":"Где-то"}`, http.StatusNotFound, "Каскад не найден"},
	} {
		rec := f.putBody(tc.path, tc.body, true)
		if rec.Code != tc.code || messageOf(t, rec) != tc.msg {
			t.Errorf("%s %s: код %d %q, ждали %d %q", tc.path, tc.body, rec.Code, messageOf(t, rec), tc.code, tc.msg)
		}
	}

	if rec := f.putBody("/api/entries/ru/title", `{"title":""}`, true); rec.Code != http.StatusOK {
		t.Fatalf("сброс: код %d", rec.Code)
	}
	if got := namesOf(t, f, "/api/stats/host"); got.Entries[1].Title != "" {
		t.Fatalf("после сброса: %+v", got.Entries)
	}
	if a := newestAudit(t, f); a.Action != "entry.rename" || a.Detail != "«Москва» → без имени" {
		t.Fatalf("журнал сброса: %+v", a)
	}
}
