// Reconciliation: join SQLite clients with the runtime status
// snapshot (M6_AUDIT.md §2.1.3). The status file is produced by the
// awg container (M5); the panel only reads it.
//
// Rules (M6 audit contract):
//   - join key: client.public_key == status peer public_key
//   - a peer without a client is never shown;
//   - a client without a peer is offline;
//   - online means last_handshake_utc != null and handshake age
//     <= 3 minutes (the exact 3-minute boundary is online);
//   - expired clients are shown as ordinary clients with their state,
//     they are never deleted or mutated here;
//   - missing status.json → NA; has_interface=false → interface down;
//     read/parse errors → generic error state (no details).
package web

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/status"
)

// OnlineMaxAge is the strict online threshold: a handshake at most this
// old keeps the client online (docs/TECHNICAL_SPEC_v2.0.md §6:
// "Online means handshake within 3 minutes").
const OnlineMaxAge = 3 * time.Minute

// InterfaceState describes what the panel knows about the runtime.
type InterfaceState int

const (
	// IfaceUp is the normal state: status.json read and interface
	// present.
	IfaceUp InterfaceState = iota
	// IfaceNA means status.json does not exist (M5 contract case E:
	// the consumer answers "na").
	IfaceNA
	// IfaceDown means has_interface=false in the status file.
	IfaceDown
	// IfaceError means the status file could not be read or parsed.
	// All errors map to this single generic state; details are never
	// surfaced to the client.
	IfaceError
)

// ClientCard is one dashboard card. SECURITY: the type deliberately
// has NO key/credential fields — db.ClientRecord.PrivateKey and
// .PresharedKey are never copied into it (M6_AUDIT.md §2.1.8).
type ClientCard struct {
	ID          int64
	Name        string
	Address     string
	Enabled     bool
	Expired     bool
	Online      bool
	HasPeer     bool
	CreatedText string
	// LastHandshakeText is a display string ("—" when never).
	LastHandshakeText string
	RxBytes           uint64
	TxBytes           uint64
	RxText            string
	TxText            string
}

// Reconciliation is the full dashboard payload.
type Reconciliation struct {
	Interface InterfaceState
	// GeneratedAtUTC is the status snapshot time (IfaceUp only).
	GeneratedAtUTC time.Time
	Cards          []ClientCard
}

// Reconcile builds the dashboard state. readErr is the error reported
// by status.ReadStatus (nil on success) and st the parsed snapshot
// (nil when the file is missing or unparsable); now is the virtual
// clock for the online decision. The function is pure — all tests
// drive it with fixed inputs.
func Reconcile(clients []db.ClientRecord, st *status.Status, readErr error, now time.Time) Reconciliation {
	rec := Reconciliation{}
	switch {
	case readErr == nil && st != nil && st.Interface != nil:
		rec.Interface = IfaceUp
		rec.GeneratedAtUTC = st.GeneratedAt
	case readErr == nil && st != nil:
		rec.Interface = IfaceDown
	case readErr == nil:
		rec.Interface = IfaceNA
	case errors.Is(readErr, fs.ErrNotExist):
		// M5 contract case E: missing status.json answers "na".
		rec.Interface = IfaceNA
	default:
		rec.Interface = IfaceError
	}

	peers := make(map[string]status.Peer, len(rec.Cards))
	if st != nil {
		for _, p := range st.Peers {
			peers[p.PublicKey] = p
		}
	}

	for _, c := range clients {
		card := ClientCard{
			ID:          c.ID,
			Name:        c.Name,
			Address:     c.Address,
			Enabled:     c.Enabled,
			Expired:     c.Expired(now),
			CreatedText: createdText(c.CreatedAt),
		}
		if p, ok := peers[c.PublicKey]; ok {
			card.HasPeer = true
			card.RxBytes = p.RxBytes
			card.TxBytes = p.TxBytes
			card.LastHandshakeText = handshakeText(p.LastHandshakeUTC)
			if p.LastHandshakeUTC != nil {
				age := now.Sub(*p.LastHandshakeUTC)
				if age <= OnlineMaxAge {
					card.Online = true
				}
			}
		} else {
			card.LastHandshakeText = "-"
		}
		card.RxText = bytesText(card.RxBytes)
		card.TxText = bytesText(card.TxBytes)
		rec.Cards = append(rec.Cards, card)
	}
	return rec
}

// Load reads the clients from SQLite and reconciles them with the
// runtime status snapshot. A database error is returned to the caller
// (it is a real server failure); status problems are expressed via the
// interface state, never via an error.
func Load(handle *sql.DB, statusPath string, now time.Time) (Reconciliation, error) {
	clients, err := db.ClientsAll(handle)
	if err != nil {
		return Reconciliation{}, fmt.Errorf("web: load clients: %w", err)
	}
	st, readErr := status.ReadStatus(statusPath)
	return Reconcile(clients, st, readErr, now), nil
}

// createdText renders the SQLite creation timestamp (RFC3339) as a
// UTC display string; unparsable stored values render as "—"
// (fail-closed, never echoed raw).
func createdText(raw string) string {
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return "-"
	}
	return t.UTC().Format("2006-01-02 15:04:05 UTC")
}

// handshakeText renders a handshake time or "—" for never.
func handshakeText(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.UTC().Format("2006-01-02 15:04:05 UTC")
}

// bytesText formats a byte count with binary units (stdlib only).
func bytesText(n uint64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	value := float64(n)
	i := 0
	for value >= 1024 && i < len(units)-1 {
		value /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.1f %s", value, units[i])
}
