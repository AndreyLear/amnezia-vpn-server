// Package awgconf renders the server-side AmneziaWG configuration
// (config/awg0.conf) from SQLite state. It implements the M3 contract:
//
//	DB rows → typed structs → validation → deterministic render → atomic write
//
// All validation rules mirror the pinned amneziawg-tools 1.0.20260223 and
// amneziawg-go 0.2.19 parsers (see docs/TECHNICAL_SPEC_v2.0.md §3).
package awgconf

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// ServerConfig is the [Interface] section of awg0.conf.
type ServerConfig struct {
	PrivateKey string
	Address    string
	ListenPort uint16
	DNS        string // empty = omit
	Params     Params
}

// PeerConfig is one [Peer] section of awg0.conf (an enabled client).
type PeerConfig struct {
	PublicKey    string
	PresharedKey string // empty = omit
	AllowedIPs   string
}

// Params holds the optional AWG obfuscation parameters of [Interface].
// A nil pointer means "not set": the line is not generated.
// Headers and signature packets are plain strings; an empty string means
// the line is not generated (upstream treats a missing value as disabled).
type Params struct {
	Jc   *uint16
	Jmin *uint16
	Jmax *uint16
	S1   *uint16
	S2   *uint16
	S3   *uint16
	S4   *uint16
	H1   string
	H2   string
	H3   string
	H4   string
	I1   string
	I2   string
	I3   string
	I4   string
	I5   string
}

// ParseParams decodes server.awg_params (a JSON object) into Params.
// Unknown keys are an error. Missing/null keys are left unset.
func ParseParams(raw string) (*Params, error) {
	p := &Params{}
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" {
		return p, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil, fmt.Errorf("awg_params: not a JSON object: %w", err)
	}

	uint16Field := func(key string) (*uint16, error) {
		v, ok := m[key]
		if !ok {
			return nil, nil
		}
		trimmed := strings.TrimSpace(string(v))
		if trimmed == "" || trimmed == "null" {
			return nil, nil
		}
		for i := 0; i < len(trimmed); i++ {
			if trimmed[i] < '0' || trimmed[i] > '9' {
				return nil, fmt.Errorf("awg_params.%s: %q is not an integer", key, string(v))
			}
		}
		u, err := strconv.ParseUint(trimmed, 10, 16)
		if err != nil {
			return nil, fmt.Errorf("awg_params.%s: %q is not an integer in [0, 65535]", key, trimmed)
		}
		val := uint16(u)
		return &val, nil
	}
	stringField := func(key string) (string, bool, error) {
		v, ok := m[key]
		if !ok {
			return "", false, nil
		}
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return "", false, fmt.Errorf("awg_params.%s: %q is not a string", key, string(v))
		}
		return s, true, nil
	}

	for key := range m {
		switch key {
		case "jc", "jmin", "jmax", "s1", "s2", "s3", "s4",
			"h1", "h2", "h3", "h4", "i1", "i2", "i3", "i4", "i5":
		default:
			return nil, fmt.Errorf("awg_params: unknown key %q", key)
		}
	}

	var err error
	if p.Jc, err = uint16Field("jc"); err != nil {
		return nil, err
	}
	if p.Jmin, err = uint16Field("jmin"); err != nil {
		return nil, err
	}
	if p.Jmax, err = uint16Field("jmax"); err != nil {
		return nil, err
	}
	if p.S1, err = uint16Field("s1"); err != nil {
		return nil, err
	}
	if p.S2, err = uint16Field("s2"); err != nil {
		return nil, err
	}
	if p.S3, err = uint16Field("s3"); err != nil {
		return nil, err
	}
	if p.S4, err = uint16Field("s4"); err != nil {
		return nil, err
	}
	for _, key := range []string{"h1", "h2", "h3", "h4", "i1", "i2", "i3", "i4", "i5"} {
		var s string
		var present bool
		if s, present, err = stringField(key); err != nil {
			return nil, err
		}
		if !present {
			continue
		}
		switch key {
		case "h1":
			p.H1 = s
		case "h2":
			p.H2 = s
		case "h3":
			p.H3 = s
		case "h4":
			p.H4 = s
		case "i1":
			p.I1 = s
		case "i2":
			p.I2 = s
		case "i3":
			p.I3 = s
		case "i4":
			p.I4 = s
		case "i5":
			p.I5 = s
		}
	}

	if err := validateParams(p); err != nil {
		return nil, err
	}
	return p, nil
}

// validateParams applies the ranges/relations enforced by pinned
// amneziawg-tools 1.0.20260223 (H 7-digit; I-tags t/r/rc/rd/b).
func validateParams(p *Params) error {
	if p.Jc != nil && *p.Jc < 1 {
		return fmt.Errorf("awg_params.jc must be >= 1")
	}
	if p.Jmin != nil && *p.Jmin < 1 {
		return fmt.Errorf("awg_params.jmin must be >= 1")
	}
	if p.Jmax != nil && *p.Jmax < 1 {
		return fmt.Errorf("awg_params.jmax must be >= 1")
	}
	if p.Jmin != nil && p.Jmax != nil && *p.Jmin > *p.Jmax {
		return fmt.Errorf("awg_params.jmin (%d) must be <= jmax (%d)", *p.Jmin, *p.Jmax)
	}
	for _, hv := range []string{p.H1, p.H2, p.H3, p.H4} {
		if hv == "" {
			continue
		}
		if err := validateHeaderSpec(hv); err != nil {
			return err
		}
	}
	for _, iv := range []string{p.I1, p.I2, p.I3, p.I4, p.I5} {
		if iv == "" {
			continue
		}
		if err := validateObfChain(iv); err != nil {
			return err
		}
	}
	return nil
}

// validateHeaderSpec mirrors pinned amneziawg-tools 1.0.20260223:
// a single 7-digit decimal in [1000000, 9999999]. "N-M" is rejected.
func validateHeaderSpec(spec string) error {
	if spec == "" {
		return nil
	}
	if strings.Contains(spec, "-") {
		return fmt.Errorf("awg_params header %q: bad format, want 7-digit N in [1000000, 9999999]", spec)
	}
	n, err := strconv.ParseUint(spec, 10, 32)
	if err != nil {
		return fmt.Errorf("awg_params header %q: failed to parse %q", spec, spec)
	}
	if strconv.FormatUint(n, 10) != spec {
		return fmt.Errorf("awg_params header %q: bad format, want 7-digit N in [1000000, 9999999]", spec)
	}
	if n < 1000000 || n > 9999999 {
		return fmt.Errorf("awg_params header %q: out of range, want 7-digit N in [1000000, 9999999]", spec)
	}
	return nil
}

// validateObfChain accepts a sequence of <tag ...> blocks. Text outside
// tags is ignored. Tags must match pinned amneziawg-tools 1.0.20260223.
func validateObfChain(spec string) error {
	if err := rejectConfControls(spec, "obf chain"); err != nil {
		return err
	}
	remaining := spec
	for {
		start := strings.IndexByte(remaining, '<')
		if start == -1 {
			return nil
		}
		relEnd := strings.IndexByte(remaining[start:], '>')
		if relEnd == -1 {
			return fmt.Errorf("awg_params obf chain %q: missing enclosing >", spec)
		}
		end := start + relEnd
		tag := strings.TrimSpace(remaining[start+1 : end])
		fields := strings.Fields(tag)
		if len(fields) == 0 {
			return fmt.Errorf("awg_params obf chain %q: empty tag", spec)
		}
		if err := validateObfTag(fields); err != nil {
			return fmt.Errorf("awg_params obf chain %q: tag <%s>: %w", spec, tag, err)
		}
		remaining = remaining[end+1:]
	}
}

// validateObfTag validates one <tag [arg]> block against pinned
// amneziawg-tools 1.0.20260223: t (no arg), r/rc/rd (int), b (hex).
// d, ds, and dz are rejected (those tools fail setconf on them).
func validateObfTag(fields []string) error {
	if len(fields) > 2 {
		return fmt.Errorf("unknown tag <%s>", strings.Join(fields, " "))
	}
	key, val := fields[0], ""
	if len(fields) > 1 {
		val = fields[1]
	}
	requireInt := func() error {
		if val == "" {
			return fmt.Errorf("missing size argument")
		}
		n, err := strconv.Atoi(val)
		if err != nil {
			return fmt.Errorf("invalid size %q", val)
		}
		if n < 0 {
			return fmt.Errorf("negative size %d", n)
		}
		return nil
	}
	switch key {
	case "b":
		hexStr := strings.TrimPrefix(val, "0x")
		if len(hexStr) == 0 {
			return fmt.Errorf("empty argument")
		}
		if len(hexStr)%2 != 0 {
			return fmt.Errorf("odd amount of symbols")
		}
		if _, err := hex.DecodeString(hexStr); err != nil {
			return fmt.Errorf("invalid hex %q", hexStr)
		}
		return nil
	case "t":
		if val != "" {
			return fmt.Errorf("unknown tag <%s>", strings.Join(fields, " "))
		}
		return nil
	case "r", "rc", "rd":
		return requireInt()
	default:
		return fmt.Errorf("unknown tag <%s>", key)
	}
}

// ValidateServer checks the plain field formats of the [Interface] section.
// Errors never echo secret values.
func ValidateServer(s ServerConfig) error {
	if !validKey(s.PrivateKey) {
		return fmt.Errorf("invalid private key: not a 32-byte base64 key")
	}
	if _, _, err := net.ParseCIDR(s.Address); err != nil {
		return fmt.Errorf("invalid address %q: not a CIDR network", s.Address)
	}
	if err := validateDNS(s.DNS); err != nil {
		return err
	}
	return nil
}

// rejectConfControls refuses CR/LF and other ASCII controls so a value
// cannot become an extra awg-quick key (PreUp/PostUp) when rendered.
func rejectConfControls(s, what string) error {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c == 0x7f {
			return fmt.Errorf("awg_params %s contains a control character", what)
		}
	}
	return nil
}

// validateDNS requires a comma-separated list of IP addresses (IPv4 or
// IPv6), matching CLI --dns. Empty is allowed (omit the DNS line).
func validateDNS(dns string) error {
	if dns == "" {
		return nil
	}
	if err := rejectConfControls(dns, "dns"); err != nil {
		return err
	}
	for _, part := range strings.Split(dns, ",") {
		part = strings.TrimSpace(part)
		if net.ParseIP(part) == nil {
			return fmt.Errorf("invalid dns %q: %q is not an IP address", dns, part)
		}
	}
	return nil
}

// ValidatePeer checks the plain field formats of one [Peer] section.
// Errors never echo secret values.
func ValidatePeer(p PeerConfig) error {
	if !validKey(p.PublicKey) {
		return fmt.Errorf("invalid public key: not a 32-byte base64 key")
	}
	if p.PresharedKey != "" && !validKey(p.PresharedKey) {
		return fmt.Errorf("invalid preshared key: not a 32-byte base64 key")
	}
	if _, _, err := net.ParseCIDR(p.AllowedIPs); err != nil {
		return fmt.Errorf("invalid allowed IPs %q: not a CIDR network", p.AllowedIPs)
	}
	return nil
}

// validKey reports whether s is a standard base64-encoded 32-byte key,
// mirroring amneziawg-tools/src/config.c key_from_base64.
func validKey(s string) bool {
	const keyLen = 32
	if len(s) != base64.StdEncoding.EncodedLen(keyLen) {
		return false
	}
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return false
	}
	return len(raw) == keyLen
}

// Render produces the deterministic awg0.conf text: precisely the
// [Interface] section (section header included — required by the real
// amneziawg-tools parser, src/config.c rejects keys outside a section
// with "Line unrecognized"), one blank line, then one [Peer] section
// per client in order. A nil pointer or empty string in Params
// suppresses that key line.
func Render(server ServerConfig, peers []PeerConfig) string {
	var b strings.Builder
	line := func(key, val string) {
		b.WriteString(key)
		b.WriteString(" = ")
		b.WriteString(val)
		b.WriteByte('\n')
	}
	b.WriteString("[Interface]\n")
	line("PrivateKey", server.PrivateKey)
	line("Address", server.Address)
	line("ListenPort", strconv.FormatUint(uint64(server.ListenPort), 10))
	if server.DNS != "" {
		line("DNS", server.DNS)
	}
	for _, kv := range renderParams(server.Params) {
		line(kv[0], kv[1])
	}
	b.WriteByte('\n')
	for i, peer := range peers {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("[Peer]\n")
		line("PublicKey", peer.PublicKey)
		if peer.PresharedKey != "" {
			line("PresharedKey", peer.PresharedKey)
		}
		line("AllowedIPs", peer.AllowedIPs)
	}
	return b.String()
}

// renderParams returns the set [Interface] option lines in the fixed order
// used by amneziawg-tools/src/config.c parse_interface.
func renderParams(p Params) [][2]string {
	out := make([][2]string, 0, 16)
	u16 := func(v *uint16, key string) {
		if v != nil {
			out = append(out, [2]string{key, strconv.FormatUint(uint64(*v), 10)})
		}
	}
	str := func(v, key string) {
		if v != "" {
			out = append(out, [2]string{key, v})
		}
	}
	u16(p.Jc, "Jc")
	u16(p.Jmin, "Jmin")
	u16(p.Jmax, "Jmax")
	u16(p.S1, "S1")
	u16(p.S2, "S2")
	u16(p.S3, "S3")
	u16(p.S4, "S4")
	str(p.H1, "H1")
	str(p.H2, "H2")
	str(p.H3, "H3")
	str(p.H4, "H4")
	str(p.I1, "I1")
	str(p.I2, "I2")
	str(p.I3, "I3")
	str(p.I4, "I4")
	str(p.I5, "I5")
	return out
}
