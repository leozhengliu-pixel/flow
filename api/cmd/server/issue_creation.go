package main

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func createIssueInWorkspace(data *domain.Bootstrap, input domain.IssueCreateInput, creationKey string, created *domain.Issue) (string, error) {
	var templateSubIssues []domain.TemplateSubIssue
	if id := creationKey; id != "" {
		if existing, err := issueByID(data, id); err == nil {
			*created = *existing
			return id, store.ErrNoMutation
		}
	}
	if input.TemplateID != "" {
		index := slices.IndexFunc(data.IssueTemplates, func(template domain.IssueTemplate) bool { return template.ID == input.TemplateID })
		if index < 0 {
			return "", errNotFound
		}
		template := data.IssueTemplates[index]
		templateSubIssues = slices.Clone(template.SubIssues)
		if input.Title == "" {
			input.Title = template.Title
			if input.Title == "" {
				input.Title = template.Name
			}
		}
		if input.Description == "" {
			input.Description = template.Body
		}
		if input.StateID == nil && template.StateID != "" {
			input.StateID = &template.StateID
		}
		if input.Priority == nil {
			input.Priority = &template.Priority
		}
		// A template's assignee who left is skipped, not refused.
		if input.AssigneeID == nil && template.AssigneeID != "" && !formerMember(data, template.AssigneeID) {
			input.AssigneeID = &template.AssigneeID
		}
		if input.ProjectID == nil && template.ProjectID != "" {
			input.ProjectID = &template.ProjectID
		}
		if len(input.LabelIDs) == 0 {
			input.LabelIDs = slices.Clone(template.LabelIDs)
		}
		if input.TeamID == "" {
			input.TeamID = template.TeamID
		}
	}
	if strings.TrimSpace(input.Title) == "" {
		return "", errInvalid
	}
	if err := checkIssueTitle(strings.TrimSpace(input.Title)); err != nil {
		return "", err // a template's title
	}
	number := data.NextIssueNumber
	if number == 0 {
		number = nextIssueNumber(data.Issues)
	}
	now := time.Now().UTC()
	team := data.Teams[0]
	if input.TeamID != "" {
		index := slices.IndexFunc(data.Teams, func(team domain.Team) bool { return team.ID == input.TeamID })
		if index < 0 {
			return "", errInvalid
		}
		team = data.Teams[index]
	}
	if team.ArchivedAt != nil {
		return "", errInvalid
	}
	if team.RetiredAt != nil {
		return "", fmt.Errorf("%w: team is retired", errInvalid)
	}
	settings := teamSettings(data, team.ID)
	defaultState := stateForTeam(data, team.ID, settings.DefaultStateID)
	if defaultState == nil {
		states := statesForTeam(data, team.ID)
		if len(states) == 0 {
			return "", errInvalid
		}
		defaultState = stateForTeam(data, team.ID, states[0].ID)
	}
	*created = domain.Issue{ID: fmt.Sprintf("issue_%d", number), Version: 1, Identifier: fmt.Sprintf("%s-%d", team.Key, number), Number: number, Title: strings.TrimSpace(input.Title), Description: strings.TrimSpace(input.Description), Priority: settings.DefaultPriority, PriorityLabel: priorityLabel(settings.DefaultPriority), SortOrder: float64(number), CreatedAt: now, UpdatedAt: now, Team: team, State: *defaultState, Creator: data.Viewer, BotActor: viewerBotActor(data, data.Viewer), Labels: []domain.IssueLabel{}, ParentID: input.ParentID, SubscriberIDs: []string{data.Viewer.ID}, Reactions: map[string][]string{}, SubIssueIDs: []string{}, Relations: []domain.IssueRelation{}, Attachments: []domain.Attachment{}, TemplateID: input.TemplateID, SuggestedLabelIDs: []string{}}
	if preferences := data.UserSettings[data.Viewer.ID]; preferences.AutoAssign && input.AssigneeID == nil {
		// Personal assignment preferences also apply to provider imports.
		input.AssigneeID = &data.Viewer.ID
	}
	if id := creationKey; id != "" {
		created.ID = id
	}
	createUpdate := domain.IssueUpdateInput{DescriptionState: input.DescriptionState, DescriptionData: input.DescriptionData, ContentState: input.ContentState, StateID: input.StateID, Priority: input.Priority, Estimate: input.Estimate, AssigneeID: input.AssigneeID, DelegateID: input.DelegateID, ProjectID: input.ProjectID, ProjectMilestoneID: input.ProjectMilestoneID, CycleID: input.CycleID, DueDate: input.DueDate, SLABreachesAt: input.SLABreachesAt, SLAType: input.SLAType, Recurrence: input.Recurrence, NextOccurrenceAt: input.NextOccurrenceAt, Icon: input.Icon}
	if len(input.LabelIDs) > 0 {
		createUpdate.LabelIDs = &input.LabelIDs
	}
	if err := refuseNewFormerMemberDescriptionMentions(data, nil, createUpdate); err != nil {
		return "", err
	}
	if _, err := applyUpdate(data, created, createUpdate); err != nil {
		return "", err
	}
	applyCycleAutomation(data, created)
	applySLARules(data, created, now)
	applyTriageRouting(data, created, now)
	if input.ParentID != nil {
		parent, err := issueByID(data, *input.ParentID)
		if err != nil {
			return "", err
		}
		parent.SubIssueIDs = appendUnique(parent.SubIssueIDs, created.ID)
	}
	children := make([]domain.Issue, 0, len(templateSubIssues))
	for childIndex, templateChild := range templateSubIssues {
		if err := checkIssueTitle(strings.TrimSpace(templateChild.Title)); err != nil {
			return "", err
		}
		childTeam := team
		if templateChild.TeamID != "" {
			teamIndex := slices.IndexFunc(data.Teams, func(item domain.Team) bool { return item.ID == templateChild.TeamID })
			if teamIndex < 0 {
				return "", errInvalid
			}
			childTeam = data.Teams[teamIndex]
		}
		childSettings := teamSettings(data, childTeam.ID)
		childState := stateForTeam(data, childTeam.ID, childSettings.DefaultStateID)
		if childState == nil {
			states := statesForTeam(data, childTeam.ID)
			if len(states) == 0 {
				return "", errInvalid
			}
			childState = &states[0]
		}
		childNumber := number + childIndex + 1
		child := domain.Issue{ID: fmt.Sprintf("issue_%d", childNumber), Version: 1, Identifier: fmt.Sprintf("%s-%d", childTeam.Key, childNumber), Number: childNumber, Title: strings.TrimSpace(templateChild.Title), Description: strings.TrimSpace(templateChild.Description), Priority: templateChild.Priority, PriorityLabel: priorityLabel(templateChild.Priority), SortOrder: float64(childNumber), CreatedAt: now, UpdatedAt: now, Team: childTeam, State: *childState, Creator: data.Viewer, BotActor: viewerBotActor(data, data.Viewer), Labels: labelsByID(data, templateChild.LabelIDs), ParentID: &created.ID, SubscriberIDs: []string{data.Viewer.ID}, Reactions: map[string][]string{}, SubIssueIDs: []string{}, Relations: []domain.IssueRelation{}, Attachments: []domain.Attachment{}}
		if templateChild.AssigneeID != "" && !formerMember(data, templateChild.AssigneeID) {
			child.Assignee = userByID(data, templateChild.AssigneeID)
		}
		created.SubIssueIDs = append(created.SubIssueIDs, child.ID)
		children = append(children, child)
	}
	data.Issues = append([]domain.Issue{*created}, append(children, data.Issues...)...)
	applyTriggeredWorkflows(data, "issueCreated", "issue", created.ID)
	createdMetadata := map[string]string{"stateId": created.State.ID, "state": created.State.Name}
	if created.CycleID != nil {
		createdMetadata["initialCycle"] = *created.CycleID
	}
	appendActivity(data, created.ID, "issue.created", data.Viewer, createdMetadata)
	for _, child := range children {
		appendActivity(data, child.ID, "issue.created", data.Viewer, map[string]string{"stateId": child.State.ID, "state": child.State.Name})
	}
	return created.ID, nil
}
