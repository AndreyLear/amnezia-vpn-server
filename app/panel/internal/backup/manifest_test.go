package backup

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestManifestContract pins the exact JSON shape and values of the M8
// manifest (§5) and its canonical byte stream.
func TestManifestContract(t *testing.T) {
	m := Manifest{
		Format:             1,
		Application:        "amnezia-vpn-server",
		ApplicationVersion: "2.0.0",
		SchemaVersion:      3,
		CreatedAt:          "2026-08-12T10:30:00Z",
	}
	b, err := m.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := `{"format":1,"application":"amnezia-vpn-server","application_version":"2.0.0","schema_version":3,"created_at":"2026-08-12T10:30:00Z"}` + "\n"
	if string(b) != want {
		t.Fatalf("manifest bytes:\n got %q\nwant %q", b, want)
	}
}

func TestManifestValidate(t *testing.T) {
	base := Manifest{
		Format:             1,
		Application:        "amnezia-vpn-server",
		ApplicationVersion: "2.0.0",
		SchemaVersion:      3,
		CreatedAt:          "2026-08-12T10:30:00Z",
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}

	cases := []struct {
		name string
		mut  func(*Manifest)
	}{
		{"format", func(m *Manifest) { m.Format = 2 }},
		{"application", func(m *Manifest) { m.Application = "other" }},
		{"application_version", func(m *Manifest) { m.ApplicationVersion = "2.1.0" }},
		{"schema_version", func(m *Manifest) { m.SchemaVersion = 4 }},
		{"schema_version_zero", func(m *Manifest) { m.SchemaVersion = 0 }},
		{"created_at_garbage", func(m *Manifest) { m.CreatedAt = "not-a-time" }},
		{"created_at_missing", func(m *Manifest) { m.CreatedAt = "" }},
	}
	for _, tc := range cases {
		m := base
		tc.mut(&m)
		if err := m.Validate(); err == nil {
			t.Fatalf("%s: must be rejected", tc.name)
		}
	}
}

func TestManifestUnmarshal(t *testing.T) {
	ok, err := UnmarshalManifest([]byte(`{"format":1,"application":"amnezia-vpn-server","application_version":"2.0.0","schema_version":3,"created_at":"2026-08-12T10:30:00Z"}`))
	if err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}
	if ok.SchemaVersion != 3 || ok.CreatedAt != "2026-08-12T10:30:00Z" {
		t.Fatalf("parsed manifest: %+v", ok)
	}
	for _, bad := range []string{
		`{`,            // garbage
		`{"format":1}`, // missing fields
		`{"format":1,"application":"x","application_version":"2.0.0","schema_version":3,"created_at":"2026-08-12T10:30:00Z"}`, // bad application
		``, // empty
	} {
		if _, err := UnmarshalManifest([]byte(bad)); err == nil {
			t.Fatalf("manifest %q must be rejected", bad)
		}
	}
	// Extra JSON fields are tolerated by design: json.Unmarshal ignores
	// them and Validate checks only the contract fields.
	if _, err := UnmarshalManifest([]byte(`{"format":1,"application":"amnezia-vpn-server","application_version":"2.0.0","schema_version":3,"created_at":"2026-08-12T10:30:00Z","extra":"field"}`)); err != nil {
		t.Fatalf("extra field must be tolerated: %v", err)
	}
}

// TestLoadManifestFromSnapshot pins that schema_version comes from the
// snapshot image itself and must equal the binary's version.
func TestLoadManifestFromSnapshot(t *testing.T) {
	handle, dir := newTestDB(t)
	snapPath := filepath.Join(dir, "snap.sqlite")
	if err := snapshot(handle, snapPath); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	m, err := loadManifest(snapPath, fakeNow)
	if err != nil {
		t.Fatalf("loadManifest: %v", err)
	}
	if m.SchemaVersion != 3 {
		t.Fatalf("schema_version = %d, want 3", m.SchemaVersion)
	}
	if m.CreatedAt != "2026-08-12T10:30:00Z" {
		t.Fatalf("created_at = %q", m.CreatedAt)
	}
	if m.CreatedAt != fakeNow.UTC().Format(time.RFC3339) {
		t.Fatalf("created_at not RFC3339 UTC")
	}
	if !strings.HasSuffix(m.CreatedAt, "Z") {
		t.Fatalf("created_at not UTC: %q", m.CreatedAt)
	}
}
