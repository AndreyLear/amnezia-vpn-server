// M7.1 auth-table accessors (TECHNICAL_SPEC_v2.0.md §3, table `auth`).
// All SQL against the auth table lives in this package: CLI/web/auth
// never touch it directly (panel is the single SQLite owner). The
// schema_version is untouched — the auth table is part of the M2
// baseline schema.
package db

import (
	"database/sql"
	"errors"
	"fmt"
)

// ErrAuthUserNotFound reports that no auth row matches the username.
var ErrAuthUserNotFound = errors.New("db: auth user not found")

// ErrAuthUserExists reports that a row with the username already
// exists: an insert would violate the UNIQUE constraint, so bootstrap
// must not overwrite it.
var ErrAuthUserExists = errors.New("db: auth user already exists")

// AuthUser is one row of the `auth` table (§3). PasswordHash is the
// encoded Argon2id hash produced by internal/auth; plaintext passwords
// are never stored here and never appear in this package's values or
// logs.
type AuthUser struct {
	ID           int64
	Username     string
	PasswordHash string
	TOTPSecret   string
	TOTPMode     string
}

// AuthUserByUsername loads the auth row by exact username match. A
// missing row is ErrAuthUserNotFound.
func AuthUserByUsername(handle *sql.DB, username string) (*AuthUser, error) {
	row := handle.QueryRow(
		`SELECT id, username, password_hash, COALESCE(totp_secret,''), COALESCE(totp_mode,'') FROM auth WHERE username = ?`,
		username,
	)
	var u AuthUser
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.TOTPSecret, &u.TOTPMode)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAuthUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("db: read auth user: %w", err)
	}
	return &u, nil
}

func GetTotpSecret(handle *sql.DB, username string) (string, error) {
	u, err := AuthUserByUsername(handle, username)
	if err != nil {
		return "", err
	}
	return u.TOTPSecret, nil
}

func SetTotpSecret(handle *sql.DB, username, secret string) error {
	res, err := handle.Exec(`UPDATE auth SET totp_secret = ? WHERE username = ?`, secret, username)
	if err != nil {
		return fmt.Errorf("db: set totp secret: %w", err)
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrAuthUserNotFound
	}
	return nil
}

func ClearTotpSecret(handle *sql.DB, username string) error {
	return SetTotpSecret(handle, username, "")
}

func SetTotpMode(handle *sql.DB, username, mode string) error {
	if mode != "" && mode != "2fa" && mode != "passwordless" {
		return errors.New("db: invalid totp mode")
	}
	res, err := handle.Exec(`UPDATE auth SET totp_mode = ? WHERE username = ?`, mode, username)
	if err != nil {
		return fmt.Errorf("db: set totp mode: %w", err)
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrAuthUserNotFound
	}
	return nil
}

func UpdateAuthPassword(handle *sql.DB, username, oldHash, newHash string) error {
	res, err := handle.Exec(`UPDATE auth SET password_hash = ? WHERE username = ? AND password_hash = ?`, newHash, username, oldHash)
	if err != nil {
		return fmt.Errorf("db: update auth password: %w", err)
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return errors.New("db: old password does not match")
	}
	return nil
}

// CreateAuthUser inserts a user with the already-derived password hash
// (internal/auth.HashPassword must run in the caller). The username is
// stored verbatim and the hash is stored verbatim — no transformation,
// no logging. A duplicate username fails with ErrAuthUserExists. The
// existence check and the insert run inside a single transaction: on
// the single SQLite connection (MaxOpenConns(1)) no other writer can
// slip between the check and the insert, so concurrent duplicate
// creates always classify as ErrAuthUserExists (never a raw
// UNIQUE-constraint error).
func CreateAuthUser(handle *sql.DB, username, passwordHash string) (*AuthUser, error) {
	if username == "" {
		return nil, errors.New("db: auth username must not be empty")
	}
	if passwordHash == "" {
		return nil, errors.New("db: auth password hash must not be empty")
	}
	tx, err := handle.Begin()
	if err != nil {
		return nil, fmt.Errorf("db: begin auth user: %w", err)
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRow(
		`SELECT COUNT(*) FROM auth WHERE username = ?`, username,
	).Scan(&exists); err != nil {
		return nil, fmt.Errorf("db: check auth user: %w", err)
	}
	if exists > 0 {
		return nil, ErrAuthUserExists
	}
	res, err := tx.Exec(
		`INSERT INTO auth (username, password_hash) VALUES (?, ?)`,
		username, passwordHash,
	)
	if err != nil {
		return nil, fmt.Errorf("db: insert auth user: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("db: auth last insert id: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("db: commit auth user: %w", err)
	}
	return &AuthUser{ID: id, Username: username, PasswordHash: passwordHash}, nil
}
