package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"strings"

	"flow/api/internal/domain"
)

// IssueAccessMetadata copies only the policy and team graph needed by bounded
// issue queries. Document bodies, projects and the people directory stay out of
// request memory. A transaction snapshot, when present, remains authoritative.
func (s *SQLiteStore) IssueAccessMetadata(ctx context.Context, workspace string) (domain.Bootstrap, bool) {
	if snapshot, ok := ctx.Value(workspaceReadKey{}).(workspaceRead); ok && (workspace == "" || workspace == snapshot.workspace) {
		return issueAccessMetadata(snapshot.metadata), true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if workspace == "" {
		workspace = s.lastWorkspaceKey
	}
	data, ok := s.workspaces[workspace]
	if !ok {
		return domain.Bootstrap{}, false
	}
	return issueAccessMetadata(data), true
}

func issueAccessMetadata(data domain.Bootstrap) domain.Bootstrap {
	result := cloneBootstrap(domain.Bootstrap{Workspace: data.Workspace, WorkspaceSettings: data.WorkspaceSettings})
	result.Teams = slices.Clone(data.Teams)
	result.TeamSettings = make(map[string]domain.TeamSettings, len(data.TeamSettings))
	for id, settings := range data.TeamSettings {
		result.TeamSettings[id] = domain.TeamSettings{TeamID: id, ParentTeamID: settings.ParentTeamID, Access: settings.Access, MembershipRestriction: settings.MembershipRestriction, SettingsPermission: settings.SettingsPermission, LabelPermission: settings.LabelPermission, MemberPermission: settings.MemberPermission}
	}
	return result
}

// Match teamVisibleToUser without rescanning all teams and memberships for each
// node. Parent ownership and the nearest private ancestor boundary are retained.
func visibleIssueTeams(data domain.Bootstrap, userID, role string) []string {
	roles := make(map[string]string, len(data.TeamMembers))
	for _, member := range data.TeamMembers {
		if member.UserID == userID {
			roles[member.TeamID] = member.Role
		}
	}
	teams := make(map[string]domain.Team, len(data.Teams))
	for _, team := range data.Teams {
		teams[team.ID] = team
	}
	visible := make([]string, 0, len(data.Teams))
	for _, team := range data.Teams {
		allowed := isWorkspaceAdminRole(role) || roles[team.ID] != ""
		settings := data.TeamSettings[team.ID]
		if !allowed && role != "guest" {
			access := strings.ToLower(strings.TrimSpace(settings.Access))
			if access == "" && team.Private {
				access = "private"
			}
			if access != "private" && !strings.EqualFold(settings.MembershipRestriction, "members") && !strings.EqualFold(settings.MembershipRestriction, "owners") {
				allowed = access != "restricted"
				seen := map[string]bool{team.ID: true}
				for parent := settings.ParentTeamID; parent != "" && !seen[parent]; parent = data.TeamSettings[parent].ParentTeamID {
					seen[parent] = true
					p := data.TeamSettings[parent]
					if teams[parent].Private || strings.EqualFold(p.Access, "private") || strings.EqualFold(p.Access, "restricted") && p.ParentTeamID == "" {
						allowed = roles[parent] != ""
						break
					}
				}
			}
		}
		if allowed {
			visible = append(visible, team.ID)
		}
	}
	return visible
}

// Only identifiers referenced by this page are returned; never copy project
// descriptions or label catalogs just to redact a shared issue's references.
func (s *SQLiteStore) IssueReferenceMetadata(ctx context.Context, query IssueRecordQuery, issues []domain.Issue) (domain.Bootstrap, error) {
	projectIDs, labelIDs := map[string]bool{}, map[string]bool{}
	for _, issue := range issues {
		if issue.Project != nil {
			projectIDs[issue.Project.ID] = true
		}
		for _, label := range issue.Labels {
			labelIDs[label.ID] = true
		}
	}
	allowed := func(id string) bool {
		return (query.Access == nil || query.Access.Admin || slices.Contains(query.Access.VisibleTeamIDs, id)) && (query.AllowedTeamIDs == nil || slices.Contains(query.AllowedTeamIDs, id))
	}
	projectAllowed := func(teamIDs []string) bool {
		if len(teamIDs) == 0 {
			return query.AllowedTeamIDs == nil
		}
		return slices.ContainsFunc(teamIDs, allowed)
	}
	var reader metadataReader = s.db
	if snapshot, ok := ctx.Value(workspaceReadKey{}).(workspaceRead); ok && (query.Workspace == "" || query.Workspace == snapshot.workspace) {
		reader = snapshot.reader
	}
	result := domain.Bootstrap{}
	for _, field := range []string{"projects", "labels"} {
		ids, property := projectIDs, "teamIds"
		if field == "labels" {
			ids, property = labelIDs, "scope"
		}
		keys := make([]string, 0, len(ids))
		for id := range ids {
			keys = append(keys, id)
		}
		for start := 0; start < len(keys); start += 200 {
			where, args := bindList("record_key", keys[start:min(start+200, len(keys))])
			args = append([]any{query.Workspace, field}, args...)
			rows, err := reader.QueryContext(ctx, "SELECT record_key,"+s.jsonText("data", property)+" FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND "+where, args...)
			if err != nil {
				return result, err
			}
			for rows.Next() {
				var id string
				var value sql.NullString
				if err := rows.Scan(&id, &value); err != nil {
					rows.Close()
					return result, err
				}
				if field == "projects" {
					var teams []string
					if value.Valid {
						if err := json.Unmarshal([]byte(value.String), &teams); err != nil {
							rows.Close()
							return result, err
						}
					}
					if projectAllowed(teams) {
						result.Projects = append(result.Projects, domain.Project{ID: id})
					}
				} else {
					scope := strings.ToLower(strings.TrimSpace(value.String))
					if scope == "" || scope == "workspace" || allowed(value.String) {
						result.Labels = append(result.Labels, domain.IssueLabel{ID: id})
					}
				}
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return result, err
			}
		}
	}
	return result, nil
}
