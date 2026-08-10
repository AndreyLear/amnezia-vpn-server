// M6.1 web foundation tests (M6_AUDIT.md §9): rendering, routing,
// generic errors, HTML escaping, secret absence, body limit, security
// headers, PRG redirect, panic recovery.
package web

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const samplePrivateKey = "4OSe6B1rDXdY4RdVJ+eenaEJOqRVZ9kxx25z9bI0t28="
const samplePresharedKey = "gOYtz2OZILLXQm5hQMqF/e8fP02sqoy6FKLsqI0nwWo="

func newTestServer(t *testing.T) *Server {
	t.Helper()
	s, err := New(Config{Addr: "127.0.0.1:0", Logger: log.New(io.Discard, "", 0)})
	if err != nil {
		t.Fatalf("web.New: %v", err)
	}
	return s
}

func doRequest(t *testing.T, s *Server, method, target string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, body)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

func TestNewValidatesAddr(t *testing.T) {
	for _, addr := range []string{"", "nohost", "1.2.3.4", ":bad-port"} {
		if _, err := New(Config{Addr: addr}); err == nil {
			t.Errorf("New(%q): want error, got nil", addr)
		}
	}
	if _, err := New(Config{Addr: "127.0.0.1:0"}); err != nil {
		t.Errorf("New valid addr: unexpected error: %v", err)
	}
}

func TestIndex200(t *testing.T) {
	s := newTestServer(t)
	rec := doRequest(t, s, http.MethodGet, "/", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /: code = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{"AmneziaVPN Panel", "<!DOCTYPE html>"} {
		if !strings.Contains(body, want) {
			t.Errorf("GET / body missing %q", want)
		}
	}
}

func TestUnknownRoute404(t *testing.T) {
	s := newTestServer(t)
	for _, target := range []string{"/nope", "/clients/1/enable", "/status.json"} {
		rec := doRequest(t, s, http.MethodGet, target, nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s: code = %d, want 404", target, rec.Code)
		}
		if strings.Contains(rec.Body.String(), target) {
			t.Errorf("GET %s: 404 body echoes the path (reflection of hostile input)", target)
		}
	}
}

func TestErrorPageGeneric(t *testing.T) {
	s := newTestServer(t)
	cases := []struct {
		code int
		want string
	}{
		{http.StatusNotFound, "Page not found."},
		{http.StatusRequestEntityTooLarge, "Request body too large."},
		{http.StatusMethodNotAllowed, "Method not allowed."},
		{http.StatusBadRequest, "Bad request."},
		{999, "Internal server error."},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		s.errorPage(rec, c.code)
		body := rec.Body.String()
		if !strings.Contains(body, c.want) {
			t.Errorf("errorPage(%d): body missing %q", c.code, c.want)
		}
		if strings.Contains(body, samplePrivateKey) || strings.Contains(body, samplePresharedKey) {
			t.Errorf("errorPage(%d): key material leaked", c.code)
		}
	}
}

func TestErrorPageDoesNotReflectInput(t *testing.T) {
	s := newTestServer(t)
	rec := doRequest(t, s, http.MethodGet, "/foo%3Cscript%3Ebar", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "<script>") || strings.Contains(body, "foo") {
		t.Errorf("404 page reflected hostile input: %q", body)
	}
}

func TestPanicRecoveryGeneric500(t *testing.T) {
	s := newTestServer(t)
	s.Handle("GET", "/panic", func(w http.ResponseWriter, r *http.Request) {
		panic("boom: " + samplePrivateKey)
	})
	rec := doRequest(t, s, http.MethodGet, "/panic", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("panic route: code = %d, want 500", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Internal server error.") {
		t.Errorf("panic response not generic: %q", body)
	}
	if strings.Contains(body, "boom") || strings.Contains(body, samplePrivateKey) {
		t.Errorf("panic details leaked to the client: %q", body)
	}
}

func TestHTMLEscaping(t *testing.T) {
	s := newTestServer(t)
	rec := doRequest(t, s, http.MethodGet, "/?msg=%3Cscript%3Ealert(1)%3C/script%3E", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Fatalf("flash message was not escaped: %q", body)
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Errorf("escaped form missing from body: %q", body)
	}
}

func TestNoSecretsInHTTPResponse(t *testing.T) {
	s := newTestServer(t)
	for _, target := range []string{"/", "/nope", "/?msg=" + samplePrivateKey} {
		rec := doRequest(t, s, http.MethodGet, target, nil)
		body := rec.Body.String()
		for _, secret := range []string{samplePrivateKey, samplePresharedKey} {
			if strings.Contains(body, secret) {
				t.Errorf("GET %s: key material %q leaked into response", target, secret)
			}
		}
		for _, field := range []string{"private_key", "preshared_key"} {
			if strings.Contains(body, field) {
				t.Errorf("GET %s: secret field name %q present in response", target, field)
			}
		}
	}
}

func TestSecurityHeaders(t *testing.T) {
	s := newTestServer(t)
	rec := doRequest(t, s, http.MethodGet, "/", nil)
	expected := map[string]string{
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "DENY",
		"Referrer-Policy":         "no-referrer",
		"Content-Security-Policy": "default-src 'self'; frame-ancestors 'none'",
		"Cache-Control":           "no-store",
	}
	for k, want := range expected {
		if got := rec.Header().Get(k); got != want {
			t.Errorf("header %s = %q, want %q", k, got, want)
		}
	}
}

func TestBodyLimitRejectsOversizedPOST(t *testing.T) {
	s := newTestServer(t)
	big := strings.Repeat("x", MaxBodyBytes+1)
	rec := doRequest(t, s, http.MethodPost, "/clients/new", strings.NewReader(big))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized POST: code = %d, want 413", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Request body too large.") {
		t.Errorf("413 body not generic: %q", rec.Body.String())
	}
}

func TestBodyLimitSmallPOSTRoutesNormally(t *testing.T) {
	s := newTestServer(t)
	rec := doRequest(t, s, http.MethodPost, "/clients/new", strings.NewReader("name=x"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("small POST to unknown route: code = %d, want 404 (limit must not trigger)", rec.Code)
	}
}

func TestRedirect303(t *testing.T) {
	s := newTestServer(t)
	s.Handle("POST", "/prg", func(w http.ResponseWriter, r *http.Request) {
		redirect303(w, r, "/?msg=ok")
	})
	rec := doRequest(t, s, http.MethodPost, "/prg", nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("PRG redirect: code = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/?msg=ok" {
		t.Errorf("Location = %q, want /?msg=ok", loc)
	}
}
