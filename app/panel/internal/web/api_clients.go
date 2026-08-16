package web

import (
	"errors"
	"net/http"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/awgconf"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/keys"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/status"
)

type clientJSON struct {
	ID               int64      `json:"id"`
	Name             string     `json:"name"`
	Description      string     `json:"description"`
	Address          string     `json:"address"`
	Enabled          bool       `json:"enabled"`
	Online           bool       `json:"online"`
	LastHandshakeUTC *time.Time `json:"last_handshake_utc"`
	RxBytes          uint64     `json:"rx_bytes"`
	TxBytes          uint64     `json:"tx_bytes"`
}

type clientCreateReq struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type clientPatchReq struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Enabled     *bool   `json:"enabled"`
}

func clientToJSON(c db.ClientRecord, st *status.Status, now time.Time) clientJSON {
	out := clientJSON{
		ID:          c.ID,
		Name:        c.Name,
		Description: c.Description,
		Address:     c.Address,
		Enabled:     c.Enabled,
	}
	if st == nil {
		return out
	}
	for _, p := range st.Peers {
		if p.PublicKey != c.PublicKey {
			continue
		}
		out.RxBytes = p.RxBytes
		out.TxBytes = p.TxBytes
		out.LastHandshakeUTC = p.LastHandshakeUTC
		if p.LastHandshakeUTC != nil && now.Sub(*p.LastHandshakeUTC) <= OnlineMaxAge {
			out.Online = true
		}
		break
	}
	return out
}

func (s *Server) loadStatus() *status.Status {
	st, err := status.ReadStatus(s.cfg.StatusPath)
	if err != nil {
		return nil
	}
	return st
}

func (s *Server) writeClientJSON(w http.ResponseWriter, code int, c db.ClientRecord) {
	writeJSON(w, code, clientToJSON(c, s.loadStatus(), time.Now()))
}

func (s *Server) apiClientsList(w http.ResponseWriter, r *http.Request) {
	clients, err := db.ClientsAll(s.db())
	if err != nil {
		internalFailure(w, r, s, "api clients list", err)
		return
	}
	st := s.loadStatus()
	now := time.Now()
	out := make([]clientJSON, 0, len(clients))
	for _, c := range clients {
		out = append(out, clientToJSON(c, st, now))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) apiClientsGet(w http.ResponseWriter, r *http.Request) {
	id, err := parseClientID(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "message": flashNotFound})
		return
	}
	c, err := db.ClientByID(s.db(), id)
	if errors.Is(err, db.ErrClientNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "message": flashNotFound})
		return
	}
	if err != nil {
		internalFailure(w, r, s, "api clients get", err)
		return
	}
	s.writeClientJSON(w, http.StatusOK, *c)
}

func (s *Server) apiClientsCreate(w http.ResponseWriter, r *http.Request) {
	var req clientCreateReq
	if !decodeJSON(w, r, &req) {
		return
	}
	name, err := validateName(req.Name)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "message": flashInvalidName})
		return
	}
	privateKey, publicKey, err := keys.GenerateKeyPair()
	if err != nil {
		internalFailure(w, r, s, "api clients create keys", err)
		return
	}
	presharedKey, err := keys.GeneratePresharedKey()
	if err != nil {
		internalFailure(w, r, s, "api clients create psk", err)
		return
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()
	server, err := db.ServerRow(s.db())
	if err != nil {
		if msg, ok := classifyExpected(err); ok {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "message": msg})
			return
		}
		internalFailure(w, r, s, "api clients create server", err)
		return
	}
	record, err := db.CreateClient(s.db(), server.Address, db.NewClient{
		Name:         name,
		PrivateKey:   privateKey,
		PublicKey:    publicKey,
		PresharedKey: presharedKey,
		Description:  req.Description,
	})
	if err != nil {
		if msg, ok := classifyExpected(err); ok {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "message": msg})
			return
		}
		internalFailure(w, r, s, "api clients create", err)
		return
	}
	if err := awgconf.Generate(s.db(), s.cfg.ConfPath); err != nil {
		internalFailure(w, r, s, "api clients create conf", err)
		return
	}
	s.writeClientJSON(w, http.StatusCreated, *record)
}

func (s *Server) apiClientsPatch(w http.ResponseWriter, r *http.Request) {
	id, err := parseClientID(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "message": flashNotFound})
		return
	}
	var req clientPatchReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name != nil {
		name, err := validateName(*req.Name)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "message": flashInvalidName})
			return
		}
		*req.Name = name
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()
	if _, err := db.ClientByID(s.db(), id); errors.Is(err, db.ErrClientNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "message": flashNotFound})
		return
	} else if err != nil {
		internalFailure(w, r, s, "api clients patch load", err)
		return
	}
	if req.Name != nil {
		if err := db.UpdateClientName(s.db(), id, *req.Name); err != nil {
			if msg, ok := classifyExpected(err); ok {
				writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "message": msg})
				return
			}
			internalFailure(w, r, s, "api clients patch name", err)
			return
		}
	}
	if req.Description != nil {
		if err := db.UpdateClientDescription(s.db(), id, *req.Description); err != nil {
			if msg, ok := classifyExpected(err); ok {
				writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "message": msg})
				return
			}
			internalFailure(w, r, s, "api clients patch description", err)
			return
		}
	}
	if req.Enabled != nil {
		if err := db.SetClientEnabled(s.db(), id, *req.Enabled); err != nil {
			if msg, ok := classifyExpected(err); ok {
				writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "message": msg})
				return
			}
			internalFailure(w, r, s, "api clients patch enabled", err)
			return
		}
	}
	if err := awgconf.Generate(s.db(), s.cfg.ConfPath); err != nil {
		internalFailure(w, r, s, "api clients patch conf", err)
		return
	}
	c, err := db.ClientByID(s.db(), id)
	if err != nil {
		internalFailure(w, r, s, "api clients patch reload", err)
		return
	}
	s.writeClientJSON(w, http.StatusOK, *c)
}

func (s *Server) apiClientsDelete(w http.ResponseWriter, r *http.Request) {
	id, err := parseClientID(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "message": flashNotFound})
		return
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if err := db.DeleteClient(s.db(), id); err != nil {
		if errors.Is(err, db.ErrClientNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "message": flashNotFound})
			return
		}
		internalFailure(w, r, s, "api clients delete", err)
		return
	}
	if err := awgconf.Generate(s.db(), s.cfg.ConfPath); err != nil {
		internalFailure(w, r, s, "api clients delete conf", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
