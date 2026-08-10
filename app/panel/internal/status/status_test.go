package status

// M5 unit tests: test matrix of M5_AUDIT.md §11, items 1–17.
//
//	1  happy path dump → model → deterministic JSON roundtrip
//	2  (none) / (null) / off placeholders
//	3  IPv6 endpoint
//	4  allowed_ip lists; keepalive off/number
//	5  last_handshake 0 → null, >0 → ISO8601 UTC
//	6  secret discard: private_key / preshared_key absent from JSON and model
//	7  dump runner failure → has_interface=false file (Generate fails-open)
//	8  interface-only dump → peers []
//	9  250 peers parse correctly and deterministically
//	10 repeated generation is byte-identical except generated_at_utc
//	11+12 concurrent producers/readers: no corrupted or partial file
//	13 file mode 0600 (written and after concurrent load)
//	14 M3.2 lifecycle → covered by app/awg/test_m32.sh (not unit-testable)
//	15 has_interface=false serializes as interface=null, peers=[]
//	16 schema-strict JSON validation (mandatory keys, unknown fields)
//	17 generated_at_utc ISO8601 UTC, seconds precision

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fixedNow is the generation clock injected into every parse call.
var fixedNow = time.Date(2026, 8, 10, 12, 0, 5, 0, time.UTC)

// formKey returns a deterministic 32-byte base64 key: the byte b
// repeated 32 times. Used to build deliberately marked key material.
func formKey(b byte) string {
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{b}, 32))
}

// marked secrets: uniquely identifiable values that must never leave
// the parser.
const (
	markedPrivateKey   = "PRIV-KEY-0xAB-MARKED"
	markedPresharedKey = "PSH-KEY-0xCD-MARKED"
)

var (
	peerPubA = formKey(0x11)
	peerPubB = formKey(0x22)
	peerPubC = formKey(0x33)
)

// ifaceDump renders the 20-field interface line: private_key ← SECRET,
// public_key, listen_port, jc..s4, h1..h4 (null), i1..i5 (null), fwmark.
func ifaceDump(pub, priv string, port int, fwmark string) string {
	return strings.Join([]string{
		priv,
		pub,
		itoa(port),
		"3", "21", "31", "904", "737", "0", "0", // jc jmin jmax s1 s2 s3 s4
		"(null)", "(null)", "(null)", "(null)", // h1..h4
		"(null)", "(null)", "(null)", "(null)", "(null)", // i1..i5
		fwmark,
	}, "\t")
}

// peerDump renders the 8-field peer line: public_key, preshared_key ←
// SECRET, endpoint, allowed_ips, last_handshake, rx, tx, keepalive.
func peerDump(pub, psk, endpoint, allowed string, hs, rx, tx int64, keepalive string) string {
	return strings.Join([]string{
		pub, psk, endpoint, allowed,
		itoa64(hs), itoa64(rx), itoa64(tx), keepalive,
	}, "\t")
}

func itoa(n int) string { return strconv.Itoa(n) }

func itoa64(n int64) string { return strconv.FormatInt(n, 10) }

func TestParseHappyPathAndRoundtrip(t *testing.T) {
	hs := time.Date(2025, 11, 3, 8, 30, 0, 0, time.UTC).Unix()
	dump := ifaceDump(formKey(0xAA), markedPrivateKey, 51820, "off") + "\n" +
		peerDump(peerPubC, markedPresharedKey, "192.0.2.10:51820", "10.8.0.4/32", hs, 900, 1200, "off") + "\n" +
		peerDump(peerPubA, "0xCD-marker-2", "198.51.100.1:51820", "10.8.0.2/32", 0, 10, 20, "25") + "\n" +
		peerDump(peerPubB, "0xCD-marker-3", "203.0.113.7:51820", "10.8.0.3/32,fd00::3/128", hs, 5, 6, "off") + "\n"

	st, err := Parse("awg0", []byte(dump), fixedNow)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if st.Schema != "v1" {
		t.Fatalf("schema = %q", st.Schema)
	}
	if st.Interface == nil || !st.Interface.HasInterface || st.Interface.Iface != "awg0" {
		t.Fatalf("interface = %+v", st.Interface)
	}
	if st.Interface.ListenPort != 51820 || st.Interface.FWMark != "off" {
		t.Fatalf("interface = %+v", st.Interface)
	}
	// peers must be sorted by public_key (M5_AUDIT.md §12.3)
	if len(st.Peers) != 3 {
		t.Fatalf("peers = %d, want 3", len(st.Peers))
	}
	if st.Peers[0].PublicKey != peerPubA || st.Peers[1].PublicKey != peerPubB || st.Peers[2].PublicKey != peerPubC {
		t.Fatalf("peer order = %q, %q, %q (want sorted)", st.Peers[0].PublicKey, st.Peers[1].PublicKey, st.Peers[2].PublicKey)
	}
	if st.Peers[0].Endpoint != "198.51.100.1:51820" || st.Peers[0].PersistentKeepalive != "25" {
		t.Fatalf("peer A = %+v", st.Peers[0])
	}
	if st.Peers[0].LastHandshakeUTC != nil {
		t.Fatalf("peer A last handshake = %v, want null", *st.Peers[0].LastHandshakeUTC)
	}
	if st.Peers[1].LastHandshakeUTC == nil || !st.Peers[1].LastHandshakeUTC.Equal(time.Unix(hs, 0).UTC()) {
		t.Fatalf("peer B last handshake = %v", st.Peers[1].LastHandshakeUTC)
	}
	if want := []string{"10.8.0.3/32", "fd00::3/128"}; !reflect.DeepEqual(st.Peers[1].AllowedIPs, want) {
		t.Fatalf("peer B allowed_ips = %v, want %v", st.Peers[1].AllowedIPs, want)
	}
	if st.Peers[1].RxBytes != 5 || st.Peers[1].TxBytes != 6 {
		t.Fatalf("peer B counters = %+v", st.Peers[1])
	}

	// item 1: serialize, then strict-parse, then re-serialize: the bytes
	// must be identical (dump → JSON → model → JSON roundtrip).
	json1 := mustMarshal(st)
	st2, err := ParseJSON(json1)
	if err != nil {
		t.Fatalf("ParseJSON: %v", err)
	}
	if !st2.GeneratedAt.Equal(fixedNow) {
		t.Fatalf("roundtrip generated_at = %v", st2.GeneratedAt)
	}
	if !bytes.Equal(mustMarshal(st2), json1) {
		t.Fatal("JSON is not roundtrip-stable")
	}
}

func TestParseGoldenJSON(t *testing.T) {
	hs := time.Date(2025, 11, 3, 8, 30, 0, 0, time.UTC).Unix()
	dump := ifaceDump(formKey(0xAA), markedPrivateKey, 51820, "off") + "\n" +
		peerDump(peerPubA, markedPresharedKey, "(none)", "(none)", 0, 0, 0, "off") + "\n" +
		peerDump(peerPubB, "x", "192.0.2.1:51820", "10.8.0.2/32", hs, 100, 200, "off")

	st, err := Parse("awg0", []byte(dump), fixedNow)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := `{"schema":"v1","generated_at_utc":"2026-08-10T12:00:05Z","interface":{"iface":"awg0","has_interface":true,"public_key":"` +
		formKey(0xAA) + `","listen_port":51820,"fwmark":"off","awg_params":{"jc":3,"jmin":21,"jmax":31,"s1":904,"s2":737,"s3":0,"s4":0}},` +
		`"peers":[{"public_key":"` + peerPubA + `","endpoint":"(none)","allowed_ips":[],"last_handshake_utc":null,"rx_bytes":0,"tx_bytes":0,"persistent_keepalive":"off"},` +
		`{"public_key":"` + peerPubB + `","endpoint":"192.0.2.1:51820","allowed_ips":["10.8.0.2/32"],"last_handshake_utc":"2025-11-03T08:30:00Z","rx_bytes":100,"tx_bytes":200,"persistent_keepalive":"off"}]}`
	if got := string(mustMarshal(st)); got != want {
		t.Fatalf("golden JSON mismatch:\n got: %s\nwant: %s", got, want)
	}
}

// item 2: placeholders (none) / (null) / off must parse without error
// and appear in the model exactly as the dump reported them.
func TestParsePlaceholders(t *testing.T) {
	dump := ifaceDump(formKey(0xAA), markedPrivateKey, 51820, "off") + "\n" +
		peerDump(peerPubA, markedPresharedKey, "(none)", "(none)", 0, 0, 0, "off") + "\n" +
		peerDump(peerPubB, "y", "(none)", "10.8.0.2/32", 0, 0, 0, "25")

	st, err := Parse("awg0", []byte(dump), fixedNow)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if st.Interface.FWMark != "off" {
		t.Fatalf("fwmark = %q, want off", st.Interface.FWMark)
	}
	if st.Peers[0].Endpoint != "(none)" || st.Peers[0].PersistentKeepalive != "off" {
		t.Fatalf("peer with placeholders = %+v", st.Peers[0])
	}
	if len(st.Peers[0].AllowedIPs) != 0 {
		t.Fatalf("allowed_ips = %v, want []", st.Peers[0].AllowedIPs)
	}
	if st.Peers[1].PersistentKeepalive != "25" {
		t.Fatalf("keepalive = %q, want 25", st.Peers[1].PersistentKeepalive)
	}
	// item 2 part 2: the JSON must stay valid after the roundtrip.
	if _, err := ParseJSON(mustMarshal(st)); err != nil {
		t.Fatalf("roundtrip: %v", err)
	}
}

// item 3: IPv6 endpoint is reported as [addr]:port and preserved.
func TestParseIPv6Endpoint(t *testing.T) {
	dump := ifaceDump(formKey(0xAA), markedPrivateKey, 51820, "off") + "\n" +
		peerDump(peerPubA, markedPresharedKey, "[fd00:1::10]:51820", "fd00::2/128", 0, 0, 0, "off")
	st, err := Parse("awg0", []byte(dump), fixedNow)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if st.Peers[0].Endpoint != "[fd00:1::10]:51820" {
		t.Fatalf("endpoint = %q", st.Peers[0].Endpoint)
	}
	if want := []string{"fd00::2/128"}; !reflect.DeepEqual(st.Peers[0].AllowedIPs, want) {
		t.Fatalf("allowed_ips = %v", st.Peers[0].AllowedIPs)
	}
}

// items 4–5: allowed_ips splitting, keepalive, last_handshake mapping.
func TestParseAllowedIPsAndHandshake(t *testing.T) {
	dump := ifaceDump(formKey(0xAA), markedPrivateKey, 51820, "0xca6c") + "\n" +
		peerDump(peerPubA, markedPresharedKey, "192.0.2.9:51820", "10.8.0.2/32,fd00::2/128,10.8.0.4/32", 0, 7, 8, "off")
	st, err := Parse("awg0", []byte(dump), fixedNow)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if st.Interface.FWMark != "0xca6c" {
		t.Fatalf("fwmark = %q, want mirror 0xca6c", st.Interface.FWMark)
	}
	want := []string{"10.8.0.2/32", "fd00::2/128", "10.8.0.4/32"}
	if !reflect.DeepEqual(st.Peers[0].AllowedIPs, want) {
		t.Fatalf("allowed_ips = %v", st.Peers[0].AllowedIPs)
	}
	if st.Peers[0].LastHandshakeUTC != nil {
		t.Fatalf("handshake = %v, want null for 0", st.Peers[0].LastHandshakeUTC)
	}
	if st.Peers[0].RxBytes != 7 || st.Peers[0].TxBytes != 8 {
		t.Fatalf("counters = %+v", st.Peers[0])
	}

	// handshake > 0 → ISO8601 UTC with seconds precision
	hs := time.Date(2026, 2, 28, 23, 59, 59, 0, time.UTC)
	dump2 := ifaceDump(formKey(0xAA), markedPrivateKey, 51820, "off") + "\n" +
		peerDump(peerPubA, markedPresharedKey, "192.0.2.9:51820", "(none)", hs.Unix(), 0, 0, "off")
	st2, err := Parse("awg0", []byte(dump2), fixedNow)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if st2.Peers[0].LastHandshakeUTC == nil || !st2.Peers[0].LastHandshakeUTC.Equal(hs) {
		t.Fatalf("handshake = %v, want %v", st2.Peers[0].LastHandshakeUTC, hs)
	}
	if got := st2.Peers[0].LastHandshakeUTC.Format(time.RFC3339); got != "2026-02-28T23:59:59Z" {
		t.Fatalf("handshake format = %q", got)
	}
}

// item 6: secret discard. Marked private/preshared keys must be absent
// from the JSON output and from the memory model.
func TestSecretDiscardFromJSON(t *testing.T) {
	dump := ifaceDump(formKey(0xAA), markedPrivateKey, 51820, "off") + "\n" +
		peerDump(peerPubA, markedPresharedKey, "192.0.2.1:51820", "10.8.0.2/32", 0, 0, 0, "off")
	st, err := Parse("awg0", []byte(dump), fixedNow)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	jsonBytes := mustMarshal(st)
	if bytes.Contains(jsonBytes, []byte(markedPrivateKey)) || bytes.Contains(jsonBytes, []byte(markedPresharedKey)) {
		t.Fatalf("JSON leaks a secret:\n%s", jsonBytes)
	}
	if strings.Contains(string(jsonBytes), "private_key") || strings.Contains(string(jsonBytes), "preshared_key") {
		t.Fatalf("JSON contains a key-material field:\n%s", jsonBytes)
	}
	// the same holds for the full roundtrip and for the model prints
	if _, err := ParseJSON(jsonBytes); err != nil {
		t.Fatalf("ParseJSON: %v", err)
	}
}

// item 6 part 2: the model types must have no secret-carrying fields at
// all (guarantee that a future refactor cannot reintroduce them).
func TestModelHasNoSecretFields(t *testing.T) {
	for _, typ := range []reflect.Type{
		reflect.TypeOf(Status{}),
		reflect.TypeOf(Interface{}),
		reflect.TypeOf(AWGParams{}),
		reflect.TypeOf(Peer{}),
	} {
		for i := 0; i < typ.NumField(); i++ {
			name := typ.Field(i).Name
			if strings.Contains(name, "Private") || strings.Contains(name, "Preshared") || strings.Contains(name, "Secret") {
				t.Fatalf("%s has a secret-carrying field %s", typ, name)
			}
		}
	}
}

// item 7: dump runner failure → has_interface=false snapshot is written,
// Generate succeeds (the producer must not die with the runtime).
func TestGenerateDumpFailureWritesNoInterface(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "status.json")
	fail := func(string) ([]byte, error) {
		return nil, errors.New("exit status 1: Unable to access interface")
	}
	if err := Generate("awg0", target, func() time.Time { return fixedNow }, fail); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if mode := fileMode(t, target); mode != 0o600 {
		t.Fatalf("mode = %o, want 600", mode)
	}
	st, err := ParseJSON(data)
	if err != nil {
		t.Fatalf("ParseJSON: %v", err)
	}
	if st.Interface != nil || len(st.Peers) != 0 {
		t.Fatalf("interface = %+v, peers = %+v; want null/[]", st.Interface, st.Peers)
	}
}

// item 15: the has_interface=false payload serializes as interface=null
// and peers=[] verbatim.
func TestNoInterfaceJSONShape(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "status.json")
	if err := Generate("awg0", target, func() time.Time { return fixedNow },
		func(string) ([]byte, error) { return nil, errors.New("down") }); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	raw, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !strings.Contains(string(m["interface"]), "null") {
		t.Fatalf("interface = %s, want null", m["interface"])
	}
	if !strings.Contains(string(m["peers"]), "[") || strings.Contains(string(m["peers"]), "null") {
		t.Fatalf("peers = %s, want []", m["peers"])
	}
}

// item 8: an interface-only dump yields peers=[] (empty array, not null).
func TestParseInterfaceOnly(t *testing.T) {
	dump := ifaceDump(formKey(0xAA), markedPrivateKey, 51820, "off")
	st, err := Parse("awg0", []byte(dump), fixedNow)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if st.Peers == nil || len(st.Peers) != 0 {
		t.Fatalf("peers = %v", st.Peers)
	}
	if got := mustMarshal(st); !strings.Contains(string(got), `"peers":[]`) {
		t.Fatalf("JSON peers = %s", got)
	}
}

// item 9: a large config (250 peers) parses correctly and quickly.
func TestParseClusterSizedConfig250Peers(t *testing.T) {
	var b strings.Builder
	b.WriteString(ifaceDump(formKey(0xAA), markedPrivateKey, 51820, "off"))
	b.WriteString("\n")
	for i := 0; i < 250; i++ {
		pub := formKey(byte(1 + i%250))
		b.WriteString(peerDump(pub, markedPresharedKey,
			"192.0.2."+itoa(1+i%200)+":51820", "10.8."+itoa(i/200)+"."+itoa(i%200)+"/32",
			int64(i), int64(i*7), int64(i*11), "off"))
		b.WriteString("\n")
	}
	start := time.Now()
	st, err := Parse("awg0", []byte(b.String()), fixedNow)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(st.Peers) != 250 {
		t.Fatalf("peers = %d, want 250", len(st.Peers))
	}
	// 250 peers must stay sorted
	for i := 1; i < len(st.Peers); i++ {
		if st.Peers[i-1].PublicKey > st.Peers[i].PublicKey {
			t.Fatalf("peers not sorted at %d", i)
		}
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("parsing 250 peers too slow: %v", elapsed)
	}
	if _, err := ParseJSON(mustMarshal(st)); err != nil {
		t.Fatalf("roundtrip: %v", err)
	}
}

// item 10: repeated generation is byte-identical except generated_at_utc.
func TestGenerationDeterministic(t *testing.T) {
	dump := ifaceDump(formKey(0xAA), markedPrivateKey, 51820, "off") + "\n" +
		peerDump(peerPubB, markedPresharedKey, "192.0.2.1:51820", "10.8.0.2/32", 0, 1, 2, "off")

	nowA := time.Date(2026, 8, 10, 12, 0, 5, 0, time.UTC)
	nowB := time.Date(2026, 8, 10, 12, 5, 0, 0, time.UTC)

	dir := t.TempDir()
	target := filepath.Join(dir, "status.json")
	if err := Generate("awg0", target, func() time.Time { return nowA },
		func(string) ([]byte, error) { return dumpBytes(dump) }); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	first, _ := os.ReadFile(target)
	if err := Generate("awg0", target, func() time.Time { return nowB },
		func(string) ([]byte, error) { return dumpBytes(dump) }); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	second, _ := os.ReadFile(target)

	// identical once each file's own timestamp is masked (items 10/12)
	re := regexp.MustCompile(`"generated_at_utc":"[^"]+"`)
	if re.ReplaceAllString(string(first), `"generated_at_utc":"<t>"`) !=
		re.ReplaceAllString(string(second), `"generated_at_utc":"<t>"`) {
		t.Fatalf("nondeterministic output:\n%s\n%s", first, second)
	}
	if bytes.Equal(first, second) {
		t.Fatal("generated_at_utc did not advance between generations")
	}
}

// items 11–12: concurrent producers plus a hot reader — every read must
// observe either the old or the new complete file, never a partial one;
// no temp files may remain; final mode is 0600.
func TestConcurrentProducersAndReaders(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "status.json")
	payloads := make([][]byte, 3)
	for i := range payloads {
		st := &Status{
			Schema:      SchemaVersion,
			GeneratedAt: fixedNow.Add(time.Duration(i) * time.Second),
			Interface: &Interface{
				Iface: "awg0", HasInterface: true, PublicKey: formKey(byte(0x40 + i)),
				ListenPort: uint16(51820 + i), FWMark: "off",
				AWGParams: AWGParams{Jc: 3, Jmin: 21, Jmax: 31, S1: 904, S2: 737},
			},
			Peers: []Peer{{PublicKey: peerPubA, Endpoint: "192.0.2.1:51820",
				AllowedIPs: []string{"10.8.0.2/32"}, PersistentKeepalive: "off"}},
		}
		payloads[i] = mustMarshal(st)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	var readErrs []string
	var mu sync.Mutex
	done := make(chan struct{})

	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			data, err := os.ReadFile(target)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				mu.Lock()
				readErrs = append(readErrs, err.Error())
				mu.Unlock()
				return
			}
			ok := false
			for _, p := range payloads {
				if bytes.Equal(data, p) {
					ok = true
					break
				}
			}
			if !ok {
				mu.Lock()
				readErrs = append(readErrs, "observed a partial/corrupt file")
				mu.Unlock()
				return
			}
		}
	}()

	for w := 0; w < 2; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				if err := WriteAtomic(target, payloads[(w+i)%len(payloads)]); err != nil {
					t.Errorf("writer %d: %v", w, err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(stop)
	<-done

	mu.Lock()
	defer mu.Unlock()
	if len(readErrs) > 0 {
		t.Fatalf("reader saw an inconsistent file: %v", readErrs)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
	if mode := fileMode(t, target); mode != 0o600 {
		t.Fatalf("final mode = %o, want 600", mode)
	}
	if _, err := ParseJSON(mustRaw(t, target)); err != nil {
		t.Fatalf("final file does not parse: %v", err)
	}
}

// item 13: a single atomic write already produces mode 0600.
func TestWriteAtomicMode0600(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "status.json")
	if err := WriteAtomic(target, []byte(`{"schema":"v1"}`)); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	if mode := fileMode(t, target); mode != 0o600 {
		t.Fatalf("mode = %o, want 600", mode)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("temp left: %s", e.Name())
		}
	}
}

// item 16: strict JSON parsing rejects unknown fields, missing mandatory
// keys, a null peers array and trailing garbage — generically, without
// echoing the payload.
func TestParseJSONStrict(t *testing.T) {
	good := `{"schema":"v1","generated_at_utc":"2026-08-10T12:00:05Z","interface":{"iface":"awg0","has_interface":true,"public_key":"` +
		formKey(0x11) + `","listen_port":51820,"fwmark":"off","awg_params":{"jc":3}},"peers":[]}`

	cases := []struct {
		name string
		json string
	}{
		{"valid baseline", good},
		{"unknown interface field", `{"schema":"v1","generated_at_utc":"2026-08-10T12:00:05Z","interface":{"iface":"awg0","has_interface":true,"public_key":"a","private_key":"secret","listen_port":51820,"fwmark":"off","awg_params":{}},"peers":[]}`},
		{"unknown top-level field", `{"schema":"v1","generated_at_utc":"2026-08-10T12:00:05Z","interface":null,"peers":[],"secret_stash":"s3cr3t"}`},
		{"missing schema", `{"generated_at_utc":"2026-08-10T12:00:05Z","interface":null,"peers":[]}`},
		{"wrong schema", `{"schema":"v0","generated_at_utc":"2026-08-10T12:00:05Z","interface":null,"peers":[]}`},
		{"missing generated_at_utc", `{"schema":"v1","interface":null,"peers":[]}`},
		{"null peers", `{"schema":"v1","generated_at_utc":"2026-08-10T12:00:05Z","interface":null,"peers":null}`},
		{"has_interface false with object", `{"schema":"v1","generated_at_utc":"2026-08-10T12:00:05Z","interface":{"iface":"awg0","has_interface":false,"public_key":"a","listen_port":1,"fwmark":"off","awg_params":{}},"peers":[]}`},
		{"trailing garbage", good + ` garbage`},
		{"not an object", `[1,2,3]`},
		{"non-numeric port", `{"schema":"v1","generated_at_utc":"2026-08-10T12:00:05Z","interface":{"iface":"awg0","has_interface":true,"public_key":"a","listen_port":"high","fwmark":"off","awg_params":{}},"peers":[]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseJSON([]byte(tc.json))
			if tc.name == "valid baseline" {
				if err != nil {
					t.Fatalf("baseline rejected: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("accepted an invalid payload")
			}
			// errors never echo payload values (only field names and
			// generic diagnostics are allowable)
			if strings.Contains(err.Error(), "s3cr3t") {
				t.Fatalf("error leaks payload value: %v", err)
			}
		})
	}
}

// item 16 part 2: a deterministically produced file always validates.
func TestParseJSONAcceptsProducerOutput(t *testing.T) {
	dump := ifaceDump(formKey(0xAA), markedPrivateKey, 51820, "off") + "\n" +
		peerDump(peerPubA, markedPresharedKey, "192.0.2.1:51820", "10.8.0.2/32", 0, 0, 0, "off")
	st, err := Parse("awg0", []byte(dump), fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseJSON(mustMarshal(st)); err != nil {
		t.Fatalf("producer output rejected: %v", err)
	}
}

// item 17: generated_at_utc is ISO8601 UTC with seconds precision and
// never later than the generation clock + 2s.
func TestGeneratedAtISO8601(t *testing.T) {
	dump := ifaceDump(formKey(0xAA), markedPrivateKey, 51820, "off")
	st, err := Parse("awg0", []byte(dump), fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	var raw struct {
		GeneratedAt string `json:"generated_at_utc"`
	}
	if err := json.Unmarshal(mustMarshal(st), &raw); err != nil {
		t.Fatal(err)
	}
	parsed, err := time.Parse(time.RFC3339, raw.GeneratedAt)
	if err != nil {
		t.Fatalf("generated_at_utc %q not ISO8601: %v", raw.GeneratedAt, err)
	}
	if !strings.HasSuffix(raw.GeneratedAt, "Z") {
		t.Fatalf("generated_at_utc %q is not UTC", raw.GeneratedAt)
	}
	if parsed.After(fixedNow.Add(2 * time.Second)) {
		t.Fatalf("generated_at_utc %q later than now+2s", raw.GeneratedAt)
	}
	if diff := fixedNow.Sub(parsed); diff < 0 || diff > time.Second {
		t.Fatalf("generated_at %q != injected clock %v", raw.GeneratedAt, fixedNow)
	}
	// no sub-second noise
	if st.GeneratedAt.Nanosecond() != 0 {
		t.Fatalf("nanosecond = %d, want 0", st.GeneratedAt.Nanosecond())
	}
}

// parse errors must never carry dump content (secret or not), only
// line/field counts.
func TestParseErrorsNeverEchoContent(t *testing.T) {
	secret := formKey(0xEF)
	cases := []struct {
		name string
		dump string
	}{
		{"empty dump", ""},
		{"blank dump", "\n\n\n"},
		{"short interface line", "only\n" + peerDump(peerPubA, markedPresharedKey, "e", "a", 0, 0, 0, "off")},
		{"short peer line", ifaceDump(formKey(0xAA), markedPrivateKey, 51820, "off") + "\n" +
			peerPubA + "\t" + markedPresharedKey + "\tendpoint"},
		{"bad handshake", ifaceDump(formKey(0xAA), markedPrivateKey, 51820, "off") + "\n" +
			strings.Join([]string{peerPubA, markedPresharedKey, "e", "a", "garbage", "0", "0", "off"}, "\t")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse("awg0", []byte(tc.dump), fixedNow)
			if err == nil {
				t.Fatal("parse succeeded, want error")
			}
			if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), markedPrivateKey) {
				t.Fatalf("error leaks key material: %v", err)
			}
			for _, tok := range []string{markedPrivateKey, markedPresharedKey, secret} {
				if strings.Contains(err.Error(), tok) {
					t.Fatalf("error leaks %q", tok)
				}
			}
		})
	}
}

// a failed write must leave the previous status.json untouched.
func TestWriteAtomicFailureKeepsOldFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "status.json")
	if err := WriteAtomic(target, []byte("old")); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(filepath.Join(dir, "missing", "status.json"), []byte("new")); err == nil {
		t.Fatal("write into a missing directory must fail")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old" {
		t.Fatalf("target overwritten by failing write: %q", got)
	}
}

// ---------- helpers ----------

func dumpBytes(s string) ([]byte, error) { return []byte(s), nil }

func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	return fi.Mode().Perm()
}

func mustRaw(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
