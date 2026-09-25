package web

import (
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/hostmetrics"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/status"
)

type hostStatsResponse struct {
	CPUPercent     *float64 `json:"cpu_percent"`
	RAMPercent     *float64 `json:"ram_percent"`
	DiskPercent    *float64 `json:"disk_percent"`
	RAMUsedBytes   *int64   `json:"ram_used_bytes"`
	RAMTotalBytes  *int64   `json:"ram_total_bytes"`
	DiskUsedBytes  *int64   `json:"disk_used_bytes"`
	DiskTotalBytes *int64   `json:"disk_total_bytes"`
	Iface          string   `json:"iface"`
	IfaceState     string   `json:"iface_state"`
}

func ifaceNameFromConf(confPath string) string {
	name := strings.TrimSuffix(filepath.Base(confPath), ".conf")
	if name == "" || name == "." {
		return "awg0"
	}
	return name
}

func ifaceStateJSON(state InterfaceState) string {
	switch state {
	case IfaceUp:
		return "up"
	case IfaceNA:
		return "na"
	case IfaceDown:
		return "down"
	case IfaceError:
		return "error"
	default:
		return "na"
	}
}

// hostCPUPeriod is the window of the CPU figure; tests shorten it.
var hostCPUPeriod = hostmetrics.CPUPeriod

func (s *Server) apiStatsHost(w http.ResponseWriter, r *http.Request) {
	// CPU is measured in the background over fixed windows; a request only
	// reads the last figure, so several open tabs do not shrink the window
	// to milliseconds (amnezia-vpn-server-76mp.23).
	snap := hostmetrics.ReadMemDisk(s.cfg.HostProcDir, s.cfg.HostDiskPath)
	snap.CPU = hostmetrics.SharedCPUMeter(s.cfg.HostProcDir, hostCPUPeriod).Percent()

	st, readErr := status.ReadStatus(s.cfg.StatusPath)
	rec := Reconcile(nil, st, readErr, time.Now().UTC())
	iface := ifaceNameFromConf(s.cfg.ConfPath)
	if rec.Interface == IfaceUp && st != nil && st.Interface != nil && st.Interface.Iface != "" {
		iface = st.Interface.Iface
	}

	writeJSON(w, http.StatusOK, hostStatsResponse{
		CPUPercent:     snap.CPU,
		RAMPercent:     snap.RAM,
		DiskPercent:    snap.Disk,
		RAMUsedBytes:   snap.RAMUsedBytes,
		RAMTotalBytes:  snap.RAMTotalBytes,
		DiskUsedBytes:  snap.DiskUsedBytes,
		DiskTotalBytes: snap.DiskTotalBytes,
		Iface:          iface,
		IfaceState:     ifaceStateJSON(rec.Interface),
	})
}
