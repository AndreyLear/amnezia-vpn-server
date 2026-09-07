package web

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/status"
)

// Что стоит на сервере (amnezia-vpn-server-rdcz).
//
// До этого панель не знала ни одной версии — ни своей, ни AmneziaWG, — и на
// вопрос «что у меня установлено» отвечал только SSH. Своя версия приходит
// переменной окружения из versions.lock (одно место, а не два), версии
// AmneziaWG — из файла, который пишет контейнер awg.
//
// Неизвестное значение возвращается пустой строкой, а не выдумывается: панель
// на старом развёртывании честно скажет «неизвестно», и это правда, а не сбой.
type versionsJSON struct {
	Product        string `json:"product"`
	AmneziaWGGo    string `json:"amneziawg_go"`
	AmneziaWGTools string `json:"amneziawg_tools"`
	Schema         string `json:"schema"`
}

// productVersion is what this deployment runs; empty when the container
// predates AMNEZIA_VERSION or the value was not passed.
func productVersion() string {
	return os.Getenv("AMNEZIA_VERSION")
}

func (s *Server) apiVersions(w http.ResponseWriter, r *http.Request) {
	out := versionsJSON{Product: productVersion()}

	if v, err := status.ReadVersions(filepath.Join(filepath.Dir(s.cfg.StatusPath), "versions.json")); err == nil && v != nil {
		out.AmneziaWGGo = v.AmneziaWGGo
		out.AmneziaWGTools = v.AmneziaWGTools
	}
	if stored, err := db.SchemaVersionStored(s.db()); err == nil {
		out.Schema = stored
	}
	writeJSON(w, http.StatusOK, out)
}
