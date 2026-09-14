package mailconf

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/status"
)

// The test letter (amnezia-vpn-server-8fg2).
//
// The panel does not reach the outside world, so it cannot send the test
// letter itself. It leaves a request beside mail.conf; a systemd path unit
// wakes awgmail on the host, which sends the letter and writes the outcome
// into status/, where the panel reads it. The request carries an id, and
// the outcome names the id it answers: «answered» is then a comparison, not
// a guess about timing, and a request is never answered twice — awgmail does
// not delete the request, because its sandbox may write only status/.

// TestLetterRequestPath is where the panel leaves the request: beside mail.conf,
// in the directory the panel writes and the host reads.
func TestLetterRequestPath(confPath string) string {
	return filepath.Join(filepath.Dir(confPath), "mail-test-request.json")
}

// TestLetterResultName is the outcome's file name in the status directory.
const TestLetterResultName = "mail-test.json"

// TestLetterRequest asks for one test letter.
type TestLetterRequest struct {
	ID    string    `json:"id"`
	AtUTC time.Time `json:"at_utc"`
}

// TestLetterResult is the outcome of the request with the same ID. Error is the
// sender's error, already free of the password (internal/mailer redacts it).
type TestLetterResult struct {
	ID string `json:"id"`
	OK bool   `json:"ok"`
	// Summary explains a failure in words; Error is the sender's error text.
	Summary string    `json:"summary,omitempty"`
	Error   string    `json:"error,omitempty"`
	AtUTC   time.Time `json:"at_utc"`
}

// WriteTestLetterRequest leaves a new request with a fresh id.
func WriteTestLetterRequest(path string, now time.Time) (*TestLetterRequest, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil, fmt.Errorf("mailconf: request id: %w", err)
	}
	req := &TestLetterRequest{ID: hex.EncodeToString(raw[:]), AtUTC: now.UTC()}
	if err := writeJSON(path, req); err != nil {
		return nil, err
	}
	return req, nil
}

// ReadTestLetterRequest returns nil, nil when there is no request.
func ReadTestLetterRequest(path string) (*TestLetterRequest, error) {
	var req TestLetterRequest
	found, err := readJSON(path, &req)
	if err != nil || !found {
		return nil, err
	}
	return &req, nil
}

// WriteTestLetterResult records an outcome.
func WriteTestLetterResult(path string, res *TestLetterResult) error {
	return writeJSON(path, res)
}

// ReadTestLetterResult returns nil, nil when there is no outcome yet.
func ReadTestLetterResult(path string) (*TestLetterResult, error) {
	var res TestLetterResult
	found, err := readJSON(path, &res)
	if err != nil || !found {
		return nil, err
	}
	return &res, nil
}

func writeJSON(path string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("mailconf: encode: %w", err)
	}
	if err := status.WriteAtomic(path, append(data, '\n')); err != nil {
		return fmt.Errorf("mailconf: %w", err)
	}
	return nil
}

func readJSON(path string, v any) (bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("mailconf: read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return false, fmt.Errorf("mailconf: parse %s: %w", path, err)
	}
	return true, nil
}
