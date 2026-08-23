package awgconf

import (
	"strings"
	"testing"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

// The tunnel MTU must be small enough that a full-size transport packet
// still fits the uplink: payload + 32 (AmneziaWG header + Poly1305 tag)
// + 8 (UDP) + 20 (IPv4) = payload + 60 bytes on the wire. Hosting
// uplinks that tunnel their own traffic (GRE/VXLAN) commonly carry 1476,
// which the historical awg-quick default of 1420 (= 1500 - 80) exceeds
// by four bytes.
func TestDefaultMTULeavesRoomForEncapsulation(t *testing.T) {
	const worstCaseUplinkPMTU = 1476
	if int(DefaultMTU)+EncapsulationOverhead > worstCaseUplinkPMTU {
		t.Fatalf("DefaultMTU %d + %d overhead = %d exceeds a %d-byte uplink",
			DefaultMTU, EncapsulationOverhead, int(DefaultMTU)+EncapsulationOverhead, worstCaseUplinkPMTU)
	}
}

func TestRenderServerEmitsMTU(t *testing.T) {
	got := Render(ServerConfig{
		PrivateKey: strings.Repeat("A", 43) + "=",
		Address:    "10.8.0.1/24",
		ListenPort: 443,
		MTU:        1400,
	}, nil)
	if !strings.Contains(got, "MTU = 1400\n") {
		t.Fatalf("server config must pin the MTU:\n%s", got)
	}
	// awg-quick consumes MTU from [Interface]; it must sit inside that
	// section, before the blank line that opens the peers.
	iface, _, _ := strings.Cut(got, "\n\n")
	if !strings.Contains(iface, "MTU = 1400") {
		t.Fatalf("MTU must live in [Interface]:\n%s", got)
	}
}

func TestRenderServerOmitsUnsetMTU(t *testing.T) {
	got := Render(ServerConfig{
		PrivateKey: strings.Repeat("A", 43) + "=",
		Address:    "10.8.0.1/24",
		ListenPort: 443,
	}, nil)
	if strings.Contains(got, "MTU") {
		t.Fatalf("an unset MTU must not be rendered:\n%s", got)
	}
}

func TestRenderClientEmitsMTU(t *testing.T) {
	got := RenderClient(ClientConfig{
		PrivateKey:      strings.Repeat("B", 43) + "=",
		Address:         "10.8.0.2/32",
		MTU:             1400,
		ServerPublicKey: strings.Repeat("C", 43) + "=",
		Endpoint:        "vpn.example.com:443",
	})
	if !strings.Contains(got, "MTU = 1400\n") {
		t.Fatalf("client config must pin the MTU:\n%s", got)
	}
	iface, _, _ := strings.Cut(got, "\n\n")
	if !strings.Contains(iface, "MTU = 1400") {
		t.Fatalf("MTU must live in the client [Interface]:\n%s", got)
	}
}

func TestValidateServerRejectsOutOfRangeMTU(t *testing.T) {
	base := ServerConfig{
		PrivateKey: strings.Repeat("A", 43) + "=",
		Address:    "10.8.0.1/24",
		ListenPort: 443,
	}
	for _, mtu := range []uint16{1279, 1501} {
		cfg := base
		cfg.MTU = mtu
		if err := ValidateServer(cfg); err == nil {
			t.Fatalf("MTU %d must be rejected", mtu)
		}
	}
	for _, mtu := range []uint16{0, MinMTU, DefaultMTU, MaxMTU} {
		cfg := base
		cfg.MTU = mtu
		if err := ValidateServer(cfg); err != nil {
			t.Fatalf("MTU %d must be accepted: %v", mtu, err)
		}
	}
}

func TestMTUFromSettingsDefaultsWhenUnset(t *testing.T) {
	handle, _ := newTestDB(t)
	got, err := MTUFromSettings(handle)
	if err != nil {
		t.Fatalf("MTUFromSettings: %v", err)
	}
	if got != DefaultMTU {
		t.Fatalf("MTUFromSettings = %d, want the default %d", got, DefaultMTU)
	}
}

func TestMTUFromSettingsHonoursStoredValue(t *testing.T) {
	handle, _ := newTestDB(t)
	if err := db.SetSetting(handle, "mtu", "1380"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	got, err := MTUFromSettings(handle)
	if err != nil {
		t.Fatalf("MTUFromSettings: %v", err)
	}
	if got != 1380 {
		t.Fatalf("MTUFromSettings = %d, want 1380", got)
	}
}

// A corrupted settings row must not silently fall back to a value that
// breaks the tunnel: the operator has to see the error.
func TestMTUFromSettingsRejectsGarbage(t *testing.T) {
	handle, _ := newTestDB(t)
	// "" is not garbage: it means the key was never set (older archives).
	for _, bad := range []string{"abc", "9000", "1279"} {
		if err := db.SetSetting(handle, "mtu", bad); err != nil {
			t.Fatalf("SetSetting: %v", err)
		}
		if _, err := MTUFromSettings(handle); err == nil {
			t.Fatalf("stored MTU %q must be rejected", bad)
		}
	}
}
