package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// RecordJournalRepairReport summarises one RepairRecordJournalPass.
type RecordJournalRepairReport struct {
	// Workspaces is the number of workspaces that needed repair, Repaired
	// those whose journal coverage is complete after the pass.
	Workspaces int `json:"workspaces"`
	Repaired   int `json:"repaired"`
	// Discarded counts workspace keys with no workspace behind them whose
	// leftover journal keys the pass removed: a workspace an older binary
	// deleted, or the empty key that memberships copied in ahead of their
	// workspaces (a bulk load of the auth tables) are journaled under. No
	// repair can publish them, and a workspace created later journals its
	// own root.
	Discarded int `json:"discarded"`
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
	return s.db.journalReady.Load() && recordJournalMode(s.dialect) != "off"
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
	// second. The first page starts at and includes the empty key.
	for _, source := range []string{
		`SELECT workspace_key FROM record_journal_coverage WHERE workspace_key>? UNION SELECT workspace_key FROM record_journal_dirty WHERE workspace_key>? ORDER BY 1 LIMIT 128`,
		`SELECT DISTINCT workspace_id FROM record_journal_event_pending WHERE workspace_id>? ORDER BY 1 LIMIT 128`,
	} {
		byKey := strings.Contains(source, "workspace_key")
		after, started := "", false
		for {
			query := source
			if !started {
				query = strings.ReplaceAll(query, ">?", ">=?")
			}
			args := []any{after}
			if byKey {
				args = append(args, after)
			}
			batch, err := queryStrings(ctx, s.db, query, args...)
			if err != nil {
				return report, err
			}
			if len(batch) == 0 {
				break
			}
			after, started = batch[len(batch)-1], true
			for _, value := range batch {
				key, id := value, value
				lookup := `SELECT workspace_id FROM workspace_states WHERE workspace_key=?`
				if !byKey {
					lookup = `SELECT workspace_key FROM workspace_states WHERE workspace_id=?`
				}
				var found string
				err := s.db.QueryRowContext(ctx, lookup, value).Scan(&found)
				if errors.Is(err, sql.ErrNoRows) && byKey {
					// No workspace owns the key: nothing can publish its
					// journal keys. The catalog's orphan pruning removes the
					// rest of a deleted workspace's sidecars.
					discarded, err := s.discardOrphanRecordJournal(ctx, key)
					if err != nil {
						if ctx.Err() != nil {
							return report, ctx.Err()
						}
						report.Incomplete = append(report.Incomplete, RecordJournalRepairGap{Workspace: key, Error: err.Error()})
					} else if discarded {
						report.Discarded++
					}
					continue
				}
				if errors.Is(err, sql.ErrNoRows) {
					// A deleted workspace's event markers: nothing waits on them.
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

// discardOrphanRecordJournal removes, in batches of 128 keys, the journal
// keys and activation walk of a workspace key no workspace owns. It stops
// (discarded false) as soon as a workspace has the key, which the next pass
// then repairs. The keys come from writers without a journal transaction:
// committed rows under a key without a workspace. Journaled writers flush
// their own keys before they commit.
func (s *SQLiteStore) discardOrphanRecordJournal(ctx context.Context, workspace string) (discarded bool, err error) {
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		done, err := s.discardOrphanRecordJournalBatch(ctx, workspace)
		if err != nil {
			return false, err
		}
		if done {
			return true, nil
		}
		if err := sleepContext(ctx, time.Millisecond); err != nil {
			return false, err
		}
	}
}

func (s *SQLiteStore) discardOrphanRecordJournalBatch(ctx context.Context, workspace string) (done bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var present int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&present); err != nil {
		return false, err
	}
	if present != 0 {
		return false, errors.New("the workspace exists again; the next pass repairs it")
	}
	rows, err := tx.QueryContext(ctx, `SELECT tx_id,field,record_key FROM record_journal_dirty WHERE workspace_key=? ORDER BY tx_id,field,record_key LIMIT 128`, workspace)
	if err != nil {
		return false, err
	}
	type dirtyKey struct{ token, field, id string }
	keys := []dirtyKey{}
	for rows.Next() {
		var key dirtyKey
		if err := rows.Scan(&key.token, &key.field, &key.id); err != nil {
			rows.Close()
			return false, err
		}
		keys = append(keys, key)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return false, err
	}
	for _, key := range keys {
		if _, err := tx.ExecContext(ctx, `DELETE FROM record_journal_dirty WHERE tx_id=? AND workspace_key=? AND field=? AND record_key=?`, key.token, workspace, key.field, key.id); err != nil {
			return false, err
		}
	}
	done = len(keys) < 128
	if done {
		if _, err := tx.ExecContext(ctx, `DELETE FROM record_journal_coverage WHERE workspace_key=?`, workspace); err != nil {
			return false, err
		}
	}
	return done, tx.Commit()
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
