package store

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"time"
)

// RecordJournalRepairReport summarises one RepairRecordJournalPass.
type RecordJournalRepairReport struct {
	// Workspaces is the number of workspaces that needed repair, Repaired
	// those whose journal coverage is complete after the pass.
	Workspaces int `json:"workspaces"`
	Repaired   int `json:"repaired"`
	// Incomplete lists the workspaces still to repair, with the reason.
	Incomplete []RecordJournalRepairGap `json:"incomplete"`
	Duration   time.Duration            `json:"-"`
}

// RecordJournalRepairGap is a workspace a repair pass did not finish.
type RecordJournalRepairGap struct {
	Workspace string `json:"workspace"`
	Error     string `json:"error"`
}

// recordJournalRepairBatches bounds the repair batches spent on one
// workspace in one pass, so a workspace that keeps changing cannot hold the
// pass; the next pass continues from the persisted cursor.
const recordJournalRepairBatches = 1 << 14

func (s *SQLiteStore) recordJournalRepairable() bool {
	return s.db.journalReady.Load() && recordJournalMode() != "off"
}

// RepairRecordJournalPass runs RepairRecordJournal over every workspace that
// still has old-writer coverage to repair: the bounded walk after the journal
// was enabled on existing data (record_journal_coverage), keys an old writer
// changed (record_journal_dirty) and the per-workspace event markers
// (record_journal_event_pending). Each batch is RepairRecordJournal's own
// bounded transaction with its cursor persisted, so the pass is idempotent
// and resumable, and safe next to live writers on a flag-off fleet: the only
// visible effect is the one workspace invalidation the repair publishes.
// A workspace that fails is logged and reported; the pass goes on.
func (s *SQLiteStore) RepairRecordJournalPass(ctx context.Context) (report RecordJournalRepairReport, err error) {
	report.Incomplete = []RecordJournalRepairGap{}
	began := time.Now()
	defer func() { report.Duration = time.Since(began) }()
	if !s.recordJournalRepairable() {
		return report, ErrRecordJournalUnavailable
	}
	// Old-writer keys and the walk after activation are keyed by workspace
	// key, the event markers by workspace ID. Each source is paged on its own
	// column (the sidecar columns are byte-ordered in every dialect), and a
	// workspace repaired from the first source has no markers left for the
	// second.
	for _, source := range []string{
		`SELECT workspace_key FROM record_journal_coverage WHERE workspace_key>? UNION SELECT workspace_key FROM record_journal_dirty WHERE workspace_key>? ORDER BY 1 LIMIT 128`,
		`SELECT DISTINCT workspace_id FROM record_journal_event_pending WHERE workspace_id>? ORDER BY 1 LIMIT 128`,
	} {
		byKey := strings.Contains(source, "workspace_key")
		after := ""
		for {
			args := []any{after}
			if byKey {
				args = append(args, after)
			}
			batch, err := queryStrings(ctx, s.db, source, args...)
			if err != nil {
				return report, err
			}
			if len(batch) == 0 {
				break
			}
			after = batch[len(batch)-1]
			for _, value := range batch {
				key, id := value, value
				lookup := `SELECT workspace_id FROM workspace_states WHERE workspace_key=?`
				if !byKey {
					lookup = `SELECT workspace_key FROM workspace_states WHERE workspace_id=?`
				}
				var found string
				err := s.db.QueryRowContext(ctx, lookup, value).Scan(&found)
				if errors.Is(err, sql.ErrNoRows) {
					// A deleted workspace: its sidecars go with the
					// catalog's orphan pruning, not with a repair.
					continue
				}
				if err != nil {
					return report, err
				}
				if byKey {
					id = found
				} else {
					key = found
				}
				report.Workspaces++
				if err := s.repairRecordJournalWorkspace(ctx, id); err != nil {
					if ctx.Err() != nil {
						return report, ctx.Err()
					}
					report.Incomplete = append(report.Incomplete, RecordJournalRepairGap{Workspace: key, Error: err.Error()})
					slog.Warn("record journal repair: workspace skipped", "workspace", key, "error", err.Error())
					continue
				}
				report.Repaired++
			}
		}
	}
	return report, nil
}

func queryStrings(ctx context.Context, db *sqlDatabase, query string, args ...any) ([]string, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []string{}
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *SQLiteStore) repairRecordJournalWorkspace(ctx context.Context, workspaceID string) error {
	for batch := 0; batch < recordJournalRepairBatches; batch++ {
		remaining, err := s.RepairRecordJournal(ctx, workspaceID)
		if err != nil {
			return err
		}
		if !remaining {
			return nil
		}
		// Let writers waiting for SQLite's single writer in between batches.
		if err := sleepContext(ctx, time.Millisecond); err != nil {
			return err
		}
	}
	return errors.New("repair not finished in this pass; the next pass continues")
}
