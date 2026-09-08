package awgconf

import (
	"strings"
	"testing"
)

// Интерфейс один на всех, поэтому размер клиенту задаёт маршрут, а не
// интерфейс (amnezia-vpn-server-wc2l).
func TestDeviceMTUNeverGoesBelowWhatClientsGet(t *testing.T) {
	cases := []struct {
		env    string
		client uint16
		want   uint16
	}{
		// Развёртывание, где потолок не измеряли: всё как было.
		{"", 1340, 1340},
		{"1440", 1340, 1440},
		// Потолок ниже осторожного значения сделал бы хуже всем сразу.
		{"1300", 1340, 1340},
		{"мусор", 1340, 1340},
		{"9000", 1340, 1340},
		{"1440", 1440, 1440},
	}
	for _, c := range cases {
		t.Setenv(deviceMTUEnv, c.env)
		if got := DeviceMTU(c.client); got != c.want {
			t.Errorf("DeviceMTU(%d) при %s=%q = %d, ожидалось %d",
				c.client, deviceMTUEnv, c.env, got, c.want)
		}
	}
}

// Размер маршрута больше размера устройства ядро не примет, и клиент остался
// бы вообще без маршрута — то есть получил бы потолок вместо своего значения.
func TestRouteMTUNeverExceedsTheDevice(t *testing.T) {
	cases := []struct {
		own, def, device, want uint16
	}{
		{0, 1340, 1440, 1340},
		{1420, 1340, 1440, 1420},
		{1500, 1340, 1440, 1440},
		{1300, 1340, 1440, 1300},
		{0, 1340, 1340, 1340},
	}
	for _, c := range cases {
		if got := RouteMTU(c.own, c.def, c.device); got != c.want {
			t.Errorf("RouteMTU(%d, %d, %d) = %d, ожидалось %d",
				c.own, c.def, c.device, got, c.want)
		}
	}
}

// Строка с размером — метка для контейнера awg, и она обязана быть
// комментарием: разбор AmneziaWG отвергает всё, чего не знает.
func TestRenderPutsRouteMTUInComments(t *testing.T) {
	server := ServerConfig{
		PrivateKey: "k", Address: "10.8.0.1/24", ListenPort: 51820,
		MTU: 1440, ClientMTU: 1340,
	}
	peers := []PeerConfig{
		{PublicKey: "a", AllowedIPs: "10.8.0.2/32"},
		{PublicKey: "b", AllowedIPs: "10.8.0.3/32", MTU: 1420},
	}
	out := Render(server, peers)

	if !strings.Contains(out, "MTU = 1440\n") {
		t.Fatalf("интерфейс не поднят до потолка:\n%s", out)
	}
	if !strings.Contains(out, "# amnezia-route-mtu = 1340\n") {
		t.Fatalf("общее значение не выписано:\n%s", out)
	}
	if !strings.Contains(out, "# amnezia-route-mtu = 1420\n") {
		t.Fatalf("собственное значение клиента не выписано:\n%s", out)
	}
	// Каждая строка размера — комментарий, иначе awg setconf откажется
	// принимать конфигурацию целиком.
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "amnezia-route-mtu") && !strings.HasPrefix(line, "#") {
			t.Fatalf("строка размера не комментарий: %q", line)
		}
	}
}

// Развёртывание без измеренного потолка обязано давать тот же файл, что и до
// этой возможности: иначе «ничего не изменилось» превратилось бы в
// перезапуск туннеля у всех.
func TestRenderIsUnchangedWithoutACeiling(t *testing.T) {
	server := ServerConfig{
		PrivateKey: "k", Address: "10.8.0.1/24", ListenPort: 51820, MTU: 1340,
	}
	peers := []PeerConfig{{PublicKey: "a", AllowedIPs: "10.8.0.2/32"}}
	if out := Render(server, peers); strings.Contains(out, "amnezia-route-mtu") {
		t.Fatalf("появилась строка размера там, где раздавать нечего:\n%s", out)
	}
}

// Клиент, попросивший максимум, получал минимум (amnezia-vpn-server-wc2l).
//
// Условие «строка не нужна, если размер совпадает с потолком устройства»
// было неверным: отсутствие строки означает не «взять потолок», а «взять
// общее осторожное значение». Потолок клиента в базе равен 1440, и на
// чистом пути потолок устройства ровно 1440 — то есть случай достижим
// обычной настройкой, а не краем.
func TestPeerLineSurvivesAClientAtTheCeiling(t *testing.T) {
	const device, common = 1440, 1340
	cases := []struct {
		name string
		own  uint16
		want uint16 // 0 = строки нет
	}{
		{"как у всех", 0, 0},
		{"меньше общего", 1300, 1300},
		{"больше общего", 1420, 1420},
		// Вот он: свой размер равен потолку устройства.
		{"ровно потолок", 1440, 1440},
		// И тот, кто попросил сверх потолка: зажимается, но строку получает.
		{"сверх потолка", 1500, 1440},
	}
	for _, c := range cases {
		got := PeerRouteMTU(c.own, common, device)
		if got != c.want {
			t.Errorf("%s: PeerRouteMTU(%d, %d, %d) = %d, ожидалось %d",
				c.name, c.own, common, device, got, c.want)
		}
	}
}

// Развёртывание без измеренного потолка: общего значения нет, и строку
// получает только тот, у кого размер свой.
func TestPeerLineWithoutAMeasuredCeiling(t *testing.T) {
	const device = 1340
	if got := PeerRouteMTU(0, 0, device); got != 0 {
		t.Errorf("клиент без своего размера получил строку: %d", got)
	}
	if got := PeerRouteMTU(1300, 0, device); got != 1300 {
		t.Errorf("свой размер не доехал: %d", got)
	}
}

// Предел скорости уезжает комментарием и только тому, кому задан
// (amnezia-vpn-server-jzzu).
func TestRenderPutsRateInComments(t *testing.T) {
	server := ServerConfig{
		PrivateKey: "k", Address: "10.8.0.1/24", ListenPort: 51820, MTU: 1340,
	}
	peers := []PeerConfig{
		{PublicKey: "a", AllowedIPs: "10.8.0.2/32"},
		{PublicKey: "b", AllowedIPs: "10.8.0.3/32", RateLimit: 50},
	}
	out := Render(server, peers)

	if !strings.Contains(out, "# amnezia-rate = 50\n") {
		t.Fatalf("предел не выписан:\n%s", out)
	}
	if strings.Count(out, "amnezia-rate") != 1 {
		t.Fatalf("предел выписан тому, кому не задан:\n%s", out)
	}
	// Комментарий, иначе awg setconf отвергнет конфигурацию целиком.
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "amnezia-rate") && !strings.HasPrefix(line, "#") {
			t.Fatalf("строка предела не комментарий: %q", line)
		}
	}
}
