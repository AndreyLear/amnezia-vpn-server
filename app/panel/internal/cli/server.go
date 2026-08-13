package cli

import (
	"errors"
	"fmt"
	"net"
	"strconv"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/awgconf"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/backup"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/keys"
)

const opServerInit = "server init"

const opServerUpdate = "server update"

// cmdServer dispatches the `server` subcommands. listen_port itself is
// set once at init and is changeable only via a full restart (awg
// syncconf does not apply ListenPort on hot reload,
// docs/TECHNICAL_SPEC_v2.0.md §10 M4).
func (a *app) cmdServer(args []string) int {
	if len(args) == 0 {
		a.usage()
		return 2
	}
	switch args[0] {
	case "init":
		return a.cmdServerInit(args[1:])
	default:
		a.usage()
		return 2
	}
}

// cmdServerInit bootstraps the server row (id = 1) with a fresh X25519
// key pair and the client-facing endpoint in settings.
//
// `panel server init <address> <listen-port> [--dns <dns>] [--awg-params <json>] [--endpoint <host:port>]`
//
// All arguments are validated before the database is touched; awg_params
// is pre-flighted through the exported ParseParams so a broken value can
// never be stored. Only then db.CreateServer runs, followed by config
// regeneration. A failed Generate returns exit 1 with the old awg0.conf
// intact (WriteAtomic); the database is not rolled back (the config is
// derived state). Without --endpoint the command still succeeds but warns
// on stderr. A repeated init fails with db.ErrServerExists.
func (a *app) cmdServerInit(args []string) int {
	parsed, err := parseArgs(args, map[string]bool{
		"dns":        true,
		"awg-params": true,
		"endpoint":   true,
	})
	if err != nil {
		return a.usageError(opServerInit, err.Error())
	}
	if len(parsed.positional) != 2 {
		return a.usageError(opServerInit, "want <address> <listen_port>")
	}

	// Pre-flight validation, no database side effects.
	address := parsed.positional[0]
	if ip, _, err := net.ParseCIDR(address); err != nil {
		return a.usageError(opServerInit, fmt.Sprintf("invalid server address %q: not a CIDR network", address))
	} else if ip.To4() == nil {
		return a.usageError(opServerInit, fmt.Sprintf("server address %q is not IPv4 (IPv6 is not supported in M4)", address))
	}
	listenPort, err := strconv.ParseUint(parsed.positional[1], 10, 16)
	if err != nil {
		return a.usageError(opServerInit, fmt.Sprintf("invalid listen port %q: must be an unsigned 16-bit value", parsed.positional[1]))
	}
	awgParams := parsed.flags["awg-params"]
	if awgParams == "" {
		awgParams = "{}"
	}
	if _, err := awgconf.ParseParams(awgParams); err != nil {
		return a.usageError(opServerInit, err.Error())
	}
	endpoint := parsed.flags["endpoint"]
	if endpoint != "" {
		if err := validateEndpointArg(endpoint); err != nil {
			return a.usageError(opServerInit, err.Error())
		}
	}

	var privateKey, publicKey string
	keyErr := fault("server-init.keys")
	if keyErr == nil {
		privateKey, publicKey, keyErr = keys.GenerateKeyPair()
	}
	if keyErr != nil {
		return a.fatal(opServerInit, fmt.Errorf("generate server keys: %w", keyErr))
	}

	handle, err := a.openDB()
	if err != nil {
		return a.fatal(opServerInit, err)
	}
	defer handle.Close()

	createErr := fault("server-init.create")
	if createErr == nil {
		createErr = db.CreateServer(handle, privateKey, publicKey, address,
			int64(listenPort), parsed.flags["dns"], awgParams, endpoint)
	}
	if createErr != nil {
		if errors.Is(createErr, db.ErrServerExists) {
			return a.fatal(opServerInit, createErr)
		}
		return a.fatal(opServerInit, fmt.Errorf("create server: %w", createErr))
	}
	// M9.3: the initialization sentinel marks "the server row exists";
	// panel-init refuses to build a fresh schema over a lost database
	// when the sentinel is present.
	sentinelErr := fault("server-init.sentinel")
	if sentinelErr == nil {
		sentinelErr = backup.WriteSentinel(db.DefaultPath())
	}
	if sentinelErr != nil {
		return a.fatal(opServerInit, sentinelErr)
	}
	if err := a.regenerate(handle); err != nil {
		return a.fatal(opServerInit, fmt.Errorf("generate config: %w", err))
	}
	if endpoint == "" {
		fmt.Fprintln(a.stderr, "panel server init: warning: --endpoint not set; `client config` will fail until it is set")
	}
	return a.ok(opServerInit, "ok")
}

// validateEndpointArg mirrors the awgconf endpoint rules
// (host:port with a numeric port in [1, 65535]) for the pre-flight
// check; the authoritive check still runs at `client config` time.
func validateEndpointArg(endpoint string) error {
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil || host == "" {
		return fmt.Errorf("invalid endpoint %q: want host:port", endpoint)
	}
	p, err := strconv.ParseUint(port, 10, 16)
	if err != nil || p == 0 {
		return fmt.Errorf("invalid endpoint %q: port must be a number in [1, 65535]", endpoint)
	}
	return nil
}
