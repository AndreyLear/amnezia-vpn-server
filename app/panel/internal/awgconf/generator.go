// Random generation of the full AmneziaWG obfuscation parameter set,
// mirroring what the AmneziaVPN app produces when creating a server.
//
// Everything here draws from crypto/rand and every generated value lies
// inside the ranges enforced by validateParams/validateHeaderSpec/
// validateObfChain (awgconf.go), which mirror pinned
// amneziawg-tools 1.0.20260223 (not the wider amneziawg-go parsers).
package awgconf

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

// Generator ranges (all inclusive).
const (
	genJcMin        = 1
	genJcMax        = 10
	genJminMin      = 50
	genJminMax      = 200
	genJmaxSpread   = 50 // Jmax = Jmin + [0, 50]
	genSMin         = 1
	genSMax         = 128
	genS3Min        = 15
	genS3Max        = 64
	genS4Min        = 8
	genS4Max        = 32
	genChainTagsMin = 1
	genChainTagsMax = 3
	genTagSizeMin   = 1
	genTagSizeMax   = 32
	genBBytesMin    = 1
	genBBytesMax    = 2
	genHMin         = 1000000
	genHMax         = 9999999
)

// GenerateParams builds a full random AWG 2.0 obfuscation set
// (Jc/Jmin/Jmax/S1-S4/H1-H4/I1-I5) from crypto/rand. S3 and S4 are
// required: the AmneziaVPN app treats a .conf with I1–I5 but no S3/S4
// as protocol 1.5, which does not handshake with a 2.x runtime.
// H1–H4 stay 7-digit scalars (pinned amneziawg-tools reject N-M ranges).
func GenerateParams() (*Params, error) {
	jc, err := randRange(genJcMin, genJcMax)
	if err != nil {
		return nil, err
	}
	jmin, err := randRange(genJminMin, genJminMax)
	if err != nil {
		return nil, err
	}
	spread, err := randRange(0, genJmaxSpread)
	if err != nil {
		return nil, err
	}
	s1, err := randRange(genSMin, genSMax)
	if err != nil {
		return nil, err
	}
	s2, err := randRange(genSMin, genSMax)
	if err != nil {
		return nil, err
	}
	s3, err := randRange(genS3Min, genS3Max)
	if err != nil {
		return nil, err
	}
	s4, err := randRange(genS4Min, genS4Max)
	if err != nil {
		return nil, err
	}
	headers := make([]string, 4)
	seen := make(map[string]struct{}, 4)
	for i := range headers {
		for {
			h, err := randRange(genHMin, genHMax)
			if err != nil {
				return nil, err
			}
			hs := strconv.FormatUint(h, 10)
			if _, dup := seen[hs]; dup {
				continue
			}
			seen[hs] = struct{}{}
			headers[i] = hs
			break
		}
	}
	chains := make([]string, 5)
	for i := range chains {
		chain, err := randomObfChain()
		if err != nil {
			return nil, err
		}
		chains[i] = chain
	}
	return &Params{
		Jc:   u16ptr(jc),
		Jmin: u16ptr(jmin),
		Jmax: u16ptr(jmin + spread),
		S1:   u16ptr(s1),
		S2:   u16ptr(s2),
		S3:   u16ptr(s3),
		S4:   u16ptr(s4),
		H1:   headers[0],
		H2:   headers[1],
		H3:   headers[2],
		H4:   headers[3],
		I1:   chains[0],
		I2:   chains[1],
		I3:   chains[2],
		I4:   chains[3],
		I5:   chains[4],
	}, nil
}

// MarshalParams renders p as the canonical awg_params JSON object:
// lowercase keys in a fixed order, plain integers for the numeric
// fields, strings for headers/obf chains. ParseParams accepts exactly
// this form, so MarshalParams(GenerateParams()) is a valid value for
// `server update --awg-params '<json>'`.
func MarshalParams(p *Params) (string, error) {
	out := paramsJSON{
		Jc: p.Jc, Jmin: p.Jmin, Jmax: p.Jmax, S1: p.S1, S2: p.S2, S3: p.S3, S4: p.S4,
		H1: p.H1, H2: p.H2, H3: p.H3, H4: p.H4,
		I1: p.I1, I2: p.I2, I3: p.I3, I4: p.I4, I5: p.I5,
	}
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		return "", fmt.Errorf("awgconf: marshal params: %w", err)
	}
	return strings.TrimSuffix(b.String(), "\n"), nil
}

// paramsJSON is the wire form of Params with the exact key names
// ParseParams reads (all-lowercase) and deterministic field order.
type paramsJSON struct {
	Jc   *uint16 `json:"jc,omitempty"`
	Jmin *uint16 `json:"jmin,omitempty"`
	Jmax *uint16 `json:"jmax,omitempty"`
	S1   *uint16 `json:"s1,omitempty"`
	S2   *uint16 `json:"s2,omitempty"`
	S3   *uint16 `json:"s3,omitempty"`
	S4   *uint16 `json:"s4,omitempty"`
	H1   string  `json:"h1,omitempty"`
	H2   string  `json:"h2,omitempty"`
	H3   string  `json:"h3,omitempty"`
	H4   string  `json:"h4,omitempty"`
	I1   string  `json:"i1,omitempty"`
	I2   string  `json:"i2,omitempty"`
	I3   string  `json:"i3,omitempty"`
	I4   string  `json:"i4,omitempty"`
	I5   string  `json:"i5,omitempty"`
}

// genObfTags is the pool accepted by pinned amneziawg-tools 1.0.20260223:
// t (no arg), r/rc/rd (int), b (hex). d/ds/dz are rejected by those tools.
var genObfTags = []string{"t", "r", "rc", "rd", "b"}

// randomObfChain builds one random signature-packet obfuscation chain:
// 1..3 well-formed <tag ...> blocks, e.g. "<t><r 4><b 0x1a>".
func randomObfChain() (string, error) {
	n, err := randRange(genChainTagsMin, genChainTagsMax)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for i := uint64(0); i < n; i++ {
		idx, err := randRange(0, uint64(len(genObfTags))-1)
		if err != nil {
			return "", err
		}
		tag := genObfTags[idx]
		b.WriteByte('<')
		b.WriteString(tag)
		switch tag {
		case "r", "rc", "rd":
			size, err := randRange(genTagSizeMin, genTagSizeMax)
			if err != nil {
				return "", err
			}
			b.WriteByte(' ')
			b.WriteString(strconv.FormatUint(size, 10))
		case "b":
			arg, err := randomHexArg()
			if err != nil {
				return "", err
			}
			b.WriteByte(' ')
			b.WriteString(arg)
		}
		b.WriteByte('>')
	}
	return b.String(), nil
}

// randomHexArg returns a "0x"-prefixed even-length hex byte string of
// 1..2 random bytes (the <b ...> argument format).
func randomHexArg() (string, error) {
	n, err := randRange(genBBytesMin, genBBytesMax)
	if err != nil {
		return "", err
	}
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("awgconf: crypto/rand read: %w", err)
	}
	return "0x" + hex.EncodeToString(buf), nil
}

// randRange returns a uniform random integer in [min, max] from
// crypto/rand (rejection-free via big.Int).
func randRange(min, max uint64) (uint64, error) {
	if max < min {
		return 0, fmt.Errorf("awgconf: randRange: max %d < min %d", max, min)
	}
	n, err := rand.Int(rand.Reader, new(big.Int).SetUint64(max-min+1))
	if err != nil {
		return 0, fmt.Errorf("awgconf: crypto/rand int: %w", err)
	}
	return min + n.Uint64(), nil
}

// u16ptr converts a value known to fit uint16 into a *uint16.
func u16ptr(v uint64) *uint16 {
	u := uint16(v)
	return &u
}
