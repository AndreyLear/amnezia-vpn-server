package keys

import (
	"crypto/ecdh"
	"encoding/base64"
	"testing"
)

func mustDecode(t *testing.T, key string) []byte {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(key)
	if err != nil {
		t.Fatalf("decode %q: %v", key, err)
	}
	return raw
}

func TestGeneratePrivateKey(t *testing.T) {
	for i := 0; i < 10; i++ {
		key, err := GeneratePrivateKey()
		if err != nil {
			t.Fatalf("GeneratePrivateKey: %v", err)
		}
		if !ValidKey(key) {
			t.Fatalf("generated key %q fails ValidKey", key)
		}
		if len(mustDecode(t, key)) != 32 {
			t.Fatalf("generated key %q is not 32 bytes", key)
		}
	}
}

func TestGeneratePresharedKey(t *testing.T) {
	for i := 0; i < 10; i++ {
		key, err := GeneratePresharedKey()
		if err != nil {
			t.Fatalf("GeneratePresharedKey: %v", err)
		}
		if !ValidKey(key) {
			t.Fatalf("generated preshared key %q fails ValidKey", key)
		}
	}
}

func TestKeyUniqueness(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		key, err := GeneratePrivateKey()
		if err != nil {
			t.Fatalf("GeneratePrivateKey: %v", err)
		}
		if seen[key] {
			t.Fatalf("duplicate key %q", key)
		}
		seen[key] = true
	}
}

func TestGenerateKeyPair(t *testing.T) {
	for i := 0; i < 10; i++ {
		priv, pub, err := GenerateKeyPair()
		if err != nil {
			t.Fatalf("GenerateKeyPair: %v", err)
		}
		if !ValidKey(priv) || !ValidKey(pub) {
			t.Fatalf("pair (%q, %q) fails ValidKey", priv, pub)
		}
		if priv == pub {
			t.Fatalf("private and public keys are identical")
		}
		// the public key must satisfy X25519(priv, basepoint) == pub
		derived, err := DerivePublicKey(priv)
		if err != nil {
			t.Fatalf("DerivePublicKey: %v", err)
		}
		if derived != pub {
			t.Fatalf("DerivePublicKey(%q) = %q, want %q", priv, derived, pub)
		}
	}
}

func TestDerivePublicKeyMatchesECDH(t *testing.T) {
	privKey, pubKey, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	raw := mustDecode(t, privKey)
	priv, err := ecdh.X25519().NewPrivateKey(raw)
	if err != nil {
		t.Fatalf("NewPrivateKey: %v", err)
	}
	got := base64.StdEncoding.EncodeToString(priv.PublicKey().Bytes())
	if got != pubKey {
		t.Fatalf("ecdh public = %q, want %q", got, pubKey)
	}
}

func TestValidKey(t *testing.T) {
	good, err := GeneratePrivateKey()
	if err != nil {
		t.Fatalf("GeneratePrivateKey: %v", err)
	}
	for _, tc := range []struct {
		name string
		key  string
		want bool
	}{
		{"generated", good, true},
		{"empty", "", false},
		{"too short", "AAAA", false},
		{"31 bytes", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA==", false},
		{"32 bytes", good, true},
		{"45 chars", good + "A", false},
		{"bad base64", "!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!", false},
		{"wrong charset", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA!!", false},
	} {
		if got := ValidKey(tc.key); got != tc.want {
			t.Errorf("ValidKey(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestDerivePublicKeyRejectsInvalid(t *testing.T) {
	if _, err := DerivePublicKey("not-a-key"); err == nil {
		t.Fatal("DerivePublicKey accepted an invalid key")
	}
	if _, err := DerivePublicKey(""); err == nil {
		t.Fatal("DerivePublicKey accepted an empty key")
	}
}
