package web

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

// Выданный конфиг несёт оба адреса, значит и панель должна показывать оба:
// иначе владелец не видит того, что роздал (amnezia-vpn-server-lhlv).
func TestAPIClientReportsIPv6Address(t *testing.T) {
	f := newFixture(t)
	c, _, _ := f.addClient("dual-stack")

	// Туннель без IPv6 — поля нет, и это нормальное состояние.
	got := decodeAPI(t, f.get(fmt.Sprintf("/api/clients/%d", c.ID)))
	if got["address6"] != "" {
		t.Fatalf("без IPv6 в туннеле address6 = %v, ожидалась пустая строка", got["address6"])
	}

	// Даём туннелю префикс — адрес клиента выводится из него арифметикой.
	addr6 := "fd42:a11e:c0de::1/64"
	if _, err := db.UpdateServer(f.h, nil, nil, nil, &addr6, nil); err != nil {
		t.Fatalf("UpdateServer: %v", err)
	}
	got = decodeAPI(t, f.get(fmt.Sprintf("/api/clients/%d", c.ID)))
	want := "fd42:a11e:c0de::2/128"
	if got["address6"] != want {
		t.Fatalf("address6 = %v, ожидалось %q", got["address6"], want)
	}

	// И в списке тоже: владелец смотрит именно туда.
	rec := f.get("/api/clients")
	if rec.Code != http.StatusOK {
		t.Fatalf("список клиентов: код %d", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, want) {
		t.Fatalf("список без адреса IPv6: %s", body)
	}
}
