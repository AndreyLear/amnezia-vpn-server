package backup

import (
	"archive/tar"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

// VLESS живёт в той же базе, что и AWG (решение владельца Q33), значит и
// в том же бэкапе: после восстановления ссылки клиентов обязаны работать
// как прежде — те же UUID, те же ключи Reality (amnezia-vpn-server-14y0.1).

var xrayFixture = db.XraySettings{
	PrivateKey:  "AAoRGB8mLTQ7QklQV15lbHN6gYiPlp2kq7K5wMfO1Vw",
	PublicKey:   "u1D_noKldM-_gg6X9g-5wUPsdBXPUU-M_Zjv9Z4FlhQ",
	ShortID:     "6ba85179e30d4fc2",
	Path:        "/f3a9c1d27e",
	Target:      db.XrayDefaultTarget,
	ServerName:  db.XrayDefaultServerName,
	LinkAddress: "vpn.example.com",
	LinkPort:    443,
}

const xrayClientUUID = "3f2504e0-4f89-41d3-9a0c-0305e82c3301"

func applyAndOpen(t *testing.T, c *restoreCtx, archive string) *sql.DB {
	t.Helper()
	if _, err := c.doRestore(archive); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	applied, err := ApplyPending(c.dbPath)
	if err != nil || !applied {
		t.Fatalf("ApplyPending: applied=%v err=%v", applied, err)
	}
	// Последовательность panel-init: открыть и мигрировать.
	h, err := db.Open(c.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() })
	if err := db.Migrate(h); err != nil {
		t.Fatalf("Migrate restored database: %v", err)
	}
	return h
}

func TestBackupRestoreCarriesVLESS(t *testing.T) {
	dir := t.TempDir()
	src, err := db.Open(filepath.Join(dir, "amnezia.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if err := db.Migrate(src); err != nil {
		t.Fatal(err)
	}
	seedImageServer(t, src)
	client, err := db.CreateClient(src, "10.66.66.1/24", db.NewClient{
		Name: "andrey", PrivateKey: probeServerPriv, PublicKey: probeServerPub,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetClientVLESSUUID(src, client.ID, xrayClientUUID); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveXraySettings(src, xrayFixture); err != nil {
		t.Fatal(err)
	}
	archive, err := Create(src, filepath.Join(dir, "backups"), func() time.Time { return fakeNow })
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Восстановление на сервер с другим живым состоянием и своим входом:
	// KeepLiveAuth переносит только вход, VLESS приходит из архива.
	c := newRestoreCtx(t)
	c.seedClient("bob", "x-test-client-private-key-bob")
	c.seedAuth("admin", testHash, "", "")
	h := applyAndOpen(t, c, archive)

	got, err := db.XraySettingsRow(h)
	if err != nil {
		t.Fatalf("настройки Xray не пережили восстановление: %v", err)
	}
	if *got != xrayFixture {
		t.Fatalf("настройки Xray после восстановления %+v, ожидались %+v", *got, xrayFixture)
	}
	restored, err := db.ClientByID(h, client.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Name != "andrey" || restored.VLESSUUID != xrayClientUUID {
		t.Fatalf("клиент после восстановления: %+v", restored)
	}
}

// Архив прежней версии (schema 10, без VLESS) восстанавливается, и каждый
// его клиент получает UUID при миграции; Xray остаётся не настроенным.
func TestRestorePreVLESSArchiveAssignsUUIDs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.sqlite")
	old, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(old); err != nil {
		t.Fatal(err)
	}
	seedImageServer(t, old)
	for _, stmt := range []string{
		`DROP INDEX idx_clients_vless_uuid`,
		`ALTER TABLE clients DROP COLUMN vless_uuid`,
		`DROP TABLE xray`,
		`UPDATE schema_meta SET value = '10' WHERE key = 'schema_version'`,
	} {
		if _, err := old.Exec(stmt); err != nil {
			t.Skipf("cannot build a v10 image here (%s): %v", stmt, err)
		}
	}
	if _, err := old.Exec(
		`INSERT INTO clients (name, private_key, public_key, address, enabled, created_at, updated_at)
		 VALUES ('alice', ?, ?, '10.66.66.2/32', 1, '2026-08-01T00:00:00Z', '2026-08-01T00:00:00Z')`,
		probeServerPriv, probeServerPub,
	); err != nil {
		t.Fatal(err)
	}
	old.Close()
	image, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	archive := buildArchive(t, []archiveEntry{
		{name: manifestFilename, typ: tar.TypeReg, data: []byte(validManifestJSONWithSchema(10))},
		{name: snapshotFilename, typ: tar.TypeReg, data: image},
	})

	c := newRestoreCtx(t)
	h := applyAndOpen(t, c, archive)
	all, err := db.ClientsAll(h)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Name != "alice" || !db.ValidVLESSUUID(all[0].VLESSUUID) {
		t.Fatalf("клиенты после восстановления архива v10: %+v", all)
	}
	if _, err := db.XraySettingsRow(h); err != db.ErrXrayNotConfigured {
		t.Fatalf("Xray настроился из архива без Xray: %v", err)
	}
}
