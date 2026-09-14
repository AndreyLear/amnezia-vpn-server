package cli

import (
	"strconv"
	"strings"
	"testing"
)

// CLI отказывает в MTU выше потолка сервера с учётом добивки S4
// (amnezia-vpn-server-bctr) и называет причину; значение до потолка принимает.
func TestClientSetMTURefusesAboveS4Ceiling(t *testing.T) {
	c := newCtx(t)
	t.Setenv("TUNNEL_MTU_MAX", "1440")
	c.seedServer("", `{"jc":3,"jmin":1,"jmax":5,"s1":1,"s2":2,"s3":3,"s4":29}`)
	id, _, _, _ := c.seedClient("phone")
	sid := strconv.FormatInt(id, 10)

	code, _, errb := c.run("client", "set-mtu", sid, "1420")
	if code != 2 || !strings.Contains(errb, "1411") {
		t.Fatalf("1420: exit %d, stderr %q — ждали отказ с потолком 1411", code, errb)
	}
	if code, _, errb := c.run("client", "set-mtu", sid, "1411"); code != 0 {
		t.Fatalf("1411: exit %d, stderr %q", code, errb)
	}
}
