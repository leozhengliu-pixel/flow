package main

import (
	"encoding/json"
	"slices"
	"strings"

	"flow/api/internal/domain"
)

// A team's group binding covers all of the team's projects, one project, or a
// chosen set of them (ChatBinding.ProjectIDs). Every project in the set must
// belong to the bound team, be active, and be shared only with teams the
// binding may disclose; deliveries and group actions stay inside the set.

const chatBindingMaxProjects = 50

func chatBindingProjects(binding domain.ChatBinding) []string {
	binding.NormalizeProjects()
	return binding.ProjectIDs
}

// chatBindingProjectScoped: the binding is limited to a set of projects.
func chatBindingProjectScoped(binding domain.ChatBinding) bool {
	return len(chatBindingProjects(binding)) > 0
}

// chatBindingLimitedToProject: the binding is limited to projects, and this
// is one of them.
func chatBindingLimitedToProject(binding domain.ChatBinding, projectID string) bool {
	return projectID != "" && slices.Contains(chatBindingProjects(binding), projectID)
}

// chatBindingProjectKey is the project scope in the binding's fingerprint:
// the project for a one-project binding (as before project sets), the sorted
// set otherwise.
func chatBindingProjectKey(binding domain.ChatBinding) string {
	return strings.Join(chatBindingProjects(binding), ",")
}

func chatIssueProjectID(issue domain.Issue) string {
	if issue.Project == nil {
		return ""
	}
	return issue.Project.ID
}

// chatBindingProjectsPermitted checks every project of the binding's set.
func chatBindingProjectsPermitted(data domain.Bootstrap, binding domain.ChatBinding) bool {
	ids := chatBindingProjects(binding)
	if len(ids) > chatBindingMaxProjects {
		return false
	}
	for _, id := range ids {
		index := slices.IndexFunc(data.Projects, func(project domain.Project) bool { return project.ID == id })
		if index < 0 {
			return false
		}
		project := data.Projects[index]
		if project.ArchivedAt != nil || !slices.Contains(project.TeamIDs, binding.TeamID) {
			return false
		}
		// A project shared with another private team would disclose that
		// team's work; only public teams may join the bound one.
		for _, teamID := range project.TeamIDs {
			if !chatTeamShareable(data, binding, teamID) {
				return false
			}
		}
	}
	return true
}

// chatCreateProject is the project of an issue created from the group: the
// binding's project, or for a set the one chosen on the form from the
// projects the form offered.
func chatCreateProject(binding domain.ChatBinding, action chatCardAction, values map[string]json.RawMessage) (string, error) {
	ids := chatBindingProjects(binding)
	switch len(ids) {
	case 0:
		return "", nil
	case 1:
		return ids[0], nil
	}
	chosen := chatString(values, "projectId")
	if !slices.Contains(ids, chosen) || !slices.Contains(action.ProjectIDs, chosen) {
		return "", chatInvalid("invalid project")
	}
	return chosen, nil
}

// chatCreateProjectSelect is the create form's project choice for a binding
// with several projects (nil otherwise). The template's project comes first
// when it is one of them.
func chatCreateProjectSelect(metadata domain.Bootstrap, binding domain.ChatBinding, preferred string) (map[string]any, []string) {
	ids := chatBindingProjects(binding)
	if len(ids) < 2 {
		return nil, nil
	}
	options, offered := []map[string]any{}, []string{}
	for _, project := range metadata.Projects {
		if slices.Contains(ids, project.ID) && project.ArchivedAt == nil && len(options) < 100 {
			options = append(options, chatOption(project.ID, project.Name))
			offered = append(offered, project.ID)
		}
	}
	initial := ""
	if slices.Contains(offered, preferred) {
		initial = preferred
	}
	return chatSelect("projectId", "Project", initial, options), offered
}

// chatProjectsLabel names the binding's projects for a card.
func chatProjectsLabel(metadata domain.Bootstrap, binding domain.ChatBinding) string {
	names := []string{}
	for _, id := range chatBindingProjects(binding) {
		for _, project := range metadata.Projects {
			if project.ID == id {
				names = append(names, project.Name)
			}
		}
	}
	if len(names) == 0 {
		return ""
	}
	if len(names) == 1 {
		return "\nProject: " + names[0]
	}
	return "\nProjects: " + strings.Join(names, ", ")
}
