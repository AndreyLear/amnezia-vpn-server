package web

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/status"
)

// «Что у меня стоит» — вопрос, на который до этого отвечал только SSH
// (amnezia-vpn-server-rdcz).
func TestAPIVersionsReportsWhatIsKnown(t *testing.T) {
	f := newFixture(t)
	t.Setenv("AMNEZIA_VERSION", "2.9.0")

	versions := status.Versions{
		Schema:         "v1",
		AmneziaWGGo:    "0.2.19",
		AmneziaWGTools: "3.1.20260812",
	}
	data, err := json.Marshal(versions)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(f.statusPath), "versions.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	got := decodeAPI(t, f.get("/api/versions"))
	if got["product"] != "2.9.0" {
		t.Fatalf("product = %v, ожидалось 2.9.0", got["product"])
	}
	if got["amneziawg_go"] != "0.2.19" || got["amneziawg_tools"] != "3.1.20260812" {
		t.Fatalf("версии AmneziaWG не дошли: %v", got)
	}
	if got["schema"] != db.SchemaVersion {
		t.Fatalf("schema = %v, ожидалось %q", got["schema"], db.SchemaVersion)
	}
}

// Развёртывание, где контейнер awg старше этой возможности, обязано отвечать
// «неизвестно», а не падать и не выдумывать.
func TestAPIVersionsSaysUnknownRatherThanGuessing(t *testing.T) {
	f := newFixture(t)
	t.Setenv("AMNEZIA_VERSION", "")

	rec := f.get("/api/versions")
	if rec.Code != http.StatusOK {
		t.Fatalf("код %d, ожидался 200", rec.Code)
	}
	got := decodeAPI(t, rec)
	if got["product"] != "" || got["amneziawg_go"] != "" {
		t.Fatalf("панель выдумала версии там, где их неоткуда взять: %v", got)
	}
	// Схема известна всегда: она в самой базе.
	if got["schema"] != db.SchemaVersion {
		t.Fatalf("schema = %v, ожидалось %q", got["schema"], db.SchemaVersion)
	}
}
