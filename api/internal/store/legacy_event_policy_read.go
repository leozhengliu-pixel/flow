package store

import (
	"context"

	"flow/api/internal/domain"
)

// readLegacyEventPolicyRecords constructs trusted event policy. workspaceID
// labels the retained source; live identity and authority are checked later.
// This literal SQL fallback neither includes root-inline Teams nor synthesizes
// TeamSettings defaults from the installed native policy.
func (s *SQLiteStore) readLegacyEventPolicyRecords(ctx context.Context, workspace, workspaceID string) (domain.Bootstrap, error) {
	if s == nil || !s.BoundedMetadataEnabled() {
		return domain.Bootstrap{}, ErrBoundedMetadataCoverage
	}
	ctx, release, err := s.tryAdmitAdditionalMetadataWork(ctx, 2<<20)
	if err != nil {
		return domain.Bootstrap{}, err
	}
	defer release()
	var tx *sqlTx
	if snapshot, ok := ctx.Value(workspaceReadKey{}).(workspaceRead); ok && snapshot.database == s.db {
		if snapshot.reader == nil {
			return domain.Bootstrap{}, ErrMetadataRecordQuery
		}
		// The legacy database wrapper binds a snapshot by database, while
		// the explicit workspace argument selects its namespace.
		tx = snapshot.reader
	} else {
		tx, err = s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
		if err != nil {
			return domain.Bootstrap{}, err
		}
		defer tx.Rollback()
		ctx = context.WithValue(ctx, workspaceReadKey{}, workspaceRead{database: s.db, workspace: workspace, reader: tx})
	}
	data := domain.Bootstrap{
		Workspace:    domain.Workspace{ID: workspaceID, URLKey: workspace},
		TeamSettings: map[string]domain.TeamSettings{},
	}
	budget := newMetadataHydrationBudget()
	teams, err := s.scaleP21SW23ReadTeamPolicyRows(ctx, tx, workspace)
	if err != nil {
		return data, err
	}
	defer teams.Close()
	for _, row := range teams.rows {
		if err := budget.consume(row.Data); err != nil {
			return data, err
		}
		if err := appendResidentMetadataRow(&data, row, "array"); err != nil {
			return data, err
		}
	}
	err = s.scanResourcePolicyHeaders(ctx, workspace, "teamSettings", func(rows []metadataCacheRow) error {
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
		return data, err
	}
	return resourcePolicyMetadata(data), nil
}
