// M7.3 in-memory server-side session store (TECHNICAL_SPEC_v2.0.md §6:
// cookie sessions). Purely process-local: restarting the panel discards
// every session, which is the expected behavior until a persistent
// store is introduced.
//
// Scope discipline:
//   - this package holds no HTTP/cookie logic (that is the web layer);
//     the CSRF token lives here because it is bound to a session
//     (M7.6): the middleware in auth.go consumes it, the template in
//     the web layer renders it into hidden form fields;
//   - a session carries only identity (username, stamps) plus the
//     per-session CSRF token — never passwords, hashes, private/
//     Preshared keys;
//   - SQLite is not touched (no sessions table, schema_version stays 3).
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"
)

// SessionTTL is the absolute lifetime of a session (no sliding
// expiration). Default lifetime chosen for an admin panel; only the
// panel process itself can extend a session via Rotate.
const SessionTTL = 24 * time.Hour

// ErrSessionNotFound reports a missing or already-expired session.
var ErrSessionNotFound = errors.New("auth: session not found")

// ErrSessionIDUnavailable reports a crypto/rand failure while deriving
// a session id (essentially a broken entropy source).
var ErrSessionIDUnavailable = errors.New("auth: cannot generate session id")

// ErrCSRFTokenUnavailable reports a crypto/rand failure while deriving
// a session's CSRF token.
var ErrCSRFTokenUnavailable = errors.New("auth: cannot generate csrf token")

// Session identifies one logged-in principal for the lifetime of the
// panel process. ID is derived from 32 random bytes (base64url, cookie
// safe); CSRFToken is the per-session CSRF secret (32 random bytes,
// base64url) used by the M7.6 middleware — it never leaves the server
// except inside hidden form fields rendered by the web layer.
// CreatedAt/ExpiresAt are UTC stamps with absolute TTL. No password,
// hash, private-key or Preshared-key material is ever present.
type Session struct {
	ID        string
	Username  string
	CSRFToken string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// randomToken returns 32 bytes from crypto/rand encoded base64url
// without padding (43 chars, alphabet-safe, unguessable). Both the
// session id and the CSRF token are derived from it: same entropy
// class, distinct namespaces.
func randomToken() (string, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf[:]), nil
}

// newSessionID returns a fresh random session id.
func newSessionID() (string, error) {
	t, err := randomToken()
	if err != nil {
		return "", ErrSessionIDUnavailable
	}
	return t, nil
}

// newCSRFToken returns a fresh random CSRF token for a new session.
func newCSRFToken() (string, error) {
	t, err := randomToken()
	if err != nil {
		return "", ErrCSRFTokenUnavailable
	}
	return t, nil
}

// SessionStore is a concurrency-safe in-memory map of live sessions.
// Expired sessions are removed lazily: on Get they count as missing and
// are deleted; Create also drops any expired entries it encounters, so
// abandoned sessions cannot accumulate beyond the store's activity.
type SessionStore struct {
	mu   sync.RWMutex
	byID map[string]Session
	ttl  time.Duration
}

// NewSessionStore returns an empty store. ttl is the absolute session
// lifetime for sessions created by this store; tests pass short ttl
// values, production code uses SessionTTL.
func NewSessionStore(ttl time.Duration) *SessionStore {
	if ttl <= 0 {
		ttl = SessionTTL
	}
	return &SessionStore{
		byID: make(map[string]Session),
		ttl:  ttl,
	}
}

// Create issues a new session for username with a fresh random id and
// a fresh random CSRF token. The random values are generated before
// any lock is taken, so a rand failure never blocks the store.
func (s *SessionStore) Create(username string) (Session, error) {
	id, err := newSessionID()
	if err != nil {
		return Session{}, err
	}
	token, err := newCSRFToken()
	if err != nil {
		return Session{}, err
	}
	now := time.Now().UTC()
	sess := Session{
		ID:        id,
		Username:  username,
		CSRFToken: token,
		CreatedAt: now,
		ExpiresAt: now.Add(s.ttl),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(now)
	s.byID[id] = sess
	return sess, nil
}

// Get returns the live session for id. An expired session counts as
// missing: it is removed and Get reports false, so callers never see a
// dead session. A session id is written at most once (fresh random ids
// per Create/Rotate), so the only races are readers vs. a concurrent
// Delete/Rotate removing the entry.
func (s *SessionStore) Get(id string) (Session, bool) {
	now := time.Now().UTC()
	s.mu.RLock()
	sess, ok := s.byID[id]
	live := ok && sess.ExpiresAt.After(now)
	s.mu.RUnlock()
	if ok && !live {
		// Expiry is observed under the read lock, but removal is a
		// write: re-acquire the write lock and re-check, so two
		// concurrent expiring Gets (or a racing Delete) stay safe.
		s.mu.Lock()
		cur, still := s.byID[id]
		if still && !cur.ExpiresAt.After(time.Now().UTC()) {
			delete(s.byID, id)
		}
		s.mu.Unlock()
		return Session{}, false
	}
	if !live {
		return Session{}, false
	}
	return sess, true
}

// Delete removes a session. It is idempotent: deleting a missing or
// already-expired id succeeds silently.
func (s *SessionStore) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byID, id)
}

// DeleteAll drops every live session. Used after an in-process restore
// swaps the auth table: cookies issued against the previous database
// must not authorize the restored one.
func (s *SessionStore) DeleteAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byID = make(map[string]Session)
}

// DeleteByUsername removes every session of username except the one
// whose id equals keep (keep "" deletes all). It enforces the panel's
// one-active-login-per-user contract (M7.5): after a successful login
// the caller keeps the fresh session and drops any earlier ones, so a
// new login invalidates sessions from other browsers or devices.
func (s *SessionStore) DeleteByUsername(username, keep string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, sess := range s.byID {
		if sess.Username == username && id != keep {
			delete(s.byID, id)
		}
	}
}

// ByUsername returns a snapshot of every session belonging to
// username. Liveness is left to Get and callers: the login flow and
// the web tests count "active sessions per user" through this view.
func (s *SessionStore) ByUsername(username string) []Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Session
	for _, sess := range s.byID {
		if sess.Username == username {
			out = append(out, sess)
		}
	}
	return out
}

// Rotate atomically invalidates the session oldID and issues a new one
// for the same principal (session-fixation protection: any old id must
// never be valid after a successful rotate). The swap happens under one
// write lock, so no concurrent Get can observe both the old session
// still valid and the new one. A missing or expired oldID is an error
// and nothing is created. The new session carries a fresh CSRF token:
// after a rotate the old CSRF token is invalid forever, so a stolen
// form token cannot survive a login rotation.
func (s *SessionStore) Rotate(oldID, username string) (Session, error) {
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.byID[oldID]
	if !ok {
		return Session{}, ErrSessionNotFound
	}
	if !old.ExpiresAt.After(now) {
		delete(s.byID, oldID)
		return Session{}, ErrSessionNotFound
	}
	id, err := newSessionID()
	if err != nil {
		return Session{}, err
	}
	token, err := newCSRFToken()
	if err != nil {
		return Session{}, err
	}
	delete(s.byID, oldID)
	sess := Session{
		ID:        id,
		Username:  username,
		CSRFToken: token,
		CreatedAt: now,
		ExpiresAt: now.Add(s.ttl),
	}
	s.byID[id] = sess
	return sess, nil
}

// pruneLocked drops expired sessions. Callers hold mu.
func (s *SessionStore) pruneLocked(now time.Time) {
	for id, sess := range s.byID {
		if !sess.ExpiresAt.After(now) {
			delete(s.byID, id)
		}
	}
}

// String renders the session for logs. It deliberately omits the CSRF
// token: the token must never appear in logs (M7.6 security contract),
// and it never echoes password/hash/key data (the type cannot carry
// it).
func (s Session) String() string {
	return fmt.Sprintf("session{id:%s user:%s created:%s expires:%s}",
		s.ID, s.Username,
		s.CreatedAt.UTC().Format(time.RFC3339),
		s.ExpiresAt.UTC().Format(time.RFC3339))
}

// CSRFValid compares a submitted form token with the session's CSRF
// token in constant time. Length differences are not secret: the
// comparison never early-returns on a prefix match.
func CSRFValid(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// IDValid compares two session ids in constant time (defense in depth;
// ids are random 32-byte values, timing is not a practical channel).
func IDValid(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
