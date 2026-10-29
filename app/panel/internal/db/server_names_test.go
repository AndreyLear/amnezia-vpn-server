package db

import (
	"errors"
	"strings"
	"testing"
)

// Имена серверов, которые задаёт владелец (amnezia-vpn-server-q1do.4):
// у основного — строка настроек, у каскада — отдельная колонка рядом с
// техническим именем узла. Пустое имя — подпись по умолчанию.

func TestNormalizeDisplayName(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		err      error
	}{
		{"", "", nil},
		{"   ", "", nil},
		{"  Москва  ", "Москва", nil},
		{"Дом <b>1</b>", "Дом <b>1</b>", nil},
		{strings.Repeat("я", 32), strings.Repeat("я", 32), nil},
		{" " + strings.Repeat("я", 32) + " ", strings.Repeat("я", 32), nil},
		{strings.Repeat("я", 33), "", ErrDisplayNameTooLong},
		{"а\nб", "", ErrDisplayNameInvalid},
		{"а\tб", "", ErrDisplayNameInvalid},
		{"а‮б", "", ErrDisplayNameInvalid},
		{"\xff", "", ErrDisplayNameInvalid},
	} {
		got, err := NormalizeDisplayName(tc.in)
		if !errors.Is(err, tc.err) || got != tc.want {
			t.Errorf("NormalizeDisplayName(%q) = %q, %v; ждали %q, %v", tc.in, got, err, tc.want, tc.err)
		}
	}
}

func TestServerName(t *testing.T) {
	handle, _ := openTest(t, "amnezia.sqlite")
	if err := Migrate(handle); err != nil {
		t.Fatal(err)
	}
	if got, err := ServerName(handle); err != nil || got != "" {
		t.Fatalf("по умолчанию имени нет: %q, %v", got, err)
	}
	old, err := SetServerName(handle, "  Основной  ")
	if err != nil || old != "" {
		t.Fatalf("первое имя: прежнее %q, %v", old, err)
	}
	if got, _ := ServerName(handle); got != "Основной" {
		t.Fatalf("имя: %q", got)
	}
	if _, err := SetServerName(handle, strings.Repeat("x", 33)); !errors.Is(err, ErrDisplayNameTooLong) {
		t.Fatalf("длинное имя: %v", err)
	}
	if got, _ := ServerName(handle); got != "Основной" {
		t.Fatalf("отказ изменил имя: %q", got)
	}
	old, err = SetServerName(handle, " ")
	if err != nil || old != "Основной" {
		t.Fatalf("сброс: прежнее %q, %v", old, err)
	}
	if got, _ := ServerName(handle); got != "" {
		t.Fatalf("после сброса: %q", got)
	}
}

func TestXrayEntryTitle(t *testing.T) {
	handle, _ := openTest(t, "amnezia.sqlite")
	// База 3.3.0: колонки имени нет, Migrate её добавляет.
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
		report_utc TEXT NOT NULL DEFAULT '',
		update_request_utc TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		t.Fatal(err)
	}
	const hash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if _, err := handle.Exec(`INSERT INTO xray_entries VALUES
		('de-1', 'a.example.com', 443, 'a.example.com', 'WQcVePR5eFDpkuq8PefEalET53I_3Zb62YjqMLuHMx4', 'a1b2', '/a', 'A ', '2026-10-27T00:00:00Z', ?, '2026-10-28T11:59:00Z', '{"release":"3.3.0"}', '2026-10-28T11:59:00Z', ''),
		('entry', 'b.example.com', 443, 'b.example.com', 'WQcVePR5eFDpkuq8PefEalET53I_3Zb62YjqMLuHMx4', 'a1b2', '/b', 'B ', '2026-10-27T00:00:00Z', '', '', '', '', '')`, hash); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := Migrate(handle); err != nil {
			t.Fatalf("Migrate #%d: %v", i+1, err)
		}
	}
	title := func(name string) string {
		t.Helper()
		reports, err := XrayEntryReports(handle)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range reports {
			if r.Name == name {
				return r.Title
			}
		}
		t.Fatalf("нет узла %s", name)
		return ""
	}
	if got := title("de-1"); got != "" {
		t.Fatalf("после миграции имени нет: %q", got)
	}

	old, err := SetXrayEntryTitle(handle, "de-1", "  Москва  ")
	if err != nil || old != "" {
		t.Fatalf("имя: прежнее %q, %v", old, err)
	}
	if got := title("de-1"); got != "Москва" {
		t.Fatalf("имя: %q", got)
	}
	entries, err := XrayEntries(handle)
	if err != nil || len(entries) != 2 || entries[0].Title != "Москва" || entries[1].Title != "" {
		t.Fatalf("XrayEntries: %+v, %v", entries, err)
	}

	// Имя узла, секрет, синк и отчёт — те же: узел ничего не замечает.
	var gotHash, gotSync, gotReport string
	if err := handle.QueryRow(`SELECT token_hash, last_sync_utc, report_json FROM xray_entries WHERE name = 'de-1'`).
		Scan(&gotHash, &gotSync, &gotReport); err != nil {
		t.Fatal(err)
	}
	if gotHash != hash || gotSync != "2026-10-28T11:59:00Z" || gotReport != `{"release":"3.3.0"}` {
		t.Fatalf("имя задело синк: %q %q %q", gotHash, gotSync, gotReport)
	}

	// Уникальность без учёта регистра, в том числе для кириллицы, и с
	// подписью по умолчанию другого узла.
	for _, taken := range []string{"москва", "МОСКВА", " Москва "} {
		if _, err := SetXrayEntryTitle(handle, "entry", taken); !errors.Is(err, ErrXrayEntryTitleTaken) {
			t.Fatalf("%q: %v, ждали ErrXrayEntryTitleTaken", taken, err)
		}
	}
	if _, err := SetXrayEntryTitle(handle, "de-1", "каскад ENTRY"); !errors.Is(err, ErrXrayEntryTitleTaken) {
		t.Fatalf("подпись другого узла по умолчанию: %v", err)
	}
	// Своё же имя в другом регистре — не занято.
	if _, err := SetXrayEntryTitle(handle, "de-1", "МОСКВА"); err != nil {
		t.Fatalf("своё имя в другом регистре: %v", err)
	}
	if _, err := SetXrayEntryTitle(handle, "entry", strings.Repeat("ж", 33)); !errors.Is(err, ErrDisplayNameTooLong) {
		t.Fatalf("длинное: %v", err)
	}
	if _, err := SetXrayEntryTitle(handle, "nope", "Где-то"); !errors.Is(err, ErrXrayEntryNotFound) {
		t.Fatalf("нет узла: %v", err)
	}

	// Пустое имя — сброс к подписи по умолчанию; два узла без имени не
	// мешают друг другу.
	old, err = SetXrayEntryTitle(handle, "de-1", "")
	if err != nil || old != "МОСКВА" {
		t.Fatalf("сброс: прежнее %q, %v", old, err)
	}
	if got := title("de-1"); got != "" {
		t.Fatalf("после сброса: %q", got)
	}

	// Технический rename из CLI уносит имя с собой.
	if _, err := SetXrayEntryTitle(handle, "entry", "Дача"); err != nil {
		t.Fatal(err)
	}
	if err := RenameXrayEntry(handle, "entry", "dacha"); err != nil {
		t.Fatal(err)
	}
	if got := title("dacha"); got != "Дача" {
		t.Fatalf("после rename: %q", got)
	}
	// И повторный `xray entry set` имя не стирает.
	e, err := XrayEntryByName(handle, "dacha")
	if err != nil {
		t.Fatal(err)
	}
	e.LinkPort = 8443
	if err := SaveXrayEntry(handle, *e); err != nil {
		t.Fatal(err)
	}
	if got := title("dacha"); got != "Дача" {
		t.Fatalf("после set: %q", got)
	}
}
