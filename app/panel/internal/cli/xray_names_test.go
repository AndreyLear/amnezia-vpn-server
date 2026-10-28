package cli

import (
	"strings"
	"testing"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/xray"
)

// Новый узел без --prefix подписывает ссылки «VLESS <имя>»; узел
// переименовывается с секретом и параметрами (решение владельца 28.10.2026).
func TestXrayEntryDefaultPrefixAndRename(t *testing.T) {
	c := newCtx(t)
	c.seedServer("", "")
	c.seedClient("andrey")
	token := entryTokenFrom(t, c.mustRun("xray", "entry", "set", "ru", "--host", "ru.example.com", "--sni", "ru.example.com",
		"--public-key", testEntryPub, "--short-id", "a1b2", "--path", "/entry"))
	if link := c.mustRun("xray", "link", "1", "--entry", "ru"); !strings.HasSuffix(link, "#VLESS%20andrey\n") {
		t.Fatalf("ссылка через новый узел: %q", link)
	}
	if out := c.mustRun("xray", "entry", "rename", "ru", "entry"); strings.Contains(out, "token") {
		t.Fatalf("переименование показало секрет: %q", out)
	}
	if name, err := xray.AuthenticateEntry(c.openDB(), token); err != nil || name != "entry" {
		t.Fatalf("секрет после переименования: %q, %v", name, err)
	}
	if code, _, _ := c.run("xray", "link", "1", "--entry", "ru"); code != 1 {
		t.Fatalf("старое имя работает: exit %d", code)
	}
	if code, _, _ := c.run("xray", "entry", "rename", "nope", "x"); code != 1 {
		t.Fatalf("неизвестный узел: exit %d", code)
	}
	if code, _, _ := c.run("xray", "entry", "rename", "entry"); code != 2 {
		t.Fatalf("без нового имени: exit %d", code)
	}
}

// Приставка прямых ссылок задаётся `xray init --link-prefix`.
func TestXrayInitLinkPrefix(t *testing.T) {
	c := newCtx(t)
	c.seedServer("", "")
	c.seedClient("andrey")
	c.mustRun("xray", "init", "--server-name", "vpn.example.com")
	if link := c.mustRun("xray", "link", "1"); !strings.HasSuffix(link, "#VLESS%20direct%20andrey\n") {
		t.Fatalf("прямая ссылка по умолчанию: %q", link)
	}
	c.mustRun("xray", "init", "--link-prefix", "VPN ")
	if link := c.mustRun("xray", "link", "1"); !strings.HasSuffix(link, "#VPN%20andrey\n") {
		t.Fatalf("прямая ссылка со своей приставкой: %q", link)
	}
	if code, _, _ := c.run("xray", "init", "--link-prefix", "a\nb"); code == 0 {
		t.Fatal("принята приставка с переводом строки")
	}
}
