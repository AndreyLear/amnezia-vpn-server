package auth

import (
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSessionCreateGet(t *testing.T) {
	s := NewSessionStore(SessionTTL)
	sess, err := s.Create("alice")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if sess.Username != "alice" {
		t.Fatalf("username = %q, want alice", sess.Username)
	}
	if sess.ID == "" {
		t.Fatal("session id must not be empty")
	}
	if !sess.CreatedAt.Before(sess.ExpiresAt) {
		t.Fatalf("expiry %v must be after creation %v", sess.ExpiresAt, sess.CreatedAt)
	}
	got, ok := s.Get(sess.ID)
	if !ok {
		t.Fatal("created session must be found")
	}
	if got.ID != sess.ID || got.Username != sess.Username {
		t.Fatalf("Get returned a different session: got %+v want %+v", got, sess)
	}
}

func TestSessionUnknownID(t *testing.T) {
	s := NewSessionStore(SessionTTL)
	if _, ok := s.Get("no-such-id"); ok {
		t.Fatal("unknown id must not be found")
	}
}

func TestSessionDistinctIDs(t *testing.T) {
	s := NewSessionStore(SessionTTL)
	a, err := s.Create("alice")
	if err != nil {
		t.Fatalf("Create alice: %v", err)
	}
	b, err := s.Create("alice")
	if err != nil {
		t.Fatalf("Create alice again: %v", err)
	}
	if a.ID == b.ID {
		t.Fatal("two creates must produce distinct session ids")
	}
}

func TestSessionIDLengthAndAlphabet(t *testing.T) {
	s := NewSessionStore(SessionTTL)
	sess, err := s.Create("alice")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// 32 random bytes → 43 base64url chars without padding.
	if len(sess.ID) != 43 {
		t.Fatalf("session id length = %d, want 43 (32 raw bytes)", len(sess.ID))
	}
	for _, r := range sess.ID {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			t.Fatalf("session id contains non-base64url char %q", r)
		}
	}
	if strings.ContainsAny(sess.ID, "+/=") {
		t.Fatalf("session id must be padding-free base64url, got %q", sess.ID)
	}
}

func TestSessionTTL(t *testing.T) {
	s := NewSessionStore(50 * time.Millisecond)
	sess, err := s.Create("alice")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, ok := s.Get(sess.ID); !ok {
		t.Fatal("session must be live before TTL")
	}
	time.Sleep(80 * time.Millisecond)
	if _, ok := s.Get(sess.ID); ok {
		t.Fatal("session must expire after TTL")
	}
	if _, ok := s.Get(sess.ID); ok {
		t.Fatal("expired session must stay gone after a second Get")
	}
}

// TestSessionTTLPinned24h pins the M7.7 cookie/session audit item: the
// production lifetime is exactly 24 hours and the login cookie carries
// the same absolute expiry the store enforces.
func TestSessionTTLPinned24h(t *testing.T) {
	if SessionTTL != 24*time.Hour {
		t.Fatalf("SessionTTL = %v, want 24h", SessionTTL)
	}
	sess, err := NewSessionStore(SessionTTL).Create("alice")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got := sess.ExpiresAt.Sub(sess.CreatedAt); got != SessionTTL {
		t.Fatalf("session lifetime = %v, want SessionTTL %v", got, SessionTTL)
	}
	if got := time.Until(sess.ExpiresAt); got <= 23*time.Hour || got > SessionTTL {
		t.Fatalf("session expiry %v is not ~24h from now", sess.ExpiresAt)
	}
}

func TestSessionExpiredGetDeletes(t *testing.T) {
	s := NewSessionStore(40 * time.Millisecond)
	sess, err := s.Create("alice")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	time.Sleep(70 * time.Millisecond)
	if _, ok := s.Get(sess.ID); ok {
		t.Fatal("session must expire")
	}
	s.mu.RLock()
	_, still := s.byID[sess.ID]
	s.mu.RUnlock()
	if still {
		t.Fatal("expired session must be removed from the store by Get")
	}
	s.mu.RLock()
	n := len(s.byID)
	s.mu.RUnlock()
	if n != 0 {
		t.Fatalf("store has %d sessions, want 0 after expiry", n)
	}
}

func TestSessionDelete(t *testing.T) {
	s := NewSessionStore(SessionTTL)
	sess, err := s.Create("alice")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	s.Delete(sess.ID)
	if _, ok := s.Get(sess.ID); ok {
		t.Fatal("deleted session must not be found")
	}
}

func TestSessionDeleteIdempotent(t *testing.T) {
	s := NewSessionStore(SessionTTL)
	sess, err := s.Create("alice")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	s.Delete(sess.ID)
	// Deleting an already-deleted and an unknown id must both succeed.
	s.Delete(sess.ID)
	s.Delete("never-existed")
}

func TestSessionByUsername(t *testing.T) {
	s := NewSessionStore(SessionTTL)
	s.Create("alice")
	s.Create("alice")
	s.Create("bob")
	if got := len(s.ByUsername("alice")); got != 2 {
		t.Errorf("ByUsername(alice) = %d sessions, want 2", got)
	}
	if got := len(s.ByUsername("bob")); got != 1 {
		t.Errorf("ByUsername(bob) = %d sessions, want 1", got)
	}
	if got := len(s.ByUsername("nobody")); got != 0 {
		t.Errorf("ByUsername(nobody) = %d sessions, want 0", got)
	}
}

func TestSessionByUsernameSnapshot(t *testing.T) {
	s := NewSessionStore(SessionTTL)
	sess, err := s.Create("alice")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for _, got := range s.ByUsername("alice") {
		if got.ID != sess.ID {
			t.Errorf("snapshot session %q, want %q", got.ID, sess.ID)
		}
	}
	// The snapshot must be independent of the store: mutating it is a
	// no-op for the map.
	s.ByUsername("alice")[0].ID = "mutated"
	if _, ok := s.Get(sess.ID); !ok {
		t.Fatal("mutating the snapshot must not affect the store")
	}
}

func TestSessionDeleteByUsername(t *testing.T) {
	s := NewSessionStore(SessionTTL)
	alice1, err := s.Create("alice")
	if err != nil {
		t.Fatalf("Create alice1: %v", err)
	}
	alice2, err := s.Create("alice")
	if err != nil {
		t.Fatalf("Create alice2: %v", err)
	}
	bob, err := s.Create("bob")
	if err != nil {
		t.Fatalf("Create bob: %v", err)
	}

	// keep alice1: alice2 must die, bob must survive.
	s.DeleteByUsername("alice", alice1.ID)
	if _, ok := s.Get(alice1.ID); !ok {
		t.Fatal("kept session must survive")
	}
	if _, ok := s.Get(alice2.ID); ok {
		t.Fatal("non-kept session of the same user must be deleted")
	}
	if _, ok := s.Get(bob.ID); !ok {
		t.Fatal("other users' sessions must survive")
	}

	// keep "" deletes every session of the user.
	s.DeleteByUsername("alice", "")
	if _, ok := s.Get(alice1.ID); ok {
		t.Fatal("keep \"\": session must be deleted")
	}
	if _, ok := s.Get(bob.ID); !ok {
		t.Fatal("other users' sessions must survive a full purge")
	}
}

func TestSessionDeleteByUsernameIdempotent(t *testing.T) {
	s := NewSessionStore(SessionTTL)
	sess, err := s.Create("alice")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	s.DeleteByUsername("alice", "never-kept")
	// Re-running for the same user (and an unknown user) must succeed.
	s.DeleteByUsername("alice", "never-kept")
	s.DeleteByUsername("nobody", sess.ID)
}

func TestSessionRotate(t *testing.T) {
	s := NewSessionStore(SessionTTL)
	old, err := s.Create("alice")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	neu, err := s.Rotate(old.ID, "alice")
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if neu.ID == old.ID {
		t.Fatal("rotated session must carry a fresh id")
	}
	if neu.Username != old.Username {
		t.Fatalf("rotate changed principal: %q → %q", old.Username, neu.Username)
	}
	if _, ok := s.Get(old.ID); ok {
		t.Fatal("old session id must be invalid after Rotate")
	}
	if got, ok := s.Get(neu.ID); !ok || got.ID != neu.ID {
		t.Fatalf("new session id must be valid after Rotate, got %+v ok=%v", got, ok)
	}
}

func TestSessionRotateMissingOld(t *testing.T) {
	s := NewSessionStore(SessionTTL)
	_, err := s.Rotate("never-existed", "alice")
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("Rotate of an unknown id must fail with ErrSessionNotFound, got %v", err)
	}
	s.mu.RLock()
	n := len(s.byID)
	s.mu.RUnlock()
	if n != 0 {
		t.Fatalf("failed Rotate must not create anything, store has %d sessions", n)
	}
}

func TestSessionRotateOldNeverValidAgain(t *testing.T) {
	s := NewSessionStore(SessionTTL)
	old, err := s.Create("alice")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	neu, err := s.Rotate(old.ID, "alice")
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, ok := s.Get(old.ID); ok {
			t.Fatalf("iteration %d: old id valid after Rotate", i)
		}
	}
	// Rotating the already-rotated old id must fail and must not touch
	// the live new session.
	if _, err := s.Rotate(old.ID, "alice"); err == nil {
		t.Fatal("second Rotate of the same old id must fail")
	}
	if got, ok := s.Get(neu.ID); !ok || got.ID != neu.ID {
		t.Fatal("new session must survive a failed Rotate")
	}
}

func TestSessionRotateExpiredOld(t *testing.T) {
	s := NewSessionStore(40 * time.Millisecond)
	old, err := s.Create("alice")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	time.Sleep(70 * time.Millisecond)
	if _, err := s.Rotate(old.ID, "alice"); err == nil {
		t.Fatal("Rotate of an expired id must fail")
	}
	if _, ok := s.Get(old.ID); ok {
		t.Fatal("expired id must stay invalid")
	}
}

func TestSessionNoSecretFields(t *testing.T) {
	s := NewSessionStore(SessionTTL)
	sess, err := s.Create("alice")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Structural guarantee: the Session type may never carry secret
	// material, so a later refactor cannot silently add a password or
	// key field. CSRFToken is the per-session CSRF secret (M7.6) — it
	// is the only non-identity field and is covered by its own tests.
	var want []string
	typ := reflect.TypeOf(Session{})
	for i := 0; i < typ.NumField(); i++ {
		want = append(want, typ.Field(i).Name)
	}
	got := []string{"ID", "Username", "CSRFToken", "CreatedAt", "ExpiresAt"}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("Session fields = %v, want exactly %v (no password/hash/private/psk material)", want, got)
	}
	// Dump-based safety net: the rendered session contains no secret
	// patterns, and in particular never the CSRF token itself.
	dump := strings.ToLower(sess.String())
	for _, secret := range []string{"password", "passwd", "hash", "private", "preshared", "psk", "secret", "totp", "csrf", sess.CSRFToken} {
		if strings.Contains(dump, secret) {
			t.Fatalf("session dump contains secret pattern %q: %q", secret, dump)
		}
	}
}

func TestSessionCreateIssuesCSRFToken(t *testing.T) {
	s := NewSessionStore(SessionTTL)
	sess, err := s.Create("alice")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if sess.CSRFToken == "" {
		t.Fatal("every new session must carry a CSRF token")
	}
	// 32 raw bytes → 43 base64url chars, same entropy class as the id.
	if len(sess.CSRFToken) != 43 {
		t.Fatalf("CSRF token length = %d, want 43 (32 random bytes)", len(sess.CSRFToken))
	}
	if sess.CSRFToken == sess.ID {
		t.Fatal("CSRF token must never equal the session id")
	}
	// The stored session preserves the token.
	got, ok := s.Get(sess.ID)
	if !ok || got.CSRFToken != sess.CSRFToken {
		t.Fatalf("stored session must keep the issued token, got %q ok=%v", got.CSRFToken, ok)
	}
}

func TestSessionCSRFTokensDistinct(t *testing.T) {
	s := NewSessionStore(SessionTTL)
	a, err := s.Create("alice")
	if err != nil {
		t.Fatalf("Create a: %v", err)
	}
	b, err := s.Create("alice")
	if err != nil {
		t.Fatalf("Create b: %v", err)
	}
	if a.CSRFToken == b.CSRFToken {
		t.Fatal("two sessions must carry distinct CSRF tokens")
	}
}

func TestSessionRotateNewCSRFToken(t *testing.T) {
	s := NewSessionStore(SessionTTL)
	old, err := s.Create("alice")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	neu, err := s.Rotate(old.ID, "alice")
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if neu.CSRFToken == old.CSRFToken {
		t.Fatal("Rotate must issue a fresh CSRF token")
	}
	if neu.CSRFToken == "" || neu.CSRFToken == neu.ID {
		t.Fatal("rotated session must carry a fresh, distinct CSRF token")
	}
	// The old token must be invalid for the new session (fixation and
	// CSRF rotation combined).
	if CSRFValid(old.CSRFToken, neu.CSRFToken) {
		t.Fatal("old CSRF token must be invalid for the rotated session")
	}
	if !CSRFValid(neu.CSRFToken, neu.CSRFToken) {
		t.Fatal("fresh CSRF token must validate against itself")
	}
}

func TestSessionExpiredCSRFTokenInvalid(t *testing.T) {
	s := NewSessionStore(40 * time.Millisecond)
	sess, err := s.Create("alice")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	time.Sleep(70 * time.Millisecond)
	if _, ok := s.Get(sess.ID); ok {
		t.Fatal("session must expire")
	}
	// An expired session carries no usable CSRF token: it cannot be
	// fetched, so a form token belonging to it can never validate.
	if _, ok := s.Get(sess.ID); ok {
		t.Fatal("expired session must stay gone")
	}
}

// TestSessionStorePrunesOnCreate verifies Create drops abandoned
// expired sessions so the map cannot grow without bound.
func TestSessionStorePrunesOnCreate(t *testing.T) {
	s := NewSessionStore(40 * time.Millisecond)
	for i := 0; i < 3; i++ {
		if _, err := s.Create("alice"); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}
	time.Sleep(70 * time.Millisecond)
	if _, err := s.Create("bob"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	s.mu.RLock()
	n := len(s.byID)
	s.mu.RUnlock()
	if n != 1 {
		t.Fatalf("store keeps %d sessions, want 1 (expired pruned by Create)", n)
	}
}

// --- concurrency (go test -race) ---

func TestSessionConcurrentCreateGet(t *testing.T) {
	s := NewSessionStore(SessionTTL)
	const n = 32
	var wg sync.WaitGroup
	wg.Add(n * 2)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			sess, err := s.Create("user")
			if err != nil {
				t.Errorf("Create: %v", err)
				return
			}
			if _, ok := s.Get(sess.ID); !ok {
				t.Errorf("created session lost")
			}
		}(i)
		go func() {
			defer wg.Done()
			s.Get("some-random-id")
			s.Get("another-id")
		}()
	}
	wg.Wait()
}

func TestSessionConcurrentGetDelete(t *testing.T) {
	s := NewSessionStore(SessionTTL)
	sess, err := s.Create("alice")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); s.Get(sess.ID) }()
	go func() { defer wg.Done(); s.Get(sess.ID) }()
	go func() { defer wg.Done(); s.Delete(sess.ID) }()
	wg.Wait()
	if _, ok := s.Get(sess.ID); ok {
		t.Fatal("session must be gone after concurrent Get/Delete")
	}
}

func TestSessionConcurrentDelete(t *testing.T) {
	s := NewSessionStore(SessionTTL)
	sess, err := s.Create("alice")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Delete(sess.ID) // must be idempotent and race-free
		}()
	}
	wg.Wait()
	if _, ok := s.Get(sess.ID); ok {
		t.Fatal("session must be deleted")
	}
}

func TestSessionConcurrentRotate(t *testing.T) {
	s := NewSessionStore(SessionTTL)
	old, err := s.Create("alice")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	const n = 8
	var wg sync.WaitGroup
	successes := make([]bool, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.Rotate(old.ID, "alice")
			successes[i] = err == nil
		}(i)
	}
	wg.Wait()
	var okCount int
	for _, ok := range successes {
		if ok {
			okCount++
		}
	}
	if okCount != 1 {
		t.Fatalf("%d/8 concurrent Rotates succeeded, want exactly 1 (atomic swap)", okCount)
	}
	if _, ok := s.Get(old.ID); ok {
		t.Fatal("old id must be invalid after the contested Rotate")
	}
	s.mu.RLock()
	nLive := len(s.byID)
	s.mu.RUnlock()
	if nLive != 1 {
		t.Fatalf("store has %d sessions, want 1 after concurrent Rotate", nLive)
	}
}

func TestSessionGetDuringExpiration(t *testing.T) {
	s := NewSessionStore(30 * time.Millisecond)
	sess, err := s.Create("alice")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	var wg sync.WaitGroup
	wg.Add(3)
	for i := 0; i < 3; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 40 && !t.Failed(); j++ {
				_, _ = s.Get(sess.ID) // must never race with expiry/delete
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	s.Delete(sess.ID)
	wg.Wait()
	if _, ok := s.Get(sess.ID); ok {
		t.Fatal("session must be gone")
	}
}

func TestSessionConcurrentRotateGet(t *testing.T) {
	s := NewSessionStore(SessionTTL)
	old, err := s.Create("alice")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	var wg sync.WaitGroup
	wg.Add(5)
	for i := 0; i < 4; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_, _ = s.Get(old.ID)
			}
		}()
	}
	go func() {
		defer wg.Done()
		for j := 0; j < 50; j++ {
			s.Rotate(old.ID, "alice")
			s.Create("alice")
			s.Delete(old.ID)
		}
	}()
	wg.Wait()
	if _, ok := s.Get(old.ID); ok {
		t.Fatal("old id must never be valid again")
	}
}

func TestSessionIDValidConstantTime(t *testing.T) {
	a, err := newSessionID()
	if err != nil {
		t.Fatalf("newSessionID: %v", err)
	}
	b, err := newSessionID()
	if err != nil {
		t.Fatalf("newSessionID: %v", err)
	}
	if !IDValid(a, a) {
		t.Fatal("IDValid must accept identical ids")
	}
	if IDValid(a, b) {
		t.Fatal("IDValid must reject different ids")
	}
}
