package backup

import (
	"archive/tar"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

// Valid 32-byte base64 keys for images the trial generation must accept.
const (
	probeServerPriv = "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE="
	probeServerPub  = "AgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI="
)

// imageWithServer returns a migrated database image whose server row is
// shaped by awgParams; seed == false leaves the server table empty.
func imageWithServer(t *testing.T, seed bool, awgParams string) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "src.sqlite")
	handle, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(handle); err != nil {
		t.Fatal(err)
	}
	if seed {
		seedImageServerParams(t, handle, awgParams)
	}
	handle.Close()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// seedImageServer gives an archive image the server row every restorable
// image has: Restore probes it with a trial generation.
func seedImageServer(t *testing.T, handle *sql.DB) {
	t.Helper()
	seedImageServerParams(t, handle, "{}")
}

func seedImageServerParams(t *testing.T, handle *sql.DB, awgParams string) {
	t.Helper()
	if _, err := handle.Exec(
		`INSERT INTO server (id, private_key, public_key, address, listen_port, dns, awg_params, created_at, updated_at)
		 VALUES (1, ?, ?, '10.66.66.1/24', 51820, '1.1.1.1', ?, '2026-08-01T00:00:00Z', '2026-08-01T00:00:00Z')`,
		probeServerPriv, probeServerPub, awgParams,
	); err != nil {
		t.Fatal(err)
	}
}

func archiveOf(t *testing.T, image []byte) string {
	t.Helper()
	return buildArchive(t, []archiveEntry{
		{name: manifestFilename, typ: tar.TypeReg, data: []byte(validManifestJSON())},
		{name: snapshotFilename, typ: tar.TypeReg, data: image},
	})
}

// Архив, после которого panel-init не поднимет сервер, отвергается до
// подмены базы: без строки server init падает «база потеряна», с
// параметрами, которых ParseParams не знает, падает генерация awg0.conf
// (amnezia-vpn-server-76mp.10).
func TestRestoreRejectsImageThatInitCannotStart(t *testing.T) {
	cases := []struct {
		name  string
		image func(t *testing.T) []byte
		want  string
	}{
		{"архив неинициализированной панели", func(t *testing.T) []byte { return imageWithServer(t, false, "") }, "no server row"},
		{"неизвестный ключ awg_params", func(t *testing.T) []byte { return imageWithServer(t, true, `{"jc":3,"zz":1}`) }, "awg0.conf"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newRestoreCtx(t)
			before, backups := c.liveState()
			_, err := c.doRestore(archiveOf(t, tc.image(t)))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Restore: err = %v, ждали отказ с %q", err, tc.want)
			}
			c.assertUntouched(before)
			c.assertNoMarker()
			c.assertNoNewBackups(backups)
			assertNoProbeLeftovers(t, c.dbPath)
		})
	}
}

// Образ с исправной строкой server проходит пробную генерацию, и после
// неё рядом с базой не остаётся ничего лишнего.
func TestRestoreProbeAcceptsStartableImage(t *testing.T) {
	c := newRestoreCtx(t)
	res, err := c.doRestore(archiveOf(t, imageWithServer(t, true, `{"jc":3,"jmin":1,"jmax":5}`)))
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(res.PendingDB))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != pendingDBName {
		t.Fatalf("в ожидающем каталоге лишнее: %v", namesOf(entries))
	}
	assertNoProbeLeftovers(t, c.dbPath)
	// Сам образ пробой не тронут: схема та, что в архиве.
	h, err := sql.Open("sqlite", res.PendingDB)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if _, err := db.ServerRow(h); err != nil {
		t.Fatalf("образ после пробы: %v", err)
	}
}

func assertNoProbeLeftovers(t *testing.T, dbPath string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".restore-probe") {
			t.Fatalf("после пробы остался %s", e.Name())
		}
	}
}
