package xray

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

// Импорт ручной настройки с боевого (amnezia-vpn-server-adaw →
// amnezia-vpn-server-14y0.1).
//
// 27.10.2026 Xray поднят на боевом руками, и ссылки уже розданы. Чтобы они
// продолжили работать, когда Xray перейдёт под панель, у сервера должны
// остаться те же ключ Reality, shortId и путь, а у каждого клиента — тот же
// UUID. Каталог ручной настройки:
//
//	users.json  — [{"client_id": 1, "name": "andrey", "uuid": "…"}, …]
//	keys.txt    — вывод `xray x25519`: «PrivateKey: …», «Password (PublicKey): …»
//	shortid.txt — shortId одной строкой
//	path.txt    — путь XHTTP одной строкой
//
// Маскировку (target, serverName) и адрес для ссылок файлы не описывают:
// они остаются прежними, если настройки уже были, иначе — по умолчанию,
// как на боевом.

// ImportResult — итог импорта: сколько клиентов получили свой UUID и чьих
// client_id в базе нет (удалены после раздачи ссылок).
type ImportResult struct {
	Imported int
	Missing  []int64
}

type importUser struct {
	ClientID int64  `json:"client_id"`
	Name     string `json:"name"`
	UUID     string `json:"uuid"`
}

// Import читает каталог ручной настройки и записывает его в базу одной
// транзакцией. Ошибки не содержат ключей.
func Import(handle *sql.DB, dir string) (ImportResult, error) {
	priv, pub, err := readKeys(filepath.Join(dir, "keys.txt"))
	if err != nil {
		return ImportResult{}, err
	}
	shortID, err := readLine(filepath.Join(dir, "shortid.txt"))
	if err != nil {
		return ImportResult{}, err
	}
	path, err := readLine(filepath.Join(dir, "path.txt"))
	if err != nil {
		return ImportResult{}, err
	}
	users, err := readUsers(filepath.Join(dir, "users.json"))
	if err != nil {
		return ImportResult{}, err
	}

	s := db.XraySettings{
		Target:     db.XrayDefaultTarget,
		ServerName: db.XrayDefaultServerName,
		LinkPort:   db.XrayDefaultLinkPort,
	}
	switch cur, err := db.XraySettingsRow(handle); {
	case err == nil:
		s = *cur
	case !errors.Is(err, db.ErrXrayNotConfigured):
		return ImportResult{}, err
	}
	s.PrivateKey, s.PublicKey, s.ShortID, s.Path = priv, pub, strings.ToLower(shortID), path

	uuids := make(map[int64]string, len(users))
	for _, u := range users {
		if _, dup := uuids[u.ClientID]; dup {
			return ImportResult{}, fmt.Errorf("xray import: users.json: client_id %d listed twice", u.ClientID)
		}
		uuids[u.ClientID] = strings.ToLower(u.UUID)
	}
	imported, missing, err := db.ImportVLESS(handle, s, uuids)
	if err != nil {
		return ImportResult{}, fmt.Errorf("xray import: %w", err)
	}
	return ImportResult{Imported: imported, Missing: missing}, nil
}

// readKeys разбирает вывод `xray x25519`. Публичный ключ всегда выводится
// из приватного; если он есть в файле, он обязан совпасть — иначе файл
// собран из разных пар, и выданные ссылки (в них публичный ключ) с этим
// сервером не заработают.
func readKeys(path string) (priv, pub string, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", fmt.Errorf("xray import: read keys.txt: %w", err)
	}
	var filePub string
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		name, value, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		switch {
		case name == "PrivateKey" || name == "Private key":
			priv = value
		case strings.Contains(name, "PublicKey") || name == "Password" || name == "Public key":
			filePub = value
		}
	}
	if priv == "" {
		return "", "", errors.New("xray import: keys.txt: no PrivateKey line")
	}
	pub, err = RealityPublicKey(priv)
	if err != nil {
		return "", "", fmt.Errorf("xray import: keys.txt: %w", err)
	}
	if filePub != "" && filePub != pub {
		return "", "", errors.New("xray import: keys.txt: the public key does not belong to the private key")
	}
	return priv, pub, nil
}

func readLine(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("xray import: read %s: %w", filepath.Base(path), err)
	}
	return strings.TrimSpace(string(data)), nil
}

func readUsers(path string) ([]importUser, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("xray import: read users.json: %w", err)
	}
	var users []importUser
	if err := json.Unmarshal(data, &users); err != nil {
		return nil, fmt.Errorf("xray import: users.json: %w", err)
	}
	for i, u := range users {
		if u.ClientID <= 0 {
			return nil, fmt.Errorf("xray import: users.json: entry %d has no client_id", i+1)
		}
	}
	return users, nil
}
