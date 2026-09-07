// Что сторож выяснил за последнюю минуту (amnezia-vpn-server-eq82).
//
// Отказ, который чинится сам, невидим ровно до тех пор, пока о нём негде
// прочитать. Сторож каждую минуту проверяет резолвер и свежесть статуса
// туннеля и перезапускает то, что перестало работать, — но узнать об этом
// можно было только по SSH, и владелец слышал о сбое от пользователей, а не
// от панели. Ровно та беда, из-за которой сторож и заводился.
//
// Панель живёт в контейнере и ни про журнал, ни про systemd знать не может,
// поэтому сторож записывает найденное рядом со status.json. Здесь оно только
// читается.
package status

import "strings"

// Service is one watched service as the watchdog last saw it.
type Service struct {
	Name string `json:"name"`
	// State: "ok", "fail" или "unknown" — последнее означает, что сторож
	// до этой службы ещё не добирался, а не что она сломана.
	State string `json:"state"`
	// Reason непуст только при отказе и говорит, что именно не сошлось.
	Reason string `json:"reason"`
	// Fails — сколько отказов подряд насчитано. Перезапуск наступает не с
	// первого: сеть моргает, и дёргать туннель из-за запинки — вред.
	Fails int `json:"fails"`
	// RestartedAtUTC и RestartReason переживают починку: через минуту всё
	// исправно, и без этих полей не осталось бы следа, что сервер сам себя
	// чинил.
	RestartedAtUTC string `json:"restarted_at_utc"`
	RestartReason  string `json:"restart_reason"`
}

// Healthy says the watchdog saw this service working. Unknown is not
// healthy and not broken: it is the absence of an opinion.
func (s Service) Healthy() bool { return s.State == "ok" }

// Services is the whole snapshot.
type Services struct {
	Schema       string    `json:"schema"`
	CheckedAtUTC string    `json:"checked_at_utc"`
	Services     []Service `json:"services"`
}

// Find returns the named service, or false when the snapshot has no
// opinion about it.
func (s *Services) Find(name string) (Service, bool) {
	if s == nil {
		return Service{}, false
	}
	for _, item := range s.Services {
		if strings.EqualFold(item.Name, name) {
			return item, true
		}
	}
	return Service{}, false
}

// ReadServices loads the snapshot. A missing file is not an error: that is
// what a deployment without a watchdog looks like, and what a deployment
// looks like in the first minute after an install.
func ReadServices(path string) (*Services, error) {
	var out Services
	found, err := readJSONFile(path, &out)
	if err != nil || !found {
		return nil, err
	}
	return &out, nil
}
