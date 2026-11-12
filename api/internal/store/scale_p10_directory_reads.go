package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"

	"flow/api/internal/domain"
)

// QueryProjectDirectoryWithAccess retains the legacy directory representation
// while invalidating an in-flight indexed read when its policy changes.
func (s *SQLiteStore) QueryProjectDirectoryWithAccess(ctx context.Context, access AccessContext, query ProjectRecordQuery) (ProjectRecordPage, error) {
	if err := s.ValidateAccess(ctx, access); err != nil {
		return ProjectRecordPage{}, err
	}
	if query.Workspace != access.policy.workspace {
		return ProjectRecordPage{}, ErrAuthForbidden
	}
	page, err := s.QueryProjectDirectory(ctx, query)
	if err == nil {
		err = s.ValidateAccess(ctx, access)
	}
	if err != nil {
		return ProjectRecordPage{}, err
	}
	return page, nil
}

func (s *SQLiteStore) queryProjectDirectoryAdapter(ctx context.Context, query ProjectRecordQuery) (ProjectRecordPage, error) {
	mode := indexedReadsMode()
	if mode == "off" {
		return s.queryProjectDirectoryLegacy(ctx, query)
	}
	// The installed legacy copy includes display normalization and live counts.
	// Keep using it until those overlays have their own indexed contracts.
	if _, installed := s.projectSnapshot(query.Workspace); installed {
		return s.queryProjectDirectoryLegacy(ctx, query)
	}
	status, err := s.MetadataCatalogStatus(ctx, query.Workspace, "projects")
	if err != nil || !status.Ready && (mode != "shadow" || !status.Complete) {
		return s.queryProjectDirectoryLegacy(ctx, query)
	}
	candidate, candidateErr := s.readProjectDirectoryCatalog(ctx, query)
	if candidateErr == nil {
		after, statusErr := s.MetadataCatalogStatus(ctx, query.Workspace, "projects")
		if statusErr != nil || after.Revision != status.Revision || mode == "on" && !after.Ready {
			candidateErr = ErrMetadataCatalogGap
		}
	}
	if mode == "on" && candidateErr == nil {
		return candidate, nil
	}
	legacy, err := s.queryProjectDirectoryLegacy(ctx, query)
	if mode == "shadow" {
		want, wantErr := json.Marshal(legacy)
		got, gotErr := json.Marshal(candidate)
		if err != nil || candidateErr != nil || wantErr != nil || gotErr != nil || !bytes.Equal(want, got) {
			// Do not log filters, resource identities, actors or record bodies.
			slog.Warn("indexed project directory shadow difference")
		}
	}
	return legacy, err
}

// Preserve the old offset token and the canonical predicates, including
// customer-only fields and null/empty team scopes. This compatibility adapter
// streams summaries; exact totals still scan matching summary rows.
func (s *SQLiteStore) readProjectDirectoryCatalog(ctx context.Context, query ProjectRecordQuery) (ProjectRecordPage, error) {
	page := ProjectRecordPage{Items: []domain.Project{}, Total: -1}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return page, err
	}
	defer tx.Rollback()
	var phase int
	var omissions int64
	if err := tx.QueryRowContext(ctx, `SELECT phase,omissions FROM metadata_catalog_collections WHERE workspace_key=? AND field='projects'`, query.Workspace).Scan(&phase, &omissions); err != nil {
		return page, err
	}
	if phase != 2 || omissions != 0 {
		return page, ErrMetadataCatalogGap
	}
	rows, err := tx.QueryContext(ctx, `SELECT summary FROM metadata_catalog WHERE workspace_key=? AND field='projects' ORDER BY collection_order,record_key`, query.Workspace)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	limit := query.Limit
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	offset, matched := decodeProjectCursor(query.Cursor), 0
	predicate := query
	predicate.Cursor, predicate.Limit, predicate.IncludeTotal = "", 1, false
	for rows.Next() {
		var raw []byte
		var project domain.Project
		if err := rows.Scan(&raw); err != nil {
			return page, err
		}
		if err := json.Unmarshal(raw, &project); err != nil {
			return page, err
		}
		if len(filterProjectDirectory([]domain.Project{project}, predicate).Items) == 0 {
			continue
		}
		matched++
		if matched <= offset {
			continue
		}
		if len(page.Items) < limit {
			page.Items = append(page.Items, ProjectListProjection(project))
		} else {
			page.HasMore = true
			if !query.IncludeTotal {
				break
			}
		}
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	if query.IncludeTotal {
		page.Total = int64(matched)
	}
	if page.HasMore {
		page.NextCursor = encodeProjectCursor(offset + len(page.Items))
	}
	return page, nil
}
