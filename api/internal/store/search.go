package store

import (
	"context"
	"strings"
	"time"

	"flow/api/internal/domain"
)

func (s *SQLiteStore) RecordSearch(ctx context.Context, workspaceID, userID, query string) error {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO search_history(user_id,workspace_id,query,use_count,last_used_at) VALUES(?,?,?,?,?)
		ON CONFLICT(user_id,workspace_id,query) DO UPDATE SET use_count=search_history.use_count+1,last_used_at=excluded.last_used_at`,
		userID, workspaceID, query, 1, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (s *SQLiteStore) SearchHistory(ctx context.Context, workspaceID, userID string, limit int) ([]domain.SearchHistoryEntry, error) {
	if limit < 1 || limit > 50 {
		limit = 8
	}
	rows, err := s.db.QueryContext(ctx, `SELECT query,use_count,last_used_at FROM search_history WHERE user_id=? AND workspace_id=? ORDER BY last_used_at DESC LIMIT ?`, userID, workspaceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.SearchHistoryEntry{}
	for rows.Next() {
		var item domain.SearchHistoryEntry
		var raw string
		if err := rows.Scan(&item.Query, &item.UseCount, &raw); err != nil {
			return nil, err
		}
		item.LastUsedAt, _ = time.Parse(time.RFC3339Nano, raw)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) ClearSearchHistory(ctx context.Context, workspaceID, userID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM search_history WHERE user_id=? AND workspace_id=?`, userID, workspaceID)
	return err
}

func (s *SQLiteStore) RecordRecent(ctx context.Context, workspaceID, userID, resourceType, resourceID string) error {
	resourceType, resourceID = strings.TrimSpace(resourceType), strings.TrimSpace(resourceID)
	if resourceType == "" || resourceID == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO recently_viewed(user_id,workspace_id,resource_type,resource_id,last_viewed_at) VALUES(?,?,?,?,?)
		ON CONFLICT(user_id,workspace_id,resource_type,resource_id) DO UPDATE SET last_viewed_at=excluded.last_viewed_at`,
		userID, workspaceID, resourceType, resourceID, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (s *SQLiteStore) RecentResources(ctx context.Context, workspaceID, userID string, limit int) ([]domain.RecentResource, error) {
	if limit < 1 || limit > 100 {
		limit = 12
	}
	rows, err := s.db.QueryContext(ctx, `SELECT resource_type,resource_id,last_viewed_at FROM recently_viewed WHERE user_id=? AND workspace_id=? ORDER BY last_viewed_at DESC LIMIT ?`, userID, workspaceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.RecentResource{}
	for rows.Next() {
		var item domain.RecentResource
		var raw string
		if err := rows.Scan(&item.ResourceType, &item.ResourceID, &raw); err != nil {
			return nil, err
		}
		item.LastViewedAt, _ = time.Parse(time.RFC3339Nano, raw)
		result = append(result, item)
	}
	return result, rows.Err()
}
