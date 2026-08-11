// Package cli implements the /app/panel command-line interface: a single
// Go binary managing the server row and the client lifecycle
// (docs/TECHNICAL_SPEC_v2.0.md §6, M4 criteria).
//
// Exit codes: 0 success, 1 runtime/operational error, 2 usage error. All
// errors are printed as "panel <operation>: <error>" and never echo
// private/preshared keys.
package cli

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/auth"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/awgconf"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/web"
)

const usageText = `usage: /app/panel <command> [args]

commands:
  init                          migrate the database and (re)generate config/awg0.conf
  serve [--addr <host:port>]    run the web panel
  status                        print the runtime AWG status (status/status.json)
  server init <address> <listen-port> [--dns <dns>] [--awg-params <json>] [--endpoint <host:port>]
  client add <name> [--expires-at <RFC3339>]
  client list
  client show <id>
  client enable <id>
  client disable <id>
  client rename <id> <name>
  client set-expiry <id> <RFC3339|none>
  client delete <id>
  client config <id>
  auth add-user <username> (--password-stdin | --password-env <ENV>)
  backup create                  create an encrypted database backup
  backup list                    list existing backups
`

// serveNotImplemented kept the M2/M6 contract; M6.1 replaces it with a
// real HTTP server (M6_AUDIT.md §2.1.1).

// Run dispatches a panel command line (without argv[0]) and returns the
// process exit code: 0 success, 1 runtime/operational error, 2 usage.
// stdin is wired to os.Stdin; tests use the internal `run` variant to
// inject a reader (auth add-user --password-stdin).
func Run(args []string, stdout, stderr io.Writer) int {
	return run(args, os.Stdin, stdout, stderr)
}

// run is Run with an injectable stdin.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	a := &app{stdin: stdin, stdout: stdout, stderr: stderr}
	if len(args) == 0 {
		a.usage()
		return 2
	}
	switch args[0] {
	case "init":
		return a.cmdInit(args[1:])
	case "serve":
		return a.cmdServe(args[1:])
	case "server":
		return a.cmdServer(args[1:])
	case "client":
		return a.cmdClient(args[1:])
	case "auth":
		return a.cmdAuth(args[1:])
	case "status":
		return a.cmdStatus(args[1:])
	case "backup":
		return a.cmdBackup(args[1:])
	default:
		a.usage()
		return 2
	}
}

type app struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
}

// fatal prints "panel <operation>: <error>" to stderr and returns the
// runtime exit code. Operation callers guarantee the error never
// contains private/preshared key material.
func (a *app) fatal(operation string, err error) int {
	fmt.Fprintf(a.stderr, "panel %s: %v\n", operation, err)
	return 1
}

// ok prints a confirmation to stdout and returns success.
func (a *app) ok(operation, message string) int {
	fmt.Fprintf(a.stdout, "panel %s: %s\n", operation, message)
	return 0
}

// usageError prints an argument diagnostic to stderr and returns the
// usage exit code.
func (a *app) usageError(operation, message string) int {
	fmt.Fprintf(a.stderr, "panel %s: %s\n", operation, message)
	return 2
}

func (a *app) usage() {
	fmt.Fprint(a.stderr, usageText)
}

// parsedArgs is the output of the manual token scan: positional
// arguments in order plus the declared --flag values. Boolean flags
// (declared with takesValue=false) are recorded with an empty value.
type parsedArgs struct {
	positional []string
	flags      map[string]string
}

// parseArgs scans raw tokens: "--name value" pairs must be declared in
// allowed (unknown flags and missing values are usage errors), "-x" and
// other dash tokens are rejected, everything else is positional. The
// stdlib flag package is deliberately not used: flags may follow
// positional arguments ("client add <name> --expires-at ...") and
// boolean flags may appear anywhere ("auth add-user <name>
// --password-stdin"). Map semantics: allowed[name] == true means the
// flag consumes the following token as its value; false means a
// boolean flag that consumes nothing.
func parseArgs(tokens []string, allowed map[string]bool) (*parsedArgs, error) {
	out := &parsedArgs{flags: map[string]string{}}
	for i := 0; i < len(tokens); i++ {
		t := tokens[i]
		if strings.HasPrefix(t, "--") {
			name := t[2:]
			takesValue, ok := allowed[name]
			if !ok {
				return nil, fmt.Errorf("unknown flag --%s", name)
			}
			if takesValue {
				if i+1 >= len(tokens) {
					return nil, fmt.Errorf("flag --%s requires a value", name)
				}
				out.flags[name] = tokens[i+1]
				i++
			} else {
				out.flags[name] = ""
			}
			continue
		}
		if strings.HasPrefix(t, "-") {
			return nil, fmt.Errorf("unknown flag %q", t)
		}
		out.positional = append(out.positional, t)
	}
	return out, nil
}

// configPath returns the awg0.conf location. AMNEZIA_CONFIG_PATH is an
// override for tests and custom setups; the default matches
// docs/TECHNICAL_SPEC_v2.0.md §2.
func configPath() string {
	if p := os.Getenv("AMNEZIA_CONFIG_PATH"); p != "" {
		return p
	}
	return "/config/awg0.conf"
}

// openDB opens (creating if needed) and migrates the SQLite database
// (AMNEZIA_DB_PATH, default /data/amnezia.sqlite) and returns the live
// handle. The caller closes it.
func (a *app) openDB() (*sql.DB, error) {
	handle, err := db.Open(db.DefaultPath())
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := db.Migrate(handle); err != nil {
		handle.Close()
		return nil, fmt.Errorf("migrate database: %w", err)
	}
	return handle, nil
}

// regenerate renders config/awg0.conf from the database. The config is
// derived state: a failure leaves the previous config intact (WriteAtomic)
// and does not touch the database.
func (a *app) regenerate(handle *sql.DB) error {
	return awgconf.Generate(handle, configPath())
}

// requireServer loads the server row, mapping a missing row to the M3
// error text used across all dependent commands.
func requireServer(handle *sql.DB) (*db.ServerRecord, error) {
	server, err := db.ServerRow(handle)
	if errors.Is(err, db.ErrServerNotFound) {
		return nil, awgconf.ErrNoServerRow
	}
	return server, err
}

// cmdInit is the M2/M3 panel-init contract: migrate the database, then
// (re)generate config/awg0.conf; exit 1 when the server row is absent.
func (a *app) cmdInit(args []string) int {
	if len(args) != 0 {
		return a.usageError("init", "unexpected arguments")
	}
	handle, err := a.openDB()
	if err != nil {
		return a.fatal("init", err)
	}
	defer handle.Close()
	if err := awgconf.Generate(handle, configPath()); err != nil {
		return a.fatal("init", err)
	}
	fmt.Fprintln(a.stdout, "panel init: awg0.conf generated")
	return 0
}

const opServe = "serve"

// cmdServe runs the web panel: it installs the SIGTERM/SIGINT
// handlers, so a clean shutdown exits 0, a fatal startup error exits 1
// and argument errors exit 2 (M6_AUDIT.md §2.1.10).
func (a *app) cmdServe(args []string) int {
	parsed, err := parseArgs(args, map[string]bool{"addr": true})
	if err != nil {
		return a.usageError(opServe, err.Error())
	}
	if len(parsed.positional) > 0 {
		return a.usageError(opServe, "unexpected arguments")
	}
	cfg := web.DefaultConfig()
	if v := parsed.flags["addr"]; v != "" {
		cfg.Addr = v
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return a.serveHTTP(ctx, cfg)
}

// serveHTTP runs the HTTP server until ctx is canceled. The database
// is opened and migrated first (the panel is the only SQLite writer,
// docs/TECHNICAL_SPEC_v2.0.md §2): a database failure is fatal and
// exits 1 before any socket is bound.
func (a *app) serveHTTP(ctx context.Context, cfg web.Config) int {
	handle, err := a.openDB()
	if err != nil {
		return a.fatal(opServe, err)
	}
	defer handle.Close()
	cfg.Logger = log.New(a.stderr, "panel serve: ", log.LstdFlags)
	cfg.DB = handle
	// M7.4: one in-memory session store per serve process; restarting
	// the panel discards every session (M7.3 contract).
	cfg.Sessions = auth.NewSessionStore(auth.SessionTTL)
	server, err := web.New(cfg)
	if err != nil {
		return a.fatal(opServe, err)
	}
	if err := server.ListenAndServe(ctx); err != nil {
		return a.fatal(opServe, err)
	}
	return 0
}
