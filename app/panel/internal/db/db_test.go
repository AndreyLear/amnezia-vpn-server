package db

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/keys"
)

func openTest(t *testing.T, name string) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	handle, err := Open(path)
	if err != nil {
		t.Fatalf("Open(%s): %v", path, err)
	}
	t.Cleanup(func() { handle.Close() })
	return handle, path
}

func tableColumns(t *testing.T, handle *sql.DB, table string) []string {
	t.Helper()
	rows, err := handle.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		t.Fatalf("PRAGMA table_info(%s): %v", table, err)
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var (
			cid     int
			name    string
			typ     string
			notNull int
			dflt    sql.NullString
			pk      int
		)
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			t.Fatalf("scan: %v", err)
		}
		cols = append(cols, name)
	}
	return cols
}

func TestMigrateCreatesFullSchema(t *testing.T) {
	handle, _ := openTest(t, "amnezia.sqlite")
	if err := Migrate(handle); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	want := []string{"server", "clients", "settings", "auth", "schema_meta"}
	for _, table := range want {
		var name string
		err := handle.QueryRow(
			`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`,
			table,
		).Scan(&name)
		if err == sql.ErrNoRows {
			t.Errorf("table %q not created", table)
			continue
		}
		if err != nil {
			t.Fatalf("sqlite_master: %v", err)
		}
	}

	type columnSet struct {
		table string
		cols  []string
	}
	mustContain := func(cols []string, want ...string) {
		t.Helper()
		got := make(map[string]bool, len(cols))
		for _, c := range cols {
			got[c] = true
		}
		for _, w := range want {
			if !got[w] {
				t.Errorf("column %q missing in %s (got %v)", w, cols, cols)
			}
		}
	}
	for _, cs := range []columnSet{
		{"server", []string{"id", "private_key", "public_key", "address",
			"listen_port", "dns", "awg_params", "created_at", "updated_at"}},
		{"clients", []string{"id", "name", "private_key", "public_key",
			"preshared_key", "address", "enabled", "created_at",
			"updated_at", "expires_at"}},
		{"settings", []string{"key", "value"}},
		{"auth", []string{"id", "username", "password_hash", "totp_secret"}},
		{"schema_meta", []string{"key", "value"}},
	} {
		mustContain(tableColumns(t, handle, cs.table), cs.cols...)
	}

	if got, err := SchemaVersionStored(handle); err != nil {
		t.Fatalf("SchemaVersionStored: %v", err)
	} else if got != SchemaVersion {
		t.Errorf("schema_version = %q, want %q", got, SchemaVersion)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	handle, _ := openTest(t, "amnezia.sqlite")
	if err := Migrate(handle); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	if _, err := handle.Exec(
		`INSERT INTO settings (key, value) VALUES ('server_name', 'test')`); err != nil {
		t.Fatalf("seed setting: %v", err)
	}

	if err := Migrate(handle); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}

	// data written before the second run must survive
	var v string
	if err := handle.QueryRow(
		`SELECT value FROM settings WHERE key = 'server_name'`).Scan(&v); err != nil {
		t.Fatalf("row lost after re-migration: %v", err)
	}
	if v != "test" {
		t.Errorf("value = %q, want %q", v, "test")
	}
	if got, err := SchemaVersionStored(handle); err != nil {
		t.Fatalf("SchemaVersionStored after re-migration: %v", err)
	} else if got != SchemaVersion {
		t.Errorf("schema_version after re-migration = %q, want %q", got, SchemaVersion)
	}
}

func TestOpenCreatesParentDirectories(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "state", "amnezia.sqlite")
	handle, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer handle.Close()
	if err := Migrate(handle); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("database file not created: %v", err)
	}
}

func TestDefaultPathOverride(t *testing.T) {
	const override = "/tmp/amnezia-test.sqlite"
	t.Setenv("AMNEZIA_DB_PATH", override)
	if got := DefaultPath(); got != override {
		t.Errorf("DefaultPath() = %q, want %q", got, override)
	}
	t.Setenv("AMNEZIA_DB_PATH", "")
	if got := DefaultPath(); got != "/data/amnezia.sqlite" {
		t.Errorf("DefaultPath() without env = %q, want %q", got, "/data/amnezia.sqlite")
	}
}

// ---- M4: server bootstrap, client CRUD, address allocator, expiry ----

const (
	testPriv = "4OSe6B1rDXdY4RdVJ+eenaEJOqRVZ9kxx25z9bI0t28="
	testPub  = "qXvOE2SbilF5RNpHXPU8xQCTvUIxYPeTNI6R6p+bqnw="
	testPub2 = "a1IqKSrVxcXN16cMZIUZ6kof0oU0kvwb0w6Q8bgGggQ="
)

func seedServerM4(t *testing.T, handle *sql.DB, address string) error {
	t.Helper()
	return CreateServer(handle, testPriv, testPub, address, 51820, "", "{}", "vpn.example.com:51820")
}

func containsPeerAddr(handle *sql.DB, addr string) bool {
	clients, err := ClientsForConfig(handle)
	if err != nil {
		return false
	}
	for _, c := range clients {
		if c.Address == addr {
			return true
		}
	}
	return false
}

func TestOpenEnforcesMode0600(t *testing.T) {
	handle, path := openTest(t, "amnezia.sqlite")
	if err := Migrate(handle); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Fatalf("db file mode = %o, want 0600", perm)
	}
	// a DB previously created with looser permissions must be fixed on open
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	handle.Close()
	handle, _ = Open(path)
	defer handle.Close()
	st, err = os.Stat(path)
	if err != nil {
		t.Fatalf("Stat after reopen: %v", err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Fatalf("db file mode after reopen = %o, want 0600", perm)
	}
}

func TestCreateServerAndReadback(t *testing.T) {
	handle, _ := openTest(t, "amnezia.sqlite")
	if err := Migrate(handle); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := seedServerM4(t, handle, "10.8.0.1/24"); err != nil {
		t.Fatalf("CreateServer: %v", err)
	}
	s, err := ServerRow(handle)
	if err != nil {
		t.Fatalf("ServerRow: %v", err)
	}
	if s.PrivateKey != testPriv || s.PublicKey != testPub {
		t.Fatalf("server row keys mismatch: got %q / %q", s.PrivateKey, s.PublicKey)
	}
	if s.Address != "10.8.0.1/24" || s.ListenPort != 51820 {
		t.Fatalf("server row address/port mismatch")
	}
	if got, ok, err := GetSetting(handle, "endpoint"); err != nil {
		t.Fatalf("GetSetting: %v", err)
	} else if !ok || got != "vpn.example.com:51820" {
		t.Fatalf("endpoint setting = %q (ok=%v), want vpn.example.com:51820", got, ok)
	}
}

func TestCreateServerTwiceRejected(t *testing.T) {
	handle, _ := openTest(t, "amnezia.sqlite")
	if err := Migrate(handle); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := seedServerM4(t, handle, "10.8.0.1/24"); err != nil {
		t.Fatalf("CreateServer #1: %v", err)
	}
	err := CreateServer(handle, testPriv, testPub, "10.9.0.1/24", 51820, "", "{}", "")
	if !errors.Is(err, ErrServerExists) {
		t.Fatalf("CreateServer #2 error = %v, want ErrServerExists", err)
	}
	// the existing row must be untouched
	s, err := ServerRow(handle)
	if err != nil {
		t.Fatalf("ServerRow: %v", err)
	}
	if s.Address != "10.8.0.1/24" {
		t.Fatalf("server row modified by rejected bootstrap: %v", s.Address)
	}
}

func TestCreateServerIPv6Rejected(t *testing.T) {
	handle, _ := openTest(t, "amnezia.sqlite")
	if err := Migrate(handle); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	err := CreateServer(handle, testPriv, testPub, "fd00::1/64", 51820, "", "{}", "")
	if err == nil {
		t.Fatal("CreateServer accepted IPv6 address")
	}
}

func TestCreateServerInvalidInputs(t *testing.T) {
	handle, _ := openTest(t, "amnezia.sqlite")
	if err := Migrate(handle); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	for _, tc := range []struct {
		priv, pub, addr string
		port            int64
	}{
		{"bad", testPub, "10.8.0.1/24", 51820},
		{testPriv, "bad", "10.8.0.1/24", 51820},
		{testPriv, testPub, "not-a-cidr", 51820},
		{testPriv, testPub, "10.8.0.1/24", 70000},
	} {
		if err := CreateServer(handle, tc.priv, tc.pub, tc.addr, tc.port, "", "{}", ""); err == nil {
			t.Errorf("CreateServer(%q,%q,%q,%d) accepted invalid input", tc.priv, tc.pub, tc.addr, tc.port)
		}
	}
}

func TestCreateClientAllocatesSequentially(t *testing.T) {
	handle, _ := openTest(t, "amnezia.sqlite")
	if err := Migrate(handle); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := seedServerM4(t, handle, "10.8.0.1/24"); err != nil {
		t.Fatalf("CreateServer: %v", err)
	}
	c1, err := CreateClient(handle, "10.8.0.1/24", NewClient{Name: "a", PrivateKey: testPriv, PublicKey: testPub})
	if err != nil {
		t.Fatalf("CreateClient #1: %v", err)
	}
	c2, err := CreateClient(handle, "10.8.0.1/24", NewClient{Name: "b", PrivateKey: testPriv, PublicKey: testPub2})
	if err != nil {
		t.Fatalf("CreateClient #2: %v", err)
	}
	if c1.Address != "10.8.0.2/32" || c2.Address != "10.8.0.3/32" {
		t.Fatalf("addresses = %q, %q; want 10.8.0.2/32, 10.8.0.3/32", c1.Address, c2.Address)
	}
	if c1.ID == c2.ID {
		t.Fatalf("client ids collide: %d", c1.ID)
	}
	if !c1.Enabled {
		t.Fatalf("new client must be enabled by default")
	}
}

func TestAllocatorSkipsUsedAndReusesFreed(t *testing.T) {
	handle, _ := openTest(t, "amnezia.sqlite")
	if err := Migrate(handle); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := seedServerM4(t, handle, "10.8.0.1/24"); err != nil {
		t.Fatalf("CreateServer: %v", err)
	}
	born := make([]*ClientRecord, 3)
	for i := range born {
		priv, pub, err := keys.GenerateKeyPair()
		if err != nil {
			t.Fatalf("GenerateKeyPair: %v", err)
		}
		born[i], err = CreateClient(handle, "10.8.0.1/24", NewClient{Name: "c" + strconv.Itoa(i+1), PrivateKey: priv, PublicKey: pub})
		if err != nil {
			t.Fatalf("CreateClient #%d: %v", i+1, err)
		}
	}
	if born[2].Address != "10.8.0.4/32" {
		t.Fatalf("third address = %q, want 10.8.0.4/32", born[2].Address)
	}
	if err := DeleteClient(handle, born[1].ID); err != nil {
		t.Fatalf("DeleteClient: %v", err)
	}
	priv, pub, err := keys.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	c4, err := CreateClient(handle, "10.8.0.1/24", NewClient{Name: "d", PrivateKey: priv, PublicKey: pub})
	if err != nil {
		t.Fatalf("CreateClient after delete: %v", err)
	}
	if c4.Address != "10.8.0.3/32" {
		t.Fatalf("freed address not reused: got %q, want 10.8.0.3/32", c4.Address)
	}
}

func TestAllocatorDoesNotUseServerOrBroadcast(t *testing.T) {
	handle, _ := openTest(t, "amnezia.sqlite")
	if err := Migrate(handle); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	// server on .254 in a /24: .255 is the broadcast, so nothing is free
	addr, err := allocClientAddress("10.8.0.254/24", nil)
	if !errors.Is(err, ErrNoFreeAddress) {
		t.Fatalf("allocClientAddress(.254/24) = %q, %v; want ErrNoFreeAddress", addr, err)
	}
	// /30: .0 network, .1 server, .2 one client, .3 broadcast
	if err := seedServerM4(t, handle, "10.8.0.1/30"); err != nil {
		t.Fatalf("CreateServer: %v", err)
	}
	c, err := CreateClient(handle, "10.8.0.1/30", NewClient{Name: "x", PrivateKey: testPriv, PublicKey: testPub})
	if err != nil {
		t.Fatalf("CreateClient /30: %v", err)
	}
	if c.Address != "10.8.0.2/32" {
		t.Fatalf("/30 first client = %q, want 10.8.0.2/32", c.Address)
	}
	if _, err := CreateClient(handle, "10.8.0.1/30", NewClient{Name: "y", PrivateKey: testPriv, PublicKey: testPub2}); !errors.Is(err, ErrNoFreeAddress) {
		t.Fatalf("second /30 client error = %v, want ErrNoFreeAddress", err)
	}
}

func TestAllocatorSkipsUsedAcrossDeletedKeepsUniqueness(t *testing.T) {
	// two clients with the same address must be impossible: schema UNIQUE
	handle, _ := openTest(t, "amnezia.sqlite")
	if err := Migrate(handle); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := seedServerM4(t, handle, "10.8.0.1/24"); err != nil {
		t.Fatalf("CreateServer: %v", err)
	}
	if _, err := CreateClient(handle, "10.8.0.1/24", NewClient{Name: "a", PrivateKey: testPriv, PublicKey: testPub}); err != nil {
		t.Fatalf("CreateClient: %v", err)
	}
	// a hypothetical second insert with the same address violates UNIQUE
	if _, err := handle.Exec(
		`INSERT INTO clients (name, private_key, public_key, address, enabled, created_at, updated_at)
		 VALUES ('b', ?, ?, '10.8.0.2/32', 1, ?, ?)`,
		testPriv, testPub2, stamp(), stamp(),
	); err == nil {
		t.Fatal("duplicate client address accepted")
	}
}

func TestClientCRUDAndNotFound(t *testing.T) {
	handle, _ := openTest(t, "amnezia.sqlite")
	if err := Migrate(handle); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := seedServerM4(t, handle, "10.8.0.1/24"); err != nil {
		t.Fatalf("CreateServer: %v", err)
	}
	c, err := CreateClient(handle, "10.8.0.1/24", NewClient{Name: "alice", PrivateKey: testPriv, PublicKey: testPub, PresharedKey: testPub2})
	if err != nil {
		t.Fatalf("CreateClient: %v", err)
	}
	if c.PresharedKey != testPub2 {
		t.Fatalf("preshared key not stored")
	}

	got, err := ClientByID(handle, c.ID)
	if err != nil {
		t.Fatalf("ClientByID: %v", err)
	}
	if got.Name != "alice" || got.PrivateKey != testPriv || got.ExpiresAt != "" {
		t.Fatalf("ClientByID mismatch: %+v", got)
	}

	if err := UpdateClientName(handle, c.ID, "alice-2"); err != nil {
		t.Fatalf("UpdateClientName: %v", err)
	}
	if got, _ := ClientByID(handle, c.ID); got.Name != "alice-2" {
		t.Fatalf("name after rename = %q", got.Name)
	}

	if err := SetClientEnabled(handle, c.ID, false); err != nil {
		t.Fatalf("SetClientEnabled(false): %v", err)
	}
	if got, _ := ClientByID(handle, c.ID); got.Enabled {
		t.Fatal("client still enabled after disable")
	}
	if containsPeerAddr(handle, "10.8.0.2/32") {
		t.Fatal("disabled client still in config set")
	}
	if err := SetClientEnabled(handle, c.ID, true); err != nil {
		t.Fatalf("SetClientEnabled(true): %v", err)
	}
	if !containsPeerAddr(handle, "10.8.0.2/32") {
		t.Fatal("re-enabled client missing from config set")
	}

	all, err := ClientsAll(handle)
	if err != nil {
		t.Fatalf("ClientsAll: %v", err)
	}
	if len(all) != 1 || all[0].ID != c.ID {
		t.Fatalf("ClientsAll = %+v", all)
	}

	if err := DeleteClient(handle, c.ID); err != nil {
		t.Fatalf("DeleteClient: %v", err)
	}
	if _, err := ClientByID(handle, c.ID); !errors.Is(err, ErrClientNotFound) {
		t.Fatalf("ClientByID after delete = %v, want ErrClientNotFound", err)
	}
	// every mutation on an unknown id must be an error, never a no-op
	for name, fn := range map[string]func() error{
		"rename":     func() error { return UpdateClientName(handle, 999, "x") },
		"enable":     func() error { return SetClientEnabled(handle, 999, true) },
		"set-expiry": func() error { return SetClientExpiry(handle, 999, "2030-01-01T00:00:00Z") },
		"delete":     func() error { return DeleteClient(handle, 999) },
	} {
		if err := fn(); !errors.Is(err, ErrClientNotFound) {
			t.Errorf("%s(999) = %v, want ErrClientNotFound", name, err)
		}
	}
	if _, err := ClientByID(handle, 999); !errors.Is(err, ErrClientNotFound) {
		t.Fatalf("ClientByID(999) = %v, want ErrClientNotFound", err)
	}
}

func TestExpirySemantics(t *testing.T) {
	handle, _ := openTest(t, "amnezia.sqlite")
	if err := Migrate(handle); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := seedServerM4(t, handle, "10.8.0.1/24"); err != nil {
		t.Fatalf("CreateServer: %v", err)
	}
	mk := func(name string) *ClientRecord {
		t.Helper()
		priv, pub, err := keys.GenerateKeyPair()
		if err != nil {
			t.Fatalf("GenerateKeyPair: %v", err)
		}
		c, err := CreateClient(handle, "10.8.0.1/24", NewClient{Name: name, PrivateKey: priv, PublicKey: pub})
		if err != nil {
			t.Fatalf("CreateClient %s: %v", name, err)
		}
		return c
	}
	future := mk("future")
	past := mk("past")
	never := mk("never")
	disabled := mk("disabled")

	if err := SetClientExpiry(handle, future.ID, time.Now().UTC().Add(24*time.Hour).Format(time.RFC3339)); err != nil {
		t.Fatalf("SetClientExpiry(future): %v", err)
	}
	if err := SetClientExpiry(handle, past.ID, time.Now().UTC().Add(-24*time.Hour).Format(time.RFC3339)); err != nil {
		t.Fatalf("SetClientExpiry(past): %v", err)
	}
	if err := SetClientEnabled(handle, disabled.ID, false); err != nil {
		t.Fatalf("SetClientEnabled(disabled): %v", err)
	}

	if err := SetClientExpiry(handle, never.ID, ""); err != nil {
		t.Fatalf("SetClientExpiry(clear): %v", err)
	}
	if got, _ := ClientByID(handle, never.ID); got.ExpiresAt != "" {
		t.Fatalf("expires_at not cleared: %q", got.ExpiresAt)
	}

	active := containsPeerAddr(handle, "10.8.0.2/32")
	if !active {
		t.Fatal("future-expiry client must be active")
	}
	if containsPeerAddr(handle, "10.8.0.3/32") {
		t.Fatal("past-expiry client must be excluded from config set")
	}
	if !containsPeerAddr(handle, "10.8.0.4/32") {
		t.Fatal("no-expiry client must be active")
	}
	if containsPeerAddr(handle, "10.8.0.5/32") {
		t.Fatal("disabled client must be excluded from config set")
	}
	// enabled flag is never mutated automatically (M4 contract): the
	// expired client is still enabled=1 in the DB
	if got, _ := ClientByID(handle, past.ID); !got.Enabled {
		t.Fatal("expired client enabled flag changed automatically")
	}
	// the row survives; expiry is a display state, not a deletion
	gotPast, err := ClientByID(handle, past.ID)
	if err != nil {
		t.Fatalf("expired client row removed: %v", err)
	}
	if !gotPast.Expired(time.Now().UTC()) {
		t.Fatal("past expiry must report Expired()")
	}
	gotFuture, err := ClientByID(handle, future.ID)
	if err != nil {
		t.Fatalf("ClientByID(future): %v", err)
	}
	if gotFuture.Expired(time.Now().UTC()) {
		t.Fatal("future expiry must not report Expired()")
	}
	// malformed stored expiry is treated as expired (fail-closed)
	if _, err := handle.Exec(`UPDATE clients SET expires_at = 'garbage' WHERE id = ?`, future.ID); err != nil {
		t.Fatalf("UPDATE expires_at: %v", err)
	}
	if containsPeerAddr(handle, "10.8.0.2/32") {
		t.Fatal("client with malformed expiry must be excluded (fail-closed)")
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	handle, _ := openTest(t, "amnezia.sqlite")
	if err := Migrate(handle); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if _, ok, err := GetSetting(handle, "endpoint"); err != nil {
		t.Fatalf("GetSetting(missing): %v", err)
	} else if ok {
		t.Fatalf("missing key reported as present")
	}
	if err := SetSetting(handle, "endpoint", "vpn.example.com:51820"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if v, ok, err := GetSetting(handle, "endpoint"); err != nil || !ok || v != "vpn.example.com:51820" {
		t.Fatalf("endpoint = %q (ok=%v), err=%v", v, ok, err)
	}
	// upsert
	if err := SetSetting(handle, "endpoint", "vpn2.example.com:51820"); err != nil {
		t.Fatalf("SetSetting #2: %v", err)
	}
	if v, _, _ := GetSetting(handle, "endpoint"); v != "vpn2.example.com:51820" {
		t.Fatalf("endpoint after upsert = %q", v)
	}
}

// ---- M4.10: unique client names (schema v4) and server update ----

func TestCreateClientDuplicateNameRejected(t *testing.T) {
	handle, _ := openTest(t, "amnezia.sqlite")
	if err := Migrate(handle); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := seedServerM4(t, handle, "10.8.0.1/24"); err != nil {
		t.Fatalf("CreateServer: %v", err)
	}
	c1, err := CreateClient(handle, "10.8.0.1/24", NewClient{Name: "alice", PrivateKey: testPriv, PublicKey: testPub})
	if err != nil {
		t.Fatalf("CreateClient #1: %v", err)
	}
	if _, err := CreateClient(handle, "10.8.0.1/24", NewClient{Name: "alice", PrivateKey: testPriv, PublicKey: testPub2}); !errors.Is(err, ErrClientNameExists) {
		t.Fatalf("duplicate CreateClient error = %v, want ErrClientNameExists", err)
	}
	// the failed insert must not consume the next address
	if containsPeerAddr(handle, "10.8.0.3/32") {
		t.Fatalf("failed duplicate insert consumed address 10.8.0.3/32")
	}
	if c1.Address != "10.8.0.2/32" {
		t.Fatalf("first client address = %q, want 10.8.0.2/32", c1.Address)
	}
	// a distinct name still succeeds
	c2, err := CreateClient(handle, "10.8.0.1/24", NewClient{Name: "alice2", PrivateKey: testPriv, PublicKey: testPub2})
	if err != nil {
		t.Fatalf("CreateClient distinct name: %v", err)
	}
	if c2.Address != "10.8.0.3/32" {
		t.Fatalf("distinct-name client address = %q, want 10.8.0.3/32", c2.Address)
	}
}

func TestUpdateClientNameDuplicateRejected(t *testing.T) {
	handle, _ := openTest(t, "amnezia.sqlite")
	if err := Migrate(handle); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := seedServerM4(t, handle, "10.8.0.1/24"); err != nil {
		t.Fatalf("CreateServer: %v", err)
	}
	alice, err := CreateClient(handle, "10.8.0.1/24", NewClient{Name: "alice", PrivateKey: testPriv, PublicKey: testPub})
	if err != nil {
		t.Fatalf("CreateClient alice: %v", err)
	}
	if _, err := CreateClient(handle, "10.8.0.1/24", NewClient{Name: "bob", PrivateKey: testPriv, PublicKey: testPub2}); err != nil {
		t.Fatalf("CreateClient bob: %v", err)
	}
	if err := UpdateClientName(handle, alice.ID, "bob"); !errors.Is(err, ErrClientNameExists) {
		t.Fatalf("rename onto taken name error = %v, want ErrClientNameExists", err)
	}
	// renaming to the current name is a no-op, not an error
	if err := UpdateClientName(handle, alice.ID, "alice"); err != nil {
		t.Fatalf("self-rename: %v", err)
	}
	if err := UpdateClientName(handle, alice.ID, "carol"); err != nil {
		t.Fatalf("rename to fresh name: %v", err)
	}
	var name string
	if err := handle.QueryRow(`SELECT name FROM clients WHERE id = ?`, alice.ID).Scan(&name); err != nil {
		t.Fatalf("read back name: %v", err)
	}
	if name != "carol" {
		t.Fatalf("name after rename = %q, want carol", name)
	}
}

func TestMigrateDedupesLegacyNames(t *testing.T) {
	handle, _ := openTest(t, "amnezia.sqlite")
	// Simulate a v3 database: clients table without the unique name
	// index and duplicated names (Migrate adds the missing tables via
	// CREATE TABLE IF NOT EXISTS).
	if _, err := handle.Exec(`CREATE TABLE clients (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		private_key TEXT NOT NULL,
		public_key TEXT NOT NULL UNIQUE,
		preshared_key TEXT,
		address TEXT NOT NULL UNIQUE,
		enabled INTEGER NOT NULL DEFAULT 1,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		expires_at TEXT
	)`); err != nil {
		t.Fatalf("create legacy clients table: %v", err)
	}
	seed := []struct{ name, key, addr string }{
		{"alice", "legacy-key-1", "10.8.0.2/32"},
		{"alice", "legacy-key-2", "10.8.0.3/32"},
		{"bob", "legacy-key-3", "10.8.0.4/32"},
		{"bob", "legacy-key-4", "10.8.0.5/32"},
		{"carol", "legacy-key-5", "10.8.0.6/32"},
	}
	for _, s := range seed {
		if _, err := handle.Exec(
			`INSERT INTO clients (name, private_key, public_key, address, enabled, created_at, updated_at)
			 VALUES (?, 'k', ?, ?, 1, 't', 't')`,
			s.name, s.key, s.addr,
		); err != nil {
			t.Fatalf("seed client %s: %v", s.name, err)
		}
	}

	if err := Migrate(handle); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	want := []struct {
		id   int64
		name string
	}{
		{1, "alice"},
		{2, "alice-2"},
		{3, "bob"},
		{4, "bob-4"},
		{5, "carol"},
	}
	rows, err := handle.Query(`SELECT id, name FROM clients ORDER BY id`)
	if err != nil {
		t.Fatalf("query names: %v", err)
	}
	defer rows.Close()
	i := 0
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if i >= len(want) {
			t.Fatalf("unexpected extra client %q (id %d)", name, id)
		}
		if id != want[i].id || name != want[i].name {
			t.Errorf("row %d = (%d, %q), want (%d, %q)", i, id, name, want[i].id, want[i].name)
		}
		i++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate: %v", err)
	}
	if i != len(want) {
		t.Fatalf("got %d clients, want %d", i, len(want))
	}

	// the unique index exists and guards raw inserts (backstop)
	var idx string
	if err := handle.QueryRow(
		`SELECT name FROM sqlite_master WHERE type = 'index' AND name = 'idx_clients_name'`,
	).Scan(&idx); err != nil {
		t.Fatalf("unique name index missing: %v", err)
	}
	if _, err := handle.Exec(
		`INSERT INTO clients (name, private_key, public_key, address, enabled, created_at, updated_at)
		 VALUES ('alice', 'k', 'legacy-key-6', '10.8.0.7/32', 1, 't', 't')`,
	); err == nil {
		t.Fatal("unique index accepted a duplicate name")
	}
	if got, err := SchemaVersionStored(handle); err != nil || got != SchemaVersion {
		t.Fatalf("schema_version after migrate = %q (err %v), want %q", got, err, SchemaVersion)
	}
}

func TestUpdateServerFields(t *testing.T) {
	handle, _ := openTest(t, "amnezia.sqlite")
	if err := Migrate(handle); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := seedServerM4(t, handle, "10.8.0.1/24"); err != nil {
		t.Fatalf("CreateServer: %v", err)
	}
	dns, params, ep := "1.1.1.1,8.8.8.8", `{"junk": false}`, "vpn2.example.com:51821"
	if err := UpdateServer(handle, &dns, &params, &ep); err != nil {
		t.Fatalf("UpdateServer: %v", err)
	}
	s, err := ServerRow(handle)
	if err != nil {
		t.Fatalf("ServerRow: %v", err)
	}
	if s.DNS != "1.1.1.1,8.8.8.8" {
		t.Errorf("dns = %q, want 1.1.1.1,8.8.8.8", s.DNS)
	}
	if s.AWGParams != `{"junk": false}` {
		t.Errorf("awg_params = %q, want %q", s.AWGParams, `{"junk": false}`)
	}
	if v, ok, _ := GetSetting(handle, "endpoint"); !ok || v != "vpn2.example.com:51821" {
		t.Errorf("endpoint = %q (ok=%v), want vpn2.example.com:51821", v, ok)
	}
	// partial update: nil args leave the stored values untouched
	ep2 := "vpn3.example.com:51822"
	if err := UpdateServer(handle, nil, nil, &ep2); err != nil {
		t.Fatalf("UpdateServer partial: %v", err)
	}
	s, err = ServerRow(handle)
	if err != nil {
		t.Fatalf("ServerRow: %v", err)
	}
	if s.DNS != "1.1.1.1,8.8.8.8" {
		t.Errorf("dns after partial update = %q", s.DNS)
	}
	if s.AWGParams != `{"junk": false}` {
		t.Errorf("awg_params after partial update = %q", s.AWGParams)
	}
	if v, _, _ := GetSetting(handle, "endpoint"); v != "vpn3.example.com:51822" {
		t.Errorf("endpoint after partial update = %q", v)
	}
	// an explicit empty pointer clears the value
	empty := ""
	if err := UpdateServer(handle, nil, nil, &empty); err != nil {
		t.Fatalf("UpdateServer clear: %v", err)
	}
	if v, ok, _ := GetSetting(handle, "endpoint"); ok || v != "" {
		t.Errorf("endpoint after clear = %q (ok=%v), want cleared", v, ok)
	}
}

func TestUpdateServerMissingRow(t *testing.T) {
	handle, _ := openTest(t, "amnezia.sqlite")
	if err := Migrate(handle); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	dns := "1.1.1.1"
	if err := UpdateServer(handle, &dns, nil, nil); !errors.Is(err, ErrServerNotFound) {
		t.Fatalf("UpdateServer without server row error = %v, want ErrServerNotFound", err)
	}
}

// TestOpenFailsWhenParentIsAFile: database paths under a regular file
// (not a directory) fail fast with an error naming the create step,
// and no partial database file appears anywhere.
func TestOpenFailsWhenParentIsAFile(t *testing.T) {
	dir := t.TempDir()
	notDir := filepath.Join(dir, "occupied")
	if err := os.WriteFile(notDir, []byte("i am a file"), 0o600); err != nil {
		t.Fatal(err)
	}
	handle, err := Open(filepath.Join(notDir, "amnezia.sqlite"))
	if err == nil {
		handle.Close()
		t.Fatal("Open succeeded under a regular file parent")
	}
	if !strings.Contains(err.Error(), "create directory") {
		t.Errorf("error %q does not name the create-directory step", err)
	}
	if _, statErr := os.Stat(notDir); statErr != nil {
		t.Fatalf("the blocking file was disturbed: %v", statErr)
	}
}

// TestOpenFailsWhenDirUnwritable: an unwritable parent names the create
// step and leaves no database file behind (the tempdir is restored to
// mode 0700 in cleanup so t.TempDir's removal succeeds).
func TestOpenFailsWhenDirUnwritable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission checks are bypassed")
	}
	dir := t.TempDir()
	sub := filepath.Join(dir, "locked")
	if err := os.Mkdir(sub, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(sub, 0o700)
	handle, err := Open(filepath.Join(sub, "amnezia.sqlite"))
	if err == nil {
		handle.Close()
		t.Fatal("Open succeeded in an unwritable directory")
	}
	if !strings.Contains(err.Error(), "create") {
		t.Errorf("error %q does not name the create step", err)
	}
	if _, statErr := os.Stat(filepath.Join(sub, "amnezia.sqlite")); statErr == nil {
		t.Fatal("database file appeared despite the failure")
	}
}
