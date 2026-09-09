package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"flow/api/internal/domain"
)

type discussionMutationKey struct{}
type discussionMutation struct{ CommentID string }

func WithIssueDiscussionMutation(ctx context.Context, issueID, commentID string) context.Context {
	return context.WithValue(WithIssueRecordMutations(ctx, issueID), discussionMutationKey{}, discussionMutation{commentID})
}

func loadDiscussionComment(ctx context.Context, tx *sqlTx, workspace, issue, commentID string) (map[string][]domain.Comment, error) {
	result := map[string][]domain.Comment{}
	if commentID == "" {
		return result, nil
	}
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT data FROM workspace_content_records WHERE workspace_key=? AND kind='comment' AND resource_id=? AND id=?`, workspace, issue, commentID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	var comment domain.Comment
	if err := json.Unmarshal(raw, &comment); err != nil {
		return result, err
	}
	result[issue] = []domain.Comment{comment}
	return result, nil
}

func persistDiscussion(ctx context.Context, tx *sqlTx, workspace, issue, commentID string, data domain.Bootstrap) error {
	found := false
	for resource, comments := range data.Comments {
		for _, comment := range comments {
			if comment.ID == commentID {
				found = true
			}
			if err := writeContentRecord(ctx, tx, workspace, "comment", resource, comment); err != nil {
				return err
			}
		}
	}
	if commentID != "" && !found {
		if _, err := tx.ExecContext(ctx, `DELETE FROM workspace_content_records WHERE workspace_key=? AND kind='comment' AND resource_id=? AND (id=? OR parent_id=?)`, workspace, issue, commentID, commentID); err != nil {
			return err
		}
	}
	for resource, events := range data.Activities {
		for _, event := range events {
			if err := writeContentRecord(ctx, tx, workspace, "activity", resource, event); err != nil {
				return err
			}
		}
	}
	for resource, notifications := range notificationRecords(data.Notifications) {
		for _, notification := range notifications {
			if err := writeContentRecord(ctx, tx, workspace, "notification", resource, notification); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *SQLiteStore) activeDiscussionNotifications(ctx context.Context, tx *sqlTx, workspace, issue string) (map[string][]domain.Notification, error) {
	result := map[string][]domain.Notification{}
	rows, err := tx.QueryContext(ctx, `SELECT data FROM workspace_content_records WHERE workspace_key=? AND kind='notification' AND resource_id=? AND status='unread' AND `+s.jsonText("data", "updatedAt")+`>=? ORDER BY created_at,id`, workspace, issue, time.Now().UTC().Add(-6*time.Hour).Format(time.RFC3339Nano))
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var notification domain.Notification
		if err := rows.Scan(&raw); err != nil {
			return result, err
		}
		if err := json.Unmarshal(raw, &notification); err != nil {
			return result, err
		}
		result[issue] = append(result[issue], notification)
	}
	return result, rows.Err()
}
