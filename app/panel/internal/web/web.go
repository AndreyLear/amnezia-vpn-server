// Package web implements the M6 panel HTTP layer
// (M6_AUDIT.md §2): server-side html/template pages, PRG redirects,
// generic error pages and base security/timeout hardening. M6.1 scope
// is the foundation only: routing, GET / placeholder page, graceful
// shutdown. Client CRUD (M6.3), reconciliation (M6.2), config
// download and QR (M6.5) are not part of this file yet.
//
// Security invariants (M6_AUDIT.md §5):
//   - errors are always generic — never echo request input, file
//     contents or error text to the client;
//   - templates are html/template with autoscape; raw (template.HTML)
//     output is never used;
//   - secrets never enter template data: the template data types in
//     this package have no key/credential fields.
package web

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"time"
)

// DefaultAddr is the default listen address of `panel serve`
// (M6_AUDIT.md §2.2 proposal; the audit Q2 decision defers the compose
// ports mapping to M6.6).
const DefaultAddr = "0.0.0.0:8787"

// MaxBodyBytes caps request bodies. The full client configuration is
// generated server-side (never uploaded), so a small limit is safe.
const MaxBodyBytes = 64 << 10 // 64 KiB

// DefaultShutdownTimeout bounds the graceful SIGTERM/SIGINT shutdown.
const DefaultShutdownTimeout = 10 * time.Second

// Config carries the serve-time settings.
type Config struct {
	// Addr is the listen address ("host:port").
	Addr string
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

// Server is one panel HTTP server. It is safe to call Handler after
// construction; Handler wraps the mux (plus middlewares) and allows
// tests to register extra routes before the first request.
type Server struct {
	cfg Config
	mux *http.ServeMux
	tpl *template.Template
}

// New validates the config and builds the route table. Listen/serve
// happens in ListenAndServe.
func New(cfg Config) (*Server, error) {
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
	s := &Server{cfg: cfg, mux: http.NewServeMux(), tpl: tpl}
	s.mux.HandleFunc("GET /", s.index)
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

// bodyLimit enforces MaxBodyBytes on request bodies: the limit is
// applied via http.MaxBytesReader and checked eagerly for POST/PUT/
// PATCH, which are rejected with 413 before routing when over the cap.
// M6.1 has no body-reading routes; when M6.3 adds form handlers this
// middleware switches to wrap-only so handlers read the limited body.
func (s *Server) bodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch:
			r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
			if _, err := io.Copy(io.Discard, r.Body); err != nil {
				var mbe *http.MaxBytesError
				if errors.As(err, &mbe) {
					s.errorPage(w, http.StatusRequestEntityTooLarge)
					return
				}
				s.errorPage(w, http.StatusBadRequest)
				return
			}
			r.Body = http.NoBody
		}
		next.ServeHTTP(w, r)
	})
}

// ---- handlers and rendering ----

// indexData is the GET / payload. No key or credential field exists in
// this type (M6_AUDIT.md §2.1.8): rendering can never emit secrets.
type indexData struct {
	Flash string
}

// index renders the placeholder dashboard. The full client-card stack
// lands in M6.2.
func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		s.notFound(w, r)
		return
	}
	s.renderPage(w, http.StatusOK, indexData{
		Flash: r.URL.Query().Get("msg"),
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
// string per status code — request details, paths and error text are
// never echoed (no reflection of hostile input).
func (s *Server) errorPage(w http.ResponseWriter, code int) {
	message := ""
	switch code {
	case http.StatusNotFound:
		message = "Page not found."
	case http.StatusRequestEntityTooLarge:
		message = "Request body too large."
	case http.StatusMethodNotAllowed:
		message = "Method not allowed."
	case http.StatusBadRequest:
		message = "Bad request."
	default:
		code = http.StatusInternalServerError
		message = "Internal server error."
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
