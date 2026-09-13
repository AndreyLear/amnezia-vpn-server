package web

import (
	"database/sql"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/auth"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

// dbSessionPersister stores login sessions in SQLite so they survive a
// panel restart (amnezia-vpn-server-4aab).
//
// It takes a function returning the handle rather than the handle itself:
// an in-process restore swaps the database under the running server
// (swapDB), and a persister holding the old handle would keep writing
// sessions into a file that is no longer the panel's database.
type dbSessionPersister struct {
	db func() *sql.DB
}

func (p dbSessionPersister) Save(s auth.PersistedSession) error {
	return db.SaveSession(p.db(), db.SessionRecord{
		IDHash:    s.IDHash,
		Username:  s.Username,
		CSRFToken: s.CSRFToken,
		CreatedAt: s.CreatedAt,
		ExpiresAt: s.ExpiresAt,
	})
}

func (p dbSessionPersister) Delete(idHash string) error { return db.DeleteSession(p.db(), idHash) }

func (p dbSessionPersister) DeleteAll() error { return db.DeleteAllSessions(p.db()) }

func (p dbSessionPersister) LoadLive(now time.Time) ([]auth.PersistedSession, error) {
	recs, err := db.LoadLiveSessions(p.db(), now)
	if err != nil {
		return nil, err
	}
	out := make([]auth.PersistedSession, 0, len(recs))
	for _, r := range recs {
		out = append(out, auth.PersistedSession{
			IDHash:    r.IDHash,
			Username:  r.Username,
			CSRFToken: r.CSRFToken,
			CreatedAt: r.CreatedAt,
			ExpiresAt: r.ExpiresAt,
		})
	}
	return out, nil
}
