// Package web implements the panel HTTP layer: embedded Vite SPA
// (dist/index.html + /assets), JSON /api/*, authenticated config/QR
// downloads, and leftover HTML POST mutation routes used by tests.
//
// Security invariants (M6_AUDIT.md §5):
//   - errors are always generic — never echo request input, file
//     contents or error text to the client;
//   - the SPA shell is static HTML from go:embed dist; API JSON uses
//     encoding/json with HTML escaping;
//   - secrets never enter client JSON list payloads (no key fields).
package web

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/auth"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/hostmetrics"
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
	// HostProcDir is the host /proc mount (Docker: /host/proc). Empty
	// selects "/host/proc". Tests pass a temp directory.
	HostProcDir string
	// HostDiskPath is the filesystem path whose usage is reported. Empty
	// selects "/data". Tests pass t.TempDir().
	HostDiskPath string
}

// DefaultConfig returns the serve defaults.
func DefaultConfig() Config {
	return Config{
		Addr:            DefaultAddr,
		ShutdownTimeout: DefaultShutdownTimeout,
	}
}

//go:embed dist
var distFS embed.FS

// Server is one panel HTTP server. It is safe to call Handler after
// construction; Handler wraps the mux (plus middlewares) and allows
// tests to register extra routes before the first request.
type Server struct {
	cfg  Config
	mux  *http.ServeMux
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
	dbMu       sync.RWMutex
	dbh        *sql.DB
	loginLimit *loginLimiter
	hostMu     sync.Mutex
	hostCPU    hostmetrics.CPUSample
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
	if cfg.HostProcDir == "" {
		cfg.HostProcDir = "/host/proc"
	}
	if cfg.HostDiskPath == "" {
		cfg.HostDiskPath = "/data"
	}
	s := &Server{cfg: cfg, mux: http.NewServeMux(), auth: auth.NewAuth(cfg.Sessions).WithDBPath(cfg.DBPath), dbh: cfg.DB, loginLimit: newLoginLimiter()}
	// Document GETs serve the embedded SPA with no RequireAuth so React
	// can boot and send the user to /login after GET /api/me 401.
	// /api/* stays RequireAPI (401 JSON, never 303). HTML POSTs remain
	// for existing form tests. "GET /{$}" is the exact-match root.
	s.mux.HandleFunc("GET /{$}", s.spaIndex)
	s.mux.Handle("POST /clients/new", s.auth.RequireAuth(s.auth.RequireCSRF(http.HandlerFunc(s.clientNew))))
	s.mux.Handle("POST /clients/{id}/enable", s.auth.RequireAuth(s.auth.RequireCSRF(http.HandlerFunc(s.clientSetEnabled(true)))))
	s.mux.Handle("POST /clients/{id}/disable", s.auth.RequireAuth(s.auth.RequireCSRF(http.HandlerFunc(s.clientSetEnabled(false)))))
	s.mux.Handle("POST /clients/{id}/delete", s.auth.RequireAuth(s.auth.RequireCSRF(http.HandlerFunc(s.clientDelete))))
	s.mux.Handle("POST /clients/{id}/rename", s.auth.RequireAuth(s.auth.RequireCSRF(http.HandlerFunc(s.clientRename))))
	s.mux.Handle("POST /logout", s.auth.RequireAuth(s.auth.RequireCSRF(http.HandlerFunc(s.logout))))
	s.mux.Handle("POST /account/password", s.auth.RequireAuth(s.auth.RequireCSRF(http.HandlerFunc(s.changePassword))))
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
	s.mux.HandleFunc("GET /login", s.spaIndex)
	s.mux.HandleFunc("POST /login", s.loginSubmit)
	s.mux.HandleFunc("POST /api/login", s.apiLogin)
	s.mux.Handle("GET /api/me", s.auth.RequireAPI(http.HandlerFunc(s.apiMe)))
	s.mux.Handle("POST /api/logout", s.auth.RequireAPI(s.auth.RequireCSRF(http.HandlerFunc(s.apiLogout))))
	s.mux.Handle("POST /api/account/password", s.auth.RequireAPI(s.auth.RequireCSRF(http.HandlerFunc(s.apiChangePassword))))
	s.mux.Handle("POST /api/backups/download", s.auth.RequireAPI(s.auth.RequireCSRF(http.HandlerFunc(s.apiBackupDownload))))
	s.mux.Handle("POST /api/backups/restore", s.auth.RequireAPI(http.HandlerFunc(s.apiBackupRestore)))
	s.mux.Handle("GET /api/stats/host", s.auth.RequireAPI(http.HandlerFunc(s.apiStatsHost)))
	s.mux.Handle("GET /api/clients", s.auth.RequireAPI(http.HandlerFunc(s.apiClientsList)))
	s.mux.Handle("POST /api/clients", s.auth.RequireAPI(s.auth.RequireCSRF(http.HandlerFunc(s.apiClientsCreate))))
	s.mux.Handle("GET /api/clients/{id}", s.auth.RequireAPI(http.HandlerFunc(s.apiClientsGet)))
	s.mux.Handle("PATCH /api/clients/{id}", s.auth.RequireAPI(s.auth.RequireCSRF(http.HandlerFunc(s.apiClientsPatch))))
	s.mux.Handle("DELETE /api/clients/{id}", s.auth.RequireAPI(s.auth.RequireCSRF(http.HandlerFunc(s.apiClientsDelete))))
	distRoot, err := fs.Sub(distFS, "dist")
	if err != nil {
		return nil, fmt.Errorf("web: dist embed: %w", err)
	}
	// Vite emits dist/assets/<hash>.*; document GETs use spaIndex, not this tree.
	s.mux.Handle("GET /assets/", http.FileServer(http.FS(distRoot)))
	s.mux.HandleFunc("/", s.notFound)
	return s, nil
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
				if strings.HasPrefix(r.URL.Path, "/api/") {
					writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "message": "Внутренняя ошибка сервера."})
					return
				}
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
			if r.Body != nil && !restoreBodyExempt(r.URL.Path) {
				r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
			}
		}
		next.ServeHTTP(w, r)
	})
}

// ---- handlers and rendering ----

// clientCardData is one dashboard card plus the CSRF token (kept for
// the leftover HTML fetch-channel fragment used by mutation JSON tests).
type clientCardData struct {
	Card    ClientCard
	CSRF    string
	Ordinal int
}

// spaIndex serves dist/index.html for document GETs. It is public: the
// React app boots without a session and redirects after GET /api/me 401.
func (s *Server) spaIndex(w http.ResponseWriter, r *http.Request) {
	data, err := distFS.ReadFile("dist/index.html")
	if err != nil {
		s.cfg.Logger.Printf("spa index: %v", err)
		s.errorPage(w, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data)
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
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	fmt.Fprintf(w, "<!doctype html><html lang=\"ru\"><body><p>%s</p></body></html>\n", html.EscapeString(message))
}

// notFound: API paths stay JSON 404; other GETs serve the SPA; other
// methods keep a generic HTML 404. Never serve index.html for /api/*.
func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "message": "Страница не найдена."})
		return
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		s.spaIndex(w, r)
		return
	}
	s.errorPage(w, http.StatusNotFound)
}

// ---- PRG infrastructure (M6.3 uses it for every mutation) ----

// redirect303 completes a POST/PRG cycle: respond 303 See Other with
// the Location header and an empty body.
func redirect303(w http.ResponseWriter, r *http.Request, path string) {
	http.Redirect(w, r, path, http.StatusSeeOther)
}
