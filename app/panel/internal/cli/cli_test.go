package cli

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/awgconf"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/keys"
)

const (
	testServerCIDR = "10.8.0.1/24"
	testEndpoint   = "vpn.example.com:51820"
	testDNS        = "8.8.8.8"
	testAWGParams  = `{"jc":5,"s1":80,"h1":"1-10","i1":"<b 0x1a2b><t>"}`
)

type ctx struct {
	t       *testing.T
	dbPath  string
	cfgPath string
	dir     string
}

// newCtx provides an isolated database and awg0.conf per test, wired
// through the same environment variables the binary honors.
func newCtx(t *testing.T) *ctx {
	t.Helper()
	dir := t.TempDir()
	c := &ctx{
		t:       t,
		dir:     dir,
		dbPath:  filepath.Join(dir, "amnezia.sqlite"),
		cfgPath: filepath.Join(dir, "awg0.conf"),
	}
	t.Setenv("AMNEZIA_DB_PATH", c.dbPath)
	t.Setenv("AMNEZIA_CONFIG_PATH", c.cfgPath)
	return c
}

// run invokes the CLI like the binary does and returns code, stdout,
// stderr.
func (c *ctx) run(args ...string) (int, string, string) {
	c.t.Helper()
	var out, errb bytes.Buffer
	code := Run(args, &out, &errb)
	return code, out.String(), errb.String()
}

// mustRun asserts success and returns stdout (stderr must be empty).
func (c *ctx) mustRun(args ...string) string {
	c.t.Helper()
	code, out, errb := c.run(args...)
	if code != 0 {
		c.t.Fatalf("panel %v: exit %d, stderr: %s", args, code, errb)
	}
	if errb != "" {
		c.t.Fatalf("panel %v: unexpected stderr: %s", args, errb)
	}
	return out
}

// openDB opens the test database (created if missing, migrated).
func (c *ctx) openDB() *sql.DB {
	c.t.Helper()
	h, err := db.Open(c.dbPath)
	if err != nil {
		c.t.Fatalf("open db: %v", err)
	}
	if err := db.Migrate(h); err != nil {
		c.t.Fatalf("migrate db: %v", err)
	}
	return h
}

func (c *ctx) seedServer(dns, awgParams string) {
	c.t.Helper()
	if awgParams == "" {
		awgParams = "{}"
	}
	priv, pub, err := keys.GenerateKeyPair()
	if err != nil {
		c.t.Fatalf("server keys: %v", err)
	}
	h := c.openDB()
	defer h.Close()
	if err := db.CreateServer(h, priv, pub, testServerCIDR, 51820, dns, awgParams, testEndpoint); err != nil {
		c.t.Fatalf("create server: %v", err)
	}
	if err := awgconf.Generate(h, c.cfgPath); err != nil {
		c.t.Fatalf("generate config: %v", err)
	}
}

// seedClient inserts a client with the fixed server network and returns
// its id, public key, private key and preshared key.
func (c *ctx) seedClient(name string) (id int64, pub, priv, psk string) {
	c.t.Helper()
	priv, pub, err := keys.GenerateKeyPair()
	if err != nil {
		c.t.Fatalf("client keys: %v", err)
	}
	psk, err = keys.GeneratePresharedKey()
	if err != nil {
		c.t.Fatalf("client psk: %v", err)
	}
	h := c.openDB()
	defer h.Close()
	rec, err := db.CreateClient(h, testServerCIDR, db.NewClient{
		Name: name, PrivateKey: priv, PublicKey: pub, PresharedKey: psk,
	})
	if err != nil {
		c.t.Fatalf("create client: %v", err)
	}
	if err := awgconf.Generate(h, c.cfgPath); err != nil {
		c.t.Fatalf("generate config: %v", err)
	}
	return rec.ID, pub, priv, psk
}

func (c *ctx) clientByID(id int64) *db.ClientRecord {
	c.t.Helper()
	h := c.openDB()
	defer h.Close()
	rec, err := db.ClientByID(h, id)
	if err != nil {
		c.t.Fatalf("client by id: %v", err)
	}
	return rec
}

// formKey returns a deterministic 32-byte base64 key: the byte b
// repeated 32 times. Used to build deliberately marked key material.
func formKey(b byte) string {
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{b}, 32))
}

func (c *ctx) readConfig() string {
	c.t.Helper()
	data, err := os.ReadFile(c.cfgPath)
	if err != nil {
		c.t.Fatalf("read config: %v", err)
	}
	return string(data)
}

// ---------- usage / exit 2 ----------

func TestRunNoArgs(t *testing.T) {
	c := newCtx(t)
	code, out, errb := c.run()
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if out != "" {
		t.Fatalf("stdout = %q, want empty", out)
	}
	if !strings.Contains(errb, "usage:") {
		t.Fatalf("stderr = %q, want usage text", errb)
	}
}

func TestRunUnknownCommand(t *testing.T) {
	c := newCtx(t)
	code, _, errb := c.run("frobnicate")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errb, "usage:") {
		t.Fatalf("stderr = %q, want usage text", errb)
	}
}

func TestNoSubcommand(t *testing.T) {
	for _, args := range [][]string{
		{"client"},
		{"server"},
		{"server", "edit"},
		{"client", "purge"},
	} {
		c := newCtx(t)
		code, _, _ := c.run(args...)
		if code != 2 {
			t.Fatalf("%v: exit = %d, want 2", args, code)
		}
	}
}

func TestUsageErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"init extra args", []string{"init", "extra"}},
		{"server init missing args", []string{"server", "init"}},
		{"server init one arg", []string{"server", "init", "10.8.0.0/24"}},
		{"server init extra arg", []string{"server", "init", "10.8.0.0/24", "51820", "extra"}},
		{"server init invalid address", []string{"server", "init", "not-an-address", "51820"}},
		{"server init ipv6 address", []string{"server", "init", "fd00::/64", "51820"}},
		{"server init invalid port", []string{"server", "init", "10.8.0.0/24", "70000"}},
		{"server init non-numeric port", []string{"server", "init", "10.8.0.0/24", "abc"}},
		{"server init negative port", []string{"server", "init", "10.8.0.0/24", "-1"}},
		{"server init invalid awg-params", []string{"server", "init", "10.8.0.0/24", "51820", "--awg-params", "{broken"}},
		{"server init semantically bad awg-params", []string{"server", "init", "10.8.0.0/24", "51820", "--awg-params", `{"jc":0}`}},
		{"server init invalid endpoint", []string{"server", "init", "10.8.0.0/24", "51820", "--endpoint", "noport"}},
		{"server init unknown flag", []string{"server", "init", "10.8.0.0/24", "51820", "--mtu", "1400"}},
		{"server init flag without value", []string{"server", "init", "10.8.0.0/24", "51820", "--endpoint"}},
		{"add missing name", []string{"client", "add"}},
		{"add extra positional", []string{"client", "add", "bob", "alice"}},
		{"add empty name", []string{"client", "add", ""}},
		{"add whitespace name", []string{"client", "add", "   "}},
		{"add 65-char name", []string{"client", "add", strings.Repeat("x", 65)}},
		{"add invalid expiry", []string{"client", "add", "bob", "--expires-at", "tomorrow"}},
		{"add unknown flag", []string{"client", "add", "bob", "--dns", "1.1.1.1"}},
		{"add missing flag value", []string{"client", "add", "bob", "--expires-at"}},
		{"list extra args", []string{"client", "list", "x"}},
		{"list unknown flag", []string{"client", "list", "--all"}},
		{"show missing id", []string{"client", "show"}},
		{"show non-numeric id", []string{"client", "show", "abc"}},
		{"show extra arg", []string{"client", "show", "1", "2"}},
		{"enable missing id", []string{"client", "enable"}},
		{"disable missing id", []string{"client", "disable"}},
		{"rename missing args", []string{"client", "rename", "1"}},
		{"rename extra arg", []string{"client", "rename", "1", "bob", "x"}},
		{"rename empty name", []string{"client", "rename", "1", ""}},
		{"set-expiry missing value", []string{"client", "set-expiry", "1"}},
		{"set-expiry invalid date", []string{"client", "set-expiry", "1", "2026-13-99"}},
		{"set-expiry invalid format", []string{"client", "set-expiry", "1", "2026-08-10 10:00:00"}},
		{"set-expiry unknown token", []string{"client", "set-expiry", "1", "never"}},
		{"delete missing id", []string{"client", "delete"}},
		{"config missing id", []string{"client", "config"}},
		{"config non-numeric id", []string{"client", "config", "abc"}},
		{"dash token", []string{"client", "add", "-n"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := newCtx(t)
			code, out, _ := c.run(tc.args...)
			if code != 2 {
				t.Fatalf("panel %v: exit = %d, want 2", tc.args, code)
			}
			if out != "" {
				t.Fatalf("panel %v: stdout = %q, want empty on usage error", tc.args, out)
			}
		})
	}
}

// ---------- server init ----------

func TestServerInitHappy(t *testing.T) {
	c := newCtx(t)
	out := c.mustRun("server", "init", testServerCIDR, "51820", "--endpoint", testEndpoint)
	if !strings.Contains(out, "panel server init: ok") {
		t.Fatalf("stdout = %q", out)
	}
	h := c.openDB()
	defer h.Close()
	server, err := db.ServerRow(h)
	if err != nil {
		t.Fatalf("server row: %v", err)
	}
	if server.Address != testServerCIDR || server.ListenPort != 51820 {
		t.Fatalf("server = %+v", server)
	}
	if !keys.ValidKey(server.PrivateKey) || !keys.ValidKey(server.PublicKey) {
		t.Fatalf("server keys invalid: %+v", server)
	}
	if ep, ok, err := db.GetSetting(h, "endpoint"); err != nil || !ok || ep != testEndpoint {
		t.Fatalf("endpoint setting = %q, %v, %v", ep, ok, err)
	}
	if !strings.Contains(c.readConfig(), "PrivateKey = "+server.PrivateKey) {
		t.Fatal("config missing server private key")
	}
}

func TestServerInitWithoutEndpointWarns(t *testing.T) {
	c := newCtx(t)
	code, out, errb := c.run("server", "init", testServerCIDR, "51820")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (--endpoint is optional)", code)
	}
	if !strings.Contains(out, "panel server init: ok") {
		t.Fatalf("stdout = %q", out)
	}
	if !strings.Contains(errb, "warning") || !strings.Contains(errb, "endpoint") {
		t.Fatalf("stderr = %q, want endpoint warning", errb)
	}
}

func TestServerInitFullFlags(t *testing.T) {
	c := newCtx(t)
	c.mustRun("server", "init", testServerCIDR, "51820",
		"--dns", testDNS, "--awg-params", testAWGParams, "--endpoint", testEndpoint)
	h := c.openDB()
	defer h.Close()
	server, err := db.ServerRow(h)
	if err != nil {
		t.Fatalf("server row: %v", err)
	}
	if server.DNS != testDNS || server.AWGParams != testAWGParams {
		t.Fatalf("server = %+v", server)
	}
}

func TestServerInitDuplicate(t *testing.T) {
	c := newCtx(t)
	c.seedServer("", "")
	code, _, errb := c.run("server", "init", "10.9.0.0/24", "51821")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(errb, "already exists") {
		t.Fatalf("stderr = %q, want ErrServerExists text", errb)
	}
	h := c.openDB()
	defer h.Close()
	server, err := db.ServerRow(h)
	if err != nil {
		t.Fatalf("server row: %v", err)
	}
	if server.Address != testServerCIDR || server.ListenPort != 51820 {
		t.Fatalf("duplicate init changed the row: %+v", server)
	}
}

func TestServerInitBadAWGParamsLeavesNoRow(t *testing.T) {
	c := newCtx(t)
	code, _, _ := c.run("server", "init", testServerCIDR, "51820",
		"--awg-params", `{"jc":0}`, "--endpoint", testEndpoint)
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (usage)", code)
	}
	if _, err := os.Stat(c.dbPath); err == nil {
		t.Fatal("database must not be created on a pre-flight failure")
	}
}

// ---------- client add ----------

func TestClientAdd(t *testing.T) {
	c := newCtx(t)
	c.seedServer("", "")
	code, out, errb := c.run("client", "add", "bob")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errb)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "id: ") || !strings.HasPrefix(lines[1], "public_key: ") {
		t.Fatalf("stdout = %q, want exactly id: and public_key:", out)
	}
	pub := strings.TrimPrefix(lines[1], "public_key: ")
	if !keys.ValidKey(pub) {
		t.Fatalf("printed public key invalid: %q", pub)
	}
	h := c.openDB()
	defer h.Close()
	clients, err := db.ClientsAll(h)
	if err != nil || len(clients) != 1 {
		t.Fatalf("clients = %v, %v", clients, err)
	}
	if clients[0].Name != "bob" || clients[0].Address != "10.8.0.2/32" || !clients[0].Enabled {
		t.Fatalf("client = %+v", clients[0])
	}
	if !strings.Contains(c.readConfig(), "PublicKey = "+pub) {
		t.Fatal("config missing new peer")
	}
}

func TestClientAddSecondGetsNextIP(t *testing.T) {
	c := newCtx(t)
	c.seedServer("", "")
	c.mustRun("client", "add", "bob")
	c.mustRun("client", "add", "alice")
	h := c.openDB()
	defer h.Close()
	clients, err := db.ClientsAll(h)
	if err != nil || len(clients) != 2 {
		t.Fatalf("clients = %v, %v", clients, err)
	}
	if clients[0].Address != "10.8.0.2/32" || clients[1].Address != "10.8.0.3/32" {
		t.Fatalf("addresses = %s, %s", clients[0].Address, clients[1].Address)
	}
}

func TestClientAddWithExpiry(t *testing.T) {
	c := newCtx(t)
	c.seedServer("", "")
	future := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
	if code, _, errb := c.run("client", "add", "bob", "--expires-at", future); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errb)
	}
	rec := c.clientByID(1)
	if rec.ExpiresAt == "" {
		t.Fatal("expires_at not stored")
	}
	if _, err := time.Parse(time.RFC3339, rec.ExpiresAt); err != nil {
		t.Fatalf("expires_at %q not RFC3339: %v", rec.ExpiresAt, err)
	}
}

func TestClientAddNameTrimmingAndUTF8(t *testing.T) {
	c := newCtx(t)
	c.seedServer("", "")
	c.mustRun("client", "add", "  bob  ")
	code, _, errb := c.run("client", "add", "клиент")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errb)
	}
	if rec := c.clientByID(1); rec.Name != "bob" {
		t.Fatalf("trimmed name = %q", rec.Name)
	}
	if rec := c.clientByID(2); rec.Name != "клиент" {
		t.Fatalf("utf8 name = %q", rec.Name)
	}
}

func TestClientAddName64Chars(t *testing.T) {
	c := newCtx(t)
	c.seedServer("", "")
	if code, _, _ := c.run("client", "add", strings.Repeat("x", 64)); code != 0 {
		t.Fatalf("exit = %d, want 0 for exactly 64 chars", code)
	}
}

// ---------- list / show ----------

func TestClientListHeaderAndOrder(t *testing.T) {
	c := newCtx(t)
	c.seedServer("", "")
	c.mustRun("client", "add", "bob")
	c.mustRun("client", "add", "alice")
	out := c.mustRun("client", "list")
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if lines[0] != "id\tname\taddress\tenabled\texpires_at\tpublic_key" {
		t.Fatalf("header = %q", lines[0])
	}
	if len(lines) != 3 {
		t.Fatalf("lines = %d, want header + 2 clients", len(lines))
	}
	for i, wantName := range []string{"bob", "alice"} {
		fields := strings.Split(lines[i+1], "\t")
		if len(fields) != 6 {
			t.Fatalf("row %d fields = %d: %q", i+1, len(fields), lines[i+1])
		}
		if fields[0] != fmt.Sprint(i+1) || fields[1] != wantName ||
			fields[2] != fmt.Sprintf("10.8.0.%d/32", i+2) || fields[3] != "1" || fields[4] != "" {
			t.Fatalf("row %d = %q", i+1, lines[i+1])
		}
		if !keys.ValidKey(fields[5]) {
			t.Fatalf("row %d public key invalid: %q", i+1, fields[5])
		}
	}
}

func TestClientListEmpty(t *testing.T) {
	c := newCtx(t)
	c.seedServer("", "")
	if out := c.mustRun("client", "list"); out != "id\tname\taddress\tenabled\texpires_at\tpublic_key\n" {
		t.Fatalf("stdout = %q", out)
	}
}

func TestClientShow(t *testing.T) {
	c := newCtx(t)
	c.seedServer("", "")
	id, pub, _, _ := c.seedClient("bob")
	out := c.mustRun("client", "show", fmt.Sprint(id))
	for _, want := range []string{
		"id: " + fmt.Sprint(id),
		"name: bob",
		"address: 10.8.0.2/32",
		"enabled: true",
		"public_key: " + pub,
		"expired: false",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("show output missing %q:\n%s", want, out)
		}
	}
}

func TestClientShowUnknownID(t *testing.T) {
	c := newCtx(t)
	c.seedServer("", "")
	code, _, errb := c.run("client", "show", "999")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(errb, "panel client show:") || !strings.Contains(errb, "not found") {
		t.Fatalf("stderr = %q", errb)
	}
}

// ---------- enable / disable ----------

func TestEnableDisablePeerInConfig(t *testing.T) {
	c := newCtx(t)
	c.seedServer("", "")
	id, pub, _, _ := c.seedClient("bob")

	if !strings.Contains(c.readConfig(), "PublicKey = "+pub) {
		t.Fatal("peer missing before disable")
	}
	c.mustRun("client", "disable", fmt.Sprint(id))
	if strings.Contains(c.readConfig(), "PublicKey = "+pub) {
		t.Fatal("peer still in config after disable")
	}
	c.mustRun("client", "enable", fmt.Sprint(id))
	if !strings.Contains(c.readConfig(), "PublicKey = "+pub) {
		t.Fatal("peer missing after enable")
	}
	if out := c.mustRun("client", "show", fmt.Sprint(id)); !strings.Contains(out, "enabled: true") {
		t.Fatalf("show = %q", out)
	}
}

func TestEnableDisableIdempotent(t *testing.T) {
	c := newCtx(t)
	c.seedServer("", "")
	id, _, _, _ := c.seedClient("bob")
	for _, args := range [][]string{
		{"client", "enable", fmt.Sprint(id)},
		{"client", "enable", fmt.Sprint(id)},
		{"client", "disable", fmt.Sprint(id)},
		{"client", "disable", fmt.Sprint(id)},
	} {
		if code, out, errb := c.run(args...); code != 0 || errb != "" {
			t.Fatalf("%v: exit %d stderr %q stdout %q", args, code, errb, out)
		}
	}
}

func TestEnableDisableUnknownIDExit1(t *testing.T) {
	c := newCtx(t)
	c.seedServer("", "")
	for _, args := range [][]string{
		{"client", "enable", "999"},
		{"client", "disable", "999"},
	} {
		code, _, errb := c.run(args...)
		if code != 1 {
			t.Fatalf("%v: exit = %d, want 1", args, code)
		}
		if !strings.Contains(errb, "not found") {
			t.Fatalf("%v: stderr = %q", args, errb)
		}
	}
}

// ---------- rename ----------

func TestRename(t *testing.T) {
	c := newCtx(t)
	c.seedServer("", "")
	id, _, _, _ := c.seedClient("bob")
	c.mustRun("client", "rename", fmt.Sprint(id), "robert")
	// idempotent re-run
	c.mustRun("client", "rename", fmt.Sprint(id), "robert")
	if out := c.mustRun("client", "show", fmt.Sprint(id)); !strings.Contains(out, "name: robert") {
		t.Fatalf("show = %q", out)
	}
}

func TestRenameUnknownID(t *testing.T) {
	c := newCtx(t)
	c.seedServer("", "")
	if code, _, _ := c.run("client", "rename", "999", "nope"); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
}

// ---------- set-expiry ----------

func TestSetExpiryAndNone(t *testing.T) {
	c := newCtx(t)
	c.seedServer("", "")
	id, pub, _, _ := c.seedClient("bob")

	past := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)
	c.mustRun("client", "set-expiry", fmt.Sprint(id), past)

	// expired is reflected without mutating enabled or expires_at
	if out := c.mustRun("client", "show", fmt.Sprint(id)); !strings.Contains(out, "expired: true") {
		t.Fatalf("show after past expiry = %q", out)
	}
	rec := c.clientByID(id)
	if !rec.Enabled {
		t.Fatal("set-expiry mutated enabled")
	}
	if rec.ExpiresAt != past {
		t.Fatalf("expires_at = %q, want %q", rec.ExpiresAt, past)
	}
	// expired client is excluded from awg0.conf but still in list
	if strings.Contains(c.readConfig(), "PublicKey = "+pub) {
		t.Fatal("expired client still in config")
	}
	if out := c.mustRun("client", "list"); !strings.Contains(out, pub) {
		t.Fatal("expired client missing from list")
	}

	// none clears the expiry, client returns to the config
	c.mustRun("client", "set-expiry", fmt.Sprint(id), "none")
	if out := c.mustRun("client", "show", fmt.Sprint(id)); !strings.Contains(out, "expired: false") {
		t.Fatalf("show after none = %q", out)
	}
	rec = c.clientByID(id)
	if rec.ExpiresAt != "" {
		t.Fatalf("expires_at = %q, want empty", rec.ExpiresAt)
	}
	if !strings.Contains(c.readConfig(), "PublicKey = "+pub) {
		t.Fatal("client missing from config after clearing expiry")
	}
}

func TestSetExpiryFutureKeepsInConfig(t *testing.T) {
	c := newCtx(t)
	c.seedServer("", "")
	id, pub, _, _ := c.seedClient("bob")
	future := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
	c.mustRun("client", "set-expiry", fmt.Sprint(id), future)
	if !strings.Contains(c.readConfig(), "PublicKey = "+pub) {
		t.Fatal("future-expiry client dropped from config")
	}
}

func TestSetExpiryUnknownID(t *testing.T) {
	c := newCtx(t)
	c.seedServer("", "")
	if code, _, _ := c.run("client", "set-expiry", "999", "none"); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
}

// ---------- delete ----------

func TestDelete(t *testing.T) {
	c := newCtx(t)
	c.seedServer("", "")
	id, pub, _, _ := c.seedClient("bob")
	c.mustRun("client", "delete", fmt.Sprint(id))
	if strings.Contains(c.readConfig(), "PublicKey = "+pub) {
		t.Fatal("deleted peer still in config")
	}
	h := c.openDB()
	defer h.Close()
	if _, err := db.ClientByID(h, id); err == nil {
		t.Fatal("client row still exists")
	}
	if code, _, _ := c.run("client", "delete", fmt.Sprint(id)); code != 1 {
		t.Fatalf("second delete: exit = %d, want 1", code)
	}
}

func TestDeleteUnknownID(t *testing.T) {
	c := newCtx(t)
	c.seedServer("", "")
	code, _, errb := c.run("client", "delete", "999")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(errb, "not found") {
		t.Fatalf("stderr = %q", errb)
	}
}

// ---------- client config ----------

func TestClientConfigGolden(t *testing.T) {
	c := newCtx(t)
	c.seedServer(testDNS, testAWGParams)
	id, _, _, _ := c.seedClient("bob")

	code, out, errb := c.run("client", "config", fmt.Sprint(id))
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errb)
	}
	if errb != "" {
		t.Fatalf("stderr = %q, want empty", errb)
	}
	h := c.openDB()
	server, err := db.ServerRow(h)
	if err != nil {
		t.Fatalf("server: %v", err)
	}
	client, err := db.ClientByID(h, id)
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	h.Close()

	var want strings.Builder
	want.WriteString("[Interface]\n")
	line := func(k, v string) {
		want.WriteString(k + " = " + v + "\n")
	}
	line("PrivateKey", client.PrivateKey)
	line("Address", client.Address)
	line("DNS", server.DNS)
	line("Jc", "5")
	line("S1", "80")
	line("H1", "1-10")
	line("I1", "<b 0x1a2b><t>")
	want.WriteString("\n")
	want.WriteString("[Peer]\n")
	line("PublicKey", server.PublicKey)
	line("PresharedKey", client.PresharedKey)
	line("AllowedIPs", "0.0.0.0/0")
	line("Endpoint", testEndpoint)
	if out != want.String() {
		t.Fatalf("golden mismatch:\n-- got --\n%s\n-- want --\n%s", out, want.String())
	}
}

func TestClientConfigDisabledAndExpiredStillWork(t *testing.T) {
	c := newCtx(t)
	c.seedServer("", "")
	id, _, _, _ := c.seedClient("bob")
	c.mustRun("client", "disable", fmt.Sprint(id))
	c.mustRun("client", "set-expiry", fmt.Sprint(id),
		time.Now().UTC().Add(-1*time.Hour).Format(time.RFC3339))
	code, out, _ := c.run("client", "config", fmt.Sprint(id))
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(out, "[Interface]") || !strings.Contains(out, "[Peer]") {
		t.Fatalf("config = %q", out)
	}
}

func TestClientConfigNoEndpoint(t *testing.T) {
	c := newCtx(t)
	// server bootstrap without the endpoint setting
	priv, pub, err := keys.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	h := c.openDB()
	if err := db.CreateServer(h, priv, pub, testServerCIDR, 51820, "", "{}", ""); err != nil {
		t.Fatal(err)
	}
	if err := awgconf.Generate(h, c.cfgPath); err != nil {
		t.Fatal(err)
	}
	h.Close()
	// wait: client creation must be done after the server exists; reuse
	// the CLI to keep the state canonical
	c.mustRun("client", "add", "bob")
	id := int64(1)
	code, _, errb := c.run("client", "config", fmt.Sprint(id))
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(errb, "endpoint") {
		t.Fatalf("stderr = %q, want endpoint diagnostic", errb)
	}
}

func TestClientConfigUnknownID(t *testing.T) {
	c := newCtx(t)
	c.seedServer("", "")
	if code, _, _ := c.run("client", "config", "999"); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
}

func TestClientConfigDeterministic(t *testing.T) {
	c := newCtx(t)
	c.seedServer(testDNS, testAWGParams)
	id, _, _, _ := c.seedClient("bob")
	first := c.mustRun("client", "config", fmt.Sprint(id))
	if second := c.mustRun("client", "config", fmt.Sprint(id)); first != second {
		t.Fatal("config not deterministic across calls")
	}
}

func TestClientConfigWritesNoFile(t *testing.T) {
	c := newCtx(t)
	c.seedServer("", "")
	id, _, _, _ := c.seedClient("bob")
	c.mustRun("client", "config", fmt.Sprint(id))
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(strings.ToLower(e.Name()), "client") {
			t.Fatalf("client config file written to disk: %s", e.Name())
		}
	}
}

// ---------- runtime errors ----------

func TestClientMutationsWithoutServer(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"add", []string{"client", "add", "bob"}, "no server row"},
		{"config", []string{"client", "config", "1"}, "no server row"},
		{"show", []string{"client", "show", "1"}, "not found"},
		{"enable", []string{"client", "enable", "1"}, "not found"},
		{"disable", []string{"client", "disable", "1"}, "not found"},
		{"rename", []string{"client", "rename", "1", "x"}, "not found"},
		{"set-expiry", []string{"client", "set-expiry", "1", "none"}, "not found"},
		{"delete", []string{"client", "delete", "1"}, "not found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newCtx(t)
			code, _, errb := c.run(tc.args...)
			if code != 1 {
				t.Fatalf("%v: exit = %d, want 1", tc.args, code)
			}
			if !strings.Contains(errb, tc.want) {
				t.Fatalf("%v: stderr = %q, want %q", tc.args, errb, tc.want)
			}
		})
	}
}

func TestClientListWithoutServerOK(t *testing.T) {
	c := newCtx(t)
	if out := c.mustRun("client", "list"); out != "id\tname\taddress\tenabled\texpires_at\tpublic_key\n" {
		t.Fatalf("stdout = %q", out)
	}
}

func TestGenerateFailureKeepsOldConfigAndCommitsDB(t *testing.T) {
	c := newCtx(t)
	c.seedServer("", "")
	id, pub, _, _ := c.seedClient("bob")
	before := c.readConfig()

	badPath := filepath.Join(c.dir, "missing", "dir", "awg0.conf")
	t.Setenv("AMNEZIA_CONFIG_PATH", badPath)

	code, _, errb := c.run("client", "disable", fmt.Sprint(id))
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.HasPrefix(errb, "panel client disable:") {
		t.Fatalf("stderr = %q", errb)
	}
	if !strings.Contains(errb, "generate config") {
		t.Fatalf("stderr = %q, want generate diagnostic", errb)
	}

	t.Setenv("AMNEZIA_CONFIG_PATH", c.cfgPath)
	// the DB mutation was committed despite the failed regeneration
	if rec := c.clientByID(id); rec.Enabled {
		t.Fatal("DB mutation not applied despite Generate failure")
	}
	// the old config is byte-identical: WriteAtomic never renamed a file
	if got := c.readConfig(); got != before {
		t.Fatal("config changed after failed Generate")
	}
	// and a later successful operation regenerates from the new state
	c.mustRun("client", "enable", fmt.Sprint(id))
	if !strings.Contains(c.readConfig(), "PublicKey = "+pub) {
		t.Fatal("peer missing after recovery run")
	}
}

// ---------- allocator exhaustion ----------

func TestAllocatorExhaustionSmallNetwork(t *testing.T) {
	c := newCtx(t)
	// server 10.0.0.1/30 → one free host (10.0.0.2); 10.0.0.3 is the
	// broadcast address and must never be handed out
	c.mustRun("server", "init", "10.0.0.1/30", "51820", "--endpoint", testEndpoint)
	c.mustRun("client", "add", "a")
	code, _, errb := c.run("client", "add", "b")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(errb, "no free client address") {
		t.Fatalf("stderr = %q", errb)
	}
}

// ---------- secrets ----------

func TestSecretsNeverLeakInErrors(t *testing.T) {
	markedPriv := formKey(0xAB)
	markedPSK := formKey(0xCD)
	c := newCtx(t)
	c.seedServer("", "")
	h := c.openDB()
	// a client whose stored keys are corrupt: private key is broken (so
	// GenerateClient validation fails) and the public key is broken too
	// (so config regeneration fails for every mutating command)
	if _, err := h.Exec(
		`INSERT INTO clients (name, private_key, public_key, preshared_key, address, enabled, created_at, updated_at)
		 VALUES ('corrupt', ?, ?, ?, '10.8.0.2/32', 1, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		markedPriv+"-broken", formKey(0x11)+"-broken", markedPSK,
	); err != nil {
		t.Fatalf("insert corrupt client: %v", err)
	}
	h.Close()
	// every command that touches the broken row fails without echoing
	// key material (the marked values would be spotted if leaked)
	for _, args := range [][]string{
		{"client", "config", "1"},
		{"client", "enable", "1"},
		{"client", "rename", "1", "x"},
		{"client", "set-expiry", "1", "none"},
	} {
		code, out, errb := c.run(args...)
		if code != 1 {
			t.Fatalf("%v: exit = %d, stderr = %s", args, code, errb)
		}
		if strings.Contains(errb+out, markedPriv) || strings.Contains(errb+out, markedPSK) {
			t.Fatalf("%v leaks a marked key: %s%s", args, out, errb)
		}
	}
	// disable succeeds (the broken peer leaves the config) but must not
	// leak the secrets either
	code, out, errb := c.run("client", "disable", "1")
	if code != 0 {
		t.Fatalf("disable: exit = %d", code)
	}
	if strings.Contains(errb+out, markedPriv) || strings.Contains(errb+out, markedPSK) {
		t.Fatalf("disable leaks a marked key: %s%s", out, errb)
	}
	// show has no failing path here, but must not leak the secrets either
	shCode, shOut, _ := c.run("client", "show", "1")
	if shCode != 0 {
		t.Fatalf("show: exit = %d", shCode)
	}
	if strings.Contains(shOut, markedPriv) || strings.Contains(shOut, markedPSK) {
		t.Fatalf("show leaks a marked key: %s", shOut)
	}
}

func TestListShowNeverLeakSecrets(t *testing.T) {
	markedPriv := formKey(0xAB)
	markedPSK := formKey(0xCD)
	markedPub := formKey(0x11)
	c := newCtx(t)
	c.seedServer("", "")
	h := c.openDB()
	if _, err := h.Exec(
		`INSERT INTO clients (name, private_key, public_key, preshared_key, address, enabled, created_at, updated_at)
		 VALUES ('marked', ?, ?, ?, '10.8.0.2/32', 1, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		markedPriv, markedPub, markedPSK,
	); err != nil {
		t.Fatalf("insert client: %v", err)
	}
	h.Close()
	for _, args := range [][]string{
		{"client", "list"},
		{"client", "show", "1"},
	} {
		code, out, errb := c.run(args...)
		if code != 0 {
			t.Fatalf("%v: exit %d", args, code)
		}
		if strings.Contains(out+errb, markedPriv) || strings.Contains(out+errb, markedPSK) {
			t.Fatalf("%v leaks a secret key", args)
		}
		if !strings.Contains(out, markedPub) {
			t.Fatalf("%v missing public key", args)
		}
	}
}

func TestClientConfigContainsRequiredSecrets(t *testing.T) {
	c := newCtx(t)
	c.seedServer("", "")
	priv, pub, err := keys.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	psk, err := keys.GeneratePresharedKey()
	if err != nil {
		t.Fatal(err)
	}
	h := c.openDB()
	if _, err := h.Exec(
		`INSERT INTO clients (name, private_key, public_key, preshared_key, address, enabled, created_at, updated_at)
		 VALUES ('marked', ?, ?, ?, '10.8.0.2/32', 1, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		priv, pub, psk,
	); err != nil {
		t.Fatalf("insert client: %v", err)
	}
	h.Close()
	code, out, _ := c.run("client", "config", "1")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(out, "PrivateKey = "+priv) {
		t.Fatal("config missing private key")
	}
	if !strings.Contains(out, "PresharedKey = "+psk) {
		t.Fatal("config missing preshared key")
	}
}

// ---------- regression: init and serve ----------

func TestInitContract(t *testing.T) {
	c := newCtx(t)
	// without a server row init exits 1 with the M3 error
	code, _, errb := c.run("init")
	if code != 1 {
		t.Fatalf("init without server: exit = %d, want 1", code)
	}
	if !strings.Contains(errb, "no server row (id=1); insert server configuration first") {
		t.Fatalf("stderr = %q", errb)
	}
	c.seedServer("", "")
	if out := c.mustRun("init"); out != "panel init: awg0.conf generated\n" {
		t.Fatalf("stdout = %q", out)
	}
	c.mustRun("init")
}

func TestServeContract(t *testing.T) {
	c := newCtx(t)
	code, out, errb := c.run("serve")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if out != "" {
		t.Fatalf("stdout = %q, want empty", out)
	}
	if errb != `panel serve: not implemented in M2. Scheduled: M6 ("Basic panel CRUD").`+"\n" {
		t.Fatalf("stderr = %q", errb)
	}
}

// ---------- config matches DB after a full lifecycle ----------

func TestConfigMatchesDBAfterLifecycle(t *testing.T) {
	c := newCtx(t)
	c.seedServer("", "")
	id1, pub1, _, _ := c.seedClient("bob")
	id2, pub2, _, _ := c.seedClient("alice")

	c.mustRun("client", "disable", fmt.Sprint(id1))
	c.mustRun("client", "delete", fmt.Sprint(id2))

	cfg := c.readConfig()
	if strings.Contains(cfg, "PublicKey = "+pub1) {
		t.Fatal("disabled peer in config")
	}
	if strings.Contains(cfg, "PublicKey = "+pub2) {
		t.Fatal("deleted peer in config")
	}
	if strings.Contains(cfg, "[Peer]") {
		t.Fatalf("unexpected [Peer] section:\n%s", cfg)
	}
}
