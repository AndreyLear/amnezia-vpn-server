package web

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/awgconf"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/status"
)

// «Что у меня стоит» — вопрос, на который до этого отвечал только SSH
// (amnezia-vpn-server-rdcz).
func TestAPIVersionsReportsWhatIsKnown(t *testing.T) {
	f := newFixture(t)
	t.Setenv("AMNEZIA_VERSION", "2.9.0")

	versions := status.Versions{
		Schema:         "v1",
		AmneziaWGGo:    "0.2.19",
		AmneziaWGTools: "3.1.20260812",
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
