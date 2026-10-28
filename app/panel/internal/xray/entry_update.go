package xray

import (
	"regexp"
	"slices"
	"time"
)

// Самообновление узла-входа (amnezia-vpn-server-q1do.2, решение владельца
// 28.10.2026).
//
// Кто что решает. Панель в ответе синка называет только целевой выпуск —
// номер X.Y.Z, по умолчанию свою версию, — и метку запроса владельца,
// если тот нажал «Обновить каскад». Ни адреса архива, ни его суммы в
// ответе нет: откуда качать, узел знает сам (репозиторий зашит в
// entry-sync.py), а сумму берёт из тела выпуска на GitHub. Украденный
// секрет узла поэтому даёт лишь попросить официальный выпуск; понижать
// версию узел отказывается, после неудачи выдерживает паузу.
//
// Узел сообщает итог в каждом отчёте: состояние, выпуск, к которому шёл,
// код причины неудачи (текст к нему — у панели: узлу не дают писать в
// интерфейс владельца свои слова), время и метку последнего запроса
// владельца, который он забрал.

// releasePattern — номер выпуска: три числа без ведущих нулей. Ни «v»,
// ни суффиксов: самообновление ставит только официальные выпуски.
var releasePattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})$`)

// updateRequestPattern — метка запроса владельца: время UTC без долей.
var updateRequestPattern = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$`)

// EntryUpdateStates — состояния обновления на узле.
var EntryUpdateStates = []string{"idle", "updating", "failed"}

// EntryUpdateReasons — коды причин неудачи, которые знает панель.
var EntryUpdateReasons = []string{"download", "checksum", "archive", "install", "inactive", "rollback", "interrupted", "version"}

// EntryUpdate — состояние самообновления из отчёта узла.
type EntryUpdate struct {
	State   string `json:"state"`
	Target  string `json:"target"`
	Reason  string `json:"reason"`
	AtUTC   string `json:"at_utc"`
	Request string `json:"request"`
}

// sanitized — состояние в пределах; nil — состояния нет или оно не из
// списка, и панель считает узел не умеющим обновляться.
func (u *EntryUpdate) sanitized() *EntryUpdate {
	if u == nil || !slices.Contains(EntryUpdateStates, u.State) {
		return nil
	}
	out := EntryUpdate{State: u.State}
	if releasePattern.MatchString(u.Target) {
		out.Target = u.Target
	}
	if u.State == "failed" && slices.Contains(EntryUpdateReasons, u.Reason) {
		out.Reason = u.Reason
	}
	if t, err := time.Parse(time.RFC3339, u.AtUTC); err == nil {
		out.AtUTC = t.UTC().Format(time.RFC3339)
	}
	if updateRequestPattern.MatchString(u.Request) {
		out.Request = u.Request
	}
	return &out
}

// EntryTargetVersion — целевой выпуск для узла по версии панели; пусто —
// версия панели не номер выпуска (сборка разработчика, старый контейнер),
// и узлу обновляться не на что.
func EntryTargetVersion(panelVersion string) string {
	if releasePattern.MatchString(panelVersion) {
		return panelVersion
	}
	return ""
}
