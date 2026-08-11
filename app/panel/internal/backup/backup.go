// M8.2 backup snapshot pipeline (docs/TECHNICAL_SPEC_v2.0.md §5):
//
//	SQLite Backup API → amnezia.sqlite → manifest.json → tar.zst → age
//	→ atomic rename into backups/
//
// Invariants (M8 contract):
//   - the snapshot is taken through the SQLite Backup API — direct file
//     copying of the live database is forbidden by the spec;
//   - the archive contains exactly manifest.json + amnezia.sqlite;
//   - the archive is always encrypted with age; the plaintext snapshot
//     and manifest live only inside a 0700 staging directory and are
//     removed on every path, including errors;
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
	"strings"
	"time"

	"filippo.io/age"
	"github.com/klauspost/compress/zstd"
	"modernc.org/sqlite"
)

// recipient in the M8 contract (Q2): the age recipient (public key)
// comes from the deployment configuration; the panel reads it from the
// AGE_RECIPIENT environment variable / .env (compose). The matching
// identity (private key) is never stored on the VPS and is supplied
// manually at restore time (M8.4). This package only ever handles the
// recipient.
const (
	envRecipient = "AGE_RECIPIENT"
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
// snapshot so a bad image is rejected before it is encrypted.
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
// backupsDir as backup-YYYY-MM-DD.tar.zst.age (spec §5). recipient is
// the age public key from the deployment configuration (AGE_RECIPIENT);
// now is injectable for tests. The returned path is the final archive.
//
// The same-day name is replaced atomically (rename); concurrent Create
// calls can never corrupt each other because every call stages its
// files under a unique 0700 directory. Overwrite policy (Q8) is a
// caller-level concern.
func Create(handle *sql.DB, backupsDir, recipient string, now func() time.Time) (string, error) {
	if now == nil {
		now = time.Now
	}
	recipient = strings.TrimSpace(recipient)
	if recipient == "" {
		return "", errors.New("backup: empty age recipient")
	}
	rec, err := age.ParseX25519Recipient(recipient)
	if err != nil {
		return "", fmt.Errorf("backup: invalid age recipient: %w", err)
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
	name := "backup-" + ts.Format(filenameTimeLayout) + archiveSuffix
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
	if err := encryptArchive(archivePath, manifestJSON, snapPath, rec); err != nil {
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

// encryptArchive streams tar(manifest, snapshot) → zstd → age into the
// final encrypted file; the plaintext archive is never materialized on
// disk (only the two plaintext inputs exist inside staging).
func encryptArchive(dstPath string, manifestJSON []byte, snapPath string, rec *age.X25519Recipient) error {
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

	enc, err := age.Encrypt(dst, rec)
	if err != nil {
		return fmt.Errorf("backup: start age: %w", err)
	}
	zw, err := zstd.NewWriter(enc, zstd.WithEncoderLevel(zstd.SpeedDefault))
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
	if err := enc.Close(); err != nil {
		return fmt.Errorf("backup: close age: %w", err)
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

// RecipientFromEnv returns the age recipient from the deployment
// configuration (AGE_RECIPIENT). The M8 contract (Q2): the public key
// may live on the VPS; the identity never does.
func RecipientFromEnv() (string, error) {
	r := strings.TrimSpace(os.Getenv(envRecipient))
	if r == "" {
		return "", fmt.Errorf("backup: %s is not set", envRecipient)
	}
	if _, err := age.ParseX25519Recipient(r); err != nil {
		return "", fmt.Errorf("backup: %s is not a valid age recipient", envRecipient)
	}
	return r, nil
}
