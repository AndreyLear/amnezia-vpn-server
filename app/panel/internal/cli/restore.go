package cli

// `panel restore` — prepare a restore from a named archive in backups/:
//
//	restore <backup-name>
//
// Only a backup file name from the backups directory is accepted
// (absolute paths and "../" are rejected). The command stops at
// "restart required" (exit 0 on success); the web panel also applies
// in-process after upload.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/backup"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

const opRestore = "restore"

func (a *app) cmdRestore(args []string) int {
	parsed, err := parseArgs(args, nil)
	if err != nil {
		return a.usageError(opRestore, err.Error())
	}
	if len(parsed.positional) != 1 {
		return a.usageError(opRestore, "expected exactly one backup file name")
	}
	name := parsed.positional[0]
	if !validBackupName(name) {
		return a.usageError(opRestore, fmt.Sprintf("invalid backup name %q", name))
	}
	full := filepath.Join(backupsPath(), name)
	st, err := os.Lstat(full)
	if err != nil {
		return a.fatal(opRestore, fmt.Errorf("backup file: %w", err))
	}
	if !st.Mode().IsRegular() {
		return a.fatal(opRestore, errors.New("backup file is not a regular file"))
	}

	handle, err := a.openDB()
	if err != nil {
		return a.fatal(opRestore, err)
	}
	defer handle.Close()
	res, err := backup.Restore(handle, db.DefaultPath(), full, backupsPath(), nil)
	if err != nil {
		return a.fatal(opRestore, err)
	}
	fmt.Fprintf(a.stdout, "panel %s: backup %s is prepared and pending\n", opRestore, res.Archive)
	fmt.Fprintf(a.stdout, "panel %s: safety backup: %s\n", opRestore, res.SafetyBackup)
	fmt.Fprintf(a.stdout, "panel %s: restart required (pending: %s)\n", opRestore, filepath.Dir(res.PendingDB))
	return 0
}
