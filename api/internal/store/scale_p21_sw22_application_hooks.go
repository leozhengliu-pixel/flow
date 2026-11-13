package store

import "context"

// ApplicationWebhookWorkspaceCount counts installed webhook workspaces in the
// store's current catalog. Only scalar predicates enter the application working
// set, even when a matching webhook has a large description or payload.
func (s *SQLiteStore) ApplicationWebhookWorkspaceCount(ctx context.Context, clientID string) (int, error) {
	// Resident roots cost at least 512 bytes each. Their key slice is therefore
	// at most 6 MiB under the 192 MiB resident cap, plus a bounded SQL group.
	ctx, release, err := s.admitMetadataWork(ctx, 8<<20)
	if err != nil {
		return 0, err
	}
	defer release()
	keys := s.WorkspaceKeys()
	count := 0
	for start := 0; start < len(keys); start += MetadataRecordBatchMaxKeys {
		chunk := keys[start:min(start+MetadataRecordBatchMaxKeys, len(keys))]
		where, values := bindList("r.workspace_key", chunk)
		args := append([]any{"application", clientID}, values...)
		var found int
		err := s.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT r.workspace_key)
			FROM workspace_metadata_records r JOIN workspace_states w ON w.workspace_key=r.workspace_key
			WHERE r.field='webhooks' AND `+s.metadataRecordKeyEquals(s.jsonText("r.data", "managedBy"), "?")+`
			AND `+s.metadataRecordKeyEquals("COALESCE("+s.jsonText("r.data", "applicationClientId")+",'')", "?")+`
			AND `+where, args...).Scan(&found)
		if err != nil {
			return 0, err
		}
		count += found
	}
	return count, nil
}
