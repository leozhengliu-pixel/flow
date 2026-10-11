package main

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// maxProjectTemplateIssues bounds the issues one project template creates.
const maxProjectTemplateIssues = 250

// normalizeProjectTemplateIssues validates a project template's issue list
// against the template as it stands after the rest of the request (its
// milestones in particular) and fills in missing ids.
func normalizeProjectTemplateIssues(data *domain.Bootstrap, template *domain.ProjectTemplate, input []domain.ProjectTemplateIssue) ([]domain.ProjectTemplateIssue, error) {
	if len(input) > maxProjectTemplateIssues {
		return nil, fmt.Errorf("%w: a project template can create at most %d issues", errInvalid, maxProjectTemplateIssues)
	}
	milestones := map[string]bool{}
	for _, milestone := range template.Milestones {
		milestones[milestone.ID] = true
	}
	now := time.Now().UnixNano()
	issues := make([]domain.ProjectTemplateIssue, 0, len(input))
	ids := map[string]bool{}
	for index, issue := range input {
		issue.ID = strings.TrimSpace(issue.ID)
		issue.Title = strings.TrimSpace(issue.Title)
		issue.Description = strings.TrimSpace(issue.Description)
		issue.TeamID = strings.TrimSpace(issue.TeamID)
		issue.AssigneeID = strings.TrimSpace(issue.AssigneeID)
		issue.MilestoneID = strings.TrimSpace(issue.MilestoneID)
		issue.ParentID = strings.TrimSpace(issue.ParentID)
		if issue.Title == "" || issue.Priority < 0 || issue.Priority > 4 {
			return nil, errInvalid
		}
		if issue.ID == "" {
			issue.ID = fmt.Sprintf("template_issue_%d_%d", now, index)
		}
		if ids[issue.ID] {
			return nil, errInvalid
		}
		ids[issue.ID] = true
		if issue.TeamID != "" && !teamExists(data, issue.TeamID) {
			return nil, errInvalid
		}
		if issue.AssigneeID != "" && userByID(data, issue.AssigneeID) == nil {
			return nil, errInvalid
		}
		issue.LabelIDs = normalizedStrings(issue.LabelIDs)
		labels := labelsByIDForResource(data, issue.LabelIDs, "issue")
		if len(labels) != len(issue.LabelIDs) || !validLabelGroupSelection(labels) || issue.TeamID != "" && !labelsAvailableToTeam(data, labels, issue.TeamID) {
			return nil, errInvalid
		}
		if len(issue.LabelIDs) == 0 {
			issue.LabelIDs = nil
		}
		if issue.MilestoneID != "" && !milestones[issue.MilestoneID] {
			return nil, fmt.Errorf("%w: unknown template milestone", errInvalid)
		}
		issues = append(issues, issue)
	}
	parents := map[string]string{}
	for _, issue := range issues {
		if issue.ParentID == "" {
			continue
		}
		if issue.ParentID == issue.ID || !ids[issue.ParentID] {
			return nil, fmt.Errorf("%w: unknown template parent issue", errInvalid)
		}
		parents[issue.ID] = issue.ParentID
	}
	for _, issue := range issues {
		seen := map[string]bool{issue.ID: true}
		for parent := parents[issue.ID]; parent != ""; parent = parents[parent] {
			if seen[parent] {
				return nil, fmt.Errorf("%w: template sub-issues form a cycle", errInvalid)
			}
			seen[parent] = true
		}
	}
	return issues, nil
}

// pruneProjectTemplateIssueMilestones clears template issue milestones the
// template no longer has.
func pruneProjectTemplateIssueMilestones(template *domain.ProjectTemplate) {
	for index := range template.Issues {
		milestoneID := template.Issues[index].MilestoneID
		if milestoneID != "" && !slices.ContainsFunc(template.Milestones, func(milestone domain.TemplateMilestone) bool { return milestone.ID == milestoneID }) {
			template.Issues[index].MilestoneID = ""
		}
	}
}

// normalizeProjectTemplateDependencyRelations validates a template's preset
// project dependencies: existing projects, blocked_by or blocks, each project
// at most once.
func normalizeProjectTemplateDependencyRelations(data *domain.Bootstrap, input []domain.ProjectDependencyRelationInput) ([]domain.ProjectDependencyRelationInput, error) {
	relations := make([]domain.ProjectDependencyRelationInput, 0, len(input))
	seen := map[string]bool{}
	for _, relation := range input {
		relation.ProjectID = strings.TrimSpace(relation.ProjectID)
		if relation.ProjectID == "" || !slices.Contains([]string{"blocked_by", "blocks"}, relation.Type) || seen[relation.ProjectID] || !validateResourceIDs(data, "project", []string{relation.ProjectID}) {
			return nil, errInvalid
		}
		seen[relation.ProjectID] = true
		relations = append(relations, relation)
	}
	if len(relations) == 0 {
		return nil, nil
	}
	return relations, nil
}

func projectTemplateBlockedByIDs(relations []domain.ProjectDependencyRelationInput) []string {
	ids := []string{}
	for _, relation := range relations {
		if relation.Type == "blocked_by" {
			ids = append(ids, relation.ProjectID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	return ids
}

// mergeProjectTemplateBlockedBy replaces a template's blocked_by relations
// with blockedBy and keeps its blocking relations to other projects.
func mergeProjectTemplateBlockedBy(relations []domain.ProjectDependencyRelationInput, blockedBy []string) []domain.ProjectDependencyRelationInput {
	merged := make([]domain.ProjectDependencyRelationInput, 0, len(relations)+len(blockedBy))
	for _, projectID := range blockedBy {
		merged = append(merged, domain.ProjectDependencyRelationInput{ProjectID: projectID, Type: "blocked_by"})
	}
	for _, relation := range relations {
		if relation.Type == "blocks" && !slices.Contains(blockedBy, relation.ProjectID) {
			merged = append(merged, relation)
		}
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

// projectTemplateDependencyInput returns the dependencies a new project takes
// from its template, skipping projects that no longer exist.
func projectTemplateDependencyInput(data *domain.Bootstrap, template domain.ProjectTemplate) ([]string, []domain.ProjectDependencyRelationInput) {
	exists := func(id string) bool {
		return slices.ContainsFunc(data.Projects, func(project domain.Project) bool { return project.ID == id })
	}
	if len(template.DependencyRelations) > 0 {
		relations := []domain.ProjectDependencyRelationInput{}
		for _, relation := range template.DependencyRelations {
			if exists(relation.ProjectID) {
				relations = append(relations, relation)
			}
		}
		return nil, relations
	}
	ids := []string{}
	for _, id := range template.DependencyIDs {
		if exists(id) {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// projectTemplateIssueTeam picks a template issue's team: its own when it is
// still active, else the project's first active team, else the workspace's
// first active team.
func projectTemplateIssueTeam(data *domain.Bootstrap, issue domain.ProjectTemplateIssue, project domain.Project) (domain.Team, bool) {
	active := func(id string) (domain.Team, bool) {
		index := slices.IndexFunc(data.Teams, func(team domain.Team) bool { return team.ID == id })
		if index < 0 || data.Teams[index].ArchivedAt != nil || data.Teams[index].RetiredAt != nil {
			return domain.Team{}, false
		}
		return data.Teams[index], true
	}
	if issue.TeamID != "" {
		if team, ok := active(issue.TeamID); ok {
			return team, true
		}
	}
	for _, id := range project.TeamIDs {
		if team, ok := active(id); ok {
			return team, true
		}
	}
	for _, team := range data.Teams {
		if candidate, ok := active(team.ID); ok {
			return candidate, true
		}
	}
	return domain.Team{}, false
}

// createProjectTemplateIssues creates a template's issues in a new project
// (already in data.Projects). milestoneIDs maps template milestone ids to the
// project's milestone ids. Issues are numbered from the workspace sequence in
// template order and prepended to the collection like issue creation does.
func createProjectTemplateIssues(data *domain.Bootstrap, template domain.ProjectTemplate, project domain.Project, milestoneIDs map[string]string) ([]domain.Issue, error) {
	if len(template.Issues) == 0 {
		return nil, nil
	}
	number := max(data.NextIssueNumber, nextIssueNumber(data.Issues))
	now := time.Now().UTC()
	summary := projectByID(data, project.ID)
	if summary == nil {
		summary = &domain.ProjectSummary{ID: project.ID, Name: project.Name, Icon: project.Icon, Color: project.Color}
	}
	created := make([]domain.Issue, 0, len(template.Issues))
	issueIDs := map[string]string{}
	for _, item := range template.Issues {
		team, ok := projectTemplateIssueTeam(data, item, project)
		if !ok {
			return nil, fmt.Errorf("%w: no active team for template issues", errInvalid)
		}
		settings := teamSettings(data, team.ID)
		state := stateForTeam(data, team.ID, settings.DefaultStateID)
		if state == nil {
			states := statesForTeam(data, team.ID)
			if len(states) == 0 {
				return nil, errInvalid
			}
			state = stateForTeam(data, team.ID, states[0].ID)
			if state == nil {
				state = &states[0]
			}
		}
		labels := []domain.IssueLabel{}
		groups := map[string]bool{}
		for _, label := range labelsByID(data, item.LabelIDs) {
			if !labelsAvailableToTeam(data, []domain.IssueLabel{label}, team.ID) || label.GroupID != "" && groups[label.GroupID] {
				continue
			}
			if label.GroupID != "" {
				groups[label.GroupID] = true
			}
			labels = append(labels, label)
		}
		projectSummary := *summary
		issue := domain.Issue{
			ID: fmt.Sprintf("issue_%d", number), Version: 1, Identifier: fmt.Sprintf("%s-%d", team.Key, number), Number: number,
			Title: item.Title, Description: item.Description, Priority: item.Priority, PriorityLabel: priorityLabel(item.Priority),
			SortOrder: float64(number), CreatedAt: now, UpdatedAt: now, Team: team, State: *state, Creator: data.Viewer, Labels: labels,
			Project: &projectSummary, SubscriberIDs: []string{data.Viewer.ID}, Reactions: map[string][]string{}, SubIssueIDs: []string{},
			Relations: []domain.IssueRelation{}, Attachments: []domain.Attachment{}, SuggestedLabelIDs: []string{},
		}
		if item.AssigneeID != "" {
			issue.Assignee = userByID(data, item.AssigneeID)
		}
		if milestoneID := milestoneIDs[item.MilestoneID]; item.MilestoneID != "" && milestoneID != "" {
			issue.ProjectMilestoneID = stringPointer(milestoneID)
		}
		issueIDs[item.ID] = issue.ID
		created = append(created, issue)
		number++
	}
	for index, item := range template.Issues {
		parentID, ok := issueIDs[item.ParentID]
		if item.ParentID == "" || !ok || parentID == created[index].ID {
			continue
		}
		created[index].ParentID = stringPointer(parentID)
		parentIndex := slices.IndexFunc(created, func(issue domain.Issue) bool { return issue.ID == parentID })
		created[parentIndex].SubIssueIDs = append(created[parentIndex].SubIssueIDs, created[index].ID)
	}
	data.Issues = append(slices.Clone(created), data.Issues...)
	data.NextIssueNumber = number
	for _, issue := range created {
		appendActivity(data, issue.ID, "issue.created", data.Viewer, map[string]string{"stateId": issue.State.ID, "state": issue.State.Name})
	}
	return created, nil
}
