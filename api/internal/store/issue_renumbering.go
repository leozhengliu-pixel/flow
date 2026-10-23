package store

import (
	"context"

	"flow/api/internal/domain"
)

type issueRenumberingContext struct{}

// WithIssueRenumbering marks a mutation that may give existing issues new
// numbers: moving an issue to another team gives it the next number of the
// workspace sequence. The store reads the sequence under a row lock before
// the callback runs (as Bootstrap.NextIssueNumber) and advances it past the
// numbers the callback assigned, so concurrent moves and creations on any
// server never hand out the same identifier.
func WithIssueRenumbering(ctx context.Context) context.Context {
	return context.WithValue(ctx, issueRenumberingContext{}, true)
}

func issueRenumbering(ctx context.Context) bool {
	enabled, _ := ctx.Value(issueRenumberingContext{}).(bool)
	return enabled
}

// lockedNextIssueNumber is the next free number of the workspace sequence,
// read under a row lock held until the transaction ends.
func (s *SQLiteStore) lockedNextIssueNumber(ctx context.Context, tx *sqlTx, workspace string) (int, error) {
	if err := writeIssueNumber(ctx, tx, workspace, 0); err != nil {
		return 0, err
	}
	lock := ""
	if s.dialect != "sqlite" {
		lock = " FOR UPDATE"
	}
	var last int
	if err := tx.QueryRowContext(ctx, `SELECT last_number FROM issue_number_sequences WHERE workspace_key=?`+lock, workspace).Scan(&last); err != nil {
		return 0, err
	}
	return last + 1, nil
}

// advanceIssueNumberSequence moves the sequence past every number in issues.
func advanceIssueNumberSequence(ctx context.Context, tx *sqlTx, workspace string, issues []domain.Issue) error {
	last := 0
	for _, issue := range issues {
		last = max(last, issue.Number)
	}
	if last == 0 {
		return nil
	}
	return writeIssueNumber(ctx, tx, workspace, last)
}
