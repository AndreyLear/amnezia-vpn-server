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
	cfg := ServerConfig{
		PrivateKey: server.PrivateKey,
		Address:    server.Address,
		ListenPort: uint16(server.ListenPort),
		DNS:        server.DNS,
		Params:     *params,
	}
	if err := ValidateServer(cfg); err != nil {
		return err
	}

	peers := make([]PeerConfig, 0, len(clients))
	for _, c := range clients {
		peer := PeerConfig{
			PublicKey:    c.PublicKey,
			PresharedKey: c.PresharedKey,
			AllowedIPs:   c.Address,
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
