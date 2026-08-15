// Package db implements SQLite storage: schema from
// docs/TECHNICAL_SPEC_v2.0.md §3 and idempotent migrations (§4).
package db

import (
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/keys"
	sqlite "modernc.org/sqlite"
)

// mapNameConstraint maps a UNIQUE-constraint failure on the clients.name
// index to ErrClientNameExists (T-114). The web process serializes
// mutations with a mutex, but the CLI is a separate process: the
// SELECT COUNT(name) pre-check races with a concurrent INSERT/UPDATE
// and the unique index answers a raw constraint error (SQLite
// 1555/2067/19) that the pre-check path never returns.
func mapNameConstraint(err error) error {
	if err == nil {
		return nil
	}
	var se *sqlite.Error
	if errors.As(err, &se) {
		switch se.Code() {
		case 1555, 2067, 19:
			if strings.Contains(err.Error(), "clients.name") {
				return ErrClientNameExists
			}
		}
	}
	return err
}

// SchemaVersion matches manifest.schema_version in §5. Bumped to 4 when
// client names became unique (M10.1): v4 adds the dedup migration plus
// the unique name index. Bumped to 6 when clients.description was
// added. Archives written by older releases are still accepted by
// restore and migrated here at apply time (T-110 backward
// compatibility).
const SchemaVersion = "6"

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
// and returns a handle. It does not touch the schema. The database file
// holds private keys: it is created/opened with mode 0600 (M4 contract,
// docs/TECHNICAL_SPEC_v2.0.md §6). Parent directory permissions are not
// touched.
func Open(path string) (*sql.DB, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("db: create directory %s: %w", dir, err)
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("db: create %s: %w", path, err)
	}
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return nil, fmt.Errorf("db: chmod %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("db: close %s: %w", path, err)
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
	// v4 dedup gate (T-116): the dedup loop plus index creation is a
	// no-op once the unique index exists, but it pays a full clients
	// scan on every panel start and on every CLI invocation (each one
	// opens and migrates the database). Run it only when the stored
	// schema version predates v4.
	stored, err := SchemaVersionStored(handle)
	if err != nil {
		return err
	}
	if stored != SchemaVersion {
		if err := migrateClientNameUniqueness(handle); err != nil {
			return err
		}
	}
	if err := migrateTOTPMode(handle); err != nil {
		return err
	}
	if err := migrateClientDescription(handle); err != nil {
		return err
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

func migrateTOTPMode(handle *sql.DB) error {
	rows, err := handle.Query(`PRAGMA table_info(auth)`)
	if err != nil {
		return fmt.Errorf("db: inspect auth schema: %w", err)
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return fmt.Errorf("db: inspect auth schema: %w", err)
		}
		if name == "totp_mode" {
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("db: inspect auth schema: %w", err)
	}
	if !found {
		if _, err := handle.Exec(`ALTER TABLE auth ADD COLUMN totp_mode TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("db: add auth totp mode: %w", err)
		}
	}
	return nil
}

func migrateClientDescription(handle *sql.DB) error {
	rows, err := handle.Query(`PRAGMA table_info(clients)`)
	if err != nil {
		return fmt.Errorf("db: inspect clients schema: %w", err)
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return fmt.Errorf("db: inspect clients schema: %w", err)
		}
		if name == "description" {
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("db: inspect clients schema: %w", err)
	}
	if !found {
		if _, err := handle.Exec(`ALTER TABLE clients ADD COLUMN description TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("db: add clients description: %w", err)
		}
	}
	return nil
}

// migrateClientNameUniqueness (schema v4) makes clients.name unique.
// Pre-existing duplicates are renamed in place to "<name>-<id>" (the
// lowest id keeps the bare name), then a unique index guards both
// inserts and renames. SQLite has no UPDATE ... LIMIT, so the dedup
// loop runs until one pass removes no duplicate (each pass renames at
// most one row per duplicate group). The migration is idempotent: once
// the index exists, every name is unique and the loop exits on the
// first pass.
func migrateClientNameUniqueness(handle *sql.DB) error {
	for {
		res, err := handle.Exec(
			`UPDATE clients SET name = name || '-' || id
			 WHERE id NOT IN (SELECT MIN(id) FROM clients GROUP BY name)`,
		)
		if err != nil {
			return fmt.Errorf("db: migrate client name uniqueness (dedup): %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("db: migrate client name uniqueness (rows affected): %w", err)
		}
		if n == 0 {
			break
		}
	}
	if _, err := handle.Exec(
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_clients_name ON clients(name)`,
	); err != nil {
		return fmt.Errorf("db: migrate client name uniqueness (index): %w", err)
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

// ErrServerNotFound reports that no server row (id = 1) exists.
var ErrServerNotFound = errors.New("db: server row not found")

// ServerRecord is the server configuration record (§3, table server).
type ServerRecord struct {
	PrivateKey string
	PublicKey  string
	Address    string
	ListenPort int64
	DNS        string
	AWGParams  string
}

// ServerRow loads the single server record (id = 1). A missing row is
// reported as ErrServerNotFound.
func ServerRow(handle *sql.DB) (*ServerRecord, error) {
	row := handle.QueryRow(
		`SELECT private_key, public_key, address, listen_port, dns, awg_params
		   FROM server WHERE id = 1`,
	)
	var (
		s   ServerRecord
		dns sql.NullString
	)
	err := row.Scan(&s.PrivateKey, &s.PublicKey, &s.Address, &s.ListenPort, &dns, &s.AWGParams)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrServerNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("db: read server row: %w", err)
	}
	s.DNS = dns.String
	return &s, nil
}

// ClientRow is a WireGuard client record relevant to awg0.conf generation
// (§3, table clients; only public parts; expires_at is not secret).
type ClientRow struct {
	ID        int64
	PublicKey string
	// PresharedKey is empty when clients.preshared_key is NULL.
	PresharedKey string
	Address      string
	// ExpiresAt is empty when clients.expires_at is NULL (no expiry).
	ExpiresAt string
}

// ClientsForConfig returns the clients active for the server AWG config,
// ordered by id: enabled = 1 AND not expired (M4 contract: expires_at
// NULL or in the future). Expiry is evaluated in Go so any RFC3339
// offset is handled; a malformed stored value is treated as expired
// (fail-closed).
func ClientsForConfig(handle *sql.DB) ([]ClientRow, error) {
	rows, err := handle.Query(
		`SELECT id, public_key, preshared_key, address, expires_at
		   FROM clients WHERE enabled = 1 ORDER BY id`,
	)
	if err != nil {
		return nil, fmt.Errorf("db: query clients: %w", err)
	}
	defer rows.Close()

	now := time.Now().UTC()
	var clients []ClientRow
	for rows.Next() {
		var (
			c   ClientRow
			psk sql.NullString
			exp sql.NullString
		)
		if err := rows.Scan(&c.ID, &c.PublicKey, &psk, &c.Address, &exp); err != nil {
			return nil, fmt.Errorf("db: scan client: %w", err)
		}
		c.PresharedKey = psk.String
		c.ExpiresAt = exp.String
		if c.Expired(now) {
			continue
		}
		clients = append(clients, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate clients: %w", err)
	}
	return clients, nil
}

// Expired reports whether the record is past expires_at. A client without
// expires_at never expires; a malformed value is treated as expired
// (fail-closed).
func (c ClientRow) Expired(now time.Time) bool {
	return clientExpired(c.ExpiresAt, now)
}

// stamp returns the current UTC time in the RFC3339 format used for
// created_at/updated_at/expires_at.
func stamp() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// ErrServerExists reports that a server row (id = 1) already exists,
// so bootstrap must not overwrite it (M4 contract).
var ErrServerExists = errors.New("db: server row (id=1) already exists")

// ErrClientNotFound reports that no client row with the given id exists.
// Unknown ids are errors, never silent no-ops (M4 contract).
var ErrClientNotFound = errors.New("db: client not found")

// ErrNoFreeAddress reports that the server network has no free host
// address left for a new client (M4 contract, IPv4 only).
var ErrNoFreeAddress = errors.New("db: no free client address in the server network")

// ErrClientNameExists reports that a client with the same name already
// exists. Client names are unique (M4.10): duplicate names previously
// produced indistinguishable `client show` rows and config fragments.
var ErrClientNameExists = errors.New("db: a client with this name already exists")

// CreateServer bootstraps the single server row (id = 1) with the given
// X25519 key pair and address, and stores the client-config endpoint
// under the settings key "endpoint". Both the row and the setting are
// written in one transaction. It fails with ErrServerExists when the row
// is already present. address must be an IPv4 CIDR (M4 contract: the
// client address allocator is IPv4-only); listenPort must fit uint16.
func CreateServer(handle *sql.DB, privateKey, publicKey, address string, listenPort int64, dns, awgParams, endpoint string) error {
	if !keys.ValidKey(privateKey) {
		return fmt.Errorf("db: invalid server private key: not a 32-byte base64 key")
	}
	if !keys.ValidKey(publicKey) {
		return fmt.Errorf("db: invalid server public key: not a 32-byte base64 key")
	}
	ip, _, err := net.ParseCIDR(address)
	if err != nil {
		return fmt.Errorf("db: invalid server address %q: not a CIDR network", address)
	}
	if ip.To4() == nil {
		return fmt.Errorf("db: server address %q is not IPv4 (IPv6 is not supported in M4)", address)
	}
	if listenPort < 0 || listenPort > 65535 {
		return fmt.Errorf("db: invalid listen port %d: must be an unsigned 16-bit value", listenPort)
	}

	tx, err := handle.Begin()
	if err != nil {
		return fmt.Errorf("db: begin server bootstrap: %w", err)
	}
	defer tx.Rollback()

	var exists int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM server WHERE id = 1`).Scan(&exists); err != nil {
		return fmt.Errorf("db: check server row: %w", err)
	}
	if exists > 0 {
		return ErrServerExists
	}

	now := stamp()
	if _, err := tx.Exec(
		`INSERT INTO server (id, private_key, public_key, address, listen_port, dns, awg_params, created_at, updated_at)
		 VALUES (1, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, ?)`,
		privateKey, publicKey, address, listenPort, dns, awgParams, now, now,
	); err != nil {
		return fmt.Errorf("db: insert server row: %w", err)
	}
	if endpoint != "" {
		if err := setSettingTx(tx, "endpoint", endpoint); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("db: commit server bootstrap: %w", err)
	}
	return nil
}

// GetSetting returns the value of a settings key. ok is false when the
// key is absent.
func GetSetting(handle *sql.DB, key string) (value string, ok bool, err error) {
	var v string
	err = handle.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("db: read setting %q: %w", key, err)
	}
	return v, true, nil
}

// SetSetting upserts a settings key.
func SetSetting(handle *sql.DB, key, value string) error {
	tx, err := handle.Begin()
	if err != nil {
		return fmt.Errorf("db: begin setting write: %w", err)
	}
	defer tx.Rollback()
	if err := setSettingTx(tx, key, value); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("db: commit setting write: %w", err)
	}
	return nil
}

func setSettingTx(tx *sql.Tx, key, value string) error {
	if _, err := tx.Exec(
		`INSERT INTO settings (key, value) VALUES (?, ?)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
		key, value,
	); err != nil {
		return fmt.Errorf("db: write setting %q: %w", key, err)
	}
	return nil
}

// UpdateServer applies the given settings to the single server row
// (id = 1). Every argument is optional: a nil pointer leaves the stored
// value untouched; a non-nil pointer is written verbatim, so an empty
// string explicitly clears the value. dns and awgParams are written to
// the row, endpoint to the settings table. It fails with
// ErrServerNotFound when the row is missing. The callers validate
// awgParams/endpoint before calling.
func UpdateServer(handle *sql.DB, dns, awgParams, endpoint *string) error {
	tx, err := handle.Begin()
	if err != nil {
		return fmt.Errorf("db: begin server update: %w", err)
	}
	defer tx.Rollback()

	var exists int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM server WHERE id = 1`).Scan(&exists); err != nil {
		return fmt.Errorf("db: check server row: %w", err)
	}
	if exists == 0 {
		return ErrServerNotFound
	}

	if dns != nil || awgParams != nil {
		updates := []string{"updated_at = ?"}
		args := []any{stamp()}
		if dns != nil {
			updates = append(updates, "dns = ?")
			args = append(args, *dns)
		}
		if awgParams != nil {
			updates = append(updates, "awg_params = ?")
			args = append(args, *awgParams)
		}
		args = append(args, int64(1))
		if _, err := tx.Exec(
			`UPDATE server SET `+strings.Join(updates, ", ")+` WHERE id = ?`,
			args...,
		); err != nil {
			return fmt.Errorf("db: update server row: %w", err)
		}
	}
	if endpoint != nil {
		if *endpoint == "" {
			if _, err := tx.Exec(`DELETE FROM settings WHERE key = 'endpoint'`); err != nil {
				return fmt.Errorf("db: clear endpoint setting: %w", err)
			}
		} else {
			if err := setSettingTx(tx, "endpoint", *endpoint); err != nil {
				return err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("db: commit server update: %w", err)
	}
	return nil
}

// ClientRecord is the full client row (§3, table clients), including the
// private key and preshared key, which are never printed by list/show or
// logs.
type ClientRecord struct {
	ID           int64
	Name         string
	PrivateKey   string
	PublicKey    string
	PresharedKey string // empty when clients.preshared_key is NULL
	Address      string
	Enabled      bool
	CreatedAt    string
	UpdatedAt    string
	ExpiresAt    string // empty when clients.expires_at is NULL
	Description  string
}

// Expired reports whether the record is past expires_at (see ClientRow.
// Expired).
func (c ClientRecord) Expired(now time.Time) bool {
	return clientExpired(c.ExpiresAt, now)
}

func clientExpired(expiresAt string, now time.Time) bool {
	if expiresAt == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return true // fail-closed: malformed stored expiry is treated as expired
	}
	return !t.After(now)
}

// NewClient carries the key material and identity of a client to be
// created. Keys are generated by internal/keys before the call; this
// package never generates or re-derives them (MVP §11: public keys are
// stored explicitly).
type NewClient struct {
	Name         string
	PrivateKey   string
	PublicKey    string
	PresharedKey string // optional
	Description  string // optional; empty string allowed; never written to awg0.conf
}

// CreateClient allocates the first free /32 host address in the server
// network (server address + 1, skipping used addresses and the network/
// broadcast boundaries) and inserts the client within one transaction.
// It fails with ErrNoFreeAddress when the network is exhausted and with
// ErrClientNameExists when the name is already taken (unique client
// names, schema v4). It returns the created record on success.
func CreateClient(handle *sql.DB, serverAddress string, nc NewClient) (*ClientRecord, error) {
	if !keys.ValidKey(nc.PrivateKey) {
		return nil, fmt.Errorf("db: invalid client private key: not a 32-byte base64 key")
	}
	if !keys.ValidKey(nc.PublicKey) {
		return nil, fmt.Errorf("db: invalid client public key: not a 32-byte base64 key")
	}
	if nc.PresharedKey != "" && !keys.ValidKey(nc.PresharedKey) {
		return nil, fmt.Errorf("db: invalid preshared key: not a 32-byte base64 key")
	}
	tx, err := handle.Begin()
	if err != nil {
		return nil, fmt.Errorf("db: begin client create: %w", err)
	}
	defer tx.Rollback()

	var taken int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM clients WHERE name = ?`, nc.Name).Scan(&taken); err != nil {
		return nil, fmt.Errorf("db: check client name: %w", err)
	}
	if taken > 0 {
		return nil, ErrClientNameExists
	}

	rows, err := tx.Query(`SELECT address FROM clients`)
	if err != nil {
		return nil, fmt.Errorf("db: query used addresses: %w", err)
	}
	var used []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			rows.Close()
			return nil, fmt.Errorf("db: scan used address: %w", err)
		}
		used = append(used, a)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("db: iterate used addresses: %w", err)
	}
	rows.Close()

	address, err := allocClientAddress(serverAddress, used)
	if err != nil {
		return nil, err
	}

	now := stamp()
	res, err := tx.Exec(
		`INSERT INTO clients (name, private_key, public_key, preshared_key, address, enabled, created_at, updated_at, description)
		 VALUES (?, ?, ?, NULLIF(?, ''), ?, 1, ?, ?, ?)`,
		nc.Name, nc.PrivateKey, nc.PublicKey, nc.PresharedKey, address, now, now, nc.Description,
	)
	if err != nil {
		if mapped := mapNameConstraint(err); errors.Is(mapped, ErrClientNameExists) {
			return nil, ErrClientNameExists
		}
		return nil, fmt.Errorf("db: insert client: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("db: last insert id: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("db: commit client create: %w", err)
	}
	return &ClientRecord{
		ID:           id,
		Name:         nc.Name,
		PrivateKey:   nc.PrivateKey,
		PublicKey:    nc.PublicKey,
		PresharedKey: nc.PresharedKey,
		Address:      address,
		Enabled:      true,
		CreatedAt:    now,
		UpdatedAt:    now,
		Description:  nc.Description,
	}, nil
}

// allocClientAddress picks the first free host in the server CIDR network
// starting at server address + 1. Already assigned client addresses are
// skipped; the network address and the broadcast address are never
// handed out. The result carries the /32 suffix.
func allocClientAddress(serverCIDR string, used []string) (string, error) {
	ip, ipnet, err := net.ParseCIDR(serverCIDR)
	if err != nil {
		return "", fmt.Errorf("db: server address %q: not a CIDR network", serverCIDR)
	}
	ip4 := ip.To4()
	if ip4 == nil {
		return "", fmt.Errorf("db: server address %q is not IPv4 (IPv6 is not supported in M4)", serverCIDR)
	}
	if len(ipnet.Mask) != net.IPv4len {
		return "", fmt.Errorf("db: server address %q has a non-IPv4 mask", serverCIDR)
	}

	mask := binary.BigEndian.Uint32(ipnet.Mask)
	start := binary.BigEndian.Uint32(ip4)
	networkStart := start & mask
	broadcast := networkStart | ^mask

	usedSet := make(map[uint32]bool, len(used))
	for _, addr := range used {
		uip, _, err := net.ParseCIDR(addr)
		if err != nil {
			continue
		}
		u4 := uip.To4()
		if u4 != nil {
			usedSet[binary.BigEndian.Uint32(u4)] = true
		}
	}

	for cand := start + 1; cand < broadcast; cand++ {
		if usedSet[cand] {
			continue
		}
		return fmt.Sprintf("%d.%d.%d.%d/32", byte(cand>>24), byte(cand>>16), byte(cand>>8), byte(cand)), nil
	}
	return "", ErrNoFreeAddress
}

// ClientByID loads the full client record. A missing row is reported as
// ErrClientNotFound.
func ClientByID(handle *sql.DB, id int64) (*ClientRecord, error) {
	row := handle.QueryRow(
		`SELECT id, name, private_key, public_key, preshared_key, address, enabled, created_at, updated_at, expires_at, description
		   FROM clients WHERE id = ?`, id,
	)
	c, err := scanClient(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrClientNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("db: read client %d: %w", id, err)
	}
	return c, nil
}

// ClientsAll loads all client records, ordered by id.
func ClientsAll(handle *sql.DB) ([]ClientRecord, error) {
	rows, err := handle.Query(
		`SELECT id, name, private_key, public_key, preshared_key, address, enabled, created_at, updated_at, expires_at, description
		   FROM clients ORDER BY id`,
	)
	if err != nil {
		return nil, fmt.Errorf("db: query clients: %w", err)
	}
	defer rows.Close()

	var clients []ClientRecord
	for rows.Next() {
		c, err := scanClient(rows)
		if err != nil {
			return nil, fmt.Errorf("db: scan client: %w", err)
		}
		clients = append(clients, *c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate clients: %w", err)
	}
	return clients, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanClient(row rowScanner) (*ClientRecord, error) {
	var (
		c         ClientRecord
		psk       sql.NullString
		enabled   int
		expiresAt sql.NullString
	)
	if err := row.Scan(&c.ID, &c.Name, &c.PrivateKey, &c.PublicKey, &psk,
		&c.Address, &enabled, &c.CreatedAt, &c.UpdatedAt, &expiresAt, &c.Description); err != nil {
		return nil, err
	}
	c.PresharedKey = psk.String
	c.Enabled = enabled == 1
	c.ExpiresAt = expiresAt.String
	return &c, nil
}

// UpdateClientName renames a client. ErrClientNotFound when the id is
// unknown; ErrClientNameExists when the new name is already taken.
// The id check runs first: renaming a missing id onto a taken name
// answers ErrClientNotFound, not ErrClientNameExists (T-114).
func UpdateClientName(handle *sql.DB, id int64, name string) error {
	var exists int
	if err := handle.QueryRow(`SELECT COUNT(*) FROM clients WHERE id = ?`, id).Scan(&exists); err != nil {
		return fmt.Errorf("db: check client id: %w", err)
	}
	if exists == 0 {
		return ErrClientNotFound
	}
	var taken int
	if err := handle.QueryRow(`SELECT COUNT(*) FROM clients WHERE name = ? AND id != ?`, name, id).Scan(&taken); err != nil {
		return fmt.Errorf("db: check client name: %w", err)
	}
	if taken > 0 {
		return ErrClientNameExists
	}
	err := mutateClient(handle, id,
		`UPDATE clients SET name = ?, updated_at = ? WHERE id = ?`,
		name, stamp(), id)
	if err != nil {
		if mapped := mapNameConstraint(err); errors.Is(mapped, ErrClientNameExists) {
			return ErrClientNameExists
		}
		return err
	}
	return nil
}

// UpdateClientDescription sets the free-text description (empty allowed).
// ErrClientNotFound when the id is unknown. The value is never written
// to awg0.conf.
func UpdateClientDescription(handle *sql.DB, id int64, description string) error {
	return mutateClient(handle, id,
		`UPDATE clients SET description = ?, updated_at = ? WHERE id = ?`,
		description, stamp(), id)
}

// SetClientEnabled sets the enabled flag. ErrClientNotFound when the id
// is unknown. expired_at is never touched (M4 contract).
func SetClientEnabled(handle *sql.DB, id int64, enabled bool) error {
	v := 0
	if enabled {
		v = 1
	}
	return mutateClient(handle, id,
		`UPDATE clients SET enabled = ?, updated_at = ? WHERE id = ?`,
		v, stamp(), id)
}

// SetClientExpiry sets or clears expires_at (expiresAt == "" clears it).
// ErrClientNotFound when the id is unknown.
func SetClientExpiry(handle *sql.DB, id int64, expiresAt string) error {
	if expiresAt == "" {
		return mutateClient(handle, id,
			`UPDATE clients SET expires_at = NULL, updated_at = ? WHERE id = ?`,
			stamp(), id)
	}
	return mutateClient(handle, id,
		`UPDATE clients SET expires_at = ?, updated_at = ? WHERE id = ?`,
		expiresAt, stamp(), id)
}

// DeleteClient removes a client row. ErrClientNotFound when the id is
// unknown; delete is never a silent no-op (M4 contract).
func DeleteClient(handle *sql.DB, id int64) error {
	return mutateClient(handle, id,
		`DELETE FROM clients WHERE id = ?`,
		id)
}

func mutateClient(handle *sql.DB, id int64, query string, args ...any) error {
	res, err := handle.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("db: mutate client %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("db: rows affected: %w", err)
	}
	if n == 0 {
		return ErrClientNotFound
	}
	return nil
}
