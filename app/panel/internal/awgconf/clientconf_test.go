package awgconf

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

const testEndpoint = "vpn.example.com:51820"

func seedEndpoint(t *testing.T, handle *sql.DB, endpoint string) {
	t.Helper()
	if err := db.SetSetting(handle, "endpoint", endpoint); err != nil {
		t.Fatalf("seed endpoint: %v", err)
	}
}

const fullParams = `{"jc":3,"jmin":21,"jmax":31,"s1":904,"s2":737,"s3":128,"s4":857,` +
	`"h1":"1234567","h2":"2345678","h3":"3456789","h4":"4567890",` +
	`"i1":"<t><r 4><b 0x01>","i2":"<r 8>","i3":"<t><b 0xab>","i4":"<rc 7>","i5":"<rd 16>"}`

func TestGenerateClientMinimal(t *testing.T) {
	handle, _ := newTestDB(t)
	seedServer(t, handle, "", "")
	seedClient(t, handle, 1, "", true, "10.8.0.2/32")
	seedEndpoint(t, handle, testEndpoint)

	cfg, err := GenerateClient(handle, 1)
	if err != nil {
		t.Fatalf("GenerateClient: %v", err)
	}
	want := "[Interface]\n" +
		"PrivateKey = " + testKey(11) + "\n" +
		"Address = 10.8.0.2/32\n" +
		"\n" +
		"[Peer]\n" +
		"PublicKey = " + testKey(2) + "\n" +
		"AllowedIPs = 0.0.0.0/0\n" +
		"Endpoint = " + testEndpoint + "\n"
	if got := string(cfg); got != want {
		t.Fatalf("minimal config =\n%q\nwant\n%q", got, want)
	}
}

func TestGenerateClientFull(t *testing.T) {
	handle, _ := newTestDB(t)
	seedServer(t, handle, fullParams, "1.1.1.1,9.9.9.9")
	seedClient(t, handle, 1, testKey(4), true, "10.8.0.2/32")
	seedEndpoint(t, handle, testEndpoint)

	cfg, err := GenerateClient(handle, 1)
	if err != nil {
		t.Fatalf("GenerateClient: %v", err)
	}
	got := string(cfg)
	want := []string{
		"[Interface]",
		"PrivateKey = " + testKey(11),
		"Address = 10.8.0.2/32",
		"DNS = 1.1.1.1,9.9.9.9",
		"Jc = 3", "Jmin = 21", "Jmax = 31",
		"S1 = 904", "S2 = 737", "S3 = 128", "S4 = 857",
		"H1 = 1234567", "H2 = 2345678", "H3 = 3456789", "H4 = 4567890",
		"I1 = <t><r 4><b 0x01>", "I2 = <r 8>", "I3 = <t><b 0xab>", "I4 = <rc 7>", "I5 = <rd 16>",
		"[Peer]",
		"PublicKey = " + testKey(2),
		"PresharedKey = " + testKey(4),
		"AllowedIPs = 0.0.0.0/0",
		"Endpoint = " + testEndpoint,
	}
	for _, line := range want {
		if !strings.Contains(got, line+"\n") {
			t.Errorf("missing line %q in:\n%s", line, got)
		}
	}
	// stable order: interface keys before the blank separator, then peer keys
	if got != "[Interface]\n"+
		"PrivateKey = "+testKey(11)+"\n"+
		"Address = 10.8.0.2/32\n"+
		"DNS = 1.1.1.1,9.9.9.9\n"+
		"Jc = 3\n"+
		"Jmin = 21\n"+
		"Jmax = 31\n"+
		"S1 = 904\n"+
		"S2 = 737\n"+
		"S3 = 128\n"+
		"S4 = 857\n"+
		"H1 = 1234567\n"+
		"H2 = 2345678\n"+
		"H3 = 3456789\n"+
		"H4 = 4567890\n"+
		"I1 = <t><r 4><b 0x01>\n"+
		"I2 = <r 8>\n"+
		"I3 = <t><b 0xab>\n"+
		"I4 = <rc 7>\n"+
		"I5 = <rd 16>\n"+
		"\n"+
		"[Peer]\n"+
		"PublicKey = "+testKey(2)+"\n"+
		"PresharedKey = "+testKey(4)+"\n"+
		"AllowedIPs = 0.0.0.0/0\n"+
		"Endpoint = "+testEndpoint+"\n" {
		t.Fatalf("full config order mismatch:\n%s", got)
	}
	// exact structural invariants
	if got[len(got)-1] != '\n' {
		t.Fatal("config must end with a newline")
	}
	if strings.Count(got, "\n\n") != 1 {
		t.Fatalf("expected exactly one blank line between [Interface] and [Peer]:\n%q", got)
	}
	if strings.Contains(got, "#") {
		t.Fatal("config must not contain comments")
	}
}

func TestGenerateClientNoPresharedKey(t *testing.T) {
	handle, _ := newTestDB(t)
	seedServer(t, handle, "", "")
	seedClient(t, handle, 1, "", true, "10.8.0.2/32")
	seedEndpoint(t, handle, testEndpoint)

	cfg, err := GenerateClient(handle, 1)
	if err != nil {
		t.Fatalf("GenerateClient: %v", err)
	}
	if strings.Contains(string(cfg), "PresharedKey") {
		t.Fatalf("PresharedKey line present with NULL preshared_key:\n%s", cfg)
	}
}

func TestGenerateClientEndpointMissing(t *testing.T) {
	handle, _ := newTestDB(t)
	seedServer(t, handle, "", "")
	seedClient(t, handle, 1, "", true, "10.8.0.2/32")

	_, err := GenerateClient(handle, 1)
	if err == nil {
		t.Fatal("expected error when endpoint setting is absent")
	}
	if !strings.Contains(err.Error(), `"endpoint"`) {
		t.Fatalf("error should name the endpoint setting: %v", err)
	}
	// empty endpoint is the same error
	seedEndpoint(t, handle, "")
	_, err = GenerateClient(handle, 1)
	if err == nil {
		t.Fatal("expected error when endpoint setting is empty")
	}
}

func TestGenerateClientNoServer(t *testing.T) {
	handle, _ := newTestDB(t)
	seedClient(t, handle, 1, "", true, "10.8.0.2/32")
	seedEndpoint(t, handle, testEndpoint)

	_, err := GenerateClient(handle, 1)
	if err == nil {
		t.Fatal("expected error when server row is absent")
	}
	if !errors.Is(err, ErrNoServerRow) {
		t.Fatalf("error = %v, want ErrNoServerRow", err)
	}
}

func TestGenerateClientNoClient(t *testing.T) {
	handle, _ := newTestDB(t)
	seedServer(t, handle, "", "")
	seedEndpoint(t, handle, testEndpoint)

	for _, id := range []int64{999, -1} {
		_, err := GenerateClient(handle, id)
		if err == nil {
			t.Fatalf("expected error for unknown client %d", id)
		}
		if !errors.Is(err, db.ErrClientNotFound) {
			t.Fatalf("client %d error = %v, want db.ErrClientNotFound", id, err)
		}
	}
}

func TestGenerateClientDisabledClient(t *testing.T) {
	handle, _ := newTestDB(t)
	seedServer(t, handle, "", "")
	seedClient(t, handle, 1, "", false, "10.8.0.2/32")
	seedEndpoint(t, handle, testEndpoint)

	cfg, err := GenerateClient(handle, 1)
	if err != nil {
		t.Fatalf("disabled client must still get its on-demand config: %v", err)
	}
	if !strings.Contains(string(cfg), "Endpoint = "+testEndpoint) {
		t.Fatalf("disabled client config incomplete:\n%s", cfg)
	}
}

func TestGenerateClientExpiredClient(t *testing.T) {
	handle, _ := newTestDB(t)
	seedServer(t, handle, "", "")
	seedClient(t, handle, 1, "", true, "10.8.0.2/32")
	seedEndpoint(t, handle, testEndpoint)
	past := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
	if err := db.SetClientExpiry(handle, 1, past); err != nil {
		t.Fatalf("SetClientExpiry: %v", err)
	}

	cfg, err := GenerateClient(handle, 1)
	if err != nil {
		t.Fatalf("expired client must still get its on-demand config: %v", err)
	}
	if !strings.Contains(string(cfg), "PrivateKey = "+testKey(11)) {
		t.Fatalf("expired client config incomplete:\n%s", cfg)
	}
	// the record must not have been deleted or disabled by the generator
	rec, err := db.ClientByID(handle, 1)
	if err != nil {
		t.Fatalf("ClientByID: %v", err)
	}
	if !rec.Enabled || rec.ExpiresAt == "" {
		t.Fatalf("generator mutated the client record: %+v", rec)
	}
}

func TestGenerateClientAlwaysFullTunnelIPv4(t *testing.T) {
	handle, _ := newTestDB(t)
	seedServer(t, handle, "", "")
	seedClient(t, handle, 1, "", true, "10.8.0.2/32")
	seedEndpoint(t, handle, testEndpoint)

	cfg, err := GenerateClient(handle, 1)
	if err != nil {
		t.Fatalf("GenerateClient: %v", err)
	}
	got := string(cfg)
	if !strings.Contains(got, "AllowedIPs = 0.0.0.0/0\n") {
		t.Fatalf("AllowedIPs must be strictly 0.0.0.0/0 and nothing else:\n%s", got)
	}
	if strings.Contains(got, "::/0") || strings.Contains(got, "AllowedIPs,") {
		t.Fatalf("unexpected additional routes:\n%s", got)
	}
}

func TestGenerateClientUsesStoredKeys(t *testing.T) {
	handle, _ := newTestDB(t)
	seedServer(t, handle, "", "")
	seedClient(t, handle, 7, "", true, "10.8.0.2/32")
	seedEndpoint(t, handle, testEndpoint)

	cfg, _ := GenerateClient(handle, 7)
	got := string(cfg)
	if !strings.Contains(got, "PrivateKey = "+testKey(17)+"\n") {
		t.Fatalf("client private key not used:\n%s", got)
	}
	if !strings.Contains(got, "PublicKey = "+testKey(2)+"\n") {
		t.Fatalf("server public key not used as peer key:\n%s", got)
	}
	// stored public key of the client (testKey(27)) must NOT appear
	if strings.Contains(got, testKey(27)) {
		t.Fatalf("client public key leaked into its own config:\n%s", got)
	}
}

func TestGenerateClientDeterministic(t *testing.T) {
	handle, _ := newTestDB(t)
	seedServer(t, handle, fullParams, "1.1.1.1")
	seedClient(t, handle, 1, testKey(4), true, "10.8.0.2/32")
	seedEndpoint(t, handle, testEndpoint)

	first, err := GenerateClient(handle, 1)
	if err != nil {
		t.Fatalf("GenerateClient #1: %v", err)
	}
	second, err := GenerateClient(handle, 1)
	if err != nil {
		t.Fatalf("GenerateClient #2: %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("render is not deterministic:\n%q\nvs\n%q", first, second)
	}
}

func TestGenerateClientNoSecretsInErrors(t *testing.T) {
	handle, _ := newTestDB(t)
	seedServer(t, handle, "", "")
	seedClient(t, handle, 1, "", true, "10.8.0.2/32")
	seedEndpoint(t, handle, testEndpoint)
	secretPSK := "sekrit-preshared-value-123"
	secretPriv := testKey(99)
	// corrupt the stored client keys directly: the generator must fail
	// validation without echoing them
	if _, err := handle.Exec(
		`UPDATE clients SET private_key = ?, preshared_key = ? WHERE id = 1`,
		secretPriv+"-broken", secretPSK,
	); err != nil {
		t.Fatalf("UPDATE: %v", err)
	}

	_, err := GenerateClient(handle, 1)
	if err == nil {
		t.Fatal("expected a validation error")
	}
	for _, secret := range []string{secretPSK, secretPriv} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error leaks a secret: %v", err)
		}
	}
}

func TestRenderClientDirect(t *testing.T) {
	cfg := ClientConfig{
		PrivateKey:      testKey(11),
		Address:         "10.8.0.2/32",
		DNS:             "",
		Params:          Params{Jc: u16(3)},
		ServerPublicKey: testKey(2),
		PresharedKey:    "",
		Endpoint:        testEndpoint,
	}
	want := "[Interface]\n" +
		"PrivateKey = " + testKey(11) + "\n" +
		"Address = 10.8.0.2/32\n" +
		"Jc = 3\n" +
		"\n" +
		"[Peer]\n" +
		"PublicKey = " + testKey(2) + "\n" +
		"AllowedIPs = 0.0.0.0/0\n" +
		"Endpoint = " + testEndpoint + "\n"
	if got := RenderClient(cfg); got != want {
		t.Fatalf("RenderClient() =\n%q\nwant\n%q", got, want)
	}
}

func TestValidateClientEndpoint(t *testing.T) {
	good := ClientConfig{
		PrivateKey:      testKey(11),
		Address:         "10.8.0.2/32",
		ServerPublicKey: testKey(2),
		Endpoint:        "vpn.example.com:51820",
	}
	if err := ValidateClient(good); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	for _, ep := range []string{"", "no-port", "host:0", "host:99999", ":51820", "host:abc"} {
		bad := good
		bad.Endpoint = ep
		if err := ValidateClient(bad); err == nil {
			t.Errorf("endpoint %q accepted", ep)
		}
	}
}
