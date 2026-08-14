// M9.3 data-loss guards for the panel-init restart workflow.
//
// Two mechanisms live here, both operating on the data directory next
// to the live database (db.DefaultPath), deliberately without any
// dependency on the age identity used by the encrypted backups/:
//
//  1. BootSnapshot: before panel-init opens the database, the live
//     file is copied to amnezia.sqlite.boot-<UTC timestamp> (0600,
//     rotation keeps the newest three). Even when the live database is
//     wiped or truncated by anything outside the panel, the last known
//     good state remains recoverable in place, without keys.
//  2. Sentinel (.server-initialized): `panel server init` writes the
//     sentinel after the server row exists. `panel init` then refuses
//     to silently create a fresh schema when the sentinel is present
//     but the database (or the server row inside it) is gone: the
//     fresh-install path must never mask data loss (the exact failure
//     observed as a silent empty schema on a wiped database).
package backup

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	// bootSnapshotGlob matches the boot-snapshot files kept next to the
	// live database. The timestamp format is sortable (ISO-8601 UTC).
	bootSnapshotGlob = "amnezia.sqlite.boot-*"

	// bootSnapshotKeep is the rotation bound: the newest N snapshots
	// are kept, older ones are removed.
	bootSnapshotKeep = 3

	// sentinelName marks a server initialized by `panel server init`.
	sentinelName = ".server-initialized"
)

// BootSnapshot snapshots the live database to a timestamped file when
// the live file exists, then trims old snapshots down to the rotation
// bound. A missing live database is a no-op (fresh install). A live
// path that is not a regular file is refused (no symlink following:
// the database and its snapshots are 0600 state with a strict
// security contract).
//
// T-115: the snapshot is taken with `VACUUM INTO`, which produces a
// consistent database copy even while another process (the running
// panel) writes — a raw io.Copy of the live file can be torn
// mid-page. The snapshot file is chmodded to 0600 and the directory is
// fsynced, so a crash never leaves a torn or mispermissioned copy that
// looks valid.
func BootSnapshot(dbPath string) (string, error) {
	fi, err := os.Lstat(dbPath)
	if err == nil {
		err = fault("snap.stat")
	}
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("backup: boot snapshot: stat live database: %w", err)
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("backup: boot snapshot: live database is not a regular file (refusing to follow)")
	}
	handle, err := sql.Open("sqlite", dbPath)
	if err == nil {
		// T-115 rework: VACUUM INTO contends with an active writer;
		// without a busy timeout the snapshot dies with SQLITE_BUSY
		// exactly when the live database is being written.
		if _, err = handle.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
			handle.Close()
		}
	}
	if err == nil {
		if err = fault("snap.open"); err != nil {
			handle.Close()
		}
	}
	if err != nil {
		return "", fmt.Errorf("backup: boot snapshot: open live database: %w", err)
	}
	defer handle.Close()

	snapshot := ""
	base := filepath.Join(
		filepath.Dir(dbPath),
		fmt.Sprintf("amnezia.sqlite.boot-%s", time.Now().UTC().Format("20060102T150405Z")),
	)
	for i := 1; ; i++ {
		name := base
		if i > 1 {
			name = fmt.Sprintf("%s-%d", base, i)
		}
		if _, err := os.Lstat(name); err == nil {
			continue // same-second collision: retry with a numeric suffix
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("backup: boot snapshot: stat %s: %w", name, err)
		}
		copyErr := fault("snap.copy")
		if copyErr == nil {
			_, copyErr = handle.Exec(
				"VACUUM INTO '" + strings.ReplaceAll(name, "'", "''") + "'",
			)
		}
		if copyErr != nil {
			return "", fmt.Errorf("backup: boot snapshot: copy to %s: %w", name, copyErr)
		}
		snapshot = name
		break
	}
	// VACUUM INTO creates the file with the process umask; enforce the
	// 0600 state contract explicitly.
	syncErr := fault("snap.sync")
	if syncErr == nil {
		syncErr = os.Chmod(snapshot, 0o600)
	}
	if syncErr != nil {
		_ = os.Remove(snapshot)
		return "", fmt.Errorf("backup: boot snapshot: sync %s: %w", snapshot, syncErr)
	}
	closeErr := fault("snap.close")
	if closeErr == nil {
		closeErr = syncDir(filepath.Dir(snapshot))
	}
	if closeErr != nil {
		_ = os.Remove(snapshot)
		return "", fmt.Errorf("backup: boot snapshot: close %s: %w", snapshot, closeErr)
	}
	if err := rotateBootSnapshots(filepath.Dir(dbPath)); err != nil {
		return "", fmt.Errorf("backup: boot snapshot: %w", err)
	}
	return snapshot, nil
}

// rotateBootSnapshots removes all but the newest bootSnapshotKeep
// snapshots in dir. Lexicographic order equals chronological order
// (sortable timestamps).
func rotateBootSnapshots(dir string) error {
	var matches []string
	err := fault("snap.rotate-glob")
	if err == nil {
		matches, err = filepath.Glob(filepath.Join(dir, bootSnapshotGlob))
	}
	if err != nil {
		return fmt.Errorf("glob snapshots: %w", err)
	}
	if len(matches) <= bootSnapshotKeep {
		return nil
	}
	sort.Strings(matches)
	for _, old := range matches[:len(matches)-bootSnapshotKeep] {
		removeErr := fault("snap.rotate-remove")
		if removeErr == nil {
			removeErr = os.Remove(old)
		}
		if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return fmt.Errorf("remove old snapshot %s: %w", old, removeErr)
		}
	}
	return nil
}

// SentinelPath returns the sentinel location for the data directory of
// dbPath.
func SentinelPath(dbPath string) string {
	return filepath.Join(filepath.Dir(dbPath), sentinelName)
}

// SentinelPresent reports whether the initialization sentinel exists.
func SentinelPresent(dbPath string) (bool, error) {
	path := SentinelPath(dbPath)
	err := fault("sentinel.present")
	if err == nil {
		_, err = os.Lstat(path)
	}
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("backup: sentinel: %w", err)
}

// WriteSentinel creates the initialization sentinel atomically (tmp +
// rename + dir fsync). It is a no-op when already present. The
// sentinel marks "the server row exists"; it is written only by
// `panel server init` (and by panel-init when it regenerates a config
// for an initialized deployment that predates the sentinel).
func WriteSentinel(dbPath string) error {
	path := SentinelPath(dbPath)
	err := fault("sentinel.lstat")
	if err == nil {
		_, err = os.Lstat(path)
	}
	if err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("backup: sentinel: %w", err)
	}
	tmp := path + ".tmp"
	mkdirErr := fault("sentinel.mkdir")
	if mkdirErr == nil {
		mkdirErr = os.MkdirAll(filepath.Dir(path), 0o750)
	}
	if mkdirErr != nil {
		return fmt.Errorf("backup: sentinel: mkdir: %w", mkdirErr)
	}
	writeErr := fault("sentinel.write")
	if writeErr == nil {
		writeErr = os.WriteFile(tmp, []byte("initialized\n"), 0o600)
	}
	if writeErr != nil {
		return fmt.Errorf("backup: sentinel: write %s: %w", tmp, writeErr)
	}
	renameErr := fault("sentinel.rename")
	if renameErr == nil {
		renameErr = os.Rename(tmp, path)
	}
	if renameErr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("backup: sentinel: install %s: %w", path, renameErr)
	}
	return syncDir(filepath.Dir(path))
}

// testFault, when set, injects the named failure into a boot-snapshot
// or sentinel I/O step so tests can exercise error paths that are not
// reachable through the public API (nil in production).
var testFault func(step string) error

// fault invokes the injection hook when installed; it is a no-op in
// production builds.
func fault(step string) error {
	if testFault == nil {
		return nil
	}
	return testFault(step)
}
