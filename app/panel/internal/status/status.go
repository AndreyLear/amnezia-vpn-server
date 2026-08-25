// Package status implements the M5 runtime-observability contract
// (M5_AUDIT.md):
//
//   - status model (status/status.json, schema "v1");
//   - parser for the `awg show <iface> dump` output;
//   - secret-discard: private_key (interface line) and preshared_key
//     (peer line) are dropped during parsing and never enter the memory
//     model, the JSON output, logs or errors;
//   - deterministic JSON (peers are sorted by public_key) except for
//     generated_at_utc;
//   - atomic writer (temp 0600 → fsync → close → rename → fsync parent
//     dir; unique temp file so concurrent producers can never interleave).
//
// The package has no imports outside the standard library: the producer
// binary inside the awg image must build without fetching the SQLite
// dependency tree (M5_AUDIT.md §13).
package status

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// SchemaVersion is the fixed value written into every status file. A
// different value is rejected by the strict consumer parser.
const SchemaVersion = "v1"

// InterfaceDumpFields is the classic field count of the interface line of
// `awg show <iface> dump` (amneziawg-tools/src/show.c, M5_AUDIT.md §3):
//
//	private_key public_key listen_port jc jmin jmax s1 s2 s3 s4
//	h1 h2 h3 h4 i1 i2 i3 i4 i5 fwmark
const InterfaceDumpFields = 20

// InterfaceDumpFieldsExtended is the live AmneziaWG dump_print count:
// classic 0–18 (through i5), then 9 extra junk/obfuscation params, then
// fwmark at the last index (28). Port, public key and J/S stay at the
// classic offsets. Trailing fields beyond 29 are ignored except fwmark
// (always the last column).
const InterfaceDumpFieldsExtended = 29

// PeerDumpFields is the exact field count of one peer line:
//
//	public_key preshared_key endpoint allowed_ips last_handshake
//	rx_bytes tx_bytes persistent_keepalive
//
// PERSPECTIVE: rx and tx are the interface's, i.e. the SERVER's, exactly as
// wg(8) prints them ("transfer: X received, Y sent"). So rx_bytes is what
// the server received from that peer — the client's upload — and tx_bytes
// is what it sent — the client's download. Anything presenting these to a
// person has to turn them around, and the panel did not
// (amnezia-vpn-server-9l30).
const PeerDumpFields = 8

// Status is one runtime snapshot. SECURITY: the model deliberately has no
// secret fields — the interface private key and every peer preshared key
// are discarded at parse time (M5_AUDIT.md §4). All remaining fields are
// SAFE to serialize, log and compare.
type Status struct {
	Schema      string     `json:"schema"`
	GeneratedAt time.Time  `json:"generated_at_utc"`
	Interface   *Interface `json:"interface"` // null when has_interface=false
	Peers       []Peer     `json:"peers"`
}

// Interface mirrors the interface line of the dump. PublicKey is the
// interface's *public* key (SAFE); the private key never leaves the
// parser. HasInterface is always true when the interface object exists.
type Interface struct {
	Iface        string    `json:"iface"`
	HasInterface bool      `json:"has_interface"`
	PublicKey    string    `json:"public_key"`
	ListenPort   uint16    `json:"listen_port"`
	FWMark       string    `json:"fwmark"`
	AWGParams    AWGParams `json:"awg_params"`
}

// AWGParams carries only the J/S obfuscation counters (M5_AUDIT.md §5).
// The H*/I* signature-packet parameters are deliberately excluded; 0
// means "not set" and is serialized as 0 (the numbers are not secret).
type AWGParams struct {
	Jc   uint16 `json:"jc"`
	Jmin uint16 `json:"jmin"`
	Jmax uint16 `json:"jmax"`
	S1   uint16 `json:"s1"`
	S2   uint16 `json:"s2"`
	S3   uint16 `json:"s3"`
	S4   uint16 `json:"s4"`
}

// Peer mirrors one peer line of the dump. LastHandshakeUTC is null until
// the first handshake (dump `last_handshake` = 0). Endpoint, FWMark and
// PersistentKeepalive mirror the dump verbatim, including the upstream
// placeholders "(none)" and "off". All fields are SAFE.
type Peer struct {
	PublicKey           string     `json:"public_key"`
	Endpoint            string     `json:"endpoint"`
	AllowedIPs          []string   `json:"allowed_ips"`
	LastHandshakeUTC    *time.Time `json:"last_handshake_utc"`
	RxBytes             uint64     `json:"rx_bytes"`
	TxBytes             uint64     `json:"tx_bytes"`
	PersistentKeepalive string     `json:"persistent_keepalive"`
}

// Parse decodes the raw output of `awg show <iface> dump` into a Status
// snapshot. The interface line (#1) is mandatory: classic 20 fields or
// the extended live layout (≥29 fields). Every following non-empty line
// is a peer (8 fields). Secret fields are discarded while the lines are
// split and are never part of the model.
//
// generatedAt is normalized to whole UTC seconds: JSON output is fully
// deterministic for a given dump and generation time. Errors carry only
// line/field counts — never dump content, so a malformed (or hostile)
// dump cannot leak key material through an error.
func Parse(iface string, dump []byte, generatedAt time.Time) (*Status, error) {
	lines := strings.Split(string(dump), "\n")
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	if len(lines) == 0 {
		return nil, errors.New("status: dump is empty, want interface line")
	}
	ifaceFields := strings.Split(lines[0], "\t")
	fwIdx, err := interfaceFWMarkIndex(len(ifaceFields))
	if err != nil {
		return nil, err
	}

	params, err := parseAWGParams(ifaceFields[3:10])
	if err != nil {
		return nil, err
	}
	port, err := strconv.ParseUint(ifaceFields[2], 10, 16)
	if err != nil {
		return nil, fmt.Errorf("status: dump line 1: listen_port: %v", err)
	}

	st := &Status{
		Schema:      SchemaVersion,
		GeneratedAt: generatedAt.UTC().Truncate(time.Second),
		Interface: &Interface{
			Iface:        iface,
			HasInterface: true,
			// ifaceFields[0] is the interface *private* key (SECRET):
			// intentionally not read and never stored.
			// Extra dump columns (header_protection_key, I*, H*, timing
			// ranges) are not stored.
			PublicKey:  ifaceFields[1],
			ListenPort: uint16(port),
			FWMark:     ifaceFields[fwIdx],
			AWGParams:  params,
		},
		Peers: []Peer{},
	}

	for i := 1; i < len(lines); i++ {
		line := lines[i]
		if line == "" {
			continue
		}
		peer, err := parsePeer(strings.Split(line, "\t"), i+1)
		if err != nil {
			return nil, err
		}
		st.Peers = append(st.Peers, peer)
	}

	// M5_AUDIT.md §12.3: peers are sorted by public_key so repeated
	// generations produce identical JSON.
	sort.Slice(st.Peers, func(i, j int) bool {
		return st.Peers[i].PublicKey < st.Peers[j].PublicKey
	})
	return st, nil
}

// interfaceFWMarkIndex maps dump field count to the fwmark column.
// Classic 20-field lines keep fwmark at index 19. Live ≥29-field lines
// insert 9 extra params after i5, so fwmark is the last field. Counts
// between 21 and 28 are rejected (ambiguous layout). Errors report only
// counts — never dump content.
func interfaceFWMarkIndex(n int) (int, error) {
	switch {
	case n == InterfaceDumpFields:
		return InterfaceDumpFields - 1, nil
	case n >= InterfaceDumpFieldsExtended:
		return n - 1, nil
	default:
		return 0, fmt.Errorf("status: dump line 1: want %d or >= %d interface fields, got %d",
			InterfaceDumpFields, InterfaceDumpFieldsExtended, n)
	}
}

// parseAWGParams decodes dump fields 4–10 (jc jmin jmax s1 s2 s3 s4)
// into AWGParams. H*/I* fields (11–19) are discarded here and never
// parsed: h1–h5/i1–i5 are excluded from the status model by contract.
func parseAWGParams(fields []string) (AWGParams, error) {
	var p AWGParams
	names := []string{"jc", "jmin", "jmax", "s1", "s2", "s3", "s4"}
	for i, name := range names {
		v, err := strconv.ParseUint(fields[i], 10, 16)
		if err != nil {
			return p, fmt.Errorf("status: dump line 1: %s: %v", name, err)
		}
		switch i {
		case 0:
			p.Jc = uint16(v)
		case 1:
			p.Jmin = uint16(v)
		case 2:
			p.Jmax = uint16(v)
		case 3:
			p.S1 = uint16(v)
		case 4:
			p.S2 = uint16(v)
		case 5:
			p.S3 = uint16(v)
		case 6:
			p.S4 = uint16(v)
		}
	}
	return p, nil
}

// parsePeer decodes dump fields into a Peer. fields[1] is the peer's
// preshared key (SECRET): intentionally not read and never stored.
// Errors never contain field values.
func parsePeer(fields []string, lineNo int) (Peer, error) {
	if len(fields) != PeerDumpFields {
		return Peer{}, fmt.Errorf("status: dump line %d: want %d peer fields, got %d",
			lineNo, PeerDumpFields, len(fields))
	}
	handshake, err := strconv.ParseUint(fields[4], 10, 64)
	if err != nil {
		return Peer{}, fmt.Errorf("status: dump line %d: last_handshake: %v", lineNo, err)
	}
	rx, err := strconv.ParseUint(fields[5], 10, 64)
	if err != nil {
		return Peer{}, fmt.Errorf("status: dump line %d: rx_bytes: %v", lineNo, err)
	}
	tx, err := strconv.ParseUint(fields[6], 10, 64)
	if err != nil {
		return Peer{}, fmt.Errorf("status: dump line %d: tx_bytes: %v", lineNo, err)
	}

	p := Peer{
		PublicKey:           fields[0],
		Endpoint:            fields[2],
		AllowedIPs:          splitAllowedIPs(fields[3]),
		RxBytes:             rx,
		TxBytes:             tx,
		PersistentKeepalive: fields[7],
	}
	if handshake > 0 {
		utc := time.Unix(int64(handshake), 0).UTC()
		p.LastHandshakeUTC = &utc
	}
	return p, nil
}

// splitAllowedIPs splits the comma-separated allowed_ip_ranges field
// (dump joins without spaces). The "(none)" placeholder (empty list)
// becomes an empty slice.
func splitAllowedIPs(raw string) []string {
	if raw == "" || raw == "(none)" {
		return []string{}
	}
	parts := strings.Split(raw, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// noInterface builds the has_interface=false snapshot: interface is null
// and peers are an empty array (M5_AUDIT.md §5, §11.15).
func noInterface(iface string, generatedAt time.Time) *Status {
	return &Status{
		Schema:      SchemaVersion,
		GeneratedAt: generatedAt.UTC().Truncate(time.Second),
		Interface:   nil,
		Peers:       []Peer{},
	}
}

// DumpRunner fetches the raw `awg show <iface> dump` output.
type DumpRunner func(iface string) ([]byte, error)

// Generate runs one full producer generation and writes status.json
// atomically (M5_AUDIT.md §13, §16):
//
//   - dump failure (awg exit != 0, binary missing) → an
//     has_interface=false snapshot is written and Generate succeeds;
//   - dump parse failure → generic error, the previous status.json is
//     left untouched (the entrypoint keeps the last good snapshot);
//   - success → the parsed status is serialized deterministically.
//
// A failed atomic write returns an error; the previous file stays put.
func Generate(iface, outPath string, now func() time.Time, dump DumpRunner) error {
	raw, err := dump(iface)
	if err != nil {
		return WriteAtomic(outPath, mustMarshal(noInterface(iface, now())))
	}
	st, err := Parse(iface, raw, now())
	if err != nil {
		return err
	}
	return WriteAtomic(outPath, mustMarshal(st))
}

// mustMarshal serializes a Status; the model is fully deterministic and
// cannot fail to marshal, so a panic here would indicate a programmer
// error.
func mustMarshal(st *Status) []byte {
	data, err := json.Marshal(st)
	if err != nil {
		panic(fmt.Sprintf("status: marshal: %v", err))
	}
	return data
}

// WriteAtomic writes data to target atomically: a unique temporary file
// in the same directory (0600) → write → fsync → close → rename →
// fsync of the parent directory. The temp name is unique per call, so
// concurrent producers (M5_AUDIT.md §11.11) can never corrupt each
// other's rename; readers only ever observe fully-written files. The
// previous target content survives any failure before the rename.
func WriteAtomic(target string, data []byte) error {
	dir := filepath.Dir(target)
	f, err := os.CreateTemp(dir, filepath.Base(target)+".tmp-*")
	if err != nil {
		return fmt.Errorf("status: create temp in %s: %w", dir, err)
	}
	tmp := f.Name()
	cleanup := func() {
		f.Close()
		os.Remove(tmp)
	}
	if err := f.Chmod(0o600); err != nil {
		cleanup()
		return fmt.Errorf("status: chmod %s: %w", tmp, err)
	}
	if _, err := f.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("status: write %s: %w", tmp, err)
	}
	if err := f.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("status: fsync %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("status: close %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, target); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("status: rename %s -> %s: %w", tmp, target, err)
	}
	if err := syncDir(dir); err != nil {
		return fmt.Errorf("status: fsync %s: %w", dir, err)
	}
	return nil
}

// syncDir fsyncs a directory so the preceding rename is durable.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// ParseJSON strictly decodes a status.json payload into the model
// (M5_AUDIT.md §11.16): exactly the documented keys are allowed at every
// nesting level, the mandatory keys must be present, and trailing data
// is rejected. SECURITY: every diagnostic is a fixed string — field
// names, values and payload fragments of a hand-tampered file never
// reach an error message.
func ParseJSON(data []byte) (*Status, error) {
	var raw map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&raw); err != nil {
		return nil, errors.New("status: invalid JSON payload")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, errors.New("status: trailing data after JSON object")
	}
	for k := range raw {
		if !allowedTopKey[k] {
			return nil, errors.New("status: unknown top-level field")
		}
	}
	for _, k := range requiredTopKeys {
		if _, ok := raw[k]; !ok {
			return nil, fmt.Errorf("status: missing required field %s", k)
		}
	}

	var st Status
	if err := json.Unmarshal(raw["schema"], &st.Schema); err != nil {
		return nil, errors.New("status: invalid schema field")
	}
	if err := json.Unmarshal(raw["generated_at_utc"], &st.GeneratedAt); err != nil {
		return nil, errors.New("status: invalid generated_at_utc field")
	}

	var peerRaws []json.RawMessage
	if err := json.Unmarshal(raw["peers"], &peerRaws); err != nil {
		return nil, errors.New("status: invalid peers field")
	}
	if peerRaws == nil {
		return nil, errors.New("status: peers must be an array")
	}
	st.Peers = make([]Peer, 0, len(peerRaws))
	for _, elem := range peerRaws {
		var keys map[string]json.RawMessage
		if err := json.Unmarshal(elem, &keys); err != nil || keys == nil {
			return nil, errors.New("status: invalid peers element")
		}
		for k := range keys {
			if !allowedPeerKey[k] {
				return nil, errors.New("status: unknown peer field")
			}
		}
		var peer Peer
		if err := json.Unmarshal(elem, &peer); err != nil {
			return nil, errors.New("status: invalid peers element")
		}
		st.Peers = append(st.Peers, peer)
	}

	if string(bytes.TrimSpace(raw["interface"])) == "null" {
		st.Interface = nil
	} else {
		var ifRaw map[string]json.RawMessage
		if err := json.Unmarshal(raw["interface"], &ifRaw); err != nil || ifRaw == nil {
			return nil, errors.New("status: invalid interface field")
		}
		for k := range ifRaw {
			if !allowedInterfaceKey[k] {
				return nil, errors.New("status: unknown interface field")
			}
		}
		paramsRaw, ok := ifRaw["awg_params"]
		if !ok {
			return nil, errors.New("status: missing required field awg_params")
		}
		var pKeys map[string]json.RawMessage
		if err := json.Unmarshal(paramsRaw, &pKeys); err != nil || pKeys == nil {
			return nil, errors.New("status: invalid awg_params field")
		}
		for k := range pKeys {
			if !allowedParamsKey[k] {
				return nil, errors.New("status: unknown awg_params field")
			}
		}
		var iface Interface
		if err := json.Unmarshal(raw["interface"], &iface); err != nil {
			return nil, errors.New("status: invalid interface field")
		}
		if err := json.Unmarshal(paramsRaw, &iface.AWGParams); err != nil {
			return nil, errors.New("status: invalid awg_params field")
		}
		st.Interface = &iface
	}

	if err := st.validate(); err != nil {
		return nil, err
	}
	return &st, nil
}

// key whitelists enforced by ParseJSON: unknown keys at any nesting
// level are rejected with a fixed, content-free diagnostic.
var (
	allowedTopKey = map[string]bool{
		"schema": true, "generated_at_utc": true, "interface": true, "peers": true,
	}
	requiredTopKeys     = []string{"schema", "generated_at_utc", "interface", "peers"}
	allowedInterfaceKey = map[string]bool{
		"iface": true, "has_interface": true, "public_key": true,
		"listen_port": true, "fwmark": true, "awg_params": true,
	}
	allowedParamsKey = map[string]bool{
		"jc": true, "jmin": true, "jmax": true,
		"s1": true, "s2": true, "s3": true, "s4": true,
	}
	allowedPeerKey = map[string]bool{
		"public_key": true, "endpoint": true, "allowed_ips": true,
		"last_handshake_utc": true, "rx_bytes": true, "tx_bytes": true,
		"persistent_keepalive": true,
	}
)

// validate enforces the mandatory keys of the strict schema. It maps
// zero values back to "missing key" diagnostics; the JSON duplicate/filter
// behaviour cannot fabricate mandatory field values.
func (st *Status) validate() error {
	if st.Schema != SchemaVersion {
		return errors.New("status: unsupported schema version")
	}
	if st.GeneratedAt.IsZero() {
		return errors.New("status: missing required field generated_at_utc")
	}
	if st.Peers == nil {
		return errors.New("status: missing required field peers")
	}
	if st.Interface != nil && !st.Interface.HasInterface {
		return errors.New("status: interface must be null when has_interface is false")
	}
	if st.Interface != nil && st.Interface.Iface == "" {
		return errors.New("status: interface missing required field iface")
	}
	return nil
}

// ReadStatus reads a status.json path in full (io.ReadAll) and strictly
// parses it. The panel consumer is read-only and never writes status
// files. Callers can detect a missing file with errors.Is(err,
// os.ErrNotExist) — the M5 contract maps that to "na" (M5_AUDIT.md §6 E,
// §16).
func ReadStatus(path string) (*Status, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("status: open %s: %w", path, err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("status: read %s: %w", path, err)
	}
	st, err := ParseJSON(data)
	if err != nil {
		return nil, fmt.Errorf("status: parse %s: %w", path, err)
	}
	return st, nil
}

// Path returns the default status.json location; AMNEZIA_STATUS_PATH
// overrides it for tests and custom setups (mirrors AMNEZIA_CONFIG_PATH).
func Path() string {
	if p := os.Getenv("AMNEZIA_STATUS_PATH"); p != "" {
		return p
	}
	return "/status/status.json"
}
