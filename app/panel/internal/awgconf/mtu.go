package awgconf

import (
	"database/sql"
	"fmt"
	"strconv"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

// EncapsulationOverhead is what AmneziaWG adds to every transport packet
// on the wire: 32 bytes of WireGuard header and Poly1305 tag, 8 bytes of
// UDP and 20 bytes of IPv4. The obfuscation parameters (Jc/S1/S2, the
// I-tags) pad handshakes and junk packets, not transport packets, so they
// do not enter this budget.
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
// 1340 puts a full packet at 1400 bytes on the wire, which the mobile
// paths seen in practice carry, and still leaves room under PPPoE (1492)
// and a tunnelled hosting uplink (1476). The cost against 1420 is about
// 5% of payload per packet — far less than one dropped packet in a video
// stream costs.
const DefaultMTU uint16 = 1340

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
