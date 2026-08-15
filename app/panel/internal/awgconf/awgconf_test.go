package awgconf

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func testKey(seed byte) string {
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{seed}, 32))
}

func u16(v uint16) *uint16 { return &v }

func TestRenderInterfaceMinimal(t *testing.T) {
	server := ServerConfig{PrivateKey: testKey(1), Address: "10.8.0.1/24", ListenPort: 51820}
	want := "[Interface]\n" +
		"PrivateKey = " + testKey(1) + "\n" +
		"Address = 10.8.0.1/24\n" +
		"ListenPort = 51820\n" +
		"\n"
	if got := Render(server, nil); got != want {
		t.Fatalf("Render() =\n%q\nwant\n%q", got, want)
	}
}

func TestRenderInterfaceFullParams(t *testing.T) {
	server := ServerConfig{
		PrivateKey: testKey(1),
		Address:    "10.8.0.1/24",
		ListenPort: 443,
		DNS:        "1.1.1.1,9.9.9.9",
		Params: Params{
			Jc: u16(3), Jmin: u16(1), Jmax: u16(5),
			S1: u16(1), S2: u16(2), S3: u16(3), S4: u16(4),
			H1: "3-5",
			I1: "<t><r 4><b 0x01>",
		},
	}
	want := "[Interface]\n" +
		"PrivateKey = " + testKey(1) + "\n" +
		"Address = 10.8.0.1/24\n" +
		"ListenPort = 443\n" +
		"DNS = 1.1.1.1,9.9.9.9\n" +
		"Jc = 3\n" +
		"Jmin = 1\n" +
		"Jmax = 5\n" +
		"S1 = 1\n" +
		"S2 = 2\n" +
		"S3 = 3\n" +
		"S4 = 4\n" +
		"H1 = 3-5\n" +
		"I1 = <t><r 4><b 0x01>\n" +
		"\n"
	if got := Render(server, nil); got != want {
		t.Fatalf("Render() =\n%q\nwant\n%q", got, want)
	}
}

func TestRenderPeers(t *testing.T) {
	server := ServerConfig{PrivateKey: testKey(1), Address: "10.8.0.1/24", ListenPort: 51820}
	peers := []PeerConfig{
		{PublicKey: testKey(2), AllowedIPs: "10.8.0.2/32"},
		{PublicKey: testKey(3), PresharedKey: testKey(4), AllowedIPs: "10.8.0.3/32"},
	}
	want := "[Interface]\n" +
		"PrivateKey = " + testKey(1) + "\n" +
		"Address = 10.8.0.1/24\n" +
		"ListenPort = 51820\n" +
		"\n" +
		"[Peer]\n" +
		"PublicKey = " + testKey(2) + "\n" +
		"AllowedIPs = 10.8.0.2/32\n" +
		"\n" +
		"[Peer]\n" +
		"PublicKey = " + testKey(3) + "\n" +
		"PresharedKey = " + testKey(4) + "\n" +
		"AllowedIPs = 10.8.0.3/32\n"
	if got := Render(server, peers); got != want {
		t.Fatalf("Render() =\n%q\nwant\n%q", got, want)
	}
}

func TestPresharedKeyOptional(t *testing.T) {
	server := ServerConfig{PrivateKey: testKey(1), Address: "10.8.0.1/24", ListenPort: 51820}

	withPSK := Render(server, []PeerConfig{{PublicKey: testKey(2), PresharedKey: testKey(4), AllowedIPs: "10.8.0.2/32"}})
	withoutPSK := Render(server, []PeerConfig{{PublicKey: testKey(2), AllowedIPs: "10.8.0.2/32"}})

	if !strings.Contains(withPSK, "PresharedKey = ") {
		t.Errorf("expected PresharedKey line in:\n%s", withPSK)
	}
	if strings.Contains(withoutPSK, "PresharedKey") {
		t.Errorf("did not expect PresharedKey line in:\n%s", withoutPSK)
	}
}

func TestRenderDeterministic(t *testing.T) {
	server := ServerConfig{
		PrivateKey: testKey(1), Address: "10.8.0.1/24", ListenPort: 51820, DNS: "1.1.1.1",
		Params: Params{Jc: u16(3), H1: "1-5", I1: "<r 4>", I2: "garbage-without-tags"},
	}
	peers := []PeerConfig{{PublicKey: testKey(2), PresharedKey: testKey(4), AllowedIPs: "10.8.0.2/32"}}
	a := Render(server, peers)
	b := Render(server, peers)
	if a != b {
		t.Fatalf("Render() not deterministic:\n%q\nvs\n%q", a, b)
	}
}

func TestParseParams(t *testing.T) {
	got, err := ParseParams(`{"jc":3,"jmin":1,"jmax":5,"s1":1,"s4":4,"h1":"1234567","i1":"<t><r 4><b 0x01>"}`)
	if err != nil {
		t.Fatalf("ParseParams() error: %v", err)
	}
	if got.Jc == nil || *got.Jc != 3 || got.Jmin == nil || *got.Jmin != 1 || got.Jmax == nil || *got.Jmax != 5 {
		t.Errorf("jc/jmin/jmax = %v/%v/%v, want 3/1/5", got.Jc, got.Jmin, got.Jmax)
	}
	if got.S1 == nil || *got.S1 != 1 || got.S4 == nil || *got.S4 != 4 {
		t.Errorf("s1/s4 = %v/%v, want 1/4", got.S1, got.S4)
	}
	if got.S2 != nil || got.S3 != nil {
		t.Errorf("s2/s3 = %v/%v, want unset", got.S2, got.S3)
	}
	if got.H1 != "1234567" || got.I1 != "<t><r 4><b 0x01>" {
		t.Errorf("h1/i1 = %q/%q", got.H1, got.I1)
	}
}

func TestParseParamsEmpty(t *testing.T) {
	for _, raw := range []string{"", "   ", "{}"} {
		got, err := ParseParams(raw)
		if err != nil {
			t.Fatalf("ParseParams(%q) error: %v", raw, err)
		}
		if got.Jc != nil || got.H2 != "" || got.I3 != "" {
			t.Errorf("ParseParams(%q) = %+v, want empty params", raw, got)
		}
	}
}

func TestParseParamsNullsAreSkipped(t *testing.T) {
	got, err := ParseParams(`{"jc":null,"h1":"1234567","i1":null}`)
	if err != nil {
		t.Fatalf("ParseParams() error: %v", err)
	}
	if got.Jc != nil || got.I1 != "" {
		t.Errorf("null fields must stay unset, got %+v", got)
	}
	if got.H1 != "1234567" {
		t.Errorf("h1 = %q, want 1234567", got.H1)
	}
}

func TestParseParamsValidation(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"unknown key", `{"jc":3,"bogus":1}`, "unknown key"},
		{"non-object", `[1,2,3]`, "not a JSON object"},
		{"jc zero", `{"jc":0}`, "jc must be >= 1"},
		{"jmin zero", `{"jmin":0}`, "jmin must be >= 1"},
		{"jmax zero", `{"jmax":0}`, "jmax must be >= 1"},
		{"jmax below jmin", `{"jmin":5,"jmax":3}`, "jmin (5) must be <= jmax (3)"},
		{"float value", `{"jc":3.5}`, "not an integer"},
		{"negative s", `{"s2":-1}`, "not an integer"},
		{"overflow s", `{"s2":70000}`, "not an integer"},
		{"string for jc", `{"jc":"3"}`, "not an integer"},
		{"header range inverted", `{"h1":"1000100-1000000"}`, "inverted range"},
		{"bad header token", `{"h1":"abc"}`, "failed to parse"},
		{"header range form", `{"h1":"1-2-3"}`, "bad format"},
		{"header N-M below 7 digits", `{"h1":"3-5"}`, "out of range"},
		{"header overlap", `{"h1":"1000000-1000100","h2":"1000050-1000200"}`, "overlap"},
		{"bad header hex", `{"h2":"0x10"}`, "failed to parse"},
		{"huge header", `{"h1":"4294967296"}`, "failed to parse"},
		{"header below 7 digits", `{"h1":"999999"}`, "out of range"},
		{"header 8 digits", `{"h1":"10000000"}`, "out of range"},
		{"header leading zero", `{"h1":"01000000"}`, "bad format"},
		{"unknown tag", `{"i1":"<xor 4>"}`, "unknown tag"},
		{"rejected tag d", `{"i1":"<d>"}`, "unknown tag"},
		{"rejected tag ds", `{"i1":"<ds>"}`, "unknown tag"},
		{"rejected tag dz", `{"i1":"<dz 8>"}`, "unknown tag"},
		{"unclosed tag", `{"i1":"<b 0x01"}`, "missing enclosing >"},
		{"empty tag", `{"i1":"<>"}`, "empty tag"},
		{"b no arg", `{"i1":"<b>"}`, "empty argument"},
		{"b odd hex", `{"i1":"<b 0x0>"}`, "odd amount of symbols"},
		{"b bad hex", `{"i1":"<b 0xzz>"}`, "invalid hex"},
		{"r no arg", `{"i1":"<r>"}`, "missing size argument"},
		{"r bad number", `{"i1":"<r 3.5>"}`, "invalid size"},
		{"r negative", `{"i1":"<r -3>"}`, "negative size"},
		{"i1 newline injection", "{\"i1\":\"\\nPreUp = /bin/true\"}", "control character"},
		{"i1 cr injection", "{\"i1\":\"\\rPostUp = /bin/true\"}", "control character"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseParams(tt.raw)
			if err == nil {
				t.Fatalf("ParseParams(%s): expected error containing %q, got nil", tt.raw, tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ParseParams(%s) error = %q, want it to contain %q", tt.raw, err, tt.want)
			}
		})
	}
}

func TestParseParamsAcceptsPinnedToolsFormats(t *testing.T) {
	oKs := []string{
		`{"i1":"<t>"}`,
		`{"i1":"<r 3>"}`,
		`{"i1":"<rc 0>"}`,
		`{"i1":"<rd 6>"}`,
		`{"i1":"<b 0x01>"}`,
		`{"i1":"<b 01020304>"}`,
		`{"i1":"garbage with no tags"}`,
		`{"i1":"<r 3>trailing text"}`,
		`{"i1":"<b 0x01><r 4><t><b 0x0002>"}`,
		`{"h1":"1000000"}`,
		`{"h2":"9999999"}`,
		`{"h3":"1234567"}`,
		`{"h1":"1000000-1000100"}`,
		`{"h1":"1000000-1000100","h2":"2000000-2000500","h3":"3000000-3000001","h4":"9999900-9999999"}`,
	}
	for _, raw := range oKs {
		if _, err := ParseParams(raw); err != nil {
			t.Errorf("ParseParams(%s): unexpected error: %v", raw, err)
		}
	}
}

func TestValidateServer(t *testing.T) {
	good := ServerConfig{PrivateKey: testKey(1), Address: "10.8.0.1/24", ListenPort: 51820}
	if err := ValidateServer(good); err != nil {
		t.Fatalf("ValidateServer(good) error: %v", err)
	}
	badKey := ServerConfig{PrivateKey: "TOP_SECRET_BAD_KEY", Address: "10.8.0.1/24", ListenPort: 51820}
	err := ValidateServer(badKey)
	if err == nil {
		t.Fatal("ValidateServer(bad key): expected error")
	}
	if strings.Contains(err.Error(), "TOP_SECRET_BAD_KEY") {
		t.Fatalf("error leaks secret: %v", err)
	}
	if err := ValidateServer(ServerConfig{PrivateKey: testKey(1), Address: "not-a-cidr", ListenPort: 51820}); err == nil {
		t.Fatal("ValidateServer(bad CIDR): expected error")
	}
	injected := ServerConfig{PrivateKey: testKey(1), Address: "10.8.0.1/24", ListenPort: 51820, DNS: "1.1.1.1\nPostUp = /bin/true"}
	if err := ValidateServer(injected); err == nil {
		t.Fatal("ValidateServer(DNS newline): expected error")
	}
}

func TestValidatePeer(t *testing.T) {
	good := PeerConfig{PublicKey: testKey(2), AllowedIPs: "10.8.0.2/32"}
	if err := ValidatePeer(good); err != nil {
		t.Fatalf("ValidatePeer(good) error: %v", err)
	}
	secret := "S3CR3T_PSK_VALUE"
	err := ValidatePeer(PeerConfig{PublicKey: testKey(2), PresharedKey: secret, AllowedIPs: "10.8.0.2/32"})
	if err == nil {
		t.Fatal("ValidatePeer(bad psk): expected error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaks secret: %v", err)
	}
	if err := ValidatePeer(PeerConfig{PublicKey: testKey(2), AllowedIPs: "10.8.0.2"}); err == nil {
		t.Fatal("ValidatePeer(bad allowed IPs): expected error")
	}
}
