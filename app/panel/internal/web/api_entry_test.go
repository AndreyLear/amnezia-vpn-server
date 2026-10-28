package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/xray"
)

// entryGet — запрос узла: без сессии панели, с секретом в заголовке.
func (f *fixture) entryGet(ip, authorization string) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/entry/config", nil)
	req.Header.Set("X-Real-IP", ip)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, req)
	return rec
}

// Узел-вход забирает свою конфигурацию без входа в панель, по секрету в
// заголовке Authorization (amnezia-vpn-server-14y0.6): клиенты и дорога
// до этого сервера. Секрет не попадает ни в журнал, ни в ответ.
func TestAPIEntryConfig(t *testing.T) {
	f, logs, c := vlessFixture(t)
	token, err := xray.RotateEntryToken(f.h, "ru")
	if err != nil {
		t.Fatal(err)
	}

	rec := f.entryGet("198.51.100.7", "Bearer "+token)
	if rec.Code != http.StatusOK {
		t.Fatalf("код %d: %s", rec.Code, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control = %q", cc)
	}
	var got xray.EntryConfig
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	s, _ := db.XraySettingsRow(f.h)
	if got.Entry != "ru" || len(got.Clients) != 1 ||
		got.Clients[0] != (xray.EntryClient{ClientID: c.ID, UUID: c.VLESSUUID, Enabled: true}) ||
		got.Upstream != (xray.EntryUpstream{Address: "vpn.example.com", Port: 443, Path: s.Path,
			ServerName: s.ServerName, PublicKey: s.PublicKey, ShortID: s.ShortID}) {
		t.Fatalf("ответ: %+v", got)
	}

	for name, h := range map[string]string{
		"без заголовка":                "",
		"чужой секрет":                 "Bearer " + strings.Repeat("A", len(token)),
		"секрет без Bearer":            token,
		"другая схема":                 "Basic " + token,
		"секрет с хвостом":             "Bearer " + token + " x",
		"вход в панель тут ни при чём": "Bearer ",
	} {
		rec := f.entryGet("198.51.100.8", h)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: код %d", name, rec.Code)
		}
		if strings.Contains(rec.Body.String(), token) {
			t.Errorf("%s: секрет в ответе", name)
		}
	}
	// Секрет в адресе не принимается: адреса оседают в журналах nginx.
	req := httptest.NewRequest(http.MethodGet, "/api/entry/config?token="+token, nil)
	req.Header.Set("X-Real-IP", "198.51.100.9")
	rec = httptest.NewRecorder()
	f.server.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("секрет в адресе: код %d", rec.Code)
	}
	// Сессия панели узлу не нужна и секрета не заменяет.
	if rec := f.get("/api/entry/config"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("с сессией панели и без секрета: код %d", rec.Code)
	}
	if strings.Contains(logs.String(), token) {
		t.Fatal("секрет узла попал в журнал")
	}

	// Xray на этом сервере не настроен — узлу нечего строить: 503, и узел
	// работает на прежнем конфиге.
	if _, err := f.h.Exec(`DELETE FROM xray`); err != nil {
		t.Fatal(err)
	}
	if rec := f.entryGet("198.51.100.10", "Bearer "+token); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("без Xray: код %d", rec.Code)
	}
}

// Подбор секрета упирается в лимит неудач с одного адреса, а частые
// запросы даже с верным секретом — в общий лимит: узлу хватает раза в
// минуту (amnezia-vpn-server-14y0.6).
func TestAPIEntryConfigRateLimit(t *testing.T) {
	f, _, _ := vlessFixture(t)
	token, err := xray.RotateEntryToken(f.h, "ru")
	if err != nil {
		t.Fatal(err)
	}
	bad := "Bearer " + strings.Repeat("B", len(token))
	for i := 0; i < entryFailLimit; i++ {
		if rec := f.entryGet("203.0.113.5", bad); rec.Code != http.StatusUnauthorized {
			t.Fatalf("неудача #%d: код %d", i+1, rec.Code)
		}
	}
	rec := f.entryGet("203.0.113.5", "Bearer "+token)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("после %d неудач: код %d, Retry-After %q", entryFailLimit, rec.Code, rec.Header().Get("Retry-After"))
	}
	// Другой адрес не страдает.
	if rec := f.entryGet("203.0.113.6", "Bearer "+token); rec.Code != http.StatusOK {
		t.Fatalf("соседний адрес: код %d", rec.Code)
	}

	for i := 1; i < entryRequestLimit; i++ {
		if rec := f.entryGet("203.0.113.7", "Bearer "+token); rec.Code != http.StatusOK {
			t.Fatalf("запрос #%d: код %d", i+1, rec.Code)
		}
	}
	if rec := f.entryGet("203.0.113.7", "Bearer "+token); rec.Code != http.StatusOK {
		t.Fatalf("последний разрешённый запрос: код %d", rec.Code)
	}
	if rec := f.entryGet("203.0.113.7", "Bearer "+token); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("сверх лимита: код %d", rec.Code)
	}
}
