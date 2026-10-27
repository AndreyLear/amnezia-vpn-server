package db

import (
	"errors"
	"testing"
	"time"
)

// Новый клиент сразу получает VLESS UUID — свой у каждого
// (amnezia-vpn-server-14y0.1).
func TestCreateClientAssignsVLESSUUID(t *testing.T) {
	handle, _ := openTest(t, "amnezia.sqlite")
	if err := Migrate(handle); err != nil {
		t.Fatal(err)
	}
	a, err := CreateClient(handle, "10.8.0.1/24", NewClient{Name: "a", PrivateKey: testPriv, PublicKey: testPub})
	if err != nil {
		t.Fatal(err)
	}
	b, err := CreateClient(handle, "10.8.0.1/24", NewClient{Name: "b", PrivateKey: testPriv, PublicKey: testPub2})
	if err != nil {
		t.Fatal(err)
	}
	if !ValidVLESSUUID(a.VLESSUUID) || !ValidVLESSUUID(b.VLESSUUID) {
		t.Fatalf("UUID не выдан или не в каноническом виде: %q, %q", a.VLESSUUID, b.VLESSUUID)
	}
	if a.VLESSUUID == b.VLESSUUID {
		t.Fatal("два клиента получили один UUID")
	}
	got, err := ClientByID(handle, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.VLESSUUID != a.VLESSUUID {
		t.Fatalf("в базе %q, вернули %q", got.VLESSUUID, a.VLESSUUID)
	}
}

func TestNewVLESSUUIDIsVersion4(t *testing.T) {
	for i := 0; i < 50; i++ {
		u, err := NewVLESSUUID()
		if err != nil {
			t.Fatal(err)
		}
		if !ValidVLESSUUID(u) || u[14] != '4' || !stringsContainsByte("89ab", u[19]) {
			t.Fatalf("не UUIDv4: %q", u)
		}
	}
}

func stringsContainsByte(s string, b byte) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return true
		}
	}
	return false
}

// База, заведённая до VLESS: миграция без потерь даёт UUID каждому
// существующему клиенту, а повторная — ничего не меняет.
func TestMigrateAssignsVLESSUUIDToExistingClients(t *testing.T) {
	handle, _ := openTest(t, "amnezia.sqlite")
	if err := Migrate(handle); err != nil {
		t.Fatal(err)
	}
	if _, err := handle.Exec(`DROP INDEX idx_clients_vless_uuid`); err != nil {
		t.Fatal(err)
	}
	if _, err := handle.Exec(`ALTER TABLE clients DROP COLUMN vless_uuid`); err != nil {
		t.Skipf("this SQLite build cannot DROP COLUMN: %v", err)
	}
	if _, err := handle.Exec(`UPDATE schema_meta SET value = '10' WHERE key = 'schema_version'`); err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"alice", "bob", "carol"} {
		if _, err := handle.Exec(
			`INSERT INTO clients (name, private_key, public_key, address, enabled, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
			name, testPriv, name+"-pub", "10.8.0."+string(rune('2'+i))+"/32", i%2,
		); err != nil {
			t.Fatal(err)
		}
	}

	if err := Migrate(handle); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	first, err := ClientsAll(handle)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 3 {
		t.Fatalf("клиентов после миграции %d, ожидалось 3", len(first))
	}
	seen := map[string]bool{}
	for _, c := range first {
		if !ValidVLESSUUID(c.VLESSUUID) {
			t.Fatalf("клиент %s без UUID после миграции: %q", c.Name, c.VLESSUUID)
		}
		if seen[c.VLESSUUID] {
			t.Fatalf("UUID %s выдан дважды", c.VLESSUUID)
		}
		seen[c.VLESSUUID] = true
	}

	if err := Migrate(handle); err != nil {
		t.Fatal(err)
	}
	second, err := ClientsAll(handle)
	if err != nil {
		t.Fatal(err)
	}
	for i := range first {
		if first[i].VLESSUUID != second[i].VLESSUUID {
			t.Fatalf("повторная миграция сменила UUID клиента %s", first[i].Name)
		}
	}
	if v, _ := SchemaVersionStored(handle); v != SchemaVersion {
		t.Fatalf("schema_version = %q, ожидалось %q", v, SchemaVersion)
	}
}

// Клиент, созданный прежней версией панели после отката (без UUID), тоже
// получает свой при следующей миграции.
func TestMigrateFillsUUIDForClientsCreatedByOlderRelease(t *testing.T) {
	handle, _ := openTest(t, "amnezia.sqlite")
	if err := Migrate(handle); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"x", "y"} {
		if _, err := handle.Exec(
			`INSERT INTO clients (name, private_key, public_key, address, created_at, updated_at)
			 VALUES (?, ?, ?, ?, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
			name, testPriv, name+"-pub", "10.8.0.1"+name+"/32",
		); err != nil {
			t.Fatalf("старая версия не смогла бы создать клиента: %v", err)
		}
	}
	if err := Migrate(handle); err != nil {
		t.Fatal(err)
	}
	all, err := ClientsAll(handle)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[0].VLESSUUID == "" || all[1].VLESSUUID == "" || all[0].VLESSUUID == all[1].VLESSUUID {
		t.Fatalf("UUID после миграции: %+v", all)
	}
}

// Отключение и удаление клиента действуют и на VLESS: в конфиг Xray
// попадают ровно те, кто попадает в awg0.conf.
func TestVLESSClientsFollowClientState(t *testing.T) {
	handle, _ := openTest(t, "amnezia.sqlite")
	if err := Migrate(handle); err != nil {
		t.Fatal(err)
	}
	mk := func(name, pub string) *ClientRecord {
		c, err := CreateClient(handle, "10.8.0.1/24", NewClient{Name: name, PrivateKey: testPriv, PublicKey: pub})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	on := mk("on", testPub)
	off := mk("off", testPub2)
	gone := mk("gone", "mK1lOO8QY2KQq7j7MEpJ8cB6x1u8m3gqYlQ1+o2LtQ0=")
	old := mk("old", "TbXk2bLCRmIQJ8bS1R3k3dVlK8b6l1T7JqYfV3QeVHE=")
	if err := SetClientEnabled(handle, off.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := DeleteClient(handle, gone.ID); err != nil {
		t.Fatal(err)
	}
	if err := SetClientExpiry(handle, old.ID, time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	got, err := VLESSClientsForConfig(handle)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != on.ID || got[0].UUID != on.VLESSUUID || got[0].Name != "on" {
		t.Fatalf("VLESS-клиенты = %+v, ожидался только %q", got, on.Name)
	}
	if err := SetClientEnabled(handle, off.ID, true); err != nil {
		t.Fatal(err)
	}
	got, _ = VLESSClientsForConfig(handle)
	if len(got) != 2 || got[1].UUID != off.VLESSUUID {
		t.Fatalf("включённый обратно клиент не вернулся в VLESS: %+v", got)
	}
}

func TestSetClientVLESSUUID(t *testing.T) {
	handle, _ := openTest(t, "amnezia.sqlite")
	if err := Migrate(handle); err != nil {
		t.Fatal(err)
	}
	a, _ := CreateClient(handle, "10.8.0.1/24", NewClient{Name: "a", PrivateKey: testPriv, PublicKey: testPub})
	b, _ := CreateClient(handle, "10.8.0.1/24", NewClient{Name: "b", PrivateKey: testPriv, PublicKey: testPub2})
	const want = "0f8b6a3e-7f1c-4b8e-9d4a-2c5e6f7a8b9c"
	if err := SetClientVLESSUUID(handle, a.ID, want); err != nil {
		t.Fatal(err)
	}
	got, _ := ClientByID(handle, a.ID)
	if got.VLESSUUID != want {
		t.Fatalf("UUID = %q, ожидался %q", got.VLESSUUID, want)
	}
	if err := SetClientVLESSUUID(handle, b.ID, want); !errors.Is(err, ErrVLESSUUIDTaken) {
		t.Fatalf("чужой UUID принят: %v", err)
	}
	if err := SetClientVLESSUUID(handle, a.ID, "0F8B6A3E-7F1C-4B8E-9D4A-2C5E6F7A8B9C"); err == nil {
		t.Fatal("UUID в верхнем регистре принят: ссылка разошлась бы с выданной")
	}
	if err := SetClientVLESSUUID(handle, 999, "1f8b6a3e-7f1c-4b8e-9d4a-2c5e6f7a8b9c"); !errors.Is(err, ErrClientNotFound) {
		t.Fatalf("неизвестный клиент: %v", err)
	}
}

func TestXraySettingsRoundTrip(t *testing.T) {
	handle, _ := openTest(t, "amnezia.sqlite")
	if err := Migrate(handle); err != nil {
		t.Fatal(err)
	}
	if _, err := XraySettingsRow(handle); !errors.Is(err, ErrXrayNotConfigured) {
		t.Fatalf("без строки xray: %v, ожидалось ErrXrayNotConfigured", err)
	}
	want := XraySettings{
		PrivateKey: "priv", PublicKey: "pub", ShortID: "6ba85179e30d4fc2", Path: "/a1b2c3",
		Target: XrayDefaultTarget, ServerName: "vpn.example.com", LinkPort: 443,
	}
	if err := SaveXraySettings(handle, want); err != nil {
		t.Fatal(err)
	}
	want.LinkAddress = "vpn.example.com"
	if err := SaveXraySettings(handle, want); err != nil {
		t.Fatal(err)
	}
	got, err := XraySettingsRow(handle)
	if err != nil {
		t.Fatal(err)
	}
	if *got != want {
		t.Fatalf("прочитано %+v, записано %+v", *got, want)
	}
	for name, bad := range map[string]XraySettings{
		"нечётный shortId":  {PrivateKey: "p", PublicKey: "q", ShortID: "abc", Path: "/x", Target: "t", ServerName: "s", LinkPort: 443},
		"shortId длиннее":   {PrivateKey: "p", PublicKey: "q", ShortID: "00112233445566778899", Path: "/x", Target: "t", ServerName: "s", LinkPort: 443},
		"путь без слеша":    {PrivateKey: "p", PublicKey: "q", ShortID: "ab", Path: "x", Target: "t", ServerName: "s", LinkPort: 443},
		"путь с пробелом":   {PrivateKey: "p", PublicKey: "q", ShortID: "ab", Path: "/a b", Target: "t", ServerName: "s", LinkPort: 443},
		"порт ноль":         {PrivateKey: "p", PublicKey: "q", ShortID: "ab", Path: "/x", Target: "t", ServerName: "s"},
		"без ключа":         {PublicKey: "q", ShortID: "ab", Path: "/x", Target: "t", ServerName: "s", LinkPort: 443},
		"без имени для SNI": {PrivateKey: "p", PublicKey: "q", ShortID: "ab", Path: "/x", Target: "t", LinkPort: 443},
	} {
		if err := SaveXraySettings(handle, bad); err == nil {
			t.Errorf("%s: принято", name)
		}
	}
}

// Служебные учётки (вход «ruentry») переносятся импортом, заменяют прежние
// целиком и не делят UUID с клиентами.
func TestImportVLESSServiceUsers(t *testing.T) {
	handle, _ := openTest(t, "amnezia.sqlite")
	if err := Migrate(handle); err != nil {
		t.Fatal(err)
	}
	a, _ := CreateClient(handle, "10.8.0.1/24", NewClient{Name: "a", PrivateKey: testPriv, PublicKey: testPub})
	settings := XraySettings{
		PrivateKey: "priv", PublicKey: "pub", ShortID: "ab", Path: "/x",
		Target: XrayDefaultTarget, ServerName: "vpn.example.com", LinkPort: 443,
	}
	const ruentry = "5a1d2c3b-4e5f-4a6b-8c7d-9e0f1a2b3c4d"
	in := VLESSImport{
		Settings:     settings,
		Clients:      map[int64]string{a.ID: "0f8b6a3e-7f1c-4b8e-9d4a-2c5e6f7a8b9c"},
		ServiceUsers: []VLESSServiceUser{{Email: "ruentry", UUID: ruentry}},
	}
	if _, _, err := ImportVLESS(handle, in); err != nil {
		t.Fatal(err)
	}
	got, err := XrayServiceUsers(handle)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != (VLESSServiceUser{"ruentry", ruentry}) {
		t.Fatalf("служебные учётки: %+v", got)
	}
	if err := SetClientVLESSUUID(handle, a.ID, ruentry); !errors.Is(err, ErrVLESSUUIDTaken) {
		t.Fatalf("клиент получил UUID служебной учётки: %v", err)
	}

	for name, bad := range map[string][]VLESSServiceUser{
		"email клиента":  {{Email: "c7", UUID: ruentry}},
		"UUID клиента":   {{Email: "ruentry", UUID: "0f8b6a3e-7f1c-4b8e-9d4a-2c5e6f7a8b9c"}},
		"битый UUID":     {{Email: "ruentry", UUID: "nope"}},
		"дважды":         {{Email: "x", UUID: ruentry}, {Email: "x", UUID: "6a1d2c3b-4e5f-4a6b-8c7d-9e0f1a2b3c4d"}},
		"пробел в email": {{Email: "ru entry", UUID: ruentry}},
	} {
		in.ServiceUsers = bad
		if _, _, err := ImportVLESS(handle, in); err == nil {
			t.Errorf("%s: принято", name)
		}
	}
	got, _ = XrayServiceUsers(handle)
	if len(got) != 1 || got[0].Email != "ruentry" {
		t.Fatalf("отвергнутый импорт изменил служебные учётки: %+v", got)
	}

	in.ServiceUsers = nil
	if _, _, err := ImportVLESS(handle, in); err != nil {
		t.Fatal(err)
	}
	if got, _ = XrayServiceUsers(handle); len(got) != 0 {
		t.Fatalf("импорт без служебных учёток оставил прежние: %+v", got)
	}
}
