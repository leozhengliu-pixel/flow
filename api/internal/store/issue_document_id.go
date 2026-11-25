package store

import (
	"context"
	"database/sql"
	"errors"
)

// IssueDocumentContentID is a point read of one issue's collaborative
// description id (documentContent.id) from its issue record, without decoding
// the record. found is false when the issue record does not exist; id is empty
// when the issue has no stored description id (the caller then falls back to
// "document_content_"+issue id, as the legacy metadata path does).
func (s *SQLiteStore) IssueDocumentContentID(ctx context.Context, workspace, issueID string) (id string, found bool, err error) {
	var value sql.NullString
	err = s.db.QueryRowContext(ctx, `SELECT COALESCE(`+s.jsonText("data", "documentContent.id")+`,'') FROM issue_records WHERE workspace_key=? AND id=?`, workspace, issueID).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value.String, true, nil
}
