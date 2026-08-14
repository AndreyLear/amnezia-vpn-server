package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/awgconf"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

// paramLines extracts the AWG J/S/H/I option lines from an awg0.conf or
// client config text.
func paramLines(cfg string) map[string]string {
	m := make(map[string]string)
	for _, ln := range strings.Split(cfg, "\n") {
		k, v, ok := strings.Cut(ln, " = ")
		if !ok {
			continue
		}
		switch k {
		case "Jc", "Jmin", "Jmax", "S1", "S2",
			"H1", "H2", "H3", "H4",
			"I1", "I2", "I3", "I4", "I5":
			m[k] = v
		}
	}
	return m
}

var allParamKeys = []string{
	"Jc", "Jmin", "Jmax", "S1", "S2",
	"H1", "H2", "H3", "H4",
	"I1", "I2", "I3", "I4", "I5",
}

func requireFullParams(t *testing.T, cfg string) map[string]string {
	t.Helper()
	got := paramLines(cfg)
	for _, key := range allParamKeys {
		if got[key] == "" {
			t.Fatalf("config missing %s line:\n%s", key, cfg)
		}
	}
	return got
}

func TestServerInitGeneratesRandomAWGParams(t *testing.T) {
	c := newCtx(t)
	c.mustRun("server", "init", testServerCIDR, "51820", "--endpoint", testEndpoint)

	h := c.openDB()
	server, err := db.ServerRow(h)
	h.Close()
	if err != nil {
		t.Fatalf("server row: %v", err)
	}
	if server.AWGParams == "" || server.AWGParams == "{}" {
		t.Fatalf("awg_params = %q, want a generated full set", server.AWGParams)
	}
	p, err := awgconf.ParseParams(server.AWGParams)
	if err != nil {
		t.Fatalf("stored awg_params rejected by ParseParams: %v", err)
	}
	if p.Jc == nil || p.Jmin == nil || p.Jmax == nil || p.S1 == nil || p.S2 == nil {
		t.Fatalf("numeric fields not generated: %+v", p)
	}
	for name, h := range map[string]string{"h1": p.H1, "h2": p.H2, "h3": p.H3, "h4": p.H4} {
		if h == "" {
			t.Fatalf("%s not generated", name)
		}
	}
	for name, chain := range map[string]string{"i1": p.I1, "i2": p.I2, "i3": p.I3, "i4": p.I4, "i5": p.I5} {
		if chain == "" {
			t.Fatalf("%s not generated", name)
		}
	}
	requireFullParams(t, c.readConfig())
}

func TestServerInitGeneratesDistinctParams(t *testing.T) {
	c1 := newCtx(t)
	c1.mustRun("server", "init", testServerCIDR, "51820", "--endpoint", testEndpoint)
	h1 := c1.openDB()
	s1, err := db.ServerRow(h1)
	h1.Close()
	if err != nil {
		t.Fatalf("server row 1: %v", err)
	}

	c2 := newCtx(t)
	c2.mustRun("server", "init", testServerCIDR, "51820", "--endpoint", testEndpoint)
	h2 := c2.openDB()
	s2, err := db.ServerRow(h2)
	h2.Close()
	if err != nil {
		t.Fatalf("server row 2: %v", err)
	}

	if s1.AWGParams == s2.AWGParams {
		t.Fatalf("two inits produced identical params: %s", s1.AWGParams)
	}
}

func TestServerInitExplicitAWGParamsPreserved(t *testing.T) {
	c := newCtx(t)
	c.mustRun("server", "init", testServerCIDR, "51820",
		"--awg-params", testAWGParams, "--endpoint", testEndpoint)
	h := c.openDB()
	server, err := db.ServerRow(h)
	h.Close()
	if err != nil {
		t.Fatalf("server row: %v", err)
	}
	if server.AWGParams != testAWGParams {
		t.Fatalf("explicit awg_params mutated: %q, want %q", server.AWGParams, testAWGParams)
	}
}

func TestServerInitExplicitEmptyParamsOptOut(t *testing.T) {
	c := newCtx(t)
	c.mustRun("server", "init", testServerCIDR, "51820",
		"--awg-params", "{}", "--endpoint", testEndpoint)
	h := c.openDB()
	server, err := db.ServerRow(h)
	h.Close()
	if err != nil {
		t.Fatalf("server row: %v", err)
	}
	if server.AWGParams != "{}" {
		t.Fatalf("explicit {} mutated: %q", server.AWGParams)
	}
	if cfg := c.readConfig(); strings.Contains(cfg, "Jc =") || strings.Contains(cfg, "I1 =") {
		t.Fatalf("obfuscation lines present despite explicit {}:\n%s", cfg)
	}
}

func TestClientConfigMirrorsGeneratedParams(t *testing.T) {
	c := newCtx(t)
	c.mustRun("server", "init", testServerCIDR, "51820", "--endpoint", testEndpoint)
	c.mustRun("client", "add", "bob")
	id := int64(1)

	serverParams := requireFullParams(t, c.readConfig())
	clientCfg := c.mustRun("client", "config", "1")
	if !strings.Contains(clientCfg, "Endpoint = "+testEndpoint) {
		t.Fatalf("client config missing endpoint:\n%s", clientCfg)
	}
	clientParams := requireFullParams(t, clientCfg)

	h := c.openDB()
	_, err := db.ClientByID(h, id)
	h.Close()
	if err != nil {
		t.Fatalf("client row: %v", err)
	}
	for _, key := range allParamKeys {
		if clientParams[key] != serverParams[key] {
			t.Fatalf("%s mismatch: client %q vs server %q", key, clientParams[key], serverParams[key])
		}
	}
}

func TestServerGenAWGParamsOutput(t *testing.T) {
	c := newCtx(t)
	out := c.mustRun("server", "gen-awg-params")
	raw := strings.TrimSpace(out)
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("stdout is not a JSON object: %q (%v)", raw, err)
	}
	for _, key := range []string{"jc", "jmin", "jmax", "s1", "s2", "h1", "h2", "h3", "h4", "i1", "i2", "i3", "i4", "i5"} {
		if _, ok := m[key]; !ok {
			t.Fatalf("missing key %q in %s", key, raw)
		}
	}
	if _, err := awgconf.ParseParams(raw); err != nil {
		t.Fatalf("generated params rejected by ParseParams: %v", err)
	}
}

func TestServerGenAWGParamsFeedsUpdate(t *testing.T) {
	c := newCtx(t)
	// legacy state: an initialized server with the empty "{}" set
	c.seedServer("", "")
	raw := strings.TrimSpace(c.mustRun("server", "gen-awg-params"))

	c.mustRun("server", "update", "--awg-params", raw)

	h := c.openDB()
	server, err := db.ServerRow(h)
	h.Close()
	if err != nil {
		t.Fatalf("server row: %v", err)
	}
	if server.AWGParams != raw {
		t.Fatalf("awg_params = %q, want %q", server.AWGParams, raw)
	}
	requireFullParams(t, c.readConfig())
}

func TestServerGenAWGParamsUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"server", "gen-awg-params", "extra"},
		{"server", "gen-awg-params", "--dns", "1.1.1.1"},
	} {
		c := newCtx(t)
		if code, _, _ := c.run(args...); code != 2 {
			t.Fatalf("panel %v: exit = %d, want 2", args, code)
		}
	}
}
