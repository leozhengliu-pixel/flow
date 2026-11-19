package store

import (
	"context"
	"fmt"
	"time"
)

// BoundedMetadataPreparation reports PrepareBoundedMetadata: the journal
// repair and catalog passes it ran and what a bounded start would still
// refuse afterwards.
type BoundedMetadataPreparation struct {
	Journal RecordJournalRepairReport `json:"journal"`
	Catalog MetadataCatalogPassReport `json:"catalog"`
	// JournalSeconds and CatalogSeconds are the passes' wall times.
	JournalSeconds float64 `json:"journalSeconds"`
	CatalogSeconds float64 `json:"catalogSeconds"`
	// UncoveredWorkspaces counts workspaces whose journal writer coverage
	// is still incomplete; IncompleteCollections the collections whose
	// catalog is not complete. Ready is true when both are zero.
	UncoveredWorkspaces   int  `json:"uncoveredWorkspaces"`
	IncompleteCollections int  `json:"incompleteCollections"`
	Ready                 bool `json:"ready"`
	// Uncovered details the first uncoveredReportLimit uncovered
	// workspace keys.
	Uncovered []RecordJournalUncovered `json:"uncovered"`
}

// RecordJournalUncovered is a workspace key whose journal coverage a bounded
// start would still refuse after the preparation.
type RecordJournalUncovered struct {
	Workspace string `json:"workspace"`
	// WorkspaceExists is false for a key no workspace owns (the next
	// repair pass discards its keys).
	WorkspaceExists bool `json:"workspaceExists"`
	// ActivationWalk is true while the walk over the workspace's records
	// after the journal was enabled is unfinished.
	ActivationWalk bool `json:"activationWalk"`
	// Pending counts the keys old writers changed, by journal scope (field;
	// "@authz" for memberships and permissions, "@core" for the root) and
	// source table.
	Pending []RecordJournalPendingScope `json:"pending"`
}

type RecordJournalPendingScope struct {
	Field  string `json:"field"`
	Source string `json:"source"`
	Keys   int    `json:"keys"`
}

const uncoveredReportLimit = 20

// PrepareBoundedMetadata runs, once and to the end, the maintenance a
// bounded (FLOW_BOUNDED_METADATA=on) start needs: the record journal repair
// over every workspace that still needs it, then the metadata catalog over
// every collection, both at full speed in the same bounded, checkpointed
// transactions the FLOW_METADATA_CATALOG=shadow worker uses. It is safe on
// a database that flag-off replicas are serving, idempotent, and resumable
// after an interruption. Rows it cannot catalog are reported, not fatal.
func (s *SQLiteStore) PrepareBoundedMetadata(ctx context.Context) (BoundedMetadataPreparation, error) {
	var result BoundedMetadataPreparation
	journal, err := s.RepairRecordJournalPass(ctx)
	result.Journal, result.JournalSeconds = journal, durationSeconds(journal.Duration)
	if err != nil {
		return result, err
	}
	catalog, err := s.metadataCatalogPass(ctx, false)
	result.Catalog, result.CatalogSeconds = catalog, durationSeconds(catalog.Duration)
	if err != nil {
		return result, err
	}
	return result, s.boundedMetadataReadiness(ctx, &result)
}

func (s *SQLiteStore) boundedMetadataReadiness(ctx context.Context, result *BoundedMetadataPreparation) error {
	catalogs, err := s.MetadataCatalogCompleteness(ctx)
	if err != nil {
		return err
	}
	result.IncompleteCollections = 0
	for _, catalog := range catalogs {
		result.IncompleteCollections += catalog.Incomplete
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM (SELECT workspace_key FROM record_journal_coverage UNION SELECT workspace_key FROM record_journal_dirty) uncovered`).Scan(&result.UncoveredWorkspaces); err != nil {
		return err
	}
	result.Uncovered = []RecordJournalUncovered{}
	if result.UncoveredWorkspaces > 0 {
		keys, err := queryStrings(ctx, s.db, `SELECT workspace_key FROM record_journal_coverage UNION SELECT workspace_key FROM record_journal_dirty ORDER BY 1 LIMIT `+fmt.Sprint(uncoveredReportLimit))
		if err != nil {
			return err
		}
		for _, key := range keys {
			gap, err := s.recordJournalUncovered(ctx, key)
			if err != nil {
				return err
			}
			result.Uncovered = append(result.Uncovered, gap)
		}
	}
	result.Ready = result.IncompleteCollections == 0 && result.UncoveredWorkspaces == 0
	return nil
}

func (s *SQLiteStore) recordJournalUncovered(ctx context.Context, workspace string) (RecordJournalUncovered, error) {
	gap := RecordJournalUncovered{Workspace: workspace, Pending: []RecordJournalPendingScope{}}
	var exists, walk int
	if err := s.db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM workspace_states WHERE workspace_key=?),(SELECT COUNT(*) FROM record_journal_coverage WHERE workspace_key=?)`, workspace, workspace).Scan(&exists, &walk); err != nil {
		return gap, err
	}
	gap.WorkspaceExists, gap.ActivationWalk = exists != 0, walk != 0
	rows, err := s.db.QueryContext(ctx, `SELECT field,source,COUNT(*) FROM record_journal_dirty WHERE workspace_key=? GROUP BY field,source ORDER BY field,source LIMIT `+fmt.Sprint(uncoveredReportLimit), workspace)
	if err != nil {
		return gap, err
	}
	defer rows.Close()
	for rows.Next() {
		var scope RecordJournalPendingScope
		if err := rows.Scan(&scope.Field, &scope.Source, &scope.Keys); err != nil {
			return gap, err
		}
		gap.Pending = append(gap.Pending, scope)
	}
	return gap, rows.Err()
}

// durationSeconds keeps reports readable.
func durationSeconds(d time.Duration) float64 { return d.Round(time.Millisecond).Seconds() }
