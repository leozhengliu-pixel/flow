package store

import (
	"context"
	"sync"
)

// tryAdmitAdditionalMetadataWork reserves an independent child allowance without
// waiting while a caller owns a SQL snapshot. The parent's allowance is neither
// reused nor enlarged; both remain charged until their separate releases.
func (s *SQLiteStore) tryAdmitAdditionalMetadataWork(ctx context.Context, bytes int) (context.Context, func(), error) {
	if err := ctx.Err(); err != nil {
		return ctx, nil, err
	}
	if s.metadataResidency == nil {
		return ctx, func() {}, nil
	}
	budget := s.metadataResidency.work
	budget.mu.Lock()
	defer budget.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return ctx, nil, err
	}
	if parent, ok := ctx.Value(metadataWorkLeaseKey{}).(*metadataWorkLease); ok && parent.budget == budget && !parent.live {
		return ctx, nil, ErrMetadataWorkBudget
	}
	if bytes <= 0 || bytes > budget.limit-budget.used {
		return ctx, nil, ErrMetadataWorkBudget
	}
	lease := &metadataWorkLease{budget: budget, bytes: bytes, live: true}
	budget.used += bytes
	var once sync.Once
	release := func() {
		once.Do(func() {
			budget.mu.Lock()
			lease.live = false
			budget.used -= bytes
			close(budget.changed)
			budget.changed = make(chan struct{})
			budget.mu.Unlock()
		})
	}
	return context.WithValue(ctx, metadataWorkLeaseKey{}, lease), release, nil
}
