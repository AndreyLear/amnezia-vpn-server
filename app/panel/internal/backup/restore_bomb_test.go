package backup

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Архив-бомба: сотни килобайт сжатых нулей разворачивались в манифест или
// базу до 1 ГиБ — манифест целиком в память (amnezia-vpn-server-76mp.29).

func TestUnpackRejectsOversizedManifest(t *testing.T) {
	manifest := append([]byte(validManifestJSON()), bytes.Repeat([]byte(" "), maxManifestSize)...)
	archive := buildArchive(t, []archiveEntry{
		{name: manifestFilename, typ: tar.TypeReg, data: manifest},
		{name: snapshotFilename, typ: tar.TypeReg, data: sqliteFileBytes(t, "")},
	})
	_, err := unpackArchive(archive, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("манифест %d байт: err = %v, ждали отказ", len(manifest), err)
	}
}

func TestUnpackRejectsSnapshotOutOfProportionToArchive(t *testing.T) {
	image := make([]byte, minSnapshotLimit+1) // нули сжимаются в килобайты
	archive := buildArchive(t, []archiveEntry{
		{name: manifestFilename, typ: tar.TypeReg, data: []byte(validManifestJSON())},
		{name: snapshotFilename, typ: tar.TypeReg, data: image},
	})
	fi, err := os.Stat(archive)
	if err != nil {
		t.Fatal(err)
	}
	if snapshotLimit(fi.Size()) >= int64(len(image)) {
		t.Fatalf("архив %d байт допускает базу %d — тест ничего не проверяет", fi.Size(), snapshotLimit(fi.Size()))
	}
	dir := t.TempDir()
	_, err = unpackArchive(archive, dir)
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("база %d байт из архива %d байт: err = %v, ждали отказ", len(image), fi.Size(), err)
	}
	if _, err := os.Stat(filepath.Join(dir, pendingDBName)); !os.IsNotExist(err) {
		t.Fatalf("база распакована вопреки пределу: %v", err)
	}
}

// Предел растёт с архивом: настоящий архив любой величины проходит.
func TestSnapshotLimitScalesWithArchive(t *testing.T) {
	if got := snapshotLimit(1); got != minSnapshotLimit {
		t.Fatalf("snapshotLimit(1) = %d, ждали нижний предел %d", got, minSnapshotLimit)
	}
	if got := snapshotLimit(10 << 20); got <= minSnapshotLimit {
		t.Fatalf("snapshotLimit(10 МиБ) = %d, не растёт с архивом", got)
	}
	if got := snapshotLimit(1 << 40); got != maxEntrySize {
		t.Fatalf("snapshotLimit(1 ТиБ) = %d, ждали потолок %d", got, maxEntrySize)
	}
}

// Inspect и Restore не распаковывают параллельно: пока одна распаковка
// идёт, вторая ждёт.
func TestInspectWaitsForConcurrentUnpack(t *testing.T) {
	archive := buildArchive(t, validEntries(t, ""))

	unpackMu.Lock()
	done := make(chan error, 1)
	go func() {
		_, err := Inspect(archive)
		done <- err
	}()
	select {
	case err := <-done:
		unpackMu.Unlock()
		t.Fatalf("Inspect распаковал, не дождавшись идущей распаковки (err = %v)", err)
	case <-time.After(200 * time.Millisecond):
	}
	unpackMu.Unlock()
	if err := <-done; err != nil {
		t.Fatalf("Inspect: %v", err)
	}
}
