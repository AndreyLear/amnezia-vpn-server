// Журнал панели наружу (amnezia-vpn-server-gqep).
package web

import (
	"net/http"
	"strconv"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

type auditEntryJSON struct {
	AtUTC   string `json:"at_utc"`
	Actor   string `json:"actor"`
	Action  string `json:"action"`
	Subject string `json:"subject"`
	Detail  string `json:"detail"`
}

func (s *Server) apiAudit(w http.ResponseWriter, r *http.Request) {
	limit := 200
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil {
			limit = v
		}
	}
	records, err := db.AuditTail(s.db(), limit)
	if err != nil {
		internalFailure(w, r, s, "api audit", err)
		return
	}
	out := make([]auditEntryJSON, 0, len(records))
	for _, rec := range records {
		out = append(out, auditEntryJSON{
			AtUTC:   rec.AtUTC,
			Actor:   rec.Actor,
			Action:  rec.Action,
			Subject: rec.Subject,
			Detail:  rec.Detail,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": out})
}
