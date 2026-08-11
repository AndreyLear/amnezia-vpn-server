// M7.1 password hashing (TECHNICAL_SPEC_v2.0.md §6: "Argon2 or bcrypt").
// Argon2id via golang.org/x/crypto/argon2; the encoded hash is the PHC
// string format carrying params, salt and digest, so VerifyPassword
// needs no external state.
//
// Security invariants:
//   - the password never appears in any error: all errors are fixed
//     strings without input;
//   - the plaintext password exists only as a transient argument;
//   - Digest verification re-hashes with the stored parameters with
//     work comparable to hashing (Argon2 is not parallelizable in the
//     memory-hard dimension), so timing leaks nothing usable.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters (RFC 9106 reference settings: OWASP password
// storage recommendations for interactive logins).
const (
	// argon2Time is the number of passes.
	argon2Time = 1
	// argon2Memory is the memory cost in KiB (64 MiB).
	argon2Memory = 64 * 1024
	// argon2Threads is the parallelism factor.
	argon2Threads = 4
	// argon2KeyLen is the digest length in bytes.
	argon2KeyLen = 32
	// argon2SaltLen is the salt length in bytes.
	argon2SaltLen = 16
)

// MaxPasswordLen caps plaintext password size (bytes, not runes). The
// limit is far above any humane password; alongside the web body limit
// it keeps the login path from being a cost amplifier.
const MaxPasswordLen = 1024

// MinPasswordLen is the minimum accepted password length in bytes.
const MinPasswordLen = 1

// ErrPasswordTooShort reports a password below MinPasswordLen.
var ErrPasswordTooShort = errors.New("auth: password too short")

// ErrPasswordTooLong reports a password above MaxPasswordLen.
var ErrPasswordTooLong = errors.New("auth: password too long")

// HashPassword derives an Argon2id digest and encodes it as a PHC
// string carrying all parameters:
//
//	$argon2id$v=19$m=65536,t=1,p=4$<b64 salt>$<b64 digest>
//
// Every call draws a fresh crypto/rand salt. Errors are fixed strings
// and never contain the password.
func HashPassword(password string) (string, error) {
	if len(password) < MinPasswordLen {
		return "", ErrPasswordTooShort
	}
	if len(password) > MaxPasswordLen {
		return "", ErrPasswordTooLong
	}
	salt := make([]byte, argon2SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: generate salt: %w", err)
	}
	digest := argon2.IDKey([]byte(password), salt, argon2Time, argon2Memory, argon2Threads, argon2KeyLen)
	enc := base64.RawStdEncoding.EncodeToString
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argon2Memory, argon2Time, argon2Threads,
		enc(salt), enc(digest)), nil
}

// VerifyPassword checks the password against an encoded hash from
// HashPassword. It returns false for any malformed or foreign hash
// (unknown algorithm, bad version, missing fields, truncated base64)
// and for a wrong password; it never returns an error, so callers have
// a single boolean contract and cannot leak a reason.
func VerifyPassword(password, encodedHash string) bool {
	if password == "" || len(password) > MaxPasswordLen {
		return false
	}
	params, salt, digest, ok := parseHash(encodedHash)
	if !ok {
		return false
	}
	candidate := argon2.IDKey([]byte(password), salt, params.time, params.memory, params.threads, uint32(len(digest)))
	return subtle.ConstantTimeCompare(candidate, digest) == 1
}

type hashParams struct {
	memory  uint32
	time    uint32
	threads uint8
}

// parseHash decodes a PHC Argon2id string. Everything is validated
// strictly: the algorithm and version must match this implementation,
// memory/time/threads must be within sane bounds, and the salt and
// digest must decode cleanly.
func parseHash(encoded string) (hashParams, []byte, []byte, bool) {
	parts := strings.Split(encoded, "$")
	// ["", "argon2id", "v=19", "m=..,t=..,p=..", salt, digest]
	if len(parts) != 6 || parts[0] != "" {
		return hashParams{}, nil, nil, false
	}
	if parts[1] != "argon2id" {
		return hashParams{}, nil, nil, false
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return hashParams{}, nil, nil, false
	}
	var m, t, p int
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return hashParams{}, nil, nil, false
	}
	if m < 8*1024 || m > 1<<20 || t < 1 || t > 16 || p < 1 || p > 32 {
		// Reject absurd re-hash costs from a tampered hash.
		return hashParams{}, nil, nil, false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < 8 || len(salt) > 64 {
		return hashParams{}, nil, nil, false
	}
	digest, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(digest) < 16 || len(digest) > 64 {
		return hashParams{}, nil, nil, false
	}
	return hashParams{memory: uint32(m), time: uint32(t), threads: uint8(p)}, salt, digest, true
}
