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

// loadDiscussionComment loads the thread around commentID: its ancestors up
// to the root and every reply below that root, so thread fan-out and thread
// subscriptions see all participants. Unchanged comments are not rewritten.
func loadDiscussionComment(ctx context.Context, tx *sqlTx, workspace, issue, commentID string) (map[string][]domain.Comment, error) {
	result := map[string][]domain.Comment{}
	if commentID == "" {
		return result, nil
	}
	load := func(id string) (*domain.Comment, error) {
		var raw []byte
		err := tx.QueryRowContext(ctx, `SELECT data FROM workspace_content_records WHERE workspace_key=? AND kind='comment' AND resource_id=? AND id=?`, workspace, issue, id).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		var comment domain.Comment
		if err := json.Unmarshal(raw, &comment); err != nil {
			return nil, err
		}
		return &comment, nil
	}
	target, err := load(commentID)
	if err != nil || target == nil {
		return result, err
	}
	root := *target
	seen := map[string]bool{root.ID: true}
	ancestors := []domain.Comment{}
	for depth := 0; root.ParentID != nil && *root.ParentID != "" && depth < 16; depth++ {
		parent, err := load(*root.ParentID)
		if err != nil {
			return result, err
		}
		if parent == nil || seen[parent.ID] {
			break
		}
		ancestors = append(ancestors, root)
		seen[parent.ID] = true
		root = *parent
	}
	comments := append([]domain.Comment{root}, ancestors...)
	frontier := []string{root.ID}
	for len(frontier) > 0 && len(comments) < 1000 {
		parentID := frontier[0]
		frontier = frontier[1:]
		rows, err := tx.QueryContext(ctx, `SELECT data FROM workspace_content_records WHERE workspace_key=? AND kind='comment' AND resource_id=? AND parent_id=? ORDER BY created_at,id LIMIT 1000`, workspace, issue, parentID)
		if err != nil {
			return result, err
		}
		for rows.Next() {
			var raw []byte
			var comment domain.Comment
			if err := rows.Scan(&raw); err != nil {
				rows.Close()
				return result, err
			}
			if err := json.Unmarshal(raw, &comment); err != nil {
				rows.Close()
				return result, err
			}
			if !seen[comment.ID] {
				seen[comment.ID] = true
				comments = append(comments, comment)
				frontier = append(frontier, comment.ID)
			} else if comment.ID != root.ID {
				frontier = append(frontier, comment.ID)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return result, err
		}
	}
	if !seen[target.ID] {
		comments = append(comments, *target)
	}
	result[issue] = comments
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
