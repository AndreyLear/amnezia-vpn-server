package xray

import (
	"strings"
	"testing"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

// Подписи ссылок (решение владельца 28.10.2026): прямая — «VLESS direct
// <имя>», приставка прямых ссылок — в настройках Xray сервера.
func TestDirectLinkPrefix(t *testing.T) {
	handle := newDB(t)
	c := addClient(t, handle, "andrey", awgPub)
	s, err := NewServerSettings("vpn.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if s.LinkPrefix != "VLESS direct " {
		t.Fatalf("приставка по умолчанию %q", s.LinkPrefix)
	}
	if err := db.SaveXraySettings(handle, *s); err != nil {
		t.Fatal(err)
	}
	link, err := ClientLink(handle, c.ID)
	if err != nil || !strings.HasSuffix(link, "#VLESS%20direct%20andrey") {
		t.Fatalf("прямая ссылка %q, %v", link, err)
	}
	s.LinkPrefix = "Home "
	if err := db.SaveXraySettings(handle, *s); err != nil {
		t.Fatal(err)
	}
	if link, _ = ClientLink(handle, c.ID); !strings.HasSuffix(link, "#Home%20andrey") {
		t.Fatalf("своя приставка: %q", link)
	}
}
