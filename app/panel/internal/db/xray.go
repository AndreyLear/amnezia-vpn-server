package db

import (
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	sqlite "modernc.org/sqlite"
)

// VLESS рядом с AWG (эпик amnezia-vpn-server-14y0, решение владельца Q33).
//
// На мобильных сетях РФ из всего проверенного живёт только
// VLESS+XHTTP+Reality на TCP 443 (эксперимент 8b37). Клиент панели — один
// человек с несколькими способами подключения, а не два разных клиента:
// одна запись, одна база, один бэкап, и отключение или удаление действует
// на оба протокола сразу. Поэтому VLESS здесь — колонка clients.vless_uuid,
// а включённость — тот же clients.enabled, что и для AWG.
//
// Настройки самого Xray (ключи Reality, shortId, путь XHTTP, маскировка) —
// одна строка таблицы xray, как server для AWG. Нет строки — Xray на этом
// сервере не настроен, и конфиг для него не пишется вовсе
// (amnezia-vpn-server-14y0.1).
const xrayTable = `CREATE TABLE IF NOT EXISTS xray (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		private_key TEXT NOT NULL,
		public_key TEXT NOT NULL,
		short_id TEXT NOT NULL,
		path TEXT NOT NULL,
		target TEXT NOT NULL,
		server_name TEXT NOT NULL,
		link_address TEXT NOT NULL DEFAULT '',
		link_port INTEGER NOT NULL DEFAULT 443,
		updated_at_utc TEXT NOT NULL
	);`

// xrayServiceUsersTable — служебные учётки VLESS, не принадлежащие ни
// одному клиенту панели. Первая такая — «ruentry»: российский вход
// (62.109.1.44) ходит на этот сервер как обычный VLESS-клиент и пускает
// через себя всех клиентов панели (решение владельца 27.10.2026). Отключать
// её вместе с кем-то из клиентов нельзя, поэтому она всегда включена и
// живёт отдельно от clients. Переносится импортом ручной настройки
// (extra_clients в settings.json), как и всё остальное — в бэкапе.
const xrayServiceUsersTable = `CREATE TABLE IF NOT EXISTS xray_service_users (
		email TEXT PRIMARY KEY,
		uuid TEXT NOT NULL UNIQUE
	);`

// Маскировка Reality по умолчанию — self-steal: serverName — собственный
// домен сервера, а чужие подключения уходят на локальный nginx с настоящим
// сертификатом этого домена. Так переведён боевой 27.10.2026: проба без
// ключа видит обычный сайт на том же имени, что и в SNI клиентов, вместо
// подставного www.microsoft.com, у которого чужой адрес. Имени по
// умолчанию нет — это домен конкретного сервера.
const (
	XrayDefaultTarget   = "127.0.0.1:4433"
	XrayDefaultLinkPort = 443
)

// ErrXrayNotConfigured — строки xray нет: ключей Reality на сервере ещё не
// заводили. Это нормальное состояние, а не авария: стек обязан подниматься
// и без Xray.
var ErrXrayNotConfigured = errors.New("db: xray is not configured")

// ErrVLESSUUIDTaken — такой UUID уже выдан другому клиенту. Два клиента с
// одним UUID неразличимы для Xray: отключение одного молча оставило бы
// доступ второму.
var ErrVLESSUUIDTaken = errors.New("db: this VLESS UUID belongs to another client")

// XraySettings — строка таблицы xray. PrivateKey — секрет сервера, как
// приватный ключ AWG: в списки, журнал и ошибки он не попадает.
type XraySettings struct {
	PrivateKey string
	PublicKey  string
	ShortID    string
	Path       string
	// Target — куда Reality отдаёт чужие подключения (dest в старых
	// версиях Xray); ServerName — имя, которое клиент шлёт в SNI.
	Target     string
	ServerName string
	// LinkAddress — адрес сервера в ссылках клиентов. Пусто — берётся
	// хост из endpoint, тот же, что у AWG.
	LinkAddress string
	LinkPort    int64
}

// XraySettingsRow читает настройки Xray; ErrXrayNotConfigured, если строки
// нет.
func XraySettingsRow(handle Querier) (*XraySettings, error) {
	var s XraySettings
	err := handle.QueryRow(
		`SELECT private_key, public_key, short_id, path, target, server_name, link_address, link_port
		   FROM xray WHERE id = 1`,
	).Scan(&s.PrivateKey, &s.PublicKey, &s.ShortID, &s.Path, &s.Target, &s.ServerName, &s.LinkAddress, &s.LinkPort)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrXrayNotConfigured
	}
	if err != nil {
		return nil, fmt.Errorf("db: read xray settings: %w", err)
	}
	return &s, nil
}

var (
	shortIDPattern = regexp.MustCompile(`^(?:[0-9a-f]{2}){0,8}$`)
	// Путь XHTTP уходит и в конфиг Xray, и в ссылку: пробелы, кавычки и
	// управляющие символы там не нужны никому.
	xhttpPathPattern = regexp.MustCompile(`^/[A-Za-z0-9._~/-]*$`)
)

// SaveXraySettings записывает настройки целиком. Формат ключей проверяет
// вызывающий (internal/xray): здесь — только то, без чего конфиг Xray не
// соберётся.
func SaveXraySettings(handle *sql.DB, s XraySettings) error {
	if err := validateXraySettings(s); err != nil {
		return err
	}
	return saveXraySettings(handle, s)
}

func validateXraySettings(s XraySettings) error {
	switch {
	case s.PrivateKey == "" || s.PublicKey == "":
		return errors.New("db: xray settings: empty reality key")
	case !shortIDPattern.MatchString(s.ShortID):
		return fmt.Errorf("db: xray settings: short id must be 0–16 lowercase hex digits, even length")
	case !xhttpPathPattern.MatchString(s.Path):
		return fmt.Errorf("db: xray settings: path %q must start with / and contain only URL-safe characters", s.Path)
	case s.Target == "" || s.ServerName == "":
		return errors.New("db: xray settings: empty target or server name")
	case s.LinkPort < 1 || s.LinkPort > 65535:
		return fmt.Errorf("db: xray settings: invalid link port %d", s.LinkPort)
	}
	return nil
}

type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func saveXraySettings(handle execer, s XraySettings) error {
	if _, err := handle.Exec(
		`INSERT INTO xray (id, private_key, public_key, short_id, path, target, server_name, link_address, link_port, updated_at_utc)
		 VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (id) DO UPDATE SET
			private_key = excluded.private_key, public_key = excluded.public_key,
			short_id = excluded.short_id, path = excluded.path, target = excluded.target,
			server_name = excluded.server_name, link_address = excluded.link_address,
			link_port = excluded.link_port, updated_at_utc = excluded.updated_at_utc`,
		s.PrivateKey, s.PublicKey, s.ShortID, s.Path, s.Target, s.ServerName, s.LinkAddress, s.LinkPort,
		time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		return fmt.Errorf("db: write xray settings: %w", err)
	}
	return nil
}

// VLESSServiceUser — служебная учётка VLESS (см. xrayServiceUsersTable).
type VLESSServiceUser struct {
	Email string
	UUID  string
}

// Email служебной учётки — то, по чему API Xray её снимает и добавляет,
// и то, что видно в журнале доступа. Вид «c<число>» занят клиентами панели.
var (
	serviceEmailPattern = regexp.MustCompile(`^[A-Za-z0-9._@-]{1,64}$`)
	clientEmailPattern  = regexp.MustCompile(`^c[0-9]+$`)
)

// XrayServiceUsers — служебные учётки, по email.
func XrayServiceUsers(handle Querier) ([]VLESSServiceUser, error) {
	rows, err := handle.Query(`SELECT email, uuid FROM xray_service_users ORDER BY email`)
	if err != nil {
		return nil, fmt.Errorf("db: query xray service users: %w", err)
	}
	defer rows.Close()
	var out []VLESSServiceUser
	for rows.Next() {
		var u VLESSServiceUser
		if err := rows.Scan(&u.Email, &u.UUID); err != nil {
			return nil, fmt.Errorf("db: scan xray service user: %w", err)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// VLESSImport — перенос ручной настройки Xray: настройки сервера, UUID
// клиентов (client_id → UUID) и служебные учётки, которые заменяют
// прежние целиком.
type VLESSImport struct {
	Settings     XraySettings
	Clients      map[int64]string
	ServiceUsers []VLESSServiceUser
}

// ImportVLESS записывает перенос одной транзакцией: он либо проходит
// целиком, либо не меняет ничего — половина выданных ссылок, переставшая
// работать, хуже, чем ни одной перенесённой. Клиенты, которых в базе нет,
// пропускаются и возвращаются в missing (amnezia-vpn-server-14y0.1).
func ImportVLESS(handle *sql.DB, in VLESSImport) (imported int, missing []int64, err error) {
	if err := validateXraySettings(in.Settings); err != nil {
		return 0, nil, err
	}
	seen := make(map[string]string, len(in.Clients)+len(in.ServiceUsers))
	for id, u := range in.Clients {
		if !ValidVLESSUUID(u) {
			return 0, nil, fmt.Errorf("db: client %d: invalid VLESS UUID", id)
		}
		who := fmt.Sprintf("client %d", id)
		if other, dup := seen[u]; dup {
			return 0, nil, fmt.Errorf("db: %s and %s share one VLESS UUID", other, who)
		}
		seen[u] = who
	}
	emails := map[string]bool{}
	for _, su := range in.ServiceUsers {
		switch {
		case !serviceEmailPattern.MatchString(su.Email):
			return 0, nil, fmt.Errorf("db: service user email %q: only letters, digits and ._@- allowed", su.Email)
		case clientEmailPattern.MatchString(su.Email):
			return 0, nil, fmt.Errorf("db: service user email %q looks like a panel client's", su.Email)
		case emails[su.Email]:
			return 0, nil, fmt.Errorf("db: service user %q listed twice", su.Email)
		case !ValidVLESSUUID(su.UUID):
			return 0, nil, fmt.Errorf("db: service user %q: invalid VLESS UUID", su.Email)
		}
		emails[su.Email] = true
		who := "service user " + su.Email
		if other, dup := seen[su.UUID]; dup {
			return 0, nil, fmt.Errorf("db: %s and %s share one VLESS UUID", other, who)
		}
		seen[su.UUID] = who
	}
	tx, err := handle.Begin()
	if err != nil {
		return 0, nil, fmt.Errorf("db: begin vless import: %w", err)
	}
	defer tx.Rollback()

	var present []int64
	for id := range in.Clients {
		var n int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM clients WHERE id = ?`, id).Scan(&n); err != nil {
			return 0, nil, fmt.Errorf("db: check client %d: %w", id, err)
		}
		if n == 0 {
			missing = append(missing, id)
			continue
		}
		present = append(present, id)
	}
	// Сначала освободить, потом раздать: иначе обмен UUID между двумя
	// клиентами упёрся бы в уникальный индекс на полпути.
	for _, id := range present {
		if _, err := tx.Exec(`UPDATE clients SET vless_uuid = '' WHERE id = ?`, id); err != nil {
			return 0, nil, fmt.Errorf("db: release vless uuid of client %d: %w", id, err)
		}
	}
	now := stamp()
	for _, id := range present {
		if _, err := tx.Exec(`UPDATE clients SET vless_uuid = ?, updated_at = ? WHERE id = ?`, in.Clients[id], now, id); err != nil {
			var se *sqlite.Error
			if errors.As(err, &se) && strings.Contains(err.Error(), "vless_uuid") {
				return 0, nil, fmt.Errorf("client %d: %w", id, ErrVLESSUUIDTaken)
			}
			return 0, nil, fmt.Errorf("db: assign vless uuid to client %d: %w", id, err)
		}
	}
	if _, err := tx.Exec(`DELETE FROM xray_service_users`); err != nil {
		return 0, nil, fmt.Errorf("db: clear xray service users: %w", err)
	}
	for _, su := range in.ServiceUsers {
		var n int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM clients WHERE vless_uuid = ?`, su.UUID).Scan(&n); err != nil {
			return 0, nil, fmt.Errorf("db: check service user uuid: %w", err)
		}
		if n > 0 {
			return 0, nil, fmt.Errorf("service user %s: %w", su.Email, ErrVLESSUUIDTaken)
		}
		if _, err := tx.Exec(`INSERT INTO xray_service_users (email, uuid) VALUES (?, ?)`, su.Email, su.UUID); err != nil {
			return 0, nil, fmt.Errorf("db: insert xray service user: %w", err)
		}
	}
	if err := saveXraySettings(tx, in.Settings); err != nil {
		return 0, nil, err
	}
	if err := tx.Commit(); err != nil {
		return 0, nil, fmt.Errorf("db: commit vless import: %w", err)
	}
	sort.Slice(missing, func(i, j int) bool { return missing[i] < missing[j] })
	return len(present), missing, nil
}

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// ValidVLESSUUID — UUID в каноническом виде, строчными буквами. Регистр
// фиксирован: UUID входит в ссылку клиента, и та должна совпадать байт в
// байт с уже выданной.
func ValidVLESSUUID(s string) bool { return uuidPattern.MatchString(s) }

// NewVLESSUUID — случайный UUID версии 4 из crypto/rand.
func NewVLESSUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("db: read random: %w", err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// migrateClientVLESS (schema v11) даёт каждому клиенту VLESS UUID.
//
// Аддитивно, как все миграции здесь (docs/adr/0001): колонка с пустым
// значением по умолчанию, поэтому прежняя версия панели, вернувшаяся
// откатом, по-прежнему создаёт клиентов — просто без UUID. Пустые UUID
// заполняются на каждом Migrate, так что такие клиенты получат свой при
// следующем обновлении. Уникальность — частичным индексом: пустых может
// быть сколько угодно, выданных — по одному (amnezia-vpn-server-14y0.1).
func migrateClientVLESS(handle *sql.DB) error {
	has, err := tableHasColumn(handle, "clients", "vless_uuid")
	if err != nil {
		return err
	}
	if !has {
		if _, err := handle.Exec(`ALTER TABLE clients ADD COLUMN vless_uuid TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("db: add clients.vless_uuid: %w", err)
		}
	}
	if _, err := handle.Exec(
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_clients_vless_uuid ON clients(vless_uuid) WHERE vless_uuid != ''`,
	); err != nil {
		return fmt.Errorf("db: index clients.vless_uuid: %w", err)
	}

	rows, err := handle.Query(`SELECT id FROM clients WHERE vless_uuid = '' ORDER BY id`)
	if err != nil {
		return fmt.Errorf("db: find clients without vless uuid: %w", err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("db: scan client id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("db: iterate clients: %w", err)
	}
	rows.Close()
	for _, id := range ids {
		u, err := NewVLESSUUID()
		if err != nil {
			return err
		}
		if _, err := handle.Exec(`UPDATE clients SET vless_uuid = ? WHERE id = ? AND vless_uuid = ''`, u, id); err != nil {
			return fmt.Errorf("db: assign vless uuid to client %d: %w", id, err)
		}
	}
	return nil
}

func tableHasColumn(handle *sql.DB, table, column string) (bool, error) {
	rows, err := handle.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false, fmt.Errorf("db: inspect %s: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid, notnull, pk int
			name, ctype      string
			dflt             sql.NullString
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false, fmt.Errorf("db: inspect %s: %w", table, err)
		}
		if name == column {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("db: inspect %s: %w", table, err)
	}
	return false, nil
}

// SetClientVLESSUUID задаёт UUID клиента. Нужен импорту ручной настройки:
// ссылки, выданные до панели, несут свои UUID, и чтобы они продолжили
// работать, клиент должен получить именно их.
func SetClientVLESSUUID(handle *sql.DB, id int64, uuid string) error {
	if !ValidVLESSUUID(uuid) {
		return fmt.Errorf("db: invalid VLESS UUID")
	}
	var other int
	if err := handle.QueryRow(`SELECT COUNT(*) FROM clients WHERE vless_uuid = ? AND id != ?`, uuid, id).Scan(&other); err != nil {
		return fmt.Errorf("db: check vless uuid: %w", err)
	}
	if other > 0 {
		return ErrVLESSUUIDTaken
	}
	if err := handle.QueryRow(`SELECT COUNT(*) FROM xray_service_users WHERE uuid = ?`, uuid).Scan(&other); err != nil {
		return fmt.Errorf("db: check vless uuid: %w", err)
	}
	if other > 0 {
		return ErrVLESSUUIDTaken
	}
	return mutateClient(handle, id,
		`UPDATE clients SET vless_uuid = ?, updated_at = ? WHERE id = ?`,
		uuid, stamp(), id)
}

// VLESSClient — то, что нужно конфигу Xray и ссылке о клиенте.
type VLESSClient struct {
	ID   int64
	Name string
	UUID string
}

// VLESSClientsForConfig — клиенты, которым Xray открыт: те же, что попадают
// в awg0.conf (включён и не истёк), и только с UUID. Одно правило на оба
// протокола — отключение в панели закрывает оба.
func VLESSClientsForConfig(handle Querier) ([]VLESSClient, error) {
	rows, err := handle.Query(
		`SELECT id, name, vless_uuid, expires_at FROM clients
		  WHERE enabled = 1 AND vless_uuid != '' ORDER BY id`,
	)
	if err != nil {
		return nil, fmt.Errorf("db: query vless clients: %w", err)
	}
	defer rows.Close()
	now := time.Now().UTC()
	var out []VLESSClient
	for rows.Next() {
		var (
			c   VLESSClient
			exp sql.NullString
		)
		if err := rows.Scan(&c.ID, &c.Name, &c.UUID, &exp); err != nil {
			return nil, fmt.Errorf("db: scan vless client: %w", err)
		}
		if clientExpired(exp.String, now) {
			continue
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate vless clients: %w", err)
	}
	return out, nil
}
