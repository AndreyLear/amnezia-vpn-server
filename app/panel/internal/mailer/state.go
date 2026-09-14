package mailer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/status"
)

// StateName is the outbox file's name in the status directory, where both
// awgmail and the panel find it.
const StateName = "mail-state.json"

// RetryDelays are the pauses after the first, second and third failed
// attempt. After the last one the message is dropped and the refusal
// remembered.
var RetryDelays = []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute}

// Pending is a message waiting to be delivered.
type Pending struct {
	Key       string    `json:"key"`
	Message   Message   `json:"message"`
	Attempts  int       `json:"attempts"`
	NextAt    time.Time `json:"next_at_utc"`
	LastError string    `json:"last_error,omitempty"`
}

// Failure is a message given up on. The panel shows it so that a silent
// channel is not mistaken for a quiet server (amnezia-vpn-server-pz2r).
type Failure struct {
	Key     string `json:"key"`
	Subject string `json:"subject"`
	// Summary explains the failure in words; Error is the server's reply.
	Summary string    `json:"summary,omitempty"`
	Error   string    `json:"error"`
	At      time.Time `json:"at_utc"`
}

// State is the outbox, persisted between timer runs. It never holds the
// password: errors are redacted by Sender before they get here.
type State struct {
	Pending       []Pending  `json:"pending"`
	LastSuccessAt *time.Time `json:"last_success_at_utc,omitempty"`
	LastFailure   *Failure   `json:"last_failure,omitempty"`
}

// Outcome is what happened to one message during Deliver, for the log.
type Outcome struct {
	Key     string
	Subject string
	Sent    bool
	GaveUp  bool
	Attempt int
	RetryAt time.Time
	Err     error
}

// Put queues msg under key, replacing a message still pending under the
// same key: the newer one describes the current state, and its attempts
// start over.
func (s *State) Put(key string, msg Message, now time.Time) {
	p := Pending{Key: key, Message: msg, NextAt: now.UTC()}
	for i := range s.Pending {
		if s.Pending[i].Key == key {
			s.Pending[i] = p
			return
		}
	}
	s.Pending = append(s.Pending, p)
}

// Deliver attempts every message that is due, in queue order.
func (s *State) Deliver(ctx context.Context, now time.Time, send func(context.Context, Message) error) []Outcome {
	var outcomes []Outcome
	kept := s.Pending[:0]
	for _, p := range s.Pending {
		if now.Before(p.NextAt) {
			kept = append(kept, p)
			continue
		}
		err := send(ctx, p.Message)
		p.Attempts++
		o := Outcome{Key: p.Key, Subject: p.Message.Subject, Attempt: p.Attempts, Err: err}
		if err == nil {
			o.Sent = true
			at := now.UTC()
			s.LastSuccessAt = &at
			s.LastFailure = nil
			outcomes = append(outcomes, o)
			continue
		}
		if p.Attempts > len(RetryDelays) {
			o.GaveUp = true
			s.LastFailure = &Failure{Key: p.Key, Subject: p.Message.Subject, Summary: Summary(err), Error: err.Error(), At: now.UTC()}
			outcomes = append(outcomes, o)
			continue
		}
		p.LastError = err.Error()
		p.NextAt = now.UTC().Add(RetryDelays[p.Attempts-1])
		o.RetryAt = p.NextAt
		kept = append(kept, p)
		outcomes = append(outcomes, o)
	}
	s.Pending = kept
	return outcomes
}

// LoadState reads the outbox. A missing file is an empty outbox. A file
// that cannot be parsed is reported and replaced by an empty outbox: a
// lost retry is better than a service that stops sending for good.
func LoadState(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &State{}, nil
	}
	if err != nil {
		return &State{}, fmt.Errorf("mailer: read state: %w", err)
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return &State{}, fmt.Errorf("mailer: state %s unreadable, starting empty", path)
	}
	return &st, nil
}

// Save writes the outbox atomically, mode 0600.
func (s *State) Save(path string) error {
	if s.Pending == nil {
		s.Pending = []Pending{}
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("mailer: encode state: %w", err)
	}
	return status.WriteAtomic(path, append(data, '\n'))
}
