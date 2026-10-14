package store

import (
	"context"
	"fmt"
	"sync"
	"time"

	"flow/api/internal/domain"
)

// Account email events are a small per-account ledger for the emails Flow sends
// outside a workspace: the welcome email, onboarding follow-ups, the devices a
// person has signed in from (for "New login" alerts) and opt-outs. A row is a
// fact that happened once; RecordAccountEmailEvent reports whether it is new.
//
// The table is created on first use so the ledger stays independent of the
// workspace schema.
var accountEmailEventsSchema sync.Map // *SQLiteStore -> *accountEmailEventsGuard

type accountEmailEventsGuard struct {
	mu    sync.Mutex
	ready bool
}

func (s *SQLiteStore) ensureAccountEmailEvents(ctx context.Context) error {
	value, _ := accountEmailEventsSchema.LoadOrStore(s, &accountEmailEventsGuard{})
	guard := value.(*accountEmailEventsGuard)
	guard.mu.Lock()
	defer guard.mu.Unlock()
	if guard.ready {
		return nil
	}
	idType := "TEXT"
	if s.dialect == "mysql" {
		idType = "VARCHAR(191)"
	}
	statement := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS auth_email_events (user_id %s NOT NULL REFERENCES auth_users(id) ON DELETE CASCADE, kind VARCHAR(64) NOT NULL, item_key VARCHAR(64) NOT NULL, created_at VARCHAR(40) NOT NULL, PRIMARY KEY(user_id,kind,item_key))`, idType)
	if _, err := s.db.ExecContext(ctx, statement); err != nil {
		return err
	}
	guard.ready = true
	return nil
}

// RecordAccountEmailEvent stores (userID, kind, key) once and reports whether
// this call stored it. key is an opaque value of at most 64 bytes ("" for
// once-per-account events).
func (s *SQLiteStore) RecordAccountEmailEvent(ctx context.Context, userID, kind, key string, at time.Time) (bool, error) {
	if err := s.ensureAccountEmailEvents(ctx); err != nil {
		return false, err
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO auth_email_events(user_id,kind,item_key,created_at) VALUES(?,?,?,?) ON CONFLICT DO NOTHING`, userID, kind, key, at.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected > 0, err
}

// HasAccountEmailEvent reports whether the account has any event of kind.
func (s *SQLiteStore) HasAccountEmailEvent(ctx context.Context, userID, kind string) (bool, error) {
	if err := s.ensureAccountEmailEvents(ctx); err != nil {
		return false, err
	}
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_email_events WHERE user_id=? AND kind=?`, userID, kind).Scan(&count)
	return count > 0, err
}

// AccountCreatedAt is when the account was created.
func (s *SQLiteStore) AccountCreatedAt(ctx context.Context, userID string) (time.Time, error) {
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT created_at FROM auth_users WHERE id=?`, userID).Scan(&raw); err != nil {
		return time.Time{}, err
	}
	return time.Parse(time.RFC3339Nano, raw)
}

// AccountsDueFollowUp lists active accounts with an `after` event recorded at
// or before cutoff and no `kind` or `skip` event yet, oldest first.
func (s *SQLiteStore) AccountsDueFollowUp(ctx context.Context, kind, after, skip string, cutoff time.Time, limit int) ([]domain.User, error) {
	if err := s.ensureAccountEmailEvents(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT u.id,u.email,u.name,u.display_name FROM auth_email_events e JOIN auth_users u ON u.id=e.user_id
		WHERE e.kind=? AND e.created_at<=? AND u.active=1 AND u.email IS NOT NULL AND u.email<>''
		AND NOT EXISTS (SELECT 1 FROM auth_email_events d WHERE d.user_id=e.user_id AND (d.kind=? OR d.kind=?))
		ORDER BY e.created_at LIMIT ?`, after, cutoff.UTC().Format(time.RFC3339Nano), kind, skip, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := []domain.User{}
	for rows.Next() {
		var user domain.User
		if err := rows.Scan(&user.ID, &user.Email, &user.Name, &user.DisplayName); err != nil {
			return nil, err
		}
		user.Active = true
		users = append(users, user)
	}
	return users, rows.Err()
}
