// M8.4 restore preparation (docs/TECHNICAL_SPEC_v2.0.md §5, §6 CLI):
//
//	backup.age
//	  → decrypt (operator identity, never stored on the VPS)
//	  → strict unpack (exactly manifest.json + amnezia.sqlite)
//	  → validate manifest
//	  → SQLite integrity_check
//	  → schema compatibility (Q6: exact match)
//	  → safety backup of the current database
//	  → pending marker → restart required
//
// Contract invariants:
//   - the live database is never modified: on any failure (including
//     behind the safety backup) the working state is untouched;
//   - the pending state is a 0700 directory next to the database
//     (<dbdir>/.restore-pending/amnezia.sqlite). Its exclusive
//     creation is the marker and the lock: a second restore fails with
//     ErrRestorePending (Q14: one restore; the marker is applied by
//     panel-init/restart);
//   - archives must contain exactly manifest.json + amnezia.sqlite as
//     regular files with bare names (Q15): directories, symlinks,
//     hard links, absolute paths, "..", duplicates and extra files are
//     rejected (Q11);
//   - the identity is consumed in memory only and never written to
//     disk.
package backup

import (
	"archive/tar"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"filippo.io/age"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
	"github.com/klauspost/compress/zstd"
)

// maxEntrySize caps a single archive entry (header-declared). The
// database snapshots of this appliance are small; the cap protects
// against hostile archives that declare enormous entries (Q13l/DoS).
const maxEntrySize = 1 << 30 // 1 GiB

const (
	pendingDirName = ".restore-pending" // marker directory next to the live DB
	pendingDBName  = "amnezia.sqlite"   // the restored image inside the marker
)

// ErrRestorePending reports that a restore is already prepared and the
// appliance must be restarted before another restore (Q14).
var ErrRestorePending = errors.New("backup: restore already pending; restart required")

// RestoreResult describes a prepared (not yet applied) restore.
type RestoreResult struct {
	Archive      string // basename of the restored archive
	PendingDB    string // path of the restored database image (pending)
	SafetyBackup string // path of the safety backup of the previous state
}

// Restore prepares restoring the archive at srcPath into the appliance:
// every validation runs before anything is written, the live database
// at dbPath is never touched, and the pending marker is the exclusive
// 0700 directory next to it (Q7a). handle is only used for the safety
// backup (SQLite Backup API). ids are the operator-supplied age
// identities; they are used in memory only. recipient is the
// deployment public key (AGE_RECIPIENT) used for the safety backup.
// now is injectable for tests.
func Restore(handle *sql.DB, dbPath, srcPath, backupsDir, recipient string, ids []age.Identity, now func() time.Time) (RestoreResult, error) {
	var res RestoreResult
	if now == nil {
		now = time.Now
	}
	if len(ids) == 0 {
		return res, errors.New("backup: no identity provided")
	}
	recipient = trimRecipient(recipient)
	if recipient == "" {
		return res, errors.New("backup: empty age recipient")
	}
	rec, err := age.ParseX25519Recipient(recipient)
	if err != nil {
		return res, fmt.Errorf("backup: invalid age recipient: %w", err)
	}
	// The source archive must be a regular file: a symlink or other
	// special entry is never opened.
	fi, err := os.Lstat(srcPath)
	if err != nil {
		return res, fmt.Errorf("backup: open archive: %w", err)
	}
	if !fi.Mode().IsRegular() {
		return res, errors.New("backup: archive is not a regular file")
	}

	pendingDir := filepath.Join(filepath.Dir(dbPath), pendingDirName)
	// The marker is created exclusively: its existence (whether from a
	// completed or an in-flight restore) blocks a second one (Q14).
	if err := os.Mkdir(pendingDir, 0o700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return res, ErrRestorePending
		}
		return res, fmt.Errorf("backup: create pending: %w", err)
	}
	ok := false
	defer func() {
		if !ok {
			os.RemoveAll(pendingDir)
		}
	}()

	// 1. decrypt + strict unpack into the pending directory.
	manifestBytes, err := unpackArchive(srcPath, pendingDir, ids)
	if err != nil {
		return res, err
	}

	// 2. validate the manifest against the M8 contract.
	m, err := UnmarshalManifest(manifestBytes)
	if err != nil {
		return res, fmt.Errorf("backup: manifest: %w", err)
	}

	// 3. SQLite integrity_check + 4. schema compatibility (Q6: the
	// archive declares schema_version 3, and its stored version must
	// match the declaration — exact match only).
	snapPath := filepath.Join(pendingDir, pendingDBName)
	stored, err := validateRestoreImage(snapPath, m.SchemaVersion)
	if err != nil {
		return res, err
	}
	if stored != strconv.Itoa(m.SchemaVersion) {
		return res, fmt.Errorf("backup: schema_version mismatch: manifest %d, stored %s", m.SchemaVersion, stored)
	}

	// 5. safety backup of the current (untouched) database.
	res.Archive = filepath.Base(srcPath)
	res.PendingDB = snapPath
	safety, err := Safety(handle, backupsDir, rec.String(), now)
	if err != nil {
		return res, err
	}
	res.SafetyBackup = safety

	// 6. make the pending state durable before announcing it.
	if err := syncDir(filepath.Dir(dbPath)); err != nil {
		return res, fmt.Errorf("backup: fsync pending: %w", err)
	}
	ok = true
	return res, nil
}

// trimRecipient trims whitespace around the deployment recipient.
func trimRecipient(r string) string {
	return strings.TrimSpace(r)
}

// unpackArchive decrypts srcPath with ids and unpacks strictly two
// entries — manifest.json and amnezia.sqlite — as regular files with
// bare names into dir, returning the manifest bytes. Anything else
// (paths, special entries, duplicates, extra files, oversized
// entries) is rejected.
func unpackArchive(srcPath, dir string, ids []age.Identity) ([]byte, error) {
	src, err := os.Open(srcPath)
	if err != nil {
		return nil, fmt.Errorf("backup: open archive: %w", err)
	}
	defer src.Close()

	rd, err := age.Decrypt(src, ids...)
	if err != nil {
		return nil, fmt.Errorf("backup: decrypt: %w", err)
	}
	zr, err := zstd.NewReader(rd)
	if err != nil {
		return nil, fmt.Errorf("backup: decompress: %w", err)
	}
	defer zr.Close()
	tr := tar.NewReader(zr)

	var manifest []byte
	seen := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("backup: unpack: %w", err)
		}
		if !validEntryName(hdr.Name) {
			return nil, fmt.Errorf("backup: unpack: unexpected entry name %q", hdr.Name)
		}
		if hdr.Typeflag != tar.TypeReg {
			return nil, fmt.Errorf("backup: unpack: entry %q is %s, not a regular file", hdr.Name, tarTypeName(hdr.Typeflag))
		}
		if hdr.Size < 0 || hdr.Size > maxEntrySize {
			return nil, fmt.Errorf("backup: unpack: entry %q too large", hdr.Name)
		}
		if seen[hdr.Name] {
			return nil, fmt.Errorf("backup: unpack: duplicate entry %q", hdr.Name)
		}
		seen[hdr.Name] = true
		if hdr.Name == manifestFilename {
			manifest, err = io.ReadAll(io.LimitReader(tr, maxEntrySize+1))
			if err != nil {
				return nil, fmt.Errorf("backup: unpack: %s: %w", hdr.Name, err)
			}
			// limitReader caps at Size == maxEntrySize already enforced
			if len(manifest) > maxEntrySize {
				return nil, fmt.Errorf("backup: unpack: manifest too large")
			}
			continue
		}
		// amnezia.sqlite
		path := filepath.Join(dir, hdr.Name)
		dst, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return nil, fmt.Errorf("backup: unpack: create %s: %w", hdr.Name, err)
		}
		n, err := io.CopyN(dst, tr, hdr.Size)
		cerr := dst.Close()
		if err != nil {
			os.Remove(path)
			return nil, fmt.Errorf("backup: unpack: write %s: %w", hdr.Name, err)
		}
		if cerr != nil {
			os.Remove(path)
			return nil, fmt.Errorf("backup: unpack: close %s: %w", hdr.Name, cerr)
		}
		if n != hdr.Size {
			os.Remove(path)
			return nil, fmt.Errorf("backup: unpack: truncated %s", hdr.Name)
		}
	}
	if !seen[manifestFilename] {
		return nil, errors.New("backup: unpack: manifest.json missing")
	}
	if !seen[pendingDBName] {
		return nil, errors.New("backup: unpack: amnezia.sqlite missing")
	}
	if len(seen) != 2 {
		return nil, fmt.Errorf("backup: unpack: unexpected extra entries")
	}
	return manifest, nil
}

// validEntryName admits only the two bare contract names: no path
// separators, no leading "/", no "."/".." components can appear.
func validEntryName(name string) bool {
	switch name {
	case manifestFilename, pendingDBName:
		return true
	}
	return false
}

func tarTypeName(t byte) string {
	switch t {
	case tar.TypeDir:
		return "a directory"
	case tar.TypeSymlink:
		return "a symlink"
	case tar.TypeLink:
		return "a hard link"
	case tar.TypeChar:
		return "a character device"
	case tar.TypeBlock:
		return "a block device"
	}
	return fmt.Sprintf("type %q", string(rune(t)))
}

// validateRestoreImage runs PRAGMA integrity_check on the restored
// image and reads its stored schema_version. want is the schema
// version declared by the manifest: the stored version must match it
// exactly (Q6 exact-match policy).
func validateRestoreImage(snapPath string, want int) (string, error) {
	snap, err := sql.Open("sqlite", snapPath)
	if err != nil {
		return "", fmt.Errorf("backup: open restored database: %w", err)
	}
	defer snap.Close()
	var result string
	if err := snap.QueryRow(`PRAGMA integrity_check`).Scan(&result); err != nil {
		return "", fmt.Errorf("backup: integrity check: %w", err)
	}
	if result != "ok" {
		return "", fmt.Errorf("backup: restored database integrity_check: %s", result)
	}
	stored, err := db.SchemaVersionStored(snap)
	if err != nil {
		return "", fmt.Errorf("backup: read schema_version: %w", err)
	}
	if stored == "" {
		return "", fmt.Errorf("backup: restored database has no schema_version")
	}
	if stored != strconv.Itoa(want) {
		return "", fmt.Errorf("backup: restored database schema_version %q, manifest %d", stored, want)
	}
	return stored, nil
}

// PendingPath returns the pending marker path for the database at
// dbPath, and whether a pending restore exists for it.
func PendingPath(dbPath string) (string, bool, error) {
	dir := filepath.Join(filepath.Dir(dbPath), pendingDirName)
	fi, err := os.Stat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return dir, false, nil
	}
	if err != nil {
		return dir, false, err
	}
	if !fi.IsDir() {
		return dir, false, fmt.Errorf("backup: pending path exists and is not a directory")
	}
	return dir, true, nil
}
