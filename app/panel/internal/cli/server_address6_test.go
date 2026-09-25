package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

// --address6 без других флагов — полноценное изменение, а не «nothing to
// update»: установщик всегда добавлял --listen-port и этого не видел,
// ручной вызов отвергался (amnezia-vpn-server-76mp.27).
func TestServerUpdateAddress6Alone(t *testing.T) {
	c := newCtx(t)
	c.seedServer("", "")

	code, out, errb := c.run("server", "update", "--address6", "fd00:8::1/64")
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errb)
	}
	if !strings.Contains(out, "address6 = fd00:8::1/64") {
		t.Fatalf("stdout %q не называет изменение", out)
	}
	h := c.openDB()
	defer h.Close()
	server, err := db.ServerRow(h)
	if err != nil {
		t.Fatal(err)
	}
	if server.Address6 != "fd00:8::1/64" {
		t.Fatalf("address6 = %q", server.Address6)
	}
	conf, err := os.ReadFile(c.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(conf), "fd00:8::1/64") {
		t.Fatalf("awg0.conf не перегенерирован:\n%s", conf)
	}

	// Пустое значение выключает IPv6 — тоже изменение.
	if code, _, errb := c.run("server", "update", "--address6", ""); code != 0 {
		t.Fatalf("--address6 \"\": exit %d, stderr %q", code, errb)
	}
}
