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
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
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

// BootSnapshot copies the live database to a timestamped snapshot when
// the live file exists, then trims old snapshots down to the rotation
// bound. A missing live database is a no-op (fresh install). A live
// path that is not a regular file is refused (no symlink following:
// the database and its snapshots are 0600 state with a strict
// security contract). The snapshot is fsynced, so a crash never leaves
// a torn copy that looks valid.
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
	in, err := os.Open(dbPath)
	if err == nil {
		if err = fault("snap.open"); err != nil {
			in.Close()
		}
	}
	if err != nil {
		return "", fmt.Errorf("backup: boot snapshot: open live database: %w", err)
	}
	defer in.Close()

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
		out, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			snapshot = name
			if err := copyStream(in, out); err != nil {
				return "", fmt.Errorf("backup: boot snapshot: %w", err)
			}
			break
		}
		if !errors.Is(err, os.ErrExist) {
			return "", fmt.Errorf("backup: boot snapshot: create %s: %w", name, err)
		}
		// same-second collision: retry with a numeric suffix
	}
	if err := rotateBootSnapshots(filepath.Dir(dbPath)); err != nil {
		return "", fmt.Errorf("backup: boot snapshot: %w", err)
	}
	return snapshot, nil
}

// copyStream copies the already-open source to the already-created
// destination with mode 0600 and fsyncs both the file and the
// destination directory (atomic-visible after a crash).
func copyStream(in *os.File, out *os.File) error {
	err := fault("snap.copy")
	if err == nil {
		_, err = io.Copy(out, in)
	}
	if err != nil {
		out.Close()
		_ = os.Remove(out.Name())
		return fmt.Errorf("copy to %s: %w", out.Name(), err)
	}
	syncErr := fault("snap.sync")
	if syncErr == nil {
		syncErr = out.Sync()
	}
	if syncErr != nil {
		out.Close()
		_ = os.Remove(out.Name())
		return fmt.Errorf("sync %s: %w", out.Name(), syncErr)
	}
	closeErr := fault("snap.close")
	if closeErr == nil {
		closeErr = out.Close()
	}
	if closeErr != nil {
		_ = os.Remove(out.Name())
		return fmt.Errorf("close %s: %w", out.Name(), closeErr)
	}
	return syncDir(filepath.Dir(out.Name()))
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
