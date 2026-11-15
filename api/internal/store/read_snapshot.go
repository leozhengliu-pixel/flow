package store

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"flow/api/internal/domain"
)

type workspaceReadKey struct{}
type workspaceRead struct {
	database  *sqlDatabase
	workspace string
	reader    *sqlTx
	metadata  domain.Bootstrap
}

func beginWorkspaceReadCapture(ctx context.Context) (context.Context, func(bool)) {
	parent, _ := ctx.Value(hydrationSnapshotReuseKey{}).(*hydrationSnapshotReuse)
	if parent == nil || !RecordHydrationEnabled() {
		return ctx, func(bool) {}
	}
	child := &hydrationSnapshotReuse{}
	return context.WithValue(ctx, hydrationSnapshotReuseKey{}, child), func(keep bool) {
		child.mu.Lock()
		if child.closed {
			child.mu.Unlock()
			return
		}
		child.closed = true
		buffers := child.buffers
		child.buffers = nil
		child.mu.Unlock()
		if keep {
			parent.mu.Lock()
			if !parent.closed {
				parent.buffers = append(parent.buffers, buffers...)
			}
			parent.mu.Unlock()
			// A closed parent cannot own returned values. Leave those
			// buffers live and unpooled for their caller and the GC.
			return
		}
		for _, buffer := range buffers {
			sharedHydrationSnapshotBuffers.put(buffer)
		}
	}
}

// Keep metadata, outlines, and streamed bodies in the same SQL read snapshot.
// The caller must release it when writing finishes or the request is cancelled.
func (s *SQLiteStore) BeginWorkspaceRead(ctx context.Context, workspace string) (context.Context, func(), error) {
	scope, scoped := metadataRecordScopeFromContext(ctx)
	if s.metadataResidency != nil && !scoped {
		return ctx, nil, fmt.Errorf("%w: an unscoped workspace read must use the compatibility stream", ErrBoundedMetadataCoverage)
	}
	if scoped {
		if err := validateMetadataRecordScope(scope); err != nil {
			return ctx, nil, err
		}
	}
	if snapshot, ok := ctx.Value(workspaceReadKey{}).(workspaceRead); ok && snapshot.database == s.db {
		return ctx, nil, fmt.Errorf("cannot start a transaction inside a workspace read snapshot")
	}
	budgetRelease := func() {}
	if s.metadataResidency != nil {
		var err error
		ctx, budgetRelease, err = s.metadataResidency.work.acquire(ctx, 2<<20)
		if err != nil {
			return ctx, nil, err
		}
	}
	budgetRelease = sync.OnceFunc(budgetRelease)
	baseCtx := ctx
	for {
		if err := baseCtx.Err(); err != nil {
			budgetRelease()
			return baseCtx, nil, err
		}
		captureCtx, finishCapture := beginWorkspaceReadCapture(baseCtx)
		var installed domain.Bootstrap
		var installedPolicy domain.Bootstrap
		var known copyRevision
		var installedOK, knownOK bool
		var publication uint64
		var compatibility *resourceReadCompatibilitySnapshot
		if !scoped || workspace == "" || s.metadataResidency == nil {
			// Never wait on the store mutex while owning a SQL connection:
			// a writer can hold it while waiting for this pool.
			s.mu.RLock()
			if err := baseCtx.Err(); err != nil {
				s.mu.RUnlock()
				finishCapture(false)
				budgetRelease()
				return baseCtx, nil, err
			}
			if workspace == "" {
				workspace = s.lastWorkspaceKey
			}
			publication = s.copyRevisionEpoch
			known, knownOK = s.copyRevisionLocked(workspace)
			if !scoped && s.metadataResidency == nil {
				installed, installedOK = s.workspaces[workspace]
				if installedOK && knownOK {
					installed = CloneHydratedReadSnapshot(captureCtx, installed)
					compatibility = &resourceReadCompatibilitySnapshot{database: s.db, workspace: workspace, data: domain.Bootstrap{
						Workspace: installed.Workspace, Viewer: installed.Viewer, Projects: installed.Projects, Initiatives: installed.Initiatives,
					}}
				} else if installedOK {
					digests, err := s.resourceReadCompatibilityDigestsLocked(baseCtx, workspace, installed)
					if err != nil {
						s.mu.RUnlock()
						finishCapture(false)
						budgetRelease()
						return baseCtx, nil, err
					}
					compatibility = &resourceReadCompatibilitySnapshot{database: s.db, workspace: workspace, digests: digests, data: cloneBootstrap(domain.Bootstrap{
						Workspace: installed.Workspace, Viewer: installed.Viewer,
					})}
				}
			} else if s.metadataResidency == nil {
				if source, ok := s.workspaces[workspace]; ok {
					installedOK = true
					if knownOK {
						installedPolicy = resourcePolicyMetadata(source)
					}
					digests, err := s.resourceReadCompatibilityDigestsLocked(baseCtx, workspace, source)
					if err != nil {
						s.mu.RUnlock()
						finishCapture(false)
						budgetRelease()
						return baseCtx, nil, err
					}
					compatibility = &resourceReadCompatibilitySnapshot{database: s.db, workspace: workspace, digests: digests, data: cloneBootstrap(domain.Bootstrap{
						Workspace: source.Workspace, Viewer: source.Viewer,
					})}
				}
			}
			s.mu.RUnlock()
		}
		ctx := baseCtx
		if compatibility != nil {
			ctx = context.WithValue(ctx, resourceReadCompatibilityKey{}, *compatibility)
		}
		tx, err := s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
		if err != nil {
			finishCapture(false)
			budgetRelease()
			return baseCtx, nil, err
		}
		abort := func(err error) (context.Context, func(), error) {
			_ = tx.Rollback()
			finishCapture(false)
			budgetRelease()
			return baseCtx, nil, err
		}
		release := func() {
			_ = tx.Rollback()
			budgetRelease()
		}
		stamp, err := readMetadataStamp(ctx, tx, workspace)
		if err != nil {
			return abort(err)
		}
		current := s.metadataResidency == nil && installedOK && knownOK && known.workspaceID == stamp.workspaceID && known.revision == stamp.revision
		if s.metadataResidency == nil && !current {
			// A newer local publication must be recaptured. An external SQL
			// write alone retains the installed compatibility representation.
			unchanged := false
			if s.mu.TryRLock() {
				unchanged = publication == s.copyRevisionEpoch
				s.mu.RUnlock()
			}
			if !unchanged {
				_ = tx.Rollback()
				finishCapture(false)
				continue
			}
		}
		if current && !scoped {
			finishCapture(true)
			ctx = context.WithValue(ctx, resourcePolicySnapshotKey{}, resourcePolicySnapshot{tx, workspace, stamp, resourcePolicyMetadata(installed)})
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
			return abort(readErr)
		}
		if len(raw) > s.maxStateBytes {
			return abort(fmt.Errorf("workspace state exceeds %d bytes", s.maxStateBytes))
		}
		data, err := s.decodeWorkspaceMetadata(ctx, workspace, raw, tx)
		if err != nil {
			return abort(err)
		}
		if !scoped {
			normalizeStoredMetadata(&data)
		}
		finishCapture(true)
		if !scoped {
			ctx = context.WithValue(ctx, resourcePolicySnapshotKey{}, resourcePolicySnapshot{tx, workspace, stamp, resourcePolicyMetadata(data)})
		} else if current {
			ctx = context.WithValue(ctx, resourcePolicySnapshotKey{}, resourcePolicySnapshot{tx, workspace, stamp, installedPolicy})
		}
		return context.WithValue(ctx, workspaceReadKey{}, workspaceRead{s.db, workspace, tx, data}), release, nil
	}
}

func (s *SQLiteStore) workspaceReadSource(ctx context.Context, workspace string) (metadataReader, domain.Bootstrap, bool) {
	if snapshot, ok := ctx.Value(workspaceReadKey{}).(workspaceRead); ok && snapshot.database == s.db && (workspace == "" || workspace == snapshot.workspace) {
		data := CloneHydratedReadSnapshot(ctx, snapshot.metadata)
		stripTrashLinks(&data)
		return snapshot.reader, data, true
	}
	if s.metadataResidency != nil {
		return s.db, domain.Bootstrap{}, false
	}
	data, ok := s.WorkspaceMetadataForRead(ctx, workspace)
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
