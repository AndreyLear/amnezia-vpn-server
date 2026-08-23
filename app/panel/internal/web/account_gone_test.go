package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/auth"
)

// Passwords are changed with `panel auth set-password` on the server; the
// panel deliberately offers no way to do it. The UI stopped showing the
// dialog earlier, but the routes stayed live — an authenticated endpoint
// accepting passwords that nothing uses is surface for nothing.
func TestPasswordChangeRoutesAreGone(t *testing.T) {
	f := newFixture(t)
	for _, path := range []string{"/account/password", "/api/account/password"} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(""))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set(auth.CSRFHeaderName, f.csrf)
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: f.sid})
		rec := httptest.NewRecorder()
		f.server.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("POST %s: code %d, want 404 — the panel must not change passwords", path, rec.Code)
		}
	}
}
