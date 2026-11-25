package store

import (
	"context"
	"database/sql"
	"encoding/json"

	"flow/api/internal/domain"
)

// ReadRealtimeMetadataScopes reads current authorization columns without
// transferring bodies, including when a peer's installed copy is behind.
func (s *SQLiteStore) ReadRealtimeMetadataScopes(ctx context.Context, workspace, kind string, ids []string) (map[string]json.RawMessage, error) {
	properties := []string{}
	jsonFields := map[string]bool{}
	switch kind {
	case "projects":
		properties = []string{"teamIds", "initiatives", "dependencyIds"}
		for _, name := range properties {
			jsonFields[name] = true
		}
	case "initiatives":
		properties = []string{"leadTeamId", "contributingTeamIds", "projectIds", "parentInitiativeIds"}
		for _, name := range properties[1:] {
			jsonFields[name] = true
		}
	default:
		return nil, ErrMetadataRecordQuery
	}
	keys := []string{}
	seen := map[string]bool{}
	for _, id := range ids {
		if id != "" && !seen[id] {
			keys = append(keys, id)
			seen[id] = true
		}
	}
	if len(keys) > eventSourceMaxReferences {
		return nil, ErrEventSourceBudget
	}
	result := map[string]json.RawMessage{}
	used := 0
	for start := 0; start < len(keys); start += 200 {
		where, args := bindList("record_key", keys[start:min(start+200, len(keys))])
		query := "SELECT record_key"
		for _, name := range properties {
			value := s.jsonText("data", name)
			query += ",LENGTH(" + value + "),CASE WHEN LENGTH(" + value + ")<=32768 THEN " + value + " ELSE NULL END"
		}
		query += ` FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND ` + where
		rows, err := s.db.QueryContext(ctx, query, append([]any{workspace, kind}, args...)...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			sizes := make([]sql.NullInt64, len(properties))
			values := make([]sql.NullString, len(properties))
			dest := []any{&id}
			for i := range properties {
				dest = append(dest, &sizes[i], &values[i])
			}
			if err := rows.Scan(dest...); err != nil {
				rows.Close()
				return nil, err
			}
			fields := map[string]json.RawMessage{"id": nil}
			fields["id"], _ = json.Marshal(id)
			for i, name := range properties {
				if sizes[i].Valid && sizes[i].Int64 > 32768 {
					rows.Close()
					return nil, ErrEventSourceBudget
				}
				used += len(values[i].String)*3 + 128
				if used > 1<<20 {
					rows.Close()
					return nil, ErrEventSourceBudget
				}
				if !values[i].Valid {
					fields[name] = json.RawMessage("null")
				} else if jsonFields[name] {
					fields[name] = json.RawMessage(values[i].String)
				} else {
					fields[name], _ = json.Marshal(values[i].String)
				}
			}
			raw, err := json.Marshal(fields)
			if err != nil {
				rows.Close()
				return nil, err
			}
			result[id] = raw
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (source *RealtimeIssueSource) TeamID(id string) string { return source.scopes[id].team }

func (s *SQLiteStore) RealtimeDocumentInheritedScope(ctx context.Context, workspace string, document domain.Document, policy *EventPolicySource) ([]string, string, error) {
	if policy == nil || policy.store != s || policy.workspace != workspace {
		return nil, "", ErrAuthForbidden
	}
	teams, _, err := s.ReadRealtimeProjectTeams(ctx, workspace, document.ProjectIDs)
	if err != nil {
		return nil, "", err
	}
	projects := []domain.Project{}
	for _, id := range document.ProjectIDs {
		if ids, exists := teams[id]; exists {
			projects = append(projects, domain.Project{ID: id, TeamIDs: ids})
		}
	}
	issueTeam := ""
	if document.IssueID != "" {
		issueTeam = document.IssueTeamID
		scope, err := s.ReadRealtimeIssueAccess(ctx, workspace, []string{document.IssueID}, policy.version)
		if err != nil {
			return nil, "", err
		}
		if current := scope.TeamID(document.IssueID); current != "" {
			issueTeam = current
		}
	}
	inherited := domain.InheritedDocumentTeams(issueTeam, document.ProjectIDs, projects, policy.metadata.Teams)
	return inherited, issueTeam, nil
}
