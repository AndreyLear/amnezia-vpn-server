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
