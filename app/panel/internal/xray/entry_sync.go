package xray

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

// Автосинхронизация узла-входа (amnezia-vpn-server-14y0.6, решение
// владельца 28.10.2026).
//
// Узел-вход раз в минуту забирает у панели свою конфигурацию: кого
// пускать (клиенты с UUID и признаком включённости) и куда отправлять не
// российское — параметры дороги до этого сервера, те же, что в прямой
// ссылке. Каждый клиент уходит с узла сюда под своим UUID, поэтому этот
// сервер сам считает его трафик и для дороги через вход. Ключи Reality
// самого узла живут только на узле: панели они не нужны, а узлу не нужны
// ни имена клиентов, ни ключи AWG — ничего этого в ответе нет.
//
// Доступ — по секрету узла в заголовке Authorization: секрет создаётся
// при `panel xray entry set`, показывается один раз, в базе лежит его
// SHA-256, меняется `panel xray entry token`.

// entryTokenBytes — 256 бит: подбор такого секрета бессмыслен и без
// лимита запросов, лимит лишь бережёт панель от лишней работы.
const entryTokenBytes = 32

// entryTokenPattern — вид секрета, который выдаёт NewEntryToken. Всё
// прочее отвергается до хэширования и сравнения.
var entryTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

// ErrEntryTokenInvalid — секрет не подошёл ни одному узлу. Почему — не
// уточняется: ни вызывающему, ни в журнал.
var ErrEntryTokenInvalid = errors.New("xray: entry token is not valid")

// entryTokenHash — SHA-256 секрета в hex, как он хранится в базе.
func entryTokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// RotateEntryToken выдаёт узлу новый секрет и возвращает его — это
// единственный раз, когда секрет виден. Прежний перестаёт действовать
// сразу. ErrXrayEntryNotFound — узла нет.
func RotateEntryToken(handle *sql.DB, name string) (string, error) {
	raw := make([]byte, entryTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("xray: read random: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	if err := db.SetXrayEntryTokenHash(handle, name, entryTokenHash(token)); err != nil {
		return "", err
	}
	return token, nil
}

// AuthenticateEntry — имя узла, которому принадлежит секрет, или
// ErrEntryTokenInvalid. Хэш сверяется со всеми узлами за постоянное время
// и без досрочного выхода: по времени ответа не узнать ни сколько символов
// совпало, ни у какого по счёту узла.
func AuthenticateEntry(handle db.Querier, token string) (string, error) {
	if !entryTokenPattern.MatchString(token) {
		return "", ErrEntryTokenInvalid
	}
	tokens, err := db.XrayEntryTokens(handle)
	if err != nil {
		return "", err
	}
	got, _ := hex.DecodeString(entryTokenHash(token))
	found := ""
	for _, t := range tokens {
		want, err := hex.DecodeString(t.Hash)
		if err != nil || len(want) != len(got) {
			// У узла нет секрета (или он испорчен): сравнивать не с чем,
			// но время уходит то же — сравнение с самим собой.
			want = make([]byte, len(got))
			subtle.ConstantTimeCompare(got, want)
			continue
		}
		if subtle.ConstantTimeCompare(got, want) == 1 {
			found = t.Name
		}
	}
	if found == "" {
		return "", ErrEntryTokenInvalid
	}
	return found, nil
}

// EntryClient — клиент для узла: его UUID и пускать ли его.
type EntryClient struct {
	ClientID int64  `json:"client_id"`
	UUID     string `json:"uuid"`
	Enabled  bool   `json:"enabled"`
}

// EntryUpstream — дорога с узла на этот сервер: те же адрес, порт и
// Reality, что в прямой ссылке клиента.
type EntryUpstream struct {
	Address    string `json:"address"`
	Port       int64  `json:"port"`
	Path       string `json:"path"`
	ServerName string `json:"server_name"`
	PublicKey  string `json:"public_key"`
	ShortID    string `json:"short_id"`
}

// EntryConfig — ответ панели узлу.
type EntryConfig struct {
	Entry    string        `json:"entry"`
	Clients  []EntryClient `json:"clients"`
	Upstream EntryUpstream `json:"upstream"`
	// TargetVersion и UpdateRequest — самообновление узла
	// (amnezia-vpn-server-q1do.2, см. entry_update.go): только номер
	// выпуска и метка запроса владельца, без адресов и сумм. Пустые не
	// отдаются; entry-sync старше самообновления их не читает.
	TargetVersion string `json:"target_version,omitempty"`
	UpdateRequest string `json:"update_request,omitempty"`
}

// BuildEntryConfig собирает конфигурацию узла name. Включённость — то же
// правило, что для конфигов этого сервера (включён и не истёк): отключение
// в панели закрывает клиенту и вход. ErrXrayNotConfigured или
// ErrNoLinkAddress — дороги на этот сервер нет, и узлу строить нечего.
func BuildEntryConfig(handle db.Querier, name string) (*EntryConfig, error) {
	if _, err := db.XrayEntryByName(handle, name); err != nil {
		return nil, err
	}
	s, err := db.XraySettingsRow(handle)
	if err != nil {
		return nil, err
	}
	address, err := LinkAddress(handle, s)
	if err != nil {
		return nil, err
	}
	users, err := db.VLESSUsersExport(handle)
	if err != nil {
		return nil, err
	}
	out := &EntryConfig{
		Entry:   name,
		Clients: make([]EntryClient, 0, len(users)),
		Upstream: EntryUpstream{
			Address: address, Port: s.LinkPort, Path: s.Path,
			ServerName: s.ServerName, PublicKey: s.PublicKey, ShortID: s.ShortID,
		},
	}
	for _, u := range users {
		out.Clients = append(out.Clients, EntryClient{ClientID: u.ClientID, UUID: u.UUID, Enabled: u.Enabled})
	}
	return out, nil
}
