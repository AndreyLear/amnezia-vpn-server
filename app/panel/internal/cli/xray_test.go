package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

// Каталог ручной настройки с боевого в том виде, в каком он лежит в
// /opt/xray-prod (amnezia-vpn-server-adaw).
var manualXrayDir = filepath.Join("..", "xray", "testdata", "manual")

// `panel xray import` переносит ручную настройку так, что ссылка клиента
// из панели совпадает с выданной вручную до байта (amnezia-vpn-server-14y0.1).
func TestXrayImportKeepsIssuedLinks(t *testing.T) {
	c := newCtx(t)
	c.seedServer("", "")
	c.seedClient("andrey")
	c.seedClient("Мама телефон")

	out := c.mustRun("xray", "import", manualXrayDir)
	if !strings.Contains(out, "2 clients") || !strings.Contains(out, "7") {
		t.Fatalf("вывод импорта: %q", out)
	}
	link := c.mustRun("xray", "link", "1")
	want := "vless://3f2504e0-4f89-41d3-9a0c-0305e82c3301@vpn.example.com:443" +
		"?type=xhttp&path=%2Ff3a9c1d27e&mode=auto&security=reality&sni=www.microsoft.com" +
		"&fp=chrome&pbk=u1D_noKldM-_gg6X9g-5wUPsdBXPUU-M_Zjv9Z4FlhQ&sid=6ba85179e30d4fc2" +
		"#VPN%20andrey\n"
	if link != want {
		t.Fatalf("ссылка:\n got %q\nwant %q", link, want)
	}
	if strings.Contains(out, "AAoRGB8mLTQ7") {
		t.Fatal("приватный ключ Reality попал в вывод")
	}
}

func TestXrayInitGeneratesKeysOnce(t *testing.T) {
	c := newCtx(t)
	c.seedServer("", "")
	id, _, _, _ := c.seedClient("andrey")

	if code, _, _ := c.run("xray", "link", "1"); code != 1 {
		t.Fatalf("ссылка без настроек Xray: exit %d, ожидался 1", code)
	}
	c.mustRun("xray", "init")
	h := c.openDB()
	first, err := db.XraySettingsRow(h)
	h.Close()
	if err != nil {
		t.Fatalf("xray init не записал настройки: %v", err)
	}

	// Повторный init не трогает ключи: иначе все выданные ссылки умерли бы.
	c.mustRun("xray", "init", "--address", "super-space.example")
	h = c.openDB()
	second, _ := db.XraySettingsRow(h)
	h.Close()
	if second.PrivateKey != first.PrivateKey || second.ShortID != first.ShortID || second.Path != first.Path {
		t.Fatal("повторный xray init сменил ключи")
	}
	if second.LinkAddress != "super-space.example" {
		t.Fatalf("адрес для ссылок %q", second.LinkAddress)
	}
	link := c.mustRun("xray", "link", "1")
	rec := c.clientByID(id)
	if !strings.HasPrefix(link, "vless://"+rec.VLESSUUID+"@super-space.example:443?") {
		t.Fatalf("ссылка %q", link)
	}
}

func TestXrayUsage(t *testing.T) {
	c := newCtx(t)
	for _, args := range [][]string{
		{"xray"},
		{"xray", "nope"},
		{"xray", "import"},
		{"xray", "link"},
		{"xray", "link", "x"},
		{"xray", "init", "--bogus", "1"},
	} {
		if code, _, _ := c.run(args...); code != 2 {
			t.Errorf("panel %v: exit %d, ожидался 2", args, code)
		}
	}
}
