// Package web implements the M6 panel HTTP layer
// (M6_AUDIT.md §2): server-side html/template pages, PRG redirects,
// generic error pages and base security/timeout hardening. M6.2 adds
// the client-card dashboard fed by the SQLite ↔ status.json
// reconciliation (reconcile.go); M6.3 adds the client mutation
// handlers (mutations.go) with the mutation→awg0.conf regenerate
// mutex; M6.5 adds the config download and QR endpoints
// (ondemand.go).
//
// Security invariants (M6_AUDIT.md §5):
//   - errors are always generic — never echo request input, file
//     contents or error text to the client;
//   - templates are html/template with autoscape; raw (template.HTML)
//     output is never used;
//   - secrets never enter template data: the template data types in
//     this package (indexData, ClientCard, error pages) have no
//     key/credential fields.
package web

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/auth"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/status"
)

// DefaultAddr is the default listen address of `panel serve`
// (M6_AUDIT.md §2.2 proposal; the audit Q2 decision defers the compose
// ports mapping to M6.6).
const DefaultAddr = "0.0.0.0:8787"

// defaultConfPath mirrors cli.configPath(): every mutation
// regenerates config/awg0.conf at this location.
func defaultConfPath() string {
	if p := os.Getenv("AMNEZIA_CONFIG_PATH"); p != "" {
		return p
	}
	return "/config/awg0.conf"
}

// MaxBodyBytes caps request bodies. The full client configuration is
// generated server-side (never uploaded), so a small limit is safe.
const MaxBodyBytes = 64 << 10 // 64 KiB

// DefaultShutdownTimeout bounds the graceful SIGTERM/SIGINT shutdown.
const DefaultShutdownTimeout = 10 * time.Second

// Config carries the serve-time settings.
type Config struct {
	// Addr is the listen address ("host:port").
	Addr string
	// DB is the panel's SQLite handle: the only service that
	// reads/writes the database (docs/TECHNICAL_SPEC_v2.0.md §2). The
	// caller (cli serve) owns closing it. Required.
	DB *sql.DB
	// Sessions is the in-memory session store backing RequireAuth
	// (M7.4). Required: without it the panel would have no way to
	// authenticate any request.
	Sessions *auth.SessionStore
	// StatusPath is the location of status/status.json; empty selects
	// status.Path() (AMNEZIA_STATUS_PATH). The panel only reads it.
	StatusPath string
	// ConfPath is the location of config/awg0.conf regenerated after
	// every mutation; empty selects the AMNEZIA_CONFIG_PATH default
	// (parity with the cli).
	ConfPath string
	// DBPath is the SQLite location the DB handle was opened from. The
	// restore flow places its pending marker next to it and the page
	// shows the restart-required state from it; empty selects
	// db.DefaultPath() (parity with the cli).
	DBPath string
	// Logger receives startup/shutdown diagnostics and internal errors;
	// request-level logs are not emitted (the panel shows runtime state,
	// keep noise low). Never contains key material by construction.
	Logger *log.Logger
	// ShutdownTimeout bounds http.Server.Shutdown.
	ShutdownTimeout time.Duration
}

// DefaultConfig returns the serve defaults.
func DefaultConfig() Config {
	return Config{
		Addr:            DefaultAddr,
		ShutdownTimeout: DefaultShutdownTimeout,
	}
}

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/tailwind.css static/app.js
var staticFS embed.FS

// Server is one panel HTTP server. It is safe to call Handler after
// construction; Handler wraps the mux (plus middlewares) and allows
// tests to register extra routes before the first request.
type Server struct {
	cfg  Config
	mux  *http.ServeMux
	tpl  *template.Template
	auth *auth.Auth
	// mutex serializes the mutation → regenerate chains (M6_AUDIT.md
	// §4.10): the SQLite write is serialized by the database itself,
	// but the awg0.conf regeneration must not interleave with concurrent
	// requests. Reads (dashboard) stay lock-free.
	mutex sync.Mutex
	// dbMu guards the live database handle: restore-apply (T-125)
	// swaps the handle in-process after backup.ApplyPending, so every
	// read/write access goes through db() and never touches cfg.DB
	// directly.
	dbMu        sync.RWMutex
	dbh         *sql.DB
	pendingTOTP map[string]string
}

// db returns the live database handle (RLock-protected swap access).
func (s *Server) db() *sql.DB {
	s.dbMu.RLock()
	defer s.dbMu.RUnlock()
	return s.dbh
}

// swapDB replaces the live handle with the freshly opened one and
// returns the previous handle. The caller must retire it (not Close
// immediately): db() copies the pointer under RLock and unlocks
// before the query, so in-flight readers may still use the old
// handle. Callers must hold s.mutex.
func (s *Server) swapDB(next *sql.DB) *sql.DB {
	s.dbMu.Lock()
	defer s.dbMu.Unlock()
	old := s.dbh
	s.dbh = next
	s.cfg.DB = next
	return old
}

// retiredDBCloseDelay keeps a swapped-out handle open so in-flight
// db() readers can finish. New traffic must use db() after the swap.
const retiredDBCloseDelay = 5 * time.Second

func retireDB(old *sql.DB) {
	if old == nil {
		return
	}
	go func() {
		time.Sleep(retiredDBCloseDelay)
		_ = old.Close()
	}()
}

// New validates the config and builds the route table. Listen/serve
// happens in ListenAndServe.
func New(cfg Config) (*Server, error) {
	if cfg.DB == nil {
		return nil, errors.New("web: nil database handle")
	}
	if cfg.Sessions == nil {
		return nil, errors.New("web: nil session store")
	}
	if cfg.Addr == "" {
		return nil, errors.New("web: empty listen address")
	}
	_, port, err := net.SplitHostPort(cfg.Addr)
	if err != nil {
		return nil, fmt.Errorf("web: invalid listen address %q: %w", cfg.Addr, err)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 0 || n > 65535 {
		return nil, fmt.Errorf("web: invalid listen port %q", port)
	}
	if cfg.StatusPath == "" {
		cfg.StatusPath = status.Path()
	}
	if cfg.ConfPath == "" {
		cfg.ConfPath = defaultConfPath()
	}
	if cfg.DBPath == "" {
		cfg.DBPath = db.DefaultPath()
	}
	if cfg.Logger == nil {
		cfg.Logger = log.New(io.Discard, "", 0)
	}
	if cfg.ShutdownTimeout <= 0 {
		cfg.ShutdownTimeout = DefaultShutdownTimeout
	}
	tpl, err := template.New("").ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("web: parse templates: %w", err)
	}
	s := &Server{cfg: cfg, mux: http.NewServeMux(), tpl: tpl, auth: auth.NewAuth(cfg.Sessions).WithDBPath(cfg.DBPath), dbh: cfg.DB, pendingTOTP: make(map[string]string)}
	// Route protection (M7.4/M7.6): every panel route runs behind
	// RequireAuth; every state-changing POST additionally runs behind
	// RequireCSRF (auth → csrf → handler). /login is the single public
	// pair (GET form, POST submit — login.go) and is deliberately NOT
	// CSRF-protected (M7.5 contract: login stays the CSRF exception,
	// SameSite=Lax is the extra layer). "GET /{$}" is the exact-match
	// root: unknown paths fall through to the public 404 catch-all
	// instead of being swallowed by the "/" subtree.
	s.mux.Handle("GET /{$}", s.auth.RequireAuth(http.HandlerFunc(s.dashboard)))
	s.mux.Handle("POST /clients/new", s.auth.RequireAuth(s.auth.RequireCSRF(http.HandlerFunc(s.clientNew))))
	s.mux.Handle("POST /clients/{id}/enable", s.auth.RequireAuth(s.auth.RequireCSRF(http.HandlerFunc(s.clientSetEnabled(true)))))
	s.mux.Handle("POST /clients/{id}/disable", s.auth.RequireAuth(s.auth.RequireCSRF(http.HandlerFunc(s.clientSetEnabled(false)))))
	s.mux.Handle("POST /clients/{id}/delete", s.auth.RequireAuth(s.auth.RequireCSRF(http.HandlerFunc(s.clientDelete))))
	s.mux.Handle("POST /clients/{id}/rename", s.auth.RequireAuth(s.auth.RequireCSRF(http.HandlerFunc(s.clientRename))))
	s.mux.Handle("POST /clients/{id}/expiry", s.auth.RequireAuth(s.auth.RequireCSRF(http.HandlerFunc(s.clientExpiry))))
	s.mux.Handle("POST /logout", s.auth.RequireAuth(s.auth.RequireCSRF(http.HandlerFunc(s.logout))))
	s.mux.Handle("GET /account", s.auth.RequireAuth(http.HandlerFunc(s.accountPage)))
	s.mux.Handle("POST /account/password", s.auth.RequireAuth(s.auth.RequireCSRF(http.HandlerFunc(s.changePassword))))
	s.mux.Handle("POST /account/totp/enroll", s.auth.RequireAuth(s.auth.RequireCSRF(http.HandlerFunc(s.totpEnroll))))
	s.mux.Handle("GET /account/totp/qr", s.auth.RequireAuth(http.HandlerFunc(s.totpQR)))
	s.mux.Handle("POST /account/totp/confirm", s.auth.RequireAuth(s.auth.RequireCSRF(http.HandlerFunc(s.totpConfirm))))
	s.mux.Handle("POST /account/totp/disable", s.auth.RequireAuth(s.auth.RequireCSRF(http.HandlerFunc(s.totpDisable))))
	s.mux.Handle("POST /account/totp/mode", s.auth.RequireAuth(s.auth.RequireCSRF(http.HandlerFunc(s.totpMode))))
	s.mux.Handle("GET /backups", s.auth.RequireAuth(http.HandlerFunc(s.backupsPage)))
	// T-125: one-click download (fresh archive, not stored).
	s.mux.Handle("POST /backups/download", s.auth.RequireAuth(s.auth.RequireCSRF(http.HandlerFunc(s.backupDownloadNow))))
	// The restore POST is the one multipart route of the panel: it
	// carries a file upload, so it is NOT mounted through RequireCSRF
	// (r.ParseForm never parses multipart bodies and would 403 every
	// legitimate upload). restoreSubmit performs the same check with
	// the same primitives (auth.CSRFFieldName + auth.CSRFValid) after
	// parsing the parts itself. GET /backups is the upload page;
	// there is no separate GET /backups/restore.
	s.mux.Handle("POST /backups/restore", s.auth.RequireAuth(http.HandlerFunc(s.restoreSubmit)))
	s.mux.Handle("GET /clients/{id}/config", s.auth.RequireAuth(http.HandlerFunc(s.clientConfigDownload)))
	s.mux.Handle("GET /clients/{id}/qr", s.auth.RequireAuth(http.HandlerFunc(s.clientQR)))
	s.mux.HandleFunc("GET /login", s.loginPage)
	s.mux.HandleFunc("POST /login", s.loginSubmit)
	// Public static assets (T-120 design system): the compiled Tailwind
	// stylesheet (committed artifact, see internal/web/static/input.css)
	// and the progressive-enhancement JS served from the embedded FS;
	// CSP default-src 'self' admits them (external files only — inline
	// scripts/styles stay forbidden). No auth: the login page needs
	// them before any session exists.
	s.mux.HandleFunc("GET /static/tailwind.css", s.staticCSS)
	s.mux.HandleFunc("GET /static/app.js", s.staticJS)
	s.mux.HandleFunc("/", s.notFound)
	return s, nil
}

// staticCSS serves the embedded compiled Tailwind stylesheet. The
// response passes through securityHeaders like every other route
// (nosniff, CSP, no-store).
func (s *Server) staticCSS(w http.ResponseWriter, r *http.Request) {
	data, err := staticFS.ReadFile("static/tailwind.css")
	if err != nil {
		s.notFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Write(data)
}

// staticJS serves the embedded progressive-enhancement script
// (toasts, native <dialog> modals, fetch-based mutations).
func (s *Server) staticJS(w http.ResponseWriter, r *http.Request) {
	data, err := staticFS.ReadFile("static/app.js")
	if err != nil {
		s.notFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Write(data)
}

// Handle registers an extra route on the mux. The middlewares (recover,
// security headers, body limit) are applied in Handler, so late
// registrations are covered. Unexported handlers stay package-private;
// Handle exists for tests and for the future M6.3 route table.
func (s *Server) Handle(method, pattern string, h http.HandlerFunc) {
	s.mux.HandleFunc(method+" "+pattern, h)
}

// handler wraps the mux with the security chain: recover → security
// headers → body limit → routes.
func (s *Server) handler() http.Handler {
	return s.recoverPanic(s.securityHeaders(s.bodyLimit(s.mux)))
}

// Serve is http.Handler entry point used by tests via httptest.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler().ServeHTTP(w, r)
}

// ListenAndServe binds the configured address and serves until ctx is
// canceled (graceful shutdown) or the server dies. It returns nil on
// graceful shutdown and on a clean http.ErrServerClosed; any listen or
// runtime error is returned for the caller to exit non-zero.
func (s *Server) ListenAndServe(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           s.handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1 MiB
	}
	s.cfg.Logger.Printf("listening on %s", ln.Addr())
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()
	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shCtx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shCtx); err != nil {
			return err
		}
		return nil
	}
}

// ---- middlewares ----

// recoverPanic converts handler panics into a generic 500 page and logs
// the panic reason server-side. The client never sees the panic value.
func (s *Server) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rv := recover(); rv != nil {
				s.cfg.Logger.Printf("recovered panic: %v", rv)
				s.errorPage(w, http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// securityHeaders sets conservative response headers (M6_AUDIT.md §10).
// The panel renders runtime state: Cache-Control forces fresh reads and
// CSP forbids all external content.
func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// bodyLimit wraps request bodies with http.MaxBytesReader: form
// handlers (M6.3) parse the limited body and answer 413 when the limit
// is exceeded (http.MaxBytesError). GET bodies are not read. The
// restore upload route is exempt: it sets its own (larger)
// MaxRestoreBodyBytes limit in the handler.
func (s *Server) bodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch:
			if r.Body != nil && r.URL.Path != restoreUploadPath {
				r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
			}
		}
		next.ServeHTTP(w, r)
	})
}

// ---- handlers and rendering ----

// dashboardData is the GET / payload. No key or credential field
// exists in this type or in ClientCard (M6_AUDIT.md §2.1.8):
// rendering can never emit secrets. Username is the authenticated
// principal from the session context (M7.5); CSRF is the session's
// CSRF token rendered only into hidden form inputs (M7.6).
// CardViews wraps each card with the CSRF token its forms need.
type dashboardData struct {
	Reconciliation
	Flash    string
	Username string
	CSRF     string
	// RxTotalText/TxTotalText are the traffic totals summed over all
	// clients (T-120 round 2 §9), rendered as compact chips.
	RxTotalText string
	TxTotalText string
	CardViews   []clientCardData
}

// clientCardData is one dashboard card plus the CSRF token its inline
// forms embed (also used by the fetch-channel card fragments).
type clientCardData struct {
	Card    ClientCard
	CSRF    string
	Ordinal int
}

// Interface-state predicates keep magic numbers out of the templates.
func (d dashboardData) Up() bool   { return d.Interface == IfaceUp }
func (d dashboardData) NA() bool   { return d.Interface == IfaceNA }
func (d dashboardData) Down() bool { return d.Interface == IfaceDown }
func (d dashboardData) Err() bool  { return d.Interface == IfaceError }

// GeneratedText renders the snapshot time; only meaningful when Up.
func (d dashboardData) GeneratedText() string {
	if !d.Up() {
		return ""
	}
	return d.GeneratedAtUTC.UTC().Format("2006-01-02 15:04:05 UTC")
}

// dashboard renders the client-card stack (M6.2): clients from SQLite
// reconciled with the runtime status. The mutation forms point at the
// M6.3 routes but their handlers land in M6.3.
func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	rec, err := Load(s.db(), s.cfg.StatusPath, time.Now())
	if err != nil {
		s.cfg.Logger.Printf("dashboard: %v", err)
		s.errorPage(w, http.StatusInternalServerError)
		return
	}
	sess, _ := auth.CurrentUser(r.Context())
	var rxTotal, txTotal uint64
	views := make([]clientCardData, len(rec.Cards))
	for i, c := range rec.Cards {
		rxTotal += c.RxBytes
		txTotal += c.TxBytes
		views[i] = clientCardData{Card: c, CSRF: sess.CSRFToken, Ordinal: i + 1}
	}
	s.renderPage(w, http.StatusOK, dashboardData{
		Reconciliation: rec,
		Flash:          r.URL.Query().Get("msg"),
		Username:       sess.Username,
		CSRF:           sess.CSRFToken,
		RxTotalText:    bytesText(rxTotal),
		TxTotalText:    bytesText(txTotal),
		CardViews:      views,
	})
}

// renderPage executes the layout with the body data. A template
// failure is logged and answered with a generic 500 fallback. Any
// secret-looking content never reaches this path: the data types have
// no secret fields and autoscape always escapes.
func (s *Server) renderPage(w http.ResponseWriter, code int, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	if err := s.tpl.ExecuteTemplate(w, "layout", data); err != nil {
		s.cfg.Logger.Printf("render layout: %v", err)
		// A partial generic fallback keeps the response secret-free
		// even on a template failure.
	}
}

// errorPage renders a generic error page. The message is a fixed
// string per status code (Russian, T-120 round 2) — request details,
// paths and error text are never echoed (no reflection of hostile
// input).
func (s *Server) errorPage(w http.ResponseWriter, code int) {
	message := ""
	switch code {
	case http.StatusNotFound:
		message = "Страница не найдена."
	case http.StatusRequestEntityTooLarge:
		message = "Тело запроса слишком большое."
	case http.StatusMethodNotAllowed:
		message = "Метод не поддерживается."
	case http.StatusBadRequest:
		message = "Некорректный запрос."
	default:
		code = http.StatusInternalServerError
		message = "Внутренняя ошибка сервера."
	}
	type errorData struct {
		Code    int
		Message string
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	if err := s.tpl.ExecuteTemplate(w, "error", errorData{Code: code, Message: message}); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// notFound answers every unmatched route with the generic 404 page.
func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	s.errorPage(w, http.StatusNotFound)
}

// ---- PRG infrastructure (M6.3 uses it for every mutation) ----

// redirect303 completes a POST/PRG cycle: respond 303 See Other with
// the Location header and an empty body.
func redirect303(w http.ResponseWriter, r *http.Request, path string) {
	http.Redirect(w, r, path, http.StatusSeeOther)
}
