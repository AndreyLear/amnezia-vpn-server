// M7.4 HTTP session-bearer middleware (M7.3 store + cookie contract);
// M7.6 adds the CSRF middleware. This file owns every cookie/session/
// CSRF concern for the panel HTTP layer: no web handler touches
// cookies or CSRF tokens directly. The middlewares deliberately know
// nothing about SQLite or templates.
//
// Unified challenge semantics: GET/HEAD requests without a valid
// session are redirected (303) to /login — a browser user clicks a
// dashboard link and lands on the login form (M7.5). Any other method
// (the PRG mutations) gets a generic 401 text/plain response. Neither
// path ever echoes the session id or the identity.
//
// CSRF contract (M7.6): RequireCSRF runs inside RequireAuth (auth →
// csrf → handler). Safe methods (GET/HEAD/OPTIONS) pass through;
// every other method must carry the session's CSRF token in the
// _csrf form field. The token is never accepted from the query
// string, a cookie, the URL or a custom header. Failures answer a
// generic 403 with no details, do not touch the session store, and
// never PRG. POST /login is intentionally exempt (M7.5 contract);
// SameSite=Lax stays as the defense-in-depth layer.
package auth

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"os"
	"time"
)

// SessionCookieName is the cookie carrying the session id. The id is
// never passed through URL, query string or form fields (M7.4
// contract): only the cookie header is accepted.
const SessionCookieName = "amnezia_session"

// CSRFFieldName is the POST form field carrying the CSRF token (M7.6).
// The token is rendered by the web layer into every protected form as
// a hidden input; only the form body is accepted, never query/cookie/
// header.
const CSRFFieldName = "_csrf"

// Cookie attribute constants (M7.4 contract):
//   - HttpOnly: the SID must never be readable by page scripts;
//   - SameSite=Lax: cross-site POSTs (CSRF carriers) drop the cookie;
//   - Path=/: the whole panel shares the session;
//   - lifetime: MaxAge + Expires, aligned with the store's SessionTTL.
//
// The Secure attribute is decided by secureCookies() (T-124).
const (
	cookiePath      = "/"
	cookieSameSite  = http.SameSiteLaxMode
	cookieHTTPOnly  = true
	cookieMaxAgeOff = -1 // immediate deletion (ClearSessionCookie)
)

// secureCookies reports whether session cookies must carry the Secure
// attribute (T-124). The panel defaults to plain HTTP on loopback
// (127.0.0.1:8787 in compose), where a Secure cookie would never be
// sent back by the browser and login would break; behind a TLS reverse
// proxy (install.sh --domain or --panel-port modes) the deployment
// sets AMNEZIA_SECURE_COOKIES=1 in the deployment .env so the session
// id never travels in clear.
func secureCookies() bool {
	return os.Getenv("AMNEZIA_SECURE_COOKIES") == "1"
}

// sessionCtxKey is the private context key for the authenticated
// identity.
type sessionCtxKey struct{}

// Auth guards handlers with the M7.3 session store and defines the
// cookie contract of the panel. One instance per server; it is safe
// for concurrent use.
type Auth struct {
	store  *SessionStore
	dbPath string
}

// NewAuth returns an Auth backed by store. A nil store is a programmer
// error: the middleware would otherwise let every request through
// without any session.
func NewAuth(store *SessionStore) *Auth {
	if store == nil {
		panic("auth: nil session store")
	}
	return &Auth{store: store}
}

// WithDBPath points Auth at the SQLite path so RequireAuth can consume
// the CLI invalidate-sessions sidecar written next to the database.
func (a *Auth) WithDBPath(dbPath string) *Auth {
	a.dbPath = dbPath
	return a
}

// WriteSessionCookie sets the amnezia_session cookie for sid, expiring
// at expiresAt (the session's store expiry — never derived anywhere
// else). Login (M7.5) and tests are the callers.
func WriteSessionCookie(w http.ResponseWriter, sid string, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    sid,
		Path:     cookiePath,
		HttpOnly: cookieHTTPOnly,
		SameSite: cookieSameSite,
		Secure:   secureCookies(),
		Expires:  expiresAt,
		MaxAge:   int(time.Until(expiresAt).Seconds()),
	})
}

// ClearSessionCookie deletes the session cookie (logout, M7.5).
func ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     cookiePath,
		HttpOnly: cookieHTTPOnly,
		SameSite: cookieSameSite,
		Secure:   secureCookies(),
		Expires:  time.Unix(1, 0),
		MaxAge:   cookieMaxAgeOff,
	})
}

// ReadSessionID extracts the session id from the cookie header and
// validates its shape (32 random bytes base64url-encoded). A missing,
// empty or malformed cookie reports false, so handlers and tests treat
// every invalid form uniformly as "no session".
func ReadSessionID(r *http.Request) (string, bool) {
	c, err := r.Cookie(SessionCookieName)
	if err != nil {
		return "", false
	}
	if c.Value == "" || !validSessionIDShape(c.Value) {
		return "", false
	}
	return c.Value, true
}

// validSessionIDShape rejects cookie values that cannot be a store
// session id (43 base64url chars decoding to 32 bytes) without ever
// looking up the store: shape and existence stay separate checks.
func validSessionIDShape(sid string) bool {
	if len(sid) != 43 {
		return false
	}
	b, err := base64.RawURLEncoding.DecodeString(sid)
	return err == nil && len(b) == 32
}

// RequireAuth protects next: with a live session the authenticated
// Session travels via the request context (CurrentUser); otherwise the
// unified challenge of this package applies.
func (a *Auth) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ConsumeInvalidateSessions(a.dbPath, a.store)
		sid, ok := ReadSessionID(r)
		if !ok {
			a.challenge(w, r)
			return
		}
		sess, ok := a.store.Get(sid)
		if !ok {
			a.challenge(w, r)
			return
		}
		ctx := context.WithValue(r.Context(), sessionCtxKey{}, sess)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// challenge answers an unauthenticated request. The response never
// contains the cookie value, the session id or the identity: the 303
// target is the static /login path, the 401 body is a fixed string.
func (a *Auth) challenge(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	default:
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, "Unauthorized.\n")
	}
}

// RequireCSRF protects a handler against cross-site request forgery
// (M7.6). It must be mounted inside RequireAuth: the authenticated
// session travels via the request context.
//
// Rules:
//   - GET/HEAD/OPTIONS pass through untouched (safe methods);
//   - any other method must present the session's CSRF token in the
//     CSRFFieldName form field — the token is read only from the form
//     body, never from the query string, a cookie, the URL or a
//     custom header;
//   - comparison is constant-time (CSRFValid);
//   - a missing or wrong token answers a generic 403 text/plain
//     response with no details, never a redirect (no PRG on CSRF
//     failure), and never touches the session store;
//   - the body is parsed here so protected handlers receive an
//     already-parsed form; oversized bodies map to the same 413 as
//     the handlers' own requestBodyError.
func (a *Auth) RequireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if err := r.ParseForm(); err != nil {
			var mbe *http.MaxBytesError
			if ok := errors.As(err, &mbe); ok {
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				http.Error(w, "Request body too large.", http.StatusRequestEntityTooLarge)
				return
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			http.Error(w, "Bad request.", http.StatusBadRequest)
			return
		}
		sess, ok := CurrentUser(r.Context())
		if !ok {
			// Mounted outside RequireAuth: a programmer error, but the
			// response must stay generic and secret-free.
			a.challenge(w, r)
			return
		}
		if !CSRFValid(sess.CSRFToken, r.PostForm.Get(CSRFFieldName)) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusForbidden)
			io.WriteString(w, "Forbidden.\n")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// CurrentUser returns the authenticated Session stored by RequireAuth.
// ok is false outside protected handlers.
func CurrentUser(ctx context.Context) (Session, bool) {
	s, ok := ctx.Value(sessionCtxKey{}).(Session)
	return s, ok
}
