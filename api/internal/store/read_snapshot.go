package store

import (
	"context"
	"fmt"
	"slices"

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
	scope, scoped := metadataRecordScopeFromContext(ctx)
	if scoped {
		if err := validateMetadataRecordScope(scope); err != nil {
			return ctx, nil, err
		}
	}
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
	// This server's copy, when it is exact at the snapshot's metadata
	// revision, is the snapshot's metadata (metadata_copy_revision.go):
	// copy it instead of decoding the workspace again. The copy is taken
	// under the read lock: team writes edit the installed copy in place
	// while they hold the write lock (mutateTeamMetadata), and the snapshot
	// outlives this call.
	stamp, err := readMetadataStamp(ctx, tx, workspace)
	if err != nil {
		release()
		return ctx, nil, err
	}
	s.mu.RLock()
	installed, installedOK := s.workspaces[workspace]
	known, knownOK := s.copyRevisionLocked(workspace)
	current := installedOK && knownOK && known.workspaceID == stamp.workspaceID && known.revision == stamp.revision
	if current && !scoped {
		installed = cloneBootstrap(installed)
	}
	s.mu.RUnlock()
	if current && !scoped {
		return context.WithValue(ctx, workspaceReadKey{}, workspaceRead{s.db, workspace, tx, installed}), release, nil
	}
	var raw []byte
	var readErr error
	if scoped {
		var size int
		readErr = tx.QueryRowContext(ctx, `SELECT LENGTH(data),CASE WHEN LENGTH(data)<=? THEN data ELSE NULL END FROM workspace_states WHERE workspace_key=?`, MetadataRecordPageMaxBytes, workspace).Scan(&size, &raw)
		if readErr == nil && (size > MetadataRecordPageMaxBytes || raw == nil) {
			readErr = ErrMetadataPageBudget
		}
	} else {
		readErr = tx.QueryRowContext(ctx, `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&raw)
	}
	if readErr != nil {
		release()
		return ctx, nil, readErr
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
	if !scoped {
		normalizeStoredMetadata(&data)
	}
	return context.WithValue(ctx, workspaceReadKey{}, workspaceRead{s.db, workspace, tx, data}), release, nil
}

func (s *SQLiteStore) workspaceReadSource(ctx context.Context, workspace string) (metadataReader, domain.Bootstrap, bool) {
	if snapshot, ok := ctx.Value(workspaceReadKey{}).(workspaceRead); ok && snapshot.database == s.db && (workspace == "" || workspace == snapshot.workspace) {
		data := cloneBootstrap(snapshot.metadata)
		stripTrashLinks(&data)
		return snapshot.reader, data, true
	}
	data, ok := s.WorkspaceMetadata(workspace)
	stripTrashLinks(&data)
	return s.db, data, ok
}

// stripTrashLinks drops the trash entries' restore links from a read
// projection: they are server-side bookkeeping and can name other members'
// favorites and subscriptions.
func stripTrashLinks(data *domain.Bootstrap) {
	if !slices.ContainsFunc(data.Trash, func(item domain.TrashEntry) bool { return item.Links != nil }) {
		return
	}
	trash := make([]domain.TrashEntry, len(data.Trash))
	for index, item := range data.Trash {
		item.Links = nil
		trash[index] = item
	}
	data.Trash = trash
}
