package xray

import (
	"slices"
	"time"
)

// Сторож узла-входа (amnezia-vpn-server-q1do.5).
//
// Restart=always у xray-entry поднимает упавший Xray, а зависший — нет: это
// делает сторож узла (entry-sync.py watchdog). О последнем перезапуске узел
// сообщает в каждом отчёте — время и код причины; слова к коду, как и у
// обновления, — у панели, а не у узла.

// EntryWatchdogReasons — коды причин перезапуска: api — API Xray не
// ответил, port — вход не принимал соединения.
var EntryWatchdogReasons = []string{"api", "port"}

// EntryWatchdog — последний перезапуск Xray сторожем узла.
type EntryWatchdog struct {
	RestartedAtUTC string `json:"restarted_at_utc"`
	Reason         string `json:"reason"`
}

// sanitized — запись в пределах; nil — без времени перезапуска сказать
// нечего.
func (w *EntryWatchdog) sanitized() *EntryWatchdog {
	if w == nil {
		return nil
	}
	t, err := time.Parse(time.RFC3339, w.RestartedAtUTC)
	if err != nil {
		return nil
	}
	out := EntryWatchdog{RestartedAtUTC: t.UTC().Format(time.RFC3339)}
	if slices.Contains(EntryWatchdogReasons, w.Reason) {
		out.Reason = w.Reason
	}
	return &out
}
