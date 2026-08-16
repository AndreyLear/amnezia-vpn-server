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
// CSRF: POST /login and POST /api/login are CSRF-exempt (M7.5 contract,
// pinned in M7.6): the login form carries no token, login itself
// cannot be CSRF-exploited to log the victim into an attacker's
// account beyond a credential re-entry, and SameSite=Lax drops the
// session cookie on cross-site POSTs. Every other POST — logout and
// all mutations — is CSRF-protected by auth.RequireCSRF (M7.6).
package web

import (
	"errors"
	"fmt"
	"html"
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

// loginData is the login form payload. Error is a flash string.
// Username is echoed back into the field. Login is password-only.
type loginData struct {
	Error    string
	Username string
}

// renderLogin answers HTML POST /login failures with the generic error
// text (existing form tests). GET /login is the SPA shell.
func (s *Server) renderLogin(w http.ResponseWriter, data loginData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "<!doctype html><html lang=\"ru\"><body>")
	if data.Error != "" {
		fmt.Fprintf(w, "<p>%s</p>", html.EscapeString(data.Error))
	}
	fmt.Fprintf(w, `<input name="password" type="password">`)
	if data.Username != "" {
		fmt.Fprintf(w, `<input name="username" value="%s">`, html.EscapeString(data.Username))
	}
	fmt.Fprintf(w, "</body></html>\n")
}

// loginSubmit handles POST /login (public). On success it issues the
// session (Create or Rotate per the session contract above), sets the
// amnezia_session cookie with the new id and answers 303 /. The new SID
// never travels in a URL or query string.
func (s *Server) loginSubmit(w http.ResponseWriter, r *http.Request) {
	if s.rejectLimitedLogin(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		requestBodyError(err, w, r)
		return
	}
	username := r.PostForm.Get("username")
	password := r.PostForm.Get("password")

	outcome, err := s.evaluateLogin(r, username, password)
	if err != nil {
		internalFailure(w, r, s, "login: read user", err)
		return
	}
	if outcome.message != "" {
		s.renderLogin(w, loginData{Error: outcome.message, Username: username})
		return
	}
	if err := s.issueLoginSession(w, r, username); err != nil {
		internalFailure(w, r, s, "login: create session", err)
		return
	}
	redirect303(w, r, "/")
}

type loginOutcome struct {
	message string
}

// evaluateLogin runs the dummy-hash password check. totp_secret and
// totp_mode are ignored: a correct password always issues a session.
func (s *Server) evaluateLogin(r *http.Request, username, password string) (loginOutcome, error) {
	user, err := db.AuthUserByUsername(s.db(), username)
	switch {
	case err == nil:
	case errors.Is(err, db.ErrAuthUserNotFound):
		user = nil
	default:
		return loginOutcome{}, err
	}
	hash := dummyPasswordHash
	if user != nil {
		hash = user.PasswordHash
	}
	if !auth.VerifyPassword(password, hash) {
		s.loginLimit.fail(clientIP(r), time.Now())
		return loginOutcome{message: loginErrorText}, nil
	}
	return loginOutcome{}, nil
}

func (s *Server) issueLoginSession(w http.ResponseWriter, r *http.Request, username string) error {
	var sess auth.Session
	var err error
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
		return err
	}
	s.cfg.Sessions.DeleteByUsername(username, sess.ID)
	auth.WriteSessionCookie(w, sess.ID, sess.ExpiresAt)
	s.loginLimit.clear(clientIP(r))
	return nil
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
