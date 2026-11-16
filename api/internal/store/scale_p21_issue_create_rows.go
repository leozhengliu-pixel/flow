package store

import (
	"context"

	"flow/api/internal/domain"
)

// A stale sequence or an imported/custom ID can collide with a stored issue.
// Own and lock every possible preimage before the literal single-row writer
// repeats its read and compares owned references; new IDs are not evidence of
// absence. The row reader's batch bound and the per-row encoding lease keep
// these allowances separate from the retained callback results.
func (s *SQLiteStore) writeIssueCreateRows(ctx context.Context, tx *sqlTx, workspace string, rows *issueMutationRows, issues []domain.Issue, data domain.Bootstrap) error {
	for start := 0; start < len(issues); start += 1000 {
		batch := issues[start:min(start+1000, len(issues))]
		ids := make([]string, 0, len(batch))
		for _, issue := range batch {
			ids = append(ids, issue.ID)
		}
		previous, err := rows.load(ctx, ids, true)
		if err != nil {
			return err
		}
		written := map[string]bool{}
		for _, issue := range batch {
			if written[issue.ID] {
				// A custom creation key can also collide with a later
				// template/workflow issue in this same callback.
				current, err := rows.load(ctx, []string{issue.ID}, true)
				if err != nil {
					return err
				}
				previous[issue.ID] = current[issue.ID]
			}
			if err := func() error {
				release, err := reserveIssueRecordEncoding(ctx, s, issue, previous[issue.ID].raw)
				if err != nil {
					return err
				}
				defer release()
				return s.writeIssueRecord(ctx, tx, workspace, issue, data)
			}(); err != nil {
				return err
			}
			written[issue.ID] = true
		}
	}
	return nil
}
