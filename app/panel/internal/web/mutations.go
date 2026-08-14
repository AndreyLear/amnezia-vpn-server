// M6.3 client mutations (M6_AUDIT.md §2.1.4, §2.2 URL table): add /
// enable / disable / delete / rename / set-expiry backed by the
// existing db functions, each followed by an atomic regeneration of
// config/awg0.conf (awgconf.Generate). Every mutation runs inside the
// server mutex (§4.10): the SQLite write is serialized by the database
// itself, but the regenerate must not interleave with a concurrent
// mutation.
//
// Response contract (T-120 round 2): a plain form POST answers 303
// with a fixed flash message in ?msg= (input is never echoed back); a
// fetch request (X-Requested-With or Accept: application/json) answers
// JSON — {"ok":bool,"message":...,"html"?:fresh-card-fragment,
// "count"?:clients} — so the external /static/app.js updates the DOM
// locally (toasts, badges, cards) without a page reload. DB errors
// that are not expectable client states are logged server-side and
// answered with the generic 500 page (M6_AUDIT.md §2.1.10).
package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/auth"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/awgconf"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/keys"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/status"
)

// Flash messages are fixed strings; they never contain request input,
// ids or secrets. The whole panel is Russian (T-120 round 2).
const (
	flashInvalidID     = "Некорректный ID клиента."
	flashNotFound      = "Клиент не найден."
	flashInvalidName   = "Недопустимое имя клиента."
	flashNameTaken     = "Клиент с таким именем уже существует."
	flashInvalidExpiry = "Недопустимый срок. Формат: ГГГГ-ММ-ДДTЧЧ:ММ."
	flashNoServer      = "Сервер не инициализирован."
	flashNoAddress     = "Нет свободных адресов в пуле."
	flashAdded         = "Клиент добавлен."
	flashDeleted       = "Клиент удалён."
	flashEnabled       = "Клиент включён."
	flashDisabled      = "Клиент отключён."
	flashRenamed       = "Клиент переименован."
	flashExpirySet     = "Срок действия обновлён."
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

// normalizeExpiry mirrors cli.normalizeRFC3339: canonical UTC form.
// The UI submits either a full RFC3339 timestamp (cli parity) or the
// browser's datetime-local shape "2006-01-02T15:04" (wall time,
// interpreted as UTC server-side, T-120 round 2 §8).
func normalizeExpiry(raw string) (string, error) {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04"} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC().Format(time.RFC3339), nil
		}
	}
	return "", errors.New("invalid expiry")
}

// parseClientID mirrors cli.parseClientID: positive integer only.
func parseClientID(raw string) (int64, error) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 1 {
		return 0, errors.New("invalid client id")
	}
	return id, nil
}

// wantsJSON reports whether the request prefers the fetch/JSON channel
// (T-120 round 2): the headers app.js sends on fetch() mutations. A
// plain form POST (no-JS fallback) never matches and keeps the 303 PRG
// flow.
func wantsJSON(r *http.Request) bool {
	if r.Header.Get("X-Requested-With") != "" {
		return true
	}
	for _, part := range strings.Split(r.Header.Get("Accept"), ",") {
		if strings.TrimSpace(strings.SplitN(part, ";", 2)[0]) == "application/json" {
			return true
		}
	}
	return false
}

// mutationResponse is the JSON answer of the fetch channel.
type mutationResponse struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	HTML    string `json:"html,omitempty"`
	Count   *int   `json:"count,omitempty"`
}

// mutationPayload is the optional per-mutation extra data attached to a
// success answer: the fresh card fragment (add/enable/disable/rename/
// expiry) and/or the new client count (add/delete).
type mutationPayload struct {
	HTML  string
	Count *int
}

// answerMutation completes a mutation for both channels: JSON for
// fetch requests, 303 + ?msg= for plain form POSTs.
func (s *Server) answerMutation(w http.ResponseWriter, r *http.Request, ok bool, msg string, extra mutationPayload) {
	if wantsJSON(r) {
		s.writeMutationJSON(w, ok, msg, extra)
		return
	}
	redirect303(w, r, "/?msg="+url.QueryEscape(msg))
}

// writeMutationJSON renders the fetch-channel answer. The HTML fragment
// (when present) was produced by html/template with autoscape — it is
// safe to insert into the DOM; encoding/json escapes it for transport.
func (s *Server) writeMutationJSON(w http.ResponseWriter, ok bool, msg string, extra mutationPayload) {
	resp := mutationResponse{OK: ok, Message: msg, HTML: extra.HTML, Count: extra.Count}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		s.cfg.Logger.Printf("write mutation json: %v", err)
	}
}

// flash completes the PRG cycle: 303 to / with the fixed message (or
// the JSON error for fetch requests).
func (s *Server) flash(w http.ResponseWriter, r *http.Request, msg string) {
	s.answerMutation(w, r, false, msg, mutationPayload{})
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
// config/awg0.conf. On fn success the flow answers 303 with okFlash
// (plain POST) or JSON with okFlash + payload (fetch); on an expectable
// db error it answers the classified flash; on any other error the real
// cause is logged and the generic 500 page is rendered. A failure of
// awgconf.Generate leaves the previous config intact (WriteAtomic) and
// answers 500. payload (when non-nil) is only built for the fetch
// channel, after the regeneration succeeded.
func (s *Server) mutate(w http.ResponseWriter, r *http.Request, okFlash string, fn func() error) {
	s.mutateWith(w, r, okFlash, nil, fn)
}

// mutateWith is mutate with an optional payload builder (see mutate).
func (s *Server) mutateWith(w http.ResponseWriter, r *http.Request, okFlash string, payload func() (mutationPayload, error), fn func() error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if err := fn(); err != nil {
		if msg, ok := classifyExpected(err); ok {
			s.answerMutation(w, r, false, msg, mutationPayload{})
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
	var extra mutationPayload
	if payload != nil && wantsJSON(r) {
		var err error
		extra, err = payload()
		if err != nil {
			s.cfg.Logger.Printf("mutation: payload: %v", err)
			s.errorPage(w, http.StatusInternalServerError)
			return
		}
	}
	s.answerMutation(w, r, true, okFlash, extra)
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

// withID validates the {id} path segment and runs the mutation; payload
// (when non-nil) builds the fetch-channel extras from the id.
func (s *Server) withID(w http.ResponseWriter, r *http.Request, okFlash string, payload func(id int64) (mutationPayload, error), fn func(id int64) error) {
	id, err := parseClientID(r.PathValue("id"))
	if err != nil {
		s.flash(w, r, flashInvalidID)
		return
	}
	s.mutateWith(w, r, okFlash, func() (mutationPayload, error) {
		if payload == nil {
			return mutationPayload{}, nil
		}
		return payload(id)
	}, func() error { return fn(id) })
}

// csrfFromContext returns the session CSRF token for rendering card
// fragments (the session is guaranteed by RequireAuth).
func csrfFromContext(r *http.Request) string {
	sess, ok := auth.CurrentUser(r.Context())
	if !ok {
		return ""
	}
	return sess.CSRFToken
}

// cardFragment renders one client card (the same "clientcard" template
// the dashboard uses) for the fetch-channel HTML updates.
func (s *Server) cardFragment(csrf string, id int64) (string, error) {
	rec, err := db.ClientByID(s.db(), id)
	if err != nil {
		return "", err
	}
	st, readErr := status.ReadStatus(s.cfg.StatusPath)
	cards := Reconcile([]db.ClientRecord{*rec}, st, readErr, time.Now()).Cards
	if len(cards) != 1 {
		return "", errors.New("reconcile produced no card")
	}
	var buf bytes.Buffer
	if err := s.tpl.ExecuteTemplate(&buf, "clientcard", clientCardData{Card: cards[0], CSRF: csrf}); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// clientCount returns the number of client rows (fetch-channel count).
func (s *Server) clientCount() (int, error) {
	clients, err := db.ClientsAll(s.db())
	if err != nil {
		return 0, err
	}
	return len(clients), nil
}

// cardPayload builds the fetch-channel payload for the mutation of one
// client: the fresh card fragment.
func (s *Server) cardPayload(r *http.Request, id int64) (mutationPayload, error) {
	html, err := s.cardFragment(csrfFromContext(r), id)
	if err != nil {
		return mutationPayload{}, err
	}
	return mutationPayload{HTML: html}, nil
}

// countPayload builds the fetch-channel payload carrying the new client
// count (add/delete).
func (s *Server) countPayload() (mutationPayload, error) {
	n, err := s.clientCount()
	if err != nil {
		return mutationPayload{}, err
	}
	return mutationPayload{Count: &n}, nil
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
		expiresAt, err = normalizeExpiry(raw)
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
	var createdID int64
	payload := func() (mutationPayload, error) {
		html, err := s.cardFragment(csrfFromContext(r), createdID)
		if err != nil {
			return mutationPayload{}, err
		}
		n, err := s.clientCount()
		if err != nil {
			return mutationPayload{}, err
		}
		return mutationPayload{HTML: html, Count: &n}, nil
	}
	s.mutateWith(w, r, flashAdded, payload, func() error {
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
		createdID = record.ID
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
		s.withID(w, r, okFlash, func(id int64) (mutationPayload, error) {
			return s.cardPayload(r, id)
		}, func(id int64) error {
			return db.SetClientEnabled(s.db(), id, enabled)
		})
	}
}

// clientDelete handles POST /clients/{id}/delete; an unknown id answers
// the not-found flash (never a silent no-op, M6_AUDIT.md §2.1.10).
func (s *Server) clientDelete(w http.ResponseWriter, r *http.Request) {
	s.withID(w, r, flashDeleted, func(id int64) (mutationPayload, error) {
		return s.countPayload()
	}, func(id int64) error {
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
	s.withID(w, r, flashRenamed, func(id int64) (mutationPayload, error) {
		return s.cardPayload(r, id)
	}, func(id int64) error {
		return db.UpdateClientName(s.db(), id, name)
	})
}

// clientExpiry handles POST /clients/{id}/expiry: an RFC3339 or
// datetime-local value (YYYY-MM-DDTHH:MM) is stored in canonical UTC
// form; "none" or an empty value clears the expiry (T-120 round 2 §8:
// empty input = no deadline).
func (s *Server) clientExpiry(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		requestBodyError(err, w)
		return
	}
	value := r.FormValue("expires_at")
	expiresAt := ""
	switch {
	case value == "none" || value == "":
	default:
		var err error
		expiresAt, err = normalizeExpiry(value)
		if err != nil {
			s.flash(w, r, flashInvalidExpiry)
			return
		}
	}
	s.withID(w, r, flashExpirySet, func(id int64) (mutationPayload, error) {
		return s.cardPayload(r, id)
	}, func(id int64) error {
		return db.SetClientExpiry(s.db(), id, expiresAt)
	})
}
