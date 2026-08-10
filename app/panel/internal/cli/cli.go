// Package cli implements the /app/panel command-line interface: a single
// Go binary managing the server row and the client lifecycle
// (docs/TECHNICAL_SPEC_v2.0.md §6, M4 criteria).
//
// Exit codes: 0 success, 1 runtime/operational error, 2 usage error. All
// errors are printed as "panel <operation>: <error>" and never echo
// private/preshared keys.
package cli

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/awgconf"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

const usageText = `usage: /app/panel <command> [args]

commands:
  init                          migrate the database and (re)generate config/awg0.conf
  serve                         run the web panel (not implemented before M6)
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
`

// serveNotImplemented keeps the M2/M6 contract and exact text of the
// previous main.go.
const serveNotImplemented = "panel serve: not implemented in M2. Scheduled: M6 (\"Basic panel CRUD\")."

// Run dispatches a panel command line (without argv[0]) and returns the
// process exit code: 0 success, 1 runtime/operational error, 2 usage.
func Run(args []string, stdout, stderr io.Writer) int {
	a := &app{stdout: stdout, stderr: stderr}
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
	case "status":
		return a.cmdStatus(args[1:])
	default:
		a.usage()
		return 2
	}
}

type app struct {
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
// arguments in order plus the declared --flag values.
type parsedArgs struct {
	positional []string
	flags      map[string]string
}

// parseArgs scans raw tokens: "--name value" pairs must be declared in
// allowed (unknown flags and missing values are usage errors), "-x" and
// other dash tokens are rejected, everything else is positional. The
// stdlib flag package is deliberately not used: flags may follow
// positional arguments ("client add <name> --expires-at ...").
func parseArgs(tokens []string, allowed map[string]bool) (*parsedArgs, error) {
	out := &parsedArgs{flags: map[string]string{}}
	for i := 0; i < len(tokens); i++ {
		t := tokens[i]
		if strings.HasPrefix(t, "--") {
			name := t[2:]
			if !allowed[name] {
				return nil, fmt.Errorf("unknown flag --%s", name)
			}
			if i+1 >= len(tokens) {
				return nil, fmt.Errorf("flag --%s requires a value", name)
			}
			out.flags[name] = tokens[i+1]
			i++
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

// cmdServe keeps the M6 stub: unchanged exit code and error text.
func (a *app) cmdServe(args []string) int {
	fmt.Fprintln(a.stderr, serveNotImplemented)
	return 1
}
