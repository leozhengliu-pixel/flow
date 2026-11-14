package store

import (
	"context"
	"database/sql"
	"sync"

	"flow/api/internal/domain"
)

// The metadata revision of this server's copy of a workspace.
//
// Servers that share a database used to decode a workspace's whole metadata
// again before every write (under the coordinator) and on every peer event,
// whether or not anything had changed: tens of MiB per decode in a large
// workspace. The database already counts the transactions that changed a
// workspace's metadata (cacheKindMetadata, cache_revisions.go): every write of
// workspace_states or workspace_metadata_records advances it by exactly one in
// the writing transaction. Two snapshots with the same count saw the same
// metadata writes, so they hold the same metadata.
//
// So each installed copy carries the revision it is exact at, when that is
// known (copyRevisions):
//
//   - A copy decoded from a read snapshot gets the revision read in the same
//     snapshot (loadWorkspaceStamped, ReloadAllWorkspaces, loadOrSeed).
//   - A reload, or a write's reload of its base, reads the revision (one
//     statement) and keeps the copy when it is still current: the decode
//     would produce the same metadata.
//   - A write that started from a copy exact at K installs its result exact
//     at K+n only when the n transactions it committed with metadata changes
//     read K, K+1, ... inside themselves, before their own increment
//     (metadataWrite). If a transaction of another writer committed in
//     between, which may not hold the workspace lock, the count read differs
//     and the copy's revision becomes unknown. If one commits after the last
//     read, the stored count passes K+n and the next check reloads. Either
//     way no copy claims a revision whose writes it lacks. The argument: a
//     transaction's snapshot and the copy's snapshot are ordered (one saw
//     every write the other saw), so equal counts mean equal sets of writes;
//     after the write's commits the stored count is K+n exactly when no other
//     transaction changed the metadata, and then the stored metadata is the
//     copy the write built from the exact base, as on a single server. (A
//     metadata transaction of the write that does not carry its context is
//     not counted in n, so it too leaves the stored count past K+n.) A write
//     that committed no metadata change keeps K; the base's revision must
//     still be the one in place when it installs (epoch).
//   - A read snapshot (BeginWorkspaceRead) at the copy's revision shares the
//     copy instead of decoding its own.
//   - Any other change of a copy (setWorkspaceLocked, installWorkspace) makes
//     its revision unknown, so the next check reloads it, as before.
//
// The workspace id is part of the stamp: a workspace deleted and created
// again under the same key restarts its count.

type copyRevision struct {
	workspaceID string
	revision    int64
	// epoch identifies this assignment: a write installs its result exact
	// only if the base's revision was not replaced meanwhile.
	epoch uint64
}

// copyStamp is a workspace's stored id and metadata revision.
type copyStamp struct {
	workspaceID string
	revision    int64
}

// WorkspaceMetadataDecodes counts the workspace metadata documents this store
// decoded from the database (diagnostics and tests). The count is per store:
// a process-wide one also counted the background reloads of other stores in
// the same process (another test's servers), so a test could not tell its
// own decodes apart.
func (s *SQLiteStore) WorkspaceMetadataDecodes() int64 { return s.metadataDecodes.Load() }

// metadataStampColumns selects a workspace_states row's stamp; its arguments
// are the workspace key and cacheKindMetadata, before the row's condition.
const metadataStampColumns = `workspace_id,(SELECT COALESCE(SUM(revision),0) FROM workspace_cache_revisions WHERE workspace_key=? AND kind=?)`

type metadataRowReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// readMetadataStamp reads the stored stamp of a workspace in one statement.
func readMetadataStamp(ctx context.Context, reader metadataRowReader, workspace string) (copyStamp, error) {
	var stamp copyStamp
	err := reader.QueryRowContext(ctx, `SELECT `+metadataStampColumns+` FROM workspace_states WHERE workspace_key=?`, workspace, cacheKindMetadata, workspace).Scan(&stamp.workspaceID, &stamp.revision)
	return stamp, err
}

// copyRevisionLocked returns the revision the installed copy is exact at;
// false when there is no copy or its revision is unknown. The caller holds mu.
func (s *SQLiteStore) copyRevisionLocked(workspace string) (copyRevision, bool) {
	if _, ok := s.workspaces[workspace]; !ok {
		return copyRevision{}, false
	}
	revision, ok := s.copyRevisions[workspace]
	return revision, ok
}

func (s *SQLiteStore) copyRevisionOf(workspace string) (copyRevision, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.copyRevisionLocked(workspace)
}

// setCopyRevisionLocked records that the installed copy is exact at stamp.
// The caller holds mu.
func (s *SQLiteStore) setCopyRevisionLocked(workspace string, stamp copyStamp) {
	if s.copyRevisions == nil {
		s.copyRevisions = map[string]copyRevision{}
	}
	s.copyRevisionEpoch++
	s.copyRevisions[workspace] = copyRevision{stamp.workspaceID, stamp.revision, s.copyRevisionEpoch}
}

// forgetCopyRevisionLocked marks the copy's revision unknown: the next check
// reloads it. The caller holds mu.
func (s *SQLiteStore) forgetCopyRevisionLocked(workspace string) {
	delete(s.copyRevisions, workspace)
}

// setWorkspaceLocked installs a copy whose revision is not known. The caller
// holds mu.
func (s *SQLiteStore) setWorkspaceLocked(workspace string, data domain.Bootstrap) {
	s.workspaces[workspace] = data
	s.forgetCopyRevisionLocked(workspace)
}

// copyIsCurrent reports whether the stored metadata is still the one the
// copy is exact at (a read error counts as changed).
func (s *SQLiteStore) copyIsCurrent(ctx context.Context, workspace string, known copyRevision, ok bool) bool {
	if !ok {
		return false
	}
	stamp, err := readMetadataStamp(ctx, s.db, workspace)
	return err == nil && stamp.workspaceID == known.workspaceID && stamp.revision == known.revision
}

// refreshWorkspaceCopy brings the copy to the stored metadata before a write
// under the coordinator: it decodes the workspace only when the metadata
// changed since the copy was made. The caller holds writeMu, not mu.
func (s *SQLiteStore) refreshWorkspaceCopy(ctx context.Context, workspace string) error {
	known, ok := s.copyRevisionOf(workspace)
	if s.copyIsCurrent(ctx, workspace, known, ok) {
		return nil
	}
	data, stamp, err := s.loadWorkspaceStamped(ctx, workspace)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.workspaces[workspace] = data
	s.setCopyRevisionLocked(workspace, stamp)
	s.mu.Unlock()
	return nil
}

// refreshWorkspaceCopyLocked is refreshWorkspaceCopy for a caller holding
// writeMu and mu (lockWorkspaceWrites).
func (s *SQLiteStore) refreshWorkspaceCopyLocked(ctx context.Context, workspace string) error {
	known, ok := s.copyRevisionLocked(workspace)
	if s.copyIsCurrent(ctx, workspace, known, ok) {
		return nil
	}
	data, stamp, err := s.loadWorkspaceStamped(ctx, workspace)
	if err != nil {
		return err
	}
	s.workspaces[workspace] = data
	s.setCopyRevisionLocked(workspace, stamp)
	return nil
}

// metadataWrite follows one write's committed metadata transactions to tell
// whether the copy it installs is exact (see the top of this file).
type metadataWrite struct {
	mu        sync.Mutex
	workspace string
	base      copyRevision
	exact     bool
	// next is the revision the copy is exact at once installed.
	next int64
}

type metadataWriteKey struct{}

// watchMetadataWrite starts following a write whose base is the installed
// copy now. The caller holds writeMu (or mu) and reads the base under the same
// hold, so the base is the copy whose revision is taken here.
func (s *SQLiteStore) watchMetadataWrite(ctx context.Context, workspace string) (context.Context, *metadataWrite) {
	s.mu.RLock()
	base, ok := s.copyRevisionLocked(workspace)
	s.mu.RUnlock()
	return s.watchMetadataWriteFrom(ctx, workspace, base, ok)
}

func (s *SQLiteStore) watchMetadataWriteFrom(ctx context.Context, workspace string, base copyRevision, ok bool) (context.Context, *metadataWrite) {
	write := &metadataWrite{workspace: workspace, base: base, exact: ok, next: base.revision}
	return context.WithValue(ctx, metadataWriteKey{}, write), write
}

// metadataWriteOf returns the write a transaction belongs to when the
// transaction changes that write's workspace metadata.
func (tx *sqlTx) metadataWriteOf() *metadataWrite {
	if tx.ctx == nil || len(tx.cacheRevisions) == 0 {
		return nil
	}
	write, _ := tx.ctx.Value(metadataWriteKey{}).(*metadataWrite)
	if write == nil || !tx.cacheRevisions[cacheRevisionKey{write.workspace, cacheKindMetadata}] {
		return nil
	}
	return write
}

// observe notes one committed transaction that read revision before its own
// increment.
func (w *metadataWrite) observe(revision int64, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err != nil || revision != w.next {
		w.exact = false
		return
	}
	w.next = revision + 1
}

func (w *metadataWrite) fail() {
	w.mu.Lock()
	w.exact = false
	w.mu.Unlock()
}

// installWrittenWorkspace installs a write's committed copy, exact at the
// revision its transactions reached when they all followed the base and the
// base's revision is still the one in place. The caller holds writeMu, not mu.
func (s *SQLiteStore) installWrittenWorkspace(workspace string, data domain.Bootstrap, last bool, write *metadataWrite) {
	s.mu.Lock()
	s.installWrittenWorkspaceLocked(workspace, data, write)
	if last {
		s.lastWorkspaceKey = workspace
	}
	s.mu.Unlock()
}

// installWrittenWorkspaceLocked is installWrittenWorkspace for a caller
// holding mu.
func (s *SQLiteStore) installWrittenWorkspaceLocked(workspace string, data domain.Bootstrap, write *metadataWrite) {
	current, ok := s.copyRevisionLocked(workspace)
	s.workspaces[workspace] = data
	if s.coldWrite != nil {
		s.installColdFullWriteLocked(workspace, write, current, ok)
	}
	if write == nil || write.workspace != workspace || !ok || current.epoch != write.base.epoch {
		s.forgetCopyRevisionLocked(workspace)
		return
	}
	write.mu.Lock()
	exact, next := write.exact, write.next
	write.mu.Unlock()
	if !exact {
		s.forgetCopyRevisionLocked(workspace)
		return
	}
	s.setCopyRevisionLocked(workspace, copyStamp{write.base.workspaceID, next})
}
