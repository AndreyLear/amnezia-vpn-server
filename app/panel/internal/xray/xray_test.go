package xray

import (
	"crypto/ecdh"
	"database/sql"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

// Ключи из testdata/manual/keys.txt — пара, выданная так же, как её
// выдаёт `xray x25519`.
const (
	fixturePriv = "AAoRGB8mLTQ7QklQV15lbHN6gYiPlp2kq7K5wMfO1Vw"
	fixturePub  = "u1D_noKldM-_gg6X9g-5wUPsdBXPUU-M_Zjv9Z4FlhQ"
)

const (
	awgPriv  = "4OSe6B1rDXdY4RdVJ+eenaEJOqRVZ9kxx25z9bI0t28="
	awgPub   = "qXvOE2SbilF5RNpHXPU8xQCTvUIxYPeTNI6R6p+bqnw="
	awgPub2  = "a1IqKSrVxcXN16cMZIUZ6kof0oU0kvwb0w6Q8bgGggQ="
	awgPub3  = "mK1lOO8QY2KQq7j7MEpJ8cB6x1u8m3gqYlQ1+o2LtQ0="
	endpoint = "vpn.example.com:51820"
)

func newDB(t *testing.T) *sql.DB {
	t.Helper()
	handle, err := db.Open(filepath.Join(t.TempDir(), "amnezia.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { handle.Close() })
	if err := db.Migrate(handle); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateServer(handle, awgPriv, awgPub, "10.8.0.1/24", "", 51820, "", "{}", endpoint); err != nil {
		t.Fatal(err)
	}
	return handle
}

func addClient(t *testing.T, handle *sql.DB, name, pub string) *db.ClientRecord {
	t.Helper()
	c, err := db.CreateClient(handle, "10.8.0.1/24", db.NewClient{Name: name, PrivateKey: awgPriv, PublicKey: pub})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Ключ Reality — в том же виде, что у `xray x25519`: base64 без
// дополнения в URL-алфавите, 32 байта, приватный ключ «зажат» по
// правилам X25519, публичный выводится из приватного.
func TestGenerateRealityKeys(t *testing.T) {
	priv, pub, err := GenerateRealityKeys()
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{priv, pub} {
		if len(k) != 43 || strings.ContainsAny(k, "+/=") {
			t.Fatalf("ключ %q не в формате base64 raw url", k)
		}
	}
	raw, err := base64.RawURLEncoding.DecodeString(priv)
	if err != nil || len(raw) != 32 {
		t.Fatalf("приватный ключ не 32 байта: %v", err)
	}
	if raw[0]&7 != 0 || raw[31]&0x80 != 0 || raw[31]&0x40 == 0 {
		t.Fatal("приватный ключ не зажат, как это делает xray x25519")
	}
	k, _ := ecdh.X25519().NewPrivateKey(raw)
	if want := base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes()); pub != want {
		t.Fatalf("публичный ключ %q не соответствует приватному (%q)", pub, want)
	}
	other, _, _ := GenerateRealityKeys()
	if other == priv {
		t.Fatal("два вызова дали один ключ")
	}
}

func TestRealityPublicKeyMatchesXray(t *testing.T) {
	got, err := RealityPublicKey(fixturePriv)
	if err != nil {
		t.Fatal(err)
	}
	if got != fixturePub {
		t.Fatalf("публичный ключ %q, xray x25519 даёт %q", got, fixturePub)
	}
	for _, bad := range []string{"", "AAAA", fixturePriv + "=", strings.Replace(fixturePriv, "_", "/", 1) + "x"} {
		if _, err := RealityPublicKey(bad); err == nil {
			t.Errorf("ключ %q принят", bad)
		}
	}
}

func TestNewServerSettings(t *testing.T) {
	if _, err := NewServerSettings(""); !errors.Is(err, ErrNoServerName) {
		t.Fatalf("без домена: %v", err)
	}
	s, err := NewServerSettings("vpn.example.com")
	if err != nil {
		t.Fatal(err)
	}
	// Self-steal: своё имя в SNI, чужие подключения — на локальный nginx.
	if s.ServerName != "vpn.example.com" || s.Target != "127.0.0.1:4433" || s.LinkPort != 443 {
		t.Fatalf("умолчания: %+v", s)
	}
	if len(s.ShortID) != 16 || !strings.HasPrefix(s.Path, "/") || len(s.Path) < 9 {
		t.Fatalf("shortId %q, path %q", s.ShortID, s.Path)
	}
	if pub, _ := RealityPublicKey(s.PrivateKey); pub != s.PublicKey {
		t.Fatal("ключи не пара")
	}
}

func fixtureSettings() *db.XraySettings {
	return &db.XraySettings{
		PrivateKey: fixturePriv, PublicKey: fixturePub, ShortID: "6ba85179e30d4fc2",
		Path: "/f3a9c1d27e", Target: db.XrayDefaultTarget, ServerName: "vpn.example.com",
		LinkPort: 443,
	}
}

// Формат ссылки — ровно тот, каким ссылки выданы вручную 27.10.2026
// (amnezia-vpn-server-adaw): тот же клиент обязан получить ту же строку
// до байта, иначе в приложении он окажется дважды.
func TestLinkFormat(t *testing.T) {
	cases := []struct {
		name, address, uuid, want string
	}{
		{
			"andrey", "vpn.example.com", "3f2504e0-4f89-41d3-9a0c-0305e82c3301",
			"vless://3f2504e0-4f89-41d3-9a0c-0305e82c3301@vpn.example.com:443" +
				"?type=xhttp&path=%2Ff3a9c1d27e&mode=auto&security=reality&sni=vpn.example.com" +
				"&fp=chrome&pbk=u1D_noKldM-_gg6X9g-5wUPsdBXPUU-M_Zjv9Z4FlhQ&sid=6ba85179e30d4fc2" +
				"#VPN%20andrey",
		},
		{
			"Мама телефон", "203.0.113.7", "9b2d7c1a-6e3f-4a5b-8c7d-1e2f3a4b5c6d",
			"vless://9b2d7c1a-6e3f-4a5b-8c7d-1e2f3a4b5c6d@203.0.113.7:443" +
				"?type=xhttp&path=%2Ff3a9c1d27e&mode=auto&security=reality&sni=vpn.example.com" +
				"&fp=chrome&pbk=u1D_noKldM-_gg6X9g-5wUPsdBXPUU-M_Zjv9Z4FlhQ&sid=6ba85179e30d4fc2" +
				"#VPN%20%D0%9C%D0%B0%D0%BC%D0%B0%20%D1%82%D0%B5%D0%BB%D0%B5%D1%84%D0%BE%D0%BD",
		},
		{
			"a&b+c", "2001:db8::1", "9b2d7c1a-6e3f-4a5b-8c7d-1e2f3a4b5c6d",
			"vless://9b2d7c1a-6e3f-4a5b-8c7d-1e2f3a4b5c6d@[2001:db8::1]:443" +
				"?type=xhttp&path=%2Ff3a9c1d27e&mode=auto&security=reality&sni=vpn.example.com" +
				"&fp=chrome&pbk=u1D_noKldM-_gg6X9g-5wUPsdBXPUU-M_Zjv9Z4FlhQ&sid=6ba85179e30d4fc2" +
				"#VPN%20a%26b%2Bc",
		},
	}
	for _, tc := range cases {
		got := Link(fixtureSettings(), tc.address, db.VLESSClient{Name: tc.name, UUID: tc.uuid})
		if got != tc.want {
			t.Errorf("%s:\n got %s\nwant %s", tc.name, got, tc.want)
		}
	}
}

func TestClientLinkAddress(t *testing.T) {
	handle := newDB(t)
	c := addClient(t, handle, "andrey", awgPub)
	if _, err := ClientLink(handle, c.ID); !errors.Is(err, db.ErrXrayNotConfigured) {
		t.Fatalf("без настроек Xray: %v", err)
	}
	s := fixtureSettings()
	if err := db.SaveXraySettings(handle, *s); err != nil {
		t.Fatal(err)
	}
	link, err := ClientLink(handle, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(link, "vless://"+c.VLESSUUID+"@vpn.example.com:443?") {
		t.Fatalf("адрес не взят из endpoint: %s", link)
	}
	s.LinkAddress = "super-space.example"
	if err := db.SaveXraySettings(handle, *s); err != nil {
		t.Fatal(err)
	}
	link, _ = ClientLink(handle, c.ID)
	if !strings.Contains(link, "@super-space.example:443?") {
		t.Fatalf("явный адрес для ссылок не применён: %s", link)
	}
	if _, err := ClientLink(handle, 999); !errors.Is(err, db.ErrClientNotFound) {
		t.Fatalf("неизвестный клиент: %v", err)
	}
}

// Импорт ручной настройки: после него ссылка клиента из панели совпадает
// с выданной вручную до байта.
func TestImportManualSetup(t *testing.T) {
	handle := newDB(t)
	andrey := addClient(t, handle, "andrey", awgPub)
	mama := addClient(t, handle, "Мама телефон", awgPub2)
	other := addClient(t, handle, "other", awgPub3)

	res, err := Import(handle, filepath.Join("testdata", "manual"))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if res.Imported != 2 || len(res.Missing) != 1 || res.Missing[0] != 7 {
		t.Fatalf("итог импорта: %+v", res)
	}
	s, err := db.XraySettingsRow(handle)
	if err != nil {
		t.Fatal(err)
	}
	want := *fixtureSettings()
	if *s != want {
		t.Fatalf("настройки после импорта %+v, ожидались %+v", *s, want)
	}
	link, err := ClientLink(handle, andrey.ID)
	if err != nil {
		t.Fatal(err)
	}
	if want := "vless://3f2504e0-4f89-41d3-9a0c-0305e82c3301@vpn.example.com:443" +
		"?type=xhttp&path=%2Ff3a9c1d27e&mode=auto&security=reality&sni=vpn.example.com" +
		"&fp=chrome&pbk=u1D_noKldM-_gg6X9g-5wUPsdBXPUU-M_Zjv9Z4FlhQ&sid=6ba85179e30d4fc2" +
		"#VPN%20andrey"; link != want {
		t.Fatalf("ссылка после импорта:\n got %s\nwant %s", link, want)
	}
	got, _ := db.ClientByID(handle, mama.ID)
	if got.VLESSUUID != "9b2d7c1a-6e3f-4a5b-8c7d-1e2f3a4b5c6d" {
		t.Fatalf("UUID второго клиента %q", got.VLESSUUID)
	}
	untouched, _ := db.ClientByID(handle, other.ID)
	if untouched.VLESSUUID != other.VLESSUUID {
		t.Fatal("импорт тронул клиента, которого нет в users.json")
	}

	// Повторный импорт — то же состояние.
	if _, err := Import(handle, filepath.Join("testdata", "manual")); err != nil {
		t.Fatalf("повторный Import: %v", err)
	}
	again, _ := ClientLink(handle, andrey.ID)
	if again != link {
		t.Fatal("повторный импорт изменил ссылку")
	}
}

// Импорт сохраняет то, что в файлах не описано: адрес для ссылок, заданный
// в панели раньше, не сбрасывается.
func TestImportKeepsLinkAddress(t *testing.T) {
	handle := newDB(t)
	addClient(t, handle, "andrey", awgPub)
	s, _ := NewServerSettings("vpn.example.com")
	s.LinkAddress = "super-space.example"
	if err := db.SaveXraySettings(handle, *s); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(handle, filepath.Join("testdata", "manual")); err != nil {
		t.Fatal(err)
	}
	got, _ := db.XraySettingsRow(handle)
	if got.LinkAddress != "super-space.example" || got.PrivateKey != fixturePriv {
		t.Fatalf("после импорта: %+v", got)
	}
}

func copyFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, f := range []string{"users.json", "keys.txt", "shortid.txt", "path.txt"} {
		data, err := os.ReadFile(filepath.Join("testdata", "manual", f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, f), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// Испорченный каталог не меняет базу ни на байт: ни ключей, ни UUID.
func TestImportRejectsBrokenInput(t *testing.T) {
	cases := map[string]func(dir string){
		"публичный ключ от другой пары": func(dir string) {
			os.WriteFile(filepath.Join(dir, "keys.txt"), []byte("PrivateKey: "+fixturePriv+"\nPassword (PublicKey): "+strings.Repeat("A", 43)+"\n"), 0o600)
		},
		"нет приватного ключа": func(dir string) {
			os.WriteFile(filepath.Join(dir, "keys.txt"), []byte("Password (PublicKey): "+fixturePub+"\n"), 0o600)
		},
		"битый UUID": func(dir string) {
			os.WriteFile(filepath.Join(dir, "users.json"), []byte(`[{"client_id":1,"name":"andrey","uuid":"nope"}]`), 0o600)
		},
		"один UUID двоим": func(dir string) {
			os.WriteFile(filepath.Join(dir, "users.json"), []byte(`[
				{"client_id":1,"name":"andrey","uuid":"3f2504e0-4f89-41d3-9a0c-0305e82c3301"},
				{"client_id":2,"name":"Мама телефон","uuid":"3f2504e0-4f89-41d3-9a0c-0305e82c3301"}]`), 0o600)
		},
		"путь без слеша": func(dir string) {
			os.WriteFile(filepath.Join(dir, "path.txt"), []byte("abc\n"), 0o600)
		},
		"нет shortid.txt": func(dir string) {
			os.Remove(filepath.Join(dir, "shortid.txt"))
		},
		"users.json не массив": func(dir string) {
			os.WriteFile(filepath.Join(dir, "users.json"), []byte(`{}`), 0o600)
		},
	}
	for name, spoil := range cases {
		t.Run(name, func(t *testing.T) {
			handle := newDB(t)
			a := addClient(t, handle, "andrey", awgPub)
			addClient(t, handle, "Мама телефон", awgPub2)
			dir := copyFixture(t)
			spoil(dir)
			if _, err := Import(handle, dir); err == nil {
				t.Fatal("импорт принят")
			} else if strings.Contains(err.Error(), fixturePriv) {
				t.Fatal("приватный ключ попал в текст ошибки")
			}
			if _, err := db.XraySettingsRow(handle); !errors.Is(err, db.ErrXrayNotConfigured) {
				t.Fatalf("настройки записаны несмотря на ошибку: %v", err)
			}
			got, _ := db.ClientByID(handle, a.ID)
			if got.VLESSUUID != a.VLESSUUID {
				t.Fatal("UUID клиента изменён несмотря на ошибку")
			}
		})
	}
}

// Каталог /opt/xray-prod в нынешнем виде (settings.json + users.json с
// enabled/deleted): ссылки совпадают с links.tsv до байта — домен из
// link_host, self-steal sni, — служебная учётка входа переносится, удалённые
// клиенты пропускаются, а включённость решает панель.
func TestImportProdSettings(t *testing.T) {
	handle := newDB(t)
	andrey := addClient(t, handle, "andrey", awgPub)
	mama := addClient(t, handle, "Мама телефон", awgPub2)
	old := addClient(t, handle, "old-phone", awgPub3)

	res, err := Import(handle, filepath.Join("testdata", "prod"))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if res.Imported != 2 || res.Deleted != 1 || res.ServiceUsers != 1 || len(res.Missing) != 0 || len(res.Warnings) != 0 {
		t.Fatalf("итог импорта: %+v", res)
	}
	s, _ := db.XraySettingsRow(handle)
	want := db.XraySettings{
		PrivateKey: fixturePriv, PublicKey: fixturePub, ShortID: "6ba85179e30d4fc2", Path: "/f3a9c1d27e",
		Target: "127.0.0.1:4433", ServerName: "super-space.example", LinkAddress: "super-space.example", LinkPort: 443,
	}
	if *s != want {
		t.Fatalf("настройки %+v, ожидались %+v", *s, want)
	}
	link, _ := ClientLink(handle, andrey.ID)
	if wantLink := "vless://3f2504e0-4f89-41d3-9a0c-0305e82c3301@super-space.example:443" +
		"?type=xhttp&path=%2Ff3a9c1d27e&mode=auto&security=reality&sni=super-space.example" +
		"&fp=chrome&pbk=u1D_noKldM-_gg6X9g-5wUPsdBXPUU-M_Zjv9Z4FlhQ&sid=6ba85179e30d4fc2" +
		"#VPN%20andrey"; link != wantLink {
		t.Fatalf("ссылка:\n got %s\nwant %s", link, wantLink)
	}
	if got, _ := db.ClientByID(handle, mama.ID); got.VLESSUUID != "9b2d7c1a-6e3f-4a5b-8c7d-1e2f3a4b5c6d" || !got.Enabled {
		t.Fatalf("клиент с enabled=false в users.json: %+v (UUID переносится, включённость решает панель)", got)
	}
	if got, _ := db.ClientByID(handle, old.ID); got.VLESSUUID != old.VLESSUUID {
		t.Fatal("удалённая в users.json запись перенесена")
	}
	service, _ := db.XrayServiceUsers(handle)
	if len(service) != 1 || service[0] != (db.VLESSServiceUser{Email: "ruentry", UUID: "5a1d2c3b-4e5f-4a6b-8c7d-9e0f1a2b3c4d"}) {
		t.Fatalf("служебные учётки: %+v", service)
	}
}

// На боевом рядом с settings.json ещё лежат txt первых часов: побеждает
// settings.json. Расхождения входа с тем, что соберёт панель, не
// переносятся, но называются.
func TestImportPrefersSettingsJSON(t *testing.T) {
	handle := newDB(t)
	addClient(t, handle, "andrey", awgPub)
	dir := t.TempDir()
	for _, f := range []string{"settings.json", "users.json"} {
		data, _ := os.ReadFile(filepath.Join("testdata", "prod", f))
		if f == "settings.json" {
			data = []byte(strings.Replace(string(data), `"port": 4432`, `"port": 4500`, 1))
		}
		os.WriteFile(filepath.Join(dir, f), data, 0o600)
	}
	os.WriteFile(filepath.Join(dir, "shortid.txt"), []byte("aaaa\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "path.txt"), []byte("/legacy\n"), 0o600)
	res, err := Import(handle, dir)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := db.XraySettingsRow(handle)
	if s.ShortID != "6ba85179e30d4fc2" || s.Path != "/f3a9c1d27e" {
		t.Fatalf("txt перебили settings.json: %+v", s)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "4500") {
		t.Fatalf("расхождение порта не названо: %v", res.Warnings)
	}
}

// Self-steal требует домена: с IP в endpoint и без server_name в файлах
// импорт отказывается, а не подставляет чужое имя.
func TestImportLegacyNeedsDomain(t *testing.T) {
	handle, err := db.Open(filepath.Join(t.TempDir(), "amnezia.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	db.Migrate(handle)
	if err := db.CreateServer(handle, awgPriv, awgPub, "10.8.0.1/24", "", 51820, "", "{}", "203.0.113.7:51820"); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(handle, filepath.Join("testdata", "manual")); !errors.Is(err, ErrNoServerName) {
		t.Fatalf("импорт без домена: %v", err)
	}
	if _, err := DefaultServerName(handle); !errors.Is(err, ErrNoServerName) {
		t.Fatalf("DefaultServerName для IP: %v", err)
	}
}
