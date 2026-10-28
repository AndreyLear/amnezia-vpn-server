package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/xray"
)

const testEntryPub = "u1D_noKldM-_gg6X9g-5wUPsdBXPUU-M_Zjv9Z4FlhQ"

// Узел-вход заводится и меняется из CLI, ссылка через него — тот же UUID
// клиента с параметрами узла и приставкой «RU » (amnezia-vpn-server-14y0.6).
func TestXrayEntryCommands(t *testing.T) {
	c := newCtx(t)
	c.seedServer("", "")
	id, _, _, _ := c.seedClient("andrey")

	if code, _, _ := c.run("xray", "entry", "set", "ru", "--host", "ru.example.com"); code != 2 {
		t.Fatalf("новый узел без --sni и ключа: exit %d, ожидался 2", code)
	}
	c.mustRun("xray", "entry", "set", "ru", "--host", "ru.example.com", "--sni", "ru.example.com",
		"--public-key", testEntryPub, "--short-id", "a1b2", "--path", "/entry", "--prefix", "RU ")
	c.mustRun("xray", "entry", "set", "ru", "--port", "8443")

	list := c.mustRun("xray", "entry", "list")
	if !strings.Contains(list, "ru\tru.example.com\t8443\t") || !strings.Contains(list, `"RU "`) {
		t.Fatalf("список узлов:\n%s", list)
	}
	uuid := c.clientByID(id).VLESSUUID
	link := c.mustRun("xray", "link", "1", "--entry", "ru")
	want := "vless://" + uuid + "@ru.example.com:8443?type=xhttp&path=%2Fentry&mode=auto&security=reality" +
		"&sni=ru.example.com&fp=chrome&pbk=" + testEntryPub + "&sid=a1b2#RU%20andrey\n"
	if link != want {
		t.Fatalf("ссылка через вход:\n got %q\nwant %q", link, want)
	}
	if code, _, _ := c.run("xray", "link", "1", "--entry", "nope"); code != 1 {
		t.Fatalf("неизвестный узел: exit %d", code)
	}
	c.mustRun("xray", "entry", "remove", "ru")
	if code, _, _ := c.run("xray", "entry", "remove", "ru"); code != 1 {
		t.Fatalf("повторное удаление: exit %d", code)
	}
}

// users-json — то, что читает build.py на входе.
func TestXrayUsersJSON(t *testing.T) {
	c := newCtx(t)
	c.seedServer("", "")
	a, _, _, _ := c.seedClient("andrey")
	b, _, _, _ := c.seedClient("mama")
	c.mustRun("client", "disable", "2")

	var got []map[string]any
	if err := json.Unmarshal([]byte(c.mustRun("xray", "users-json")), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("выгрузка: %v", got)
	}
	if got[0]["client_id"] != float64(a) || got[0]["name"] != "andrey" ||
		got[0]["uuid"] != c.clientByID(a).VLESSUUID || got[0]["enabled"] != true {
		t.Fatalf("первая запись: %v", got[0])
	}
	if got[1]["client_id"] != float64(b) || got[1]["enabled"] != false {
		t.Fatalf("отключённый клиент: %v", got[1])
	}
	if len(got[0]) != 4 {
		t.Fatalf("лишние поля: %v", got[0])
	}
}

// Секрет узла для автосинхронизации (amnezia-vpn-server-14y0.6): новый узел
// получает его при `entry set`, показывается он один раз, правка полей его
// не меняет, `entry token` выдаёт новый взамен прежнего.
func TestXrayEntryToken(t *testing.T) {
	c := newCtx(t)
	c.seedServer("", "")

	out := c.mustRun("xray", "entry", "set", "ru", "--host", "ru.example.com", "--sni", "ru.example.com",
		"--public-key", testEntryPub, "--short-id", "a1b2", "--path", "/entry", "--prefix", "RU ")
	first := entryTokenFrom(t, out)
	if name, err := xray.AuthenticateEntry(c.openDB(), first); err != nil || name != "ru" {
		t.Fatalf("секрет из вывода не принят: %q, %v", name, err)
	}
	if out := c.mustRun("xray", "entry", "set", "ru", "--port", "8443"); strings.Contains(out, "token") {
		t.Fatalf("правка узла показала секрет снова:\n%s", out)
	}
	if !strings.Contains(c.mustRun("xray", "entry", "list"), "\tyes\n") {
		t.Fatal("в списке не видно, что у узла есть секрет")
	}

	second := entryTokenFrom(t, c.mustRun("xray", "entry", "token", "ru"))
	if second == first {
		t.Fatal("новый секрет совпал с прежним")
	}
	if _, err := xray.AuthenticateEntry(c.openDB(), first); err == nil {
		t.Fatal("прежний секрет работает после замены")
	}
	if code, _, _ := c.run("xray", "entry", "token", "nope"); code != 1 {
		t.Fatalf("секрет неизвестному узлу: exit %d", code)
	}
	if code, _, _ := c.run("xray", "entry", "token"); code != 2 {
		t.Fatalf("без имени: exit %d", code)
	}
}

// Узел, заведённый до автосинхронизации, получает секрет первой же
// правкой: так переводится уже работающий вход.
func TestXrayEntrySetGivesLegacyEntryAToken(t *testing.T) {
	c := newCtx(t)
	c.seedServer("", "")
	if err := db.SaveXrayEntry(c.openDB(), db.XrayEntry{Name: "ru", LinkHost: "ru.example.com", LinkPort: 443,
		SNI: "ru.example.com", PublicKey: testEntryPub, ShortID: "a1b2", Path: "/entry"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c.mustRun("xray", "entry", "list"), "\tno\n") {
		t.Fatal("в списке не видно, что секрета нет")
	}
	token := entryTokenFrom(t, c.mustRun("xray", "entry", "set", "ru", "--prefix", "RU "))
	if name, err := xray.AuthenticateEntry(c.openDB(), token); err != nil || name != "ru" {
		t.Fatalf("секрет не принят: %q, %v", name, err)
	}
}

// entryTokenFrom — секрет из строки «… token …: <секрет>».
func entryTokenFrom(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "token") {
			f := strings.Fields(line)
			return f[len(f)-1]
		}
	}
	t.Fatalf("секрета нет в выводе:\n%s", out)
	return ""
}
