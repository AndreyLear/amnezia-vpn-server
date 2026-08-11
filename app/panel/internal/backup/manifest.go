// M8.2 backup manifest contract (docs/TECHNICAL_SPEC_v2.0.md §5).
// The manifest is the only plaintext metadata of a backup: it holds no
// keys, hashes or connection details — nothing derived from database
// content. Its JSON shape is fixed by the spec and pinned by tests.
package backup

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

// Manifest fields, per §5 manifest.json.
const (
	formatValue        = 1
	applicationValue   = "amnezia-vpn-server"
	applicationVersion = "2.0.0"
	manifestFilename   = "manifest.json"
	snapshotFilename   = "amnezia.sqlite"
	archiveSuffix      = ".tar.zst.age"
	filenameTimeLayout = "2006-01-02"
	manifestTimeLayout = time.RFC3339
)

// Manifest is the archive metadata. JSON field names and values are the
// M8 contract; do not add fields or rename keys without a spec change.
type Manifest struct {
	Format             int    `json:"format"`
	Application        string `json:"application"`
	ApplicationVersion string `json:"application_version"`
	SchemaVersion      int    `json:"schema_version"`
	CreatedAt          string `json:"created_at"`
}

// loadManifest builds the manifest for a fresh backup from the snapshot
// itself: schema_version is read from the backup image (schema_meta),
// never from the live database, so the manifest always describes the
// exact bytes it ships in. The stored version must match the version
// this binary implements (db.SchemaVersion), so the archive is always
// restorable by the binary that made it.
func loadManifest(snapPath string, now time.Time) (Manifest, error) {
	snap, err := sql.Open("sqlite", snapPath)
	if err != nil {
		return Manifest{}, fmt.Errorf("backup: open snapshot for manifest: %w", err)
	}
	defer snap.Close()
	stored, err := db.SchemaVersionStored(snap)
	if err != nil {
		return Manifest{}, fmt.Errorf("backup: read schema_version: %w", err)
	}
	if stored != db.SchemaVersion {
		return Manifest{}, fmt.Errorf("backup: database schema_version %q, binary supports %q", stored, db.SchemaVersion)
	}
	sv, err := strconv.Atoi(stored)
	if err != nil {
		return Manifest{}, fmt.Errorf("backup: schema_version %q is not an integer", stored)
	}
	return Manifest{
		Format:             formatValue,
		Application:        applicationValue,
		ApplicationVersion: applicationVersion,
		SchemaVersion:      sv,
		CreatedAt:          now.UTC().Format(manifestTimeLayout),
	}, nil
}

// Validate checks a parsed manifest against the M8 contract. Used now
// by tests and later by the restore pipeline (M8.4) before any state is
// touched.
func (m Manifest) Validate() error {
	switch {
	case m.Format != formatValue:
		return fmt.Errorf("backup: manifest format %d, want %d", m.Format, formatValue)
	case m.Application != applicationValue:
		return fmt.Errorf("backup: manifest application %q", m.Application)
	case m.ApplicationVersion != applicationVersion:
		return fmt.Errorf("backup: manifest application_version %q", m.ApplicationVersion)
	case m.SchemaVersion != schemaVersion():
		return fmt.Errorf("backup: manifest schema_version %d", m.SchemaVersion)
	}
	if _, err := time.Parse(manifestTimeLayout, m.CreatedAt); err != nil {
		return fmt.Errorf("backup: manifest created_at %q: %w", m.CreatedAt, err)
	}
	return nil
}

// schemaVersion is the only schema_version the M8 contract admits
// (manifest.schema_version = 3, §5). It mirrors db.SchemaVersion.
func schemaVersion() int {
	v, _ := strconv.Atoi(db.SchemaVersion)
	return v
}

// Marshal returns the canonical JSON bytes of the manifest (a single
// line; the exact byte stream is part of the contract tests).
func (m Manifest) Marshal() ([]byte, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("backup: marshal manifest: %w", err)
	}
	return append(b, '\n'), nil
}

// UnmarshalManifest parses and validates manifest JSON from an archive.
func UnmarshalManifest(data []byte) (Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, fmt.Errorf("backup: parse manifest: %w", err)
	}
	if err := m.Validate(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}
