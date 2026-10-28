package db

import (
	"errors"
	"testing"
)

// Приставка прямых ссылок настраивается (решение владельца 28.10.2026):
// база выпуска 2.12 получает «VLESS direct » — так теперь подписаны прямые
// ссылки, — а записанная приставка переживает сохранение настроек.
func TestMigrateAddsXrayLinkPrefix(t *testing.T) {
	handle, _ := openTest(t, "amnezia.sqlite")
	if _, err := handle.Exec(`CREATE TABLE xray (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		private_key TEXT NOT NULL, public_key TEXT NOT NULL, short_id TEXT NOT NULL,
		path TEXT NOT NULL, target TEXT NOT NULL, server_name TEXT NOT NULL,
		link_address TEXT NOT NULL DEFAULT '', link_port INTEGER NOT NULL DEFAULT 443,
		updated_at_utc TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := handle.Exec(`INSERT INTO xray VALUES (1, 'p', 'q', 'ab', '/x', 't', 's', '', 443, '2026-10-27T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := Migrate(handle); err != nil {
			t.Fatal(err)
		}
	}
	s, err := XraySettingsRow(handle)
	if err != nil {
		t.Fatal(err)
	}
	if s.LinkPrefix != XrayDefaultLinkPrefix || XrayDefaultLinkPrefix != "VLESS direct " {
		t.Fatalf("приставка после миграции: %q", s.LinkPrefix)
	}
	s.LinkPrefix = "VPN "
	if err := SaveXraySettings(handle, *s); err != nil {
		t.Fatal(err)
	}
	if got, _ := XraySettingsRow(handle); got.LinkPrefix != "VPN " {
		t.Fatalf("приставка не сохранилась: %q", got.LinkPrefix)
	}
	s.LinkPrefix = "VPN\n"
	if err := SaveXraySettings(handle, *s); err == nil {
		t.Fatal("принята приставка с переводом строки")
	}
}

// Переименование узла сохраняет его параметры и секрет: так узел «ru»
// становится «entry» без перенастройки самого узла.
func TestRenameXrayEntry(t *testing.T) {
	handle, _ := openTest(t, "amnezia.sqlite")
	if err := Migrate(handle); err != nil {
		t.Fatal(err)
	}
	e := XrayEntry{Name: "ru", LinkHost: "ru.example.com", LinkPort: 443, SNI: "ru.example.com",
		PublicKey: "u1D_noKldM-_gg6X9g-5wUPsdBXPUU-M_Zjv9Z4FlhQ", ShortID: "a1b2", Path: "/entry", LinkPrefix: "RU "}
	if err := SaveXrayEntry(handle, e); err != nil {
		t.Fatal(err)
	}
	other := e
	other.Name = "kz"
	if err := SaveXrayEntry(handle, other); err != nil {
		t.Fatal(err)
	}
	const hash = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"
	if err := SetXrayEntryTokenHash(handle, "ru", hash); err != nil {
		t.Fatal(err)
	}
	if err := RenameXrayEntry(handle, "ru", "entry"); err != nil {
		t.Fatal(err)
	}
	got, err := XrayEntryByName(handle, "entry")
	if err != nil {
		t.Fatal(err)
	}
	want := e
	want.Name = "entry"
	if *got != want {
		t.Fatalf("после переименования %+v", *got)
	}
	if _, err := XrayEntryByName(handle, "ru"); !errors.Is(err, ErrXrayEntryNotFound) {
		t.Fatalf("старое имя осталось: %v", err)
	}
	tokens, _ := XrayEntryTokens(handle)
	if len(tokens) != 2 || tokens[0] != (XrayEntryToken{Name: "entry", Hash: hash}) {
		t.Fatalf("секрет после переименования: %+v", tokens)
	}
	if err := RenameXrayEntry(handle, "nope", "x"); !errors.Is(err, ErrXrayEntryNotFound) {
		t.Fatalf("неизвестный узел: %v", err)
	}
	if err := RenameXrayEntry(handle, "entry", "kz"); !errors.Is(err, ErrXrayEntryExists) {
		t.Fatalf("занятое имя: %v", err)
	}
	if err := RenameXrayEntry(handle, "entry", "Bad Name"); err == nil {
		t.Fatal("принято недопустимое имя")
	}
}
