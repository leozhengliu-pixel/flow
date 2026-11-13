package store

import (
	"context"
	"fmt"
	"slices"

	"flow/api/internal/domain"
)

func (s *SQLiteStore) residentRecordBodyColumn() string {
	if s.metadataResidency == nil {
		return "data"
	}
	return `CASE WHEN LENGTH(data)<=262144 THEN data ELSE NULL END`
}

func (s *SQLiteStore) validateResidentRecordBody(raw []byte) error {
	if s.metadataResidency == nil {
		return nil
	}
	if raw == nil {
		return ErrMetadataPageBudget
	}
	budget := newMetadataHydrationBudget()
	return budget.consume(raw)
}

// Native policy consumers need no body collection or complete-copy stamp.
// ResolveAccess already holds a read transaction and a reentrant work lease.
func (s *SQLiteStore) readResidentResourcePolicy(ctx context.Context, workspace, workspaceID string) (domain.Bootstrap, error) {
	stamp, err := readMetadataStamp(ctx, s.db, workspace)
	if err != nil {
		return domain.Bootstrap{}, err
	}
	if stamp.workspaceID != workspaceID {
		return domain.Bootstrap{}, ErrResourceAccessChanged
	}
	if s.metadataResidency.requireCoverage {
		if err := checkRecordJournalCoverage(ctx, s.db, workspace); err != nil {
			return domain.Bootstrap{}, err
		}
	}
	data := domain.Bootstrap{Workspace: domain.Workspace{ID: workspaceID, URLKey: workspace}}
	budget := newMetadataHydrationBudget()
	order := map[string]int{}
	for _, field := range []string{"teams", "teamSettings"} {
		err := s.scanResidentMetadataField(ctx, s.db, workspace, field, stamp, func(rows []metadataCacheRow) error {
			for _, row := range rows {
				if err := budget.consume(row.Data); err != nil {
					return err
				}
				shape := "map"
				if field == "teams" {
					shape = "array"
					order[row.Key] = row.Order
				}
				if err := appendResidentMetadataRow(&data, row, shape); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return domain.Bootstrap{}, fmt.Errorf("resident policy %s: %w", field, err)
		}
	}
	slices.SortStableFunc(data.Teams, func(a, b domain.Team) int {
		if order[a.ID] < order[b.ID] {
			return -1
		}
		if order[a.ID] > order[b.ID] {
			return 1
		}
		return compareResidentRecordKeys(RecordKey{ID: a.ID}, RecordKey{ID: b.ID})
	})
	return resourcePolicyMetadata(data), nil
}
