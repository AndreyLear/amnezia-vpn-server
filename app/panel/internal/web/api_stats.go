package web

import (
	"net/http"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/hostmetrics"
)

type hostStatsResponse struct {
	CPUPercent  *float64 `json:"cpu_percent"`
	RAMPercent  *float64 `json:"ram_percent"`
	DiskPercent *float64 `json:"disk_percent"`
}

func (s *Server) apiStatsHost(w http.ResponseWriter, r *http.Request) {
	s.hostMu.Lock()
	snap, next := hostmetrics.Read(s.cfg.HostProcDir, s.cfg.HostDiskPath, s.hostCPU)
	s.hostCPU = next
	s.hostMu.Unlock()

	writeJSON(w, http.StatusOK, hostStatsResponse{
		CPUPercent:  snap.CPU,
		RAMPercent:  snap.RAM,
		DiskPercent: snap.Disk,
	})
}
