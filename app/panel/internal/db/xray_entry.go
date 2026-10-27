package db

import (
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// Узлы-входы (минимум amnezia-vpn-server-14y0.6, решение владельца
// 27.10.2026).
//
// Российский вход — отдельный сервер без панели и базы: принимает VLESS от
// клиентов внутри страны, российское пускает напрямую, остальное отправляет
// на этот сервер. Клиент подключается к нему с тем же UUID, что и сюда:
// вход получает список пользователей панели (`panel xray users-json`) и
// собирает свой Xray сам. Панели о входе нужно знать только то, из чего
// складывается ссылка: адрес, Reality входа, путь XHTTP и приставку к имени
// («RU »), по которой человек отличит ссылку через вход от прямой.
const xrayEntriesTable = `CREATE TABLE IF NOT EXISTS xray_entries (
		name TEXT PRIMARY KEY,
		link_host TEXT NOT NULL,
		link_port INTEGER NOT NULL DEFAULT 443,
		sni TEXT NOT NULL,
		public_key TEXT NOT NULL,
		short_id TEXT NOT NULL,
		path TEXT NOT NULL,
		link_prefix TEXT NOT NULL DEFAULT '',
		updated_at_utc TEXT NOT NULL
	);`

// XrayEntry — узел-вход. Секретов в нём нет: всё это и так видно в любой
// выданной через него ссылке.
type XrayEntry struct {
	Name       string
	LinkHost   string
	LinkPort   int64
	SNI        string
	PublicKey  string
	ShortID    string
	Path       string
	LinkPrefix string
}

// ErrXrayEntryNotFound — узла с таким именем нет.
var ErrXrayEntryNotFound = errors.New("db: xray entry not found")

var (
	entryNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	hostPattern      = regexp.MustCompile(`^[A-Za-z0-9.:-]{1,253}$`)
)

// ValidateXrayEntry — всё, без чего ссылка через узел не заработает.
func ValidateXrayEntry(e XrayEntry) error {
	switch {
	case !entryNamePattern.MatchString(e.Name):
		return fmt.Errorf("db: entry name must be 1–32 lowercase letters, digits or -")
	case !hostPattern.MatchString(e.LinkHost):
		return fmt.Errorf("db: entry %s: link host must be a host name or an IP address", e.Name)
	case e.LinkPort < 1 || e.LinkPort > 65535:
		return fmt.Errorf("db: entry %s: invalid link port %d", e.Name, e.LinkPort)
	case !hostPattern.MatchString(e.SNI) || strings.Contains(e.SNI, ":"):
		return fmt.Errorf("db: entry %s: sni must be a host name", e.Name)
	case !shortIDPattern.MatchString(e.ShortID):
		return fmt.Errorf("db: entry %s: short id must be 0–16 lowercase hex digits, even length", e.Name)
	case !xhttpPathPattern.MatchString(e.Path):
		return fmt.Errorf("db: entry %s: path must start with / and contain only URL-safe characters", e.Name)
	case len([]rune(e.LinkPrefix)) > 16 || strings.IndexFunc(e.LinkPrefix, unicode.IsControl) >= 0:
		return fmt.Errorf("db: entry %s: link prefix must be at most 16 printable characters", e.Name)
	}
	if raw, err := base64.RawURLEncoding.DecodeString(e.PublicKey); err != nil || len(raw) != 32 {
		return fmt.Errorf("db: entry %s: public key is not 32 bytes of base64 raw url", e.Name)
	}
	return nil
}

// SaveXrayEntry создаёт или заменяет узел по имени.
func SaveXrayEntry(handle *sql.DB, e XrayEntry) error {
	if err := ValidateXrayEntry(e); err != nil {
		return err
	}
	if _, err := handle.Exec(
		`INSERT INTO xray_entries (name, link_host, link_port, sni, public_key, short_id, path, link_prefix, updated_at_utc)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (name) DO UPDATE SET
			link_host = excluded.link_host, link_port = excluded.link_port, sni = excluded.sni,
			public_key = excluded.public_key, short_id = excluded.short_id, path = excluded.path,
			link_prefix = excluded.link_prefix, updated_at_utc = excluded.updated_at_utc`,
		e.Name, e.LinkHost, e.LinkPort, e.SNI, e.PublicKey, e.ShortID, e.Path, e.LinkPrefix,
		time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		return fmt.Errorf("db: write xray entry: %w", err)
	}
	return nil
}

// XrayEntries — все узлы по имени.
func XrayEntries(handle Querier) ([]XrayEntry, error) {
	rows, err := handle.Query(
		`SELECT name, link_host, link_port, sni, public_key, short_id, path, link_prefix
		   FROM xray_entries ORDER BY name`,
	)
	if err != nil {
		return nil, fmt.Errorf("db: query xray entries: %w", err)
	}
	defer rows.Close()
	var out []XrayEntry
	for rows.Next() {
		var e XrayEntry
		if err := rows.Scan(&e.Name, &e.LinkHost, &e.LinkPort, &e.SNI, &e.PublicKey, &e.ShortID, &e.Path, &e.LinkPrefix); err != nil {
			return nil, fmt.Errorf("db: scan xray entry: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// XrayEntryByName — узел по имени; ErrXrayEntryNotFound, если его нет.
func XrayEntryByName(handle Querier, name string) (*XrayEntry, error) {
	var e XrayEntry
	err := handle.QueryRow(
		`SELECT name, link_host, link_port, sni, public_key, short_id, path, link_prefix
		   FROM xray_entries WHERE name = ?`, name,
	).Scan(&e.Name, &e.LinkHost, &e.LinkPort, &e.SNI, &e.PublicKey, &e.ShortID, &e.Path, &e.LinkPrefix)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrXrayEntryNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("db: read xray entry: %w", err)
	}
	return &e, nil
}

// DeleteXrayEntry удаляет узел; ErrXrayEntryNotFound, если его нет.
func DeleteXrayEntry(handle *sql.DB, name string) error {
	res, err := handle.Exec(`DELETE FROM xray_entries WHERE name = ?`, name)
	if err != nil {
		return fmt.Errorf("db: delete xray entry: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("db: delete xray entry: %w", err)
	} else if n == 0 {
		return ErrXrayEntryNotFound
	}
	return nil
}

// VLESSUserExport — строка выгрузки для узла-входа: users.json того вида,
// что читает build.py на 62.109.1.44.
type VLESSUserExport struct {
	ClientID int64  `json:"client_id"`
	Name     string `json:"name"`
	UUID     string `json:"uuid"`
	Enabled  bool   `json:"enabled"`
}

// VLESSUsersExport — все клиенты с UUID. Enabled — то же правило, что для
// конфигов: включён и не истёк; узел пускает только таких.
func VLESSUsersExport(handle Querier) ([]VLESSUserExport, error) {
	rows, err := handle.Query(
		`SELECT id, name, vless_uuid, enabled, expires_at FROM clients
		  WHERE vless_uuid != '' ORDER BY id`,
	)
	if err != nil {
		return nil, fmt.Errorf("db: query vless users: %w", err)
	}
	defer rows.Close()
	now := time.Now().UTC()
	out := []VLESSUserExport{}
	for rows.Next() {
		var (
			u       VLESSUserExport
			enabled int
			exp     sql.NullString
		)
		if err := rows.Scan(&u.ClientID, &u.Name, &u.UUID, &enabled, &exp); err != nil {
			return nil, fmt.Errorf("db: scan vless user: %w", err)
		}
		u.Enabled = enabled == 1 && !clientExpired(exp.String, now)
		out = append(out, u)
	}
	return out, rows.Err()
}
