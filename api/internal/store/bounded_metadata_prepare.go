package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
)

func metadataCatalogAutoEnabled(bounded bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("FLOW_METADATA_CATALOG"))) {
	case "on":
		return true
	case "":
		return bounded
	default:
		return false
	}
}

func (s *SQLiteStore) loadStartupMetadata(ctx context.Context, bounded, prepare bool) error {
	// The old singleton root and the test fixture still need the existing
	// seeding path before the bounded loader can read workspace_states.
	if bounded && prepare {
		var populated, legacy int
		if err := s.db.QueryRowContext(ctx, `SELECT
			CASE WHEN EXISTS(SELECT 1 FROM workspace_states) THEN 1 ELSE 0 END,
			CASE WHEN EXISTS(SELECT 1 FROM workspace_state WHERE id=1) THEN 1 ELSE 0 END`).Scan(&populated, &legacy); err != nil {
			return err
		}
		if populated == 0 && (legacy != 0 || strings.EqualFold(s.fixtureProfile, "test")) {
			if err := s.loadOrSeed(ctx); err != nil {
				return err
			}
			s.workspaces = nil
		}
	}
	if prepare {
		ready, err := s.prepareStartupMetadata(ctx)
		if err != nil || !ready {
			boundedAdmissionFailure("startup metadata preparation incomplete; using legacy reads", err)
			bounded = false
		}
	}
	if bounded {
		// A legacy image may have been loaded for historical migrations.
		// Admission must still account for every workspace and its viewer.
		s.workspaces, s.copyRevisions = nil, nil
		s.metadataResidency = newMetadataResidency()
		err := s.loadOrSeed(ctx)
		if err == nil {
			applyMetadataMemoryPlan()
			return nil
		}
		if !prepare && strings.TrimSpace(os.Getenv("FLOW_BOUNDED_METADATA")) != "" {
			boundedAdmissionFailure("startup metadata: bounded workspace admission failed", err)
			return err
		}
		s.releaseMetadataLogicalSources()
		s.metadataResidency = nil
		boundedAdmissionFailure("startup metadata: bounded workspace admission failed; using legacy reads", err)
	}
	return s.loadOrSeed(ctx)
}

// Reload can encounter writes from a replica that was already serving when
// startup installed the journal triggers. They have durable dirty keys and
// record versions, but only a journaled transaction can update the catalog.
func (s *SQLiteStore) repairJournalBeforeReload(ctx context.Context, workspace string) error {
	if !s.metadataResidency.requireCoverage || s.startupLock == nil {
		return nil
	}
	pending := func(ctx context.Context) (string, error) {
		var id string
		err := s.db.QueryRowContext(ctx, `SELECT w.workspace_id FROM workspace_states w
			WHERE w.workspace_key=? AND EXISTS(SELECT 1 FROM record_journal_mode WHERE id=1 AND enabled=1)
			AND (EXISTS(SELECT 1 FROM record_journal_coverage WHERE workspace_key=w.workspace_key)
				OR EXISTS(SELECT 1 FROM record_journal_dirty WHERE workspace_key=w.workspace_key)
				OR EXISTS(SELECT 1 FROM record_journal_event_pending WHERE workspace_id=`+recordJournalIdentitySQL(s.dialect, "w.workspace_id")+`))`,
			workspace).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return id, err
	}
	if id, err := pending(ctx); err != nil || id == "" {
		return err
	}
	return s.WithStartupLock(ctx, "journal reload repair", func(ctx context.Context) error {
		id, err := pending(ctx)
		if err != nil || id == "" {
			return err
		}
		if err := s.repairRecordJournalWorkspace(ctx, id); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return ErrRecordJournalCoverage
		}
		return nil
	})
}

// prepareStartupMetadata runs under the schema startup lock. A completed
// checkpoint is maintained by journaled writes and must never be reset by
// a replica starting alongside an already serving bounded replica.
func (s *SQLiteStore) prepareStartupMetadata(ctx context.Context) (bool, error) {
	var report BoundedMetadataPreparation
	if !s.RecordJournalReady() {
		return false, ErrRecordJournalUnavailable
	}
	if err := s.boundedMetadataReadiness(ctx, &report); err != nil || report.Ready {
		return report.Ready, err
	}
	began := time.Now()
	journal, err := s.RepairRecordJournalPass(ctx)
	if err != nil {
		return false, err
	}
	catalog, err := s.metadataCatalogPass(ctx, catalogRepairOnly)
	if err != nil {
		return false, err
	}
	if err := s.boundedMetadataReadiness(ctx, &report); err != nil {
		return false, err
	}
	// A replica running the previous release can still write while this
	// replica prepares the catalog. Drain those captured keys before trying
	// to admit the workspace. Admission checks coverage in its own snapshot.
	if report.UncoveredWorkspaces > 0 && report.IncompleteCollections == 0 {
		catchup, err := s.RepairRecordJournalPass(ctx)
		if err != nil {
			return false, err
		}
		journal.Repaired += catchup.Repaired
		journal.Duration += catchup.Duration
		if err := s.boundedMetadataReadiness(ctx, &report); err != nil {
			return false, err
		}
	}
	slog.Info("startup metadata preparation", "ready", report.Ready,
		"workspaces_repaired", journal.Repaired, "workspaces_uncovered", report.UncoveredWorkspaces,
		"collections_completed", catalog.Completed, "collections_incomplete", report.IncompleteCollections,
		"journal_ms", journal.Duration.Milliseconds(), "catalog_ms", catalog.Duration.Milliseconds(),
		"duration_ms", time.Since(began).Milliseconds())
	return report.Ready, nil
}

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
	catalog, err := s.metadataCatalogPass(ctx, catalogRevisit)
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
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM (
		SELECT workspace_key FROM record_journal_coverage UNION SELECT workspace_key FROM record_journal_dirty
		UNION SELECT w.workspace_key FROM record_journal_event_pending p JOIN workspace_states w ON w.workspace_id=`+recordJournalIdentitySQL(s.dialect, "p.workspace_id")+`
	) uncovered`).Scan(&result.UncoveredWorkspaces); err != nil {
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
