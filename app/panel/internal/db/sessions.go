package db

import (
	"database/sql"
	"fmt"
	"time"
)

// Login sessions that survive a panel restart (amnezia-vpn-server-4aab).
//
// Sessions used to live only in the serve process's memory, so every
// restart logged everybody out. The panel restarts itself on every
// update, which meant an update always ended with a second modal stacked
// on top of the progress window — «Сессия сброшена… пароль сменили на
// сервере» — asking for a password in the middle of an ordinary update.
//
// What is stored, and why it is safe to store:
//
//   - id_hash is the SHA-256 of the session cookie value, never the value
//     itself. Reading the database does not let anyone log in: a hash
//     cannot be turned back into the cookie the panel expects.
//   - csrf_token is stored as is, because it has to be compared with what
//     a form or an API call submits. On its own it opens nothing — every
//     request that checks it has already been authenticated by the cookie.
//
// Sessions never reach a backup: the snapshot is cleared of them before it
// is archived (internal/backup). A restore therefore always starts with no
// sessions at all — cookies issued against the previous database must not
// authorize the restored one — and it does so whether the restore runs in
// the panel or from the command line.
const sessionsTable = `CREATE TABLE IF NOT EXISTS sessions (
		id_hash TEXT PRIMARY KEY,
		username TEXT NOT NULL,
		csrf_token TEXT NOT NULL,
		created_at_utc TEXT NOT NULL,
		expires_at_utc TEXT NOT NULL
	);`

// sessionStampLayout has a fixed width on purpose. The prune in
// LoadLiveSessions compares stamps as strings in SQL, and RFC3339Nano trims
// trailing fractional zeros: "…:00Z" and "…:00.5Z" differ in length, and
// since 'Z' sorts after '.', the string order stops matching the time
// order at sub-second boundaries. A live session could be pruned. Nine
// fractional digits always, UTC always — string order is time order.
const sessionStampLayout = "2006-01-02T15:04:05.000000000Z"

// SessionRecord is one stored session. It carries no password, hash of a
// password or key material — only identity, the CSRF secret and stamps.
type SessionRecord struct {
	IDHash    string
	Username  string
	CSRFToken string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// SaveSession writes or replaces one session.
func SaveSession(handle *sql.DB, rec SessionRecord) error {
	if _, err := handle.Exec(
		`INSERT INTO sessions (id_hash, username, csrf_token, created_at_utc, expires_at_utc)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT (id_hash) DO UPDATE SET
		   username = excluded.username,
		   csrf_token = excluded.csrf_token,
		   created_at_utc = excluded.created_at_utc,
		   expires_at_utc = excluded.expires_at_utc`,
		rec.IDHash, rec.Username, rec.CSRFToken,
		rec.CreatedAt.UTC().Format(sessionStampLayout),
		rec.ExpiresAt.UTC().Format(sessionStampLayout),
	); err != nil {
		return fmt.Errorf("db: save session: %w", err)
	}
	return nil
}

// DeleteSession removes one session by the hash of its id. Deleting a
// session that is not there succeeds.
func DeleteSession(handle *sql.DB, idHash string) error {
	if _, err := handle.Exec(`DELETE FROM sessions WHERE id_hash = ?`, idHash); err != nil {
		return fmt.Errorf("db: delete session: %w", err)
	}
	return nil
}

// DeleteAllSessions removes every stored session.
func DeleteAllSessions(handle *sql.DB) error {
	if _, err := handle.Exec(`DELETE FROM sessions`); err != nil {
		return fmt.Errorf("db: delete sessions: %w", err)
	}
	return nil
}

// LoadLiveSessions returns the sessions still valid at now and drops the
// ones that have expired, so the table cannot grow with abandoned logins.
func LoadLiveSessions(handle *sql.DB, now time.Time) ([]SessionRecord, error) {
	stamp := now.UTC().Format(sessionStampLayout)
	if _, err := handle.Exec(`DELETE FROM sessions WHERE expires_at_utc <= ?`, stamp); err != nil {
		return nil, fmt.Errorf("db: prune sessions: %w", err)
	}
	rows, err := handle.Query(
		`SELECT id_hash, username, csrf_token, created_at_utc, expires_at_utc FROM sessions`,
	)
	if err != nil {
		return nil, fmt.Errorf("db: load sessions: %w", err)
	}
	defer rows.Close()
	var out []SessionRecord
	for rows.Next() {
		var rec SessionRecord
		var created, expires string
		if err := rows.Scan(&rec.IDHash, &rec.Username, &rec.CSRFToken, &created, &expires); err != nil {
			return nil, fmt.Errorf("db: scan session: %w", err)
		}
		// A row whose stamps cannot be read is skipped rather than failing
		// the whole load: one damaged row must not log everybody else out.
		if rec.CreatedAt, err = time.Parse(sessionStampLayout, created); err != nil {
			continue
		}
		if rec.ExpiresAt, err = time.Parse(sessionStampLayout, expires); err != nil {
			continue
		}
		if !rec.ExpiresAt.After(now) {
			continue
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: read sessions: %w", err)
	}
	return out, nil
}

// DeleteSessionsByUsername removes every stored session of username.
//
// The command line calls this when it changes a password
// (amnezia-vpn-server-4aab). Before sessions were stored, a password
// changed while the panel was stopped logged everybody out by itself: the
// next start had no sessions to restore. Now they survive the start, so
// the change must remove them from storage directly rather than rely on
// the running panel picking up the invalidation file in time.
func DeleteSessionsByUsername(handle *sql.DB, username string) error {
	if _, err := handle.Exec(`DELETE FROM sessions WHERE username = ?`, username); err != nil {
		return fmt.Errorf("db: delete user sessions: %w", err)
	}
	return nil
}
