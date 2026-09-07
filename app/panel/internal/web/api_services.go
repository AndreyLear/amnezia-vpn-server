// Состояние служб глазами панели (amnezia-vpn-server-eq82).
//
// Всё, что здесь есть, добыл сторож на хосте; панель только читает его
// снимок. Действий нет и не задумано: это правда о состоянии, а не пульт.
package web

import (
	"net/http"
	"path/filepath"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/status"
)

type serviceJSON struct {
	Name           string `json:"name"`
	State          string `json:"state"`
	Reason         string `json:"reason"`
	Fails          int    `json:"fails"`
	RestartedAtUTC string `json:"restarted_at_utc"`
	RestartReason  string `json:"restart_reason"`
}

type servicesJSON struct {
	// CheckedAtUTC — когда сторож смотрел в последний раз. Пусто означает
	// «не смотрел ни разу», и это разные вещи с «смотрел, всё хорошо».
	CheckedAtUTC string `json:"checked_at_utc"`
	// Watchdog говорит, поставлен ли сторож вообще. Без этого молчащий
	// снимок читался бы как поломка, тогда как владелец мог просто
	// отказаться от сторожа при установке.
	Watchdog *bool         `json:"watchdog"`
	Services []serviceJSON `json:"services"`
}

func (s *Server) apiServices(w http.ResponseWriter, r *http.Request) {
	out := servicesJSON{Services: []serviceJSON{}}
	dir := s.statusDir()

	if d, err := status.ReadDeployment(filepath.Join(dir, "deployment.json")); err == nil && d != nil {
		out.Watchdog = d.Watchdog
	}
	if snap, err := status.ReadServices(filepath.Join(dir, "services.json")); err == nil && snap != nil {
		out.CheckedAtUTC = snap.CheckedAtUTC
		for _, item := range snap.Services {
			out.Services = append(out.Services, serviceJSON{
				Name:           item.Name,
				State:          item.State,
				Reason:         item.Reason,
				Fails:          item.Fails,
				RestartedAtUTC: item.RestartedAtUTC,
				RestartReason:  item.RestartReason,
			})
		}
	}
	writeJSON(w, http.StatusOK, out)
}
