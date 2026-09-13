// Package mailconf is the contract of data/mail.conf — the mail settings
// the host notification service reads (amnezia-vpn-server-2kr4, dfs2).
//
// The file is derived state, like awg0.conf: the truth is the
// mail_settings row in SQLite (internal/db/mail.go renders it through
// Write), and the file is reproduced from it at any time. Deleting the file
// loses nothing.
//
// The package imports nothing that touches SQLite on purpose: the sender
// (cmd/awgmail) runs on the host and only needs to read this file.
//
// Where it lives, and why there: data/ is written by panel and panel-init
// and is not mounted into the awg container at all. status/ is read-only
// for the panel, and config/ is readable by awg — the tunnel service has
// no use for the operator's mailbox password. The backup archive is a
// manifest plus a SQLite snapshot, so the file never enters it either.
//
// Format is JSON: the password may contain any character, and JSON quotes
// it unambiguously where a key=value file would need an escaping scheme
// of its own. Mode is 0600 and the write is atomic, so the reader never
// sees half a file.
//
// When there is nothing a sender could use — mail was never set up, or the
// password is missing after a restore — the file is removed rather than
// written: the service then stays silent instead of failing to log in, and
// a stale file with an old password never outlives the row it came from.
package mailconf

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/status"
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

// Write replaces mail.conf with f, or removes it when f is nil. On a write
// failure the previous file stays intact. Errors name only the path, never
// the content.
func Write(path string, f *File) error {
	if f == nil {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("mailconf: remove %s: %w", path, err)
		}
		return nil
	}
	data, err := json.Marshal(f)
	if err != nil {
		return fmt.Errorf("mailconf: encode: %w", err)
	}
	if err := status.WriteAtomic(path, append(data, '\n')); err != nil {
		return fmt.Errorf("mailconf: %w", err)
	}
	return nil
}

// ErrUnusable reports a mail.conf that exists but cannot be used to send.
// The message never includes the file content.
var ErrUnusable = errors.New("mailconf: file unusable")

// Load reads mail.conf. A missing file is returned as an error wrapping
// os.ErrNotExist, so the sender can tell «mail is off» from a broken file.
func Load(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		// json errors may quote a fragment of the input; the input holds
		// the password, so the cause is dropped.
		return nil, fmt.Errorf("%w: not valid JSON", ErrUnusable)
	}
	if f.Host == "" || f.Username == "" || f.Password == "" || f.Recipient == "" || f.Port < 1 || f.Port > 65535 {
		return nil, fmt.Errorf("%w: a field is missing", ErrUnusable)
	}
	return &f, nil
}
