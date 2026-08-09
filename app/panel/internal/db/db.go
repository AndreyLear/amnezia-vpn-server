// Package db implements SQLite storage: schema from
// docs/TECHNICAL_SPEC_v2.0.md §3 and idempotent migrations (§4).
package db

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// SchemaVersion matches manifest.schema_version in §5.
const SchemaVersion = "3"

var schemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS server (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		private_key TEXT NOT NULL,
		public_key TEXT NOT NULL,
		address TEXT NOT NULL,
		listen_port INTEGER NOT NULL,
		dns TEXT,
		awg_params TEXT NOT NULL DEFAULT '{}',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	);`,
	`CREATE TABLE IF NOT EXISTS clients (
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
	);`,
	`CREATE TABLE IF NOT EXISTS settings (
		key TEXT PRIMARY KEY,
		value TEXT
	);`,
	`CREATE TABLE IF NOT EXISTS auth (
		id INTEGER PRIMARY KEY,
		username TEXT NOT NULL UNIQUE,
		password_hash TEXT NOT NULL,
		totp_secret TEXT
	);`,
	`CREATE TABLE IF NOT EXISTS schema_meta (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);`,
}

// Open creates a new SQLite database file (including parent directories)
// and returns a handle. It does not touch the schema.
func Open(path string) (*sql.DB, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("db: create directory %s: %w", dir, err)
		}
	}
	handle, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("db: open %s: %w", path, err)
	}
	handle.SetMaxOpenConns(1)
	if _, err := handle.Exec(`PRAGMA busy_timeout = 5000;`); err != nil {
		handle.Close()
		return nil, fmt.Errorf("db: configure %s: %w", path, err)
	}
	return handle, nil
}

// Migrate applies the schema from §3 idempotently and records the current
// schema version in schema_meta (§5). Existing data is never modified.
func Migrate(handle *sql.DB) error {
	for _, stmt := range schemaStatements {
		if _, err := handle.Exec(stmt); err != nil {
			return fmt.Errorf("db: migrate: %w", err)
		}
	}
	if _, err := handle.Exec(
		`INSERT INTO schema_meta (key, value) VALUES ('schema_version', ?)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
		SchemaVersion,
	); err != nil {
		return fmt.Errorf("db: record schema_version: %w", err)
	}
	return nil
}

// ErrSchemaVersionMismatch reports a stored schema version different from
// the one this binary implements. Not expected during M2 migrations.
var ErrSchemaVersionMismatch = errors.New("db: schema_version mismatch")

// SchemaVersionStored returns the schema_version recorded in the database.
func SchemaVersionStored(handle *sql.DB) (string, error) {
	var v string
	err := handle.QueryRow(
		`SELECT value FROM schema_meta WHERE key = 'schema_version'`,
	).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("db: read schema_version: %w", err)
	}
	return v, nil
}

// DefaultPath returns the default SQLite location (docs/TECHNICAL_SPEC §2)
// unless overridden via the AMNEZIA_DB_PATH environment variable.
func DefaultPath() string {
	if p := os.Getenv("AMNEZIA_DB_PATH"); p != "" {
		return p
	}
	return "/data/amnezia.sqlite"
}
