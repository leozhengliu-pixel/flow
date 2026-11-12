package store

import (
	"context"
	"encoding/json"
	"time"

	"flow/api/internal/domain"
)

// LiveEventContext keeps a request's older page snapshot out of shared
// delivery proofs and source caches. A sole held connection cannot confirm
// live authority, so that case must stop instead of waiting on itself.
func (s *SQLiteStore) LiveEventContext(ctx context.Context) (context.Context, error) {
	if snapshot, ok := ctx.Value(workspaceReadKey{}).(workspaceRead); ok && snapshot.database == s.db {
		if s.db.Stats().MaxOpenConnections == 1 {
			return ctx, ErrRecordJournalUnavailable
		}
		return context.WithValue(ctx, workspaceReadKey{}, workspaceRead{}), nil
	}
	return ctx, nil
}

// CheckCredential also handles expiry without an epoch change. Revocation and
// scope edits advance AuthzEpoch and must still be checked at delivery.
func (a EventAccess) CheckCredential() error {
	if a.policy == nil || a.checkBinding(a.policy.store) != nil || a.expiresAt != nil && !a.expiresAt.After(time.Now()) {
		return ErrAuthForbidden
	}
	return nil
}

// ForSource rebinds immutable principal policy without SQL. Its epoch must
// still be validated against live authority before delivering a projection.
func (a EventAccess) ForSource(source *EventPolicySource, epoch string) (EventAccess, error) {
	if err := a.CheckCredential(); err != nil {
		return EventAccess{}, err
	}
	if source == nil || source.store != a.policy.store || source.workspace != a.policy.workspace || source.version.WorkspaceID != a.WorkspaceID || epoch != a.AuthzEpoch {
		return EventAccess{}, ErrAuthForbidden
	}
	a.version, a.source = source.version, source
	return a, nil
}

// CacheBytes accounts for principal-owned state. The cache must additionally
// retain and account for the immutable EventPolicySource once per source.
func (a EventAccess) CacheBytes() int {
	if a.policy == nil {
		return 0
	}
	p := a.policy
	raw, _ := json.Marshal(struct {
		Viewer     domain.User
		Members    []domain.TeamMember
		Credential *domain.APIKey
		Teams      bootstrapTeamScope
		Settings   map[string]bool
	}{p.metadata.Viewer, p.metadata.TeamMembers, p.key, p.teams, p.settingsTeams})
	return len(raw)*3 + 2048
}
