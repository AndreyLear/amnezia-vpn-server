// M6.5 on-demand payload tests (M6_AUDIT.md §9): config download is
// byte-for-byte awgconf.GenerateClient output with safe attachment
// naming; QR is a valid PNG whose decoded payload equals that same
// client config; unknown ids answer explicit generic errors; nothing
// is written to disk and secrets never reach HTML/errors/logs — key
// material exists only inside the payload responses.
package web

import (
	"bytes"
	"fmt"
	"image"
	_ "image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/awgconf"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
)

// TestConfigDownloadMatchesGenerateClient is the byte-parity assertion:
// the HTTP response equals awgconf.GenerateClient for the same id
// (which is what the CLI prints).
func TestConfigDownloadMatchesGenerateClient(t *testing.T) {
	f := newFixture(t)
	c, _, _ := f.addClient("carol")
	want, err := awgconf.GenerateClient(f.h, c.ID)
	if err != nil {
		t.Fatalf("GenerateClient: %v", err)
	}
	rec := f.get(fmt.Sprintf("/clients/%d/config", c.ID))
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200; body: %q", rec.Code, rec.Body.String())
	}
	if !bytes.Equal(rec.Body.Bytes(), want) {
		t.Fatalf("config bytes differ from GenerateClient")
	}
}

func TestConfigDownloadHeaders(t *testing.T) {
	f := newFixture(t)
	c, _, _ := f.addClient("dave")
	rec := f.get(fmt.Sprintf("/clients/%d/config", c.ID))
	if got := rec.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="dave.conf"` {
		t.Errorf("Content-Disposition = %q", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}

func TestConfigDownloadSafeFilename(t *testing.T) {
	f := newFixture(t)
	cases := []struct {
		name string
		want string
	}{
		{"alice", "alice.conf"},
		{"Иван", ""}, // non-ASCII: falls back to client-<id>
		{"../../etc/passwd", "etc-passwd.conf"},
		{"a\r\nb\"c", "a--b-c.conf"},
		{"a b/c:d", "a-b-c-d.conf"},
		{"!!!", ""}, // sanitizes to empty: falls back to client-<id>
	}
	for _, tc := range cases {
		c, _, _ := f.addClient(tc.name)
		rec := f.get(fmt.Sprintf("/clients/%d/config", c.ID))
		disp := rec.Header().Get("Content-Disposition")
		if !strings.HasPrefix(disp, `attachment; filename="`) || !strings.HasSuffix(disp, `"`) {
			t.Fatalf("%s: malformed Content-Disposition %q", tc.name, disp)
		}
		fn := strings.TrimSuffix(strings.TrimPrefix(disp, `attachment; filename="`), `"`)
		if strings.ContainsAny(fn, `\/:;<>'"&$`+"\r\n") {
			t.Errorf("%s: unsafe characters in filename %q", tc.name, fn)
		}
		if tc.want != "" && fn != tc.want {
			t.Errorf("%s: filename = %q, want %q", tc.name, fn, tc.want)
		}
		if tc.want == "" && fn != fmt.Sprintf("client-%d.conf", c.ID) {
			t.Errorf("%s: fallback filename = %q, want client-%d.conf", tc.name, fn, c.ID)
		}
	}
}

func TestConfigDownloadUnknownID(t *testing.T) {
	f := newFixture(t)
	rec := f.get("/clients/4242/config")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown id: code = %d, want 404", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "4242") || strings.Contains(body, "/config") {
		t.Errorf("404 echoes request details: %q", body)
	}
	// an invalid numeric form is the same explicit 404
	if rec := f.get("/clients/abc/config"); rec.Code != http.StatusNotFound {
		t.Errorf("bad id: code = %d, want 404", rec.Code)
	}
}

func TestConfigDownloadDisabledAndExpired(t *testing.T) {
	f := newFixture(t)
	c, _, _ := f.addClient("erin")
	// disabled client still gets its on-demand config (M4.3 parity)
	f.post(fmt.Sprintf("/clients/%d/disable", c.ID), nil)
	rec := f.get(fmt.Sprintf("/clients/%d/config", c.ID))
	if rec.Code != http.StatusOK {
		t.Fatalf("disabled client: code = %d, want 200", rec.Code)
	}
	// expired client likewise
	past := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
	if err := db.SetClientExpiry(f.h, c.ID, past); err != nil {
		t.Fatalf("set expiry: %v", err)
	}
	rec = f.get(fmt.Sprintf("/clients/%d/config", c.ID))
	if rec.Code != http.StatusOK {
		t.Fatalf("expired client: code = %d, want 200", rec.Code)
	}
	// the config is still the same secret payload, including keys
	if !bytes.Contains(rec.Body.Bytes(), []byte("PrivateKey")) {
		t.Errorf("expired config misses PrivateKey section")
	}
}

func TestQRValidPNG(t *testing.T) {
	f := newFixture(t)
	c, _, _ := f.addClient("frank")
	rec := f.get(fmt.Sprintf("/clients/%d/qr", c.ID))
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("Content-Type = %q, want image/png", got)
	}
	body := rec.Body.Bytes()
	if !bytes.HasPrefix(body, []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}) {
		t.Fatalf("not a PNG (bad magic)")
	}
	if _, _, err := image.Decode(bytes.NewReader(body)); err != nil {
		t.Fatalf("image.Decode: %v", err)
	}
}

// decodeQR reads the text encoded in a QR PNG.
func decodeQR(t *testing.T, png []byte) string {
	t.Helper()
	img, _, err := image.Decode(bytes.NewReader(png))
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	bin, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		t.Fatalf("binarize: %v", err)
	}
	res, err := qrcode.NewQRCodeReader().Decode(bin, nil)
	if err != nil {
		t.Fatalf("QR decode: %v", err)
	}
	return res.GetText()
}

func TestQRPayloadMatchesClientConfig(t *testing.T) {
	f := newFixture(t)
	c, _, _ := f.addClient("grace")
	cfg, err := awgconf.GenerateClient(f.h, c.ID)
	if err != nil {
		t.Fatalf("GenerateClient: %v", err)
	}
	qr := f.get(fmt.Sprintf("/clients/%d/qr", c.ID)).Body.Bytes()
	if got := decodeQR(t, qr); got != string(cfg) {
		t.Fatalf("QR payload differs from client config")
	}
}

func TestQRUnknownIDGenericError(t *testing.T) {
	f := newFixture(t)
	rec := f.get("/clients/777/qr")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown id QR: code = %d, want 404", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "777") || strings.Contains(body, "/qr") {
		t.Errorf("404 echoes request details: %q", body)
	}
	if strings.Contains(body, "PrivateKey") {
		t.Errorf("secret leaked into QR error page")
	}
}

// TestOndemandNoDiskWrites proves the config/QR handlers never touch
// the filesystem: the fixture's file set is identical before and after.
func TestOndemandNoDiskWrites(t *testing.T) {
	f := newFixture(t)
	c, _, _ := f.addClient("heidi")
	before := f.files()
	f.get(fmt.Sprintf("/clients/%d/config", c.ID))
	f.get(fmt.Sprintf("/clients/%d/qr", c.ID))
	f.get("/clients/12345/config")
	f.get("/clients/12345/qr")
	after := f.files()
	if len(before) != len(after) {
		for p := range after {
			if _, ok := before[p]; !ok {
				t.Errorf("file created by on-demand handler: %s", p)
			}
		}
		for p := range before {
			if _, ok := after[p]; !ok {
				t.Errorf("file removed by on-demand handler: %s", p)
			}
		}
	}
}

// TestOndemandNoSecretsInLogs drives error paths and the dashboard with
// a captured log sink: private/preshared keys never reach logs (the
// only allowed locations are the config/QR response bodies).
func TestOndemandNoSecretsInLogs(t *testing.T) {
	var logBuf bytes.Buffer
	f := newFixtureWithLogger(t, &logBuf)
	c, priv, psk := f.addClient("ivan")
	serverRow, err := db.ServerRow(f.h)
	if err != nil {
		t.Fatalf("ServerRow: %v", err)
	}
	f.get("/clients/12345/config")
	f.get("/clients/12345/qr")
	f.get("/clients/abc/qr")
	f.get("/")
	f.setStatus(upStatusWith(peerFor(c.PublicKey)))
	f.get("/")
	logs := logBuf.String()
	for _, secret := range []string{priv, psk, serverRow.PrivateKey, samplePrivateKey} {
		if secret != "" && strings.Contains(logs, secret) {
			t.Errorf("secret %q leaked into logs", secret)
		}
	}
	for _, field := range []string{"PrivateKey", "PresharedKey"} {
		if strings.Contains(logs, field) {
			t.Errorf("secret field %q in logs", field)
		}
	}
}

// TestOndemandMethodsEnforced mirrors the mutation method check: only
// GET is wired for config/qr; other methods answer the generic 404.
func TestOndemandMethodsEnforced(t *testing.T) {
	f := newFixture(t)
	c, _, _ := f.addClient("judy")
	for _, m := range []string{http.MethodPost, http.MethodDelete, http.MethodPut} {
		for _, p := range []string{
			fmt.Sprintf("/clients/%d/config", c.ID),
			fmt.Sprintf("/clients/%d/qr", c.ID),
		} {
			req := httptest.NewRequest(m, p, nil)
			rec := httptest.NewRecorder()
			f.server.ServeHTTP(rec, req)
			if rec.Code != http.StatusNotFound {
				t.Errorf("%s %s: code = %d, want 404", m, p, rec.Code)
			}
		}
	}
}

// TestConfigDownloadErrorsAreExplicit checks the M4 invariant once
// more at the HTTP layer: no silent no-op is possible (every unknown id
// yields an error status, never an empty 200).
func TestConfigDownloadErrorsAreExplicit(t *testing.T) {
	f := newFixture(t)
	for _, p := range []string{"/clients/0/config", "/clients/-1/config", "/clients/999999/config"} {
		if rec := f.get(p); rec.Code != http.StatusNotFound {
			t.Errorf("%s: code = %d, want 404", p, rec.Code)
		}
	}
}
