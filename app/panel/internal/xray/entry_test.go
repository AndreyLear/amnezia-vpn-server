package xray

import (
	"errors"
	"testing"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

// Узел-вход, как на 62.109.1.44: свои Reality и путь, приставка «RU ».
func ruEntry() db.XrayEntry {
	return db.XrayEntry{
		Name: "ru", LinkHost: "ru.example.com", LinkPort: 443, SNI: "ru.example.com",
		PublicKey: fixturePub, ShortID: "a1b2", Path: "/entry", LinkPrefix: "RU ",
	}
}

// Ссылка через вход — тот же UUID, параметры узла и имя с его приставкой:
// так ссылки выдаются на входе сейчас (amnezia-vpn-server-14y0.6).
func TestEntryLinkFormat(t *testing.T) {
	e := ruEntry()
	got := EntryLink(&e, db.VLESSClient{Name: "Мама", UUID: "3f2504e0-4f89-41d3-9a0c-0305e82c3301"})
	want := "vless://3f2504e0-4f89-41d3-9a0c-0305e82c3301@ru.example.com:443" +
		"?type=xhttp&path=%2Fentry&mode=auto&security=reality&sni=ru.example.com" +
		"&fp=chrome&pbk=u1D_noKldM-_gg6X9g-5wUPsdBXPUU-M_Zjv9Z4FlhQ&sid=a1b2" +
		"#RU%20%D0%9C%D0%B0%D0%BC%D0%B0"
	if got != want {
		t.Fatalf("\n got %s\nwant %s", got, want)
	}
}

func TestClientRoutes(t *testing.T) {
	handle := newDB(t)
	c := addClient(t, handle, "andrey", awgPub)

	routes, err := ClientRoutes(handle, c.ID)
	if err != nil || len(routes) != 0 {
		t.Fatalf("без Xray и узлов: %v %v", routes, err)
	}
	if err := db.SaveXraySettings(handle, *fixtureSettings()); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveXrayEntry(handle, ruEntry()); err != nil {
		t.Fatal(err)
	}
	routes, err = ClientRoutes(handle, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	direct, _ := ClientLink(handle, c.ID)
	if len(routes) != 2 || routes[0] != (Route{Link: direct}) || routes[1].Entry != "ru" {
		t.Fatalf("маршруты: %+v", routes)
	}

	// Отключённому и истёкшему — ни одной ссылки.
	if err := db.SetClientEnabled(handle, c.ID, false); err != nil {
		t.Fatal(err)
	}
	if routes, _ = ClientRoutes(handle, c.ID); len(routes) != 0 {
		t.Fatalf("отключённому выданы ссылки: %+v", routes)
	}
	db.SetClientEnabled(handle, c.ID, true)
	db.SetClientExpiry(handle, c.ID, time.Now().Add(-time.Hour).UTC().Format(time.RFC3339))
	if routes, _ = ClientRoutes(handle, c.ID); len(routes) != 0 {
		t.Fatalf("истёкшему выданы ссылки: %+v", routes)
	}
	if _, err := ClientRoutes(handle, 999); !errors.Is(err, db.ErrClientNotFound) {
		t.Fatalf("неизвестный клиент: %v", err)
	}
}

func TestXrayEntryStore(t *testing.T) {
	handle := newDB(t)
	e := ruEntry()
	if err := db.SaveXrayEntry(handle, e); err != nil {
		t.Fatal(err)
	}
	e.LinkPrefix = "RU2 "
	if err := db.SaveXrayEntry(handle, e); err != nil {
		t.Fatal(err)
	}
	got, err := db.XrayEntryByName(handle, "ru")
	if err != nil || *got != e {
		t.Fatalf("узел %+v, %v", got, err)
	}
	for name, mut := range map[string]func(*db.XrayEntry){
		"имя с заглавной": func(e *db.XrayEntry) { e.Name = "RU" },
		"порт ноль":       func(e *db.XrayEntry) { e.LinkPort = 0 },
		"битый ключ":      func(e *db.XrayEntry) { e.PublicKey = "AAAA" },
		"путь без слеша":  func(e *db.XrayEntry) { e.Path = "x" },
		"нечётный sid":    func(e *db.XrayEntry) { e.ShortID = "abc" },
		"sni с портом":    func(e *db.XrayEntry) { e.SNI = "a.b:443" },
		"перевод строки":  func(e *db.XrayEntry) { e.LinkPrefix = "RU\n" },
	} {
		bad := ruEntry()
		mut(&bad)
		if err := db.SaveXrayEntry(handle, bad); err == nil {
			t.Errorf("%s: принято", name)
		}
	}
	if err := db.DeleteXrayEntry(handle, "ru"); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteXrayEntry(handle, "ru"); !errors.Is(err, db.ErrXrayEntryNotFound) {
		t.Fatalf("повторное удаление: %v", err)
	}
}

// Выгрузка для build.py на входе: все клиенты с UUID, enabled — по тем же
// правилам, что и конфиг.
func TestVLESSUsersExport(t *testing.T) {
	handle := newDB(t)
	a := addClient(t, handle, "andrey", awgPub)
	b := addClient(t, handle, "mama", awgPub2)
	db.SetClientEnabled(handle, b.ID, false)
	got, err := db.VLESSUsersExport(handle)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != (db.VLESSUserExport{ClientID: a.ID, Name: "andrey", UUID: a.VLESSUUID, Enabled: true}) ||
		got[1].Enabled {
		t.Fatalf("выгрузка: %+v", got)
	}
}
