package store

import (
	"context"
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
}

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
	result.Ready = result.IncompleteCollections == 0 && result.UncoveredWorkspaces == 0
	return nil
}

// durationSeconds keeps reports readable.
func durationSeconds(d time.Duration) float64 { return d.Round(time.Millisecond).Seconds() }
