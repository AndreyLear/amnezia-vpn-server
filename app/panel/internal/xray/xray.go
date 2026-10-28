// Package xray — VLESS+XHTTP+Reality рядом с AmneziaWG (эпик
// amnezia-vpn-server-14y0).
//
// На мобильных сетях РФ из проверенного работает только VLESS+XHTTP+Reality
// на TCP 443 (эксперимент 8b37): nginx на хосте делит 443 по SNI, имя панели
// уходит панели, остальное — Xray на 127.0.0.1:4432 с PROXY protocol. Здесь
// — то, что панель знает о Xray: ключи Reality, ссылка клиента, импорт
// ручной настройки с боевого (amnezia-vpn-server-14y0.1), конфиг Xray и его
// применение через API (amnezia-vpn-server-14y0.2).
package xray

import (
	"crypto/ecdh"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

// Адрес входа VLESS: только петля, снаружи на него приходит nginx stream,
// деля TCP 443 по SNI, — как на боевом (amnezia-vpn-server-adaw).
const (
	inboundListen = "127.0.0.1"
	inboundPort   = 4432
)

// realityEncoding — как у `xray x25519` по умолчанию. Ключ в другом
// алфавите Xray не прочтёт, а ссылка разойдётся с выданной.
var realityEncoding = base64.RawURLEncoding

// GenerateRealityKeys выдаёт пару X25519 для Reality так же, как `xray
// x25519`: случайные 32 байта, «зажатые» по правилам X25519 (так ключ в
// файле совпадает с тем, что реально используется), и публичный ключ из
// приватного.
func GenerateRealityKeys() (privateKey, publicKey string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("xray: read random: %w", err)
	}
	raw[0] &= 248
	raw[31] &= 127
	raw[31] |= 64
	key, err := ecdh.X25519().NewPrivateKey(raw)
	if err != nil {
		return "", "", fmt.Errorf("xray: x25519: %w", err)
	}
	return realityEncoding.EncodeToString(raw), realityEncoding.EncodeToString(key.PublicKey().Bytes()), nil
}

// RealityPublicKey выводит публичный ключ из приватного. Текст ошибки
// ключа не содержит.
func RealityPublicKey(privateKey string) (string, error) {
	raw, err := realityEncoding.DecodeString(privateKey)
	if err != nil || len(raw) != 32 {
		return "", errors.New("xray: reality private key is not 32 bytes of base64 raw url")
	}
	key, err := ecdh.X25519().NewPrivateKey(raw)
	if err != nil {
		return "", errors.New("xray: reality private key is not an x25519 key")
	}
	return realityEncoding.EncodeToString(key.PublicKey().Bytes()), nil
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("xray: read random: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// NewServerSettings — свежие настройки Xray: новые ключи Reality, shortId
// на 8 байт (наибольший, что принимает Xray), случайный путь XHTTP и
// маскировка self-steal: serverName — домен этого сервера, чужие
// подключения — на локальный nginx с его настоящим сертификатом. Адрес для
// ссылок пуст — берётся из endpoint.
func NewServerSettings(serverName string) (*db.XraySettings, error) {
	if serverName == "" {
		return nil, ErrNoServerName
	}
	priv, pub, err := GenerateRealityKeys()
	if err != nil {
		return nil, err
	}
	sid, err := randomHex(8)
	if err != nil {
		return nil, err
	}
	path, err := randomHex(6)
	if err != nil {
		return nil, err
	}
	return &db.XraySettings{
		PrivateKey: priv,
		PublicKey:  pub,
		ShortID:    sid,
		Path:       "/" + path,
		Target:     db.XrayDefaultTarget,
		ServerName: serverName,
		LinkPort:   db.XrayDefaultLinkPort,
		LinkPrefix: db.XrayDefaultLinkPrefix,
	}, nil
}

// ErrNoServerName — не из чего взять имя для Reality: endpoint сервера —
// IP-адрес (или не задан), а self-steal нужен домен с настоящим
// сертификатом.
var ErrNoServerName = errors.New("xray: no domain for reality: the server endpoint is not a domain name; pass the server's domain explicitly")

// DefaultServerName — домен сервера для self-steal: хост endpoint, если
// это имя, а не IP-адрес. CLIENT_DOMAIN при установке попадает именно в
// endpoint, и на этом же имени nginx держит сертификат Let's Encrypt.
func DefaultServerName(handle db.Querier) (string, error) {
	ep, ok, err := db.GetSetting(handle, "endpoint")
	if err != nil {
		return "", err
	}
	if !ok {
		return "", ErrNoServerName
	}
	host, _, err := net.SplitHostPort(ep)
	if err != nil || host == "" || net.ParseIP(host) != nil {
		return "", ErrNoServerName
	}
	return host, nil
}

// escape — процентное кодирование всего, кроме незарезервированных
// символов RFC 3986, заглавными шестнадцатеричными цифрами (как
// питоновский quote(s, safe="")). Так закодированы выданные вручную
// ссылки: путь «/x» — «%2Fx», пробел в имени — «%20», а не «+».
func escape(s string) string {
	const hexDigits = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if 'A' <= c && c <= 'Z' || 'a' <= c && c <= 'z' || '0' <= c && c <= '9' ||
			c == '-' || c == '.' || c == '_' || c == '~' {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hexDigits[c>>4])
		b.WriteByte(hexDigits[c&15])
	}
	return b.String()
}

// Link — ссылка vless:// клиента.
//
// Порядок параметров и кодирование повторяют ссылки, выданные вручную
// 27.10.2026 (amnezia-vpn-server-adaw): приложение различает подключения
// по строке целиком, и та же ссылка в другом порядке стала бы для него
// вторым сервером. Имя в ссылке — приставка из настроек Xray и имя
// клиента («VLESS direct andrey»).
func Link(s *db.XraySettings, address string, c db.VLESSClient) string {
	return buildLink(linkParams{
		Address: address, Port: s.LinkPort, Path: s.Path, SNI: s.ServerName,
		PublicKey: s.PublicKey, ShortID: s.ShortID, NamePrefix: s.LinkPrefix,
	}, c)
}

// EntryLink — ссылка того же клиента через узел-вход: UUID тот же, всё
// остальное — узла, имя — с его приставкой («RU andrey»). Так ссылки
// выдаются на входе сейчас (amnezia-vpn-server-14y0.6).
func EntryLink(e *db.XrayEntry, c db.VLESSClient) string {
	return buildLink(linkParams{
		Address: e.LinkHost, Port: e.LinkPort, Path: e.Path, SNI: e.SNI,
		PublicKey: e.PublicKey, ShortID: e.ShortID, NamePrefix: e.LinkPrefix,
	}, c)
}

type linkParams struct {
	Address, Path, SNI, PublicKey, ShortID, NamePrefix string
	Port                                               int64
}

func buildLink(p linkParams, c db.VLESSClient) string {
	var b strings.Builder
	b.WriteString("vless://")
	b.WriteString(c.UUID)
	b.WriteByte('@')
	b.WriteString(net.JoinHostPort(p.Address, strconv.FormatInt(p.Port, 10)))
	b.WriteString("?type=xhttp&path=")
	b.WriteString(escape(p.Path))
	b.WriteString("&mode=auto&security=reality&sni=")
	b.WriteString(escape(p.SNI))
	b.WriteString("&fp=chrome&pbk=")
	b.WriteString(escape(p.PublicKey))
	b.WriteString("&sid=")
	b.WriteString(escape(p.ShortID))
	b.WriteByte('#')
	b.WriteString(escape(p.NamePrefix + c.Name))
	return b.String()
}

// ErrNoLinkAddress — некуда направить ссылку: ни адреса для ссылок в
// настройках Xray, ни endpoint сервера.
var ErrNoLinkAddress = errors.New("xray: no address for client links: set the server endpoint or the xray link address")

// LinkAddress — адрес сервера в ссылках: явный из настроек Xray, иначе
// хост endpoint — тот же, куда ходят AWG-клиенты (домен CLIENT_DOMAIN,
// если он задан при установке).
func LinkAddress(handle db.Querier, s *db.XraySettings) (string, error) {
	if s.LinkAddress != "" {
		return s.LinkAddress, nil
	}
	ep, ok, err := db.GetSetting(handle, "endpoint")
	if err != nil {
		return "", err
	}
	if !ok || ep == "" {
		return "", ErrNoLinkAddress
	}
	host, _, err := net.SplitHostPort(ep)
	if err != nil || host == "" {
		return "", ErrNoLinkAddress
	}
	return host, nil
}

// ClientLink — ссылка клиента по id. Как и конфиг AWG, выдаётся и
// отключённому клиенту: работать она начнёт, когда его включат.
func ClientLink(handle *sql.DB, id int64) (string, error) {
	s, err := db.XraySettingsRow(handle)
	if err != nil {
		return "", err
	}
	c, err := db.ClientByID(handle, id)
	if err != nil {
		return "", err
	}
	if c.VLESSUUID == "" {
		return "", fmt.Errorf("xray: client %d has no VLESS UUID", id)
	}
	address, err := LinkAddress(handle, s)
	if err != nil {
		return "", err
	}
	return Link(s, address, db.VLESSClient{ID: c.ID, Name: c.Name, UUID: c.VLESSUUID}), nil
}

// Route — один способ подключиться по VLESS: напрямую к этому серверу
// (Entry пусто) или через узел-вход Entry.
type Route struct {
	Entry string
	Link  string
}

// ClientRoutes — ссылки клиента по всем маршрутам: сначала прямая (если
// Xray на этом сервере настроен), затем по узлам-входам в порядке имён.
// Отключённому или истёкшему клиенту — ни одной: раздавать ссылки, которые
// не работают, незачем. Ошибка — только когда клиента нет или база не
// читается.
func ClientRoutes(handle *sql.DB, id int64) ([]Route, error) {
	c, err := db.ClientByID(handle, id)
	if err != nil {
		return nil, err
	}
	routes := []Route{}
	if !c.Enabled || c.Expired(time.Now().UTC()) || c.VLESSUUID == "" {
		return routes, nil
	}
	vc := db.VLESSClient{ID: c.ID, Name: c.Name, UUID: c.VLESSUUID}
	switch s, err := db.XraySettingsRow(handle); {
	case err == nil:
		if address, err := LinkAddress(handle, s); err == nil {
			routes = append(routes, Route{Link: Link(s, address, vc)})
		} else if !errors.Is(err, ErrNoLinkAddress) {
			return nil, err
		}
	case !errors.Is(err, db.ErrXrayNotConfigured):
		return nil, err
	}
	entries, err := db.XrayEntries(handle)
	if err != nil {
		return nil, err
	}
	for i := range entries {
		routes = append(routes, Route{Entry: entries[i].Name, Link: EntryLink(&entries[i], vc)})
	}
	return routes, nil
}
