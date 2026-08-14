// M6.3 client mutations (M6_AUDIT.md §2.1.4, §2.2 URL table): add /
// enable / disable / delete / rename / set-expiry backed by the
// existing db functions, each followed by an atomic regeneration of
// config/awg0.conf (awgconf.Generate). Every mutation runs inside the
// server mutex (§4.10): the SQLite write is serialized by the database
// itself, but the regenerate must not interleave with a concurrent
// mutation.
//
// All flows are PRG: success and expected failures answer 303 with a
// fixed flash message in ?msg= — input is never echoed back. DB errors
// that are not expectable client states are logged server-side and
// answered with the generic 500 page (M6_AUDIT.md §2.1.10).
package web

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/awgconf"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/keys"
)

// Flash messages are fixed strings; they never contain request input,
// ids or secrets.
const (
	flashInvalidID     = "Invalid client id."
	flashNotFound      = "Client not found."
	flashInvalidName   = "Invalid client name."
	flashNameTaken     = "A client with this name already exists."
	flashInvalidExpiry = "Expected RFC3339 timestamp or \"none\"."
	flashNoServer      = "Server not initialized."
	flashNoAddress     = "No free address in the pool."
	flashAdded         = "Client added."
	flashDeleted       = "Client deleted."
	flashEnabled       = "Client enabled."
	flashDisabled      = "Client disabled."
	flashRenamed       = "Client renamed."
	flashExpirySet     = "Expiry updated."
)

// clientNameMaxRunes mirrors cli.clientNameMaxRunes: the web layer uses
// the same name rules as the CLI.
const clientNameMaxRunes = 64

// validateName mirrors cli.validateName (trimmed, non-empty, valid
// UTF-8, at most 64 runes); the stored form is returned.
func validateName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", errors.New("name must not be empty")
	}
	if !utf8.ValidString(name) {
		return "", errors.New("name must be valid UTF-8")
	}
	if n := utf8.RuneCountInString(name); n > clientNameMaxRunes {
		return "", errors.New("name too long")
	}
	return name, nil
}

// normalizeRFC3339 mirrors cli.normalizeRFC3339: canonical UTC form.
func normalizeRFC3339(raw string) (string, error) {
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return "", err
	}
	return t.UTC().Format(time.RFC3339), nil
}

// parseClientID mirrors cli.parseClientID: positive integer only.
func parseClientID(raw string) (int64, error) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 1 {
		return 0, errors.New("invalid client id")
	}
	return id, nil
}

// flash completes the PRG cycle: 303 to / with the fixed message.
func (s *Server) flash(w http.ResponseWriter, r *http.Request, msg string) {
	redirect303(w, r, "/?msg="+url.QueryEscape(msg))
}

// classifyExpected maps known client-state errors to their flash
// message. Everything else returns ok=false: the caller treats it as an
// internal failure (log + generic 500).
func classifyExpected(err error) (string, bool) {
	switch {
	case errors.Is(err, db.ErrClientNotFound):
		return flashNotFound, true
	case errors.Is(err, db.ErrClientNameExists):
		return flashNameTaken, true
	case errors.Is(err, db.ErrServerNotFound):
		return flashNoServer, true
	case errors.Is(err, db.ErrNoFreeAddress):
		return flashNoAddress, true
	default:
		return "", false
	}
}

// mutate runs fn under the mutation mutex, then regenerates
// config/awg0.conf. On fn success the flow answers 303 with okFlash;
// on an expectable db error it answers 303 with the classified flash;
// on any other error the real cause is logged and the generic 500 page
// is rendered. A failure of awgconf.Generate leaves the previous
// config intact (WriteAtomic) and answers 500.
func (s *Server) mutate(w http.ResponseWriter, r *http.Request, okFlash string, fn func() error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if err := fn(); err != nil {
		if msg, ok := classifyExpected(err); ok {
			s.flash(w, r, msg)
		} else {
			s.cfg.Logger.Printf("mutation: %v", err)
			s.errorPage(w, http.StatusInternalServerError)
		}
		return
	}
	if err := awgconf.Generate(s.db(), s.cfg.ConfPath); err != nil {
		s.cfg.Logger.Printf("mutation: regenerate config: %v", err)
		s.errorPage(w, http.StatusInternalServerError)
		return
	}
	s.flash(w, r, okFlash)
}

// requestBodyError maps body read failures: over the limit → 413,
// anything else → generic 400.
func requestBodyError(err error, w http.ResponseWriter) {
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		http.Error(w, "Request body too large.", http.StatusRequestEntityTooLarge)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	http.Error(w, "Bad request.", http.StatusBadRequest)
}

// withID validates the {id} path segment and runs the mutation.
func (s *Server) withID(w http.ResponseWriter, r *http.Request, okFlash string, fn func(id int64) error) {
	id, err := parseClientID(r.PathValue("id"))
	if err != nil {
		s.flash(w, r, flashInvalidID)
		return
	}
	s.mutate(w, r, okFlash, func() error { return fn(id) })
}

// clientNew handles POST /clients/new: validates name and optional
// expiry, generates the keypair, inserts via db.CreateClient (address
// allocation is transactional) and regenerates the config.
func (s *Server) clientNew(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		requestBodyError(err, w)
		return
	}
	name, err := validateName(r.FormValue("name"))
	if err != nil {
		s.flash(w, r, flashInvalidName)
		return
	}
	expiresAt := ""
	if raw := r.FormValue("expires_at"); raw != "" {
		expiresAt, err = normalizeRFC3339(raw)
		if err != nil {
			s.flash(w, r, flashInvalidExpiry)
			return
		}
	}
	privateKey, publicKey, err := keys.GenerateKeyPair()
	if err != nil {
		internalFailure(w, s, "generate keys", err)
		return
	}
	presharedKey, err := keys.GeneratePresharedKey()
	if err != nil {
		internalFailure(w, s, "generate preshared key", err)
		return
	}
	s.mutate(w, r, flashAdded, func() error {
		server, err := db.ServerRow(s.db())
		if err != nil {
			return err
		}
		record, err := db.CreateClient(s.db(), server.Address, db.NewClient{
			Name:         name,
			PrivateKey:   privateKey,
			PublicKey:    publicKey,
			PresharedKey: presharedKey,
		})
		if err != nil {
			return err
		}
		if expiresAt != "" {
			return db.SetClientExpiry(s.db(), record.ID, expiresAt)
		}
		return nil
	})
}

// internalFailure logs the real error and renders the generic 500 page.
func internalFailure(w http.ResponseWriter, s *Server, what string, err error) {
	s.cfg.Logger.Printf("%s: %v", what, err)
	s.errorPage(w, http.StatusInternalServerError)
}

// clientSetEnabled handles POST /clients/{id}/enable and /disable; it
// mirrors cli.cmdClientSetEnabled including idempotent repeats.
func (s *Server) clientSetEnabled(enabled bool) http.HandlerFunc {
	okFlash := flashDisabled
	if enabled {
		okFlash = flashEnabled
	}
	return func(w http.ResponseWriter, r *http.Request) {
		s.withID(w, r, okFlash, func(id int64) error {
			return db.SetClientEnabled(s.db(), id, enabled)
		})
	}
}

// clientDelete handles POST /clients/{id}/delete; an unknown id answers
// the not-found flash (never a silent no-op, M6_AUDIT.md §2.1.10).
func (s *Server) clientDelete(w http.ResponseWriter, r *http.Request) {
	s.withID(w, r, flashDeleted, func(id int64) error {
		return db.DeleteClient(s.db(), id)
	})
}

// clientRename handles POST /clients/{id}/rename with the same name
// rules as add.
func (s *Server) clientRename(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		requestBodyError(err, w)
		return
	}
	name, err := validateName(r.FormValue("name"))
	if err != nil {
		s.flash(w, r, flashInvalidName)
		return
	}
	s.withID(w, r, flashRenamed, func(id int64) error {
		return db.UpdateClientName(s.db(), id, name)
	})
}

// clientExpiry handles POST /clients/{id}/expiry: an RFC3339 value is
// stored in canonical UTC form, "none" clears the expiry (parity with
// cli "set-expiry <id> none").
func (s *Server) clientExpiry(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		requestBodyError(err, w)
		return
	}
	value := r.FormValue("expires_at")
	expiresAt := ""
	switch value {
	case "none":
	case "":
		s.flash(w, r, flashInvalidExpiry)
		return
	default:
		var err error
		expiresAt, err = normalizeRFC3339(value)
		if err != nil {
			s.flash(w, r, flashInvalidExpiry)
			return
		}
	}
	s.withID(w, r, flashExpirySet, func(id int64) error {
		return db.SetClientExpiry(s.db(), id, expiresAt)
	})
}
