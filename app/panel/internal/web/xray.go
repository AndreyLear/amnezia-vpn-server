package web

import (
	"database/sql"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/awgconf"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/xray"
)

// regenerate пересобирает производные конфиги после изменения: awg0.conf
// (его ошибка — ошибка изменения) и конфиг Xray (amnezia-vpn-server-14y0.2),
// сбой которого только пишется в журнал — изменение уже в базе и в AWG.
func (s *Server) regenerate(handle *sql.DB) error {
	if err := awgconf.Generate(handle, s.cfg.ConfPath); err != nil {
		return err
	}
	s.regenerateXray(handle)
	return nil
}

func (s *Server) regenerateXray(handle *sql.DB) {
	xray.Regenerate(handle, s.cfg.ConfPath, s.cfg.Logger.Printf)
}
