package xray

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

// Секрет узла: показывается один раз, в базе — только хэш; принимается
// только свой, а после замены прежний перестаёт работать
// (amnezia-vpn-server-14y0.6).
func TestEntryTokenLifecycle(t *testing.T) {
	handle := newDB(t)
	if err := db.SaveXrayEntry(handle, ruEntry()); err != nil {
		t.Fatal(err)
	}
	other := ruEntry()
	other.Name = "kz"
	if err := db.SaveXrayEntry(handle, other); err != nil {
		t.Fatal(err)
	}
	if _, err := AuthenticateEntry(handle, "anything"); !errors.Is(err, ErrEntryTokenInvalid) {
		t.Fatalf("без секретов принят токен: %v", err)
	}

	ru, err := RotateEntryToken(handle, "ru")
	if err != nil {
		t.Fatal(err)
	}
	kz, err := RotateEntryToken(handle, "kz")
	if err != nil {
		t.Fatal(err)
	}
	if len(ru) < 40 || ru == kz {
		t.Fatalf("секреты %q и %q", ru, kz)
	}
	tokens, err := db.XrayEntryTokens(handle)
	if err != nil {
		t.Fatal(err)
	}
	for _, tk := range tokens {
		if strings.Contains(tk.Hash, ru) || strings.Contains(tk.Hash, kz) || len(tk.Hash) != 64 {
			t.Fatalf("в базе не хэш: %+v", tk)
		}
	}
	for token, want := range map[string]string{ru: "ru", kz: "kz"} {
		if name, err := AuthenticateEntry(handle, token); err != nil || name != want {
			t.Fatalf("секрет узла %s: %q, %v", want, name, err)
		}
	}
	for _, bad := range []string{"", ru + "x", ru[:len(ru)-1], strings.ToUpper(ru), "Bearer " + ru} {
		if _, err := AuthenticateEntry(handle, bad); !errors.Is(err, ErrEntryTokenInvalid) {
			t.Errorf("принят чужой секрет %q: %v", bad, err)
		}
	}

	again, err := RotateEntryToken(handle, "ru")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AuthenticateEntry(handle, ru); !errors.Is(err, ErrEntryTokenInvalid) {
		t.Fatal("после замены прежний секрет работает")
	}
	if name, err := AuthenticateEntry(handle, again); err != nil || name != "ru" {
		t.Fatalf("новый секрет: %q, %v", name, err)
	}
	if _, err := RotateEntryToken(handle, "nope"); !errors.Is(err, db.ErrXrayEntryNotFound) {
		t.Fatalf("секрет неизвестному узлу: %v", err)
	}
	// Удалённый узел уносит секрет с собой.
	if err := db.DeleteXrayEntry(handle, "ru"); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveXrayEntry(handle, ruEntry()); err != nil {
		t.Fatal(err)
	}
	if _, err := AuthenticateEntry(handle, again); !errors.Is(err, ErrEntryTokenInvalid) {
		t.Fatal("секрет удалённого узла пережил удаление")
	}
}

// Что узел получает от панели: клиенты {client_id, uuid, enabled} — без
// имён и ключей AWG — и параметры дороги до этого сервера, те же, что в
// прямой ссылке (amnezia-vpn-server-14y0.6).
func TestBuildEntryConfig(t *testing.T) {
	handle := newDB(t)
	if err := db.SaveXrayEntry(handle, ruEntry()); err != nil {
		t.Fatal(err)
	}
	a := addClient(t, handle, "andrey", awgPub)
	b := addClient(t, handle, "mama", awgPub2)
	if err := db.SetClientEnabled(handle, b.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildEntryConfig(handle, "ru"); !errors.Is(err, db.ErrXrayNotConfigured) {
		t.Fatalf("без Xray на сервере: %v", err)
	}
	if err := db.SaveXraySettings(handle, *fixtureSettings()); err != nil {
		t.Fatal(err)
	}
	got, err := BuildEntryConfig(handle, "ru")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"entry":"ru","clients":[` +
		`{"client_id":` + itoa(a.ID) + `,"uuid":"` + a.VLESSUUID + `","enabled":true},` +
		`{"client_id":` + itoa(b.ID) + `,"uuid":"` + b.VLESSUUID + `","enabled":false}],` +
		`"upstream":{"address":"vpn.example.com","port":443,"path":"/f3a9c1d27e",` +
		`"server_name":"vpn.example.com","public_key":"` + fixturePub + `","short_id":"6ba85179e30d4fc2"}}`
	if string(raw) != want {
		t.Fatalf("\n got %s\nwant %s", raw, want)
	}
	if strings.Contains(string(raw), "andrey") || strings.Contains(string(raw), fixturePriv) {
		t.Fatal("узлу ушло лишнее: имя клиента или приватный ключ сервера")
	}

	// Явный адрес для ссылок — он же адрес дороги с узла.
	s := fixtureSettings()
	s.LinkAddress, s.LinkPort = "super-space.com.de", 8443
	if err := db.SaveXraySettings(handle, *s); err != nil {
		t.Fatal(err)
	}
	if got, _ = BuildEntryConfig(handle, "ru"); got.Upstream.Address != "super-space.com.de" || got.Upstream.Port != 8443 {
		t.Fatalf("дорога: %+v", got.Upstream)
	}
	if _, err := BuildEntryConfig(handle, "nope"); !errors.Is(err, db.ErrXrayEntryNotFound) {
		t.Fatalf("неизвестный узел: %v", err)
	}
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
