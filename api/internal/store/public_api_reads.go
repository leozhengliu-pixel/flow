package store

import (
	"context"
	"encoding/json"
	"slices"

	"flow/api/internal/domain"
)

// ContentCursor is the raw (created_at, id) key of one content record. The
// created_at value is kept exactly as stored so cursor comparisons match the
// ordering of content_resource_order_idx.
type ContentCursor struct {
	CreatedAt string
	ID        string
}

// CommentConnection is one Relay page of a resource's comments, newest first.
type CommentConnection struct {
	Comments    []domain.Comment
	Keys        []ContentCursor
	HasNext     bool
	HasPrevious bool
}

// ResourceCommentConnection pages the comments of one issue or document using
// content_resource_order_idx(workspace_key,kind,resource_id,created_at,id).
// Forward pages (first/after) walk from newest to oldest; backward pages
// (last/before) read the opposite direction and are returned newest first.
// The caller authorizes the resource first.
func (s *SQLiteStore) ResourceCommentConnection(ctx context.Context, workspace, resource string, after, before *ContentCursor, limit int, backward bool) (CommentConnection, error) {
	result := CommentConnection{Comments: []domain.Comment{}, Keys: []ContentCursor{}}
	limit = min(max(limit, 1), 250)
	base := "workspace_key=? AND kind='comment' AND resource_id=?"
	baseArgs := []any{workspace, resource}
	where, args := base, slices.Clone(baseArgs)
	older := func(c *ContentCursor) (string, []any) {
		return " AND (created_at<? OR (created_at=? AND id<?))", []any{c.CreatedAt, c.CreatedAt, c.ID}
	}
	newer := func(c *ContentCursor) (string, []any) {
		return " AND (created_at>? OR (created_at=? AND id>?))", []any{c.CreatedAt, c.CreatedAt, c.ID}
	}
	if after != nil {
		clause, values := older(after)
		where, args = where+clause, append(args, values...)
	}
	if before != nil {
		clause, values := newer(before)
		where, args = where+clause, append(args, values...)
	}
	order := " ORDER BY created_at DESC,id DESC"
	if backward {
		order = " ORDER BY created_at ASC,id ASC"
	}
	// Read the page and close its rows before any further query: SQLite pools
	// may hold one connection, and other drivers would pin two per request.
	more, err := s.readCommentPage(ctx, "SELECT data,created_at,id FROM workspace_content_records WHERE "+where+order+" LIMIT ?", append(args, limit+1), limit, &result)
	if err != nil {
		return result, err
	}
	if more && backward {
		result.HasPrevious = true
	} else if more {
		result.HasNext = true
	}
	if backward {
		slices.Reverse(result.Comments)
		slices.Reverse(result.Keys)
	}
	exists := func(clause string, values []any) (bool, error) {
		var found int
		err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM (SELECT id FROM workspace_content_records WHERE "+base+clause+" LIMIT 1) edge", append(slices.Clone(baseArgs), values...)...).Scan(&found)
		return found > 0, err
	}
	if len(result.Keys) > 0 {
		if !result.HasPrevious {
			clause, values := newer(&result.Keys[0])
			if result.HasPrevious, err = exists(clause, values); err != nil {
				return result, err
			}
		}
		if !result.HasNext {
			clause, values := older(&result.Keys[len(result.Keys)-1])
			if result.HasNext, err = exists(clause, values); err != nil {
				return result, err
			}
		}
	}
	ids := make([]string, 0, len(result.Comments))
	for _, comment := range result.Comments {
		ids = append(ids, comment.User.ID)
	}
	refs, err := s.readIssueReferences(ctx, workspace, ids)
	if err != nil {
		return result, err
	}
	for index, comment := range result.Comments {
		if user, ok := refs.users[comment.User.ID]; ok {
			result.Comments[index].User = user
		}
	}
	return result, nil
}

// readCommentPage scans up to limit comments into result and reports whether
// another row followed. Its rows are closed when it returns.
func (s *SQLiteStore) readCommentPage(ctx context.Context, query string, args []any, limit int, result *CommentConnection) (bool, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		if len(result.Comments) == limit {
			return true, nil
		}
		var raw []byte
		var key ContentCursor
		if err := rows.Scan(&raw, &key.CreatedAt, &key.ID); err != nil {
			return false, err
		}
		var comment domain.Comment
		if err := json.Unmarshal(raw, &comment); err != nil {
			return false, err
		}
		result.Comments = append(result.Comments, comment)
		result.Keys = append(result.Keys, key)
	}
	return false, rows.Err()
}

// AttachmentIssueIDs returns up to limit issues visible under q that carry an
// attachment with exactly this URL (attachmentsForURL), using
// issue_attachment_url_idx(workspace_key,url_hash,issue_id).
func (s *SQLiteStore) AttachmentIssueIDs(ctx context.Context, q IssueRecordQuery, url string, limit int) ([]string, error) {
	q.Filter = IssueFilter{}
	q.Archived = "all"
	where, args, err := issueRecordWhere(q)
	if err != nil {
		return nil, err
	}
	prefix, prefixArgs := issueAccessCTE(q)
	rows, err := s.db.QueryContext(ctx, prefix+`SELECT i.id FROM issue_attachment_records a JOIN issue_records i ON i.workspace_key=a.workspace_key AND i.id=a.issue_id WHERE a.workspace_key=? AND a.url_hash=? AND `+where+` ORDER BY i.id LIMIT ?`, append(append(append(prefixArgs, q.Workspace, attachmentKey(url)), args...), min(max(limit, 1), 250))...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
