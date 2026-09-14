package awgconf

import (
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

// EncapsulationOverhead is what AmneziaWG adds to every transport packet
// on the wire: 32 bytes of WireGuard header and Poly1305 tag, 8 bytes of
// UDP and 20 bytes of IPv4. Jc/S1/S2/S3 and the I-tags pad handshakes and
// junk packets only; S4 (AWG 2.0) does pad every transport packet and adds
// to this on a real server — it is per-server and random, so it is not
// folded into the constant (amnezia-vpn-server-rplm).
const EncapsulationOverhead = 32 + 8 + 20

// MTU bounds. The floor is the IPv6 minimum link MTU, which every path is
// required to carry; the ceiling is plain Ethernet.
const (
	MinMTU uint16 = 1280
	MaxMTU uint16 = 1500
)

// DefaultMTU is the tunnel MTU used when the operator has not pinned one.
//
// awg-quick derives its own default from the MTU of the interface holding
// the default route (1500 - 80 = 1420). That is wrong twice over.
//
// It is wrong for the server's uplink whenever that uplink is itself
// tunnelled: hosting providers running GRE/VXLAN commonly hand out ens3
// with MTU 1500 while the real path MTU is 1476, so a 1420-byte payload
// leaves as a 1480-byte packet the uplink cannot carry.
//
// It is wrong for the client's last mile always, and that is the shorter
// path. A mobile carrier measured on a live deployment carried 1411 bytes
// against the server's 1476, and the server cannot probe it: the last mile
// differs per client and changes as a phone moves between mobile, home and
// someone else's Wi-Fi. Sizing the tunnel from what the server can see
// leaves full-size packets to be dropped or fragmented out there, which
// shows up as pages loading normally while video stalls.
//
// 1360 is the smallest round value that carries QUIC from TikTok: its CDN
// sends 1348-byte packets and ignores «fragmentation needed», so a 1340
// route dropped every one of them and the app waited for a TCP fallback —
// the feed loaded slowly and video stalled (measured on the test server
// 14.09.2026, amnezia-vpn-server-dy91). The earlier 1340 was sized for a
// mobile path measured at 1411 bytes; 1360 costs 20 bytes more on the wire,
// and the owner chose to raise it and watch rather than stay below TikTok
// (amnezia-vpn-server-rplm). A client whose last mile cannot carry it gets
// its own MTU in the panel.
//
// The wire cost is MTU + EncapsulationOverhead + S4: AWG 2.0 pads transport
// packets by S4 as well, so on a server with S4 = 29 a full 1360 packet is
// 1449 bytes, not 1420.
const DefaultMTU uint16 = 1360

// settingsMTUKey is the settings key holding the tunnel MTU. install.sh
// measures the uplink path MTU and pins the value; when the key is absent
// DefaultMTU applies.
const settingsMTUKey = "mtu"

// ValidateMTU accepts 0 ("not pinned") and any value within the bounds.
func ValidateMTU(mtu uint16) error {
	if mtu == 0 {
		return nil
	}
	if mtu < MinMTU || mtu > MaxMTU {
		return fmt.Errorf("invalid mtu %d: must be between %d and %d", mtu, MinMTU, MaxMTU)
	}
	return nil
}

// MTUFromSettings returns the pinned tunnel MTU, or DefaultMTU when the
// settings key is absent. A present but unusable value is an error rather
// than a silent fallback: a wrong MTU breaks large transfers in a way that
// is hard to attribute, so the operator must see it.
func MTUFromSettings(handle *sql.DB) (uint16, error) {
	raw, ok, err := db.GetSetting(handle, settingsMTUKey)
	if err != nil {
		return 0, fmt.Errorf("read mtu setting: %w", err)
	}
	// Absent, or present but empty: an archive written before this setting
	// existed carries no value, and refusing to render a config over that
	// would fail the restore rather than fall back.
	if !ok || raw == "" {
		return DefaultMTU, nil
	}
	parsed, err := strconv.ParseUint(raw, 10, 16)
	if err != nil {
		return 0, fmt.Errorf("invalid mtu setting %q: not an unsigned 16-bit value", raw)
	}
	mtu := uint16(parsed)
	if mtu == 0 {
		return 0, fmt.Errorf("invalid mtu setting %q: must be between %d and %d", raw, MinMTU, MaxMTU)
	}
	if err := ValidateMTU(mtu); err != nil {
		return 0, fmt.Errorf("invalid mtu setting %q: %w", raw, err)
	}
	return mtu, nil
}

// deviceMTUEnv — потолок, который тянет путь самого сервера. Его измеряет
// установщик и кладёт в .env развёртывания; панель читает оттуда, потому
// что это свойство машины, а не настройка продукта.
const deviceMTUEnv = "TUNNEL_MTU_MAX"

// DeviceMTU возвращает MTU интерфейса awg0 (amnezia-vpn-server-wc2l).
//
// Интерфейс один на всех, поэтому раньше он и был осторожным: значение
// рассчитывали на худшую последнюю милю, какая может встретиться кому
// угодно. Из-за этого роутер на оптике получал половину выигрыша — быструю
// отдачу и прежнее скачивание, потому что обратное направление ограничено
// интерфейсом.
//
// Теперь интерфейс поднимается до того, что тянет сервер, а осторожное
// значение раздаётся маршрутами. Меньше clientMTU потолок не бывает: это
// сделало бы хуже всем сразу. Развёртывание, где потолок не измеряли,
// получает потолок, равный осторожному значению, — то есть ровно то, что
// было до этой возможности.
func DeviceMTU(clientMTU uint16) uint16 {
	device, _ := TunnelMTUs(clientMTU, 0)
	return device
}

// TransportPadding — сколько AWG 2.0 добавляет к каждому транспортному
// пакету сверх EncapsulationOverhead: S4. Параметр случайный и свой у
// каждого сервера, поэтому в константу не входит (amnezia-vpn-server-bctr).
func TransportPadding(p *Params) uint16 {
	if p == nil || p.S4 == nil {
		return 0
	}
	return *p.S4
}

// TunnelMTUs возвращает MTU интерфейса awg0 и общее значение для клиентов с
// учётом добивки S4 (amnezia-vpn-server-bctr).
//
// Установщик кладёт в TUNNEL_MTU_MAX путь сервера минус 60 байт — заголовок
// AmneziaWG, UDP и IPv4. Но AWG 2.0 добивает каждый транспортный пакет ещё на
// S4 байт, и на пути 1500 при S4 = 29 без нарезки проходит 1411, а не 1440:
// всё выше сервер резал на куски (на тестовом — почти половину исходящего).
// Поэтому потолок интерфейса — измеренное минус S4, а общее значение для
// клиентов не больше потолка. Где потолок не измеряли, S4 не вычитается: там
// значения совпадают с осторожным, и поведение прежнее.
func TunnelMTUs(clientMTU, padding uint16) (device, clientDefault uint16) {
	raw := strings.TrimSpace(os.Getenv(deviceMTUEnv))
	if raw == "" {
		return clientMTU, clientMTU
	}
	v, err := strconv.ParseUint(raw, 10, 16)
	if err != nil {
		return clientMTU, clientMTU
	}
	measured := uint16(v)
	if ValidateMTU(measured) != nil {
		return clientMTU, clientMTU
	}
	// Без добивки — прежнее правило wc2l: потолок не бывает ниже осторожного
	// значения. Установщик и не пишет такого (осторожное значение он сам
	// зажимает измеренным), так что случай возможен только вручную.
	if padding == 0 {
		if measured < clientMTU {
			return clientMTU, clientMTU
		}
		return measured, clientMTU
	}
	device = measured
	if padding > 0 {
		if measured-MinMTU > padding {
			device = measured - padding
		} else {
			device = MinMTU
		}
	}
	clientDefault = clientMTU
	if clientDefault > device {
		clientDefault = device
	}
	if device < clientDefault {
		device = clientDefault
	}
	return device, clientDefault
}

// ClientMTUMax — наибольший MTU, который панель разрешает задать клиенту на
// этом сервере: потолок интерфейса. Больше ядро всё равно не выпишет в
// маршрут, а до потолка пакет проходит путь сервера без нарезки
// (amnezia-vpn-server-bctr).
//
// 0 — потолок не измеряли (TUNNEL_MTU_MAX не задан): сказать нечего, и
// действует общая граница db.ClientMTUCeiling, как до этой задачи.
func ClientMTUMax(handle *sql.DB) (uint16, error) {
	if strings.TrimSpace(os.Getenv(deviceMTUEnv)) == "" {
		return 0, nil
	}
	server, err := db.ServerRow(handle)
	if err != nil {
		return 0, err
	}
	params, err := ParseParams(server.AWGParams)
	if err != nil {
		return 0, err
	}
	mtu, err := MTUFromSettings(handle)
	if err != nil {
		return 0, err
	}
	device, _ := TunnelMTUs(mtu, TransportPadding(params))
	return device, nil
}

// PeerRouteMTU — размер для строки этого пира, или 0, когда строка не нужна.
//
// Строка не нужна ровно в одном случае: клиенту не задано своего размера, и
// он получит общее значение, выписанное один раз в [Interface]. Во всех
// остальных случаях она обязательна — включая тот, где свой размер совпал с
// потолком устройства.
//
// Прежде здесь стояло ещё одно условие, «не выписывать, если совпало с
// потолком»: казалось, что отсутствие строки означает «взять потолок». Оно
// означает «взять общее осторожное значение», поэтому клиент, попросивший
// максимум, получал минимум. Потолок клиента равен 1440, и на чистом пути
// потолок устройства ровно 1440 — то есть случай достижим обычной
// настройкой, а не краем (amnezia-vpn-server-wc2l).
func PeerRouteMTU(clientOwn, clientDefault, device uint16) uint16 {
	if clientOwn == 0 {
		return 0
	}
	return RouteMTU(clientOwn, clientDefault, device)
}

// RouteMTU — размер, который получит этот клиент: свой, если задан, иначе
// общий. Больше потолка устройства не бывает никогда: ядро откажется
// принять такой маршрут, и клиент остался бы вообще без маршрута.
func RouteMTU(clientOwn, clientDefault, device uint16) uint16 {
	mtu := clientDefault
	if clientOwn != 0 {
		mtu = clientOwn
	}
	if device != 0 && mtu > device {
		mtu = device
	}
	return mtu
}
