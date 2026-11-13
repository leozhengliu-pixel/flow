package store

import "context"

// AgentSessionHasDraftLoop is a trusted exact-owner predicate. It transfers no
// transcript or loop body, including when either exceeds the detail budget.
// Callers retain their existing run authorization and deleted-session rules.
func (s *SQLiteStore) AgentSessionHasDraftLoop(ctx context.Context, workspace, sessionID, userID string) (bool, error) {
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
	linked, linkID := "json_each(chat.data, '$.loopIds') linked", "linked.value"
	switch s.dialect {
	case "mysql":
		linked = "JSON_TABLE(COALESCE(JSON_EXTRACT(CONVERT(chat.data USING utf8mb4), '$.loopIds'),'[]'), '$[*]' COLUMNS(loop_id LONGTEXT PATH '$')) linked"
		linkID = "linked.loop_id"
	case "postgres":
		linked = "jsonb_array_elements_text(COALESCE((" + s.jsonText("chat.data", "loopIds") + ")::jsonb,'[]'::jsonb)) linked(loop_id)"
		linkID = "linked.loop_id"
	}
	owner := "COALESCE(" + s.jsonText("chat.data", "userId") + ",'')"
	if s.dialect == "mysql" {
		owner = "COALESCE(JSON_UNQUOTE(JSON_EXTRACT(CONVERT(chat.data USING utf8mb4), '$.userId')),'')"
	}
	var draft int
	err = s.db.QueryRowContext(ctx, `SELECT CASE WHEN EXISTS(
		SELECT 1 FROM workspace_metadata_records chat
		JOIN `+linked+` ON TRUE
		JOIN workspace_metadata_records candidate ON candidate.workspace_key=chat.workspace_key AND candidate.field='loops' AND `+s.metadataRecordKeyEquals("candidate.record_key", linkID)+`
		WHERE `+s.metadataRecordKeyEquals("chat.workspace_key", "?")+` AND chat.field='agentSessions' AND `+s.metadataRecordKeyEquals("chat.record_key", "?")+`
			AND `+s.metadataRecordKeyEquals(owner, "?")+` AND `+s.metadataRecordKeyEquals(s.jsonText("candidate.data", "status"), "'draft'")+`
	) THEN 1 ELSE 0 END`, workspace, sessionID, userID).Scan(&draft)
	return draft != 0, err
}
