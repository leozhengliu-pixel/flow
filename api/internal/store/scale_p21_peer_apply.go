package store

import (
	"context"
	"encoding/json"
	"reflect"
	"strconv"
)

// Body records stay in canonical SQL. A verified page advances the resident
// policy snapshot and invalidates revision-bound cache entries atomically.
func (s *SQLiteStore) applyResidentCommittedPage(ctx context.Context, workspaceID string, page CommittedPage) (string, error) {
	next, err := strconv.ParseInt(page.Next, 10, 64)
	if err != nil || next < 0 || page.Head != page.Next || page.HasMore || page.ResyncRequired || len(page.Transactions) > 100 {
		return "", ErrPeerSnapshotRequired
	}
	ctx, release, err := s.admitMetadataWork(ctx, metadataPolicyBytes+2<<20)
	if err != nil {
		return "", err
	}
	defer release()
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var workspace string
	if err := tx.QueryRowContext(ctx, `SELECT workspace_key FROM workspace_states WHERE workspace_id=?`, workspaceID).Scan(&workspace); err != nil {
		return "", err
	}
	head, err := residentJournalHead(ctx, tx, workspaceID)
	if err != nil {
		return "", err
	}
	if head != next {
		return "", ErrPeerSnapshotRequired
	}
	stamp, err := readMetadataStamp(ctx, tx, workspace)
	if err != nil {
		return "", err
	}
	basis, after, known := s.residentStamp(workspace)
	if !known || basis.workspaceID != workspaceID || after > next {
		return "", ErrPeerSnapshotRequired
	}
	if err := checkRecordJournalCoverage(ctx, tx, workspace); err != nil {
		return "", err
	}
	if basis != stamp {
		if err := verifyResidentCommittedTransactions(ctx, tx, workspaceID, after, next, page); err != nil {
			return "", err
		}
	}
	data, installed, cost, err := s.readResidentWorkspace(ctx, tx, workspace)
	if err != nil {
		return "", err
	}
	if installed != stamp {
		return "", ErrPeerSnapshotRequired
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	if err := s.installResidentPolicy(workspace, data, stamp, next, cost); err != nil {
		return "", err
	}
	return page.Next, nil
}

func verifyResidentCommittedTransactions(ctx context.Context, tx *sqlTx, workspaceID string, after, next int64, page CommittedPage) error {
	var pending int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM record_journal WHERE workspace_id=? AND seq IS NULL`, workspaceID).Scan(&pending); err != nil {
		return err
	}
	if pending != 0 {
		return ErrPeerSnapshotRequired
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,seq,CASE WHEN running_bytes<=? THEN payload ELSE NULL END FROM (SELECT bounded.*,SUM(LENGTH(payload)+512) OVER (ORDER BY seq,id) AS running_bytes FROM (SELECT id,seq,payload FROM record_journal WHERE workspace_id=? AND seq>? AND seq<=? ORDER BY seq,id LIMIT 101) bounded) budgeted ORDER BY seq,id`, MetadataRecordPageMaxBytes, workspaceID, after, next)
	if err != nil {
		return err
	}
	defer rows.Close()
	committed := map[string]CommittedChange{}
	budget := newMetadataHydrationBudget()
	for rows.Next() {
		var id string
		var seq int64
		var raw []byte
		if err := rows.Scan(&id, &seq, &raw); err != nil {
			return err
		}
		if len(committed) == 100 || raw == nil {
			return ErrPeerSnapshotRequired
		}
		if err := budget.consume(raw); err != nil {
			return err
		}
		var change CommittedChange
		if err := json.Unmarshal(raw, &change); err != nil {
			return err
		}
		change.Seq = strconv.FormatInt(seq, 10)
		committed[id] = change
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, change := range page.Transactions {
		stored, ok := committed[change.TxID]
		if !ok || !reflect.DeepEqual(stored, change) {
			return ErrPeerSnapshotRequired
		}
		delete(committed, change.TxID)
	}
	if len(committed) != 0 {
		return ErrPeerSnapshotRequired
	}
	return nil
}
