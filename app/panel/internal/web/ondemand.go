// M6.5 on-demand client payloads (M6_AUDIT.md §2.1.5–2.1.6): config
// download and QR, both mirroring the CLI's `client config` output —
// the exact bytes of awgconf.GenerateClient — without touching the
// filesystem.
//
// Security: private/preshared keys exist ONLY inside these responses
// (the intended payload per the M6.5 requirement). They never reach
// HTML, flash messages, redirects, error pages or logs; the download
// filename is sanitized to plain [A-Za-z0-9._-] so no path traversal
// or header injection is possible; unknown ids answer explicit generic
// errors (never a silent 200).
package web

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/awgconf"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
	"github.com/skip2/go-qrcode"
)

// clientIDError maps a bad/unknown id to a generic page: the id itself
// is never echoed (M6 generic-error invariant).
func (s *Server) clientIDError(w http.ResponseWriter, err error) {
	if errors.Is(err, db.ErrClientNotFound) {
		s.errorPage(w, http.StatusNotFound)
		return
	}
	s.cfg.Logger.Printf("client payload: %v", err)
	s.errorPage(w, http.StatusInternalServerError)
}

// clientConfigDownload handles GET /clients/{id}/config: the response
// body is byte-for-byte the same configuration the CLI prints
// (awgconf.GenerateClient). Disabled and expired clients still receive
// their on-demand config (M4.3 parity).
func (s *Server) clientConfigDownload(w http.ResponseWriter, r *http.Request) {
	id, err := parseClientID(r.PathValue("id"))
	if err != nil {
		s.errorPage(w, http.StatusNotFound)
		return
	}
	rec, err := db.ClientByID(s.db(), id)
	if err != nil {
		s.clientIDError(w, err)
		return
	}
	cfg, err := awgconf.GenerateClient(s.db(), id)
	if err != nil {
		s.cfg.Logger.Printf("client config: generate: %v", err)
		s.errorPage(w, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+safeConfigFilename(rec.Name, id)+`"`)
	w.Write(cfg)
}

// qrModulePixels is the side of one QR module in the served PNG. Eight
// pixels keep the symbol readable after the browser scales it down into
// the dialog, and on a 1x display still leave a module wider than the
// blur of a hand-held camera.
const qrModulePixels = 8

// clientQR handles GET /clients/{id}/qr: the QR encodes exactly the
// same client config bytes as the config download. Nothing is written
// to disk; the PNG is produced in memory.
func (s *Server) clientQR(w http.ResponseWriter, r *http.Request) {
	id, err := parseClientID(r.PathValue("id"))
	if err != nil {
		s.errorPage(w, http.StatusNotFound)
		return
	}
	cfg, err := awgconf.GenerateClient(s.db(), id)
	if err != nil {
		if errors.Is(err, db.ErrClientNotFound) {
			s.errorPage(w, http.StatusNotFound)
			return
		}
		s.cfg.Logger.Printf("client qr: generate: %v", err)
		s.errorPage(w, http.StatusInternalServerError)
		return
	}
	// Medium error correction balances density and robustness.
	//
	// The size is negative on purpose: go-qrcode then reads it as pixels
	// per module instead of a canvas width (qrcode.Image). A fixed canvas
	// is mapped with module = int(pixel*realSize/size), which only comes
	// out even when the module count divides it — over the 89 modules of a
	// real client config, 256 px left 78 columns 3 px wide next to 11
	// columns 2 px wide. Cameras sample module centres, and a grid whose
	// pitch drifts every ninth module gives them nothing to lock onto
	// (T-ky6l). qrModulePixels also puts the symbol well above the
	// resolution a phone needs to photograph it off a screen; the PNG stays
	// a few kilobytes because a QR is two colours.
	png, err := qrcode.Encode(string(cfg), qrcode.Medium, -qrModulePixels)
	if err != nil {
		s.cfg.Logger.Printf("client qr: encode: %v", err)
		s.errorPage(w, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Write(png)
}

// safeConfigFilename derives the "<safe-name>.conf" attachment name:
// only [A-Za-z0-9._-] survive, everything else (including '/', '\',
// '..', CR/LF) becomes '-'; the result is trimmed, capped, and falls
// back to "client-<id>" when nothing remains. Header injection is
// impossible because the result contains no quotes or control
// characters.
func safeConfigFilename(name string, id int64) string {
	const maxRunes = 40
	runes := make([]rune, 0, len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			runes = append(runes, r)
		default:
			runes = append(runes, '-')
		}
	}
	s := strings.Trim(string(runes), "-.")
	if s == "" {
		s = fmt.Sprintf("client-%d", id)
	}
	r := []rune(s)
	if len(r) > maxRunes {
		s = string(r[:maxRunes])
	}
	return s + ".conf"
}
