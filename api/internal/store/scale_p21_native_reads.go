package store

import (
	"context"
	"fmt"

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
	ctx, release, err := s.tryAdmitAdditionalMetadataWork(ctx, 2<<20)
	if err != nil {
		return domain.Bootstrap{}, err
	}
	defer release()
	// Header projection and the legacy settings expressions share one
	// repeatable snapshot, including trusted direct callers without one.
	var tx *sqlTx
	if snapshot, ok := ctx.Value(workspaceReadKey{}).(workspaceRead); ok && snapshot.database == s.db {
		if snapshot.reader == nil {
			return domain.Bootstrap{}, ErrMetadataRecordQuery
		}
		tx = snapshot.reader
	} else {
		tx, err = s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
		if err != nil {
			return domain.Bootstrap{}, err
		}
		defer tx.Rollback()
		ctx = context.WithValue(ctx, workspaceReadKey{}, workspaceRead{database: s.db, workspace: workspace, reader: tx})
	}
	stamp, err := readMetadataStamp(ctx, tx, workspace)
	if err != nil {
		return domain.Bootstrap{}, err
	}
	if stamp.workspaceID != workspaceID {
		return domain.Bootstrap{}, ErrResourceAccessChanged
	}
	if s.metadataResidency.requireCoverage {
		// The database wrapper routes to tx but preserves the legacy coverage
		// reader's dirty-token rules; passing tx changes that contract.
		if err := checkRecordJournalCoverage(ctx, s.db, workspace); err != nil {
			return domain.Bootstrap{}, err
		}
	}
	data := domain.Bootstrap{Workspace: domain.Workspace{ID: workspaceID, URLKey: workspace}}
	budget := newMetadataHydrationBudget()
	teamRows, err := s.scaleP21SW23ReadTeamPolicyRows(ctx, tx, workspace)
	if err != nil {
		return domain.Bootstrap{}, fmt.Errorf("resident policy teams: %w", err)
	}
	defer teamRows.Close()
	for _, row := range teamRows.rows {
		if err := budget.consume(row.Data); err != nil {
			return domain.Bootstrap{}, err
		}
		if err := appendResidentMetadataRow(&data, row, "array"); err != nil {
			return domain.Bootstrap{}, err
		}
	}
	for _, field := range []string{"teamSettings"} {
		err := s.scanResourcePolicyHeaders(ctx, workspace, field, func(rows []metadataCacheRow) error {
			for _, row := range rows {
				if err := budget.consume(row.Data); err != nil {
					return err
				}
				if err := appendResidentMetadataRow(&data, row, "map"); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return domain.Bootstrap{}, fmt.Errorf("resident policy %s: %w", field, err)
		}
	}
	return resourcePolicyMetadata(data), nil
}
