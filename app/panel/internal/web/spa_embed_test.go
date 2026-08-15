package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestAssetsServedFromEmbed(t *testing.T) {
	html, err := os.ReadFile("dist/index.html")
	if err != nil {
		t.Fatalf("dist/index.html: %v", err)
	}
	if !strings.Contains(string(html), `id="root"`) {
		t.Fatalf("dist/index.html missing #root")
	}
	asset := regexp.MustCompile(`/assets/[^"']+`).FindString(string(html))
	if asset == "" {
		t.Fatal("no /assets/ reference in dist/index.html")
	}

	f := newFixture(t)
	req := httptest.NewRequest(http.MethodGet, asset, nil)
	rec := httptest.NewRecorder()
	f.serve(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s code=%d body=%s", asset, rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Content-Security-Policy") != "default-src 'self'; frame-ancestors 'none'" {
		t.Fatalf("CSP=%q", rec.Header().Get("Content-Security-Policy"))
	}

	idx := httptest.NewRequest(http.MethodGet, "/", nil)
	idxRec := httptest.NewRecorder()
	f.server.ServeHTTP(idxRec, idx)
	assertSPA(t, idxRec)
}
