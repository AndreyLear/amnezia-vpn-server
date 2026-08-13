package cli

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/awgconf"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/keys"
)

const (
	opClientAdd       = "client add"
	opClientList      = "client list"
	opClientShow      = "client show"
	opClientEnable    = "client enable"
	opClientDisable   = "client disable"
	opClientRename    = "client rename"
	opClientSetExpiry = "client set-expiry"
	opClientDelete    = "client delete"
	opClientConfig    = "client config"
)

// clientNameMaxRunes is the name length limit shared by add and rename.
const clientNameMaxRunes = 64

// cmdClient dispatches the `client` lifecycle subcommands.
func (a *app) cmdClient(args []string) int {
	if len(args) == 0 {
		a.usage()
		return 2
	}
	switch args[0] {
	case "add":
		return a.cmdClientAdd(args[1:])
	case "list":
		return a.cmdClientList(args[1:])
	case "show":
		return a.cmdClientShow(args[1:])
	case "enable":
		return a.cmdClientSetEnabled(args[1:], true)
	case "disable":
		return a.cmdClientSetEnabled(args[1:], false)
	case "rename":
		return a.cmdClientRename(args[1:])
	case "set-expiry":
		return a.cmdClientSetExpiry(args[1:])
	case "delete":
		return a.cmdClientDelete(args[1:])
	case "config":
		return a.cmdClientConfig(args[1:])
	default:
		a.usage()
		return 2
	}
}

// validateName validates a client name (trimmed, non-empty, valid
// UTF-8, at most 64 characters) and returns the trimmed form.
func validateName(raw string) (string, error) {
	return validateNamed(raw, "client name")
}

// validateNamed is the shared name validator used for client names and
// auth usernames. Errors are fixed strings that never echo the input.
func validateNamed(raw, what string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", fmt.Errorf("%s must not be empty", what)
	}
	if !utf8.ValidString(name) {
		return "", fmt.Errorf("%s must be valid UTF-8", what)
	}
	if n := utf8.RuneCountInString(name); n > clientNameMaxRunes {
		return "", fmt.Errorf("%s must be at most %d characters (got %d)", what, clientNameMaxRunes, n)
	}
	return name, nil
}

// normalizeRFC3339 parses an RFC3339 timestamp and returns its canonical
// UTC form for deterministic storage.
func normalizeRFC3339(raw string) (string, error) {
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return "", fmt.Errorf("invalid RFC3339 timestamp %q", raw)
	}
	return t.UTC().Format(time.RFC3339), nil
}

// parseClientID requires a positive integer id; a non-numeric id is a
// usage error while an unknown id is a runtime error at the call site.
func parseClientID(raw string) (int64, error) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 1 {
		return 0, fmt.Errorf("invalid client id %q", raw)
	}
	return id, nil
}

// tsvCell makes one value safe for the tab-separated list output.
func tsvCell(s string) string {
	s = strings.ReplaceAll(s, "\t", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return strings.ReplaceAll(s, "\n", " ")
}

// cmdClientAdd generates the keys, allocates the first free address and
// inserts the client (one transaction), then regenerates awg0.conf. The
// flag --expires-at is stored via db.SetClientExpiry right after the
// insert; a failed Generate returns exit 1, the previous config stays
// intact and the database is not rolled back. Success prints exactly
// "id:" and "public_key:".
func (a *app) cmdClientAdd(args []string) int {
	parsed, err := parseArgs(args, map[string]bool{"expires-at": true})
	if err != nil {
		return a.usageError(opClientAdd, err.Error())
	}
	if len(parsed.positional) != 1 {
		return a.usageError(opClientAdd, "want <name> [--expires-at <RFC3339>]")
	}
	name, err := validateName(parsed.positional[0])
	if err != nil {
		return a.usageError(opClientAdd, err.Error())
	}
	expiresAt := ""
	if raw, ok := parsed.flags["expires-at"]; ok {
		expiresAt, err = normalizeRFC3339(raw)
		if err != nil {
			return a.usageError(opClientAdd, err.Error())
		}
	}

	privateKey, publicKey, err := keys.GenerateKeyPair()
	if err != nil {
		return a.fatal(opClientAdd, fmt.Errorf("generate keys: %w", err))
	}
	presharedKey, err := keys.GeneratePresharedKey()
	if err != nil {
		return a.fatal(opClientAdd, fmt.Errorf("generate preshared key: %w", err))
	}

	handle, err := a.openDB()
	if err != nil {
		return a.fatal(opClientAdd, err)
	}
	defer handle.Close()

	server, err := requireServer(handle)
	if err != nil {
		return a.fatal(opClientAdd, err)
	}
	record, err := db.CreateClient(handle, server.Address, db.NewClient{
		Name:         name,
		PrivateKey:   privateKey,
		PublicKey:    publicKey,
		PresharedKey: presharedKey,
	})
	if err != nil {
		if errors.Is(err, db.ErrClientNameExists) {
			return a.fatal(opClientAdd, err)
		}
		return a.fatal(opClientAdd, fmt.Errorf("create client: %w", err))
	}
	if expiresAt != "" {
		if err := db.SetClientExpiry(handle, record.ID, expiresAt); err != nil {
			return a.fatal(opClientAdd, fmt.Errorf("set expiry: %w", err))
		}
	}
	if err := a.regenerate(handle); err != nil {
		return a.fatal(opClientAdd, fmt.Errorf("generate config: %w", err))
	}
	fmt.Fprintf(a.stdout, "id: %d\n", record.ID)
	fmt.Fprintf(a.stdout, "public_key: %s\n", record.PublicKey)
	return 0
}

// cmdClientList prints every client ordered by id as TSV with a header,
// including disabled and expired ones. Private/preshared keys never
// reach the output.
func (a *app) cmdClientList(args []string) int {
	if len(args) != 0 {
		return a.usageError(opClientList, "unexpected arguments")
	}
	handle, err := a.openDB()
	if err != nil {
		return a.fatal(opClientList, err)
	}
	defer handle.Close()
	clients, err := db.ClientsAll(handle)
	if err != nil {
		return a.fatal(opClientList, err)
	}
	fmt.Fprintln(a.stdout, "id\tname\taddress\tenabled\texpires_at\tpublic_key")
	for _, c := range clients {
		enabled := "0"
		if c.Enabled {
			enabled = "1"
		}
		fmt.Fprintf(a.stdout, "%d\t%s\t%s\t%s\t%s\t%s\n",
			c.ID, tsvCell(c.Name), c.Address, enabled, c.ExpiresAt, c.PublicKey)
	}
	return 0
}

// cmdClientShow prints the public client card; expired is computed on
// the fly and the database is never mutated.
func (a *app) cmdClientShow(args []string) int {
	parsed, err := parseArgs(args, nil)
	if err != nil {
		return a.usageError(opClientShow, err.Error())
	}
	if len(parsed.positional) != 1 {
		return a.usageError(opClientShow, "want <id>")
	}
	id, err := parseClientID(parsed.positional[0])
	if err != nil {
		return a.usageError(opClientShow, err.Error())
	}
	handle, err := a.openDB()
	if err != nil {
		return a.fatal(opClientShow, err)
	}
	defer handle.Close()
	c, err := db.ClientByID(handle, id)
	if err != nil {
		return a.fatal(opClientShow, err)
	}
	fmt.Fprintf(a.stdout, "id: %d\n", c.ID)
	fmt.Fprintf(a.stdout, "name: %s\n", c.Name)
	fmt.Fprintf(a.stdout, "address: %s\n", c.Address)
	fmt.Fprintf(a.stdout, "enabled: %t\n", c.Enabled)
	fmt.Fprintf(a.stdout, "public_key: %s\n", c.PublicKey)
	fmt.Fprintf(a.stdout, "created_at: %s\n", c.CreatedAt)
	fmt.Fprintf(a.stdout, "updated_at: %s\n", c.UpdatedAt)
	fmt.Fprintf(a.stdout, "expires_at: %s\n", c.ExpiresAt)
	fmt.Fprintf(a.stdout, "expired: %t\n", c.Expired(time.Now().UTC()))
	return 0
}

// cmdClientSetEnabled toggles the enabled flag and regenerates the
// config. Unknown ids exit 1; repeating the same operation is a
// successful idempotent no-op.
func (a *app) cmdClientSetEnabled(args []string, enabled bool) int {
	op := opClientEnable
	verb := "enable"
	if !enabled {
		op = opClientDisable
		verb = "disable"
	}
	parsed, err := parseArgs(args, nil)
	if err != nil {
		return a.usageError(op, err.Error())
	}
	if len(parsed.positional) != 1 {
		return a.usageError(op, "want <id>")
	}
	id, err := parseClientID(parsed.positional[0])
	if err != nil {
		return a.usageError(op, err.Error())
	}
	handle, err := a.openDB()
	if err != nil {
		return a.fatal(op, err)
	}
	defer handle.Close()
	if err := db.SetClientEnabled(handle, id, enabled); err != nil {
		return a.fatal(op, err)
	}
	if err := a.regenerate(handle); err != nil {
		return a.fatal(op, fmt.Errorf("generate config: %w", err))
	}
	return a.ok(op, verb+"d client "+strconv.FormatInt(id, 10))
}

// cmdClientRename renames a client (same rules as add) and regenerates
// the config. Names do not appear in awg0.conf, so the peer section is
// unchanged; the operation is idempotent.
func (a *app) cmdClientRename(args []string) int {
	parsed, err := parseArgs(args, nil)
	if err != nil {
		return a.usageError(opClientRename, err.Error())
	}
	if len(parsed.positional) != 2 {
		return a.usageError(opClientRename, "want <id> <name>")
	}
	id, err := parseClientID(parsed.positional[0])
	if err != nil {
		return a.usageError(opClientRename, err.Error())
	}
	name, err := validateName(parsed.positional[1])
	if err != nil {
		return a.usageError(opClientRename, err.Error())
	}
	handle, err := a.openDB()
	if err != nil {
		return a.fatal(opClientRename, err)
	}
	defer handle.Close()
	if err := db.UpdateClientName(handle, id, name); err != nil {
		return a.fatal(opClientRename, err)
	}
	if err := a.regenerate(handle); err != nil {
		return a.fatal(opClientRename, fmt.Errorf("generate config: %w", err))
	}
	return a.ok(opClientRename, "renamed client "+strconv.FormatInt(id, 10))
}

// cmdClientSetExpiry sets (RFC3339, canonicalized) or clears ("none")
// expires_at and regenerates the config. An invalid timestamp is a
// usage error; an unknown id exits 1.
func (a *app) cmdClientSetExpiry(args []string) int {
	parsed, err := parseArgs(args, nil)
	if err != nil {
		return a.usageError(opClientSetExpiry, err.Error())
	}
	if len(parsed.positional) != 2 {
		return a.usageError(opClientSetExpiry, "want <id> <RFC3339|none>")
	}
	id, err := parseClientID(parsed.positional[0])
	if err != nil {
		return a.usageError(opClientSetExpiry, err.Error())
	}
	expiresAt := ""
	if value := parsed.positional[1]; value != "none" {
		expiresAt, err = normalizeRFC3339(value)
		if err != nil {
			return a.usageError(opClientSetExpiry, err.Error())
		}
	}
	handle, err := a.openDB()
	if err != nil {
		return a.fatal(opClientSetExpiry, err)
	}
	defer handle.Close()
	if err := db.SetClientExpiry(handle, id, expiresAt); err != nil {
		return a.fatal(opClientSetExpiry, err)
	}
	if err := a.regenerate(handle); err != nil {
		return a.fatal(opClientSetExpiry, fmt.Errorf("generate config: %w", err))
	}
	return a.ok(opClientSetExpiry, "set expiry of client "+strconv.FormatInt(id, 10))
}

// cmdClientDelete removes the client row and regenerates the config. An
// unknown id exits 1 (never a silent no-op), a second delete of the same
// id returns the same error.
func (a *app) cmdClientDelete(args []string) int {
	parsed, err := parseArgs(args, nil)
	if err != nil {
		return a.usageError(opClientDelete, err.Error())
	}
	if len(parsed.positional) != 1 {
		return a.usageError(opClientDelete, "want <id>")
	}
	id, err := parseClientID(parsed.positional[0])
	if err != nil {
		return a.usageError(opClientDelete, err.Error())
	}
	handle, err := a.openDB()
	if err != nil {
		return a.fatal(opClientDelete, err)
	}
	defer handle.Close()
	if err := db.DeleteClient(handle, id); err != nil {
		return a.fatal(opClientDelete, err)
	}
	if err := a.regenerate(handle); err != nil {
		return a.fatal(opClientDelete, fmt.Errorf("generate config: %w", err))
	}
	return a.ok(opClientDelete, "deleted client "+strconv.FormatInt(id, 10))
}

// cmdClientConfig prints the on-demand client configuration to stdout:
// full IPv4 tunnel, endpoint exclusively from settings["endpoint"],
// server AWG parameters mirrored. Nothing is written to disk; disabled
// and expired clients still receive their config. The error cases map
// through awgconf.GenerateClient (missing server/client/endpoint are
// exit 1).
func (a *app) cmdClientConfig(args []string) int {
	parsed, err := parseArgs(args, nil)
	if err != nil {
		return a.usageError(opClientConfig, err.Error())
	}
	if len(parsed.positional) != 1 {
		return a.usageError(opClientConfig, "want <id>")
	}
	id, err := parseClientID(parsed.positional[0])
	if err != nil {
		return a.usageError(opClientConfig, err.Error())
	}
	handle, err := a.openDB()
	if err != nil {
		return a.fatal(opClientConfig, err)
	}
	defer handle.Close()

	cfg, err := awgconf.GenerateClient(handle, id)
	if err != nil {
		return a.fatal(opClientConfig, err)
	}
	if _, err := a.stdout.Write(cfg); err != nil {
		return a.fatal(opClientConfig, fmt.Errorf("write config: %w", err))
	}
	return 0
}
