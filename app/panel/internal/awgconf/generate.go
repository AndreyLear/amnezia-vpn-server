package awgconf

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

// ErrNoServerRow is returned by Generate when the server table (id=1) is
// empty; the binary must exit 1 with this text. It is deliberately not
// wrapped so the message matches the M3 contract verbatim.
var ErrNoServerRow = errors.New("no server row (id=1); insert server configuration first")

// Generate loads the server and enabled clients from handle, renders the
// AmneziaWG configuration and writes it atomically to path (awg0.conf).
func Generate(handle *sql.DB, path string) error {
	server, err := db.ServerRow(handle)
	if err != nil {
		if errors.Is(err, db.ErrServerNotFound) {
			return ErrNoServerRow
		}
		return fmt.Errorf("load server: %w", err)
	}
	clients, err := db.ClientsForConfig(handle)
	if err != nil {
		return fmt.Errorf("load clients: %w", err)
	}

	params, err := ParseParams(server.AWGParams)
	if err != nil {
		return err
	}
	if server.ListenPort < 0 || server.ListenPort > 65535 {
		return fmt.Errorf("invalid listen port %d: must be an unsigned 16-bit value", server.ListenPort)
	}
	mtu, err := MTUFromSettings(handle)
	if err != nil {
		return err
	}
	// Интерфейс поднимается до того, что тянет сервер, а осторожное
	// значение раздаётся маршрутами (amnezia-vpn-server-wc2l). Там, где
	// потолок не измеряли, они совпадают, и файл остаётся прежним до
	// байта — вместе с поведением.
	device := DeviceMTU(mtu)
	cfg := ServerConfig{
		PrivateKey: server.PrivateKey,
		Address:    server.Address,
		Address6:   server.Address6,
		ListenPort: uint16(server.ListenPort),
		DNS:        server.DNS,
		MTU:        device,
		Params:     *params,
	}
	if device != mtu {
		cfg.ClientMTU = mtu
	}
	if err := ValidateServer(cfg); err != nil {
		return err
	}

	peers := make([]PeerConfig, 0, len(clients))
	for _, c := range clients {
		// Derived, not stored: see db.ClientAddress6. Empty whenever the
		// tunnel carries IPv4 only, which leaves the peer line byte-for-
		// byte what it has always been.
		address6, err := db.ClientAddress6(server.Address, server.Address6, c.Address)
		if err != nil {
			return fmt.Errorf("client %d: %w", c.ID, err)
		}
		peer := PeerConfig{
			PublicKey:    c.PublicKey,
			PresharedKey: c.PresharedKey,
			AllowedIPs:   c.Address,
			AllowedIPs6:  address6,
		}
		// Строка нужна только там, где размер отличается от общего:
		// одинаковое значение у каждого пира — это шум, который ещё и
		// заставил бы контейнер класть маршрут там, где он ничего не
		// меняет.
		if route := RouteMTU(uint16(c.MTU), mtu, device); route != cfg.ClientMTU && route != device {
			peer.MTU = route
		}
		if err := ValidatePeer(peer); err != nil {
			return fmt.Errorf("client %d: %w", c.ID, err)
		}
		peers = append(peers, peer)
	}

	return WriteAtomic(path, []byte(Render(cfg, peers)))
}

// WriteAtomic writes data to target atomically: a unique temporary file
// in the same directory (0600) → write → fsync → close → rename →
// fsync of the parent directory so the new config survives a crash.
// The temp name is unique per call, so concurrent Generates (web
// mutations are serialized by the server mutex, but Generate is also
// callable from other goroutines/tests) can never truncate or rename
// each other's temp file. The target keeps its old content if anything
// fails before the rename.
func WriteAtomic(target string, data []byte) error {
	dir := filepath.Dir(target)
	f, err := os.CreateTemp(dir, filepath.Base(target)+".tmp-*")
	if err != nil {
		return fmt.Errorf("awgconf: create temp in %s: %w", dir, err)
	}
	tmp := f.Name()
	cleanup := func() {
		f.Close()
		os.Remove(tmp)
	}
	if err := f.Chmod(0o600); err != nil {
		cleanup()
		return fmt.Errorf("awgconf: chmod %s: %w", tmp, err)
	}
	if _, err := f.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("awgconf: write %s: %w", tmp, err)
	}
	if err := f.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("awgconf: fsync %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("awgconf: close %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, target); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("awgconf: rename %s -> %s: %w", tmp, target, err)
	}
	if err := syncDir(dir); err != nil {
		return fmt.Errorf("awgconf: fsync %s: %w", dir, err)
	}
	return nil
}

// syncDir fsyncs a directory so the previous rename is durable.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
