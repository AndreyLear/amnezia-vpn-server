package web

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

// Свой MTU у клиента (amnezia-vpn-server-h2pg). Панель обязана показывать
// действующее значение, уметь его менять и отказывать во вздоре — последнее
// важнее прочего: значение вводится руками.
func TestAPIClientMTURoundTrip(t *testing.T) {
	f := newFixture(t)
	c, _, _ := f.addClient("mtu-client")
	path := fmt.Sprintf("/api/clients/%d", c.ID)

	// Ноль — нормальное состояние: клиент следует за сервером.
	rec := f.apiCSRF(http.MethodGet, path, nil)
	if got := decodeAPI(t, rec); got["mtu"] != float64(0) {
		t.Fatalf("новый клиент: mtu = %v, ожидался 0", got["mtu"])
	}

	rec = f.apiCSRF(http.MethodPatch, path, map[string]any{"mtu": 1420})
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH mtu: код %d; тело=%s", rec.Code, rec.Body.String())
	}
	if got := decodeAPI(t, rec); got["mtu"] != float64(1420) {
		t.Fatalf("ответ после правки: mtu = %v, ожидалось 1420", got["mtu"])
	}
	stored, err := db.ClientByID(f.h, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.MTU != 1420 {
		t.Fatalf("в базе mtu = %d, ожидалось 1420", stored.MTU)
	}

	// Ноль снимает собственное значение, а не запрещает туннель.
	rec = f.apiCSRF(http.MethodPatch, path, map[string]any{"mtu": 0})
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH mtu=0: код %d; тело=%s", rec.Code, rec.Body.String())
	}
	stored, _ = db.ClientByID(f.h, c.ID)
	if stored.MTU != 0 {
		t.Fatalf("после снятия mtu = %d, ожидался 0", stored.MTU)
	}
}

func TestAPIClientMTUOutOfRangeIsRefused(t *testing.T) {
	f := newFixture(t)
	c, _, _ := f.addClient("mtu-bounds")
	path := fmt.Sprintf("/api/clients/%d", c.ID)

	for _, mtu := range []int{1279, 1441, 9000, -1} {
		rec := f.apiCSRF(http.MethodPatch, path, map[string]any{"mtu": mtu})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("mtu=%d: код %d, ожидался 400; тело=%s", mtu, rec.Code, rec.Body.String())
		}
		got := decodeAPI(t, rec)
		msg, _ := got["message"].(string)
		if msg == "" {
			t.Fatalf("mtu=%d: отказ без объяснения: %v", mtu, got)
		}
	}
	stored, err := db.ClientByID(f.h, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.MTU != 0 {
		t.Fatalf("отклонённое значение записалось: %d", stored.MTU)
	}
}
