package db

import (
	"errors"
	"strings"
	"testing"
)

func TestCreateAndReadAuthUser(t *testing.T) {
	handle, _ := openTest(t, "auth.sqlite")
	if err := Migrate(handle); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	hash := "$argon2id$v=19$m=65536,t=1,p=4$c2FsdHNhbHRzYWx0c2FsdA$c2FsdHNhbHRzYWx0c2FsdHNhbHRz"
	u, err := CreateAuthUser(handle, "admin", hash)
	if err != nil {
		t.Fatalf("CreateAuthUser: %v", err)
	}
	if u.Username != "admin" || u.PasswordHash != hash || u.ID < 1 {
		t.Fatalf("created user mismatch: %+v", u)
	}

	got, err := AuthUserByUsername(handle, "admin")
	if err != nil {
		t.Fatalf("AuthUserByUsername: %v", err)
	}
	if got.Username != "admin" || got.PasswordHash != hash || got.ID != u.ID {
		t.Fatalf("read user mismatch: got %+v, want id=%d username=admin", got, u.ID)
	}
}

func TestAuthUserNotFound(t *testing.T) {
	handle, _ := openTest(t, "auth.sqlite")
	if err := Migrate(handle); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if _, err := AuthUserByUsername(handle, "nobody"); !errors.Is(err, ErrAuthUserNotFound) {
		t.Fatalf("missing user: want ErrAuthUserNotFound, got %v", err)
	}
}

func TestCreateAuthUserDuplicate(t *testing.T) {
	handle, _ := openTest(t, "auth.sqlite")
	if err := Migrate(handle); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if _, err := CreateAuthUser(handle, "admin", "hash-1"); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := CreateAuthUser(handle, "admin", "hash-2"); !errors.Is(err, ErrAuthUserExists) {
		t.Fatalf("duplicate username: want ErrAuthUserExists, got %v", err)
	}
	got, err := AuthUserByUsername(handle, "admin")
	if err != nil {
		t.Fatalf("read after duplicate: %v", err)
	}
	if got.PasswordHash != "hash-1" {
		t.Fatalf("duplicate create must not overwrite the hash: got %q", got.PasswordHash)
	}
}

func TestCreateAuthUserValidation(t *testing.T) {
	handle, _ := openTest(t, "auth.sqlite")
	if err := Migrate(handle); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if _, err := CreateAuthUser(handle, "", "hash"); err == nil {
		t.Fatal("empty username must fail")
	}
	if _, err := CreateAuthUser(handle, "admin", ""); err == nil {
		t.Fatal("empty hash must fail")
	}
}

func TestAuthHashStoredVerbatim(t *testing.T) {
	handle, _ := openTest(t, "auth.sqlite")
	if err := Migrate(handle); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	hash := "$argon2id$v=19$m=65536,t=1,p=4$c2FsdHNhbHRzYWx0c2FsdA$c2FsdHNhbHRzYWx0c2FsdHNhbHRz"
	if _, err := CreateAuthUser(handle, "admin", hash); err != nil {
		t.Fatalf("CreateAuthUser: %v", err)
	}
	got, err := AuthUserByUsername(handle, "admin")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.PasswordHash != hash {
		t.Fatalf("hash was transformed: stored %q != given %q", got.PasswordHash, hash)
	}
}

func TestAuthHashNotInErrors(t *testing.T) {
	handle, _ := openTest(t, "auth.sqlite")
	if err := Migrate(handle); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	secretHash := "$argon2id$v=19$m=65536,t=1,p=4$c2FsdHNhbHRzYWx0c2FsdA$c2FsdHNhbHRzYWx0c2FsdHNhbHRz"
	if _, err := CreateAuthUser(handle, "admin", secretHash); err != nil {
		t.Fatalf("CreateAuthUser: %v", err)
	}
	var errs []error
	if _, err := AuthUserByUsername(handle, "missing"); err != nil {
		errs = append(errs, err)
	}
	if _, err := CreateAuthUser(handle, "admin", secretHash); err != nil {
		errs = append(errs, err)
	}
	if len(errs) == 0 {
		t.Fatal("expected at least one error path")
	}
	for _, err := range errs {
		if strings.Contains(err.Error(), secretHash) {
			t.Fatalf("error leaks the password hash: %q", err)
		}
	}
}

func TestAuthSchemaVersionUnchanged(t *testing.T) {
	handle, _ := openTest(t, "auth.sqlite")
	if err := Migrate(handle); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	v, err := SchemaVersionStored(handle)
	if err != nil {
		t.Fatalf("SchemaVersionStored: %v", err)
	}
	if v != SchemaVersion {
		t.Fatalf("schema_version = %q, want %q (auth must not bump it)", v, SchemaVersion)
	}
	if _, err := CreateAuthUser(handle, "admin", "hash"); err != nil {
		t.Fatalf("CreateAuthUser: %v", err)
	}
	v, err = SchemaVersionStored(handle)
	if err != nil {
		t.Fatalf("SchemaVersionStored after create: %v", err)
	}
	if v != SchemaVersion {
		t.Fatalf("schema_version changed after CreateAuthUser: %q", v)
	}
}
