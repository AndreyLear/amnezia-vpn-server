package auth

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// InvalidateSessionsPath is the sidecar file next to the SQLite
// database. The CLI process writes it after a password change; the
// serve process consumes it on RequireAuth. The two processes do not
// share the in-memory SessionStore.
func InvalidateSessionsPath(dbPath string) string {
	return dbPath + ".invalidate-sessions"
}

// RequestInvalidateSessions records that live panel sessions for
// username must be dropped. username "*" invalidates every session.
// The file is 0600 and replaced atomically (unique temp + rename).
func RequestInvalidateSessions(dbPath, username string) error {
	username = strings.TrimSpace(username)
	if username == "" {
		return os.ErrInvalid
	}
	path := InvalidateSessionsPath(dbPath)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	payload := []byte(username + "\n")
	f, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	cleanup := func() {
		f.Close()
		os.Remove(tmp)
	}
	if err := f.Chmod(0o600); err != nil {
		cleanup()
		return err
	}
	if _, err := f.Write(payload); err != nil {
		cleanup()
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// ConsumeInvalidateSessions applies a pending sidecar (if any) to
// store and removes the consumed file. Concurrent consumers race on
// rename: only one wins, later callers see os.ErrNotExist. A CLI
// write that lands after the rename stays on disk for the next
// request. Missing file is a no-op. Leftover .taking-* files from a
// crashed consume are applied as well so a restart cannot skip them.
func ConsumeInvalidateSessions(dbPath string, store *SessionStore) {
	if dbPath == "" || store == nil {
		return
	}
	path := InvalidateSessionsPath(dbPath)
	taking := fmt.Sprintf("%s.taking-%d-%d", path, os.Getpid(), time.Now().UnixNano())
	_ = os.Rename(path, taking)
	matches, err := filepath.Glob(path + ".taking-*")
	if err != nil {
		return
	}
	for _, p := range matches {
		applyInvalidateFile(p, store)
		os.Remove(p)
	}
}

func applyInvalidateFile(path string, store *SessionStore) {
	data, err := os.ReadFile(path)
	if err != nil {
		store.DeleteAll()
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if line == "*" {
			store.DeleteAll()
			return
		}
		store.ForgetByUsername(line)
	}
}
