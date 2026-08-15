package cli

// `panel backup` — M8.3 commands (docs/TECHNICAL_SPEC_v2.0.md §5, §6):
//
//	backup create   create a tar.zst backup of the database
//	backup list     list the existing backup archives
//
// The heavy lifting lives in internal/backup (M8.2); this file only
// wires it to the CLI conventions: parseArgs, openDB, generic errors,
// and the M4 exit codes (0 success, 1 runtime error, 2 usage).
//
// The command output is strictly diagnostic: create prints the path of
// the installed archive, list prints bare file names. It never prints
// database content, keys or the password hash; the archive itself is
// never read or unpacked by list.

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/backup"
)

// backupsPath returns the backups directory. AMNEZIA_BACKUPS_PATH
// overrides the default for tests and custom setups, mirroring
// AMNEZIA_DB_PATH / AMNEZIA_CONFIG_PATH. The default matches the
// M8.7 compose mount (./backups:/data/backups, RW only panel), which
// the web UI also serves.
func backupsPath() string {
	if p := os.Getenv("AMNEZIA_BACKUPS_PATH"); p != "" {
		return p
	}
	return "/data/backups"
}

// backupNameRe matches exactly the archive naming contract (§5):
// backup-YYYY-MM-DD.tar.zst. Only names matching this shape are
// listed, so no entry can smuggle path separators or traversal
// sequences into the output (a name containing "/" or ".." can never
// match).
var backupNameRe = regexp.MustCompile(`^backup-(\d{4}-\d{2}-\d{2})\.tar\.zst$`)

// validBackupName reports whether name is a backup-<date>.tar.zst
// file with a real calendar date (backup-2026-13-99 is not ours).
func validBackupName(name string) bool {
	m := backupNameRe.FindStringSubmatch(name)
	if m == nil {
		return false
	}
	_, err := time.Parse("2006-01-02", m[1])
	return err == nil
}

// cmdBackup dispatches the backup subcommands.
func (a *app) cmdBackup(args []string) int {
	if len(args) == 0 {
		return a.usageError(opBackup, "missing subcommand")
	}
	switch args[0] {
	case "create":
		return a.cmdBackupCreate(args[1:])
	case "list":
		return a.cmdBackupList(args[1:])
	default:
		return a.usageError(opBackup, fmt.Sprintf("unknown subcommand %q", args[0]))
	}
}

// cmdBackupCreate snapshots the database and installs the archive
// into backups/. On success the absolute path of the installed archive
// is printed — the only output, and it carries no secrets.
func (a *app) cmdBackupCreate(args []string) int {
	parsed, err := parseArgs(args, nil)
	if err != nil {
		return a.usageError(opBackupCreate, err.Error())
	}
	if len(parsed.positional) > 0 {
		return a.usageError(opBackupCreate, "unexpected arguments")
	}
	handle, err := a.openDB()
	if err != nil {
		return a.fatal(opBackupCreate, err)
	}
	defer handle.Close()
	path, err := backup.Create(handle, backupsPath(), nil)
	if err != nil {
		return a.fatal(opBackupCreate, err)
	}
	return a.ok(opBackupCreate, path)
}

// cmdBackupList prints the names of the existing backups, one per
// line, strictly in directory order (ReadDir sorts by name). The
// command never opens the archives and never follows directory
// entries: only regular files with the exact archive name shape are
// listed, and only the bare file name is printed. A missing backups
// directory is not an error: the answer is an empty list (exit 0).
func (a *app) cmdBackupList(args []string) int {
	parsed, err := parseArgs(args, nil)
	if err != nil {
		return a.usageError(opBackupList, err.Error())
	}
	if len(parsed.positional) > 0 {
		return a.usageError(opBackupList, "unexpected arguments")
	}
	entries, err := os.ReadDir(backupsPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0
		}
		return a.fatal(opBackupList, fmt.Errorf("read backups directory: %w", err))
	}
	for _, e := range entries {
		if !e.Type().IsRegular() || !validBackupName(e.Name()) {
			continue
		}
		fmt.Fprintln(a.stdout, e.Name())
	}
	return 0
}

const (
	opBackup       = "backup"
	opBackupCreate = "backup create"
	opBackupList   = "backup list"
)
