package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Loop document appends: a scheduled loop appends one section per occurrence
// (its scheduled date) to a document. A row claims (loop, occurrence,
// document) for the run that appends, so a second run for the same
// occurrence — a manual run right after the scheduled one, a run started
// again after a restart, or another server starting the same occurrence —
// finds the claim instead of appending the section again. The primary key
// makes the claim atomic across servers sharing the database.

// LoopDocumentAppend is a run's claim on an occurrence's append to a document.
type LoopDocumentAppend struct {
	LoopID     string
	Occurrence string
	DocumentID string
	RunID      string
	// TextKey identifies the appended text; TextProbe is its start, to find
	// it in the document when the claiming run ended before recording the
	// outcome.
	TextKey       string
	TextProbe     string
	VersionBefore int64
	VersionAfter  int64
	// pending: the run is appending; appended: the document shows the append.
	Status    string
	CreatedAt time.Time
}

const (
	LoopAppendPending  = "pending"
	LoopAppendAppended = "appended"
)

// loopDocumentAppendRetention is how long occurrence claims are kept.
const loopDocumentAppendRetention = 400 * 24 * time.Hour

func (s *SQLiteStore) ensureLoopDocumentAppends(ctx context.Context) error {
	text := "TEXT"
	if s.dialect == "mysql" {
		text = "MEDIUMTEXT"
	}
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS loop_document_appends(workspace_key VARCHAR(191) NOT NULL,loop_id VARCHAR(191) NOT NULL,occurrence VARCHAR(64) NOT NULL,document_id VARCHAR(191) NOT NULL,run_id VARCHAR(191) NOT NULL,text_key VARCHAR(191) NOT NULL,text_probe `+text+` NOT NULL,version_before BIGINT NOT NULL,version_after BIGINT NOT NULL,status VARCHAR(16) NOT NULL,created_at VARCHAR(40) NOT NULL,PRIMARY KEY(workspace_key,loop_id,occurrence,document_id))`)
	return err
}

// ClaimLoopDocumentAppend records claim for its run unless the occurrence's
// append to the document is already claimed; then it returns that claim and
// false.
func (s *SQLiteStore) ClaimLoopDocumentAppend(ctx context.Context, workspace string, claim LoopDocumentAppend) (LoopDocumentAppend, bool, error) {
	now := time.Now().UTC()
	// Old occurrences are not run again; drop their claims.
	_, _ = s.db.ExecContext(ctx, `DELETE FROM loop_document_appends WHERE workspace_key=? AND loop_id=? AND created_at<?`, workspace, claim.LoopID, formatLoopRunTime(now.Add(-loopDocumentAppendRetention)))
	claim.Status, claim.CreatedAt, claim.VersionAfter = LoopAppendPending, now, 0
	_, insertErr := s.db.ExecContext(ctx, `INSERT INTO loop_document_appends(workspace_key,loop_id,occurrence,document_id,run_id,text_key,text_probe,version_before,version_after,status,created_at) VALUES(?,?,?,?,?,?,?,?,0,?,?)`,
		workspace, claim.LoopID, claim.Occurrence, claim.DocumentID, claim.RunID, claim.TextKey, claim.TextProbe, claim.VersionBefore, claim.Status, formatLoopRunTime(now))
	if insertErr == nil {
		return claim, true, nil
	}
	existing, err := s.LoopDocumentAppend(ctx, workspace, claim.LoopID, claim.Occurrence, claim.DocumentID)
	if errors.Is(err, sql.ErrNoRows) {
		return LoopDocumentAppend{}, false, insertErr
	}
	return existing, false, err
}

// LoopDocumentAppend reads one claim; sql.ErrNoRows when there is none.
func (s *SQLiteStore) LoopDocumentAppend(ctx context.Context, workspace, loopID, occurrence, documentID string) (LoopDocumentAppend, error) {
	item := LoopDocumentAppend{LoopID: loopID, Occurrence: occurrence, DocumentID: documentID}
	var created string
	err := s.db.QueryRowContext(ctx, `SELECT run_id,text_key,text_probe,version_before,version_after,status,created_at FROM loop_document_appends WHERE workspace_key=? AND loop_id=? AND occurrence=? AND document_id=?`, workspace, loopID, occurrence, documentID).
		Scan(&item.RunID, &item.TextKey, &item.TextProbe, &item.VersionBefore, &item.VersionAfter, &item.Status, &created)
	if err != nil {
		return item, err
	}
	item.CreatedAt, _ = time.Parse(loopRunTime, created)
	return item, nil
}

// TakeOverLoopDocumentAppend moves a pending claim whose run ended without
// appending to claim's run. It reports false when the claim changed meanwhile
// (another run took it over, or its append was recorded).
func (s *SQLiteStore) TakeOverLoopDocumentAppend(ctx context.Context, workspace string, claim LoopDocumentAppend, previousRunID string) (bool, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE loop_document_appends SET run_id=?,text_key=?,text_probe=?,version_before=?,version_after=0,status=?,created_at=? WHERE workspace_key=? AND loop_id=? AND occurrence=? AND document_id=? AND run_id=? AND status=?`,
		claim.RunID, claim.TextKey, claim.TextProbe, claim.VersionBefore, LoopAppendPending, formatLoopRunTime(time.Now().UTC()), workspace, claim.LoopID, claim.Occurrence, claim.DocumentID, previousRunID, LoopAppendPending)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

// SettleLoopDocumentAppend records how the claiming run's append ended: the
// document shows it (appended, with the version after it), or nothing was
// written and the pending claim is released for a later run of the
// occurrence.
func (s *SQLiteStore) SettleLoopDocumentAppend(ctx context.Context, workspace string, claim LoopDocumentAppend, appended bool, versionAfter int64) error {
	if appended {
		_, err := s.db.ExecContext(ctx, `UPDATE loop_document_appends SET status=?,version_after=? WHERE workspace_key=? AND loop_id=? AND occurrence=? AND document_id=? AND run_id=?`,
			LoopAppendAppended, versionAfter, workspace, claim.LoopID, claim.Occurrence, claim.DocumentID, claim.RunID)
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM loop_document_appends WHERE workspace_key=? AND loop_id=? AND occurrence=? AND document_id=? AND run_id=? AND status=?`,
		workspace, claim.LoopID, claim.Occurrence, claim.DocumentID, claim.RunID, LoopAppendPending)
	return err
}

// deleteLoopDocumentAppends removes a deleted loop's claims.
func (s *SQLiteStore) deleteLoopDocumentAppends(ctx context.Context, workspace, loopID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM loop_document_appends WHERE workspace_key=? AND loop_id=?`, workspace, loopID)
	return err
}
