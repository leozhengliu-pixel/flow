package store

import (
	"context"
	"strings"
)

// LoopRecordExists is a trusted exact-ID predicate. It does not authorize a
// viewer and never transfers loop instructions or other metadata bodies.
func (s *SQLiteStore) LoopRecordExists(ctx context.Context, workspace, id string) (bool, error) {
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
	var found int
	inline := `EXISTS (SELECT 1 FROM json_each(w.data,'$.loops') item WHERE ` + s.metadataRecordKeyEquals("json_extract(item.value,'$.id')", "?") + `)`
	switch s.dialect {
	case "mysql":
		inline = `JSON_CONTAINS(JSON_EXTRACT(CONVERT(w.data USING utf8mb4),'$.loops'),JSON_OBJECT('id',?))`
	case "postgres":
		value := `convert_from(w.data,'UTF8')::jsonb->'loops'`
		inline = `EXISTS (SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(` + value + `)='array' THEN ` + value + ` ELSE '[]'::jsonb END) item WHERE ` + s.metadataRecordKeyEquals("item->>'id'", "?") + `)`
	}
	err = s.db.QueryRowContext(ctx, `SELECT CASE WHEN r.record_key IS NOT NULL OR `+inline+` THEN 1 ELSE 0 END
		FROM workspace_states w LEFT JOIN workspace_metadata_records r
		ON r.workspace_key=w.workspace_key AND r.field='loops' AND `+s.metadataRecordKeyEquals("r.record_key", "?")+`
		WHERE w.workspace_key=?`, id, id, workspace).Scan(&found)
	return found != 0, err
}

// LoopTeamKeyAllowed retains the installed team's first-match order. The
// caller supplies its existing allowed-team set; this predicate adds no grant.
func (s *SQLiteStore) LoopTeamKeyAllowed(workspace, key string, allowed map[string]bool) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if workspace == "" {
		workspace = s.lastWorkspaceKey
	}
	data, ok := s.workspaces[workspace]
	if !ok {
		return false
	}
	for _, team := range data.Teams {
		if strings.EqualFold(team.Key, key) || strings.EqualFold(team.Name, key) {
			return allowed[team.ID]
		}
	}
	return false
}
