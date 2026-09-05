package awgconf

import "testing"

// amnezia-vpn-server-0rmu: the tunnel may now carry IPv6, and the config
// must say so — but only when it is true, and never at the cost of the
// ::/0 route that keeps a dual-stack client's real address hidden.

const testPrefix6 = "fd42:a11e:c0de::1/64"

// A deployment without IPv6 must render exactly what it rendered before.
// Anything else would change every existing server on upgrade.
func TestRenderWithoutIPv6IsUnchanged(t *testing.T) {
	server := ServerConfig{PrivateKey: testKey(1), Address: "10.8.0.1/24", ListenPort: 51820}
	peers := []PeerConfig{{PublicKey: testKey(2), AllowedIPs: "10.8.0.2/32"}}
	got := Render(server, peers)
	if !contains(got, "Address = 10.8.0.1/24\n") {
		t.Fatalf("interface address line changed:\n%s", got)
	}
	if !contains(got, "AllowedIPs = 10.8.0.2/32\n") {
		t.Fatalf("peer line changed:\n%s", got)
	}
	if contains(got, ":") && contains(got, "fd42") {
		t.Fatalf("IPv6 appeared in an IPv4-only config:\n%s", got)
	}
}

func TestRenderWithIPv6CarriesBothFamilies(t *testing.T) {
	server := ServerConfig{
		PrivateKey: testKey(1), Address: "10.8.0.1/24",
		Address6: testPrefix6, ListenPort: 51820,
	}
	peers := []PeerConfig{{
		PublicKey: testKey(2), AllowedIPs: "10.8.0.2/32",
		AllowedIPs6: "fd42:a11e:c0de::2/128",
	}}
	got := Render(server, peers)
	if !contains(got, "Address = 10.8.0.1/24, "+testPrefix6+"\n") {
		t.Fatalf("interface address missing IPv6:\n%s", got)
	}
	if !contains(got, "AllowedIPs = 10.8.0.2/32, fd42:a11e:c0de::2/128\n") {
		t.Fatalf("peer missing IPv6:\n%s", got)
	}
}

// The invariant this task exists to protect. Making ::/0 conditional was
// the obvious-looking change and it would have reintroduced the address
// leak on every tunnel without IPv6: the client's own IPv6 would leave
// around the VPN carrying the real address. The blackhole is the lesser
// evil and stays.
func TestClientAlwaysRoutesIPv6IntoTheTunnel(t *testing.T) {
	for _, tc := range []struct {
		name     string
		address6 string
	}{
		{"туннель без IPv6", ""},
		{"туннель с IPv6", "fd42:a11e:c0de::2/128"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := ClientConfig{
				PrivateKey: testKey(1), Address: "10.8.0.2/32", Address6: tc.address6,
				ServerPublicKey: testKey(2), Endpoint: "vpn.example.com:51820",
			}
			got := RenderClient(cfg)
			if !contains(got, "AllowedIPs = 0.0.0.0/0, ::/0\n") {
				t.Fatalf("::/0 is missing — the client's own IPv6 would leave around the VPN:\n%s", got)
			}
		})
	}
}

func TestClientAddressFollowsTheTunnel(t *testing.T) {
	base := ClientConfig{
		PrivateKey: testKey(1), Address: "10.8.0.2/32",
		ServerPublicKey: testKey(2), Endpoint: "vpn.example.com:51820",
	}
	if got := RenderClient(base); !contains(got, "Address = 10.8.0.2/32\n") {
		t.Fatalf("IPv4-only client address changed:\n%s", got)
	}
	withV6 := base
	withV6.Address6 = "fd42:a11e:c0de::2/128"
	if got := RenderClient(withV6); !contains(got, "Address = 10.8.0.2/32, fd42:a11e:c0de::2/128\n") {
		t.Fatalf("client address missing IPv6:\n%s", got)
	}
}

// An IPv4 value in an IPv6 field would render a duplicate entry and make
// the config claim something untrue about the tunnel.
func TestIPv6FieldsRejectNonIPv6(t *testing.T) {
	for _, bad := range []string{"10.8.0.1/24", "fd42::1", "nonsense"} {
		s := ServerConfig{PrivateKey: testKey(1), Address: "10.8.0.1/24", Address6: bad, ListenPort: 51820}
		if err := ValidateServer(s); err == nil {
			t.Errorf("ValidateServer accepted address6 %q", bad)
		}
		p := PeerConfig{PublicKey: testKey(2), AllowedIPs: "10.8.0.2/32", AllowedIPs6: bad}
		if err := ValidatePeer(p); err == nil {
			t.Errorf("ValidatePeer accepted allowedIPs6 %q", bad)
		}
		c := ClientConfig{
			PrivateKey: testKey(1), Address: "10.8.0.2/32", Address6: bad,
			ServerPublicKey: testKey(2), Endpoint: "vpn.example.com:51820",
		}
		if err := ValidateClient(c); err == nil {
			t.Errorf("ValidateClient accepted address6 %q", bad)
		}
	}
}

func contains(haystack, needle string) bool {
	return len(needle) == 0 || indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
