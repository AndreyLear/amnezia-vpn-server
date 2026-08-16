package backup

import (
	"database/sql"
	"fmt"
	"os"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

type liveAuthRow struct {
	username string
	hash     string
	secret   string
	mode     string
}

// KeepLiveAuth copies the auth table from livePath+".pre-restore" onto
// the restored live database when the retired copy had at least one
// auth row. An absent retired file or an empty auth table leaves the
// archive login in place (restore before first user).
func KeepLiveAuth(livePath string) error {
	retired := livePath + preRestoreSuffix
	if _, err := os.Lstat(retired); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("backup: keep live auth: stat retired: %w", err)
	}

	src, err := openSQLiteReadOnly(retired)
	if err != nil {
		return fmt.Errorf("backup: keep live auth: open retired: %w", err)
	}
	defer src.Close()

	rows, err := loadAuthRows(src)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}

	dst, err := db.Open(livePath)
	if err != nil {
		return fmt.Errorf("backup: keep live auth: open live: %w", err)
	}
	defer dst.Close()
	if err := ensureAuthTOTPMode(dst); err != nil {
		return err
	}

	tx, err := dst.Begin()
	if err != nil {
		return fmt.Errorf("backup: keep live auth: begin: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM auth`); err != nil {
		return fmt.Errorf("backup: keep live auth: delete: %w", err)
	}
	for _, row := range rows {
		if _, err := tx.Exec(
			`INSERT INTO auth (username, password_hash, totp_secret, totp_mode) VALUES (?, ?, ?, ?)`,
			row.username, row.hash, row.secret, row.mode,
		); err != nil {
			return fmt.Errorf("backup: keep live auth: insert: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("backup: keep live auth: commit: %w", err)
	}
	return nil
}

func openSQLiteReadOnly(path string) (*sql.DB, error) {
	handle, err := sql.Open("sqlite", "file://"+path+"?mode=ro")
	if err != nil {
		return nil, err
	}
	handle.SetMaxOpenConns(1)
	if _, err := handle.Exec(`PRAGMA busy_timeout = 5000;`); err != nil {
		handle.Close()
		return nil, err
	}
	return handle, nil
}

func loadAuthRows(handle *sql.DB) ([]liveAuthRow, error) {
	hasMode, err := authHasColumn(handle, "totp_mode")
	if err != nil {
		return nil, err
	}
	query := `SELECT username, password_hash, COALESCE(totp_secret,''), COALESCE(totp_mode,'') FROM auth`
	if !hasMode {
		query = `SELECT username, password_hash, COALESCE(totp_secret,''), '' FROM auth`
	}
	rs, err := handle.Query(query)
	if err != nil {
		return nil, fmt.Errorf("backup: keep live auth: read retired: %w", err)
	}
	defer rs.Close()
	var out []liveAuthRow
	for rs.Next() {
		var row liveAuthRow
		if err := rs.Scan(&row.username, &row.hash, &row.secret, &row.mode); err != nil {
			return nil, fmt.Errorf("backup: keep live auth: scan: %w", err)
		}
		out = append(out, row)
	}
	if err := rs.Err(); err != nil {
		return nil, fmt.Errorf("backup: keep live auth: rows: %w", err)
	}
	return out, nil
}

func authHasColumn(handle *sql.DB, name string) (bool, error) {
	rs, err := handle.Query(`PRAGMA table_info(auth)`)
	if err != nil {
		return false, fmt.Errorf("backup: keep live auth: inspect auth: %w", err)
	}
	defer rs.Close()
	for rs.Next() {
		var cid, notnull, pk int
		var col, typ string
		var dflt sql.NullString
		if err := rs.Scan(&cid, &col, &typ, &notnull, &dflt, &pk); err != nil {
			return false, fmt.Errorf("backup: keep live auth: inspect auth: %w", err)
		}
		if col == name {
			return true, nil
		}
	}
	if err := rs.Err(); err != nil {
		return false, fmt.Errorf("backup: keep live auth: inspect auth: %w", err)
	}
	return false, nil
}

func ensureAuthTOTPMode(handle *sql.DB) error {
	ok, err := authHasColumn(handle, "totp_mode")
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	if _, err := handle.Exec(`ALTER TABLE auth ADD COLUMN totp_mode TEXT NOT NULL DEFAULT ''`); err != nil {
		return fmt.Errorf("backup: keep live auth: add totp_mode: %w", err)
	}
	return nil
}
