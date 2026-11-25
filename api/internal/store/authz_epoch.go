package store

import (
	"context"
	"strconv"
)

func readAuthzEpoch(ctx context.Context, reader metadataRowReader, workspaceID string) (string, error) {
	var epoch int64
	var generation string
	var enabled int
	err := reader.QueryRowContext(ctx, `SELECT COALESCE((SELECT epoch FROM workspace_authz_epochs a WHERE a.workspace_id=`+recordJournalIdentity(reader, "w.workspace_id")+`),0),m.generation,m.enabled FROM workspace_states w CROSS JOIN record_journal_mode m WHERE w.workspace_id=? AND m.id=1`, workspaceID).Scan(&epoch, &generation, &enabled)
	if err == nil && enabled == 0 {
		err = ErrRecordJournalUnavailable
	}
	return generation + ":" + strconv.FormatInt(epoch, 10), err
}

// AuthzEpoch always checks the live authority, including when a caller holds
// an older record-page snapshot. Consumers must stop sensitive delivery if
// this read fails. Epochs are workspace-wide; effective credential scope is
// still a separate part of every AccessContext/cache key.
func (s *SQLiteStore) AuthzEpoch(ctx context.Context, workspaceID string) (string, error) {
	if !s.db.journalReady.Load() {
		return "", ErrRecordJournalUnavailable
	}
	if snapshot, ok := ctx.Value(workspaceReadKey{}).(workspaceRead); ok && snapshot.database == s.db && s.db.Stats().MaxOpenConnections == 1 {
		// A separate live read cannot acquire SQLite's sole connection until
		// this caller releases its snapshot. Stop instead of deadlocking or
		// treating the older snapshot's epoch as current authority.
		return "", ErrRecordJournalUnavailable
	}
	ctx = context.WithValue(ctx, workspaceReadKey{}, workspaceRead{})
	return readAuthzEpoch(ctx, s.db, workspaceID)
}

func (s *SQLiteStore) accessStampAndEpoch(ctx context.Context, workspace string) (copyStamp, string, error) {
	var stamp copyStamp
	if !s.db.journalReady.Load() {
		err := s.db.QueryRowContext(ctx, `SELECT `+metadataStampColumns+` FROM workspace_states WHERE workspace_key=?`, workspace, cacheKindMetadata, workspace).Scan(&stamp.workspaceID, &stamp.revision)
		return stamp, "", err
	}
	var epoch int64
	var generation string
	var enabled int
	err := s.db.QueryRowContext(ctx, `SELECT `+metadataStampColumns+`,COALESCE((SELECT epoch FROM workspace_authz_epochs a WHERE a.workspace_id=`+recordJournalIdentitySQL(s.dialect, "workspace_states.workspace_id")+`),0),m.generation,m.enabled FROM workspace_states CROSS JOIN record_journal_mode m WHERE workspace_key=? AND m.id=1`, workspace, cacheKindMetadata, workspace).Scan(&stamp.workspaceID, &stamp.revision, &epoch, &generation, &enabled)
	if err == nil && enabled == 0 {
		err = ErrRecordJournalUnavailable
	}
	return stamp, generation + ":" + strconv.FormatInt(epoch, 10), err
}
