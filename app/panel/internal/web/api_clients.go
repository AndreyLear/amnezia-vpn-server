package web

import (
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/awgconf"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/keys"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/status"
)

type clientJSON struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Address     string `json:"address"`
	// Address6 — адрес клиента внутри префикса IPv6 туннеля; пусто, когда
	// туннель несёт только IPv4. Выводится из префикса сервера, а не
	// хранится: конфиг клиента уже несёт оба адреса, и панель должна
	// показывать то, что роздано (amnezia-vpn-server-lhlv).
	Address6         string     `json:"address6"`
	Enabled          bool       `json:"enabled"`
	Online           bool       `json:"online"`
	LastHandshakeUTC *time.Time `json:"last_handshake_utc"`
	// Straight from the wg dump, in the server's perspective: RxBytes is
	// what the server received from this peer (the client's upload) and
	// TxBytes what it sent (the client's download). The names are kept
	// because they mirror the dump; a client-facing view must swap them
	// (amnezia-vpn-server-9l30).
	RxBytes uint64 `json:"rx_bytes"`
	TxBytes uint64 `json:"tx_bytes"`
	// DNSBypass says this client sends traffic through the tunnel and asks
	// somebody else for names (amnezia-vpn-server-g0vd). False also means
	// "nothing to say": no snapshot, client offline, or nothing
	// transferred yet.
	DNSBypass bool `json:"dns_bypass"`
	// MTU is this client's own tunnel MTU; 0 means it follows the server's
	// (amnezia-vpn-server-h2pg). The panel shows both, because "1340
	// because nobody chose" and "1340 because somebody did" are different
	// answers to the same question.
	MTU int64 `json:"mtu"`
}

type clientCreateReq struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type clientPatchReq struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Enabled     *bool   `json:"enabled"`
	// MTU: 0 returns the client to the server value; out of range is
	// refused rather than clamped.
	MTU *int64 `json:"mtu"`
}

// dnsBypassMinRx: сколько клиент должен был прислать, прежде чем молчание
// его резолвера что-то значит. Только что подключившийся ещё ничего не
// спрашивал — это не обход, это первая секунда.
const dnsBypassMinRx = 1 << 20 // 1 МиБ

// clientToJSON собирает представление клиента для панели. dns и addr6 нужны
// только для отметки об обходе резолвера: dns == nil означает «сказать
// нечего», а не «все обходят».
func clientToJSON(c db.ClientRecord, st *status.Status, dns *status.DNSSeen, addr6 string, now time.Time) clientJSON {
	out := clientJSON{
		ID:          c.ID,
		Name:        c.Name,
		Description: c.Description,
		Address:     c.Address,
		Address6:    addr6,
		Enabled:     c.Enabled,
		MTU:         c.MTU,
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
	// Отметка ставится только когда есть о чём говорить: снимок снят, клиент
	// на связи и через туннель уже что-то прошло. Молчание резолвера у
	// клиента, который ничего не передавал, ничего не значит.
	if dns != nil && out.Online && out.RxBytes >= dnsBypassMinRx {
		out.DNSBypass = !dns.Seen(hostAddress(c.Address), hostAddress(addr6))
	}
	return out
}

// hostAddress отбрасывает длину префикса: в базе адреса лежат как 10.8.0.4/32,
// а ядро в множестве держит просто адрес.
func hostAddress(cidr string) string {
	if i := strings.IndexByte(cidr, '/'); i >= 0 {
		return cidr[:i]
	}
	return cidr
}

func (s *Server) loadStatus() *status.Status {
	st, err := status.ReadStatus(s.cfg.StatusPath)
	if err != nil {
		return nil
	}
	return st
}

// loadDNSSeen возвращает снимок или nil, если сказать нечего.
func (s *Server) loadDNSSeen() *status.DNSSeen {
	dir := filepath.Dir(s.cfg.StatusPath)
	seen, err := status.ReadDNSSeen(filepath.Join(dir, "dns-seen.json"))
	if err != nil {
		return nil
	}
	return seen
}

// clientAddress6 выводит адрес IPv6 клиента из строки сервера; пусто, когда
// туннель несёт только IPv4.
func (s *Server) clientAddress6(c db.ClientRecord) string {
	server, err := db.ServerRow(s.db())
	if err != nil || server == nil {
		return ""
	}
	addr6, err := db.ClientAddress6(server.Address, server.Address6, c.Address)
	if err != nil {
		return ""
	}
	return addr6
}

func (s *Server) writeClientJSON(w http.ResponseWriter, code int, c db.ClientRecord) {
	writeJSON(w, code, clientToJSON(c, s.loadStatus(), s.loadDNSSeen(), s.clientAddress6(c), time.Now()))
}

func (s *Server) apiClientsList(w http.ResponseWriter, r *http.Request) {
	clients, err := db.ClientsAll(s.db())
	if err != nil {
		internalFailure(w, r, s, "api clients list", err)
		return
	}
	st := s.loadStatus()
	dns := s.loadDNSSeen()
	// Строку сервера читаем один раз на весь список, а не на каждого
	// клиента: адрес IPv6 выводится из неё арифметикой.
	var serverAddress, serverAddress6 string
	if server, err := db.ServerRow(s.db()); err == nil && server != nil {
		serverAddress, serverAddress6 = server.Address, server.Address6
	}
	now := time.Now()
	out := make([]clientJSON, 0, len(clients))
	for _, c := range clients {
		addr6, _ := db.ClientAddress6(serverAddress, serverAddress6, c.Address)
		out = append(out, clientToJSON(c, st, dns, addr6, now))
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
	s.audit(r, auditClientAdd, record.Name, "")
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
	changes := []string{}
	if req.Name != nil {
		changes = append(changes, "имя")
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
		changes = append(changes, "описание")
		if err := db.UpdateClientDescription(s.db(), id, *req.Description); err != nil {
			if msg, ok := classifyExpected(err); ok {
				writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "message": msg})
				return
			}
			internalFailure(w, r, s, "api clients patch description", err)
			return
		}
	}
	if req.MTU != nil {
		if err := db.UpdateClientMTU(s.db(), id, *req.MTU); err != nil {
			if errors.Is(err, db.ErrClientNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "message": flashNotFound})
				return
			}
			// Значение вне границ — ошибка человека, а не сбой: он вводил
			// его руками, и ему надо сказать, в каких пределах можно.
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"ok":      false,
				"message": fmt.Sprintf("MTU должен быть от %d до %d", db.ClientMTUFloor, db.ClientMTUCeiling),
			})
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
	// Включение и MTU записываются отдельно от переименования: по журналу
	// разбирают «что стало с этим клиентом», и «изменён» без указания чего
	// отвечает на этот вопрос ровно наполовину.
	if req.MTU != nil {
		detail := "как у сервера"
		if *req.MTU != 0 {
			detail = strconv.FormatInt(*req.MTU, 10)
		}
		s.audit(r, auditClientMTU, c.Name, detail)
	}
	if req.Enabled != nil {
		detail := "отключён"
		if *req.Enabled {
			detail = "включён"
		}
		s.audit(r, auditClientToggle, c.Name, detail)
	}
	if len(changes) > 0 {
		s.audit(r, auditClientEdit, c.Name, strings.Join(changes, ", "))
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
	// Имя читается до удаления: после него в журнал попал бы номер, по
	// которому уже некого искать.
	name := ""
	if c, err := db.ClientByID(s.db(), id); err == nil {
		name = c.Name
	}
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
	s.audit(r, auditClientDelete, name, "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
