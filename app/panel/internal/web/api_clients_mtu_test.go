package web

import (
	"fmt"
	"net/http"
	"strings"
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

// Потолок MTU клиента на сервере с добивкой S4 (amnezia-vpn-server-bctr):
// выше 1411 пакет на пути 1500 режется, и панель должна это сказать, а не
// принять число, которое всё равно зажмётся.
func TestAPIClientMTUCeilingAccountsForS4(t *testing.T) {
	t.Setenv("TUNNEL_MTU_MAX", "1440")
	f := newFixture(t)
	params := `{"jc":3,"jmin":1,"jmax":5,"s1":1,"s2":2,"s3":3,"s4":29}`
	if _, err := db.UpdateServer(f.h, nil, &params, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	c, _, _ := f.addClient("mtu-s4")
	path := fmt.Sprintf("/api/clients/%d", c.ID)

	if got := decodeAPI(t, f.apiCSRF(http.MethodGet, path, nil)); got["mtu_max"] != float64(1411) {
		t.Fatalf("mtu_max = %v, ждали 1411", got["mtu_max"])
	}
	rec := f.apiCSRF(http.MethodPatch, path, map[string]any{"mtu": 1412})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "до 1411") {
		t.Fatalf("1412 принят или граница не названа: код %d, тело=%s", rec.Code, rec.Body.String())
	}
	if stored, _ := db.ClientByID(f.h, c.ID); stored.MTU != 0 {
		t.Fatalf("отвергнутое значение записалось: %d", stored.MTU)
	}
	if rec := f.apiCSRF(http.MethodPatch, path, map[string]any{"mtu": 1411}); rec.Code != http.StatusOK {
		t.Fatalf("1411 отвергнут: код %d, тело=%s", rec.Code, rec.Body.String())
	}
	list := decodeClientList(t, f.get("/api/clients"))
	if len(list) == 0 || list[0]["mtu_max"] != float64(1411) {
		t.Fatalf("в списке нет mtu_max 1411: %v", list)
	}
}
