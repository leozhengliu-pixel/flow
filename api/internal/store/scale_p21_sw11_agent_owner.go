package store

import "context"

// AgentSessionOwnedBy is a trusted ownership predicate, not an authorization
// boundary. Only the two scalar fields enter the application working set; a
// session's transcript may exceed the ordinary detail-record budget.
// A missing workspace returns sql.ErrNoRows and a missing session returns false.
func (s *SQLiteStore) AgentSessionOwnedBy(ctx context.Context, workspace, sessionID, userID string) (bool, error) {
	ctx, release, err := s.admitMetadataWork(ctx, 2<<20)
	if err != nil {
		return false, err
	}
	defer release()
	if workspace == "" {
		s.mu.RLock()
		workspace = s.lastWorkspaceKey
		s.mu.RUnlock()
	}
	owner := "COALESCE(" + s.jsonText("r.data", "userId") + ",'')"
	var owned int
	err = s.db.QueryRowContext(ctx, `SELECT CASE WHEN r.record_key IS NOT NULL AND `+s.metadataRecordKeyEquals(owner, "?")+` AND `+s.jsonText("r.data", "deletedAt")+` IS NULL THEN 1 ELSE 0 END
		FROM workspace_states w LEFT JOIN workspace_metadata_records r ON r.workspace_key=w.workspace_key AND r.field='agentSessions' AND `+s.metadataRecordKeyEquals("r.record_key", "?")+`
		WHERE w.workspace_key=?`, userID, sessionID, workspace).Scan(&owned)
	return owned != 0, err
}
