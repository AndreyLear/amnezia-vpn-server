// M7.5 login/logout (TECHNICAL_SPEC_v2.0.md §6: cookie sessions).
// Login runs the M6.2 Argon2id pipeline (db.AuthUserByUsername +
// auth.VerifyPassword) and issues a session through the M7.3 store;
// logout deletes the session and clears the cookie.
//
// Failure contract: every failed login answers the same generic
// message ("Invalid username or password.") regardless of the reason —
// unknown user, wrong password, empty credentials, malformed stored
// hash — and never reveals the password, the hash, the session id or
// any internal error. A failed login never touches the session store.
//
// Session contract (M7.5 §3): a login with a valid session cookie
// rotates it (old SID dies, new SID is issued — fixation protection);
// a login without one creates a fresh session. In both cases the
// store is reduced to exactly one active session per user.
//
// CSRF: POST /login is the single CSRF-exempt route (M7.5 contract,
// pinned in M7.6): the login form carries no token, login itself
// cannot be CSRF-exploited to log the victim into an attacker's
// account beyond a credential re-entry, and SameSite=Lax drops the
// session cookie on cross-site POSTs. Every other POST — logout and
// all mutations — is CSRF-protected by auth.RequireCSRF (M7.6).
package web

import (
	"errors"
	"net/http"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/auth"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

// loginErrorText is the single failure message of the login form. It is
// a fixed string: no variant exists for unknown user, wrong password,
// empty credentials or a damaged hash, so the response can never reveal
// which of them happened. Russian (T-120 round 2).
const loginErrorText = "Неверное имя пользователя или пароль."

// dummyPasswordHash is a precomputed Argon2id hash that login verifies
// against when the submitted username does not exist in the auth table.
// An unknown user then costs the same Argon2 pass as a wrong password,
// so the response timing cannot reveal whether a username exists. It is
// generated once at startup (one extra hash per process, ~100 ms).
var dummyPasswordHash = func() string {
	h, err := auth.HashPassword("amnezia-panel-timing-equalizer")
	if err != nil {
		panic(err)
	}
	return h
}()

// loginData is the GET /login payload: only a fixed failure message is
// ever rendered. No session id, password, hash or key can reach the
// template through this type.
type loginData struct {
	Error        string
	Username     string
	NeedCode     bool
	Passwordless bool
}

// loginPage renders the login form (GET /login, public). An already
// authenticated visitor is redirected straight to the dashboard — the
// login form must never be shown to someone with a live session. The
// page is its own standalone template ("login", like "error"): it must
// not define the layout blocks or it would hijack the dashboard's
// title/body.
func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	if sid, ok := auth.ReadSessionID(r); ok {
		if _, ok := s.cfg.Sessions.Get(sid); ok {
			redirect303(w, r, "/")
			return
		}
	}
	s.renderLogin(w, loginData{})
}

// renderLogin executes the standalone login template.
func (s *Server) renderLogin(w http.ResponseWriter, data loginData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if err := s.tpl.ExecuteTemplate(w, "login", data); err != nil {
		s.cfg.Logger.Printf("render login: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// loginSubmit handles POST /login (public). On success it issues the
// session (Create or Rotate per the session contract above), sets the
// amnezia_session cookie with the new id and answers 303 /. The new SID
// never travels in a URL or query string.
func (s *Server) loginSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		requestBodyError(err, w)
		return
	}
	username := r.PostForm.Get("username")
	password := r.PostForm.Get("password")
	code := r.PostForm.Get("code")

	user, err := db.AuthUserByUsername(s.db(), username)
	switch {
	case err == nil:
		// Stored hash verified below.
	case errors.Is(err, db.ErrAuthUserNotFound):
		// Unknown user: verify against the dummy hash so the timing
		// does not differ from a wrong password.
		user = nil
	default:
		internalFailure(w, s, "login: read user", err)
		return
	}
	if user != nil && user.TOTPMode == "passwordless" {
		if !auth.VerifyTOTP(user.TOTPSecret, code, time.Now()) {
			s.renderLogin(w, loginData{Error: loginErrorText, Username: username, Passwordless: true})
			return
		}
	} else {
		hash := dummyPasswordHash
		if user != nil {
			hash = user.PasswordHash
		}
		if !auth.VerifyPassword(password, hash) {
			s.renderLogin(w, loginData{Error: loginErrorText, Username: username})
			return
		}
		if user != nil && user.TOTPMode == "2fa" && !auth.VerifyTOTP(user.TOTPSecret, code, time.Now()) {
			s.renderLogin(w, loginData{Error: "Неверный код.", Username: username, NeedCode: true})
			return
		}
	}

	// Issue the session. A presented live session is rotated (old SID
	// is invalidated atomically); a missing/unknown/expired one falls
	// back to Create. Failed rotations (concurrent expiry between the
	// shape check and the store) behave like a session-less login.
	var sess auth.Session
	if sid, ok := auth.ReadSessionID(r); ok {
		sess, err = s.cfg.Sessions.Rotate(sid, username)
		if errors.Is(err, auth.ErrSessionNotFound) {
			err = nil
			sess, err = s.cfg.Sessions.Create(username)
		}
	} else {
		sess, err = s.cfg.Sessions.Create(username)
	}
	if err != nil {
		internalFailure(w, s, "login: create session", err)
		return
	}
	// One active login per user: every other session of this user (for
	// example from another browser) is dropped.
	s.cfg.Sessions.DeleteByUsername(username, sess.ID)
	auth.WriteSessionCookie(w, sess.ID, sess.ExpiresAt)
	redirect303(w, r, "/")
}

// logout handles POST /logout (protected by RequireAuth, so a live
// session is guaranteed). It deletes the store session and clears the
// cookie with the same Name/Path it was set with, then redirects to
// /login. A repeat logout finds no session and answers the generic
// challenge — no session details are ever revealed.
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if sid, ok := auth.ReadSessionID(r); ok {
		s.cfg.Sessions.Delete(sid)
	}
	auth.ClearSessionCookie(w)
	redirect303(w, r, "/login")
}
