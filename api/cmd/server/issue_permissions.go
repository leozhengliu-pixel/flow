package main

import (
	"net/http"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

func issuePermissionRank(role string) int {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "owner":
		return 4
	case "editor":
		return 3
	case "commenter":
		return 2
	case "viewer":
		return 1
	default:
		return 0
	}
}

// issueRole resolves the strongest direct or inherited issue grant. Sub-issues
// inherit permissions from their parent, while team membership still governs
// the normal visibility of public and private team issues.
func issueRole(s *server, data domain.Bootstrap, issue domain.Issue) string {
	if s.authDisabled || issue.Creator.ID == data.Viewer.ID || workspaceAdminRole(data.ViewerRole) {
		return "owner"
	}
	if inheritedTeamOwner(data, issue.Team.ID, data.Viewer.ID) {
		return "owner"
	}
	best := ""
	for current := &issue; current != nil; {
		for _, permission := range current.Permissions {
			matched := permission.SubjectType == "user" && permission.SubjectID == data.Viewer.ID
			if !matched && permission.SubjectType == "workspace" {
				matched = permission.SubjectID == "" || permission.SubjectID == data.Workspace.ID || permission.SubjectID == data.Workspace.URLKey
			}
			if !matched && permission.SubjectType == "team" {
				matched = slices.ContainsFunc(data.TeamMembers, func(member domain.TeamMember) bool {
					return member.TeamID == permission.SubjectID && member.UserID == data.Viewer.ID
				})
			}
			if matched && issuePermissionRank(permission.Role) > issuePermissionRank(best) {
				best = strings.ToLower(strings.TrimSpace(permission.Role))
			}
		}
		if issue.ParentID == nil || *issue.ParentID == "" {
			break
		}
		parent := slices.IndexFunc(data.Issues, func(candidate domain.Issue) bool { return candidate.ID == *current.ParentID })
		if parent < 0 || parent == slices.IndexFunc(data.Issues, func(candidate domain.Issue) bool { return candidate.ID == current.ID }) {
			break
		}
		current = &data.Issues[parent]
	}
	if best != "" {
		return best
	}
	if slices.ContainsFunc(data.TeamMembers, func(member domain.TeamMember) bool {
		return member.TeamID == issue.Team.ID && member.UserID == data.Viewer.ID
	}) {
		return "editor"
	}
	// Public teams are visible to workspace members even without explicit team
	// membership. Private/restricted teams are projected out by the store.
	for _, team := range data.Teams {
		if team.ID == issue.Team.ID && !team.Private {
			settings := data.TeamSettings[team.ID]
			if !strings.EqualFold(settings.Access, "private") && !strings.EqualFold(settings.Access, "restricted") {
				return "viewer"
			}
		}
	}
	return "none"
}

func inheritedTeamOwner(data domain.Bootstrap, teamID, userID string) bool {
	seen := map[string]bool{}
	for current := teamID; current != "" && !seen[current]; {
		seen[current] = true
		for _, member := range data.TeamMembers {
			if member.TeamID == current && member.UserID == userID && strings.EqualFold(member.Role, "owner") {
				return true
			}
		}
		settings, ok := data.TeamSettings[current]
		if !ok {
			break
		}
		current = settings.ParentTeamID
	}
	return false
}

func (s *server) findIssueForPermissions(r *http.Request) (*domain.Bootstrap, *domain.Issue, bool) {
	data := s.workspaceData(r)
	id := r.PathValue("id")
	index := slices.IndexFunc(data.Issues, func(item domain.Issue) bool { return item.ID == id })
	if index < 0 {
		return &data, nil, false
	}
	return &data, &data.Issues[index], true
}

func (s *server) listIssuePermissions(w http.ResponseWriter, r *http.Request) {
	data, issue, ok := s.findIssueForPermissions(r)
	if !ok || issueRole(s, *data, *issue) == "none" {
		writeError(w, http.StatusNotFound, "issue not found")
		return
	}
	writeJSON(w, http.StatusOK, slices.Clone(issue.Permissions))
}

func validIssuePermission(data domain.Bootstrap, permission domain.IssuePermission) bool {
	permission.SubjectType = strings.ToLower(strings.TrimSpace(permission.SubjectType))
	permission.Role = strings.ToLower(strings.TrimSpace(permission.Role))
	if permission.SubjectID == "" && permission.SubjectType != "workspace" {
		return false
	}
	if !slices.Contains([]string{"viewer", "commenter", "editor", "owner"}, permission.Role) {
		return false
	}
	if permission.Role == "owner" {
		return false // the issue creator remains the sole canonical owner
	}
	switch permission.SubjectType {
	case "user":
		return slices.ContainsFunc(data.Users, func(user domain.User) bool { return user.ID == permission.SubjectID })
	case "team":
		return slices.ContainsFunc(data.Teams, func(team domain.Team) bool { return team.ID == permission.SubjectID })
	case "workspace":
		return permission.SubjectID == "" || permission.SubjectID == data.Workspace.ID || permission.SubjectID == data.Workspace.URLKey
	default:
		return false
	}
}

func (s *server) replaceIssuePermissions(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Permissions []domain.IssuePermission `json:"permissions"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	data, issue, ok := s.findIssueForPermissions(r)
	if !ok {
		writeError(w, http.StatusNotFound, "issue not found")
		return
	}
	if issueRole(s, *data, *issue) != "owner" {
		writeError(w, http.StatusForbidden, "issue owner access required")
		return
	}
	var updated []domain.IssuePermission
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "issue.permissions_updated", issue.ID, input, func(next *domain.Bootstrap) error {
		index := slices.IndexFunc(next.Issues, func(item domain.Issue) bool { return item.ID == issue.ID })
		if index < 0 {
			return errNotFound
		}
		now := time.Now().UTC()
		seen := map[string]bool{}
		permissions := make([]domain.IssuePermission, 0, len(input.Permissions))
		for _, permission := range input.Permissions {
			permission.IssueID = issue.ID
			permission.SubjectType = strings.ToLower(strings.TrimSpace(permission.SubjectType))
			permission.SubjectID = strings.TrimSpace(permission.SubjectID)
			permission.Role = strings.ToLower(strings.TrimSpace(permission.Role))
			key := permission.SubjectType + ":" + permission.SubjectID
			if seen[key] || !validIssuePermission(*data, permission) {
				return errInvalid
			}
			seen[key] = true
			permission.ID = "issue_permission_" + strings.ReplaceAll(key, ":", "_")
			permission.CreatedAt, permission.UpdatedAt = now, now
			permissions = append(permissions, permission)
		}
		next.Issues[index].Permissions = permissions
		updated = slices.Clone(permissions)
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) updateIssuePermission(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Role string `json:"role"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	data, issue, ok := s.findIssueForPermissions(r)
	if !ok {
		writeError(w, http.StatusNotFound, "issue not found")
		return
	}
	if issueRole(s, *data, *issue) != "owner" {
		writeError(w, http.StatusForbidden, "issue owner access required")
		return
	}
	var updated domain.IssuePermission
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "issue.permission_updated", issue.ID, input, func(next *domain.Bootstrap) error {
		index := slices.IndexFunc(next.Issues, func(item domain.Issue) bool { return item.ID == issue.ID })
		if index < 0 {
			return errNotFound
		}
		permissionIndex := slices.IndexFunc(next.Issues[index].Permissions, func(item domain.IssuePermission) bool { return item.ID == r.PathValue("permissionId") })
		if permissionIndex < 0 || !slices.Contains([]string{"viewer", "commenter", "editor"}, strings.ToLower(strings.TrimSpace(input.Role))) {
			return errInvalid
		}
		next.Issues[index].Permissions[permissionIndex].Role = strings.ToLower(strings.TrimSpace(input.Role))
		next.Issues[index].Permissions[permissionIndex].UpdatedAt = time.Now().UTC()
		updated = next.Issues[index].Permissions[permissionIndex]
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) deleteIssuePermission(w http.ResponseWriter, r *http.Request) {
	data, issue, ok := s.findIssueForPermissions(r)
	if !ok {
		writeError(w, http.StatusNotFound, "issue not found")
		return
	}
	if issueRole(s, *data, *issue) != "owner" {
		writeError(w, http.StatusForbidden, "issue owner access required")
		return
	}
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "issue.permission_deleted", issue.ID, map[string]string{"permissionId": r.PathValue("permissionId")}, func(next *domain.Bootstrap) error {
		index := slices.IndexFunc(next.Issues, func(item domain.Issue) bool { return item.ID == issue.ID })
		if index < 0 {
			return errNotFound
		}
		permissionIndex := slices.IndexFunc(next.Issues[index].Permissions, func(item domain.IssuePermission) bool { return item.ID == r.PathValue("permissionId") })
		if permissionIndex < 0 {
			return errNotFound
		}
		next.Issues[index].Permissions = slices.Delete(next.Issues[index].Permissions, permissionIndex, permissionIndex+1)
		return nil
	})
	respondMutation(w, err, http.StatusNoContent, nil)
}
