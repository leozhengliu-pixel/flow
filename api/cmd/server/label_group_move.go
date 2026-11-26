package main

import (
	"fmt"
	"net/http"
	"slices"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

// moveWorkspaceLabelGroupToTeams moves a workspace issue label group, with its
// labels, to each team whose issues use one of them (the design reference's
// "Move to teams"): every such team gets its own copy of the group and of its
// labels, that team's issues switch to the copies, and the workspace group and
// its labels are removed. Like moving a single label, it fails when no issue
// uses the group.
func (s *server) moveWorkspaceLabelGroupToTeams(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// The mutation loads only the issues carrying one of the group's labels.
	memberIDs := []string{}
	if metadata, ok := s.store.WorkspaceMetadata(workspaceKey(r)); ok {
		for _, label := range metadata.Labels {
			if label.GroupID == id {
				memberIDs = append(memberIDs, label.ID)
			}
		}
	}
	moved := []domain.LabelGroup{}
	ctx := store.WithMutationScope(r.Context(), store.MutationScope{LabelIssues: memberIDs})
	err := s.store.MutateWorkspaceWithAggregate(ctx, workspaceKey(r), "label_group.moved_to_teams", id, func(data *domain.Bootstrap) (string, error) {
		index := slices.IndexFunc(data.LabelGroups, func(group domain.LabelGroup) bool {
			return group.ID == id && labelScopeIsWorkspace(group.Scope)
		})
		if index < 0 {
			return "", errNotFound
		}
		source := data.LabelGroups[index]
		if source.ResourceType != "" && source.ResourceType != "issue" {
			return "", errInvalid
		}
		members := []domain.IssueLabel{}
		isMember := map[string]bool{}
		for _, label := range data.Labels {
			if label.GroupID == id {
				members = append(members, label)
				isMember[label.ID] = true
			}
		}
		usedByTeam := map[string]bool{}
		for _, issue := range data.Issues {
			if slices.ContainsFunc(issue.Labels, func(label domain.IssueLabel) bool { return isMember[label.ID] }) {
				usedByTeam[issue.Team.ID] = true
			}
		}
		if len(usedByTeam) == 0 {
			return "", errInvalid
		}
		for teamIndex, team := range data.Teams {
			if !usedByTeam[team.ID] {
				continue
			}
			stamp := idgen.Stamp()
			group := source
			group.ID = fmt.Sprintf("label_group_%s_%d", stamp, teamIndex)
			group.Scope = team.ID
			moved = append(moved, group)
			data.LabelGroups = append(data.LabelGroups, group)
			copies := make(map[string]domain.IssueLabel, len(members))
			for labelIndex, label := range members {
				copied := label
				copied.ID = fmt.Sprintf("label_%s_%d_%d", stamp, teamIndex, labelIndex)
				copied.Scope = team.ID
				copied.GroupID = group.ID
				copies[label.ID] = copied
				data.Labels = append(data.Labels, copied)
			}
			for issueIndex := range data.Issues {
				if data.Issues[issueIndex].Team.ID != team.ID {
					continue
				}
				for labelIndex, label := range data.Issues[issueIndex].Labels {
					if copied, ok := copies[label.ID]; ok {
						data.Issues[issueIndex].Labels[labelIndex] = copied
					}
				}
			}
		}
		data.LabelGroups = slices.Delete(data.LabelGroups, index, index+1)
		data.Labels = slices.DeleteFunc(data.Labels, func(label domain.IssueLabel) bool { return isMember[label.ID] })
		return id, nil
	})
	respondMutation(w, err, http.StatusOK, moved)
}
