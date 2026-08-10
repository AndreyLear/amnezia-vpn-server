// Package keys generates AmneziaWG/WireGuard key material with the Go
// standard library only (crypto/ecdh, crypto/rand). Format matches what
// amneziawg-tools and amneziawg-go expect: base64 standard encoding of
// 32 raw bytes (see awgconf.validKey in app/panel/internal/awgconf).
package keys

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

// keyLen is the X25519 key size in bytes.
const keyLen = 32

// GeneratePrivateKey returns a new random X25519 private key in the
// standard base64 form ("<44 chars>="). The public key can be derived
// from it with DerivePublicKey.
func GeneratePrivateKey() (string, error) {
	raw := make([]byte, keyLen)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("keys: read random: %w", err)
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

// GeneratePresharedKey returns a new random symmetric preshared key in
// the standard base64 form.
func GeneratePresharedKey() (string, error) {
	return GeneratePrivateKey()
}

// DerivePublicKey computes the X25519 public key for a private key in
// the standard base64 form. Used only at generation time: the MVP keeps
// public keys stored explicitly (docs/TECHNICAL_SPEC_v2.0.md §11).
func DerivePublicKey(privateKey string) (string, error) {
	if !ValidKey(privateKey) {
		return "", fmt.Errorf("keys: invalid private key: not a 32-byte base64 key")
	}
	raw, err := base64.StdEncoding.DecodeString(privateKey)
	if err != nil {
		return "", fmt.Errorf("keys: decode private key: %w", err)
	}
	priv, err := ecdh.X25519().NewPrivateKey(raw)
	if err != nil {
		return "", fmt.Errorf("keys: x25519 private key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(priv.PublicKey().Bytes()), nil
}

// GenerateKeyPair returns a fresh private/public key pair in the
// standard base64 form.
func GenerateKeyPair() (privateKey, publicKey string, err error) {
	privateKey, err = GeneratePrivateKey()
	if err != nil {
		return "", "", err
	}
	publicKey, err = DerivePublicKey(privateKey)
	if err != nil {
		return "", "", err
	}
	return privateKey, publicKey, nil
}

// ValidKey reports whether s is a standard base64-encoded 32-byte key,
// mirroring amneziawg-tools/src/config.c key_from_base64. The empty
// string is not a valid key.
func ValidKey(s string) bool {
	if len(s) != base64.StdEncoding.EncodedLen(keyLen) {
		return false
	}
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return false
	}
	return len(raw) == keyLen
}
