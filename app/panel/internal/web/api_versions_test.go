package web

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/awgconf"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/status"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/xray"
)

// После AmneziaWG окно показывает Xray: версию ядра (из versions.lock через
// окружение панели), включён ли VLESS — то же состояние, что vless_state в
// шапке, — и когда каждый узел-вход последний раз забирал конфиг
// (amnezia-vpn-server-14y0.16). online считает сервер по своим часам:
// часы браузера могут отставать.
func TestAPIVersionsReportsXray(t *testing.T) {
	f, _, _ := vlessFixture(t)
	t.Setenv("AMNEZIA_XRAY_VERSION", "26.10.21")
	for _, name := range []string{"de", "fi"} {
		if err := db.SaveXrayEntry(f.h, db.XrayEntry{
			Name: name, LinkHost: name + ".example.com", LinkPort: 443, SNI: name + ".example.com",
			PublicKey: "WQcVePR5eFDpkuq8PefEalET53I_3Zb62YjqMLuHMx4", ShortID: "a1b2", Path: "/" + name,
		}); err != nil {
			t.Fatal(err)
		}
	}
	// ru забрал конфиг только что, de — шесть минут назад, fi — ни разу.
	token, err := xray.RotateEntryToken(f.h, "ru")
	if err != nil {
		t.Fatal(err)
	}
	if rec := f.entryGet("198.51.100.7", "Bearer "+token); rec.Code != http.StatusOK {
		t.Fatalf("запрос узла: код %d", rec.Code)
	}
	stale := time.Now().UTC().Add(-6 * time.Minute).Truncate(time.Second)
	if err := db.TouchXrayEntrySync(f.h, "de", stale); err != nil {
		t.Fatal(err)
	}

	var got struct {
		Xray       string `json:"xray"`
		VLESSState string `json:"vless_state"`
		Entries    []struct {
			Name     string  `json:"name"`
			LastSync *string `json:"last_sync_utc"`
			Online   bool    `json:"online"`
		} `json:"entries"`
	}
	rec := f.get("/api/versions")
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Xray != "26.10.21" {
		t.Fatalf("xray = %q", got.Xray)
	}
	if want := hostVLESSState(t, f); got.VLESSState != want || want == "none" {
		t.Fatalf("vless_state = %q, в шапке %v", got.VLESSState, want)
	}
	if len(got.Entries) != 3 {
		t.Fatalf("entries: %+v", got.Entries)
	}
	de, fi, ru := got.Entries[0], got.Entries[1], got.Entries[2]
	if de.Name != "de" || de.LastSync == nil || *de.LastSync != stale.Format(time.RFC3339) || de.Online {
		t.Fatalf("de: %+v", de)
	}
	if fi.Name != "fi" || fi.LastSync != nil || fi.Online {
		t.Fatalf("fi: %+v", fi)
	}
	if ru.Name != "ru" || ru.LastSync == nil || !ru.Online {
		t.Fatalf("ru: %+v", ru)
	}
}

// Установка только с AWG: VLESS выключен, узлов нет — окну нечего
// показывать, и строки Xray оно прячет.
func TestAPIVersionsWithoutXray(t *testing.T) {
	f := newFixture(t)
	got := decodeAPI(t, f.get("/api/versions"))
	if got["vless_state"] != "none" {
		t.Fatalf("vless_state = %v, want none", got["vless_state"])
	}
	if entries, ok := got["entries"].([]any); !ok || len(entries) != 0 {
		t.Fatalf("entries = %#v, want []", got["entries"])
	}
}

// «Что у меня стоит» — вопрос, на который до этого отвечал только SSH
// (amnezia-vpn-server-rdcz).
func TestAPIVersionsReportsWhatIsKnown(t *testing.T) {
	f := newFixture(t)
	t.Setenv("AMNEZIA_VERSION", "2.9.0")

	versions := status.Versions{
		Schema:          "v1",
		AmneziaWGGo:     "0.2.19",
		AmneziaWGTools:  "3.1.20260812",
		TunnelDriver:    "kernel",
		AmneziaWGModule: "3.1.20260812",
	}
	data, err := json.Marshal(versions)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(f.statusPath), "versions.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	got := decodeAPI(t, f.get("/api/versions"))
	if got["product"] != "2.9.0" {
		t.Fatalf("product = %v, ожидалось 2.9.0", got["product"])
	}
	if got["amneziawg_go"] != "0.2.19" || got["amneziawg_tools"] != "3.1.20260812" {
		t.Fatalf("версии AmneziaWG не дошли: %v", got)
	}
	// На чём работает туннель и версия модуля (amnezia-vpn-server-y7bp).
	if got["tunnel_driver"] != "kernel" || got["amneziawg_module"] != "3.1.20260812" {
		t.Fatalf("модуль ядра не дошёл: %v", got)
	}
	if got["schema"] != db.SchemaVersion {
		t.Fatalf("schema = %v, ожидалось %q", got["schema"], db.SchemaVersion)
	}
}

// Развёртывание, где контейнер awg старше этой возможности, обязано отвечать
// «неизвестно», а не падать и не выдумывать.
func TestAPIVersionsSaysUnknownRatherThanGuessing(t *testing.T) {
	f := newFixture(t)
	t.Setenv("AMNEZIA_VERSION", "")

	rec := f.get("/api/versions")
	if rec.Code != http.StatusOK {
		t.Fatalf("код %d, ожидался 200", rec.Code)
	}
	got := decodeAPI(t, rec)
	if got["product"] != "" || got["amneziawg_go"] != "" {
		t.Fatalf("панель выдумала версии там, где их неоткуда взять: %v", got)
	}
	// Схема известна всегда: она в самой базе.
	if got["schema"] != db.SchemaVersion {
		t.Fatalf("schema = %v, ожидалось %q", got["schema"], db.SchemaVersion)
	}
}

// Про хост панель знать не может: она в контейнере. Всё, что она про него
// говорит, приходит из файла, который пишет установщик (amnezia-vpn-server-8bt5).
func TestAPIVersionsReportsTheDeploymentItRunsOn(t *testing.T) {
	f := newFixture(t)
	yes, no := true, false
	dep := status.Deployment{
		Schema:      "v1",
		OS:          "ubuntu 24.04 (noble)",
		Docker:      "27.3.1",
		Fail2ban:    &yes,
		Watchdog:    &yes,
		UpdateCheck: &no,
	}
	data, err := json.Marshal(dep)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(f.statusPath), "deployment.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	// Снимок последнего выпуска — тот же файл, что читает проверка обновлений.
	rel := `{"tag_name":"v9.9.9","body":"что нового\n\namnezia-sha256: abc"}`
	if err := os.WriteFile(filepath.Join(filepath.Dir(f.statusPath), "update-latest.json"), []byte(rel), 0o600); err != nil {
		t.Fatal(err)
	}

	got := decodeAPI(t, f.get("/api/versions"))
	if got["os"] != "ubuntu 24.04 (noble)" || got["docker"] != "27.3.1" {
		t.Fatalf("система сервера не дошла: %v", got)
	}
	if got["fail2ban"] != true || got["watchdog"] != true {
		t.Fatalf("включённое показано как выключенное: %v", got)
	}
	// Выключенное и неизвестное — разные вещи, и false тут значит именно
	// «выключено владельцем».
	if got["update_check"] != false {
		t.Fatalf("update_check = %v, ожидалось false", got["update_check"])
	}
	if got["latest"] != "9.9.9" {
		t.Fatalf("latest = %v, ожидалось 9.9.9", got["latest"])
	}
}

// Развёртывание, поставленное установщиком старше этой возможности, не должно
// выглядеть как сервер с выключенным сторожем.
func TestAPIVersionsKeepsUnknownApartFromOff(t *testing.T) {
	f := newFixture(t)
	got := decodeAPI(t, f.get("/api/versions"))
	for _, key := range []string{"watchdog", "fail2ban", "update_check"} {
		if got[key] != nil {
			t.Fatalf("%s = %v, ожидалось null (неизвестно)", key, got[key])
		}
	}
}

// Уровень протокола не хранится нигде — он выводится из набора параметров,
// который несёт сам сервер. Сказать «2.0» про туннель без I1–I5 значило бы
// утверждать то, что нечем подтвердить (amnezia-vpn-server-8bt5).
func TestTunnelProtocolIsReadOffTheParameters(t *testing.T) {
	jc := uint16(4)
	cases := []struct {
		name   string
		params *awgconf.Params
		want   string
	}{
		{"2.0 by its signature packets", &awgconf.Params{I1: "<b 0xf0>"}, "AmneziaWG 2.0"},
		{"1.5 by junk and headers only", &awgconf.Params{Jc: &jc, H1: "1000000"}, "AmneziaWG 1.5"},
		{"nothing to go on", &awgconf.Params{}, ""},
		{"no parameters at all", nil, ""},
	}
	for _, c := range cases {
		if got := tunnelProtocol(c.params); got != c.want {
			t.Errorf("%s: tunnelProtocol = %q, ожидалось %q", c.name, got, c.want)
		}
	}
}

// Резолвер включён тогда, когда клиентам выдан адрес самого туннеля. Это
// видно по тому, что клиенты получают, а не по флагу, который кто-то мог
// забыть обновить.
func TestResolverIsProvenByWhatClientsGet(t *testing.T) {
	cases := []struct {
		dns, tunnel string
		want        bool
	}{
		{"10.8.0.1", "10.8.0.1", true},
		{"10.8.0.1, fded:a0b::1", "10.8.0.1", true},
		{"1.1.1.1, 8.8.8.8", "10.8.0.1", false},
		{"", "10.8.0.1", false},
		{"10.8.0.1", "", false},
	}
	for _, c := range cases {
		if got := dnsIsOurResolver(c.dns, c.tunnel); got != c.want {
			t.Errorf("dnsIsOurResolver(%q, %q) = %v, ожидалось %v", c.dns, c.tunnel, got, c.want)
		}
	}
}
