// M8.2 backup snapshot pipeline (T-143 home-use):
//
//	SQLite Backup API → amnezia.sqlite → manifest.json → tar.zst
//	→ atomic rename into backups/
//
// Invariants:
//   - the snapshot is taken through the SQLite Backup API — direct file
//     copying of the live database is forbidden;
//   - the archive contains exactly manifest.json + amnezia.sqlite;
//   - there is no age layer: the operator downloads/uploads tar.zst
//     over the already-authenticated panel session;
//   - the snapshot and manifest live only inside a 0700 staging
//     directory and are removed on every path, including errors;
//   - the final file is installed with an atomic rename + directory
//     fsync, and unique staging names make concurrent backups safe;
//   - the backups directory is 0700, the backup file 0600;
//   - errors are generic: they never contain database content.
package backup

import (
	"archive/tar"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/klauspost/compress/zstd"
	"modernc.org/sqlite"
)

// backuper is the exported method set of modernc's unexported conn type
// (modernc.org/sqlite v1.54.0). The SQLite Backup API lives on the
// driver's internal connection: NewBackup is an exported method of an
// unexported type, reached through sql.Conn.Raw + interface assertion.
// The returned *sqlite.Backup is an exported type with exported Step /
// Finish. If a future driver renames or removes the method, the
// assertion fails loudly and the snapshot test catches it immediately.
type backuper interface {
	NewBackup(dstURI string) (*sqlite.Backup, error)
}

// snapshot copies the live database into dstPath via the SQLite Backup
// API. dstPath must not exist. While the source connection is held the
// panel's single connection is busy, which serializes concurrent writes
// — the snapshot is a consistent point-in-time image, and the backup
// API itself also protects against torn reads.
func snapshot(handle *sql.DB, dstPath string) error {
	f, err := os.OpenFile(dstPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("backup: create snapshot: %w", err)
	}
	f.Close()

	conn, err := handle.Conn(context.Background())
	if err != nil {
		return fmt.Errorf("backup: connect: %w", err)
	}
	defer conn.Close()

	var b *sqlite.Backup
	if err := conn.Raw(func(d any) error {
		bi, ok := d.(backuper)
		if !ok {
			return errors.New("backup: sqlite driver does not expose the backup API")
		}
		b, err = bi.NewBackup(dstPath)
		return err
	}); err != nil {
		return fmt.Errorf("backup: start backup: %w", err)
	}
	for {
		more, err := b.Step(-1)
		if err != nil {
			return fmt.Errorf("backup: copy pages: %w", err)
		}
		if !more {
			break
		}
	}
	if err := b.Finish(); err != nil {
		return fmt.Errorf("backup: finish backup: %w", err)
	}
	return nil
}

// verifySnapshot runs PRAGMA integrity_check on the freshly created
// snapshot so a bad image is rejected before it is packed.
func verifySnapshot(snapPath string) error {
	snap, err := sql.Open("sqlite", snapPath)
	if err != nil {
		return fmt.Errorf("backup: open snapshot: %w", err)
	}
	defer snap.Close()
	var result string
	if err := snap.QueryRow(`PRAGMA integrity_check`).Scan(&result); err != nil {
		return fmt.Errorf("backup: integrity check: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("backup: snapshot integrity_check: %s", result)
	}
	return nil
}

// Create builds a full backup of the database and installs it into
// backupsDir as backup-YYYY-MM-DD.tar.zst. now is injectable for tests.
// The returned path is the final archive.
//
// The same-day name is replaced atomically (rename); concurrent Create
// calls can never corrupt each other because every call stages its
// files under a unique 0700 directory. Overwrite policy (Q8) is a
// caller-level concern.
func Create(handle *sql.DB, backupsDir string, now func() time.Time) (string, error) {
	return create(handle, backupsDir, now, func(ts time.Time) string {
		return "backup-" + ts.Format(filenameTimeLayout) + archiveSuffix
	})
}

// Safety builds a safety backup of the current database ahead of a
// restore (spec §5). Unlike Create it never overwrites: the name
// carries the time of day, so every safety backup is unique and no
// archive is ever lost.
func Safety(handle *sql.DB, backupsDir string, now func() time.Time) (string, error) {
	return create(handle, backupsDir, now, func(ts time.Time) string {
		return "safety-backup-" + ts.Format(filenameTimeLayout) + "-" + ts.Format(filenameTimeOfDayLayout) + archiveSuffix
	})
}

// create is the shared backup pipeline; nameOf derives the archive
// file name from the creation timestamp (which also feeds the
// manifest). create() never echoes live database state through the
// returned path or errors (except the chosen file name in case of
// failure — carrying no secrets by construction).
func create(handle *sql.DB, backupsDir string, now func() time.Time, nameOf func(time.Time) string) (string, error) {
	if now == nil {
		now = time.Now
	}

	if err := os.MkdirAll(backupsDir, 0o700); err != nil {
		return "", fmt.Errorf("backup: create backups dir: %w", err)
	}
	staging, err := os.MkdirTemp(backupsDir, ".staging-*")
	if err != nil {
		return "", fmt.Errorf("backup: create staging: %w", err)
	}
	// The staging directory holds plaintext (snapshot + manifest); it
	// must never survive, success or failure.
	defer os.RemoveAll(staging)

	ts := now().UTC()
	name := nameOf(ts)
	target := filepath.Join(backupsDir, name)

	snapPath := filepath.Join(staging, snapshotFilename)
	if err := snapshot(handle, snapPath); err != nil {
		return "", err
	}
	if err := verifySnapshot(snapPath); err != nil {
		return "", err
	}

	m, err := loadManifest(snapPath, ts)
	if err != nil {
		return "", err
	}
	manifestJSON, err := m.Marshal()
	if err != nil {
		return "", err
	}

	archivePath := filepath.Join(staging, name)
	if err := writeArchive(archivePath, manifestJSON, snapPath); err != nil {
		return "", err
	}

	if err := os.Rename(archivePath, target); err != nil {
		return "", fmt.Errorf("backup: install %s: %w", target, err)
	}
	if err := syncDir(backupsDir); err != nil {
		return "", fmt.Errorf("backup: fsync %s: %w", backupsDir, err)
	}
	return target, nil
}

// writeArchive streams tar(manifest, snapshot) → zstd into dstPath.
// The uncompressed tar is never materialized on disk.
func writeArchive(dstPath string, manifestJSON []byte, snapPath string) error {
	dst, err := os.OpenFile(dstPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("backup: create archive: %w", err)
	}
	ok := false
	defer func() {
		if !ok {
			dst.Close()
			os.Remove(dstPath)
		}
	}()

	zw, err := zstd.NewWriter(dst, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		return fmt.Errorf("backup: start zstd: %w", err)
	}
	tw := tar.NewWriter(zw)

	if err := addBytesEntry(tw, manifestFilename, manifestJSON); err != nil {
		return err
	}
	if err := addFileEntry(tw, snapshotFilename, snapPath); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("backup: close tar: %w", err)
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("backup: close zstd: %w", err)
	}
	if err := dst.Sync(); err != nil {
		return fmt.Errorf("backup: sync archive: %w", err)
	}
	if err := dst.Close(); err != nil {
		return fmt.Errorf("backup: close archive: %w", err)
	}
	ok = true
	return nil
}

func addBytesEntry(tw *tar.Writer, name string, data []byte) error {
	hdr := &tar.Header{
		Name:     name,
		Mode:     0o600,
		Size:     int64(len(data)),
		Typeflag: tar.TypeReg,
		ModTime:  time.Unix(0, 0).UTC(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("backup: write %s header: %w", name, err)
	}
	if _, err := tw.Write(data); err != nil {
		return fmt.Errorf("backup: write %s: %w", name, err)
	}
	return nil
}

func addFileEntry(tw *tar.Writer, name, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("backup: open %s: %w", name, err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("backup: stat %s: %w", name, err)
	}
	hdr := &tar.Header{
		Name:     name,
		Mode:     0o600,
		Size:     info.Size(),
		Typeflag: tar.TypeReg,
		ModTime:  time.Unix(0, 0).UTC(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("backup: write %s header: %w", name, err)
	}
	if _, err := io.Copy(tw, f); err != nil {
		return fmt.Errorf("backup: write %s: %w", name, err)
	}
	return nil
}

// syncDir fsyncs a directory so the preceding rename is durable.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
