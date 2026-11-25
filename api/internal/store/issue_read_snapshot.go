package store

import (
	"context"
	"database/sql"
	"errors"
)

// IssueReadSnapshot owns the policy and SQL snapshot of a record-only issue
// read. The caller checks live authorization before delivering its result.
type IssueReadSnapshot struct {
	store              *SQLiteStore
	live, ctx          context.Context
	tx                 *sqlTx
	work               *ResourceRead
	workspaceID, epoch string
}

type issueRecordSQLSnapshotKey struct{}

func issueRecordSQLSnapshot(ctx context.Context) bool {
	pinned, _ := ctx.Value(issueRecordSQLSnapshotKey{}).(bool)
	return pinned
}

func (s *SQLiteStore) BeginIssueReadSnapshot(ctx context.Context, workspace string) (_ *IssueReadSnapshot, err error) {
	if !s.BoundedMetadataEnabled() || !s.RecordJournalReady() || workspace == "" {
		return nil, ErrBoundedMetadataCoverage
	}
	read := &IssueReadSnapshot{store: s, live: ctx, work: &ResourceRead{store: s}}
	defer func() {
		if err != nil {
			read.Close()
		}
	}()
	if err := read.work.reserve(ctx, 2<<20); err != nil {
		return nil, err
	}
	read.tx, err = s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
	if err != nil {
		return nil, err
	}
	snapshot := workspaceRead{database: s.db, workspace: workspace, reader: read.tx}
	pinned := context.WithValue(ctx, workspaceReadKey{}, snapshot)
	stamp, epoch, err := s.accessStampAndEpoch(pinned, workspace)
	if err != nil {
		return nil, err
	}
	read.workspaceID, read.epoch = stamp.workspaceID, epoch
	s.mu.RLock()
	data, found := s.workspaces[workspace]
	installed, _, known := s.residentStamp(workspace)
	current := found && known && installed == stamp && data.Workspace.ID == stamp.workspaceID
	if current {
		data = issueAccessMetadata(data)
	}
	s.mu.RUnlock()
	if !current {
		// Read just resident policy at this transaction's revision. Bootstrap
		// and release-scoped queries have additional metadata dependencies.
		policy, _, _, readErr := s.readResidentWorkspacePolicy(pinned, read.tx, workspace, func(size uint64) error {
			return read.work.reserve(pinned, size)
		}, true, "teams", "teamSettings", "workspaceSettings")
		if readErr != nil {
			return nil, readErr
		}
		normalizeStoredMetadata(&policy)
		data = issueAccessMetadata(policy)
	}
	pinned = context.WithValue(ctx, workspaceReadKey{}, workspaceRead{database: s.db, workspace: workspace, reader: read.tx, metadata: data})
	read.ctx = context.WithValue(pinned, issueRecordSQLSnapshotKey{}, true)
	return read, nil
}

func (r *IssueReadSnapshot) Context() context.Context { return r.ctx }

func (r *IssueReadSnapshot) Close() {
	if r.tx != nil {
		_ = r.tx.Rollback()
		r.tx = nil
	}
	r.work.Close()
	r.ctx = nil
}

func (r *IssueReadSnapshot) End() (context.Context, error) {
	// Release the read transaction first, including on single-connection
	// SQLite stores. A query inside it would just return the old epoch.
	if r.tx == nil {
		return r.live, sql.ErrTxDone
	}
	err := r.tx.Rollback()
	r.tx = nil
	if err != nil && !errors.Is(err, sql.ErrTxDone) {
		return r.live, err
	}
	return r.live, nil
}

func (r *IssueReadSnapshot) Validate() error {
	epoch, err := r.store.AuthzEpoch(r.live, r.workspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrAuthForbidden
	}
	if err == nil && epoch != r.epoch {
		return ErrResourceAccessChanged
	}
	return err
}
