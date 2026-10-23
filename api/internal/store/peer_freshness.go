package store

import (
	"context"
	"time"

	"flow/api/internal/domain"
)

// With several servers on one database (a WorkspaceCoordinator is set), each
// server's in-memory copy of a workspace learns of another server's change
// from its peer event, asynchronously. These helpers read the database for
// callers that must not act on a copy that is missing or behind.

// SetAccountAccessSink registers a callback for account-level access changes
// (sessions revoked, an account deactivated): open realtime connections
// authenticated by them must be re-checked.
func (s *SQLiteStore) SetAccountAccessSink(sink func(userID string)) {
	if sink == nil {
		s.accountSink.Store(nil)
		return
	}
	s.accountSink.Store(&sink)
}

func (s *SQLiteStore) accountAccessChanged(userID string) {
	if sink := s.accountSink.Load(); sink != nil && userID != "" {
		(*sink)(userID)
	}
}

// SessionValid reports, without touching it, whether a session token is
// still signed in as userID (not revoked or expired, the account active).
func (s *SQLiteStore) SessionValid(ctx context.Context, token, userID string) bool {
	var owner, expiresRaw string
	if err := s.db.QueryRowContext(ctx, `SELECT user_id,expires_at FROM auth_sessions WHERE token_hash=?`, tokenHash(token)).Scan(&owner, &expiresRaw); err != nil || owner != userID {
		return false
	}
	expires, err := time.Parse(time.RFC3339Nano, expiresRaw)
	if err != nil || time.Now().After(expires) {
		return false
	}
	user, err := s.authUserByID(ctx, userID)
	return err == nil && user.Active
}

// HasWorkspace reports whether this instance holds the workspace in memory.
func (s *SQLiteStore) HasWorkspace(workspaceKey string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.workspaces[workspaceKey]
	return ok
}

// LoadMissingWorkspaces loads every workspace another server created that
// this instance has not loaded yet.
func (s *SQLiteStore) LoadMissingWorkspaces(ctx context.Context) error {
	if s.coordinator == nil {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT workspace_key FROM workspace_states`)
	if err != nil {
		return err
	}
	var missing []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return err
		}
		if !s.HasWorkspace(key) {
			missing = append(missing, key)
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, key := range missing {
		if err := s.ReloadWorkspace(ctx, key); err != nil {
			return err
		}
	}
	return nil
}

// currentWorkspaceByID is workspaceByID read from the database when servers
// share it, for flows that name a workspace only by id (an invitation token):
// the workspace may be new, or its teams changed, on another server.
func (s *SQLiteStore) currentWorkspaceByID(ctx context.Context, id string) (domain.Bootstrap, string, bool) {
	if s.coordinator != nil && id != "" {
		var key string
		if err := s.db.QueryRowContext(ctx, `SELECT workspace_key FROM workspace_states WHERE workspace_id=?`, id).Scan(&key); err == nil {
			_ = s.ReloadWorkspace(ctx, key)
		}
	}
	return s.workspaceByID(id)
}
