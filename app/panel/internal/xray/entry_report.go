package xray

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"regexp"
)

// Показатели узла-входа (amnezia-vpn-server-q1do.1).
//
// Узел после каждого успешного синка присылает POST /api/entry/report:
// свои CPU, память, диск — то же, что шапка показывает про этот сервер, —
// трафик основного интерфейса за интервал между синками, аптайм и версии.
// Узел — чужая машина с секретом, который могли украсть, поэтому отчёт
// не доверяется вслепую: проценты обрезаются до 0–100, числа — до
// неотрицательных и разумного потолка, занятое — до всего, строки версий
// проходят по шаблону и длине, а всё непохожее пропадает и показывается
// прочерком. Неизвестные поля пропускаются: более новый узел не должен
// ломаться о более старую панель.

// maxEntryReportBytes — потолок тела отчёта: настоящий отчёт — пара сотен
// байт.
const maxEntryReportBytes = 4 << 10

// maxEntryCounter — потолок любого счётчика (байты, секунды): 2^53, дальше
// float64 в браузере теряет точность, а настоящих значений там нет.
const maxEntryCounter = int64(1) << 53

// entryVersionPattern — версия Xray или выпуска: 26.10.21, 3.1.0-rc.1.
var entryVersionPattern = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+_-]{0,31}$`)

// ErrEntryReportInvalid — тело не разбирается как отчёт. Что именно не
// так, не уточняется: ответ узлу общий, как у прочих ошибок API.
var ErrEntryReportInvalid = errors.New("xray: entry report is not valid")

// EntryReport — последний отчёт узла после проверки. nil и пустая строка —
// «неизвестно», а не ноль.
type EntryReport struct {
	CPUPercent      *float64 `json:"cpu_percent"`
	RAMPercent      *float64 `json:"ram_percent"`
	DiskPercent     *float64 `json:"disk_percent"`
	RAMUsedBytes    *int64   `json:"ram_used_bytes"`
	RAMTotalBytes   *int64   `json:"ram_total_bytes"`
	DiskUsedBytes   *int64   `json:"disk_used_bytes"`
	DiskTotalBytes  *int64   `json:"disk_total_bytes"`
	RxBytes         *int64   `json:"rx_bytes"`
	TxBytes         *int64   `json:"tx_bytes"`
	IntervalSeconds *int64   `json:"interval_seconds"`
	UptimeSeconds   *int64   `json:"uptime_seconds"`
	XrayVersion     string   `json:"xray_version"`
	Release         string   `json:"release"`
	// Update — состояние самообновления (amnezia-vpn-server-q1do.2); nil —
	// узел старше самообновления и о нём не сообщает.
	Update *EntryUpdate `json:"update,omitempty"`
	// Watchdog — последний перезапуск Xray сторожем узла
	// (amnezia-vpn-server-q1do.5); nil — не было или узел старше сторожа.
	Watchdog *EntryWatchdog `json:"watchdog,omitempty"`
}

// rawEntryReport — тело как пришло: все числа — float64, чтобы дробное или
// отрицательное обрезалось, а не валило весь отчёт.
type rawEntryReport struct {
	CPUPercent      *float64       `json:"cpu_percent"`
	RAMPercent      *float64       `json:"ram_percent"`
	DiskPercent     *float64       `json:"disk_percent"`
	RAMUsedBytes    *float64       `json:"ram_used_bytes"`
	RAMTotalBytes   *float64       `json:"ram_total_bytes"`
	DiskUsedBytes   *float64       `json:"disk_used_bytes"`
	DiskTotalBytes  *float64       `json:"disk_total_bytes"`
	RxBytes         *float64       `json:"rx_bytes"`
	TxBytes         *float64       `json:"tx_bytes"`
	IntervalSeconds *float64       `json:"interval_seconds"`
	UptimeSeconds   *float64       `json:"uptime_seconds"`
	XrayVersion     string         `json:"xray_version"`
	Release         string         `json:"release"`
	Update          *EntryUpdate   `json:"update"`
	Watchdog        *EntryWatchdog `json:"watchdog"`
}

// ParseEntryReport разбирает и проверяет тело отчёта. Ошибка — тело не
// объект JSON нужного вида или больше maxEntryReportBytes; значения вне
// пределов ошибкой не считаются, а обрезаются.
func ParseEntryReport(body []byte) (EntryReport, error) {
	if len(body) > maxEntryReportBytes {
		return EntryReport{}, ErrEntryReportInvalid
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return EntryReport{}, ErrEntryReportInvalid
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	var raw rawEntryReport
	if err := dec.Decode(&raw); err != nil {
		return EntryReport{}, ErrEntryReportInvalid
	}
	if dec.More() {
		return EntryReport{}, ErrEntryReportInvalid
	}
	return EntryReport{
		CPUPercent:      raw.CPUPercent,
		RAMPercent:      raw.RAMPercent,
		DiskPercent:     raw.DiskPercent,
		RAMUsedBytes:    counter(raw.RAMUsedBytes),
		RAMTotalBytes:   counter(raw.RAMTotalBytes),
		DiskUsedBytes:   counter(raw.DiskUsedBytes),
		DiskTotalBytes:  counter(raw.DiskTotalBytes),
		RxBytes:         counter(raw.RxBytes),
		TxBytes:         counter(raw.TxBytes),
		IntervalSeconds: counter(raw.IntervalSeconds),
		UptimeSeconds:   counter(raw.UptimeSeconds),
		XrayVersion:     raw.XrayVersion,
		Release:         raw.Release,
		Update:          raw.Update,
		Watchdog:        raw.Watchdog,
	}.Sanitized(), nil
}

// Sanitized — отчёт в пределах. Применяется и к телу запроса, и к тому,
// что прочитано из базы: строка в базе могла остаться от другой версии
// панели или быть исправлена руками.
func (r EntryReport) Sanitized() EntryReport {
	r.CPUPercent = percent(r.CPUPercent)
	r.RAMPercent = percent(r.RAMPercent)
	r.DiskPercent = percent(r.DiskPercent)
	for _, p := range []**int64{&r.RAMUsedBytes, &r.RAMTotalBytes, &r.DiskUsedBytes, &r.DiskTotalBytes,
		&r.RxBytes, &r.TxBytes, &r.IntervalSeconds, &r.UptimeSeconds} {
		*p = clampCounter(*p)
	}
	r.RAMUsedBytes = notAbove(r.RAMUsedBytes, r.RAMTotalBytes)
	r.DiskUsedBytes = notAbove(r.DiskUsedBytes, r.DiskTotalBytes)
	if !entryVersionPattern.MatchString(r.XrayVersion) {
		r.XrayVersion = ""
	}
	if !entryVersionPattern.MatchString(r.Release) {
		r.Release = ""
	}
	r.Update = r.Update.sanitized()
	r.Watchdog = r.Watchdog.sanitized()
	return r
}

func percent(v *float64) *float64 {
	if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) {
		return nil
	}
	out := math.Min(100, math.Max(0, *v))
	return &out
}

func counter(v *float64) *int64 {
	if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) {
		return nil
	}
	f := math.Trunc(math.Min(float64(maxEntryCounter), math.Max(0, *v)))
	out := int64(f)
	return &out
}

func clampCounter(v *int64) *int64 {
	if v == nil {
		return nil
	}
	out := min(maxEntryCounter, max(0, *v))
	return &out
}

func notAbove(used, total *int64) *int64 {
	if used == nil || total == nil || *used <= *total {
		return used
	}
	out := *total
	return &out
}
