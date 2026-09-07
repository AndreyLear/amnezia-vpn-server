// Какое развёртывание получилось (amnezia-vpn-server-8bt5).
//
// Панель живёт в контейнере и про хост знать не может: ни какая там система,
// ни какой Docker, ни включён ли сторож. Всё это решает установщик, он же и
// записывает — рядом со status.json, откуда панель читает только на чтение.
//
// Отсутствие файла — «неизвестно», а не поломка: так выглядит развёртывание,
// поставленное установщиком старше этой возможности.
package status

import (
	"encoding/json"
	"fmt"
	"os"
)

// Deployment is what install.sh recorded about the host it ran on.
// Pointer booleans so that "not recorded" stays different from "off".
type Deployment struct {
	Schema         string `json:"schema"`
	OS             string `json:"os"`
	Docker         string `json:"docker"`
	Installer      string `json:"installer"`
	Fail2ban       *bool  `json:"fail2ban"`
	Watchdog       *bool  `json:"watchdog"`
	UpdateCheck    *bool  `json:"update_check"`
	InstalledAtUTC string `json:"installed_at_utc"`
}

// ReadDeployment loads the facts. A missing file is not an error: the
// caller gets nil and must present every row as "unknown".
func ReadDeployment(path string) (*Deployment, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("status: read %s: %w", path, err)
	}
	var out Deployment
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("status: parse %s: %w", path, err)
	}
	return &out, nil
}
