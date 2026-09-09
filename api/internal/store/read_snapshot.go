package store

import (
	"context"
	"fmt"

	"flow/api/internal/domain"
)

type workspaceReadKey struct{}
type workspaceRead struct {
	database  *sqlDatabase
	workspace string
	reader    *sqlTx
	metadata  domain.Bootstrap
}

// Keep metadata, outlines, and streamed bodies in the same SQL read snapshot.
// The caller must release it when writing finishes or the request is cancelled.
func (s *SQLiteStore) BeginWorkspaceRead(ctx context.Context, workspace string) (context.Context, func(), error) {
	if workspace == "" {
		s.mu.RLock()
		workspace = s.lastWorkspaceKey
		s.mu.RUnlock()
	}
	tx, err := s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
	if err != nil {
		return ctx, nil, err
	}
	release := func() { _ = tx.Rollback() }
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&raw); err != nil {
		release()
		return ctx, nil, err
	}
	if len(raw) > s.maxStateBytes {
		release()
		return ctx, nil, fmt.Errorf("workspace state exceeds %d bytes", s.maxStateBytes)
	}
	data, err := s.decodeWorkspaceMetadata(ctx, workspace, raw, tx)
	if err != nil {
		release()
		return ctx, nil, err
	}
	normalizeStoredMetadata(&data)
	return context.WithValue(ctx, workspaceReadKey{}, workspaceRead{s.db, workspace, tx, data}), release, nil
}

func (s *SQLiteStore) workspaceReadSource(ctx context.Context, workspace string) (metadataReader, domain.Bootstrap, bool) {
	if snapshot, ok := ctx.Value(workspaceReadKey{}).(workspaceRead); ok && (workspace == "" || workspace == snapshot.workspace) {
		return snapshot.reader, cloneBootstrap(snapshot.metadata), true
	}
	data, ok := s.WorkspaceMetadata(workspace)
	return s.db, data, ok
}
