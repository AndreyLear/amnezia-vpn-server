package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Mail settings for the operator's notifications (amnezia-vpn-server-2kr4,
// first step of dfs2).
//
// The database is the only truth about them. The host service that
// actually talks to SMTP cannot read SQLite — sqlite3 is not installed on
// the host — so the panel renders a derived file from this row
// (internal/mailconf), the same way awg0.conf is rendered from the server
// and clients tables.
//
// One row, id = 1: there is exactly one mailbox the server writes from.
//
// The password is somebody else's secret. It opens the operator's mailbox,
// not this server, so it must not travel with a backup: the snapshot is
// cleared of it before it is archived (internal/backup), and a restored
// database carries every mail setting except the password. PasswordMissing
// is how the panel tells that state apart from «mail was never set up».
const mailSettingsTable = `CREATE TABLE IF NOT EXISTS mail_settings (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		host TEXT NOT NULL,
		port INTEGER NOT NULL,
		username TEXT NOT NULL,
		password TEXT NOT NULL DEFAULT '',
		recipient TEXT NOT NULL,
		updated_at_utc TEXT NOT NULL
	);`

// MailDefaultPort is the submission port with STARTTLS. 465 (implicit TLS)
// is supported too; the sender picks the encryption by the port number, so
// there is no separate switch to store.
const MailDefaultPort = 587

// ErrMailNotConfigured reports that mail was never set up.
var ErrMailNotConfigured = errors.New("db: mail settings not configured")

// ErrMailInvalid reports settings that cannot be stored. The message never
// includes the offending value: the caller may be an HTTP handler, and the
// password must not reach a response or a log.
var ErrMailInvalid = errors.New("db: mail settings invalid")

// MailSettings is the stored mail configuration. The sender address is the
// login; there is no separate field for it.
type MailSettings struct {
	Host      string
	Port      int
	Username  string
	Password  string
	Recipient string
	UpdatedAt time.Time
}

// PasswordMissing is true when the settings exist but carry no password —
// the state right after a restore from a backup.
func (m MailSettings) PasswordMissing() bool {
	return m.Password == ""
}

// Validate rejects settings the sender could not use. Line breaks are
// refused in every field that ends up in an SMTP command or a header: a
// value with CR or LF could smuggle a second command or header in.
func (m MailSettings) Validate() error {
	if strings.TrimSpace(m.Host) == "" {
		return fmt.Errorf("%w: host is empty", ErrMailInvalid)
	}
	if m.Port < 1 || m.Port > 65535 {
		return fmt.Errorf("%w: port out of range", ErrMailInvalid)
	}
	if strings.TrimSpace(m.Username) == "" {
		return fmt.Errorf("%w: login is empty", ErrMailInvalid)
	}
	if strings.TrimSpace(m.Recipient) == "" {
		return fmt.Errorf("%w: recipient is empty", ErrMailInvalid)
	}
	for name, v := range map[string]string{
		"host": m.Host, "login": m.Username, "password": m.Password, "recipient": m.Recipient,
	} {
		if strings.ContainsAny(v, "\r\n") {
			return fmt.Errorf("%w: %s contains a line break", ErrMailInvalid, name)
		}
	}
	return nil
}

// LoadMailSettings returns the stored settings or ErrMailNotConfigured.
func LoadMailSettings(handle *sql.DB) (MailSettings, error) {
	var m MailSettings
	var updated string
	err := handle.QueryRow(
		`SELECT host, port, username, password, recipient, updated_at_utc
		 FROM mail_settings WHERE id = 1`,
	).Scan(&m.Host, &m.Port, &m.Username, &m.Password, &m.Recipient, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return MailSettings{}, ErrMailNotConfigured
	}
	if err != nil {
		return MailSettings{}, fmt.Errorf("db: load mail settings: %w", err)
	}
	// A stamp that cannot be read does not make the settings unusable.
	m.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return m, nil
}

// SaveMailSettings validates and stores the settings, replacing any
// previous ones. UpdatedAt is set to now.
func SaveMailSettings(handle *sql.DB, m MailSettings, now time.Time) error {
	if err := m.Validate(); err != nil {
		return err
	}
	if _, err := handle.Exec(
		`INSERT INTO mail_settings (id, host, port, username, password, recipient, updated_at_utc)
		 VALUES (1, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (id) DO UPDATE SET
		   host = excluded.host,
		   port = excluded.port,
		   username = excluded.username,
		   password = excluded.password,
		   recipient = excluded.recipient,
		   updated_at_utc = excluded.updated_at_utc`,
		strings.TrimSpace(m.Host), m.Port, strings.TrimSpace(m.Username), m.Password,
		strings.TrimSpace(m.Recipient), now.UTC().Format(time.RFC3339Nano),
	); err != nil {
		return fmt.Errorf("db: save mail settings: %w", err)
	}
	return nil
}

// DeleteMailSettings turns mail off. Deleting settings that are not there
// succeeds.
func DeleteMailSettings(handle *sql.DB) error {
	if _, err := handle.Exec(`DELETE FROM mail_settings`); err != nil {
		return fmt.Errorf("db: delete mail settings: %w", err)
	}
	return nil
}
