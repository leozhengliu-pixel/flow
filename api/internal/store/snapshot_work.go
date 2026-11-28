package store

import "context"

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
	admission := budget.recorder.BeginAdmission(ctx)
	defer admission.End()
	budget.mu.Lock()
	defer budget.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return ctx, nil, err
	}
	if parent, ok := ctx.Value(metadataWorkLeaseKey{}).(*metadataWorkLease); ok && parent.budget == budget && !parent.live {
		admission.Refused(false)
		return ctx, nil, ErrMetadataWorkBudget
	}
	demand := metadataWorkDemandFrom(ctx)
	if bytes <= 0 || bytes > budget.limit-budget.used {
		if bytes > 0 && demand != nil && demand.fits(bytes, budget.limit) {
			// The attempt would fit the budget: other work holds the room
			// this admission needs.
			demand.refuse(bytes)
			admission.Refused(true)
			return ctx, nil, errMetadataWorkContended
		}
		admission.Refused(false)
		return ctx, nil, ErrMetadataWorkBudget
	}
	lease := &metadataWorkLease{budget: budget, bytes: bytes, live: true}
	demand.charge(bytes)
	budget.used += bytes
	budget.peak = max(budget.peak, budget.used)
	admission.Charge(int64(bytes))
	return context.WithValue(ctx, metadataWorkLeaseKey{}, lease), lease.releaser(demand, admission), nil
}
