package xray

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

// Конфиг Xray собирается из базы так же, как awg0.conf: файл производный,
// руками его не правят (amnezia-vpn-server-14y0.2). По смыслу он повторяет
// ручной конфиг с боевого (amnezia-vpn-server-adaw): VLESS+XHTTP+Reality на
// 127.0.0.1:4432 за nginx stream, который делит TCP 443 по SNI и передаёт
// адрес клиента через PROXY protocol.

// InboundTag — тег входа VLESS; по нему API Xray добавляет и снимает
// пользователей.
const InboundTag = "vless-xhttp"

// DefaultAPIAddr — адрес API Xray (HandlerService): Unix-сокет в каталоге,
// который смонтирован и в Xray, и в панель (решение оркестратора по
// amnezia-vpn-server-14y0.2). Xray живёт в сети хоста, панель — в своей
// сети Docker, и 127.0.0.1 у них разный: TCP-порт на петле хоста панель не
// видит, а открывать API в сеть значило бы дать любому соседу менять
// пользователей. Сокет связывает их через файловую систему, без сети, и
// доступ к нему решают права файла.
const DefaultAPIAddr = "unix:/run/xray/api.sock"

// unixPrefix — так в AMNEZIA_XRAY_API_ADDR записывается путь сокета.
const unixPrefix = "unix:"

// apiSocketMode — сокет API только для root: и Xray, и панель работают от
// root, больше к нему ходить некому.
const apiSocketMode = "0600"

// API-вход: dokodemo-door на сокете с тегом api, маршрут inboundTag api →
// outbound api (его создаёт сам Xray для HandlerService). Адрес и порт в
// settings нужны только потому, что dokodemo-door без них не знает, куда
// «ведёт» соединение, — наружу ничего не уходит.
const (
	apiTag         = "api"
	apiDokodemoIP  = "127.0.0.1"
	apiDokodemoPrt = 10085
)

// ConfigPath — где лежит конфиг Xray. AMNEZIA_XRAY_CONFIG_PATH задаёт его
// явно; без него — config/xray/config.json рядом с awg0.conf. Отдельный
// каталог, а не файл рядом: контейнер Xray монтирует каталог целиком (файл,
// смонтированный сам по себе, после атомарной замены остался бы старым), и
// ключи AWG из config/ ему видеть незачем.
func ConfigPath(awgConfPath string) string {
	if p := os.Getenv("AMNEZIA_XRAY_CONFIG_PATH"); p != "" {
		return p
	}
	return filepath.Join(filepath.Dir(awgConfPath), "xray", "config.json")
}

// APIAddr — адрес API Xray (AMNEZIA_XRAY_API_ADDR или DefaultAPIAddr).
func APIAddr() string {
	if a := os.Getenv("AMNEZIA_XRAY_API_ADDR"); a != "" {
		return a
	}
	return DefaultAPIAddr
}

// Options — то, что в конфиге зависит не от базы, а от развёртывания.
type Options struct {
	APIAddr string
}

// Порядок полей структур — порядок ключей в файле: одинаковые данные дают
// одинаковые байты, и сравнение старого файла с новым имеет смысл.
type config struct {
	Log       logConfig       `json:"log"`
	API       apiConfig       `json:"api"`
	Inbounds  []inbound       `json:"inbounds"`
	Outbounds []outbound      `json:"outbounds"`
	Routing   routingSettings `json:"routing"`
}

// logConfig — журналы Xray (amnezia-vpn-server-14y0.3). Журнала доступа
// нет: в нём адрес каждого клиента рядом с каждым сайтом, куда он ходил, —
// у сервера, который забрали, это история всех клиентов; и такой файл
// растёт без предела, а ротировать его пришлось бы на хосте отдельной
// службой. Ошибки — в stderr, то есть в json-file Docker, где они уже
// ограничены 10m×3, как у остальных сервисов. Журнал доступа понадобится
// для графиков — включать его тогда вместе с ротацией.
type logConfig struct {
	Access   string `json:"access"`
	Error    string `json:"error"`
	LogLevel string `json:"loglevel"`
}

type apiConfig struct {
	Tag      string   `json:"tag"`
	Listen   string   `json:"listen,omitempty"`
	Services []string `json:"services"`
}

// inbound — и вход VLESS, и API-вход dokodemo-door: у второго нет ни
// порта, ни транспорта, поэтому они необязательны.
type inbound struct {
	Tag            string          `json:"tag"`
	Listen         string          `json:"listen"`
	Port           int             `json:"port,omitempty"`
	Protocol       string          `json:"protocol"`
	Settings       inboundSettings `json:"settings"`
	StreamSettings *streamSettings `json:"streamSettings,omitempty"`
	Sniffing       *sniffingConfig `json:"sniffing,omitempty"`
}

// inboundSettings — settings обоих входов. Clients — указатель: у входа
// VLESS пустой список пишется как [], у API-входа его нет совсем.
type inboundSettings struct {
	Clients    *[]vlessUser `json:"clients,omitempty"`
	Decryption string       `json:"decryption,omitempty"`
	Address    string       `json:"address,omitempty"`
	Port       int          `json:"port,omitempty"`
	Network    string       `json:"network,omitempty"`
}

type vlessUser struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

type streamSettings struct {
	Network         string          `json:"network"`
	XHTTPSettings   xhttpSettings   `json:"xhttpSettings"`
	Security        string          `json:"security"`
	RealitySettings realitySettings `json:"realitySettings"`
	Sockopt         sockopt         `json:"sockopt"`
}

type xhttpSettings struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
}

type realitySettings struct {
	Show        bool     `json:"show"`
	Target      string   `json:"target"`
	Xver        int      `json:"xver"`
	ServerNames []string `json:"serverNames"`
	PrivateKey  string   `json:"privateKey"`
	ShortIDs    []string `json:"shortIds"`
}

type sockopt struct {
	AcceptProxyProtocol bool `json:"acceptProxyProtocol"`
}

type sniffingConfig struct {
	Enabled      bool     `json:"enabled"`
	DestOverride []string `json:"destOverride"`
	RouteOnly    bool     `json:"routeOnly"`
}

type outbound struct {
	Tag      string `json:"tag"`
	Protocol string `json:"protocol"`
}

type routingSettings struct {
	Rules []routingRule `json:"rules"`
}

type routingRule struct {
	Type        string   `json:"type"`
	InboundTag  []string `json:"inboundTag,omitempty"`
	IP          []string `json:"ip,omitempty"`
	OutboundTag string   `json:"outboundTag"`
}

// Email пользователя в Xray — «c<id клиента>», как в ручной настройке:
// по нему API снимает пользователя, а журнал доступа связывает
// подключение с клиентом панели, не раскрывая его имени.
func userEmail(id int64) string { return fmt.Sprintf("c%d", id) }

func build(s *db.XraySettings, clients []db.VLESSClient, service []db.VLESSServiceUser, opt Options) config {
	users := make([]vlessUser, 0, len(clients)+len(service))
	for _, c := range clients {
		users = append(users, vlessUser{ID: c.UUID, Email: userEmail(c.ID)})
	}
	// Служебные учётки (вход «ruentry») — всегда, под своим email: ни
	// отключение, ни удаление клиентов панели их не касается.
	for _, u := range service {
		users = append(users, vlessUser{ID: u.UUID, Email: u.Email})
	}
	c := config{
		Log: logConfig{
			Access:   "none",
			Error:    "",
			LogLevel: "warning",
		},
		API: apiConfig{Tag: apiTag, Services: []string{"HandlerService"}},
		Inbounds: []inbound{{
			Tag:      InboundTag,
			Listen:   inboundListen,
			Port:     inboundPort,
			Protocol: "vless",
			Settings: inboundSettings{Clients: &users, Decryption: "none"},
			StreamSettings: &streamSettings{
				Network:       "xhttp",
				XHTTPSettings: xhttpSettings{Path: s.Path, Mode: "auto"},
				Security:      "reality",
				RealitySettings: realitySettings{
					Target:      s.Target,
					ServerNames: []string{s.ServerName},
					PrivateKey:  s.PrivateKey,
					ShortIDs:    []string{s.ShortID},
				},
				// Подключения приходят от nginx stream: без PROXY protocol
				// журнал видел бы у всех клиентов адрес 127.0.0.1.
				Sockopt: sockopt{AcceptProxyProtocol: true},
			},
			Sniffing: &sniffingConfig{Enabled: true, DestOverride: []string{"http", "tls", "quic"}, RouteOnly: true},
		}},
		Outbounds: []outbound{
			{Tag: "direct", Protocol: "freedom"},
			{Tag: "block", Protocol: "blackhole"},
		},
		// Частные сети хостера клиентам не положены — тот же запрет, что
		// для туннеля AWG (amnezia-vpn-server-ubjp).
		Routing: routingSettings{Rules: []routingRule{
			{Type: "field", IP: []string{"geoip:private"}, OutboundTag: "block"},
		}},
	}
	if path, ok := strings.CutPrefix(opt.APIAddr, unixPrefix); ok {
		c.Inbounds = append(c.Inbounds, inbound{
			Tag:      apiTag,
			Listen:   path + "," + apiSocketMode,
			Protocol: "dokodemo-door",
			Settings: inboundSettings{Address: apiDokodemoIP, Port: apiDokodemoPrt, Network: "unix"},
		})
		// Первым: правило про частные сети не должно перехватить API.
		c.Routing.Rules = append([]routingRule{
			{Type: "field", InboundTag: []string{apiTag}, OutboundTag: apiTag},
		}, c.Routing.Rules...)
	} else {
		// TCP-адрес — только для разработки и тестов.
		c.API.Listen = opt.APIAddr
	}
	return c
}

func marshal(c config) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(c); err != nil {
		return nil, fmt.Errorf("xray: marshal config: %w", err)
	}
	return b.Bytes(), nil
}

// Render — конфиг Xray из настроек, включённых клиентов и служебных
// учёток.
func Render(s *db.XraySettings, clients []db.VLESSClient, service []db.VLESSServiceUser, opt Options) ([]byte, error) {
	return marshal(build(s, clients, service, opt))
}
