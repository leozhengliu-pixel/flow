package main

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net"
	"net/http"
	"net/mail"
	"slices"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/domain"
)

type triageResponsibilityInput struct {
	Name    string   `json:"name"`
	Mode    string   `json:"mode"`
	UserIDs []string `json:"userIds"`
	Enabled *bool    `json:"enabled,omitempty"`
}

type triageRuleInput struct {
	Name             string            `json:"name"`
	Position         *int              `json:"position,omitempty"`
	Enabled          *bool             `json:"enabled,omitempty"`
	Conditions       map[string]string `json:"conditions"`
	ResponsibilityID string            `json:"responsibilityId"`
	Priority         *int              `json:"priority,omitempty"`
	LabelIDs         []string          `json:"labelIds"`
}

func (s *server) listTriageResponsibilities(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	teamID := r.PathValue("id")
	result := make([]domain.TriageResponsibility, 0)
	for _, item := range data.TriageResponsibilities {
		if item.TeamID == teamID {
			result = append(result, item)
		}
	}
	writeJSON(w, http.StatusOK, result)
}

func validateTriageResponsibility(data *domain.Bootstrap, teamID string, input triageResponsibilityInput) error {
	if strings.TrimSpace(input.Name) == "" || !slices.Contains([]string{"individual", "roundRobin"}, input.Mode) || len(input.UserIDs) == 0 {
		return errInvalid
	}
	if input.Mode == "individual" && len(input.UserIDs) != 1 {
		return fmt.Errorf("%w: individual responsibility requires exactly one user", errInvalid)
	}
	for _, userID := range input.UserIDs {
		if userByID(data, userID) == nil || !slices.ContainsFunc(data.TeamMembers, func(member domain.TeamMember) bool { return member.TeamID == teamID && member.UserID == userID }) {
			return fmt.Errorf("%w: responsibility users must be active team members", errInvalid)
		}
	}
	return nil
}

func (s *server) createTriageResponsibility(w http.ResponseWriter, r *http.Request) {
	var input triageResponsibilityInput
	if !decodeJSON(w, r, &input) {
		return
	}
	teamID := r.PathValue("id")
	var created domain.TriageResponsibility
	err := s.store.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "triage_responsibility.created", input, func(data *domain.Bootstrap) (string, error) {
		if !teamExists(data, teamID) {
			return "", errNotFound
		}
		if err := validateTriageResponsibility(data, teamID, input); err != nil {
			return "", err
		}
		now := time.Now().UTC()
		enabled := true
		if input.Enabled != nil {
			enabled = *input.Enabled
		}
		created = domain.TriageResponsibility{ID: fmt.Sprintf("triage_resp_%d", now.UnixNano()), TeamID: teamID, Name: strings.TrimSpace(input.Name), Mode: input.Mode, UserIDs: slices.Clone(input.UserIDs), Enabled: enabled, CreatedAt: now, UpdatedAt: now}
		data.TriageResponsibilities = append(data.TriageResponsibilities, created)
		return created.ID, nil
	})
	respondMutation(w, err, http.StatusCreated, created)
}

func (s *server) updateTriageResponsibility(w http.ResponseWriter, r *http.Request) {
	var input triageResponsibilityInput
	if !decodeJSON(w, r, &input) {
		return
	}
	teamID, id := r.PathValue("id"), r.PathValue("responsibilityId")
	var updated domain.TriageResponsibility
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "triage_responsibility.updated", id, input, func(data *domain.Bootstrap) error {
		if err := validateTriageResponsibility(data, teamID, input); err != nil {
			return err
		}
		index := slices.IndexFunc(data.TriageResponsibilities, func(item domain.TriageResponsibility) bool { return item.ID == id && item.TeamID == teamID })
		if index < 0 {
			return errNotFound
		}
		item := &data.TriageResponsibilities[index]
		item.Name, item.Mode, item.UserIDs, item.UpdatedAt = strings.TrimSpace(input.Name), input.Mode, slices.Clone(input.UserIDs), time.Now().UTC()
		if input.Enabled != nil {
			item.Enabled = *input.Enabled
		}
		if item.Cursor >= len(item.UserIDs) {
			item.Cursor = 0
		}
		updated = *item
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) deleteTriageResponsibility(w http.ResponseWriter, r *http.Request) {
	teamID, id := r.PathValue("id"), r.PathValue("responsibilityId")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "triage_responsibility.deleted", id, nil, func(data *domain.Bootstrap) error {
		if slices.ContainsFunc(data.TriageRoutingRules, func(rule domain.TriageRoutingRule) bool { return rule.TeamID == teamID && rule.ResponsibilityID == id }) {
			return fmt.Errorf("%w: responsibility is used by a routing rule", errConflict)
		}
		before := len(data.TriageResponsibilities)
		data.TriageResponsibilities = slices.DeleteFunc(data.TriageResponsibilities, func(item domain.TriageResponsibility) bool { return item.ID == id && item.TeamID == teamID })
		if len(data.TriageResponsibilities) == before {
			return errNotFound
		}
		return nil
	})
	respondMutation(w, err, http.StatusNoContent, nil)
}

func (s *server) listTriageRules(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	teamID := r.PathValue("id")
	result := make([]domain.TriageRoutingRule, 0)
	for _, item := range data.TriageRoutingRules {
		if item.TeamID == teamID {
			result = append(result, item)
		}
	}
	slices.SortFunc(result, func(a, b domain.TriageRoutingRule) int { return a.Position - b.Position })
	writeJSON(w, http.StatusOK, result)
}

func validateTriageRule(data *domain.Bootstrap, teamID string, input triageRuleInput) error {
	if strings.TrimSpace(input.Name) == "" || !slices.ContainsFunc(data.TriageResponsibilities, func(item domain.TriageResponsibility) bool {
		return item.ID == input.ResponsibilityID && item.TeamID == teamID
	}) {
		return errInvalid
	}
	if input.Priority != nil && (*input.Priority < 0 || *input.Priority > 4) {
		return errInvalid
	}
	for _, labelID := range input.LabelIDs {
		if !slices.ContainsFunc(data.Labels, func(label domain.IssueLabel) bool { return label.ID == labelID }) {
			return errInvalid
		}
	}
	return nil
}

func (s *server) createTriageRule(w http.ResponseWriter, r *http.Request) {
	var input triageRuleInput
	if !decodeJSON(w, r, &input) {
		return
	}
	teamID := r.PathValue("id")
	var created domain.TriageRoutingRule
	err := s.store.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "triage_rule.created", input, func(data *domain.Bootstrap) (string, error) {
		if err := validateTriageRule(data, teamID, input); err != nil {
			return "", err
		}
		now := time.Now().UTC()
		enabled := true
		if input.Enabled != nil {
			enabled = *input.Enabled
		}
		position := len(data.TriageRoutingRules)
		if input.Position != nil {
			position = *input.Position
		}
		created = domain.TriageRoutingRule{ID: fmt.Sprintf("triage_rule_%d", now.UnixNano()), TeamID: teamID, Name: strings.TrimSpace(input.Name), Position: position, Enabled: enabled, Conditions: input.Conditions, ResponsibilityID: input.ResponsibilityID, Priority: input.Priority, LabelIDs: slices.Clone(input.LabelIDs), CreatedAt: now, UpdatedAt: now}
		if created.Conditions == nil {
			created.Conditions = map[string]string{}
		}
		data.TriageRoutingRules = append(data.TriageRoutingRules, created)
		return created.ID, nil
	})
	respondMutation(w, err, http.StatusCreated, created)
}

func (s *server) updateTriageRule(w http.ResponseWriter, r *http.Request) {
	var input triageRuleInput
	if !decodeJSON(w, r, &input) {
		return
	}
	teamID, id := r.PathValue("id"), r.PathValue("ruleId")
	var updated domain.TriageRoutingRule
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "triage_rule.updated", id, input, func(data *domain.Bootstrap) error {
		if err := validateTriageRule(data, teamID, input); err != nil {
			return err
		}
		index := slices.IndexFunc(data.TriageRoutingRules, func(item domain.TriageRoutingRule) bool { return item.ID == id && item.TeamID == teamID })
		if index < 0 {
			return errNotFound
		}
		item := &data.TriageRoutingRules[index]
		item.Name, item.Conditions, item.ResponsibilityID, item.Priority, item.LabelIDs, item.UpdatedAt = strings.TrimSpace(input.Name), input.Conditions, input.ResponsibilityID, input.Priority, slices.Clone(input.LabelIDs), time.Now().UTC()
		if input.Enabled != nil {
			item.Enabled = *input.Enabled
		}
		if input.Position != nil {
			item.Position = *input.Position
		}
		updated = *item
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) deleteTriageRule(w http.ResponseWriter, r *http.Request) {
	teamID, id := r.PathValue("id"), r.PathValue("ruleId")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "triage_rule.deleted", id, nil, func(data *domain.Bootstrap) error {
		before := len(data.TriageRoutingRules)
		data.TriageRoutingRules = slices.DeleteFunc(data.TriageRoutingRules, func(item domain.TriageRoutingRule) bool { return item.ID == id && item.TeamID == teamID })
		if before == len(data.TriageRoutingRules) {
			return errNotFound
		}
		return nil
	})
	respondMutation(w, err, http.StatusNoContent, nil)
}

func applyTriageRouting(data *domain.Bootstrap, issue *domain.Issue, now time.Time) {
	settings := teamSettings(data, issue.Team.ID)
	if !settings.TriageEnabled {
		return
	}
	rules := make([]*domain.TriageRoutingRule, 0)
	for index := range data.TriageRoutingRules {
		if data.TriageRoutingRules[index].TeamID == issue.Team.ID && data.TriageRoutingRules[index].Enabled {
			rules = append(rules, &data.TriageRoutingRules[index])
		}
	}
	slices.SortFunc(rules, func(a, b *domain.TriageRoutingRule) int { return a.Position - b.Position })
	for _, rule := range rules {
		if !triageRuleMatches(*rule, *issue) {
			continue
		}
		respIndex := slices.IndexFunc(data.TriageResponsibilities, func(item domain.TriageResponsibility) bool {
			return item.ID == rule.ResponsibilityID && item.Enabled && len(item.UserIDs) > 0
		})
		if respIndex < 0 {
			continue
		}
		resp := &data.TriageResponsibilities[respIndex]
		userID := resp.UserIDs[0]
		if resp.Mode == "roundRobin" {
			userID = resp.UserIDs[resp.Cursor%len(resp.UserIDs)]
			resp.Cursor = (resp.Cursor + 1) % len(resp.UserIDs)
			resp.UpdatedAt = now
		}
		issue.Assignee = userByID(data, userID)
		if rule.Priority != nil {
			issue.Priority, issue.PriorityLabel = *rule.Priority, priorityLabel(*rule.Priority)
		}
		issue.Labels = append(issue.Labels, labelsByID(data, rule.LabelIDs)...)
		data.TriageAssignments = append(data.TriageAssignments, domain.TriageAssignment{ID: fmt.Sprintf("triage_assignment_%d", now.UnixNano()), IssueID: issue.ID, RuleID: rule.ID, ResponsibilityID: resp.ID, AssigneeID: userID, CreatedAt: now})
		return
	}
	if issue.Assignee == nil && settings.TriageAction == "creator" {
		issue.Assignee = userByID(data, issue.Creator.ID)
	}
	if issue.Assignee == nil && settings.TriageAction == "teamOwner" {
		for _, member := range data.TeamMembers {
			if member.TeamID == issue.Team.ID && member.Role == "owner" {
				issue.Assignee = userByID(data, member.UserID)
				break
			}
		}
	}
}

func triageRuleMatches(rule domain.TriageRoutingRule, issue domain.Issue) bool {
	for key, expected := range rule.Conditions {
		switch key {
		case "titleContains":
			if !strings.Contains(strings.ToLower(issue.Title), strings.ToLower(expected)) {
				return false
			}
		case "creatorId":
			if issue.Creator.ID != expected {
				return false
			}
		case "priority":
			if strconv.Itoa(issue.Priority) != expected {
				return false
			}
		case "labelId":
			if !slices.ContainsFunc(issue.Labels, func(label domain.IssueLabel) bool { return label.ID == expected }) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

type workflowInput struct {
	Name, Description, TeamID, Trigger, Schedule string
	Conditions                                   map[string]string       `json:"conditions"`
	Actions                                      []domain.WorkflowAction `json:"actions"`
	Enabled                                      *bool                   `json:"enabled,omitempty"`
	MaxAttempts                                  int                     `json:"maxAttempts,omitempty"`
}

func validateWorkflowInput(data *domain.Bootstrap, input workflowInput) (*time.Time, error) {
	if strings.TrimSpace(input.Name) == "" || !slices.Contains([]string{"manual", "schedule", "issueCreated"}, input.Trigger) || len(input.Actions) == 0 {
		return nil, errInvalid
	}
	if input.TeamID != "" && !teamExists(data, input.TeamID) {
		return nil, errInvalid
	}
	for _, action := range input.Actions {
		if !slices.Contains([]string{"notify", "assignIssue", "setIssuePriority", "addIssueLabel", "createIssue"}, action.Type) {
			return nil, fmt.Errorf("%w: unsupported workflow action %s", errInvalid, action.Type)
		}
	}
	if input.Trigger == "schedule" {
		next, err := nextWorkflowSchedule(input.Schedule, time.Now().UTC())
		if err != nil {
			return nil, err
		}
		return &next, nil
	}
	return nil, nil
}

func (s *server) listWorkflowDefinitions(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	writeArrayPage(w, r, data.WorkflowDefinitions)
}
func (s *server) listWorkflowRuns(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	id := r.URL.Query().Get("workflowId")
	result := make([]domain.WorkflowRun, 0)
	for _, run := range data.WorkflowRuns {
		if id == "" || run.WorkflowID == id {
			result = append(result, run)
		}
	}
	slices.SortFunc(result, func(a, b domain.WorkflowRun) int { return b.StartedAt.Compare(a.StartedAt) })
	writeArrayPage(w, r, result)
}

func (s *server) createWorkflowDefinition(w http.ResponseWriter, r *http.Request) {
	var input workflowInput
	if !decodeJSON(w, r, &input) {
		return
	}
	var created domain.WorkflowDefinition
	err := s.store.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "workflow_definition.created", input, func(data *domain.Bootstrap) (string, error) {
		next, err := validateWorkflowInput(data, input)
		if err != nil {
			return "", err
		}
		now := time.Now().UTC()
		enabled := true
		if input.Enabled != nil {
			enabled = *input.Enabled
		}
		max := input.MaxAttempts
		if max < 1 {
			max = 3
		}
		created = domain.WorkflowDefinition{ID: fmt.Sprintf("workflow_%d", now.UnixNano()), Name: strings.TrimSpace(input.Name), Description: strings.TrimSpace(input.Description), TeamID: input.TeamID, Trigger: input.Trigger, Schedule: strings.TrimSpace(input.Schedule), Conditions: input.Conditions, Actions: input.Actions, Enabled: enabled, MaxAttempts: max, NextRunAt: next, CreatorID: data.Viewer.ID, CreatedAt: now, UpdatedAt: now}
		if created.Conditions == nil {
			created.Conditions = map[string]string{}
		}
		data.WorkflowDefinitions = append(data.WorkflowDefinitions, created)
		return created.ID, nil
	})
	respondMutation(w, err, http.StatusCreated, created)
}

func (s *server) updateWorkflowDefinition(w http.ResponseWriter, r *http.Request) {
	var input workflowInput
	if !decodeJSON(w, r, &input) {
		return
	}
	id := r.PathValue("id")
	var updated domain.WorkflowDefinition
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "workflow_definition.updated", id, input, func(data *domain.Bootstrap) error {
		next, err := validateWorkflowInput(data, input)
		if err != nil {
			return err
		}
		index := slices.IndexFunc(data.WorkflowDefinitions, func(item domain.WorkflowDefinition) bool { return item.ID == id })
		if index < 0 {
			return errNotFound
		}
		item := &data.WorkflowDefinitions[index]
		item.Name, item.Description, item.TeamID, item.Trigger, item.Schedule, item.Conditions, item.Actions, item.MaxAttempts, item.NextRunAt, item.UpdatedAt = strings.TrimSpace(input.Name), strings.TrimSpace(input.Description), input.TeamID, input.Trigger, strings.TrimSpace(input.Schedule), input.Conditions, input.Actions, max(1, input.MaxAttempts), next, time.Now().UTC()
		if input.Enabled != nil {
			item.Enabled = *input.Enabled
		}
		updated = *item
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}
func (s *server) deleteWorkflowDefinition(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "workflow_definition.deleted", id, nil, func(data *domain.Bootstrap) error {
		before := len(data.WorkflowDefinitions)
		data.WorkflowDefinitions = slices.DeleteFunc(data.WorkflowDefinitions, func(item domain.WorkflowDefinition) bool { return item.ID == id })
		if before == len(data.WorkflowDefinitions) {
			return errNotFound
		}
		return nil
	})
	respondMutation(w, err, http.StatusNoContent, nil)
}
func (s *server) runWorkflowDefinition(w http.ResponseWriter, r *http.Request) {
	run, err := s.executeWorkflow(r.Context(), workspaceKey(r), r.PathValue("id"), "manual", "", "")
	respondMutation(w, err, http.StatusAccepted, run)
}
func (s *server) retryWorkflowRun(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	runID := r.PathValue("runId")
	index := slices.IndexFunc(data.WorkflowRuns, func(item domain.WorkflowRun) bool { return item.ID == runID })
	if index < 0 {
		writeError(w, http.StatusNotFound, "workflow run not found")
		return
	}
	run, err := s.executeWorkflow(r.Context(), workspaceKey(r), data.WorkflowRuns[index].WorkflowID, "retry", data.WorkflowRuns[index].ResourceType, data.WorkflowRuns[index].ResourceID)
	respondMutation(w, err, http.StatusAccepted, run)
}

func (s *server) executeWorkflow(ctx context.Context, key, id, trigger, resourceType, resourceID string) (domain.WorkflowRun, error) {
	var run domain.WorkflowRun
	err := s.store.MutateWorkspace(ctx, key, "workflow_run.executed", id, map[string]string{"trigger": trigger}, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.WorkflowDefinitions, func(item domain.WorkflowDefinition) bool { return item.ID == id })
		if index < 0 {
			return errNotFound
		}
		definition := &data.WorkflowDefinitions[index]
		if !definition.Enabled && trigger != "manual" {
			return errInvalid
		}
		now := time.Now().UTC()
		attempt := 1
		for i := len(data.WorkflowRuns) - 1; i >= 0; i-- {
			if data.WorkflowRuns[i].WorkflowID == id {
				attempt = data.WorkflowRuns[i].Attempt + 1
				if trigger == "retry" && data.WorkflowRuns[i].Status == "failed" && data.WorkflowRuns[i].NextRetryAt != nil {
					data.WorkflowRuns[i].NextRetryAt = nil
				}
				break
			}
		}
		run = domain.WorkflowRun{ID: fmt.Sprintf("workflow_run_%d", now.UnixNano()), WorkflowID: id, Trigger: trigger, ResourceType: resourceType, ResourceID: resourceID, Status: "running", Attempt: attempt, Output: map[string]string{}, ScheduledAt: now, StartedAt: now}
		execErr := applyWorkflowActions(data, *definition, &run)
		done := time.Now().UTC()
		run.CompletedAt = &done
		definition.LastRunAt = &done
		definition.UpdatedAt = done
		if definition.Trigger == "schedule" {
			if next, e := nextWorkflowSchedule(definition.Schedule, done); e == nil {
				definition.NextRunAt = &next
			}
		}
		if execErr != nil {
			run.Status = "failed"
			run.Error = execErr.Error()
			definition.LastRunStatus = "failed"
			definition.ConsecutiveErr++
			if attempt < definition.MaxAttempts {
				retry := done.Add(time.Duration(1<<min(attempt, 6)) * time.Minute)
				run.NextRetryAt = &retry
			}
		} else {
			run.Status = "succeeded"
			definition.LastRunStatus = "succeeded"
			definition.ConsecutiveErr = 0
		}
		data.WorkflowRuns = append(data.WorkflowRuns, run)
		if len(data.WorkflowRuns) > 500 {
			data.WorkflowRuns = data.WorkflowRuns[len(data.WorkflowRuns)-500:]
		}
		return nil
	})
	return run, err
}

func applyWorkflowActions(data *domain.Bootstrap, definition domain.WorkflowDefinition, run *domain.WorkflowRun) error {
	var issue *domain.Issue
	if run.ResourceType == "issue" && run.ResourceID != "" {
		issue, _ = issueByID(data, run.ResourceID)
	}
	for _, action := range definition.Actions {
		switch action.Type {
		case "notify":
			recipient := action.Config["recipientId"]
			if recipient == "" {
				recipient = definition.CreatorID
			}
			if userByID(data, recipient) == nil {
				return fmt.Errorf("notification recipient no longer exists")
			}
			now := time.Now().UTC()
			data.Notifications = append(data.Notifications, domain.Notification{ID: fmt.Sprintf("notification_workflow_%d", now.UnixNano()), RecipientID: recipient, Type: "workflow", SourceType: "workflow", SourceID: definition.ID, IssueID: run.ResourceID, Actor: data.Viewer, Category: "loops", GroupKey: "workflow:" + definition.ID, OccurrenceCount: 1, LatestActorIDs: []string{data.Viewer.ID}, CreatedAt: now, UpdatedAt: now})
		case "assignIssue":
			if issue == nil {
				return fmt.Errorf("assignIssue requires an issue trigger")
			}
			user := userByID(data, action.Config["userId"])
			if user == nil {
				return fmt.Errorf("workflow assignee no longer exists")
			}
			issue.Assignee = user
			issue.UpdatedAt = time.Now().UTC()
		case "setIssuePriority":
			if issue == nil {
				return fmt.Errorf("setIssuePriority requires an issue trigger")
			}
			priority, err := strconv.Atoi(action.Config["priority"])
			if err != nil || priority < 0 || priority > 4 {
				return fmt.Errorf("invalid priority")
			}
			issue.Priority, issue.PriorityLabel = priority, priorityLabel(priority)
			issue.UpdatedAt = time.Now().UTC()
		case "addIssueLabel":
			if issue == nil {
				return fmt.Errorf("addIssueLabel requires an issue trigger")
			}
			labels := labelsByID(data, []string{action.Config["labelId"]})
			if len(labels) == 0 {
				return fmt.Errorf("workflow label no longer exists")
			}
			if !slices.ContainsFunc(issue.Labels, func(item domain.IssueLabel) bool { return item.ID == labels[0].ID }) {
				issue.Labels = append(issue.Labels, labels[0])
			}
		case "createIssue":
			teamID := action.Config["teamId"]
			teamIndex := slices.IndexFunc(data.Teams, func(item domain.Team) bool { return item.ID == teamID })
			if teamIndex < 0 {
				return fmt.Errorf("workflow team no longer exists")
			}
			number := nextIssueNumber(data.Issues)
			settings := teamSettings(data, teamID)
			state := stateForTeam(data, teamID, settings.DefaultStateID)
			if state == nil {
				return fmt.Errorf("workflow team has no default state")
			}
			now := time.Now().UTC()
			created := domain.Issue{ID: fmt.Sprintf("issue_%d", number), Version: 1, Identifier: fmt.Sprintf("%s-%d", data.Teams[teamIndex].Key, number), Number: number, Title: strings.TrimSpace(action.Config["title"]), Priority: settings.DefaultPriority, PriorityLabel: priorityLabel(settings.DefaultPriority), SortOrder: float64(number), CreatedAt: now, UpdatedAt: now, Team: data.Teams[teamIndex], State: *state, Creator: data.Viewer, Labels: []domain.IssueLabel{}, SubscriberIDs: []string{data.Viewer.ID}, Reactions: map[string][]string{}, SubIssueIDs: []string{}, Relations: []domain.IssueRelation{}, Attachments: []domain.Attachment{}}
			if created.Title == "" {
				return fmt.Errorf("createIssue title is required")
			}
			data.Issues = append([]domain.Issue{created}, data.Issues...)
			run.Output["issueId"] = created.ID
		}
	}
	return nil
}

func applyTriggeredWorkflows(data *domain.Bootstrap, trigger, resourceType, resourceID string) {
	for index := range data.WorkflowDefinitions {
		definition := &data.WorkflowDefinitions[index]
		if !definition.Enabled || definition.Trigger != trigger {
			continue
		}
		if definition.TeamID != "" && resourceType == "issue" {
			issue, err := issueByID(data, resourceID)
			if err != nil || issue.Team.ID != definition.TeamID {
				continue
			}
		}
		if !workflowConditionsMatch(data, *definition, resourceType, resourceID) {
			continue
		}
		now := time.Now().UTC()
		run := domain.WorkflowRun{ID: fmt.Sprintf("workflow_run_%d_%d", now.UnixNano(), index), WorkflowID: definition.ID, Trigger: trigger, ResourceType: resourceType, ResourceID: resourceID, Status: "running", Attempt: 1, Output: map[string]string{}, ScheduledAt: now, StartedAt: now}
		err := applyWorkflowActions(data, *definition, &run)
		done := time.Now().UTC()
		run.CompletedAt = &done
		definition.LastRunAt = &done
		if err != nil {
			run.Status = "failed"
			run.Error = err.Error()
			definition.LastRunStatus = "failed"
			definition.ConsecutiveErr++
		} else {
			run.Status = "succeeded"
			definition.LastRunStatus = "succeeded"
			definition.ConsecutiveErr = 0
		}
		data.WorkflowRuns = append(data.WorkflowRuns, run)
	}
}

func workflowConditionsMatch(data *domain.Bootstrap, definition domain.WorkflowDefinition, resourceType, resourceID string) bool {
	if len(definition.Conditions) == 0 {
		return true
	}
	if resourceType != "issue" {
		return false
	}
	issue, err := issueByID(data, resourceID)
	if err != nil {
		return false
	}
	for key, expected := range definition.Conditions {
		switch key {
		case "teamId":
			if issue.Team.ID != expected {
				return false
			}
		case "priority":
			if strconv.Itoa(issue.Priority) != expected {
				return false
			}
		case "stateId":
			if issue.State.ID != expected {
				return false
			}
		case "labelId":
			if !slices.ContainsFunc(issue.Labels, func(label domain.IssueLabel) bool { return label.ID == expected }) {
				return false
			}
		case "titleContains":
			if !strings.Contains(strings.ToLower(issue.Title), strings.ToLower(expected)) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func nextWorkflowSchedule(schedule string, after time.Time) (time.Time, error) {
	value := strings.TrimSpace(strings.ToLower(schedule))
	switch value {
	case "@hourly":
		return after.Truncate(time.Hour).Add(time.Hour), nil
	case "@daily":
		return time.Date(after.Year(), after.Month(), after.Day()+1, 0, 0, 0, 0, time.UTC), nil
	case "@weekly":
		day := time.Date(after.Year(), after.Month(), after.Day(), 0, 0, 0, 0, time.UTC)
		return day.AddDate(0, 0, 7-int(day.Weekday())), nil
	}
	if strings.HasPrefix(value, "@every ") {
		duration, err := time.ParseDuration(strings.TrimSpace(strings.TrimPrefix(value, "@every ")))
		if err != nil || duration < time.Minute {
			return time.Time{}, fmt.Errorf("%w: schedule interval must be at least one minute", errInvalid)
		}
		return after.Add(duration), nil
	}
	parts := strings.Fields(value)
	if len(parts) != 5 {
		return time.Time{}, fmt.Errorf("%w: schedule must be a five-field cron expression", errInvalid)
	}
	candidate := after.Truncate(time.Minute).Add(time.Minute)
	for i := 0; i < 366*24*60; i++ {
		if cronField(parts[0], candidate.Minute(), 0, 59) && cronField(parts[1], candidate.Hour(), 0, 23) && cronField(parts[2], candidate.Day(), 1, 31) && cronField(parts[3], int(candidate.Month()), 1, 12) && cronField(parts[4], int(candidate.Weekday()), 0, 6) {
			return candidate, nil
		}
		candidate = candidate.Add(time.Minute)
	}
	return time.Time{}, fmt.Errorf("%w: schedule has no occurrence in the next year", errInvalid)
}
func cronField(expression string, value, minValue, maxValue int) bool {
	for _, part := range strings.Split(expression, ",") {
		step := 1
		base := part
		if pair := strings.SplitN(part, "/", 2); len(pair) == 2 {
			parsed, err := strconv.Atoi(pair[1])
			if err != nil || parsed < 1 {
				return false
			}
			step = parsed
			base = pair[0]
		}
		low, high := minValue, maxValue
		if base != "*" {
			if pair := strings.SplitN(base, "-", 2); len(pair) == 2 {
				var e1, e2 error
				low, e1 = strconv.Atoi(pair[0])
				high, e2 = strconv.Atoi(pair[1])
				if e1 != nil || e2 != nil {
					return false
				}
			} else {
				parsed, err := strconv.Atoi(base)
				if err != nil {
					return false
				}
				low, high = parsed, parsed
			}
		}
		if value >= low && value <= high && (value-low)%step == 0 {
			return true
		}
	}
	return false
}

func (s *server) startWorkflowScheduler() {
	if !s.workflowSchedulerStarted.CompareAndSwap(false, true) {
		return
	}
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			now := time.Now().UTC()
			for _, key := range s.store.WorkspaceKeys() {
				data, ok := s.store.BootstrapFor(key)
				if !ok {
					continue
				}
				for _, definition := range data.WorkflowDefinitions {
					if definition.Enabled && definition.Trigger == "schedule" && definition.NextRunAt != nil && !definition.NextRunAt.After(now) {
						_, _ = s.executeWorkflow(context.Background(), key, definition.ID, "schedule", "", "")
					}
				}
				for _, run := range data.WorkflowRuns {
					if run.Status == "failed" && run.NextRetryAt != nil && !run.NextRetryAt.After(now) {
						_, _ = s.executeWorkflow(context.Background(), key, run.WorkflowID, "retry", run.ResourceType, run.ResourceID)
					}
				}
			}
		}
	}()
}

type emailIntakeInput struct {
	LocalPart, Domain string
	Enabled           *bool `json:"enabled,omitempty"`
}

func (s *server) listEmailIntakeAddresses(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	teamID := r.PathValue("id")
	result := make([]domain.EmailIntakeAddress, 0)
	for _, item := range data.EmailIntakeAddresses {
		if item.TeamID == teamID {
			item.InboundTokenHash = ""
			for index := range item.Aliases {
				item.Aliases[index].TokenHash = ""
			}
			result = append(result, item)
		}
	}
	writeJSON(w, http.StatusOK, result)
}
func (s *server) createEmailIntakeAddress(w http.ResponseWriter, r *http.Request) {
	var input emailIntakeInput
	if !decodeJSON(w, r, &input) {
		return
	}
	teamID := r.PathValue("id")
	input.LocalPart = strings.ToLower(strings.TrimSpace(input.LocalPart))
	input.Domain = strings.ToLower(strings.TrimSpace(input.Domain))
	if _, err := mail.ParseAddress(input.LocalPart + "@" + input.Domain); err != nil {
		writeError(w, http.StatusBadRequest, "valid localPart and domain are required")
		return
	}
	var created domain.EmailIntakeAddress
	var inboundToken string
	err := s.store.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "email_intake.created", input, func(data *domain.Bootstrap) (string, error) {
		if !teamExists(data, teamID) {
			return "", errNotFound
		}
		if slices.ContainsFunc(data.EmailIntakeAddresses, func(item domain.EmailIntakeAddress) bool {
			return strings.EqualFold(item.Address, input.LocalPart+"@"+input.Domain) && item.Enabled
		}) {
			return "", errConflict
		}
		now := time.Now().UTC()
		inboundToken = randomURLToken(24)
		verification := randomURLToken(18)
		created = domain.EmailIntakeAddress{ID: fmt.Sprintf("email_intake_%d", now.UnixNano()), TeamID: teamID, LocalPart: input.LocalPart, Domain: input.Domain, Address: input.LocalPart + "@" + input.Domain, InboundTokenHash: secretHash(inboundToken), VerificationToken: verification, VerificationState: "pending", Aliases: []domain.EmailIntakeAlias{}, Enabled: true, CreatedAt: now, UpdatedAt: now}
		if input.Enabled != nil {
			created.Enabled = *input.Enabled
		}
		data.EmailIntakeAddresses = append(data.EmailIntakeAddresses, created)
		return created.ID, nil
	})
	if err == nil {
		publicAddress := created
		publicAddress.InboundTokenHash = ""
		writeJSON(w, http.StatusCreated, map[string]any{"address": publicAddress, "inboundToken": inboundToken, "dnsRecord": map[string]string{"type": "TXT", "name": "_flow-intake." + created.Domain, "value": "flow-verification=" + created.VerificationToken}})
		return
	}
	respondMutation(w, err, http.StatusCreated, nil)
}
func (s *server) verifyEmailIntakeAddress(w http.ResponseWriter, r *http.Request) {
	var input struct {
		TXTValue string `json:"txtValue"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	teamID, id := r.PathValue("id"), r.PathValue("addressId")
	var updated domain.EmailIntakeAddress
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "email_intake.verified", id, input, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.EmailIntakeAddresses, func(item domain.EmailIntakeAddress) bool { return item.ID == id && item.TeamID == teamID })
		if index < 0 {
			return errNotFound
		}
		item := &data.EmailIntakeAddresses[index]
		expected := "flow-verification=" + item.VerificationToken
		found := strings.TrimSpace(input.TXTValue) == expected
		if !found && strings.TrimSpace(input.TXTValue) == "" {
			records, _ := net.LookupTXT("_flow-intake." + item.Domain)
			found = slices.Contains(records, expected)
		}
		if !found {
			return fmt.Errorf("%w: TXT verification record was not found", errInvalid)
		}
		now := time.Now().UTC()
		item.VerificationState = "verified"
		item.VerifiedAt = &now
		item.VerificationToken = ""
		item.UpdatedAt = now
		updated = *item
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}
func (s *server) rotateEmailIntakeAddress(w http.ResponseWriter, r *http.Request) {
	teamID, id := r.PathValue("id"), r.PathValue("addressId")
	var result map[string]any
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "email_intake.rotated", id, nil, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.EmailIntakeAddresses, func(item domain.EmailIntakeAddress) bool { return item.ID == id && item.TeamID == teamID })
		if index < 0 {
			return errNotFound
		}
		item := &data.EmailIntakeAddresses[index]
		now := time.Now().UTC()
		expires := now.Add(7 * 24 * time.Hour)
		item.Aliases = append(item.Aliases, domain.EmailIntakeAlias{Address: item.Address, TokenHash: item.InboundTokenHash, ExpiresAt: expires})
		token := randomURLToken(24)
		suffix := strconv.FormatInt(now.Unix()%100000, 36)
		item.LocalPart = strings.TrimSuffix(item.LocalPart, "-"+suffix) + "-" + suffix
		item.Address = item.LocalPart + "@" + item.Domain
		item.InboundTokenHash = secretHash(token)
		item.UpdatedAt = now
		publicAddress := *item
		publicAddress.InboundTokenHash = ""
		for index := range publicAddress.Aliases {
			publicAddress.Aliases[index].TokenHash = ""
		}
		result = map[string]any{"address": publicAddress, "inboundToken": token}
		return nil
	})
	if err == nil {
		writeJSON(w, http.StatusOK, result)
		return
	}
	respondMutation(w, err, http.StatusOK, nil)
}
func (s *server) deleteEmailIntakeAddress(w http.ResponseWriter, r *http.Request) {
	teamID, id := r.PathValue("id"), r.PathValue("addressId")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "email_intake.deleted", id, nil, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.EmailIntakeAddresses, func(item domain.EmailIntakeAddress) bool { return item.ID == id && item.TeamID == teamID })
		if index < 0 {
			return errNotFound
		}
		data.EmailIntakeAddresses[index].Enabled = false
		data.EmailIntakeAddresses[index].UpdatedAt = time.Now().UTC()
		return nil
	})
	respondMutation(w, err, http.StatusNoContent, nil)
}

func (s *server) receiveEmailIntake(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	var input struct {
		MessageID, From, Subject, Text string
		Attachments                    []string `json:"attachments"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.MessageID = strings.TrimSpace(input.MessageID)
	if input.MessageID == "" || strings.TrimSpace(input.Subject) == "" {
		writeError(w, http.StatusBadRequest, "messageId and subject are required")
		return
	}
	var created domain.Issue
	key := ""
	for _, workspaceKey := range s.store.WorkspaceKeys() {
		data, ok := s.store.BootstrapFor(workspaceKey)
		if !ok {
			continue
		}
		for _, address := range data.EmailIntakeAddresses {
			valid := subtle.ConstantTimeCompare([]byte(address.InboundTokenHash), []byte(secretHash(token))) == 1
			for _, alias := range address.Aliases {
				if alias.ExpiresAt.After(time.Now().UTC()) && subtle.ConstantTimeCompare([]byte(alias.TokenHash), []byte(secretHash(token))) == 1 {
					valid = true
				}
			}
			if address.Enabled && address.VerificationState == "verified" && valid {
				key = workspaceKey
				break
			}
		}
		if key != "" {
			break
		}
	}
	if key == "" {
		writeError(w, http.StatusUnauthorized, "invalid or unverified intake address")
		return
	}
	err := s.store.MutateWorkspaceWithAggregate(r.Context(), key, "email_intake.received", input, func(data *domain.Bootstrap) (string, error) {
		if existing := slices.IndexFunc(data.EmailIntakeMessages, func(item domain.EmailIntakeMessage) bool { return item.MessageID == input.MessageID }); existing >= 0 {
			if issue, e := issueByID(data, data.EmailIntakeMessages[existing].IssueID); e == nil {
				created = *issue
				return created.ID, nil
			}
			return "", errConflict
		}
		addressIndex := slices.IndexFunc(data.EmailIntakeAddresses, func(item domain.EmailIntakeAddress) bool {
			if subtle.ConstantTimeCompare([]byte(item.InboundTokenHash), []byte(secretHash(token))) == 1 {
				return true
			}
			return slices.ContainsFunc(item.Aliases, func(alias domain.EmailIntakeAlias) bool {
				return alias.ExpiresAt.After(time.Now().UTC()) && subtle.ConstantTimeCompare([]byte(alias.TokenHash), []byte(secretHash(token))) == 1
			})
		})
		if addressIndex < 0 {
			return "", errNotFound
		}
		address := data.EmailIntakeAddresses[addressIndex]
		teamIndex := slices.IndexFunc(data.Teams, func(item domain.Team) bool { return item.ID == address.TeamID })
		if teamIndex < 0 {
			return "", errNotFound
		}
		settings := teamSettings(data, address.TeamID)
		state := stateForTeam(data, address.TeamID, settings.DefaultStateID)
		if state == nil {
			return "", errInvalid
		}
		now := time.Now().UTC()
		number := nextIssueNumber(data.Issues)
		description := strings.TrimSpace(input.Text)
		if len(input.Attachments) > 0 {
			description += "\n\nAttachments:\n- " + strings.Join(input.Attachments, "\n- ")
		}
		created = domain.Issue{ID: fmt.Sprintf("issue_%d", number), Version: 1, Identifier: fmt.Sprintf("%s-%d", data.Teams[teamIndex].Key, number), Number: number, Title: strings.TrimSpace(input.Subject), Description: description, Priority: settings.DefaultPriority, PriorityLabel: priorityLabel(settings.DefaultPriority), SortOrder: float64(number), CreatedAt: now, UpdatedAt: now, Team: data.Teams[teamIndex], State: *state, Creator: data.Viewer, Labels: []domain.IssueLabel{}, SubscriberIDs: []string{data.Viewer.ID}, Reactions: map[string][]string{}, SubIssueIDs: []string{}, Relations: []domain.IssueRelation{}, Attachments: []domain.Attachment{}}
		applyTriageRouting(data, &created, now)
		data.Issues = append([]domain.Issue{created}, data.Issues...)
		data.EmailIntakeMessages = append(data.EmailIntakeMessages, domain.EmailIntakeMessage{ID: fmt.Sprintf("email_message_%d", now.UnixNano()), AddressID: address.ID, MessageID: input.MessageID, From: input.From, Subject: input.Subject, IssueID: created.ID, Status: "processed", ReceivedAt: now, ProcessedAt: &now})
		return created.ID, nil
	})
	respondMutation(w, err, http.StatusCreated, created)
}

type pushSubscriptionInput struct{ Endpoint, P256DH, Auth string }

func (s *server) listPushSubscriptions(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	result := make([]domain.PushSubscription, 0)
	for _, item := range data.PushSubscriptions {
		if item.UserID == data.Viewer.ID {
			item.P256DH, item.Auth = "", ""
			result = append(result, item)
		}
	}
	writeJSON(w, http.StatusOK, result)
}
func (s *server) createPushSubscription(w http.ResponseWriter, r *http.Request) {
	var input pushSubscriptionInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if !strings.HasPrefix(input.Endpoint, "https://") || input.P256DH == "" || input.Auth == "" {
		writeError(w, http.StatusBadRequest, "valid endpoint and browser keys are required")
		return
	}
	viewerID := s.workspaceData(r).Viewer.ID
	var created domain.PushSubscription
	err := s.store.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "push_subscription.created", input, func(data *domain.Bootstrap) (string, error) {
		now := time.Now().UTC()
		index := slices.IndexFunc(data.PushSubscriptions, func(item domain.PushSubscription) bool {
			return item.UserID == viewerID && item.Endpoint == input.Endpoint
		})
		if index >= 0 {
			item := &data.PushSubscriptions[index]
			item.P256DH, item.Auth, item.Enabled, item.UpdatedAt = input.P256DH, input.Auth, true, now
			created = *item
			return item.ID, nil
		}
		created = domain.PushSubscription{ID: fmt.Sprintf("push_subscription_%d", now.UnixNano()), UserID: viewerID, Endpoint: input.Endpoint, P256DH: input.P256DH, Auth: input.Auth, UserAgent: r.UserAgent(), Enabled: true, CreatedAt: now, UpdatedAt: now}
		data.PushSubscriptions = append(data.PushSubscriptions, created)
		return created.ID, nil
	})
	created.P256DH, created.Auth = "", ""
	respondMutation(w, err, http.StatusCreated, created)
}
func (s *server) deletePushSubscription(w http.ResponseWriter, r *http.Request) {
	viewerID := s.workspaceData(r).Viewer.ID
	id := r.PathValue("id")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "push_subscription.deleted", id, nil, func(data *domain.Bootstrap) error {
		before := len(data.PushSubscriptions)
		data.PushSubscriptions = slices.DeleteFunc(data.PushSubscriptions, func(item domain.PushSubscription) bool { return item.ID == id && item.UserID == viewerID })
		if before == len(data.PushSubscriptions) {
			return errNotFound
		}
		return nil
	})
	respondMutation(w, err, http.StatusNoContent, nil)
}
