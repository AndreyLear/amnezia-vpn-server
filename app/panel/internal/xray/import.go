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
// остаться те же ключ Reality, shortId, путь и имя в SNI, а у каждого
// клиента — тот же UUID. Каталог ручной настройки (/opt/xray-prod):
//
//	settings.json — всё о сервере: ключи, short_id, path, server_name, dest,
//	                link_host/link_port для ссылок и extra_clients —
//	                служебные учётки (вход «ruentry»), не принадлежащие
//	                клиентам панели
//	users.json    — [{"client_id": 1, "name": "andrey", "uuid": "…",
//	                "enabled": true, "deleted": false}, …]
//
// Каталог первых часов, без settings.json, тоже понимается: keys.txt (вывод
// `xray x25519`), shortid.txt и path.txt. Чего файлы не описывают, остаётся
// прежним, если настройки уже были, иначе — по умолчанию (self-steal на
// домене из endpoint).

// ImportResult — итог импорта.
type ImportResult struct {
	// Imported — сколько клиентов панели получили свой UUID.
	Imported int
	// Missing — client_id из users.json, которых в базе нет.
	Missing []int64
	// Deleted — сколько записей users.json помечены удалёнными и пропущены.
	Deleted int
	// ServiceUsers — сколько служебных учёток перенесено.
	ServiceUsers int
	// Warnings — расхождения, которые импорт не переносит, но о которых
	// надо знать: в ручной настройке было не так, как соберёт панель.
	Warnings []string
}

type importUser struct {
	ClientID int64  `json:"client_id"`
	Name     string `json:"name"`
	UUID     string `json:"uuid"`
	// Enabled не переносится: включён ли клиент, решает панель, и для
	// VLESS так же, как для AWG.
	Enabled *bool `json:"enabled"`
	Deleted bool  `json:"deleted"`
}

// prodSettings — /opt/xray-prod/settings.json.
type prodSettings struct {
	Listen              string `json:"listen"`
	Port                int    `json:"port"`
	AcceptProxyProtocol *bool  `json:"accept_proxy_protocol"`
	Path                string `json:"path"`
	ShortID             string `json:"short_id"`
	PrivateKey          string `json:"private_key"`
	PublicKey           string `json:"public_key"`
	ServerName          string `json:"server_name"`
	Dest                string `json:"dest"`
	LinkHost            string `json:"link_host"`
	LinkPort            int64  `json:"link_port"`
	LinkPrefix          string `json:"link_prefix"`
	ExtraClients        []struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	} `json:"extra_clients"`
}

// Import читает каталог ручной настройки и записывает его в базу одной
// транзакцией. Ошибки не содержат ключей.
func Import(handle *sql.DB, dir string) (ImportResult, error) {
	var res ImportResult
	s := db.XraySettings{Target: db.XrayDefaultTarget, LinkPort: db.XrayDefaultLinkPort}
	switch cur, err := db.XraySettingsRow(handle); {
	case err == nil:
		s = *cur
	case !errors.Is(err, db.ErrXrayNotConfigured):
		return res, err
	}

	var service []db.VLESSServiceUser
	data, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	switch {
	case err == nil:
		if service, err = applyProdSettings(&s, data, &res); err != nil {
			return ImportResult{}, err
		}
	case errors.Is(err, os.ErrNotExist):
		if err := applyLegacyFiles(&s, dir); err != nil {
			return ImportResult{}, err
		}
	default:
		return res, fmt.Errorf("xray import: read settings.json: %w", err)
	}
	if s.ServerName == "" {
		name, err := DefaultServerName(handle)
		if err != nil {
			return ImportResult{}, fmt.Errorf("xray import: %w", err)
		}
		s.ServerName = name
	}

	users, err := readUsers(filepath.Join(dir, "users.json"))
	if err != nil {
		return ImportResult{}, err
	}
	uuids := make(map[int64]string, len(users))
	for _, u := range users {
		if u.Deleted {
			res.Deleted++
			continue
		}
		if _, dup := uuids[u.ClientID]; dup {
			return ImportResult{}, fmt.Errorf("xray import: users.json: client_id %d listed twice", u.ClientID)
		}
		uuids[u.ClientID] = strings.ToLower(u.UUID)
	}
	imported, missing, err := db.ImportVLESS(handle, db.VLESSImport{Settings: s, Clients: uuids, ServiceUsers: service})
	if err != nil {
		return ImportResult{}, fmt.Errorf("xray import: %w", err)
	}
	res.Imported, res.Missing, res.ServiceUsers = imported, missing, len(service)
	return res, nil
}

// linkNamePrefix — начало имени подключения в ссылке («VPN andrey»).
const linkNamePrefix = "VPN "

func applyProdSettings(s *db.XraySettings, data []byte, res *ImportResult) ([]db.VLESSServiceUser, error) {
	var p prodSettings
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("xray import: settings.json: %w", err)
	}
	if p.PrivateKey == "" {
		return nil, errors.New("xray import: settings.json: no private_key")
	}
	pub, err := RealityPublicKey(p.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("xray import: settings.json: %w", err)
	}
	if p.PublicKey != "" && p.PublicKey != pub {
		return nil, errors.New("xray import: settings.json: public_key does not belong to private_key")
	}
	if p.ShortID == "" || p.Path == "" {
		return nil, errors.New("xray import: settings.json: short_id and path are required")
	}
	s.PrivateKey, s.PublicKey = p.PrivateKey, pub
	s.ShortID, s.Path = strings.ToLower(p.ShortID), p.Path
	if p.ServerName != "" {
		s.ServerName = p.ServerName
	}
	if p.Dest != "" {
		s.Target = p.Dest
	}
	if p.LinkHost != "" {
		s.LinkAddress = p.LinkHost
	}
	if p.LinkPort != 0 {
		s.LinkPort = p.LinkPort
	}

	// Вход панель собирает всегда одинаково: 127.0.0.1:4432 за nginx stream
	// с PROXY protocol. Иное в ручной настройке не переносится, но о нём
	// надо сказать — иначе nginx после перехода будет стучаться не туда.
	if p.Listen != "" && p.Listen != inboundListen {
		res.Warnings = append(res.Warnings, fmt.Sprintf("listen %q is not carried over: the panel's xray listens on %s", p.Listen, inboundListen))
	}
	if p.Port != 0 && p.Port != inboundPort {
		res.Warnings = append(res.Warnings, fmt.Sprintf("port %d is not carried over: the panel's xray listens on port %d", p.Port, inboundPort))
	}
	if p.AcceptProxyProtocol != nil && !*p.AcceptProxyProtocol {
		res.Warnings = append(res.Warnings, "accept_proxy_protocol=false is not carried over: the panel's xray expects PROXY protocol from nginx stream")
	}
	if p.LinkPrefix != "" && p.LinkPrefix != linkNamePrefix {
		res.Warnings = append(res.Warnings, fmt.Sprintf("link_prefix %q is not carried over: links are named %q + client name", p.LinkPrefix, linkNamePrefix))
	}

	service := make([]db.VLESSServiceUser, 0, len(p.ExtraClients))
	for _, c := range p.ExtraClients {
		service = append(service, db.VLESSServiceUser{Email: c.Email, UUID: strings.ToLower(c.ID)})
	}
	return service, nil
}

func applyLegacyFiles(s *db.XraySettings, dir string) error {
	priv, pub, err := readKeys(filepath.Join(dir, "keys.txt"))
	if err != nil {
		return err
	}
	shortID, err := readLine(filepath.Join(dir, "shortid.txt"))
	if err != nil {
		return err
	}
	path, err := readLine(filepath.Join(dir, "path.txt"))
	if err != nil {
		return err
	}
	s.PrivateKey, s.PublicKey, s.ShortID, s.Path = priv, pub, strings.ToLower(shortID), path
	return nil
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
