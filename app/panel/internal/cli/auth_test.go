package cli

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/auth"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

// runInput runs the CLI with an injectable stdin and returns code,
// stdout, stderr.
func runInput(stdin io.Reader, args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(args, stdin, &out, &errb)
	return code, out.String(), errb.String()
}

// authUser loads the user like the panel would (db accessor only).
func authUser(t *testing.T, username string) *db.AuthUser {
	t.Helper()
	handle, err := db.Open(db.DefaultPath())
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer handle.Close()
	if err := db.Migrate(handle); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	u, err := db.AuthUserByUsername(handle, username)
	if err != nil {
		t.Fatalf("AuthUserByUsername(%q): %v", username, err)
	}
	return u
}

func assertNoSecrets(t *testing.T, out, errb string, password string) {
	t.Helper()
	for _, s := range []string{password, "$argon2id$"} {
		if strings.Contains(out, s) || strings.Contains(errb, s) {
			t.Fatalf("secret %q leaked into output (stdout=%q stderr=%q)", s, out, errb)
		}
	}
}

func TestAuthAddUserStdin(t *testing.T) {
	newCtx(t)
	code, out, errb := runInput(strings.NewReader("s3cr3t-stdin-pw\n"),
		"auth", "add-user", "alice", "--password-stdin")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, errb)
	}
	if !strings.Contains(out, "user created: alice") {
		t.Fatalf("missing confirmation in stdout: %q", out)
	}
	assertNoSecrets(t, out, errb, "s3cr3t-stdin-pw")
	if !strings.Contains(errb, "") {
		t.Fatalf("stderr must be empty: %q", errb)
	}
	u := authUser(t, "alice")
	if !auth.VerifyPassword("s3cr3t-stdin-pw", u.PasswordHash) {
		t.Fatal("stored hash must verify the stdin password")
	}
}

func TestAuthChangePasswordCLI(t *testing.T) {
	newCtx(t)
	if code, _, errb := runInput(strings.NewReader("old-password\n"), "auth", "add-user", "alice", "--password-stdin"); code != 0 {
		t.Fatalf("add user: %d %s", code, errb)
	}
	code, out, errb := runInput(strings.NewReader("old-password\nnew-password\n"), "auth", "change-password", "alice", "--old-password-stdin", "--new-password-stdin")
	if code != 0 || !strings.Contains(out, "password changed") {
		t.Fatalf("change password: code=%d out=%q err=%q", code, out, errb)
	}
	u := authUser(t, "alice")
	if !auth.VerifyPassword("new-password", u.PasswordHash) || auth.VerifyPassword("old-password", u.PasswordHash) {
		t.Fatal("CLI did not replace password")
	}
	assertNoSecrets(t, out, errb, "new-password")
	raw, err := os.ReadFile(auth.InvalidateSessionsPath(db.DefaultPath()))
	if err != nil {
		t.Fatalf("invalidate sentinel: %v", err)
	}
	if string(raw) != "alice\n" {
		t.Fatalf("sentinel = %q", raw)
	}
}

func TestAuthChangePasswordCLIFailureIsSafe(t *testing.T) {
	newCtx(t)
	if code, _, _ := runInput(strings.NewReader("old-password\n"), "auth", "add-user", "alice", "--password-stdin"); code != 0 {
		t.Fatal("add user failed")
	}
	code, out, errb := runInput(strings.NewReader("wrong-password\nnew-password\n"), "auth", "change-password", "alice", "--old-password-stdin", "--new-password-stdin")
	if code != 1 || !strings.Contains(errb, "invalid credentials") || out != "" {
		t.Fatalf("failure: code=%d out=%q err=%q", code, out, errb)
	}
	assertNoSecrets(t, out, errb, "wrong-password")
	if _, err := os.Stat(auth.InvalidateSessionsPath(db.DefaultPath())); !os.IsNotExist(err) {
		t.Fatalf("failed change-password must not write sentinel, stat err = %v", err)
	}
}

func TestAuthSetPasswordCLI(t *testing.T) {
	newCtx(t)
	if code, _, errb := runInput(strings.NewReader("old-password\n"), "auth", "add-user", "alice", "--password-stdin"); code != 0 {
		t.Fatalf("add user: %d %s", code, errb)
	}
	code, out, errb := runInput(strings.NewReader("brand-new\n"), "auth", "set-password", "alice", "--password-stdin")
	if code != 0 || !strings.Contains(out, "password changed") {
		t.Fatalf("set password: code=%d out=%q err=%q", code, out, errb)
	}
	u := authUser(t, "alice")
	if !auth.VerifyPassword("brand-new", u.PasswordHash) || auth.VerifyPassword("old-password", u.PasswordHash) {
		t.Fatal("CLI did not replace password without old password")
	}
	assertNoSecrets(t, out, errb, "brand-new")
	assertNoSecrets(t, out, errb, "old-password")
	raw, err := os.ReadFile(auth.InvalidateSessionsPath(db.DefaultPath()))
	if err != nil {
		t.Fatalf("invalidate sentinel: %v", err)
	}
	if string(raw) != "alice\n" {
		t.Fatalf("sentinel = %q", raw)
	}
}

func TestAuthSetPasswordCLIUnknownUser(t *testing.T) {
	newCtx(t)
	code, out, errb := runInput(strings.NewReader("brand-new\n"), "auth", "set-password", "nobody", "--password-stdin")
	if code == 0 {
		t.Fatalf("unknown user: code=%d out=%q err=%q", code, out, errb)
	}
	assertNoSecrets(t, out, errb, "brand-new")
	if _, err := os.Stat(auth.InvalidateSessionsPath(db.DefaultPath())); !os.IsNotExist(err) {
		t.Fatalf("failed set-password must not write sentinel, stat err = %v", err)
	}
}

func TestAuth2FAStatusAndDisableCLI(t *testing.T) {
	newCtx(t)
	if code, _, _ := runInput(strings.NewReader("password\n"), "auth", "add-user", "alice", "--password-stdin"); code != 0 {
		t.Fatal("add user failed")
	}
	h, err := db.Open(db.DefaultPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(h); err != nil {
		t.Fatal(err)
	}
	if err := db.SetTotpSecret(h, "alice", "JBSWY3DPEHPK3PXP"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetTotpMode(h, "alice", "2fa"); err != nil {
		t.Fatal(err)
	}
	h.Close()
	code, out, errb := runInput(nil, "auth", "2fa", "status", "alice")
	if code != 0 || !strings.Contains(out, "enabled: true") || !strings.Contains(out, "mode: 2fa") || errb != "" {
		t.Fatalf("status: code=%d out=%q err=%q", code, out, errb)
	}
	code, out, errb = runInput(nil, "auth", "2fa", "disable", "alice")
	if code != 0 || !strings.Contains(out, "2FA disabled") || errb != "" {
		t.Fatalf("disable: code=%d out=%q err=%q", code, out, errb)
	}
	u := authUser(t, "alice")
	if u.TOTPSecret != "" || u.TOTPMode != "" {
		t.Fatalf("2FA remains enabled: %+v", u)
	}
}

func TestAuthAddUserEnv(t *testing.T) {
	newCtx(t)
	t.Setenv("ADMIN_PASSWORD", "env-secret-99")
	code, out, errb := runInput(strings.NewReader("unused"),
		"auth", "add-user", "--password-env", "ADMIN_PASSWORD", "bob")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, errb)
	}
	if !strings.Contains(out, "user created: bob") {
		t.Fatalf("missing confirmation in stdout: %q", out)
	}
	assertNoSecrets(t, out, errb, "env-secret-99")
	u := authUser(t, "bob")
	if !auth.VerifyPassword("env-secret-99", u.PasswordHash) {
		t.Fatal("stored hash must verify the env password")
	}
}

func TestAuthAddUserFlagAfterPositional(t *testing.T) {
	newCtx(t)
	t.Setenv("PW", "flag-order-1")
	code, out, errb := runInput(nil, "auth", "add-user", "charlie", "--password-env", "PW")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, errb)
	}
	if !strings.Contains(out, "user created: charlie") {
		t.Fatalf("missing confirmation in stdout: %q", out)
	}
	_ = errb
}

func TestAuthAddUserDuplicate(t *testing.T) {
	newCtx(t)
	t.Setenv("PW", "first-run")
	if code, _, errb := runInput(nil, "auth", "add-user", "dup", "--password-env", "PW"); code != 0 {
		t.Fatalf("first create: code = %d, stderr = %q", code, errb)
	}
	code, out, errb := runInput(nil, "auth", "add-user", "dup", "--password-env", "PW")
	if code != 1 {
		t.Fatalf("duplicate: code = %d, want 1", code)
	}
	if !strings.Contains(errb, "panel auth add-user: db: auth user already exists") {
		t.Fatalf("unexpected duplicate error: %q", errb)
	}
	assertNoSecrets(t, out, errb, "first-run")
	if out != "" {
		t.Fatalf("duplicate must not print confirmation: %q", out)
	}
}

func TestAuthAddUserNoOverwrite(t *testing.T) {
	newCtx(t)
	t.Setenv("PWA", "original-pw")
	t.Setenv("PWB", "second-pw")
	if code, _, errb := runInput(nil, "auth", "add-user", "keep", "--password-env", "PWA"); code != 0 {
		t.Fatalf("first create: code = %d, stderr = %q", code, errb)
	}
	if code, _, _ := runInput(nil, "auth", "add-user", "keep", "--password-env", "PWB"); code != 1 {
		t.Fatalf("second create: code = %d, want 1", code)
	}
	u := authUser(t, "keep")
	if !auth.VerifyPassword("original-pw", u.PasswordHash) {
		t.Fatal("original password must still verify")
	}
	if auth.VerifyPassword("second-pw", u.PasswordHash) {
		t.Fatal("second password must NOT verify (user must not be overwritten)")
	}
}

func TestAuthAddUserUsageErrors(t *testing.T) {
	t.Setenv("PW", "leak-marker-9f3a")
	cases := []struct {
		name string
		args []string
	}{
		{"missing username", []string{"auth", "add-user", "--password-stdin"}},
		{"missing username env", []string{"auth", "add-user", "--password-env", "PW"}},
		{"no password source", []string{"auth", "add-user", "alice"}},
		{"both password sources", []string{"auth", "add-user", "alice", "--password-stdin", "--password-env", "PW"}},
		{"unknown flag", []string{"auth", "add-user", "alice", "--password-stdin", "--bogus"}},
		{"extra positional", []string{"auth", "add-user", "alice", "bob", "--password-stdin"}},
		{"unknown subcommand", []string{"auth", "delete-user", "alice"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, out, errb := runInput(nil, tc.args...)
			if code != 2 {
				t.Fatalf("code = %d, want 2 (stdout=%q stderr=%q)", code, out, errb)
			}
			if out != "" {
				t.Fatalf("usage error must not write stdout: %q", out)
			}
			assertNoSecrets(t, out, errb, "leak-marker-9f3a")
		})
	}
}

func TestAuthAddUserEmptyPasswordIsUsage(t *testing.T) {
	newCtx(t)
	code, out, errb := runInput(strings.NewReader("\n"),
		"auth", "add-user", "empty", "--password-stdin")
	if code != 2 {
		t.Fatalf("empty password: code = %d, want 2 (validation semantics), stderr = %q", code, errb)
	}
	if !strings.Contains(errb, "password too short") {
		t.Fatalf("unexpected empty-password error: %q", errb)
	}
	if out != "" {
		t.Fatalf("usage error must not write stdout: %q", out)
	}
}

func TestAuthAddUserOversizedPasswordIsUsage(t *testing.T) {
	newCtx(t)
	// An unbounded pipe must be cut off by the reader cap: the oversized
	// input must fail fast with the length error, not be read in full.
	code, out, errb := runInput(strings.NewReader(strings.Repeat("x", auth.MaxPasswordLen*4)),
		"auth", "add-user", "big", "--password-stdin")
	if code != 2 {
		t.Fatalf("oversized password: code = %d, want 2, stderr = %q", code, errb)
	}
	if !strings.Contains(errb, "password too long") {
		t.Fatalf("unexpected oversized-password error: %q", errb)
	}
	assertNoSecrets(t, out, errb, strings.Repeat("x", 8))
}

func TestAuthAddUserPasswordAtLimit(t *testing.T) {
	newCtx(t)
	atLimit := strings.Repeat("y", auth.MaxPasswordLen)
	code, out, errb := runInput(strings.NewReader(atLimit+"\n"),
		"auth", "add-user", "atlimit", "--password-stdin")
	if code != 0 {
		t.Fatalf("password at limit: code = %d, stderr = %q", code, errb)
	}
	assertNoSecrets(t, out, errb, atLimit)
	u := authUser(t, "atlimit")
	if !auth.VerifyPassword(atLimit, u.PasswordHash) {
		t.Fatal("password at MaxPasswordLen must verify")
	}
}

func TestAuthAddUserEnvUnsetIsOperational(t *testing.T) {
	newCtx(t)
	code, out, errb := runInput(nil, "auth", "add-user", "nobody", "--password-env", "VAR_THAT_DOES_NOT_EXIST_42")
	if code != 1 {
		t.Fatalf("unset env: code = %d, want 1", code)
	}
	if !strings.Contains(errb, "environment variable VAR_THAT_DOES_NOT_EXIST_42 is not set") {
		t.Fatalf("unexpected env error: %q", errb)
	}
	if out != "" {
		t.Fatalf("failure must not write stdout: %q", out)
	}
}

func TestAuthAddUserInvalidUsername(t *testing.T) {
	newCtx(t)
	t.Setenv("PW", "x")
	cases := []struct {
		name string
		user string
		leak string
	}{
		{"whitespace only", "   ", "   "},
		{"too long", strings.Repeat("u", 65), strings.Repeat("u", 65)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, out, errb := runInput(nil, "auth", "add-user", tc.user, "--password-env", "PW")
			if code != 2 {
				t.Fatalf("code = %d, want 2", code)
			}
			assertNoSecrets(t, out, errb, tc.leak)
		})
	}
}

func TestAuthAddUserTrailingNewlineStripped(t *testing.T) {
	newCtx(t)
	code, _, errb := runInput(strings.NewReader("padded\r\n"),
		"auth", "add-user", "win", "--password-stdin")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, errb)
	}
	u := authUser(t, "win")
	if !auth.VerifyPassword("padded", u.PasswordHash) {
		t.Fatal("password with trailing CRLF must verify as 'padded'")
	}
	if auth.VerifyPassword("padded\r\n", u.PasswordHash) {
		t.Fatal("password must not include the trailing newline")
	}
}

func TestAuthAddUserKeepsInnerSpaces(t *testing.T) {
	newCtx(t)
	t.Setenv("PW", " pass word ")
	code, _, errb := runInput(nil, "auth", "add-user", "spaces", "--password-env", "PW")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, errb)
	}
	u := authUser(t, "spaces")
	if !auth.VerifyPassword(" pass word ", u.PasswordHash) {
		t.Fatal("env password must be verified verbatim, spaces included")
	}
}
