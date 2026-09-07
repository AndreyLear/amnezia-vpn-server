// Версии, которые несёт развёртывание (amnezia-vpn-server-rdcz).
//
// Панель до этого не знала ни одной: номера существовали только как аргументы
// сборки образов и как тег в versions.lock. На вопрос «что у меня стоит»
// отвечал SSH, а не панель.
//
// Свою версию панель получает переменной окружения при запуске; версии
// AmneziaWG кладёт рядом со status.json контейнер awg, который их знает.
// Отсутствие файла — «неизвестно», а не поломка: так выглядит развёртывание,
// где контейнер awg старше этой возможности.
package status

import (
	"encoding/json"
	"fmt"
	"os"
)

// Versions is the snapshot the awg container writes at startup.
type Versions struct {
	Schema         string `json:"schema"`
	AmneziaWGGo    string `json:"amneziawg_go"`
	AmneziaWGTools string `json:"amneziawg_tools"`
}

// ReadVersions loads the snapshot. A missing file is not an error: the
// caller gets nil and must present it as "unknown".
func ReadVersions(path string) (*Versions, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("status: read %s: %w", path, err)
	}
	var out Versions
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("status: parse %s: %w", path, err)
	}
	return &out, nil
}
