package db

import (
	"errors"
	"testing"
	"time"
)

// База выпуска 2.11/2.12: таблица узлов без секрета. Migrate добавляет
// колонку, узел остаётся на месте и без секрета, а секрет потом задаётся
// и не трогается правкой остальных полей (amnezia-vpn-server-14y0.6).
func TestMigrateAddsEntryTokenColumn(t *testing.T) {
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
		updated_at_utc TEXT NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := handle.Exec(`INSERT INTO xray_entries VALUES
		('ru', 'super-space.ru', 443, 'super-space.ru', 'WQcVePR5eFDpkuq8PefEalET53I_3Zb62YjqMLuHMx4', 'a50c249d61c252d0', '/r5', 'RU ', '2026-10-27T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := Migrate(handle); err != nil {
			t.Fatalf("Migrate #%d: %v", i+1, err)
		}
	}
	tokens, err := XrayEntryTokens(handle)
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 1 || tokens[0] != (XrayEntryToken{Name: "ru"}) {
		t.Fatalf("после миграции: %+v", tokens)
	}

	const hash = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"
	if err := SetXrayEntryTokenHash(handle, "ru", hash); err != nil {
		t.Fatal(err)
	}
	e, err := XrayEntryByName(handle, "ru")
	if err != nil {
		t.Fatal(err)
	}
	e.LinkPrefix = "RU2 "
	if err := SaveXrayEntry(handle, *e); err != nil {
		t.Fatal(err)
	}
	if tokens, _ = XrayEntryTokens(handle); tokens[0].Hash != hash {
		t.Fatalf("правка узла стёрла секрет: %+v", tokens)
	}
	for _, bad := range []string{"", "abc", hash + "0", "9F86D081884C7D659A2FEAA0C55AD015A3BF4F1B2B0B822CD15D6C15B0F00A08"} {
		if err := SetXrayEntryTokenHash(handle, "ru", bad); err == nil {
			t.Errorf("принят хэш %q", bad)
		}
	}
	if err := SetXrayEntryTokenHash(handle, "nope", hash); !errors.Is(err, ErrXrayEntryNotFound) {
		t.Fatalf("секрет неизвестному узлу: %v", err)
	}
}

// Окно «О версиях» показывает, когда узел последний раз забирал конфиг
// (amnezia-vpn-server-14y0.16). База 3.0.0 колонки не знает — Migrate её
// добавляет; время пишется не чаще раза в entrySyncResolution, чтобы
// повторы узла подряд не писали в базу каждый раз.
func TestXrayEntrySyncTime(t *testing.T) {
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
		token_hash TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := handle.Exec(`INSERT INTO xray_entries VALUES
		('ru', 'super-space.ru', 443, 'super-space.ru', 'WQcVePR5eFDpkuq8PefEalET53I_3Zb62YjqMLuHMx4', 'a50c249d61c252d0', '/r5', 'RU ', '2026-10-27T00:00:00Z', ''),
		('de', 'de.example.com', 443, 'de.example.com', 'WQcVePR5eFDpkuq8PefEalET53I_3Zb62YjqMLuHMx4', 'a50c', '/d', '', '2026-10-27T00:00:00Z', '')`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := Migrate(handle); err != nil {
			t.Fatalf("Migrate #%d: %v", i+1, err)
		}
	}
	syncs, err := XrayEntrySyncs(handle)
	if err != nil {
		t.Fatal(err)
	}
	if len(syncs) != 2 || syncs[0] != (XrayEntrySync{Name: "de"}) || syncs[1] != (XrayEntrySync{Name: "ru"}) {
		t.Fatalf("до первого запроса: %+v", syncs)
	}

	t0 := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	last := func(name string) time.Time {
		t.Helper()
		syncs, err := XrayEntrySyncs(handle)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range syncs {
			if s.Name == name {
				return s.LastSync
			}
		}
		t.Fatalf("нет узла %s", name)
		return time.Time{}
	}
	if err := TouchXrayEntrySync(handle, "ru", t0); err != nil {
		t.Fatal(err)
	}
	if got := last("ru"); !got.Equal(t0) {
		t.Fatalf("время синка = %v, want %v", got, t0)
	}
	if got := last("de"); !got.IsZero() {
		t.Fatalf("запрос ru записал время de: %v", got)
	}
	// Повтор в пределах entrySyncResolution базу не трогает.
	if err := TouchXrayEntrySync(handle, "ru", t0.Add(entrySyncResolution-time.Second)); err != nil {
		t.Fatal(err)
	}
	if got := last("ru"); !got.Equal(t0) {
		t.Fatalf("повтор через %v переписал время: %v", entrySyncResolution-time.Second, got)
	}
	t1 := t0.Add(entrySyncResolution)
	if err := TouchXrayEntrySync(handle, "ru", t1); err != nil {
		t.Fatal(err)
	}
	if got := last("ru"); !got.Equal(t1) {
		t.Fatalf("синк через %v не записан: %v", entrySyncResolution, got)
	}
	// Часы сервера ушли назад: запись из «будущего» не должна замораживать
	// время, пока часы её не догонят.
	back := t0.Add(-time.Hour)
	if err := TouchXrayEntrySync(handle, "ru", back); err != nil {
		t.Fatal(err)
	}
	if got := last("ru"); !got.Equal(back) {
		t.Fatalf("после отката часов время = %v, want %v", got, back)
	}
	// Переименование узла время сохраняет.
	if err := RenameXrayEntry(handle, "ru", "entry"); err != nil {
		t.Fatal(err)
	}
	if got := last("entry"); !got.Equal(back) {
		t.Fatalf("после переименования время = %v", got)
	}
	// Неизвестный узел — не ошибка: узел могли удалить между проверкой
	// секрета и записью.
	if err := TouchXrayEntrySync(handle, "nope", t0); err != nil {
		t.Fatalf("неизвестный узел: %v", err)
	}
}
