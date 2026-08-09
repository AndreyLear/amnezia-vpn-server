package db

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
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
