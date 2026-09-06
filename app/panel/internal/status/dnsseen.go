// Кто из клиентов спрашивает имена у нашего резолвера
// (amnezia-vpn-server-g0vd).
//
// Снимок множества nftables пишет контейнер awg рядом со status.json: у него
// есть права на сеть, а у панели их нет и не будет. Панель только читает.
//
// Отсутствие файла и пустой список — разные вещи. Нет файла — сказать нечего
// (старая установка, правила ещё не применены, контейнер не той версии);
// пустой список — никто не спрашивал. Первое не должно выглядеть как второе,
// иначе панель обвинит всех разом.
package status

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// DNSSeen is the snapshot of the addresses that queried the in-tunnel
// resolver recently. Addresses are the clients' tunnel addresses, IPv4
// and IPv6 alike.
type DNSSeen struct {
	Schema      string    `json:"schema"`
	GeneratedAt time.Time `json:"generated_at_utc"`
	Addresses   []string  `json:"addresses"`
}

// Seen reports whether any of the given addresses queried the resolver.
// A nil snapshot answers false for everything, and callers must treat
// "no snapshot" as "no opinion" rather than as evidence.
func (d *DNSSeen) Seen(addrs ...string) bool {
	if d == nil {
		return false
	}
	for _, want := range addrs {
		if want == "" {
			continue
		}
		for _, got := range d.Addresses {
			if got == want {
				return true
			}
		}
	}
	return false
}

// ReadDNSSeen loads the snapshot. A missing file is not an error: it is
// the normal state of a deployment whose awg container predates this, and
// the caller gets nil to mean "no opinion".
func ReadDNSSeen(path string) (*DNSSeen, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("status: read %s: %w", path, err)
	}
	var out DNSSeen
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("status: parse %s: %w", path, err)
	}
	return &out, nil
}
