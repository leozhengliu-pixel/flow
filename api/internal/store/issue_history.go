package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"slices"

	"flow/api/internal/domain"
)

type IssueHistory struct {
	Comments         []domain.Comment       `json:"comments"`
	Activities       []domain.ActivityEvent `json:"activities"`
	CommentsCursor   string                 `json:"commentsCursor,omitempty"`
	ActivitiesCursor string                 `json:"activitiesCursor,omitempty"`
}

func readContentPage[T any](ctx context.Context, s *SQLiteStore, workspace, resource, kind, cursor string, limits ...int) ([]T, string, error) {
	items := []T{}
	limit := 100
	if len(limits) > 0 {
		limit = min(100, max(1, limits[0]))
	}
	if cursor == "-" {
		return items, "", nil
	}
	where := "workspace_key=? AND resource_id=? AND kind=?"
	args := []any{workspace, resource, kind}
	if cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		var c contentCursor
		if err != nil || json.Unmarshal(raw, &c) != nil || c.Date == "" || c.ID == "" {
			return items, "", ErrIssueQuery
		}
		where += " AND (created_at<? OR (created_at=? AND id<?))"
		args = append(args, c.Date, c.Date, c.ID)
	}
	rows, err := s.db.QueryContext(ctx, "SELECT data,created_at,id FROM workspace_content_records WHERE "+where+" ORDER BY created_at DESC,id DESC LIMIT ?", append(args, limit+1)...)
	if err != nil {
		return items, "", err
	}
	defer rows.Close()
	var last contentCursor
	bytes := 0
	next := ""
	for rows.Next() {
		var raw []byte
		var c contentCursor
		if err := rows.Scan(&raw, &c.Date, &c.ID); err != nil {
			return items, "", err
		}
		if len(items) == limit || len(items) > 0 && bytes+len(raw) > 1<<20 {
			encoded, _ := json.Marshal(last)
			next = base64.RawURLEncoding.EncodeToString(encoded)
			break
		}
		var item T
		if err := json.Unmarshal(raw, &item); err != nil {
			return items, "", err
		}
		items = append(items, item)
		bytes += len(raw)
		last = c
	}
	slices.Reverse(items)
	return items, next, rows.Err()
}

// The caller authorizes the resource before requesting its discussion page.
func (s *SQLiteStore) ResourceCommentsPage(ctx context.Context, workspace, resource, cursor string, limit int) ([]domain.Comment, string, error) {
	return readContentPage[domain.Comment](ctx, s, workspace, resource, "comment", cursor, limit)
}

func (s *SQLiteStore) IssueHistoryPage(ctx context.Context, workspace, issue, commentsCursor, activitiesCursor string) (IssueHistory, error) {
	var result IssueHistory
	var err error
	result.Comments, result.CommentsCursor, err = readContentPage[domain.Comment](ctx, s, workspace, issue, "comment", commentsCursor)
	if err != nil {
		return result, err
	}
	result.Activities, result.ActivitiesCursor, err = readContentPage[domain.ActivityEvent](ctx, s, workspace, issue, "activity", activitiesCursor)
	if err != nil {
		return result, err
	}
	ids := []string{}
	for _, c := range result.Comments {
		ids = append(ids, c.User.ID)
	}
	for _, a := range result.Activities {
		ids = append(ids, a.Actor.ID)
	}
	refs, err := s.readIssueReferences(ctx, workspace, ids)
	if err != nil {
		return result, err
	}
	for i, c := range result.Comments {
		if user, ok := refs.users[c.User.ID]; ok {
			result.Comments[i].User = user
		}
	}
	for i, a := range result.Activities {
		if user, ok := refs.users[a.Actor.ID]; ok {
			result.Activities[i].Actor = user
		}
	}
	return result, nil
}
