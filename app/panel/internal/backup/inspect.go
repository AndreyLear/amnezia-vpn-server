package backup

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ArchiveSettings holds the settings inside a backup that describe the
// machine it was taken on rather than the deployment as a whole.
//
// Restoring onto a freshly installed server is how a migration works, and
// carrying these two over silently breaks it: Endpoint is the address baked
// into every client config (a client pointed at the old server's IP simply
// stops connecting), and MTU was measured against the old uplink. The panel
// reads them before applying anything so it can ask which to keep.
//
// Values are returned verbatim, as stored; an absent key reads as empty
// because archives written before these settings existed are still valid.
type ArchiveSettings struct {
	Endpoint string
	MTU      string
}

// Inspect reports the host-specific settings of an archive without touching
// the live database, the pending marker or the backups directory. The
// archive is unpacked into a temporary directory that is removed again.
//
// Errors deliberately match the restore path's wording and never echo the
// archive's contents.
func Inspect(srcPath string) (ArchiveSettings, error) {
	var out ArchiveSettings

	dir, err := os.MkdirTemp("", "panel-inspect-*")
	if err != nil {
		return out, fmt.Errorf("backup: inspect: create temp dir: %w", err)
	}
	defer os.RemoveAll(dir)

	if _, err := unpackArchive(srcPath, dir); err != nil {
		return out, err
	}

	snapPath := filepath.Join(dir, pendingDBName)
	handle, err := sql.Open("sqlite", snapPath)
	if err != nil {
		return out, fmt.Errorf("backup: inspect: open snapshot: %w", err)
	}
	defer handle.Close()

	for _, item := range []struct {
		key  string
		dest *string
	}{
		{"endpoint", &out.Endpoint},
		{"mtu", &out.MTU},
	} {
		value, err := settingFromSnapshot(handle, item.key)
		if err != nil {
			return ArchiveSettings{}, err
		}
		*item.dest = value
	}
	return out, nil
}

// settingFromSnapshot reads one settings row out of a restored image. A
// missing row and a missing table both mean "not set": an archive from an
// older release is not an error.
func settingFromSnapshot(handle *sql.DB, key string) (string, error) {
	var value string
	err := handle.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	switch {
	case err == nil:
		return value, nil
	case errors.Is(err, sql.ErrNoRows):
		return "", nil
	default:
		// Do not surface the driver's text: it can quote archive content.
		return "", fmt.Errorf("backup: inspect: read setting %q", key)
	}
}
