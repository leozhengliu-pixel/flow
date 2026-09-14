package store

import (
	"context"
	"fmt"
	"time"

	"flow/api/internal/domain"
)

func (s *SQLiteStore) ensureTeamDefaultFavorites(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS team_default_favorites (id VARCHAR(191) PRIMARY KEY, workspace_key VARCHAR(191) NOT NULL, team_id VARCHAR(191) NOT NULL, resource_type VARCHAR(64) NOT NULL, resource_id VARCHAR(191) NOT NULL, position DOUBLE PRECISION NOT NULL, created_at VARCHAR(64) NOT NULL, updated_at VARCHAR(64) NOT NULL, UNIQUE(workspace_key,team_id,resource_type,resource_id))`)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS team_default_favorite_overrides (workspace_key VARCHAR(191) NOT NULL, user_id VARCHAR(191) NOT NULL, resource_type VARCHAR(64) NOT NULL, resource_id VARCHAR(191) NOT NULL, hidden INTEGER NOT NULL, updated_at VARCHAR(64) NOT NULL, PRIMARY KEY(workspace_key,user_id,resource_type,resource_id))`)
	return err
}

func (s *SQLiteStore) ListTeamDefaultFavorites(ctx context.Context, workspace, teamID string) ([]domain.TeamDefaultFavorite, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,workspace_key,team_id,resource_type,resource_id,position,created_at,updated_at FROM team_default_favorites WHERE workspace_key=? AND team_id=? ORDER BY position,id`, workspace, teamID)
	if err != nil {
		return nil, err
	}
	result := []domain.TeamDefaultFavorite{}
	for rows.Next() {
		var item domain.TeamDefaultFavorite
		var created, updated string
		if err := rows.Scan(&item.ID, &item.WorkspaceKey, &item.TeamID, &item.ResourceType, &item.ResourceID, &item.Position, &created, &updated); err != nil {
			return nil, err
		}
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		item.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
		result = append(result, item)
	}
	err = rows.Err()
	rows.Close()
	return result, err
}

func (s *SQLiteStore) ReplaceTeamDefaultFavorites(ctx context.Context, workspace, teamID string, items []domain.TeamDefaultFavorite) error {
	if len(items) > 100 {
		return fmt.Errorf("a team can have at most 100 default favorites")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Compare only the bounded team configuration. An unchanged save must not
	// rotate IDs or emit delete/insert pairs into the replication log.
	rows, err := tx.QueryContext(ctx, `SELECT resource_type,resource_id FROM team_default_favorites WHERE workspace_key=? AND team_id=? ORDER BY position,id`, workspace, teamID)
	if err != nil {
		return err
	}
	unchanged, count := true, 0
	for rows.Next() {
		var resourceType, resourceID string
		if err := rows.Scan(&resourceType, &resourceID); err != nil {
			rows.Close()
			return err
		}
		if count >= len(items) || items[count].ResourceType != resourceType || items[count].ResourceID != resourceID {
			unchanged = false
		}
		count++
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if unchanged && count == len(items) {
		return nil
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM team_default_favorites WHERE workspace_key=? AND team_id=?`, workspace, teamID); err != nil {
		return err
	}
	for i, item := range items {
		if item.ResourceType == "" || item.ResourceID == "" {
			return fmt.Errorf("resource type and id are required")
		}
		// IDs are server-owned. Never accept a client-supplied primary key here:
		// the table key is global and reusing an ID from another team/workspace
		// could otherwise overwrite that row on MySQL's duplicate-key path.
		id := fmt.Sprintf("team_default_favorite_%d_%d", time.Now().UnixNano(), i)
		q := `INSERT INTO team_default_favorites(id,workspace_key,team_id,resource_type,resource_id,position,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`
		if _, err = tx.ExecContext(ctx, q, id, workspace, teamID, item.ResourceType, item.ResourceID, float64(i), now, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *SQLiteStore) mergeTeamDefaultFavorites(ctx context.Context, data *domain.Bootstrap, userID string) error {
	if data == nil || data.Workspace.URLKey == "" || userID == "" {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT d.id,d.team_id,d.resource_type,d.resource_id,d.position,d.created_at,d.updated_at FROM team_default_favorites d JOIN team_memberships tm ON tm.team_id=d.team_id AND tm.workspace_id=? AND tm.user_id=? JOIN workspace_memberships m ON m.workspace_id=tm.workspace_id AND m.user_id=tm.user_id AND m.status='active' WHERE d.workspace_key=? ORDER BY d.position,d.id`, data.Workspace.ID, userID, data.Workspace.URLKey)
	if err != nil {
		return err
	}
	type defaultRow struct {
		id, typ, rid, created, updated string
		pos                            float64
	}
	defaults := []defaultRow{}
	for rows.Next() {
		var row defaultRow
		if err := rows.Scan(&row.id, new(string), &row.typ, &row.rid, &row.pos, &row.created, &row.updated); err != nil {
			rows.Close()
			return err
		}
		defaults = append(defaults, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	hidden := map[string]bool{}
	orows, err := s.db.QueryContext(ctx, `SELECT resource_type,resource_id FROM team_default_favorite_overrides WHERE workspace_key=? AND user_id=? AND hidden=1`, data.Workspace.URLKey, userID)
	if err != nil {
		return err
	}
	for orows.Next() {
		var typ, id string
		if err := orows.Scan(&typ, &id); err != nil {
			orows.Close()
			return err
		}
		hidden[typ+":"+id] = true
	}
	orows.Close()
	seen := map[string]bool{}
	for _, f := range data.Favorites {
		if f.UserID == userID {
			seen[f.ResourceType+":"+f.ResourceID] = true
		}
	}
	for _, row := range defaults {
		id, typ, rid, pos, created := row.id, row.typ, row.rid, row.pos, row.created
		key := typ + ":" + rid
		if hidden[key] || seen[key] {
			continue
		}
		seen[key] = true
		createdAt, _ := time.Parse(time.RFC3339Nano, created)
		data.Favorites = append(data.Favorites, domain.Favorite{ID: "team-default:" + id, UserID: userID, ResourceType: typ, ResourceID: rid, Position: pos, CreatedAt: createdAt})
	}
	return nil
}

func (s *SQLiteStore) ApplyTeamDefaultFavorites(ctx context.Context, data *domain.Bootstrap, userID string) error {
	return s.mergeTeamDefaultFavorites(ctx, data, userID)
}

func (s *SQLiteStore) SetTeamDefaultFavoriteOverride(ctx context.Context, workspace, userID, typ, rid string, hidden bool) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if hidden {
		q := `INSERT INTO team_default_favorite_overrides(workspace_key,user_id,resource_type,resource_id,hidden,updated_at) VALUES(?,?,?,?,1,?)`
		if s.dialect == "mysql" {
			q += ` ON DUPLICATE KEY UPDATE hidden=1,updated_at=VALUES(updated_at)`
		} else {
			q += ` ON CONFLICT(workspace_key,user_id,resource_type,resource_id) DO UPDATE SET hidden=1,updated_at=excluded.updated_at`
		}
		_, err := s.db.ExecContext(ctx, q, workspace, userID, typ, rid, now)
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM team_default_favorite_overrides WHERE workspace_key=? AND user_id=? AND resource_type=? AND resource_id=?`, workspace, userID, typ, rid)
	return err
}

func (s *SQLiteStore) TeamDefaultFavoriteExists(ctx context.Context, workspace, typ, rid string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM team_default_favorites WHERE workspace_key=? AND resource_type=? AND resource_id=?`, workspace, typ, rid).Scan(&count)
	return count > 0, err
}
