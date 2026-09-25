package cli

import (
	"strings"
	"testing"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

// Клиент и срок пишутся одной транзакцией: срок, записанный отдельно после
// вставки, при сбое (SQLITE_BUSY от параллельного веба) оставлял клиента
// включённым и бессрочным — при следующей перегенерации он получал доступ
// навсегда (amnezia-vpn-server-76mp.24).
//
// Сбой отдельной записи срока изображает триггер, который её отвергает.
func TestClientAddWritesExpiryWithTheClient(t *testing.T) {
	c := newCtx(t)
	c.seedServer("", "")
	h := c.openDB()
	if _, err := h.Exec(`CREATE TRIGGER refuse_expiry_update BEFORE UPDATE OF expires_at ON clients
		BEGIN SELECT RAISE(ABORT, 'database is locked'); END`); err != nil {
		t.Fatal(err)
	}
	h.Close()

	code, _, errb := c.run("client", "add", "phone", "--expires-at", "2030-01-01T00:00:00Z")

	h = c.openDB()
	defer h.Close()
	clients, err := db.ClientsAll(h)
	if err != nil {
		t.Fatal(err)
	}
	for _, cl := range clients {
		if cl.Name == "phone" && cl.ExpiresAt == "" {
			t.Fatalf("клиент создан без срока (exit %d, stderr %q)", code, errb)
		}
	}
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errb)
	}
	if len(clients) != 1 || !strings.HasPrefix(clients[0].ExpiresAt, "2030-01-01T00:00:00") {
		t.Fatalf("клиенты после add: %+v", clients)
	}
}
