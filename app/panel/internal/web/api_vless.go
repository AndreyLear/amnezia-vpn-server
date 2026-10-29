package web

import (
	"errors"
	"net/http"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/xray"
	"github.com/skip2/go-qrcode"
)

// Ссылки VLESS в окне клиента (amnezia-vpn-server-14y0.5).
//
// Ссылка — такой же секрет, как конфиг AWG: с ней подключается кто угодно.
// Поэтому обе ручки — только для вошедшего, ответы помечены no-store (в
// кеше браузера ссылка не остаётся), а ссылки не попадают ни в журнал, ни
// в тексты ошибок. QR рисует сервер, как и для AWG.

type vlessRouteJSON struct {
	// Entry — имя узла-входа; пусто — напрямую к этому серверу.
	Entry string `json:"entry"`
	// Prefix — приставка узла («RU »): по ней окно подписывает маршрут.
	Prefix string `json:"prefix"`
	// Title — имя каскада от владельца (amnezia-vpn-server-q1do.4): окно
	// подписывает им маршрут; пусто — «Каскад»/«Каскад <узел>».
	Title string `json:"title"`
	Link  string `json:"link"`
}

type vlessJSON struct {
	Enabled bool             `json:"enabled"`
	Routes  []vlessRouteJSON `json:"routes"`
}

// vlessRoutes — маршруты клиента; false — ответ уже записан.
func (s *Server) vlessRoutes(w http.ResponseWriter, r *http.Request) (*db.ClientRecord, []xray.Route, bool) {
	id, err := parseClientID(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "message": flashNotFound})
		return nil, nil, false
	}
	c, err := db.ClientByID(s.db(), id)
	if err == nil {
		var routes []xray.Route
		if routes, err = xray.ClientRoutes(s.db(), id); err == nil {
			return c, routes, true
		}
	}
	if errors.Is(err, db.ErrClientNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "message": flashNotFound})
		return nil, nil, false
	}
	internalFailure(w, r, s, "api client vless", err)
	return nil, nil, false
}

// apiClientVLESS — GET /api/clients/{id}/vless: ссылки по всем маршрутам.
// Отключённому или истёкшему клиенту — пустой список.
func (s *Server) apiClientVLESS(w http.ResponseWriter, r *http.Request) {
	c, routes, ok := s.vlessRoutes(w, r)
	if !ok {
		return
	}
	prefixes, titles := map[string]string{}, map[string]string{}
	if entries, err := db.XrayEntries(s.db()); err == nil {
		for _, e := range entries {
			prefixes[e.Name] = e.LinkPrefix
			titles[e.Name] = e.Title
		}
	}
	out := vlessJSON{Enabled: c.Enabled, Routes: []vlessRouteJSON{}}
	for _, rt := range routes {
		out.Routes = append(out.Routes, vlessRouteJSON{Entry: rt.Entry, Prefix: prefixes[rt.Entry], Title: titles[rt.Entry], Link: rt.Link})
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

// apiClientVLESSQR — GET /api/clients/{id}/vless/qr?entry=<узел>: PNG со
// ссылкой маршрута (без entry — прямого). Маршрута нет — 404.
func (s *Server) apiClientVLESSQR(w http.ResponseWriter, r *http.Request) {
	_, routes, ok := s.vlessRoutes(w, r)
	if !ok {
		return
	}
	entry := r.URL.Query().Get("entry")
	for _, rt := range routes {
		if rt.Entry != entry {
			continue
		}
		png, err := qrcode.Encode(rt.Link, qrcode.Medium, -qrModulePixels)
		if err != nil {
			internalFailure(w, r, s, "api client vless qr", errors.New("encode failed"))
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(png)
		return
	}
	writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "message": flashNotFound})
}
