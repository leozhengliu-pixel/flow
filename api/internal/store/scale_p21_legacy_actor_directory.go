package store

import (
	"context"
	"database/sql"
)

// boundedLegacyActorDirectory preserves the full writer's best-effort role
// and direct-team lookups. Its caller owns retained values through callback use.
func (s *SQLiteStore) boundedLegacyActorDirectory(ctx context.Context, workspaceID, userID string, owner *ResourceRead) (string, []string, bool, error) {
	if s == nil || !s.BoundedMetadataEnabled() || owner == nil || owner.store != s {
		return "", nil, false, ErrBoundedMetadataCoverage
	}
	owner.mu.Lock()
	closed := owner.closed
	owner.mu.Unlock()
	if closed {
		return "", nil, false, ErrMetadataRecordQuery
	}
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	scratch := &ResourceRead{store: s}
	defer scratch.Close()
	if err := scratch.reserve(ctx, 2<<20+64<<10); err != nil {
		return "", nil, false, err
	}
	limit := MetadataRecordPageMaxBytes
	length := "(" + scratch.byteLength("role") + "+" + scratch.byteLength("status") + ")"
	var size sql.NullInt64
	var role, status sql.NullString
	err := s.db.QueryRowContext(ctx, "SELECT "+length+",CASE WHEN "+length+
		"<=? THEN role ELSE NULL END,CASE WHEN "+length+
		"<=? THEN status ELSE NULL END FROM workspace_memberships WHERE workspace_id=? AND user_id=?",
		limit, limit, workspaceID, userID).Scan(&size, &role, &status)
	if err := ctx.Err(); err != nil {
		return "", nil, false, err
	}
	if err != nil || !size.Valid {
		return "", nil, false, nil
	}
	if size.Int64 < 0 || size.Int64 > int64(limit) {
		return "", nil, false, ErrMetadataWorkBudget
	}
	if !role.Valid || !status.Valid || status.String != "active" {
		return "", nil, false, nil
	}
	if err := owner.reserve(ctx, uint64(len(role.String))*8+256); err != nil {
		return "", nil, false, err
	}
	teams := &ResourceRead{store: s}
	transferred := false
	defer func() {
		if !transferred {
			teams.Close()
		}
	}()
	length = scratch.byteLength("team_id")
	rows, err := s.db.QueryContext(ctx, "SELECT "+length+",CASE WHEN "+length+
		"<=? THEN team_id ELSE NULL END FROM team_memberships WHERE workspace_id=? AND user_id=?",
		limit, workspaceID, userID)
	if err != nil {
		if ctx.Err() != nil {
			return "", nil, false, ctx.Err()
		}
		return role.String, nil, true, nil
	}
	defer rows.Close()
	result := []string{}
	for rows.Next() {
		var size sql.NullInt64
		var id sql.NullString
		if err := rows.Scan(&size, &id); err != nil {
			continue
		}
		if !size.Valid {
			continue
		}
		if size.Int64 < 0 || size.Int64 > int64(limit) {
			return "", nil, false, ErrMetadataWorkBudget
		}
		if !id.Valid {
			continue
		}
		if err := teams.reserve(ctx, uint64(len(id.String))*8+256); err != nil {
			return "", nil, false, err
		}
		result = append(result, id.String)
	}
	if err := ctx.Err(); err != nil {
		return "", nil, false, err
	}
	if rows.Err() != nil {
		return role.String, nil, true, nil
	}
	owner.release = append(owner.release, teams.Close)
	transferred = true
	return role.String, result, true, nil
}
