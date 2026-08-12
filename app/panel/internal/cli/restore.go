package cli

// `panel restore` — M8.4 command (docs/TECHNICAL_SPEC_v2.0.md §5, §6):
//
//	restore <backup-name> --identity-stdin
//
// Only a backup file name from the backups directory is accepted (the
// M8.3 name validation applies; absolute paths and "../" are rejected).
// The age identity is read from stdin and never written to disk; the
// panel never restarts itself (no docker.sock) and does not regenerate
// awg0.conf here — that is the post-restart panel-init workflow. The
// command stops at "restart required" (exit 0 on success).

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"filippo.io/age"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/backup"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

const (
	opRestore = "restore"
	// identityStdinLimit caps the stdin identity budget (one or a few
	// AGE-SECRET-KEY lines; plenty of headroom, DoS-safe).
	identityStdinLimit = 64 << 10
)

// readIdentityStdin consumes the operator-supplied age identity from
// stdin, parsing every non-empty line as an X25519 identity. The raw
// secret never leaves memory and never reaches any output.
func (a *app) readIdentityStdin() ([]age.Identity, error) {
	raw, err := io.ReadAll(io.LimitReader(a.stdin, identityStdinLimit))
	if err != nil {
		return nil, fmt.Errorf("read identity: %w", err)
	}
	var ids []age.Identity
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		id, err := age.ParseX25519Identity(line)
		if err != nil {
			return nil, errors.New("restore: invalid identity on stdin")
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, errors.New("restore: no identity on stdin")
	}
	return ids, nil
}

func (a *app) cmdRestore(args []string) int {
	parsed, err := parseArgs(args, map[string]bool{"identity-stdin": false})
	if err != nil {
		return a.usageError(opRestore, err.Error())
	}
	if len(parsed.positional) != 1 {
		return a.usageError(opRestore, "expected exactly one backup file name")
	}
	if _, ok := parsed.flags["identity-stdin"]; !ok {
		return a.usageError(opRestore, "--identity-stdin is required")
	}
	name := parsed.positional[0]
	if !validBackupName(name) {
		return a.usageError(opRestore, fmt.Sprintf("invalid backup name %q", name))
	}
	// The file must live inside the backups directory and be a regular
	// file: a symlink is never opened.
	full := filepath.Join(backupsPath(), name)
	st, err := os.Lstat(full)
	if err != nil {
		return a.fatal(opRestore, fmt.Errorf("backup file: %w", err))
	}
	if !st.Mode().IsRegular() {
		return a.fatal(opRestore, errors.New("backup file is not a regular file"))
	}

	ids, err := a.readIdentityStdin()
	if err != nil {
		return a.fatal(opRestore, err)
	}
	handle, err := a.openDB()
	if err != nil {
		return a.fatal(opRestore, err)
	}
	defer handle.Close()
	recipient, err := backup.RecipientFromEnv()
	if err != nil {
		return a.fatal(opRestore, err)
	}
	res, err := backup.Restore(handle, db.DefaultPath(), full, backupsPath(), recipient, ids, nil)
	if err != nil {
		return a.fatal(opRestore, err)
	}
	fmt.Fprintf(a.stdout, "panel %s: backup %s is prepared and pending\n", opRestore, res.Archive)
	fmt.Fprintf(a.stdout, "panel %s: safety backup: %s\n", opRestore, res.SafetyBackup)
	fmt.Fprintf(a.stdout, "panel %s: restart required (pending: %s)\n", opRestore, filepath.Dir(res.PendingDB))
	return 0
}
