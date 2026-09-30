package store

import (
	"context"
	"encoding/json"

	"flow/api/internal/domain"
)

// issueRecordBatchSize bounds the ids bound into one IN (...) lookup.
const issueRecordBatchSize = 200

// storedIssueRecord is an issue record as loaded for a write: its decoded
// value, its stored payload (for change detection) and its collection order.
type storedIssueRecord struct {
	issue domain.Issue
	raw   []byte
	order int64
}

// loadIssueRecordsByID reads issue records by id in batches (one round trip
// per issueRecordBatchSize ids). Unknown ids are skipped.
func (s *SQLiteStore) loadIssueRecordsByID(ctx context.Context, tx *sqlTx, workspace string, ids []string, lock bool) (map[string]storedIssueRecord, error) {
	suffix := ""
	if lock && s.dialect != "sqlite" {
		suffix = " FOR UPDATE"
	}
	result := make(map[string]storedIssueRecord, len(ids))
	pending := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id != "" && !seen[id] {
			seen[id] = true
			pending = append(pending, id)
		}
	}
	for start := 0; start < len(pending); start += issueRecordBatchSize {
		clause, args := bindList("id", pending[start:min(start+issueRecordBatchSize, len(pending))])
		rows, err := tx.QueryContext(ctx, `SELECT id,collection_order,data FROM issue_records WHERE workspace_key=? AND `+clause+suffix, append([]any{workspace}, args...)...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var record storedIssueRecord
			var id string
			if err := rows.Scan(&id, &record.order, &record.raw); err != nil {
				rows.Close()
				return nil, err
			}
			if err := json.Unmarshal(record.raw, &record.issue); err != nil {
				rows.Close()
				return nil, err
			}
			normalizeIssueRecord(&record.issue)
			result[id] = record
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

// writeIssueRecordBatch persists the issues whose owned data differs from the
// stored payloads with multi-row statements: one stats update, one upsert per
// 100 records and one index sync per table for the whole batch, instead of
// several round trips per issue. previous holds the stored payloads the caller
// already read (keyed by id); missing ones are read here. It returns the ids
// it wrote.
func (s *SQLiteStore) writeIssueRecordBatch(ctx context.Context, tx *sqlTx, workspace string, issues []domain.Issue, metadata domain.Bootstrap, previous map[string][]byte) ([]string, error) {
	if len(issues) == 0 {
		return nil, nil
	}
	missing := []string{}
	for _, issue := range issues {
		if _, ok := previous[issue.ID]; !ok {
			missing = append(missing, issue.ID)
		}
	}
	if len(missing) > 0 {
		payloads := make(map[string][]byte, len(previous)+len(missing))
		for id, raw := range previous {
			payloads[id] = raw
		}
		for start := 0; start < len(missing); start += issueRecordBatchSize {
			clause, args := bindList("id", missing[start:min(start+issueRecordBatchSize, len(missing))])
			rows, err := tx.QueryContext(ctx, `SELECT id,data FROM issue_records WHERE workspace_key=? AND `+clause, append([]any{workspace}, args...)...)
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				var id string
				var raw []byte
				if err := rows.Scan(&id, &raw); err != nil {
					rows.Close()
					return nil, err
				}
				payloads[id] = raw
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return nil, err
			}
		}
		previous = payloads
	}
	refs := newIssueReferences(metadata)
	deltas := map[issueStatsKey]int64{}
	changed := []domain.Issue{}
	written := []string{}
	for _, issue := range issues {
		values, err := issueRecordValues(workspace, issue)
		if err != nil {
			return nil, err
		}
		old := previous[issue.ID]
		if equalIssueRecordData(old, values[len(values)-1].([]byte)) || len(old) > 0 && refs.equalOwned(old, issue) {
			continue
		}
		if len(old) > 0 {
			var before domain.Issue
			if err := json.Unmarshal(old, &before); err != nil {
				return nil, err
			}
			addIssueStats(deltas, issueStatsOf(before), -1)
		}
		addIssueStats(deltas, issueStatsOf(issue), 1)
		changed = append(changed, issue)
		written = append(written, issue.ID)
	}
	if len(changed) == 0 {
		return nil, nil
	}
	if err := writeIssueStats(ctx, tx, workspace, deltas); err != nil {
		return nil, err
	}
	if err := s.importIssueRecordBatch(ctx, tx, workspace, changed); err != nil {
		return nil, err
	}
	for _, issue := range changed {
		if err := syncApplicationTask(ctx, tx, workspace, issue, previous[issue.ID]); err != nil {
			return nil, err
		}
	}
	return written, nil
}
