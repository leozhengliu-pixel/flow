package store

import (
	"context"
	"database/sql"
	"errors"

	"flow/api/internal/domain"
)

// The full writer's lazy services capture the database reader before callback
// execution. They do not share a hydration or eventual write transaction.
func newLegacyIssueEditorLookups(ctx context.Context, inputs *issueHotInputs, workspace, workspaceID string, target domain.Issue) (_ *issueEditorLookups, err error) {
	if inputs == nil || inputs.store == nil || inputs.store.db == nil || inputs.source == nil {
		return nil, ErrMetadataRecordQuery
	}
	read := &issueEditorLookups{
		ctx: ctx, store: inputs.store, reader: inputs.store.db, legacy: true,
		source: inputs.source, workspace: workspace, workspaceID: workspaceID,
		documentID: "document_content_" + target.ID,
	}
	if target.DocumentContent != nil && target.DocumentContent.ID != "" {
		read.documentID = target.DocumentContent.ID
	}
	if err := read.reserve(4096); err != nil {
		return nil, err
	}
	if err := read.source.Retain(); err != nil {
		read.source = nil
		read.close()
		return nil, err
	}
	return read, nil
}

func (r *issueEditorLookups) legacyFailure(err error) error {
	if r.ctx.Err() != nil {
		return r.failure(r.ctx.Err())
	}
	if errors.Is(err, ErrMetadataWorkBudget) || errors.Is(err, ErrMetadataRecordQuery) ||
		errors.Is(err, ErrBoundedMetadataCoverage) || errors.Is(err, errMetadataLogicalClosed) {
		return r.failure(err)
	}
	return err
}

func (r *issueEditorLookups) legacySizes(query string, args ...any) (count, size int64, err error) {
	rows, err := r.reader.QueryContext(r.ctx, query, args...)
	if err != nil {
		return 0, 0, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return 0, 0, err
		}
		return 0, 0, sql.ErrNoRows
	}
	if err := rows.Scan(&count, &size); err != nil {
		return 0, 0, err
	}
	return count, size, rows.Err()
}

func (r *issueEditorLookups) loadLegacyFormer() error {
	if r.workspaceID == "" {
		return nil
	}
	rows, err := r.reader.QueryContext(r.ctx, `SELECT 1 FROM workspace_memberships WHERE workspace_id=? LIMIT 1`, r.workspaceID)
	if err != nil {
		return err
	}
	hasMemberships := rows.Next()
	if err := rows.Close(); err != nil {
		return err
	}
	if !hasMemberships {
		// The original lookup also falls back when the existence cursor
		// reaches EOF with an ordinary driver error.
		if err := r.loadNativeFormer(); err != nil {
			return r.failure(err)
		}
		return nil
	}
	length := r.bytes("user_id")
	from := " FROM (" + formerMembersQuery + ") legacy_former"
	count, size, err := r.legacySizes("SELECT COUNT(*),COALESCE(SUM("+length+"),0)"+from, r.workspaceID, r.workspaceID)
	if err != nil {
		return err
	}
	if count < 0 || size < 0 || uint64(count) > (metadataWorkBytes-(64<<10))/512 ||
		uint64(size) > (metadataWorkBytes-(64<<10)-uint64(count)*512)/8 {
		return ErrMetadataWorkBudget
	}
	if err := r.reserve(uint64(count)*512 + uint64(size)*8 + 64<<10); err != nil {
		return err
	}
	rows, err = r.reader.QueryContext(r.ctx,
		"SELECT CASE WHEN "+length+"<=? THEN user_id ELSE NULL END,"+length+from,
		size, r.workspaceID, r.workspaceID)
	if err != nil {
		return err
	}
	defer rows.Close()
	var actual, actualBytes uint64
	for rows.Next() {
		var id sql.NullString
		var bytes sql.NullInt64
		if err := rows.Scan(&id, &bytes); err != nil {
			return err
		}
		if !id.Valid || !bytes.Valid || bytes.Int64 < 0 || bytes.Int64 > size || int64(len(id.String)) != bytes.Int64 {
			return r.failure(ErrMetadataRecordQuery)
		}
		actual++
		actualBytes += uint64(bytes.Int64)
		if actual > uint64(count) || actualBytes > uint64(size) {
			return r.failure(ErrMetadataRecordQuery)
		}
		r.former[id.String] = true
	}
	return rows.Err()
}

// Unlike the scoped editor service, the literal full callback accepts any
// document ID and reads fresh ordered rows on every invocation.
func (r *issueEditorLookups) legacyCollaborationUpdates(documentID string) ([][]byte, error) {
	if err := r.check(); err != nil {
		return nil, err
	}
	if err := r.reserve(4096); err != nil {
		return nil, r.failure(err)
	}
	length := r.bytes("update_data")
	from := " FROM document_collaboration_updates WHERE workspace_key=? AND document_id=?"
	count, size, err := r.legacySizes("SELECT COUNT(*),COALESCE(SUM("+length+"),0)"+from, r.workspace, documentID)
	if err != nil {
		return nil, r.legacyFailure(err)
	}
	if count < 0 || size < 0 || uint64(count) > (metadataWorkBytes-(64<<10))/256 ||
		uint64(size) > (metadataWorkBytes-(64<<10)-uint64(count)*256)/4 {
		return nil, r.failure(ErrMetadataWorkBudget)
	}
	if err := r.reserve(uint64(count)*256 + uint64(size)*4 + 64<<10); err != nil {
		return nil, r.failure(err)
	}
	rows, err := r.reader.QueryContext(r.ctx,
		"SELECT CASE WHEN "+length+"<=? THEN update_data ELSE NULL END,"+length+from+" ORDER BY created_at,update_id",
		size, r.workspace, documentID)
	if err != nil {
		return nil, r.legacyFailure(err)
	}
	defer rows.Close()
	var updates [][]byte
	var actual, actualBytes uint64
	for rows.Next() {
		var update []byte
		var bytes sql.NullInt64
		if err := rows.Scan(&update, &bytes); err != nil {
			return nil, r.legacyFailure(err)
		}
		if bytes.Valid {
			if bytes.Int64 < 0 || bytes.Int64 > size || int64(len(update)) != bytes.Int64 {
				return nil, r.failure(ErrMetadataRecordQuery)
			}
			actualBytes += uint64(bytes.Int64)
		} else if update != nil {
			return nil, r.failure(ErrMetadataRecordQuery)
		}
		actual++
		if actual > uint64(count) || actualBytes > uint64(size) {
			return nil, r.failure(ErrMetadataRecordQuery)
		}
		updates = append(updates, update)
	}
	// Preserve a partial EOF result and its ordinary driver error. Returned
	// mutable rows remain charged even when the callback ignores that error.
	r.legacyUpdates = append(r.legacyUpdates, updates)
	return updates, r.legacyFailure(rows.Err())
}
