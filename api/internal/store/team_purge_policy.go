package store

import (
	"context"
	"strings"
	"sync"
	"time"
	"unsafe"

	"flow/api/internal/domain"
)

const scaleP21SW20TeamPurgeMaxWork = 64 << 20

// ExpiredTeamPolicyRead owns the ordered due IDs and their work allowance.
// Context carries that allowance into sequential downstream processing; a
// downstream reader must account for its own inputs within that allowance.
type ExpiredTeamPolicyRead struct {
	ctx     context.Context
	ids     []string
	release func()
	once    sync.Once
}

func (r *ExpiredTeamPolicyRead) IDs() []string            { return r.ids }
func (r *ExpiredTeamPolicyRead) Context() context.Context { return r.ctx }
func (r *ExpiredTeamPolicyRead) Close() {
	if r != nil {
		r.once.Do(r.release)
	}
}

// BeginExpiredTeamPolicyRead snapshots only IDs passing the legacy restore
// window predicate. Resident Team array order and exact Go workspace lookup
// semantics are retained; no SQL source or team settings enter this selection.
func (s *SQLiteStore) BeginExpiredTeamPolicyRead(ctx context.Context, workspace string, now time.Time, window time.Duration) (*ExpiredTeamPolicyRead, bool, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		s.mu.RLock()
		if workspace == "" {
			workspace = s.lastWorkspaceKey
		}
		_, found := s.workspaces[workspace]
		cost, _, err := scaleP21SW20TeamPurgeCost(ctx, s.workspaces[workspace].Teams, now, window)
		s.mu.RUnlock()
		if !found {
			return nil, false, nil
		}
		if err != nil {
			return nil, true, err
		}
		var admitted context.Context
		var release func()
		parent, nested := ctx.Value(metadataWorkLeaseKey{}).(*metadataWorkLease)
		if nested && s.metadataResidency != nil && parent.budget == s.metadataResidency.work {
			// The owned result can outlive the incoming lease.
			admitted, release, err = s.tryAdmitAdditionalMetadataWork(ctx, cost)
		} else {
			admitted, release, err = s.admitMetadataWork(ctx, cost)
		}
		if err != nil {
			return nil, true, err
		}
		s.mu.RLock()
		source, found := s.workspaces[workspace]
		current, currentCount, err := scaleP21SW20TeamPurgeCost(admitted, source.Teams, now, window)
		if !found || err != nil || current > cost {
			s.mu.RUnlock()
			release()
			if !found || err != nil {
				return nil, found, err
			}
			continue
		}
		ids := make([]string, 0, currentCount)
		for _, team := range source.Teams {
			if team.ArchivedAt != nil && !now.Before(team.ArchivedAt.Add(window)) {
				ids = append(ids, strings.Clone(team.ID))
			}
		}
		s.mu.RUnlock()
		if err := admitted.Err(); err != nil {
			release()
			return nil, true, err
		}
		return &ExpiredTeamPolicyRead{ctx: admitted, ids: ids, release: release}, true, nil
	}
}

func scaleP21SW20TeamPurgeCost(ctx context.Context, teams []domain.Team, now time.Time, window time.Duration) (int, int, error) {
	cost := int64(2<<20) + int64(unsafe.Sizeof(ExpiredTeamPolicyRead{}))
	count := 0
	for _, team := range teams {
		if err := ctx.Err(); err != nil {
			return 0, 0, err
		}
		if team.ArchivedAt == nil || now.Before(team.ArchivedAt.Add(window)) {
			continue
		}
		count++
		cost += int64(len(team.ID)) + int64(unsafe.Sizeof("")) + 64
		if cost > scaleP21SW20TeamPurgeMaxWork {
			return 0, 0, ErrMetadataWorkBudget
		}
	}
	return int(cost), count, nil
}
