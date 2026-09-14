package notify

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/status"
)

// Told values: what the operator was last told about a trouble.
const (
	toldNothing = ""
	toldDown    = "down"
)

// Trouble tracks one condition that can start and end.
type Trouble struct {
	// Since is when the current occurrence started; nil while all is well.
	Since *time.Time `json:"since_utc,omitempty"`
	// Told is toldDown once a letter about it went into the outbox, and
	// back to toldNothing once its end was written about.
	Told string `json:"told,omitempty"`
	// ToldSince is the start of the occurrence the letter was about, for
	// the «it is over» letter.
	ToldSince *time.Time `json:"told_since_utc,omitempty"`
}

// Restart tracks what the watchdog did to one service.
type Restart struct {
	// Seen is the last restarted_at_utc taken into account.
	Seen string `json:"seen,omitempty"`
	// Pending is a restart whose outcome is not known yet.
	Pending *time.Time `json:"pending_utc,omitempty"`
	Reason  string     `json:"reason,omitempty"`
	// ToldBroken: a letter said the service does not work after a restart.
	ToldBroken bool `json:"told_broken,omitempty"`
}

// Flap counts changes of state in one group.
type Flap struct {
	Changes    []time.Time `json:"changes_utc,omitempty"`
	MutedUntil *time.Time  `json:"muted_until_utc,omitempty"`
}

// State is what the rules remember between runs. It holds no secrets.
type State struct {
	Initialized bool    `json:"initialized"`
	Tunnel      Trouble `json:"tunnel"`
	// TunnelBackAt is when the tunnel last came back. Clients that fell
	// silent before it did were cut by the tunnel drop, not by anything
	// the client rule should report.
	TunnelBackAt *time.Time         `json:"tunnel_back_at_utc,omitempty"`
	Clients      Trouble            `json:"clients"`
	Restarts     map[string]Restart `json:"restarts,omitempty"`
	UpdateSeen   string             `json:"update_seen,omitempty"`
	// UpdateBroken — письмо сказало, что обновление не удалось и откатить не
	// вышло; UpdateHealthySince — с какого момента сервер после этого
	// непрерывно исправен. Нужны для письма о возврате к норме
	// (amnezia-vpn-server-crar).
	UpdateBroken       *time.Time      `json:"update_broken_utc,omitempty"`
	UpdateHealthySince *time.Time      `json:"update_healthy_since_utc,omitempty"`
	Release            string          `json:"release_seen,omitempty"`
	Flaps              map[string]Flap `json:"flaps,omitempty"`
}

// LoadState reads the rules' memory. A missing file is a first run. A file
// that cannot be read is reported and treated as a first run too: a lost
// memory costs one baseline, a stuck one would cost every letter.
func LoadState(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &State{}, nil
	}
	if err != nil {
		return &State{}, fmt.Errorf("notify: read state: %w", err)
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return &State{}, fmt.Errorf("notify: state %s unreadable, starting over", path)
	}
	return &st, nil
}

// Marshal is the stored form; awgmail compares it to skip writes that
// change nothing.
func (s *State) Marshal() []byte {
	data, _ := json.MarshalIndent(s, "", "  ")
	return append(data, '\n')
}

// Save writes the state atomically, mode 0600.
func (s *State) Save(path string) error {
	return status.WriteAtomic(path, s.Marshal())
}
