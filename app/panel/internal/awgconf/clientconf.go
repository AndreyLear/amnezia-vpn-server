// Client-side AmneziaWG configuration (M4.3).
//
// GenerateClient renders the on-demand client configuration from SQLite
// state (docs/TECHNICAL_SPEC_v2.0.md §6, M4 criteria):
//
//	server row + client row + settings(endpoint) → validate → deterministic render
//
// The result is returned as bytes; nothing is ever written to disk here
// (the `client config` CLI command is added in M4.4).
package awgconf

import (
	"database/sql"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

// settingsEndpointKey is the settings key holding the client-facing
// endpoint host:port (M4 criteria; set by `panel server init`).
const settingsEndpointKey = "endpoint"

// clientAllowedIPs is the full-tunnel IPv4 route of the client config.
// IPv6 (::/0) is deliberately not added in M4.
const clientAllowedIPs = "0.0.0.0/0"

// ClientConfig is the [Interface] and [Peer] section of the client-side
// configuration. Params mirrors the server's AWG obfuscation parameters.
type ClientConfig struct {
	PrivateKey string
	Address    string
	DNS        string // empty = omit
	Params     Params
	// ServerPublicKey is the server's public key: the peer the client
	// connects to.
	ServerPublicKey string
	// PresharedKey is the client's preshared key; empty = omit.
	PresharedKey string
	// Endpoint is the client-facing server address from settings.
	Endpoint string
}

// ValidateClient checks the plain field formats of a client config.
// Errors never echo secret values.
func ValidateClient(c ClientConfig) error {
	if !validKey(c.PrivateKey) {
		return fmt.Errorf("invalid client private key: not a 32-byte base64 key")
	}
	if !validKey(c.ServerPublicKey) {
		return fmt.Errorf("invalid server public key: not a 32-byte base64 key")
	}
	if c.PresharedKey != "" && !validKey(c.PresharedKey) {
		return fmt.Errorf("invalid preshared key: not a 32-byte base64 key")
	}
	if _, _, err := net.ParseCIDR(c.Address); err != nil {
		return fmt.Errorf("invalid client address %q: not a CIDR network", c.Address)
	}
	if err := validateDNS(c.DNS); err != nil {
		return err
	}
	if err := validateEndpoint(c.Endpoint); err != nil {
		return err
	}
	return nil
}

// validateEndpoint requires a host:port pair with a numeric non-zero
// port. The endpoint is not secret and may appear in errors.
func validateEndpoint(endpoint string) error {
	if err := rejectConfControls(endpoint, "endpoint"); err != nil {
		return err
	}
	if endpoint == "" {
		return fmt.Errorf("endpoint is empty, want host:port")
	}
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil {
		return fmt.Errorf("invalid endpoint %q: want host:port", endpoint)
	}
	if host == "" {
		return fmt.Errorf("invalid endpoint %q: empty host", endpoint)
	}
	p, err := strconv.ParseUint(port, 10, 16)
	if err != nil || p == 0 {
		return fmt.Errorf("invalid endpoint %q: port must be a number in [1, 65535]", endpoint)
	}
	return nil
}

// RenderClient produces the deterministic client config text: the
// [Interface] section (PrivateKey, Address, DNS when set, the server's
// AWG J/S/H/I parameter lines in the fixed renderParams order), one
// blank line, then the [Peer] section (server PublicKey, PresharedKey
// when set, AllowedIPs = 0.0.0.0/0, Endpoint, PersistentKeepalive = 25).
// No comments, final newline, CanonicalKeyCasing.
func RenderClient(c ClientConfig) string {
	var b strings.Builder
	line := func(key, val string) {
		b.WriteString(key)
		b.WriteString(" = ")
		b.WriteString(val)
		b.WriteByte('\n')
	}
	b.WriteString("[Interface]\n")
	line("PrivateKey", c.PrivateKey)
	line("Address", c.Address)
	if c.DNS != "" {
		line("DNS", c.DNS)
	}
	for _, kv := range renderParams(c.Params) {
		line(kv[0], kv[1])
	}
	b.WriteByte('\n')
	b.WriteString("[Peer]\n")
	line("PublicKey", c.ServerPublicKey)
	if c.PresharedKey != "" {
		line("PresharedKey", c.PresharedKey)
	}
	line("AllowedIPs", clientAllowedIPs)
	line("Endpoint", c.Endpoint)
	line("PersistentKeepalive", "25")
	return b.String()
}

// GenerateClient renders the client configuration for the client with
// the given id. Server key material is taken from the server row
// (id = 1), the client's own keys from its row, and the endpoint
// exclusively from the settings key "endpoint" — there is no fallback
// to env/hostname/listen_port. The operation is on-demand for the
// concrete client record: enabled/expires_at are not consulted (unlike
// the server awg0.conf path). The result never contains anything else
// and failures never echo private/preshared keys. The returned error
// wraps db.ErrClientNotFound / awgconf.ErrNoServerRow for the callers.
func GenerateClient(handle *sql.DB, clientID int64) ([]byte, error) {
	server, err := db.ServerRow(handle)
	if errors.Is(err, db.ErrServerNotFound) {
		return nil, fmt.Errorf("client config: %w", ErrNoServerRow)
	}
	if err != nil {
		return nil, fmt.Errorf("client config: load server: %w", err)
	}
	client, err := db.ClientByID(handle, clientID)
	if err != nil {
		if errors.Is(err, db.ErrClientNotFound) {
			return nil, fmt.Errorf("client config: client %d: %w", clientID, db.ErrClientNotFound)
		}
		return nil, fmt.Errorf("client config: load client %d: %w", clientID, err)
	}
	endpoint, ok, err := db.GetSetting(handle, settingsEndpointKey)
	if err != nil {
		return nil, fmt.Errorf("client config: read endpoint setting: %w", err)
	}
	if !ok || endpoint == "" {
		return nil, fmt.Errorf("client config: settings key %q is not set (run `panel server init --endpoint <host:port>`)", settingsEndpointKey)
	}
	params, err := ParseParams(server.AWGParams)
	if err != nil {
		return nil, fmt.Errorf("client config: %w", err)
	}
	cfg := ClientConfig{
		PrivateKey:      client.PrivateKey,
		Address:         client.Address,
		DNS:             server.DNS,
		Params:          *params,
		ServerPublicKey: server.PublicKey,
		PresharedKey:    client.PresharedKey,
		Endpoint:        endpoint,
	}
	if err := ValidateClient(cfg); err != nil {
		return nil, fmt.Errorf("client config: %w", err)
	}
	return []byte(RenderClient(cfg)), nil
}
