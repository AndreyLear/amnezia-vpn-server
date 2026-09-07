// Что известно про обновление (amnezia-vpn-server-zklt, -nukf, -8bt5).
//
// Панель наружу не ходит: она открыта в интернет по паролю, и чем меньше она
// умеет, тем меньше можно сделать, захватив её. Наружу ходит хост — раз в
// сутки спрашивает GitHub и кладёт ответ рядом со status.json. Здесь этот
// ответ только читается.
//
// Три файла, три разных вопроса. Что вышло (снимок ответа GitHub, как есть).
// Когда мы спрашивали и получилось ли. Что сейчас делает агент обновления.
// Отсутствие любого из них — «неизвестно», а не сбой: так выглядит и свежая
// установка, где ещё ничего не спрашивали.
package status

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Release is the part of the GitHub answer the panel needs. The file holds
// the whole answer byte for byte, because assembling JSON around arbitrary
// release notes in shell would tear one of them eventually.
type Release struct {
	TagName string `json:"tag_name"`
	Body    string `json:"body"`
}

// Version is the tag without its leading "v": what versions.lock calls a
// version, and what the panel compares against.
func (r *Release) Version() string {
	if r == nil {
		return ""
	}
	return strings.TrimPrefix(r.TagName, "v")
}

// Notes is the release body without the machine-readable trailer. The
// checksum line is for the update agent; showing it to a person would be
// noise in the middle of what they came to read.
func (r *Release) Notes() string {
	if r == nil {
		return ""
	}
	var kept []string
	for _, line := range strings.Split(r.Body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "amnezia-sha256:") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

// UpdateCheck is the host's own record of the last look: "never checked"
// and "checked, no luck" are different news.
type UpdateCheck struct {
	Schema       string `json:"schema"`
	CheckedAtUTC string `json:"checked_at_utc"`
	Result       string `json:"result"`
	Reason       string `json:"reason"`
}

// UpdateState is what the update agent is doing, or how it ended. It
// survives a restart on purpose: the update restarts the panel itself, and
// a browser that was closed the whole time must still find the outcome.
type UpdateState struct {
	Schema  string `json:"schema"`
	State   string `json:"state"`
	From    string `json:"from"`
	To      string `json:"to"`
	Step    string `json:"step"`
	Message string `json:"message"`
	AtUTC   string `json:"at_utc"`
}

func readJSONFile(path string, out any) (bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("status: read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return false, fmt.Errorf("status: parse %s: %w", path, err)
	}
	return true, nil
}

// ReadRelease loads the snapshot of the latest release. Missing file → nil.
func ReadRelease(path string) (*Release, error) {
	var out Release
	found, err := readJSONFile(path, &out)
	if err != nil || !found {
		return nil, err
	}
	return &out, nil
}

// ReadUpdateCheck loads the record of the last check. Missing file → nil.
func ReadUpdateCheck(path string) (*UpdateCheck, error) {
	var out UpdateCheck
	found, err := readJSONFile(path, &out)
	if err != nil || !found {
		return nil, err
	}
	return &out, nil
}

// ReadUpdateState loads what the agent is doing. Missing file → nil, which
// is the ordinary state of a server that has never been asked to update.
func ReadUpdateState(path string) (*UpdateState, error) {
	var out UpdateState
	found, err := readJSONFile(path, &out)
	if err != nil || !found {
		return nil, err
	}
	return &out, nil
}

// IsNewer reports whether candidate is a strictly later version than
// current. Both are "N.N.N"; anything else answers false rather than
// guessing — a version we cannot read is not a version we offer.
//
// Numeric, not lexicographic: 2.10.0 comes after 2.9.0, and comparing the
// strings would have said the opposite for the whole life of the product.
func IsNewer(candidate, current string) bool {
	a, okA := parseVersion(candidate)
	b, okB := parseVersion(current)
	if !okA || !okB {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}

func parseVersion(v string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(v), "v"), ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
