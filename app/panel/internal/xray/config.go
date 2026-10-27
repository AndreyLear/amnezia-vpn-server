package xray

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

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

// DefaultAPIAddr — адрес API Xray (HandlerService). Только петля: API
// меняет список пользователей и не должен быть доступен никому, кроме
// панели.
const DefaultAPIAddr = "127.0.0.1:10085"

// DefaultLogDir — каталог журналов Xray внутри его контейнера.
const DefaultLogDir = "/var/log/xray"

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

// LogDir — каталог журналов, как его видит Xray (AMNEZIA_XRAY_LOG_DIR или
// DefaultLogDir). Журнал доступа понадобится панели для журнала и графиков.
func LogDir() string {
	if d := os.Getenv("AMNEZIA_XRAY_LOG_DIR"); d != "" {
		return d
	}
	return DefaultLogDir
}

// Options — то, что в конфиге зависит не от базы, а от развёртывания.
type Options struct {
	APIAddr string
	LogDir  string
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

type logConfig struct {
	Access   string `json:"access"`
	Error    string `json:"error"`
	LogLevel string `json:"loglevel"`
}

type apiConfig struct {
	Tag      string   `json:"tag"`
	Listen   string   `json:"listen"`
	Services []string `json:"services"`
}

type inbound struct {
	Tag            string         `json:"tag"`
	Listen         string         `json:"listen"`
	Port           int            `json:"port"`
	Protocol       string         `json:"protocol"`
	Settings       vlessSettings  `json:"settings"`
	StreamSettings streamSettings `json:"streamSettings"`
	Sniffing       sniffingConfig `json:"sniffing"`
}

type vlessSettings struct {
	Clients    []vlessUser `json:"clients"`
	Decryption string      `json:"decryption"`
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
	IP          []string `json:"ip"`
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
	return config{
		Log: logConfig{
			Access:   filepath.Join(opt.LogDir, "access.log"),
			Error:    filepath.Join(opt.LogDir, "error.log"),
			LogLevel: "warning",
		},
		API: apiConfig{Tag: "api", Listen: opt.APIAddr, Services: []string{"HandlerService"}},
		Inbounds: []inbound{{
			Tag:      InboundTag,
			Listen:   inboundListen,
			Port:     inboundPort,
			Protocol: "vless",
			Settings: vlessSettings{Clients: users, Decryption: "none"},
			StreamSettings: streamSettings{
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
			Sniffing: sniffingConfig{Enabled: true, DestOverride: []string{"http", "tls", "quic"}, RouteOnly: true},
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
