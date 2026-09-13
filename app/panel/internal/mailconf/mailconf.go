// Package mailconf renders data/mail.conf — the mail settings the host
// notification service reads (amnezia-vpn-server-2kr4, dfs2).
//
// The file is derived state, like awg0.conf: the truth is the
// mail_settings row in SQLite (internal/db/mail.go), and Render reproduces
// the file from it at any time. Deleting the file loses nothing.
//
// Where it lives, and why there: data/ is written by panel and panel-init
// and is not mounted into the awg container at all. status/ is read-only
// for the panel, and config/ is readable by awg — the tunnel service has
// no use for the operator's mailbox password. The backup archive is a
// manifest plus a SQLite snapshot, so the file never enters it either.
//
// Format is JSON: the password may contain any character, and JSON quotes
// it unambiguously where a key=value file would need an escaping scheme
// of its own. Mode is 0600 and the write is atomic (awgconf.WriteAtomic),
// so the reader never sees half a file.
//
// When there is nothing a sender could use — mail was never set up, or the
// password is missing after a restore — the file is removed rather than
// written: the service then stays silent instead of failing to log in, and
// a stale file with an old password never outlives the row it came from.
package mailconf

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/awgconf"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

// PathFor returns the mail.conf location for the database at dbPath: next
// to it, so /data/amnezia.sqlite gives /data/mail.conf, and a panel run
// against a scratch database never touches the real data directory.
// AMNEZIA_MAIL_CONF_PATH overrides it, like every path of the project.
func PathFor(dbPath string) string {
	if p := os.Getenv("AMNEZIA_MAIL_CONF_PATH"); p != "" {
		return p
	}
	return filepath.Join(filepath.Dir(dbPath), "mail.conf")
}

// File is the on-disk shape of mail.conf. The sender address is the login.
type File struct {
	Host      string `json:"host"`
	Port      int    `json:"port"`
	Username  string `json:"username"`
	Password  string `json:"password"`
	Recipient string `json:"recipient"`
}

// Render writes mail.conf from the database, or removes it when there are
// no usable settings. On a write failure the previous file stays intact.
func Render(handle *sql.DB, path string) error {
	settings, err := db.LoadMailSettings(handle)
	if errors.Is(err, db.ErrMailNotConfigured) {
		return remove(path)
	}
	if err != nil {
		return err
	}
	if settings.PasswordMissing() {
		return remove(path)
	}
	data, err := json.Marshal(File{
		Host:      settings.Host,
		Port:      settings.Port,
		Username:  settings.Username,
		Password:  settings.Password,
		Recipient: settings.Recipient,
	})
	if err != nil {
		return fmt.Errorf("mailconf: encode: %w", err)
	}
	if err := awgconf.WriteAtomic(path, append(data, '\n')); err != nil {
		// The wrapped error names only paths, never the content.
		return fmt.Errorf("mailconf: %w", err)
	}
	return nil
}

func remove(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("mailconf: remove %s: %w", path, err)
	}
	return nil
}
