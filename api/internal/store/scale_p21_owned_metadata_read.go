package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"

	"flow/api/internal/domain"
)

const metadataOwnedSelectionWork = 8*(2<<20) + 64<<10

// InstalledMetadataRead pins the exact installed logical generation. It is a
// trusted storage lifetime, not authorization or an SQL snapshot. Keep it open
// through the final response write or close each returned selection earlier.
type InstalledMetadataRead struct {
	mu         sync.Mutex
	store      *SQLiteStore
	workspace  string
	source     *metadataLogicalSource
	release    func()
	selections map[*OwnedMetadataSelection]struct{}
	closed     bool
}

// OwnedMetadataSelection owns Data and its complete processing/encoding
// allowance until Close. Data must not escape that lifetime.
type OwnedMetadataSelection struct {
	Data    domain.Bootstrap
	origin  metadataLogicalOrigin
	mu      sync.Mutex
	owner   *InstalledMetadataRead
	release func()
	closed  bool
}

// BeginInstalledMetadataRead preserves WorkspaceMetadataForRead's installed
// source choice. Empty workspace resolves the default with the source under
// the same lock. An unknown revision remains unknown; no SQL stamp is invented.
func (s *SQLiteStore) BeginInstalledMetadataRead(ctx context.Context, workspace string) (*InstalledMetadataRead, bool, error) {
	if !s.BoundedMetadataEnabled() {
		// Callers preserve their particular literal accessor off mode. This
		// helper cannot infer whether that accessor was full or field-only.
		return nil, false, ErrBoundedMetadataCoverage
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		s.mu.RLock()
		selected := workspace
		if selected == "" {
			selected = s.lastWorkspaceKey
		}
		_, found := s.workspaces[selected]
		cost := 64<<10 + len(selected)*8
		s.mu.RUnlock()
		if !found {
			return nil, false, nil
		}
		if cost > 2<<20 {
			return nil, true, ErrMetadataWorkBudget
		}
		_, release, err := s.tryAdmitAdditionalMetadataWork(ctx, cost)
		if err != nil {
			return nil, true, err
		}
		s.mu.RLock()
		selected = workspace
		if selected == "" {
			selected = s.lastWorkspaceKey
		}
		_, found = s.workspaces[selected]
		if !found || 64<<10+len(selected)*8 > cost {
			s.mu.RUnlock()
			release()
			if !found {
				return nil, false, nil
			}
			continue
		}
		r := s.metadataResidency
		r.mu.Lock()
		source := r.logical[selected]
		if source == nil {
			err = ErrBoundedMetadataCoverage
		} else {
			err = source.Retain()
		}
		r.mu.Unlock()
		selected = strings.Clone(selected)
		s.mu.RUnlock()
		if err != nil || ctx.Err() != nil {
			if err == nil {
				source.Release()
				err = ctx.Err()
			}
			release()
			return nil, true, err
		}
		return &InstalledMetadataRead{
			store: s, workspace: selected, source: source,
			release: release, selections: map[*OwnedMetadataSelection]struct{}{},
		}, true, nil
	}
}

// Context binds this generation without changing deadlines, cancellation,
// principals, or credentials. Borrowed parent work is cleared: this handle is
// charged independently, and nested authority/read helpers admit their own
// work instead of borrowing its deliberately small descriptor allowance.
func (r *InstalledMetadataRead) Context(ctx context.Context) (context.Context, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, errMetadataLogicalClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	return context.WithValue(ctx, metadataLogicalReadKey{}, metadataLogicalRead{
		database: r.store.db, workspace: r.workspace, source: r.source, owner: r,
	}), nil
}

func (r *InstalledMetadataRead) retainSource(ctx context.Context, workspace string) (*metadataLogicalSource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, errMetadataLogicalClosed
	}
	if workspace != r.workspace || r.source == nil {
		return nil, ErrMetadataRecordQuery
	}
	if err := r.source.Retain(); err != nil {
		return nil, err
	}
	return r.source, nil
}

func (r *InstalledMetadataRead) Select(ctx context.Context, scope MetadataRecordScope) (*OwnedMetadataSelection, error) {
	if err := validateMetadataRecordScope(scope); err != nil {
		return nil, err
	}
	bound, err := r.Context(ctx)
	if err != nil {
		return nil, err
	}
	source, err := r.retainSource(bound, r.workspace)
	if err != nil {
		return nil, err
	}
	defer source.Release()
	work, release, err := r.store.tryAdmitAdditionalMetadataWork(bound, metadataOwnedSelectionWork)
	if err != nil {
		return nil, err
	}
	data, err := source.Select(work, scope, 2<<20)
	if errors.Is(err, ErrMetadataWorkBudget) {
		err = ErrMetadataPageBudget
	}
	if err == nil {
		err = validateMetadataLogicalSelection(data, scope)
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		release()
		return nil, err
	}
	selection := &OwnedMetadataSelection{
		Data: data, owner: r, release: release,
		origin: metadataLogicalOrigin{workspace: r.workspace, stamp: source.Stamp()},
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		selection.Close()
		return nil, errMetadataLogicalClosed
	}
	r.selections[selection] = struct{}{}
	r.mu.Unlock()
	return selection, nil
}

func (r *InstalledMetadataRead) Close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	source, release, selections := r.source, r.release, r.selections
	r.source, r.release, r.selections = nil, nil, nil
	r.mu.Unlock()
	for selection := range selections {
		selection.Close()
	}
	if source != nil {
		source.Release()
	}
	if release != nil {
		release()
	}
}

func (s *OwnedMetadataSelection) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	owner, release := s.owner, s.release
	s.Data, s.owner, s.release = domain.Bootstrap{}, nil, nil
	s.mu.Unlock()
	if owner != nil {
		owner.mu.Lock()
		delete(owner.selections, s)
		owner.mu.Unlock()
	}
	if release != nil {
		release()
	}
}

func installedMetadataReadFromContext(ctx context.Context, s *SQLiteStore, workspace string) (*InstalledMetadataRead, bool, error) {
	if writing, _ := ctx.Value(scopedMetadataWriteKey{}).(bool); writing {
		return nil, false, nil
	}
	read, found := ctx.Value(metadataLogicalReadKey{}).(metadataLogicalRead)
	if !found {
		return nil, false, nil
	}
	if read.database != s.db || workspace != "" && read.workspace != workspace || read.owner == nil {
		return nil, true, ErrMetadataRecordQuery
	}
	return read.owner, true, nil
}

func (r *InstalledMetadataRead) validateWorkspaceIdentity(ctx context.Context, workspaceID string) error {
	source, err := r.retainSource(ctx, r.workspace)
	if err != nil {
		return err
	}
	defer source.Release()
	return r.store.validateMetadataLogicalOrigin(ctx, metadataLogicalOrigin{
		workspace: r.workspace, stamp: source.Stamp(),
	}, workspaceID)
}

type metadataLogicalOrigin struct {
	workspace string
	stamp     copyStamp
}

func (s *SQLiteStore) validateMetadataLogicalOrigin(ctx context.Context, origin metadataLogicalOrigin, workspaceID string) error {
	if origin.stamp.workspaceID == workspaceID && workspaceID != "" {
		return nil
	}
	if origin.stamp.workspaceID == "" || workspaceID == "" {
		return ErrResourceAccessChanged
	}
	// Preserve the original database's identity comparison, including its
	// collation and Postgres text normalization, on this exceptional path.
	var matched int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM workspace_states WHERE workspace_key=? AND workspace_id=? AND workspace_id=?`,
		origin.workspace, origin.stamp.workspaceID, workspaceID).Scan(&matched)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrResourceAccessChanged
	}
	return err
}
