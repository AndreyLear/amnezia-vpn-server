package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Имена серверов в панели (amnezia-vpn-server-q1do.4, просьба владельца
// 29.10.2026: «чтобы пользователю было понятно, а не мы за него решили»).
//
// Имя — только подпись для человека. У основного сервера оно лежит в
// settings (server_name), у каскада — в xray_entries.title рядом с
// техническим именем узла. Техническое имя остаётся ключом: на нём
// держатся ручки /api/entries/{name}, QR маршрута (?entry=), команды CLI
// и журнал, и оно ограничено [a-z0-9-] — «Москва» или «Дача 2» туда не
// влезают. Отдельная подпись не трогает ни секрет узла, ни синк, ни
// отчёт: узел про неё не знает.
//
// Пустое имя — подпись по умолчанию, как было до этой возможности.

// DisplayNameMaxRunes — длина имени в символах: шапка на телефоне длиннее
// не вместит, а различать серверы хватает и этого.
const DisplayNameMaxRunes = 32

var (
	// ErrDisplayNameTooLong — имя длиннее DisplayNameMaxRunes.
	ErrDisplayNameTooLong = errors.New("db: display name is too long")
	// ErrDisplayNameInvalid — в имени управляющие символы или не UTF-8.
	ErrDisplayNameInvalid = errors.New("db: display name has control characters")
	// ErrXrayEntryTitleTaken — такое имя (без учёта регистра) уже у
	// другого каскада.
	ErrXrayEntryTitleTaken = errors.New("db: entry title is taken")
)

// serverNameKey — строка settings с именем основного сервера.
const serverNameKey = "server_name"

// NormalizeDisplayName обрезает пробелы по краям и проверяет имя. Разметка
// не экранируется и не запрещается: панель выводит имя текстом, и «<b>»
// в нём — просто символы. Запрещены управляющие символы и знаки смены
// направления текста: первые ломают строку шапки, вторые переворачивают
// соседний текст и позволяют выдать одно имя за другое.
func NormalizeDisplayName(raw string) (string, error) {
	if !utf8.ValidString(raw) {
		return "", ErrDisplayNameInvalid
	}
	name := strings.TrimSpace(raw)
	if utf8.RuneCountInString(name) > DisplayNameMaxRunes {
		return "", ErrDisplayNameTooLong
	}
	if strings.IndexFunc(name, func(r rune) bool { return unicode.IsControl(r) || bidiControl(r) }) >= 0 {
		return "", ErrDisplayNameInvalid
	}
	return name, nil
}

func bidiControl(r rune) bool {
	return r == '؜' || r == '‎' || r == '‏' ||
		(r >= '‪' && r <= '‮') || (r >= '⁦' && r <= '⁩')
}

// ServerName — имя основного сервера; пусто — не задано.
func ServerName(handle Querier) (string, error) {
	v, _, err := GetSetting(handle, serverNameKey)
	return v, err
}

// SetServerName задаёт имя основного сервера и возвращает прежнее (для
// журнала). Пустое после обрезки — сброс.
func SetServerName(handle *sql.DB, raw string) (old string, err error) {
	name, err := NormalizeDisplayName(raw)
	if err != nil {
		return "", err
	}
	tx, err := handle.Begin()
	if err != nil {
		return "", fmt.Errorf("db: begin server name: %w", err)
	}
	defer tx.Rollback()
	if old, _, err = GetSetting(tx, serverNameKey); err != nil {
		return "", err
	}
	if name == "" {
		if _, err := tx.Exec(`DELETE FROM settings WHERE key = ?`, serverNameKey); err != nil {
			return "", fmt.Errorf("db: clear server name: %w", err)
		}
	} else if err := setSettingTx(tx, serverNameKey, name); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("db: commit server name: %w", err)
	}
	return old, nil
}

// xrayEntryDefaultLabel — подпись каскада без имени, когда узлов
// несколько (её же собирает панель). Имя другого каскада не должно с ней
// совпасть: в шапке встали бы две одинаковые строки.
func xrayEntryDefaultLabel(name string) string {
	return "Каскад " + name
}

// SetXrayEntryTitle задаёт имя каскада name и возвращает прежнее (для
// журнала). Пустое после обрезки — сброс к подписи по умолчанию. Имя
// уникально среди каскадов без учёта регистра.
// ErrXrayEntryNotFound — узла нет; ErrXrayEntryTitleTaken — имя занято.
func SetXrayEntryTitle(handle *sql.DB, name, raw string) (old string, err error) {
	title, err := NormalizeDisplayName(raw)
	if err != nil {
		return "", err
	}
	tx, err := handle.Begin()
	if err != nil {
		return "", fmt.Errorf("db: begin entry title: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT name, title FROM xray_entries`)
	if err != nil {
		return "", fmt.Errorf("db: query entry titles: %w", err)
	}
	found, taken := false, false
	for rows.Next() {
		var n, t string
		if err := rows.Scan(&n, &t); err != nil {
			rows.Close()
			return "", fmt.Errorf("db: scan entry title: %w", err)
		}
		if n == name {
			found, old = true, t
			continue
		}
		// strings.EqualFold сравнивает и кириллицу; SQLite lower() знает
		// только ASCII.
		if title != "" && (strings.EqualFold(t, title) || strings.EqualFold(xrayEntryDefaultLabel(n), title)) {
			taken = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return "", fmt.Errorf("db: query entry titles: %w", err)
	}
	rows.Close()
	switch {
	case !found:
		return "", ErrXrayEntryNotFound
	case taken:
		return "", ErrXrayEntryTitleTaken
	}
	// updated_at_utc не трогается: это время параметров ссылки, а имя в
	// ссылку не попадает.
	if _, err := tx.Exec(`UPDATE xray_entries SET title = ? WHERE name = ?`, title, name); err != nil {
		return "", fmt.Errorf("db: write entry title: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("db: commit entry title: %w", err)
	}
	return old, nil
}

// migrateXrayEntryTitle добавляет колонку имени в таблицу узлов выпуска
// 3.3.0. Аддитивно, без подъёма SchemaVersion, как update_request_utc:
// прежняя панель, вернувшаяся откатом, колонку просто не читает.
func migrateXrayEntryTitle(handle *sql.DB) error {
	has, err := tableHasColumn(handle, "xray_entries", "title")
	if err != nil || has {
		return err
	}
	if _, err := handle.Exec(`ALTER TABLE xray_entries ADD COLUMN title TEXT NOT NULL DEFAULT ''`); err != nil {
		return fmt.Errorf("db: add xray_entries.title: %w", err)
	}
	return nil
}
