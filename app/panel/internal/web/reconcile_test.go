// Pure reconciliation unit tests (M6_AUDIT.md §9): join rules, the
// strict 3-minute online boundary, interface states, ordering and
// display formatting. No HTTP, no SQLite.
package web

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/status"
)

const testPubA = "aA11wAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
const testPubB = "bB22wAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
const testPubC = "cC33wAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

var testNow = time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)

func client(id int64, name, pub string) db.ClientRecord {
	return db.ClientRecord{
		ID:        id,
		Name:      name,
		PublicKey: pub,
		Address:   "10.8.0.2/32",
		Enabled:   true,
		CreatedAt: "2026-08-01T10:00:00Z",
	}
}

func upStatus(peers []status.Peer) *status.Status {
	return &status.Status{
		Schema:      status.SchemaVersion,
		GeneratedAt: testNow.Add(-2 * time.Second),
		Interface: &status.Interface{
			Iface:        "awg0",
			HasInterface: true,
			PublicKey:    testPubA,
			ListenPort:   51820,
			FWMark:       "off",
		},
		Peers: peers,
	}
}

func peer(pub string, hs *time.Time, rx, tx uint64) status.Peer {
	return status.Peer{
		PublicKey:        pub,
		Endpoint:         "(none)",
		AllowedIPs:       []string{"10.8.0.2/32"},
		LastHandshakeUTC: hs,
		RxBytes:          rx,
		TxBytes:          tx,
	}
}

func TestReconcileOnlineBoundary(t *testing.T) {
	clients := []db.ClientRecord{client(1, "alice", testPubA)}
	cases := []struct {
		name string
		age  time.Duration
		want bool
	}{
		{"59s, online", time.Minute, true},
		{"exactly 3min, online", OnlineMaxAge, true},
		{"3min + 1ns, offline", OnlineMaxAge + time.Nanosecond, false},
		{"1h, offline", time.Hour, false},
	}
	for _, tc := range cases {
		hs := testNow.Add(-tc.age)
		rec := Reconcile(clients, upStatus([]status.Peer{peer(testPubA, &hs, 1, 2)}), nil, testNow)
		if got := rec.Cards[0].Online; got != tc.want {
			t.Errorf("%s: Online = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestReconcileNullHandshakeOffline(t *testing.T) {
	rec := Reconcile([]db.ClientRecord{client(1, "alice", testPubA)},
		upStatus([]status.Peer{peer(testPubA, nil, 0, 0)}), nil, testNow)
	c := rec.Cards[0]
	if c.Online {
		t.Error("null handshake: Online = true, want false")
	}
	if c.HasPeer != true {
		t.Error("null handshake: HasPeer = false, want true")
	}
	if c.LastHandshakeText != "-" {
		t.Errorf("LastHandshakeText = %q, want -", c.LastHandshakeText)
	}
	if c.RxBytes != 0 || c.TxBytes != 0 {
		t.Errorf("rx/tx = %d/%d, want 0/0", c.RxBytes, c.TxBytes)
	}
}

func TestReconcileClientWithoutPeerOffline(t *testing.T) {
	rec := Reconcile([]db.ClientRecord{client(1, "alice", testPubA)}, upStatus(nil), nil, testNow)
	c := rec.Cards[0]
	if c.HasPeer || c.Online {
		t.Errorf("client without peer: HasPeer=%v Online=%v, want false/false", c.HasPeer, c.Online)
	}
	if c.LastHandshakeText != "-" || c.RxText != "0 B" || c.TxText != "0 B" {
		t.Errorf("display fields wrong: hs=%q rx=%q tx=%q", c.LastHandshakeText, c.RxText, c.TxText)
	}
}

func TestReconcilePeerWithoutClientHidden(t *testing.T) {
	hs := testNow.Add(-time.Minute)
	rec := Reconcile([]db.ClientRecord{client(1, "alice", testPubA)},
		upStatus([]status.Peer{
			peer(testPubA, &hs, 1, 2),   // matches alice
			peer(testPubB, &hs, 99, 99), // no client → hidden
		}), nil, testNow)
	if len(rec.Cards) != 1 {
		t.Fatalf("cards = %d, want 1 (peer without client hidden)", len(rec.Cards))
	}
	if !rec.Cards[0].Online {
		t.Error("matching peer must be online")
	}
}

func TestReconcilePeerValuesCopied(t *testing.T) {
	hs := testNow.Add(-30 * time.Second)
	rec := Reconcile([]db.ClientRecord{client(1, "alice", testPubA)},
		upStatus([]status.Peer{peer(testPubA, &hs, 5000, 9000)}), nil, testNow)
	c := rec.Cards[0]
	if c.RxBytes != 5000 || c.TxBytes != 9000 {
		t.Errorf("rx/tx = %d/%d, want 5000/9000", c.RxBytes, c.TxBytes)
	}
	if c.RxText != "4.9 KiB" || c.TxText != "8.8 KiB" {
		t.Errorf("rx/tx text = %q/%q", c.RxText, c.TxText)
	}
	if c.LastHandshakeText != "2026-08-10 11:59:30 UTC" {
		t.Errorf("handshake text = %q", c.LastHandshakeText)
	}
	if c.CreatedText != "2026-08-01 10:00:00 UTC" {
		t.Errorf("created text = %q", c.CreatedText)
	}
}

func TestReconcileInterfaceStates(t *testing.T) {
	hs := testNow.Add(-time.Minute)
	upPeers := upStatus([]status.Peer{peer(testPubA, &hs, 0, 0)})
	down := &status.Status{Schema: status.SchemaVersion, GeneratedAt: testNow, Interface: nil}
	noClients := []db.ClientRecord{}

	rec := Reconcile(noClients, upPeers, nil, testNow)
	if rec.Interface != IfaceUp {
		t.Errorf("healthy: state = %v, want IfaceUp", rec.Interface)
	}
	if rec.GeneratedAtUTC.IsZero() {
		t.Error("healthy: GeneratedAtUTC missing")
	}

	rec = Reconcile(noClients, down, nil, testNow)
	if rec.Interface != IfaceDown {
		t.Errorf("has_interface=false: state = %v, want IfaceDown", rec.Interface)
	}

	rec = Reconcile(noClients, nil, os.ErrNotExist, testNow)
	if rec.Interface != IfaceNA {
		t.Errorf("missing file: state = %v, want IfaceNA", rec.Interface)
	}

	rec = Reconcile(noClients, nil, errors.New("boom"), testNow)
	if rec.Interface != IfaceError {
		t.Errorf("parse error: state = %v, want IfaceError", rec.Interface)
	}
	if rec.GeneratedAtUTC != (time.Time{}) {
		t.Error("error state must not carry a timestamp")
	}
}

func TestReconcileOrderMatchesClients(t *testing.T) {
	hs := testNow.Add(-time.Minute)
	clients := []db.ClientRecord{
		client(1, "aaa", testPubA),
		client(2, "bbb", testPubB),
		client(3, "ccc", testPubC),
	}
	// only the second client has a peer; order must be preserved
	rec := Reconcile(clients, upStatus([]status.Peer{peer(testPubB, &hs, 0, 0)}), nil, testNow)
	var names []string
	for _, c := range rec.Cards {
		names = append(names, c.Name)
	}
	if len(names) != 3 || names[0] != "aaa" || names[1] != "bbb" || names[2] != "ccc" {
		t.Fatalf("order = %v, want [aaa bbb ccc]", names)
	}
	if rec.Cards[1].Online != true || rec.Cards[0].Online || rec.Cards[2].Online {
		t.Errorf("online flags wrong: %v %v %v", rec.Cards[0].Online, rec.Cards[1].Online, rec.Cards[2].Online)
	}
}

func TestReconcileExpiredShown(t *testing.T) {
	expired := client(1, "old", testPubA)
	expired.Enabled = false
	expired.ExpiresAt = "2026-08-01T00:00:00Z"
	rec := Reconcile([]db.ClientRecord{expired}, upStatus(nil), nil, testNow)
	c := rec.Cards[0]
	if len(rec.Cards) != 1 {
		t.Fatalf("expired client hidden: %d cards", len(rec.Cards))
	}
	if !c.Expired {
		t.Error("expired client: Expired = false")
	}
	if c.Enabled {
		t.Error("enabled flag must be preserved as stored")
	}
}

func TestBytesText(t *testing.T) {
	cases := []struct {
		n    uint64
		want string
	}{
		{0, "0 B"},
		{999, "999 B"},
		{1024, "1.0 KiB"},
		{5 * 1024, "5.0 KiB"},
		{1024 * 1024, "1.0 MiB"},
		{1024 * 1024 * 1024, "1.0 GiB"},
	}
	for _, tc := range cases {
		if got := bytesText(tc.n); got != tc.want {
			t.Errorf("bytesText(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

func TestCreatedTextMalformed(t *testing.T) {
	if got := createdText("not-a-date"); got != "-" {
		t.Errorf("createdText(malformed) = %q, want -", got)
	}
	if got := createdText(""); got != "-" {
		t.Errorf("createdText(empty) = %q, want -", got)
	}
}
