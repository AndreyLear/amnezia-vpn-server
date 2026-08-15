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
	"bufio"
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
	case "change-password":
		return a.cmdAuthChangePassword(args[1:])
	case "set-password":
		return a.cmdAuthSetPassword(args[1:])
	case "2fa":
		if len(args) < 2 {
			a.usage()
			return 2
		}
		switch args[1] {
		case "status":
			return a.cmdAuth2FAStatus(args[2:])
		case "disable":
			return a.cmdAuth2FADisable(args[2:])
		default:
			a.usage()
			return 2
		}
	default:
		a.usage()
		return 2
	}
}

func readCLISecret(r io.Reader) (string, error) {
	b, err := r.(*bufio.Reader).ReadString('\n')
	if err != nil && len(b) == 0 {
		return "", err
	}
	return trimTrailingNewline(b), nil
}

func (a *app) cmdAuthChangePassword(args []string) int {
	p, err := parseArgs(args, map[string]bool{"old-password-stdin": false, "new-password-stdin": false})
	if err != nil {
		return a.usageError("auth change-password", err.Error())
	}
	if len(p.positional) != 1 || len(p.flags) != 2 {
		return a.usageError("auth change-password", "want <username> --old-password-stdin --new-password-stdin")
	}
	username, err := validateNamed(p.positional[0], "username")
	if err != nil {
		return a.usageError("auth change-password", err.Error())
	}
	reader := bufio.NewReader(a.stdin)
	old, err := readCLISecret(reader)
	if err != nil {
		return a.fatal("auth change-password", err)
	}
	newp, err := readCLISecret(reader)
	if err != nil {
		return a.fatal("auth change-password", err)
	}
	u, err := a.openDB()
	if err != nil {
		return a.fatal("auth change-password", err)
	}
	defer u.Close()
	row, err := db.AuthUserByUsername(u, username)
	if err != nil || !auth.VerifyPassword(old, row.PasswordHash) {
		return a.fatal("auth change-password", errors.New("invalid credentials"))
	}
	hash, err := auth.HashPassword(newp)
	if err != nil {
		if errors.Is(err, auth.ErrPasswordTooShort) || errors.Is(err, auth.ErrPasswordTooLong) {
			return a.usageError("auth change-password", err.Error())
		}
		return a.fatal("auth change-password", err)
	}
	if err := db.UpdateAuthPassword(u, username, row.PasswordHash, hash); err != nil {
		return a.fatal("auth change-password", err)
	}
	// The CLI and `panel serve` are separate processes and do not
	// share the in-memory SessionStore. A sidecar next to the DB
	// tells serve to drop this user's live sessions on the next
	// authenticated request.
	if err := auth.RequestInvalidateSessions(db.DefaultPath(), username); err != nil {
		return a.fatal("auth change-password", err)
	}
	return a.ok("auth change-password", "password changed")
}

// cmdAuthSetPassword resets a user's password from stdin without the
// current password. SSH (or equivalent host access) is the trust
// boundary; there is no web forgot-password form.
func (a *app) cmdAuthSetPassword(args []string) int {
	p, err := parseArgs(args, map[string]bool{"password-stdin": false})
	if err != nil {
		return a.usageError("auth set-password", err.Error())
	}
	if len(p.positional) != 1 || len(p.flags) != 1 {
		return a.usageError("auth set-password", "want <username> --password-stdin")
	}
	username, err := validateNamed(p.positional[0], "username")
	if err != nil {
		return a.usageError("auth set-password", err.Error())
	}
	reader := bufio.NewReader(a.stdin)
	newp, err := readCLISecret(reader)
	if err != nil {
		return a.fatal("auth set-password", err)
	}
	handle, err := a.openDB()
	if err != nil {
		return a.fatal("auth set-password", err)
	}
	defer handle.Close()
	row, err := db.AuthUserByUsername(handle, username)
	if err != nil {
		return a.fatal("auth set-password", err)
	}
	hash, err := auth.HashPassword(newp)
	if err != nil {
		if errors.Is(err, auth.ErrPasswordTooShort) || errors.Is(err, auth.ErrPasswordTooLong) {
			return a.usageError("auth set-password", err.Error())
		}
		return a.fatal("auth set-password", err)
	}
	if err := db.UpdateAuthPassword(handle, username, row.PasswordHash, hash); err != nil {
		return a.fatal("auth set-password", err)
	}
	if err := auth.RequestInvalidateSessions(db.DefaultPath(), username); err != nil {
		return a.fatal("auth set-password", err)
	}
	return a.ok("auth set-password", "password changed")
}

func (a *app) cmdAuth2FAStatus(args []string) int {
	if len(args) != 1 {
		return a.usageError("auth 2fa status", "want <username>")
	}
	h, err := a.openDB()
	if err != nil {
		return a.fatal("auth 2fa status", err)
	}
	defer h.Close()
	u, err := db.AuthUserByUsername(h, args[0])
	if err != nil {
		return a.fatal("auth 2fa status", err)
	}
	fmt.Fprintf(a.stdout, "enabled: %t\nmode: %s\n", u.TOTPSecret != "", u.TOTPMode)
	return 0
}

func (a *app) cmdAuth2FADisable(args []string) int {
	if len(args) != 1 {
		return a.usageError("auth 2fa disable", "want <username>")
	}
	h, err := a.openDB()
	if err != nil {
		return a.fatal("auth 2fa disable", err)
	}
	defer h.Close()
	if err := db.ClearTotpSecret(h, args[0]); err != nil {
		return a.fatal("auth 2fa disable", err)
	}
	if err := db.SetTotpMode(h, args[0], ""); err != nil {
		return a.fatal("auth 2fa disable", err)
	}
	return a.ok("auth 2fa disable", "2FA disabled")
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
