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

func TestFaviconSVGServedFromEmbed(t *testing.T) {
	assertFaviconSVGBody := func(t *testing.T, body, src string) {
		t.Helper()
		if strings.Contains(body, `id="root"`) {
			t.Fatalf("%s looks like HTML SPA shell", src)
		}
		if strings.Contains(body, "863bff") {
			t.Fatalf("%s still has Vite purple #863bff", src)
		}
		for _, want := range []string{"#171717", "#fafafa", `fill-rule="evenodd"`} {
			if !strings.Contains(body, want) {
				t.Fatalf("%s missing %q", src, want)
			}
		}
	}

	disk, err := os.ReadFile("dist/favicon.svg")
	if err != nil {
		t.Fatalf("dist/favicon.svg: %v", err)
	}
	assertFaviconSVGBody(t, string(disk), "dist/favicon.svg")

	f := newFixture(t)
	req := httptest.NewRequest(http.MethodGet, "/favicon.svg", nil)
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /favicon.svg code=%d body=%s", rec.Code, rec.Body.String())
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "image/svg+xml") {
		t.Fatalf("Content-Type=%q, want image/svg+xml", ct)
	}
	assertFaviconSVGBody(t, rec.Body.String(), "GET /favicon.svg")
}
