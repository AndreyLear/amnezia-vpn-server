package db

import (
	"errors"
	"testing"
	"time"
)

// Запрос владельца на обновление каскада (amnezia-vpn-server-q1do.2): база
// 3.3.0 колонки не знает — Migrate её добавляет; запрос — время нажатия,
// новый заменяет прежний, отчёт и время синка он не трогает.
func TestXrayEntryUpdateRequest(t *testing.T) {
	handle, _ := openTest(t, "amnezia.sqlite")
	if _, err := handle.Exec(`CREATE TABLE xray_entries (
		name TEXT PRIMARY KEY,
		link_host TEXT NOT NULL,
		link_port INTEGER NOT NULL DEFAULT 443,
		sni TEXT NOT NULL,
		public_key TEXT NOT NULL,
		short_id TEXT NOT NULL,
		path TEXT NOT NULL,
		link_prefix TEXT NOT NULL DEFAULT '',
		updated_at_utc TEXT NOT NULL,
		token_hash TEXT NOT NULL DEFAULT '',
		last_sync_utc TEXT NOT NULL DEFAULT '',
		report_json TEXT NOT NULL DEFAULT '',
		report_utc TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := handle.Exec(`INSERT INTO xray_entries VALUES
		('ru', 'super-space.ru', 443, 'super-space.ru', 'WQcVePR5eFDpkuq8PefEalET53I_3Zb62YjqMLuHMx4', 'a50c249d61c252d0', '/r5', 'RU ', '2026-10-27T00:00:00Z', '', '2026-10-28T11:59:00Z', '{"release":"3.3.0"}', '2026-10-28T11:59:00Z')`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := Migrate(handle); err != nil {
			t.Fatalf("Migrate #%d: %v", i+1, err)
		}
	}
	if got, err := XrayEntryUpdateRequest(handle, "ru"); err != nil || got != "" {
		t.Fatalf("после миграции запроса нет: %q, %v", got, err)
	}
	t0 := time.Date(2026, 10, 28, 12, 0, 0, 0, time.FixedZone("MSK", 3*3600))
	if err := RequestXrayEntryUpdate(handle, "ru", t0); err != nil {
		t.Fatal(err)
	}
	if got, err := XrayEntryUpdateRequest(handle, "ru"); err != nil || got != "2026-10-28T09:00:00Z" {
		t.Fatalf("запрос: %q, %v", got, err)
	}
	reports, err := XrayEntryReports(handle)
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 1 || reports[0].UpdateRequest != "2026-10-28T09:00:00Z" ||
		reports[0].Report != `{"release":"3.3.0"}` || reports[0].LastSync.IsZero() {
		t.Fatalf("отчёты: %+v", reports)
	}
	if err := RequestXrayEntryUpdate(handle, "nope", t0); !errors.Is(err, ErrXrayEntryNotFound) {
		t.Fatalf("нет узла: %v", err)
	}
	if _, err := XrayEntryUpdateRequest(handle, "nope"); !errors.Is(err, ErrXrayEntryNotFound) {
		t.Fatalf("нет узла: %v", err)
	}
}
