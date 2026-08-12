// M7.2: CLI bootstrap of the first authentication user
// (TECHNICAL_SPEC_v2.0.md §6: username/password login; the `auth`
// table is §3). The web panel is not reachable until a user exists —
// this command provides the initial login on a fresh VPS by executing
// inside the panel container:
//
//	/app/panel auth add-user alice --password-stdin
//	/app/panel auth add-user alice --password-env ADMIN_PASSWORD
//
// Exactly one password source must be selected. Passwords are consumed
// without echo: stdin is read to EOF (a single trailing newline is
// stripped), the environment is read via os.LookupEnv. The plaintext
// password exists only as a local variable passed to
// internal/auth.HashPassword — it never reaches stdout, stderr,
// errors or the database (only the Argon2id hash is stored via
// internal/db.CreateAuthUser).
package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/auth"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

const opAuthAddUser = "auth add-user"

// cmdAuth dispatches the `auth` subcommands. M7.2 implements only
// add-user (login/logout, sessions and CSRF are M7.3+).
func (a *app) cmdAuth(args []string) int {
	if len(args) == 0 {
		a.usage()
		return 2
	}
	switch args[0] {
	case "add-user":
		return a.cmdAuthAddUser(args[1:])
	default:
		a.usage()
		return 2
	}
}

// cmdAuthAddUser creates the first admin user. Exit semantics:
//   - 2 for usage errors (missing/extra positionals, password source
//     ambiguity, unknown flags, and password validation failures
//     reported by HashPassword — empty or oversized password);
//   - 1 for operational failures (unreadable stdin, unset env var,
//     database errors, duplicate username, hash entropy failure).
//
// No error path ever contains the password or the encoded hash.
func (a *app) cmdAuthAddUser(args []string) int {
	parsed, err := parseArgs(args, map[string]bool{
		"password-stdin": false, // boolean flag
		"password-env":   true,  // takes a value
	})
	if err != nil {
		return a.usageError(opAuthAddUser, err.Error())
	}
	if len(parsed.positional) != 1 {
		return a.usageError(opAuthAddUser, "want <username> and exactly one password source")
	}
	username, err := validateNamed(parsed.positional[0], "username")
	if err != nil {
		return a.usageError(opAuthAddUser, err.Error())
	}
	_, stdinFlag := parsed.flags["password-stdin"]
	envName, envFlag := parsed.flags["password-env"]
	if stdinFlag == envFlag {
		return a.usageError(opAuthAddUser, "exactly one of --password-stdin or --password-env is required")
	}

	password := ""
	if stdinFlag {
		// Cap the read before validating: MaxPasswordLen must protect
		// the process from an arbitrarily large pipe, not only from a
		// large in-memory string. The +2 headroom lets the capped read
		// accept exactly MaxPasswordLen bytes plus the trailing newline
		// that trimTrailingNewline strips; HashPassword stays the single
		// length validator.
		raw, err := io.ReadAll(io.LimitReader(a.stdin, auth.MaxPasswordLen+2))
		if err != nil {
			return a.fatal(opAuthAddUser, fmt.Errorf("read password from stdin: %w", err))
		}
		password = trimTrailingNewline(string(raw))
	} else {
		v, ok := os.LookupEnv(envName)
		if !ok {
			return a.fatal(opAuthAddUser, fmt.Errorf("environment variable %s is not set", envName))
		}
		password = v
	}

	handle, err := a.openDB()
	if err != nil {
		return a.fatal(opAuthAddUser, err)
	}
	defer handle.Close()

	hash, err := auth.HashPassword(password)
	if err != nil {
		if errors.Is(err, auth.ErrPasswordTooShort) || errors.Is(err, auth.ErrPasswordTooLong) {
			return a.usageError(opAuthAddUser, err.Error())
		}
		return a.fatal(opAuthAddUser, err)
	}
	if _, err := db.CreateAuthUser(handle, username, hash); err != nil {
		return a.fatal(opAuthAddUser, err)
	}
	fmt.Fprintf(a.stdout, "user created: %s\n", username)
	return 0
}

// trimTrailingNewline strips the final line ending a terminal/pipe adds
// to typed input ("\n" and the "\r\n" form). The rest of the password
// — including leading/trailing spaces — is preserved verbatim.
func trimTrailingNewline(s string) string {
	s = strings.TrimSuffix(s, "\n")
	return strings.TrimSuffix(s, "\r")
}
