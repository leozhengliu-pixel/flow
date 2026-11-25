package store

import (
	"context"
	"reflect"

	"flow/api/internal/domain"
)

// KeepIssueResults owns already-materialized native response values through
// ctx's output lifetime. A transactional producer must call it before commit.
func (s *SQLiteStore) KeepIssueResults(ctx context.Context, issues []domain.Issue, affected []domain.IssueVersion) error {
	if !s.BoundedMetadataEnabled() || !metadataOutputOwnerPresent(ctx) {
		return ErrBoundedMetadataCoverage
	}
	cost, err := metadataAggregateNativeCost(ctx, reflect.ValueOf(issues), 0)
	if err != nil {
		return err
	}
	var extra uint64
	if len(issues) > 0 {
		// The handler attaches this sidecar only after optional triage.
		// Meter it on a detached header without changing that timing.
		head := issues[0]
		head.AffectedIssues = affected
		extra, err = metadataAggregateNativeCost(ctx, reflect.ValueOf(head.AffectedIssues), 0)
		if err != nil {
			return err
		}
	}
	if cost > metadataWorkBytes-4096 || extra > metadataWorkBytes-4096-cost {
		return ErrMetadataWorkBudget
	}
	owner := &ResourceRead{store: s}
	if err := owner.reserve(ctx, cost+extra+4096); err != nil {
		owner.Close()
		return err
	}
	owner.Data.Issues = issues
	if !registerMetadataOutputClose(ctx, owner.Close) {
		owner.Close()
		return ErrBoundedMetadataCoverage
	}
	return nil
}
