package auth

import (
	"errors"
	"strings"
	"testing"
)

func TestHashVerifyRoundTrip(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !VerifyPassword("correct horse battery staple", hash) {
		t.Fatal("correct password must verify")
	}
	if VerifyPassword("wrong password", hash) {
		t.Fatal("wrong password must not verify")
	}
}

func TestHashCarriesParams(t *testing.T) {
	hash, err := HashPassword("pw")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	want := "$argon2id$v=19$m=65536,t=1,p=4$"
	if !strings.HasPrefix(hash, want) {
		t.Fatalf("hash %q does not start with %q", hash, want)
	}
	parts := strings.Split(hash, "$")
	if len(parts) != 6 {
		t.Fatalf("hash %q has %d fields, want 6 (PHC)", hash, len(parts))
	}
	if parts[4] == "" || parts[5] == "" {
		t.Fatalf("hash %q misses salt/digest", hash)
	}
}

func TestHashesDifferBetweenPasswords(t *testing.T) {
	pw1 := strings.Repeat("a", 10)
	pw2 := strings.Repeat("b", 10)
	h1, err := HashPassword(pw1)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	h2, err := HashPassword(pw2)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if h1 == h2 {
		t.Fatal("distinct passwords must produce distinct hashes")
	}
}

func TestFreshSaltEveryHash(t *testing.T) {
	h1, err := HashPassword("same")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	h2, err := HashPassword("same")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if h1 == h2 {
		t.Fatal("two hashes of the same password must differ (fresh salt)")
	}
	if !VerifyPassword("same", h1) || !VerifyPassword("same", h2) {
		t.Fatal("both salts must verify")
	}
}

func TestVerifyRejectsTamperedHashes(t *testing.T) {
	hash, err := HashPassword("secret")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	cases := []string{
		"",
		"not-a-hash",
		"$argon2id$v=19$m=65536,t=1,p=4$AAAA",
		"$argon2i$v=19$m=65536,t=1,p=4$c2FsdHNhbHRzYWx0c2FsdA$c2FsdHNhbHRzYWx0c2FsdHNhbHRz",
		"$argon2id$v=18$m=65536,t=1,p=4$c2FsdHNhbHRzYWx0c2FsdA$c2FsdHNhbHRzYWx0c2FsdHNhbHRz",
		"$argon2id$v=19$m=99999999,t=1,p=4$c2FsdHNhbHRzYWx0c2FsdA$c2FsdHNhbHRzYWx0c2FsdHNhbHRz",
		"$argon2id$v=19$m=1024,t=1,p=4$c2FsdHNhbHRzYWx0c2FsdA$c2FsdHNhbHRzYWx0c2FsdHNhbHRz",
		// corrupt the salt segment with valid base64 of different bytes
		func() string {
			parts := strings.Split(hash, "$")
			parts[4] = "c2FsdHNhbHRzYWx0c2FsdA"
			return strings.Join(parts, "$")
		}(),
		// corrupt the digest first char (fully-deterministic bit flip)
		func() string {
			parts := strings.Split(hash, "$")
			if parts[5][:1] == "A" {
				parts[5] = "B" + parts[5][1:]
			} else {
				parts[5] = "A" + parts[5][1:]
			}
			return strings.Join(parts, "$")
		}(),
	}
	for _, c := range cases {
		if VerifyPassword("secret", c) {
			t.Errorf("VerifyPassword must reject tampered hash %q", c)
		}
	}
}

func TestVerifyRejectsEmptyOrOversizedPassword(t *testing.T) {
	hash, err := HashPassword("okpw")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if VerifyPassword("", hash) {
		t.Fatal("empty password must not verify")
	}
	if VerifyPassword(strings.Repeat("x", MaxPasswordLen+1), hash) {
		t.Fatal("oversized password must not verify")
	}
}

func TestPasswordLengthLimits(t *testing.T) {
	if _, err := HashPassword(""); !errors.Is(err, ErrPasswordTooShort) {
		t.Fatalf("empty password: want ErrPasswordTooShort, got %v", err)
	}
	atLimit := strings.Repeat("x", MaxPasswordLen)
	if _, err := HashPassword(atLimit); err != nil {
		t.Fatalf("password at limit must hash: %v", err)
	}
	over := strings.Repeat("x", MaxPasswordLen+1)
	if _, err := HashPassword(over); !errors.Is(err, ErrPasswordTooLong) {
		t.Fatalf("oversized password: want ErrPasswordTooLong, got %v", err)
	}
}

func TestErrorsNeverContainSecrets(t *testing.T) {
	secret := "hunter2-secret-password"
	over := secret + strings.Repeat("x", MaxPasswordLen)
	_, errShort := HashPassword("")
	_, errLong := HashPassword(over)
	for _, err := range []error{errShort, errLong} {
		if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "hunter2") {
			t.Fatalf("error leaks password material: %q", err)
		}
	}
	if !errors.Is(errShort, ErrPasswordTooShort) || !errors.Is(errLong, ErrPasswordTooLong) {
		t.Fatal("errors must keep their identity via errors.Is")
	}
}

func TestVerifyIsStableAgainstHiddenFields(t *testing.T) {
	for _, extra := range []string{
		"$$", "$$$$$$", "$abc$def$ghi$jkl$mno$pqr",
		"$argon2id$v=19$m=65536,t=1,p=4$$",
	} {
		if VerifyPassword("whatever", extra) {
			t.Errorf("VerifyPassword must reject %q", extra)
		}
	}
}

// TestColdHashVector pins an externally generated argon2id hash (PHC
// string, RFC 9106/OWASP reference parameters). It proves the toolchain
// and this implementation agree on the digest, independent of the code
// paths that produce hashes at runtime. Regenerating the vector is a
// deliberate, documented act: it must be computed once with the current
// parameters, not with previous ones.
func TestColdHashVector(t *testing.T) {
	const (
		password = "cold-vector-password"
		vector   = "$argon2id$v=19$m=65536,t=1,p=4$lnwkDuf0LJh2FSuIZyuglQ$oL8h4AyUF0WVwUHrkHdJBE5u/zXJl9zyj5XKT1CSNMk"
	)
	if !VerifyPassword(password, vector) {
		t.Fatal("pinned cold vector must verify with the current implementation")
	}
	if VerifyPassword("cold-vector-wrong", vector) {
		t.Fatal("wrong password must not verify against the pinned vector")
	}
}
