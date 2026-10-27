package cli

import (
	"net"
	"os"
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
		"?type=xhttp&path=%2Ff3a9c1d27e&mode=auto&security=reality&sni=vpn.example.com" +
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

// Self-steal требует домена: на сервере с IP в endpoint `xray init` без
// --server-name отказывается, а не подставляет чужое имя.
func TestXrayInitNeedsDomain(t *testing.T) {
	c := newCtx(t)
	c.mustRun("server", "init", testServerCIDR, "51820", "--endpoint", "203.0.113.7:51820")
	if code, _, errb := c.run("xray", "init"); code != 1 || !strings.Contains(errb, "domain") {
		t.Fatalf("xray init без домена: exit %d, %q", code, errb)
	}
	c.mustRun("xray", "init", "--server-name", "vpn.example.com")
	h := c.openDB()
	defer h.Close()
	s, err := db.XraySettingsRow(h)
	if err != nil {
		t.Fatal(err)
	}
	if s.ServerName != "vpn.example.com" || s.Target != "127.0.0.1:4433" {
		t.Fatalf("настройки: %+v", s)
	}
}

// Команды клиентов и panel init перегенерируют конфиг Xray вслед за
// awg0.conf; недоступный API Xray — предупреждение, а не отказ команды
// (amnezia-vpn-server-14y0.2).
func TestClientCommandsRegenerateXrayConfig(t *testing.T) {
	c := newCtx(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("AMNEZIA_XRAY_API_ADDR", ln.Addr().String())
	t.Setenv("AMNEZIA_XRAY_CONFIG_PATH", "")
	ln.Close()
	c.seedServer("", "")
	c.mustRun("xray", "init")
	xrayPath := filepath.Join(c.dir, "xray", "config.json")

	c.run("client", "add", "andrey")
	code, _, errb := c.run("client", "add", "mama")
	if code != 0 {
		t.Fatalf("client add при недоступном API Xray: exit %d, %s", code, errb)
	}
	if !strings.Contains(errb, "xray") {
		t.Fatalf("недоступный API Xray не отмечен: %q", errb)
	}
	cfg, err := os.ReadFile(xrayPath)
	if err != nil {
		t.Fatalf("конфиг Xray не записан: %v", err)
	}
	if !strings.Contains(string(cfg), `"email": "c2"`) {
		t.Fatalf("новый клиент не попал в конфиг Xray:\n%s", cfg)
	}

	if code, _, errb := c.run("client", "disable", "2"); code != 0 {
		t.Fatalf("client disable: %d %s", code, errb)
	}
	cfg, _ = os.ReadFile(xrayPath)
	if strings.Contains(string(cfg), `"email": "c2"`) {
		t.Fatal("отключённый клиент остался в конфиге Xray")
	}

	os.Remove(xrayPath)
	if code, _, errb := c.run("init"); code != 0 {
		t.Fatalf("panel init: %d %s", code, errb)
	}
	if cfg, err := os.ReadFile(xrayPath); err != nil || !strings.Contains(string(cfg), `"email": "c1"`) {
		t.Fatalf("panel init не записал конфиг Xray: %v", err)
	}
}

// Без настроек Xray panel init ведёт себя как прежде: ни файла, ни
// предупреждений.
func TestInitWithoutXray(t *testing.T) {
	c := newCtx(t)
	c.seedServer("", "")
	c.seedClient("andrey")
	c.mustRun("init")
	if _, err := os.Stat(filepath.Join(c.dir, "xray")); !os.IsNotExist(err) {
		t.Fatalf("каталог Xray без настроек: %v", err)
	}
}
