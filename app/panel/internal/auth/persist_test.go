package auth

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// memPersister is a stand-in for the database: it records exactly what the
// store hands over, so the tests can check what would land on disk.
type memPersister struct {
	mu   sync.Mutex
	rows map[string]PersistedSession
}

func newMemPersister() *memPersister { return &memPersister{rows: map[string]PersistedSession{}} }

func (m *memPersister) Save(p PersistedSession) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[p.IDHash] = p
	return nil
}
func (m *memPersister) Delete(h string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.rows, h)
	return nil
}
func (m *memPersister) DeleteAll() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows = map[string]PersistedSession{}
	return nil
}
func (m *memPersister) LoadLive(now time.Time) ([]PersistedSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []PersistedSession
	for _, p := range m.rows {
		if p.ExpiresAt.After(now) {
			out = append(out, p)
		}
	}
	return out, nil
}

// restart builds a fresh store over the same storage — what happens when
// the panel restarts during an update.
func restart(t *testing.T, p *memPersister) *SessionStore {
	t.Helper()
	s := NewSessionStore(SessionTTL)
	if err := s.SetPersister(p, nil); err != nil {
		t.Fatalf("SetPersister: %v", err)
	}
	return s
}

// Главное: вход переживает перезапуск панели. Без этого каждое обновление
// заканчивалось вторым окном «Сессия сброшена» поверх хода обновления
// (amnezia-vpn-server-4aab).
func TestSessionSurvivesRestart(t *testing.T) {
	p := newMemPersister()
	before := restart(t, p)
	sess, err := before.Create("admin")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	after := restart(t, p)
	got, reason, ok := after.Lookup(sess.ID)
	if !ok {
		t.Fatalf("после перезапуска сессия потеряна: reason=%q", reason)
	}
	if got.Username != "admin" || got.CSRFToken != sess.CSRFToken {
		t.Errorf("сессия после перезапуска = %+v, ждали admin с тем же CSRF", got)
	}
	// Веб-слой пишет sess.ID обратно в куку: пустой id разлогинил бы
	// человека первым же сохранением.
	if got.ID != sess.ID {
		t.Errorf("Lookup вернул id %q, ждали тот, по которому искали", got.ID)
	}
	touched, ok := after.Touch(sess.ID)
	if !ok || touched.ID != sess.ID {
		t.Errorf("Touch после перезапуска: ok=%v id=%q", ok, touched.ID)
	}
}

// Прочитав хранилище, войти нельзя: там хеш, а не значение куки.
func TestPersistedSessionCarriesNoCookieValue(t *testing.T) {
	p := newMemPersister()
	s := restart(t, p)
	sess, err := s.Create("admin")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(p.rows) != 1 {
		t.Fatalf("строк в хранилище %d, ждали 1", len(p.rows))
	}
	for key, row := range p.rows {
		if key == sess.ID || row.IDHash == sess.ID {
			t.Fatal("в хранилище лежит само значение куки")
		}
		if strings.Contains(row.Username+row.CSRFToken, sess.ID) {
			t.Fatal("значение куки просочилось в сохранённые поля")
		}
		if key != hashID(sess.ID) {
			t.Errorf("ключ %q — не хеш id", key)
		}
	}
}

// Выход, повторный вход, поворот id и смена пароля обязаны доходить до
// хранилища, иначе после перезапуска отозванная сессия воскресла бы.
func TestRevokedSessionsStayRevokedAfterRestart(t *testing.T) {
	cases := []struct {
		name   string
		revoke func(s *SessionStore, id string)
	}{
		{"выход", func(s *SessionStore, id string) { s.Delete(id) }},
		{"смена пароля извне", func(s *SessionStore, _ string) { s.ForgetByUsername("admin") }},
		{"вход с другого устройства", func(s *SessionStore, _ string) {
			other, err := s.Create("admin")
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			s.DeleteByUsername("admin", other.ID)
		}},
		{"восстановление из копии", func(s *SessionStore, _ string) { s.DeleteAll() }},
		{"поворот id при входе", func(s *SessionStore, id string) {
			if _, err := s.Rotate(id, "admin"); err != nil {
				t.Fatalf("Rotate: %v", err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newMemPersister()
			s := restart(t, p)
			sess, err := s.Create("admin")
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			tc.revoke(s, sess.ID)

			after := restart(t, p)
			if _, _, ok := after.Lookup(sess.ID); ok {
				t.Error("отозванная сессия ожила после перезапуска")
			}
		})
	}
}

// Истёкшая сессия после перезапуска не оживает.
func TestExpiredSessionIsNotLoaded(t *testing.T) {
	p := newMemPersister()
	s := NewSessionStore(20 * time.Millisecond)
	if err := s.SetPersister(p, nil); err != nil {
		t.Fatalf("SetPersister: %v", err)
	}
	sess, err := s.Create("admin")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	time.Sleep(40 * time.Millisecond)

	after := restart(t, p)
	if _, _, ok := after.Lookup(sess.ID); ok {
		t.Error("истёкшая сессия ожила после перезапуска")
	}
}
