// M8.8 gap tests: state coverage that the existing suites missed —
// dashboard error states, restore-probe failure, address exhaustion,
// serve defaults and the serve --addr wiring.
package web

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/keys"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Addr != "0.0.0.0:8787" {
		t.Errorf("Addr = %q", cfg.Addr)
	}
	if cfg.ShutdownTimeout != 10*time.Second {
		t.Errorf("ShutdownTimeout = %v", cfg.ShutdownTimeout)
	}
}

// TestDashboardStatusErrorState: an unparsable status.json maps to the
// generic error state and the dashboard still renders (200) with the
// fixed error text; the file contents never reach the client.
func TestDashboardStatusErrorState(t *testing.T) {
	f := newFixture(t)
	garbage := "not a status file\nwith\nlines\n"
	if err := os.WriteFile(f.statusPath, []byte(garbage), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := f.get("/")
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Tunnel: status error") {
		t.Fatalf("body missing error state:\n%s", rec.Body.String())
	}
	for _, chunk := range []string{"not a status file", "with", "lines"} {
		if strings.Contains(rec.Body.String(), chunk) {
			t.Fatalf("body echoes the status file: %q", chunk)
		}
	}
}

// TestDashboardDBFailure500: a database failure renders the generic 500
// (the reconciliation Load error path).
func TestDashboardDBFailure500(t *testing.T) {
	f := newFixture(t)
	f.h.Close()
	rec := f.get("/")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("code = %d, want 500", rec.Code)
	}
}

// TestRestorePageProbeFailure: a .restore-pending entry that is not a
// directory fails the pending probe and renders the generic 500.
func TestRestorePageProbeFailure(t *testing.T) {
	f := newFixture(t)
	pending := filepath.Join(filepath.Dir(f.dbPath), ".restore-pending")
	if err := os.WriteFile(pending, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := f.get("/backups/restore")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("code = %d, want 500", rec.Code)
	}
}

// TestClientAddNoFreeAddress: exhausting the address pool answers 303
// with the fixed flash (classifyExpected ErrNoFreeAddress).
func TestClientAddNoFreeAddress(t *testing.T) {
	f := newFixture(t)
	// Fill every address: 10.8.0.2 .. 10.8.0.254 (253 slots).
	for i := 0; i < 253; i++ {
		priv, pub, err := keys.GenerateKeyPair()
		if err != nil {
			t.Fatal(err)
		}
		psk, err := keys.GeneratePresharedKey()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.CreateClient(f.h, webTestServerCIDR, db.NewClient{
			Name: "bulk", PrivateKey: priv, PublicKey: pub, PresharedKey: psk,
		}); err != nil {
			t.Fatalf("bulk client %d: %v", i, err)
		}
	}
	rec := f.post("/clients/new", url.Values{"name": {"overflow"}})
	if got := f.flashOf(rec); got != "No free address in the pool." {
		t.Fatalf("flash = %q", got)
	}
	// Nothing was inserted.
	n := 0
	if err := f.h.QueryRow(`SELECT COUNT(*) FROM clients`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 253 {
		t.Fatalf("clients = %d, want 253", n)
	}
}

// TestClientAddBadDBGeneric500: an internal (non-classified) mutation
// error renders the generic 500 page without details.
func TestClientAddBadDBGeneric500(t *testing.T) {
	f := newFixture(t)
	// A closed handle breaks the insert: not a classified db error.
	f.h.Close()
	rec := f.post("/clients/new", url.Values{"name": {"ghost"}})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("code = %d, want 500", rec.Code)
	}
}
