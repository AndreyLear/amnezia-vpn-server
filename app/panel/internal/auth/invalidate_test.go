package auth

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInvalidateSessionsPath(t *testing.T) {
	got := InvalidateSessionsPath("/data/amnezia.sqlite")
	if got != "/data/amnezia.sqlite.invalidate-sessions" {
		t.Fatalf("path = %q", got)
	}
}

func TestRequestInvalidateSessionsMode0600(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "amnezia.sqlite")
	if err := RequestInvalidateSessions(dbPath, "alice"); err != nil {
		t.Fatal(err)
	}
	path := InvalidateSessionsPath(dbPath)
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 0600", fi.Mode().Perm())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "alice\n" {
		t.Fatalf("content = %q", raw)
	}
}

func TestConsumeInvalidateSessionsDeletesUser(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "amnezia.sqlite")
	store := NewSessionStore(SessionTTL)
	alice, err := store.Create("alice")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := store.Create("bob")
	if err != nil {
		t.Fatal(err)
	}
	if err := RequestInvalidateSessions(dbPath, "alice"); err != nil {
		t.Fatal(err)
	}
	ConsumeInvalidateSessions(dbPath, store)
	if _, ok := store.Get(alice.ID); ok {
		t.Fatal("alice session must be gone")
	}
	if _, ok := store.Get(bob.ID); !ok {
		t.Fatal("bob session must survive")
	}
	if _, err := os.Stat(InvalidateSessionsPath(dbPath)); !os.IsNotExist(err) {
		t.Fatalf("sentinel must be consumed, stat err = %v", err)
	}
}

func TestConsumeInvalidateSessionsStarDeletesAll(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "amnezia.sqlite")
	store := NewSessionStore(SessionTTL)
	a, _ := store.Create("alice")
	b, _ := store.Create("bob")
	if err := RequestInvalidateSessions(dbPath, "*"); err != nil {
		t.Fatal(err)
	}
	ConsumeInvalidateSessions(dbPath, store)
	if _, ok := store.Get(a.ID); ok {
		t.Fatal("alice must be gone")
	}
	if _, ok := store.Get(b.ID); ok {
		t.Fatal("bob must be gone")
	}
}

func TestConsumeInvalidateSessionsMissingIsNoop(t *testing.T) {
	store := NewSessionStore(SessionTTL)
	sess, err := store.Create("alice")
	if err != nil {
		t.Fatal(err)
	}
	ConsumeInvalidateSessions(filepath.Join(t.TempDir(), "amnezia.sqlite"), store)
	if _, ok := store.Get(sess.ID); !ok {
		t.Fatal("missing sentinel must not drop sessions")
	}
}

func TestRequestInvalidateSessionsRejectsEmpty(t *testing.T) {
	if err := RequestInvalidateSessions(filepath.Join(t.TempDir(), "db"), "  "); err == nil {
		t.Fatal("empty username must fail")
	}
}

func TestConsumeInvalidateSessionsAppliesLeftoverTaking(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "amnezia.sqlite")
	store := NewSessionStore(SessionTTL)
	alice, err := store.Create("alice")
	if err != nil {
		t.Fatal(err)
	}
	leftover := InvalidateSessionsPath(dbPath) + ".taking-crashed"
	if err := os.WriteFile(leftover, []byte("alice\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ConsumeInvalidateSessions(dbPath, store)
	if _, ok := store.Get(alice.ID); ok {
		t.Fatal("leftover taking file must still drop the session")
	}
}
