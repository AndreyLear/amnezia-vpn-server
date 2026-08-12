// M8.5/M8.8 restart workflow: application of a prepared restore.
//
// The restore pipeline (restore.go) stops at the pending marker
// (.restore-pending/ next to the live database). The ТЗ §5 chain
// requires the marker to be APPLIED at stack restart, inside
// panel-init, before the database is opened:
//
//	restore (prepare) → restart → panel-init applies → migrations →
//	regenerate awg0.conf → AWG startup
//
// ApplyPending serializes with a persistent advisory lock
// (.restore-apply.lock) and swaps atomically (same filesystem,
// rename(2)):
//
//  1. claim:   marker → marker+".applying"   (rename, atomic)
//  2. retire:  live → live+".pre-restore"
//  3. install: applying/amnezia.sqlite → live
//  4. commit:  fsync, remove the now-empty applying dir, fsync
//
// Safety model:
//
//   - Concurrency: the flock is exclusive and non-blocking; only one
//     apply runs at a time, a concurrent caller no-ops. No rollback
//     logic exists, so a process can never destroy another's swap.
//   - Crash: the claim step leaves visible progress on disk, so the
//     next init RESUMES forward from any crash window (the steps are
//     renames, each is atomic): claim-then-crash retries the swap,
//     swap-then-crash finishes the cleanup, and a half-swap (retired
//     copy present, live missing) installs the image.
//   - One-shot applies: a fresh data directory without a live database
//     is supported (restore before first start).
//
// The retire copy (live+".pre-restore") is deliberately KEPT after a
// successful apply: it is 0600, root-owned (the same security class as
// the live database) and gives an in-place recovery net that does not
// depend on the age identity used at restore time. The restore-time
// safety backup (backups/) remains the encrypted copy; ApplyPending
// never writes outside the database directory, and a leftover applying
// directory that fails the consistency check is left in place for
// operator inspection.
package backup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// applyingSuffix marks the claim step of an apply: the marker renamed
// aside while its image is being swapped in.
const applyingSuffix = ".applying"

// lockName is the persistent advisory lock serializing applies.
const applyLockName = ".restore-apply.lock"

// preRestoreSuffix is the retired live database name kept after an
// apply as an in-place recovery copy.
const preRestoreSuffix = ".pre-restore"

// ApplyPending applies the pending restore next to dbPath. It reports
// whether a restore was applied; a missing marker is a no-op
// (false, nil). Callers run it BEFORE opening the database.
func ApplyPending(dbPath string) (bool, error) {
	dir := filepath.Dir(dbPath)
	marker := filepath.Join(dir, pendingDirName)
	applying := marker + applyingSuffix

	// Serialize: a concurrent apply either finishes before us (lock
	// busy → no-op) or died (lock released by the kernel → crash
	// resume below).
	lock, err := os.OpenFile(filepath.Join(dir, applyLockName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return false, fmt.Errorf("backup: apply: open lock: %w", err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return false, nil
		}
		return false, fmt.Errorf("backup: apply: lock: %w", err)
	}

	// Claim the marker (or resume a crashed apply that already claimed
	// it). The rename is atomic and exclusive; the marker directory
	// itself stays visible to the web UI until the swap is done.
	if _, err := os.Lstat(applying); errors.Is(err, os.ErrNotExist) {
		if _, ok, err := PendingPath(dbPath); err != nil {
			return false, err
		} else if !ok {
			return false, nil
		}
		if err := os.Rename(marker, applying); err != nil {
			return false, fmt.Errorf("backup: apply: claim pending marker: %w", err)
		}
	} else if err != nil {
		return false, err
	}

	return finishApply(dbPath, marker, applying)
}

// finishApply drives the swap to completion for the claimed applying
// directory. It is forward-only: every step is an atomic rename, the
// states in between are detectable and resumable, and no rollback ever
// runs (a rollback could destroy a concurrent apply's work).
func finishApply(dbPath, marker, applying string) (bool, error) {
	image := filepath.Join(applying, pendingDBName)
	live := dbPath
	retired := live + preRestoreSuffix

	haveImage, err := exists(image)
	if err != nil {
		return false, fmt.Errorf("backup: apply: stat restored image: %w", err)
	}
	if haveImage {
		fi, err := os.Lstat(image)
		if err != nil {
			return false, fmt.Errorf("backup: apply: stat restored image: %w", err)
		}
		if !fi.Mode().IsRegular() {
			// Nothing was touched: restore the pre-claim state.
			_ = os.Rename(applying, marker)
			return false, errors.New("backup: apply: restored image is not a regular file")
		}
	}
	haveLive, err := exists(live)
	if err != nil {
		return false, fmt.Errorf("backup: apply: stat live database: %w", err)
	}
	haveRetired, err := exists(retired)
	if err != nil {
		return false, fmt.Errorf("backup: apply: stat retired database: %w", err)
	}

	// Refuse inconsistent states BEFORE anything is touched: a
	// claimed apply must have something to install (image) or a
	// finished install to clean up (retired copy present).
	if !haveImage && !haveRetired {
		// Nothing was touched: restore the pre-claim state so the
		// marker stays visible and web mutations stay blocked.
		_ = os.Rename(applying, marker)
		return false, fmt.Errorf("backup: apply: inconsistent state: restored image missing and no retired database at %s", retired)
	}

	// Retire (step 2) if the crash fell between claim and retire, or on
	// a first run with an existing live database.
	if haveLive && !haveRetired {
		if err := os.Rename(live, retired); err != nil {
			return false, fmt.Errorf("backup: apply: retire live database: %w", err)
		}
	}

	// Install (step 3): the image is missing only when a previous
	// attempt already installed it.
	if haveImage {
		if err := os.Rename(image, live); err != nil {
			return false, fmt.Errorf("backup: apply: install restored database: %w", err)
		}
	}

	// The live database must exist after the swap (either it was there
	// all along or the image was installed onto it).
	haveLive, err = exists(live)
	if err != nil {
		return false, fmt.Errorf("backup: apply: stat live database: %w", err)
	}
	if !haveLive {
		return false, fmt.Errorf("backup: apply: inconsistent state: restored image absent and no database at %s", live)
	}

	// Commit (step 4): the applying directory is empty after the image
	// rename; removing it unblocks web mutations (Q10).
	if err := syncDir(filepath.Dir(live)); err != nil {
		return false, fmt.Errorf("backup: apply: sync swap: %w", err)
	}
	if err := os.Remove(applying); err != nil {
		return true, fmt.Errorf("backup: apply: remove applying marker: %w", err)
	}
	if err := syncDir(filepath.Dir(live)); err != nil {
		return false, fmt.Errorf("backup: apply: sync commit: %w", err)
	}
	return true, nil
}

// exists reports whether the path exists.
func exists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}
