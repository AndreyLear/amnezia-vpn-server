package db

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func openSessionsDB(t *testing.T) *sqlDBT {
	t.Helper()
	h, err := Open(filepath.Join(t.TempDir(), "amnezia.sqlite"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { h.Close() })
	if err := Migrate(h); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return &sqlDBT{h}
}

// Сессия проходит полный круг: записана, прочитана, удалена
// (amnezia-vpn-server-4aab).
func TestSessionsRoundTrip(t *testing.T) {
	d := openSessionsDB(t)
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	rec := SessionRecord{IDHash: "h1", Username: "admin", CSRFToken: "c1", CreatedAt: now, ExpiresAt: now.Add(20 * time.Minute)}
	if err := SaveSession(d.h, rec); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	got, err := LoadLiveSessions(d.h, now)
	if err != nil || len(got) != 1 || got[0].Username != "admin" || got[0].CSRFToken != "c1" {
		t.Fatalf("LoadLiveSessions = %+v, %v", got, err)
	}
	if !got[0].ExpiresAt.Equal(rec.ExpiresAt) {
		t.Errorf("срок %v, записан %v", got[0].ExpiresAt, rec.ExpiresAt)
	}
	if err := DeleteSession(d.h, "h1"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if got, _ := LoadLiveSessions(d.h, now); len(got) != 0 {
		t.Errorf("после удаления осталось %d", len(got))
	}
}

// Уборка истёкших не должна задевать живую сессию на границе секунды.
// С RFC3339Nano «…:00.5Z» сравнивалось со «…:00Z» не по времени.
func TestSessionsPruneRespectsSubsecondOrder(t *testing.T) {
	d := openSessionsDB(t)
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	live := SessionRecord{IDHash: "live", Username: "admin", CSRFToken: "c", CreatedAt: now, ExpiresAt: now.Add(500 * time.Millisecond)}
	if err := SaveSession(d.h, live); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	got, err := LoadLiveSessions(d.h, now)
	if err != nil {
		t.Fatalf("LoadLiveSessions: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("живая сессия, истекающая через полсекунды, удалена уборкой: %+v", got)
	}
}

// Смена пароля из командной строки убирает сессии только этого человека.
func TestDeleteSessionsByUsername(t *testing.T) {
	d := openSessionsDB(t)
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	for _, r := range []SessionRecord{
		{IDHash: "a1", Username: "admin", CSRFToken: "c", CreatedAt: now, ExpiresAt: now.Add(time.Hour)},
		{IDHash: "a2", Username: "admin", CSRFToken: "c", CreatedAt: now, ExpiresAt: now.Add(time.Hour)},
		{IDHash: "o1", Username: "other", CSRFToken: "c", CreatedAt: now, ExpiresAt: now.Add(time.Hour)},
	} {
		if err := SaveSession(d.h, r); err != nil {
			t.Fatalf("SaveSession: %v", err)
		}
	}
	if err := DeleteSessionsByUsername(d.h, "admin"); err != nil {
		t.Fatalf("DeleteSessionsByUsername: %v", err)
	}
	got, _ := LoadLiveSessions(d.h, now)
	if len(got) != 1 || got[0].Username != "other" {
		t.Errorf("осталось %+v, ждали только other", got)
	}
}

// sqlDBT keeps the handle together for the helpers above.
type sqlDBT struct{ h *sql.DB }
