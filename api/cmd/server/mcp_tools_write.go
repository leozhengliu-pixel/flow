package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

type mcpPendingUpload struct {
	WorkspaceKey string
	UserID       string
	IssueID      string
	Filename     string
	ContentType  string
	Title        string
	Subtitle     string
	ExpectedSize int64
	ObjectKey    string
	ActualSize   int64
	Completed    bool
	ExpiresAt    time.Time
}

func (s *server) callLinearCompatibleWriteTool(ctx context.Context, actor mcpActor, data domain.Bootstrap, name string, args map[string]any) (any, error) {
	switch name {
	case "create_issue_label", "create_initiative_label":
		return s.createMCPLabel(ctx, actor, data, name, args)
	case "save_issue":
		return s.saveMCPIssue(ctx, actor, data, args)
	case "save_project":
		return s.saveMCPProject(ctx, actor, data, args)
	case "save_initiative":
		return s.saveMCPInitiative(ctx, actor, data, args)
	case "save_milestone":
		return s.saveMCPMilestone(ctx, actor, data, args)
	case "save_release":
		return s.saveMCPRelease(ctx, actor, data, args)
	case "save_comment":
		return s.saveMCPComment(ctx, actor, data, args)
	case "delete_comment":
		return s.deleteMCPComment(ctx, actor, stringArg(args, "id"))
	case "prepare_attachment_upload":
		return s.prepareMCPAttachmentUpload(actor, data, args)
	case "create_attachment":
		return s.createMCPAttachment(ctx, actor, data, args)
	case "create_attachment_from_upload":
		return s.finalizeMCPAttachmentUpload(ctx, actor, data, args)
	case "delete_attachment":
		return s.deleteMCPAttachment(ctx, actor, data, stringArg(args, "id"))
	case "merge_diff":
		return s.updateMCPReview(ctx, actor, data, args, "merge")
	case "submit_diff_review":
		return s.updateMCPReview(ctx, actor, data, args, "submit")
	case "resolve_diff_thread":
		return s.resolveMCPDiffThread(ctx, actor, data, args)
	case "delete_diff_comment":
		return s.deleteMCPDiffComment(ctx, actor, args)
	default:
		return nil, fmt.Errorf("tool %q is not implemented", name)
	}
}

func (s *server) createMCPLabel(ctx context.Context, actor mcpActor, data domain.Bootstrap, tool string, args map[string]any) (any, error) {
	name := stringArg(args, "name")
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	resourceType := "issue"
	if tool == "create_initiative_label" {
		resourceType = "initiative"
	}
	scope := ""
	if teamID := stringArg(args, "teamId"); teamID != "" {
		team, err := mcpFindTeam(data, teamID)
		if err != nil {
			return nil, err
		}
		scope = team.ID
	}
	color := stringArg(args, "color")
	if color == "" {
		color = "#5e6ad2"
	}
	if boolArg(args, "isGroup") {
		group := domain.LabelGroup{ID: fmt.Sprintf("label_group_%d", time.Now().UnixNano()), Name: name, Color: color, Description: stringArg(args, "description"), Scope: scope, ResourceType: resourceType, CreatedAt: time.Now().UTC()}
		err := s.store.MutateWorkspace(ctx, actor.WorkspaceKey, "label_group.created", group.ID, args, func(next *domain.Bootstrap) error { next.LabelGroups = append(next.LabelGroups, group); return nil })
		return group, err
	}
	groupID := ""
	if parent := stringArg(args, "parent"); parent != "" {
		for _, group := range data.LabelGroups {
			if equalFoldAny(parent, group.ID, group.Name) && group.ResourceType == resourceType {
				groupID = group.ID
				break
			}
		}
		if groupID == "" {
			return nil, fmt.Errorf("parent label group not found")
		}
	}
	label := domain.IssueLabel{ID: fmt.Sprintf("label_%d", time.Now().UnixNano()), Name: name, Color: color, Description: stringArg(args, "description"), Scope: scope, ResourceType: resourceType, GroupID: groupID, CreatorID: actor.User.ID, CreatedAt: time.Now().UTC()}
	err := s.store.MutateWorkspace(ctx, actor.WorkspaceKey, "label.created", label.ID, args, func(next *domain.Bootstrap) error { next.Labels = append(next.Labels, label); return nil })
	return label, err
}

func (s *server) saveMCPIssue(ctx context.Context, actor mcpActor, data domain.Bootstrap, args map[string]any) (any, error) {
	id := stringArg(args, "id")
	var current domain.Issue
	var err error
	if id != "" {
		current, err = mcpFindIssue(data, id)
		if err != nil {
			return nil, err
		}
	}
	team := current.Team
	if query := stringArg(args, "team"); query != "" {
		team, err = mcpFindTeam(data, query)
		if err != nil {
			return nil, err
		}
	}
	if team.ID == "" {
		return nil, fmt.Errorf("team is required when creating an issue")
	}
	description := current.Description
	if value, ok := args["description"].(string); ok {
		description = value
	}
	if patches, ok := args["patch"].([]any); ok {
		description, err = applyTextPatches(description, patches)
		if err != nil {
			return nil, err
		}
	}
	stateID, err := resolveStateID(data, team.ID, stringArg(args, "state"))
	if err != nil {
		return nil, err
	}
	assigneeID, err := resolveNullableUserID(data, args, "assignee")
	if err != nil {
		return nil, err
	}
	delegateID, err := resolveNullableUserID(data, args, "delegate")
	if err != nil {
		return nil, err
	}
	projectID, err := resolveNullableProjectID(data, args, "project")
	if err != nil {
		return nil, err
	}
	cycleID, err := resolveNullableCycleID(data, args, "cycle")
	if err != nil {
		return nil, err
	}
	parentID, err := resolveNullableIssueID(data, args, "parentId")
	if err != nil {
		return nil, err
	}
	labelIDs, err := resolveLabelIDs(data, stringsArg(args, "labels"), "issue")
	if err != nil {
		return nil, err
	}
	milestoneID := ""
	if milestone := stringArg(args, "milestone"); milestone != "" {
		milestoneID, err = resolveMilestoneID(data, projectID, milestone)
		if err != nil {
			return nil, err
		}
	}
	var result any
	if id == "" {
		title := stringArg(args, "title")
		if title == "" {
			return nil, fmt.Errorf("title is required when creating an issue")
		}
		input := domain.IssueCreateInput{Title: title, Description: description, TeamID: team.ID}
		if stateID != "" {
			input.StateID = &stateID
		}
		if hasNumberArg(args, "priority") {
			value := intArg(args, "priority", 0)
			input.Priority = &value
		}
		input.AssigneeID, input.DelegateID, input.ProjectID, input.CycleID, input.ParentID = assigneeID, delegateID, projectID, cycleID, parentID
		if milestoneID != "" {
			input.ProjectMilestoneID = &milestoneID
		}
		if value, present := nullableStringArg(args, "dueDate"); present {
			input.DueDate = &value
		}
		if value, present := nullableStringArg(args, "slaBreachesAt"); present {
			input.SLABreachesAt = &value
		}
		if value, present := nullableStringArg(args, "slaType"); present {
			input.SLAType = &value
		}
		input.LabelIDs = labelIDs
		result, err = invokeJSONHandler(ctx, http.MethodPost, nil, input, s.createIssue)
	} else {
		input := domain.IssueUpdateInput{}
		if value, ok := args["title"].(string); ok {
			input.Title = &value
		}
		if _, ok := args["description"]; ok || args["patch"] != nil {
			input.Description = &description
		}
		if stateID != "" {
			input.StateID = &stateID
		}
		if hasNumberArg(args, "priority") {
			value := intArg(args, "priority", 0)
			input.Priority = &value
		}
		input.AssigneeID, input.DelegateID, input.ProjectID, input.CycleID, input.ParentID = assigneeID, delegateID, projectID, cycleID, parentID
		if _, present := args["labels"]; present {
			input.LabelIDs = &labelIDs
		}
		if value, present := nullableStringArg(args, "dueDate"); present {
			input.DueDate = &value
		}
		if value, present := nullableStringArg(args, "slaBreachesAt"); present {
			input.SLABreachesAt = &value
		}
		if value, present := nullableStringArg(args, "slaType"); present {
			input.SLAType = &value
		}
		if milestoneID != "" {
			input.ProjectMilestoneID = &milestoneID
		}
		result, err = invokeJSONHandler(ctx, http.MethodPatch, map[string]string{"id": current.ID}, input, s.updateIssue)
	}
	if err != nil {
		return nil, err
	}
	var saved domain.Issue
	if err := jsonClone(result, &saved); err != nil {
		return nil, err
	}
	if links, ok := args["links"].([]any); ok {
		for _, raw := range links {
			var link domain.IssueLinkInput
			if err := jsonClone(raw, &link); err != nil {
				return nil, err
			}
			if _, err := invokeJSONHandler(ctx, http.MethodPost, map[string]string{"id": saved.ID}, link, s.createIssueLink); err != nil {
				return nil, err
			}
		}
	}
	if err := s.updateMCPIssueRelations(ctx, data, saved.ID, args); err != nil {
		return nil, err
	}
	if duplicate := stringArg(args, "duplicateOf"); duplicate != "" {
		related, err := mcpFindIssue(data, duplicate)
		if err != nil {
			return nil, err
		}
		if _, err := invokeJSONHandler(ctx, http.MethodPost, map[string]string{"id": saved.ID}, map[string]string{"type": "duplicate", "relatedIssueId": related.ID}, s.createRelation); err != nil {
			return nil, err
		}
	}
	if _, present := args["estimate"]; present {
		var estimate *float64
		if value, ok := args["estimate"].(float64); ok {
			estimate = &value
		}
		if err := s.store.MutateWorkspace(ctx, actor.WorkspaceKey, "issue.estimate_updated", saved.ID, args["estimate"], func(next *domain.Bootstrap) error {
			issue, err := issueByID(next, saved.ID)
			if err != nil {
				return err
			}
			issue.Estimate = estimate
			return nil
		}); err != nil {
			return nil, err
		}
	}
	if hasAnyArg(args, "addReleases", "removeReleases", "setReleases") {
		if err := s.updateMCPIssueReleases(ctx, data, saved.ID, args); err != nil {
			return nil, err
		}
	}
	updated, _, _ := s.store.BootstrapForUser(ctx, actor.WorkspaceKey, actor.User.ID)
	return mcpFindIssue(updated, saved.ID)
}

func (s *server) saveMCPProject(ctx context.Context, actor mcpActor, data domain.Bootstrap, args map[string]any) (any, error) {
	id := stringArg(args, "id")
	var current domain.Project
	var err error
	if id != "" {
		current, err = mcpFindProject(data, id)
		if err != nil {
			return nil, err
		}
	}
	description := current.Description
	if value, ok := args["description"].(string); ok {
		description = value
	}
	if patches, ok := args["patch"].([]any); ok {
		description, err = applyTextPatches(description, patches)
		if err != nil {
			return nil, err
		}
	}
	teamIDs, err := mergeResolvedIDs(current.TeamIDs, args, "addTeams", "removeTeams", "setTeams", func(value string) (string, error) { team, err := mcpFindTeam(data, value); return team.ID, err })
	if err != nil {
		return nil, err
	}
	initiativeIDs, err := mergeResolvedIDs(current.Initiatives, args, "addInitiatives", "removeInitiatives", "setInitiatives", func(value string) (string, error) { item, err := mcpFindInitiative(data, value); return item.ID, err })
	if err != nil {
		return nil, err
	}
	input := domain.ProjectMutationInput{TeamIDs: teamIDs, Initiatives: initiativeIDs}
	if value, ok := args["name"].(string); ok {
		input.Name = &value
	}
	if value, ok := args["summary"].(string); ok {
		input.Summary = &value
	}
	if _, ok := args["description"]; ok || args["patch"] != nil {
		input.Description = &description
	}
	if value, ok := args["icon"].(string); ok {
		input.Icon = &value
	}
	if value, ok := args["color"].(string); ok {
		input.Color = &value
	}
	if hasNumberArg(args, "priority") {
		value := intArg(args, "priority", 0)
		input.Priority = &value
	}
	if value, present := nullableStringArg(args, "startDate"); present {
		input.StartDate = &value
	}
	if value, ok := args["startDateResolution"].(string); ok {
		input.StartDateResolution = &value
	}
	if value, present := nullableStringArg(args, "targetDate"); present {
		input.TargetDate = &value
	}
	if value, ok := args["targetDateResolution"].(string); ok {
		input.TargetDateResolution = &value
	}
	if lead, present := nullableStringArg(args, "lead"); present {
		if lead == "" {
			input.LeadID = &lead
		} else {
			user, err := mcpFindUser(data, lead)
			if err != nil {
				return nil, err
			}
			input.LeadID = &user.ID
		}
	}
	if state := stringArg(args, "state"); state != "" {
		for _, status := range data.ProjectStatuses {
			if equalFoldAny(state, status.ID, status.Name, status.Type) {
				input.StatusID = &status.ID
				break
			}
		}
		if input.StatusID == nil {
			return nil, fmt.Errorf("project status not found")
		}
	}
	if labels, present := args["labels"]; present {
		_ = labels
		input.LabelIDs, err = resolveLabelIDs(data, stringsArg(args, "labels"), "project")
		if err != nil {
			return nil, err
		}
	}
	if id == "" && (input.Name == nil || strings.TrimSpace(*input.Name) == "") {
		return nil, fmt.Errorf("name is required when creating a project")
	}
	if id == "" && len(input.TeamIDs) == 0 {
		return nil, fmt.Errorf("at least one team is required when creating a project")
	}
	var result any
	if id == "" {
		result, err = invokeJSONHandler(ctx, http.MethodPost, nil, input, s.createProject)
	} else {
		result, err = invokeJSONHandler(ctx, http.MethodPatch, map[string]string{"id": current.ID}, input, s.updateProject)
	}
	if err != nil {
		return nil, err
	}
	var saved domain.Project
	if err := jsonClone(result, &saved); err != nil {
		return nil, err
	}
	if links, ok := args["links"].([]any); ok {
		for _, raw := range links {
			var link struct {
				Title string `json:"title"`
				URL   string `json:"url"`
			}
			if err := jsonClone(raw, &link); err != nil {
				return nil, err
			}
			resourceType := "link"
			input := domain.ProjectResourceMutationInput{Type: &resourceType, Title: &link.Title, URL: &link.URL}
			if _, err := invokeJSONHandler(ctx, http.MethodPost, map[string]string{"id": saved.ID}, input, s.createProjectResource); err != nil {
				return nil, err
			}
		}
	}
	updated, _, _ := s.store.BootstrapForUser(ctx, actor.WorkspaceKey, actor.User.ID)
	return mcpFindProject(updated, saved.ID)
}

func (s *server) saveMCPInitiative(ctx context.Context, actor mcpActor, data domain.Bootstrap, args map[string]any) (any, error) {
	id := stringArg(args, "id")
	var current domain.Initiative
	var err error
	if id != "" {
		current, err = mcpFindInitiative(data, id)
		if err != nil {
			return nil, err
		}
	}
	description := current.Description
	if value, ok := args["description"].(string); ok {
		description = value
	}
	if patches, ok := args["patch"].([]any); ok {
		description, err = applyTextPatches(description, patches)
		if err != nil {
			return nil, err
		}
	}
	input := domain.InitiativeMutationInput{}
	if value, ok := args["name"].(string); ok {
		input.Name = &value
	}
	if value, ok := args["summary"].(string); ok {
		input.Summary = &value
	}
	if _, ok := args["description"]; ok || args["patch"] != nil {
		input.Description = &description
	}
	if value, ok := args["icon"].(string); ok {
		input.Icon = &value
	}
	if value, ok := args["color"].(string); ok {
		input.Color = &value
	}
	if value, ok := args["status"].(string); ok {
		input.Status = &value
	}
	if hasNumberArg(args, "priority") {
		value := intArg(args, "priority", 0)
		input.Priority = &value
	}
	if value, present := nullableStringArg(args, "targetDate"); present {
		input.TargetDate = &value
	}
	if owner, present := nullableStringArg(args, "owner"); present {
		if owner == "" {
			input.OwnerID = &owner
		} else {
			user, err := mcpFindUser(data, owner)
			if err != nil {
				return nil, err
			}
			input.OwnerID = &user.ID
		}
	}
	if team, present := nullableStringArg(args, "leadTeam"); present {
		if team == "" {
			input.LeadTeamID = &team
		} else {
			found, err := mcpFindTeam(data, team)
			if err != nil {
				return nil, err
			}
			input.LeadTeamID = &found.ID
		}
	}
	if _, present := args["labels"]; present {
		ids, err := resolveLabelIDs(data, stringsArg(args, "labels"), "initiative")
		if err != nil {
			return nil, err
		}
		input.LabelIDs = &ids
	}
	if _, present := args["parentInitiatives"]; present {
		ids := []string{}
		for _, query := range stringsArg(args, "parentInitiatives") {
			parent, err := mcpFindInitiative(data, query)
			if err != nil {
				return nil, err
			}
			ids = append(ids, parent.ID)
		}
		input.ParentInitiativeIDs = &ids
	}
	if id == "" && (input.Name == nil || strings.TrimSpace(*input.Name) == "") {
		return nil, fmt.Errorf("name is required when creating an initiative")
	}
	var result any
	if id == "" {
		result, err = invokeJSONHandler(ctx, http.MethodPost, nil, input, s.createInitiative)
	} else {
		result, err = invokeJSONHandler(ctx, http.MethodPatch, map[string]string{"id": current.ID}, input, s.updateInitiative)
	}
	return result, err
}

func (s *server) saveMCPMilestone(ctx context.Context, _ mcpActor, data domain.Bootstrap, args map[string]any) (any, error) {
	project, err := mcpFindProject(data, stringArg(args, "project"))
	if err != nil {
		return nil, err
	}
	id := stringArg(args, "id")
	input := domain.ProjectMilestoneMutationInput{}
	if value, ok := args["name"].(string); ok {
		input.Name = &value
	}
	if value, ok := args["description"].(string); ok {
		input.Description = &value
	}
	if value, present := nullableStringArg(args, "targetDate"); present {
		input.TargetDate = &value
	}
	if id == "" {
		if input.Name == nil || strings.TrimSpace(*input.Name) == "" {
			return nil, fmt.Errorf("name is required when creating a milestone")
		}
		return invokeJSONHandler(ctx, http.MethodPost, map[string]string{"id": project.ID}, input, s.createProjectMilestone)
	}
	for _, item := range project.Milestones {
		if equalFoldAny(id, item.ID, item.Name) {
			return invokeJSONHandler(ctx, http.MethodPatch, map[string]string{"id": project.ID, "milestoneId": item.ID}, input, s.updateProjectMilestone)
		}
	}
	return nil, fmt.Errorf("milestone not found")
}

func (s *server) saveMCPRelease(ctx context.Context, actor mcpActor, data domain.Bootstrap, args map[string]any) (any, error) {
	id := stringArg(args, "id")
	var current domain.Release
	if id != "" {
		var found bool
		for _, item := range data.Releases {
			if equalFoldAny(id, item.ID, item.SlugID) {
				current = item
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("release not found")
		}
	}
	input := releaseInput{}
	if value, ok := args["name"].(string); ok {
		input.Name = &value
	}
	if value, ok := args["version"].(string); ok {
		input.Version = &value
	}
	if value, ok := args["description"].(string); ok {
		input.Description = &value
	}
	if value, ok := args["commitSha"].(string); ok {
		input.CommitSHA = &value
	}
	if value, present := nullableStringArg(args, "targetDate"); present {
		input.TargetDate = &value
	}
	if pipeline := stringArg(args, "pipeline"); pipeline != "" {
		for _, item := range data.ReleasePipelines {
			if equalFoldAny(pipeline, item.ID, item.SlugID, item.Name) {
				input.PipelineID = &item.ID
				break
			}
		}
		if input.PipelineID == nil {
			return nil, fmt.Errorf("release pipeline not found")
		}
	}
	if stage := stringArg(args, "stage"); stage != "" {
		pipelineID := current.PipelineID
		if input.PipelineID != nil {
			pipelineID = *input.PipelineID
		}
		var pipeline *domain.ReleasePipeline
		for index := range data.ReleasePipelines {
			if data.ReleasePipelines[index].ID == pipelineID {
				pipeline = &data.ReleasePipelines[index]
				break
			}
		}
		if pipeline == nil {
			return nil, fmt.Errorf("release pipeline is required when setting a stage")
		}
		if slices.ContainsFunc(pipeline.Stages, func(value string) bool { return strings.EqualFold(value, stage) }) {
			for _, value := range pipeline.Stages {
				if strings.EqualFold(value, stage) {
					input.Stage = &value
					status := pipeline.StageStatuses[value]
					if status != "" {
						input.Status = &status
					}
					break
				}
			}
		} else {
			wanted := map[string]string{"planned": "planned", "started": "inProgress", "completed": "released", "canceled": "canceled"}[stage]
			for _, value := range pipeline.Stages {
				if pipeline.StageStatuses[value] == wanted {
					input.Stage = &value
					status := wanted
					input.Status = &status
					break
				}
			}
			if input.Stage == nil {
				return nil, fmt.Errorf("release stage not found")
			}
		}
	}
	var result any
	var err error
	if id == "" {
		if input.Name == nil || input.PipelineID == nil {
			return nil, fmt.Errorf("name and pipeline are required when creating a release")
		}
		result, err = invokeJSONHandler(ctx, http.MethodPost, nil, input, s.createRelease)
	} else {
		result, err = invokeJSONHandler(ctx, http.MethodPatch, map[string]string{"id": current.ID}, input, s.updateRelease)
	}
	if err != nil {
		return nil, err
	}
	var saved domain.Release
	if err := jsonClone(result, &saved); err != nil {
		return nil, err
	}
	if hasAnyArg(args, "createdAt", "startDate", "startedAt", "completedAt") {
		err = s.store.MutateWorkspace(ctx, actor.WorkspaceKey, "release.timestamps_updated", saved.ID, args, func(next *domain.Bootstrap) error {
			index := slices.IndexFunc(next.Releases, func(item domain.Release) bool { return item.ID == saved.ID })
			if index < 0 {
				return errNotFound
			}
			release := &next.Releases[index]
			if value := stringArg(args, "createdAt"); value != "" {
				parsed, err := time.Parse(time.RFC3339, value)
				if err != nil {
					return errInvalid
				}
				release.CreatedAt = parsed.UTC()
			}
			if value, present := nullableStringArg(args, "startDate"); present {
				if value == "" {
					release.StartDate = nil
				} else if _, err := time.Parse("2006-01-02", value); err != nil {
					return errInvalid
				} else {
					release.StartDate = &value
				}
			}
			if value, present := nullableStringArg(args, "startedAt"); present {
				parsed, err := nullableRFC3339(value)
				if err != nil {
					return errInvalid
				}
				release.StartedAt = parsed
			}
			if value, present := nullableStringArg(args, "completedAt"); present {
				parsed, err := nullableRFC3339(value)
				if err != nil {
					return errInvalid
				}
				release.ReleasedAt = parsed
			}
			release.UpdatedAt = time.Now().UTC()
			saved = *release
			return nil
		})
	}
	return saved, err
}

func nullableRFC3339(value string) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, err
	}
	parsed = parsed.UTC()
	return &parsed, nil
}

func (s *server) saveMCPComment(ctx context.Context, actor mcpActor, data domain.Bootstrap, args map[string]any) (any, error) {
	body := stringArg(args, "body")
	if body == "" {
		return nil, fmt.Errorf("body is required")
	}
	id := stringArg(args, "id")
	parentID := stringArg(args, "parentId")
	if id != "" {
		return s.mutateAnyComment(ctx, actor, data, id, body, "update", "")
	}
	if parentID != "" {
		return s.mutateAnyComment(ctx, actor, data, parentID, body, "reply", "")
	}
	parents := []string{"issueId", "projectId", "initiativeId", "documentId", "milestoneId", "statusUpdateId"}
	parent := ""
	for _, key := range parents {
		if value := stringArg(args, key); value != "" {
			if parent != "" {
				return nil, fmt.Errorf("provide exactly one comment parent")
			}
			parent = value
		}
	}
	if parent == "" {
		return nil, fmt.Errorf("comment parent is required")
	}
	return s.mutateAnyComment(ctx, actor, data, parent, body, "create", "")
}

func (s *server) deleteMCPComment(ctx context.Context, actor mcpActor, id string) (any, error) {
	data, err := s.mcpWorkspaceData(ctx, actor)
	if err != nil {
		return nil, err
	}
	return s.mutateAnyComment(ctx, actor, data, id, "", "delete", "")
}

func (s *server) mutateAnyComment(ctx context.Context, actor mcpActor, data domain.Bootstrap, targetID, body, operation, _ string) (any, error) {
	var result domain.Comment
	err := s.store.MutateWorkspace(ctx, actor.WorkspaceKey, "comment."+operation, targetID, map[string]string{"body": body}, func(next *domain.Bootstrap) error {
		findIn := func(items *[]domain.Comment) (bool, error) {
			index := slices.IndexFunc(*items, func(item domain.Comment) bool { return item.ID == targetID })
			if index < 0 {
				return false, nil
			}
			switch operation {
			case "update":
				now := time.Now().UTC()
				(*items)[index].Body = body
				(*items)[index].EditedAt = &now
				(*items)[index].Version++
				result = (*items)[index]
			case "reply":
				parent := (*items)[index].ID
				result = newMCPComment(next.Viewer, body, &parent)
				*items = append(*items, result)
			case "delete":
				*items = slices.DeleteFunc(*items, func(item domain.Comment) bool {
					return item.ID == targetID || item.ParentID != nil && *item.ParentID == targetID
				})
			}
			return true, nil
		}
		for key, items := range next.Comments {
			copy := items
			if found, _ := findIn(&copy); found {
				next.Comments[key] = copy
				return nil
			}
		}
		for index := range next.Projects {
			if found, _ := findIn(&next.Projects[index].Comments); found {
				return nil
			}
		}
		for index := range next.Initiatives {
			if found, _ := findIn(&next.Initiatives[index].Comments); found {
				return nil
			}
		}
		for key, updates := range next.ProjectUpdates {
			for index := range updates {
				if found, _ := findIn(&updates[index].Comments); found {
					next.ProjectUpdates[key] = updates
					return nil
				}
			}
		}
		for key, updates := range next.InitiativeUpdates {
			for index := range updates {
				if found, _ := findIn(&updates[index].Comments); found {
					next.InitiativeUpdates[key] = updates
					return nil
				}
			}
		}
		if operation == "create" {
			if issue, err := issueByID(next, targetID); err == nil {
				result = newMCPComment(next.Viewer, body, nil)
				next.Comments[issue.ID] = append(next.Comments[issue.ID], result)
				return nil
			}
			if project, err := fullProjectByID(next, targetID); err == nil {
				result = newMCPComment(next.Viewer, body, nil)
				project.Comments = append(project.Comments, result)
				return nil
			}
			if initiative, err := initiativeByID(next, targetID); err == nil {
				result = newMCPComment(next.Viewer, body, nil)
				initiative.Comments = append(initiative.Comments, result)
				return nil
			}
			for _, document := range next.Documents {
				if equalFoldAny(targetID, document.ID, document.SlugID) {
					result = newMCPComment(next.Viewer, body, nil)
					next.Comments[document.ID] = append(next.Comments[document.ID], result)
					return nil
				}
			}
			for _, project := range next.Projects {
				for _, milestone := range project.Milestones {
					if milestone.ID == targetID {
						result = newMCPComment(next.Viewer, body, nil)
						next.Comments[targetID] = append(next.Comments[targetID], result)
						return nil
					}
				}
			}
			for key, updates := range next.ProjectUpdates {
				for index := range updates {
					if updates[index].ID == targetID {
						result = newMCPComment(next.Viewer, body, nil)
						updates[index].Comments = append(updates[index].Comments, result)
						next.ProjectUpdates[key] = updates
						return nil
					}
				}
			}
			for key, updates := range next.InitiativeUpdates {
				for index := range updates {
					if updates[index].ID == targetID {
						result = newMCPComment(next.Viewer, body, nil)
						updates[index].Comments = append(updates[index].Comments, result)
						next.InitiativeUpdates[key] = updates
						return nil
					}
				}
			}
		}
		return errNotFound
	})
	if err != nil {
		return nil, err
	}
	if operation == "delete" {
		return map[string]any{"deleted": true, "id": targetID}, nil
	}
	return result, nil
}

func newMCPComment(user domain.User, body string, parentID *string) domain.Comment {
	return domain.Comment{ID: fmt.Sprintf("comment_%d", time.Now().UnixNano()), Version: 1, Body: body, ParentID: parentID, Reactions: map[string][]string{}, CreatedAt: time.Now().UTC(), User: user}
}

func (s *server) updateMCPReview(ctx context.Context, _ mcpActor, data domain.Bootstrap, args map[string]any, action string) (any, error) {
	review, err := mcpFindReview(data, stringArg(args, "urlOrId"))
	if err != nil {
		return nil, err
	}
	if action == "merge" {
		status := "merged"
		return invokeJSONHandler(ctx, http.MethodPatch, map[string]string{"id": review.ID}, reviewInput{Status: &status}, s.updateReview)
	}
	decision := stringArg(args, "decision")
	mapped := map[string]string{"approved": "approve", "changesRequested": "requestChanges", "commented": "comment"}[decision]
	if mapped == "" {
		return nil, fmt.Errorf("invalid review decision")
	}
	return invokeJSONHandler(ctx, http.MethodPost, map[string]string{"id": review.ID}, map[string]string{"decision": mapped, "body": stringArg(args, "body")}, s.submitReview)
}

func (s *server) resolveMCPDiffThread(ctx context.Context, actor mcpActor, data domain.Bootstrap, args map[string]any) (any, error) {
	threadID := stringArg(args, "threadId")
	resolved := boolArg(args, "resolved")
	var updated domain.ReviewEvent
	err := s.store.MutateWorkspace(ctx, actor.WorkspaceKey, "review.thread_resolved", threadID, args, func(next *domain.Bootstrap) error {
		for reviewIndex := range next.Reviews {
			for eventIndex := range next.Reviews[reviewIndex].Events {
				if next.Reviews[reviewIndex].Events[eventIndex].ID == threadID {
					next.Reviews[reviewIndex].Events[eventIndex].Resolved = resolved
					updated = next.Reviews[reviewIndex].Events[eventIndex]
					return nil
				}
			}
		}
		return errNotFound
	})
	return updated, err
}

func (s *server) deleteMCPDiffComment(ctx context.Context, actor mcpActor, args map[string]any) (any, error) {
	id := stringArg(args, "commentId")
	if id == "" {
		id = stringArg(args, "draftId")
	}
	if id == "" {
		return nil, fmt.Errorf("commentId or draftId is required")
	}
	err := s.store.MutateWorkspace(ctx, actor.WorkspaceKey, "review.comment_deleted", id, nil, func(next *domain.Bootstrap) error {
		for index := range next.Reviews {
			before := len(next.Reviews[index].Events)
			next.Reviews[index].Events = slices.DeleteFunc(next.Reviews[index].Events, func(item domain.ReviewEvent) bool { return item.ID == id })
			if len(next.Reviews[index].Events) != before {
				return nil
			}
		}
		return errNotFound
	})
	return map[string]any{"deleted": err == nil, "id": id}, err
}

func (s *server) prepareMCPAttachmentUpload(actor mcpActor, data domain.Bootstrap, args map[string]any) (any, error) {
	issue, err := mcpFindIssue(data, stringArg(args, "issue"))
	if err != nil {
		return nil, err
	}
	size := int64(intArg(args, "size", 0))
	if size < 0 || size > 20<<20 {
		return nil, fmt.Errorf("Flow attachments must be 20 MB or smaller")
	}
	token, err := randomSecret("upload_")
	if err != nil {
		return nil, err
	}
	pending := &mcpPendingUpload{WorkspaceKey: actor.WorkspaceKey, UserID: actor.User.ID, IssueID: issue.ID, Filename: filepath.Base(stringArg(args, "filename")), ContentType: stringArg(args, "contentType"), Title: stringArg(args, "title"), Subtitle: stringArg(args, "subtitle"), ExpectedSize: size, ExpiresAt: time.Now().UTC().Add(15 * time.Minute)}
	s.mcpUploadMu.Lock()
	if s.mcpUploads == nil {
		s.mcpUploads = map[string]*mcpPendingUpload{}
	}
	s.mcpUploads[token] = pending
	s.mcpUploadMu.Unlock()
	assetURL := strings.TrimRight(stringArg(args, "__flowBaseURL"), "/") + "/api/mcp/uploads/" + token
	return map[string]any{"uploadUrl": assetURL, "assetUrl": assetURL, "headers": map[string]string{"Content-Type": pending.ContentType}, "expiresAt": pending.ExpiresAt}, nil
}

func (s *server) putMCPUpload(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	s.mcpUploadMu.Lock()
	pending := s.mcpUploads[token]
	s.mcpUploadMu.Unlock()
	if pending == nil || time.Now().UTC().After(pending.ExpiresAt) {
		writeError(w, http.StatusNotFound, "upload token is invalid or expired")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, (20<<20)+1)
	objectKey := fmt.Sprintf("mcp_%d_%s", time.Now().UnixNano(), pending.Filename)
	storage, err := s.storage()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage unavailable")
		return
	}
	size, err := storage.Put(r.Context(), objectKey, r.Body, pending.ContentType)
	if err != nil || size > 20<<20 || pending.ExpectedSize > 0 && size != pending.ExpectedSize {
		_ = storage.Delete(r.Context(), objectKey)
		writeError(w, http.StatusBadRequest, "uploaded size does not match the prepared upload")
		return
	}
	s.mcpUploadMu.Lock()
	pending.ObjectKey = objectKey
	pending.ActualSize = size
	pending.Completed = true
	s.mcpUploadMu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) finalizeMCPAttachmentUpload(ctx context.Context, actor mcpActor, data domain.Bootstrap, args map[string]any) (any, error) {
	assetURL := stringArg(args, "assetUrl")
	parsed, err := url.Parse(assetURL)
	if err != nil {
		return nil, fmt.Errorf("invalid assetUrl")
	}
	token := filepath.Base(parsed.Path)
	s.mcpUploadMu.Lock()
	pending := s.mcpUploads[token]
	if pending != nil && pending.Completed {
		delete(s.mcpUploads, token)
	}
	s.mcpUploadMu.Unlock()
	if pending == nil || !pending.Completed || pending.WorkspaceKey != actor.WorkspaceKey || pending.UserID != actor.User.ID {
		return nil, fmt.Errorf("assetUrl is not a completed Flow upload")
	}
	issue, err := mcpFindIssue(data, stringArg(args, "issue"))
	if err != nil || issue.ID != pending.IssueID {
		return nil, fmt.Errorf("upload was prepared for a different issue")
	}
	title := stringArg(args, "title")
	if title == "" {
		title = pending.Title
	}
	if title == "" {
		title = pending.Filename
	}
	return s.attachStoredObject(ctx, actor, issue.ID, title, pending.ContentType, pending.ObjectKey, pending.ActualSize)
}

func (s *server) createMCPAttachment(ctx context.Context, actor mcpActor, data domain.Bootstrap, args map[string]any) (any, error) {
	issue, err := mcpFindIssue(data, stringArg(args, "issue"))
	if err != nil {
		return nil, err
	}
	content, err := base64.StdEncoding.DecodeString(stringArg(args, "base64Content"))
	if err != nil {
		return nil, fmt.Errorf("base64Content is invalid")
	}
	digest := sha256.Sum256(content)
	if !strings.EqualFold(hex.EncodeToString(digest[:]), stringArg(args, "sha256")) {
		return nil, fmt.Errorf("sha256 checksum mismatch")
	}
	if expected := intArg(args, "size", 0); expected > 0 && expected != len(content) {
		return nil, fmt.Errorf("attachment size mismatch")
	}
	if len(content) > 20<<20 {
		return nil, fmt.Errorf("Flow attachments must be 20 MB or smaller")
	}
	key := fmt.Sprintf("mcp_%d_%s", time.Now().UnixNano(), filepath.Base(stringArg(args, "filename")))
	storage, err := s.storage()
	if err != nil {
		return nil, err
	}
	size, err := storage.Put(ctx, key, bytes.NewReader(content), stringArg(args, "contentType"))
	if err != nil {
		return nil, err
	}
	title := stringArg(args, "title")
	if title == "" {
		title = filepath.Base(stringArg(args, "filename"))
	}
	return s.attachStoredObject(ctx, actor, issue.ID, title, stringArg(args, "contentType"), key, size)
}

func (s *server) attachStoredObject(ctx context.Context, actor mcpActor, issueID, title, contentType, key string, size int64) (any, error) {
	attachment := domain.Attachment{ID: fmt.Sprintf("attachment_%d", time.Now().UnixNano()), IssueID: issueID, Title: title, URL: "/uploads/" + key, ContentType: contentType, Size: size, CreatedAt: time.Now().UTC(), Creator: actor.User}
	err := s.store.MutateWorkspace(ctx, actor.WorkspaceKey, "attachment.created", issueID, nil, func(data *domain.Bootstrap) error {
		issue, err := issueByID(data, issueID)
		if err != nil {
			return err
		}
		issue.Attachments = append(issue.Attachments, attachment)
		return nil
	})
	if err != nil {
		if storage, e := s.storage(); e == nil {
			_ = storage.Delete(ctx, key)
		}
	}
	return attachment, err
}

func (s *server) deleteMCPAttachment(ctx context.Context, actor mcpActor, data domain.Bootstrap, id string) (any, error) {
	for _, issue := range data.Issues {
		for _, attachment := range issue.Attachments {
			if attachment.ID == id {
				_, err := invokeJSONHandler(ctx, http.MethodDelete, map[string]string{"id": issue.ID, "attachmentId": id}, nil, s.deleteAttachment)
				if err != nil {
					return nil, err
				}
				return map[string]any{"deleted": true, "id": id}, nil
			}
		}
	}
	return nil, fmt.Errorf("attachment not found")
}

func invokeJSONHandler(ctx context.Context, method string, pathValues map[string]string, input any, handler http.HandlerFunc) (any, error) {
	var body io.Reader
	if input != nil {
		raw, err := json.Marshal(input)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(raw)
	}
	request := httptest.NewRequest(method, "http://flow.internal", body).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	for key, value := range pathValues {
		request.SetPathValue(key, value)
	}
	response := httptest.NewRecorder()
	handler(response, request)
	if response.Code >= 400 {
		var object map[string]any
		_ = json.Unmarshal(response.Body.Bytes(), &object)
		if message, ok := object["error"].(string); ok {
			return nil, fmt.Errorf("%s", message)
		}
		return nil, fmt.Errorf("Flow API returned HTTP %d", response.Code)
	}
	if response.Body.Len() == 0 {
		return map[string]any{"ok": true}, nil
	}
	var result any
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		return nil, err
	}
	return result, nil
}

func applyTextPatches(value string, patches []any) (string, error) {
	for _, raw := range patches {
		patch, ok := raw.(map[string]any)
		if !ok {
			return value, fmt.Errorf("invalid text patch")
		}
		op, _ := patch["op"].(string)
		switch op {
		case "replace":
			old, _ := patch["old_string"].(string)
			next, _ := patch["new_string"].(string)
			if old == "" || !strings.Contains(value, old) {
				return value, fmt.Errorf("patch text not found")
			}
			if replaceAll, _ := patch["replace_all"].(bool); replaceAll {
				value = strings.ReplaceAll(value, old, next)
			} else {
				value = strings.Replace(value, old, next, 1)
			}
		case "insert_before":
			anchor, _ := patch["anchor"].(string)
			text, _ := patch["text"].(string)
			index := strings.Index(value, anchor)
			if index < 0 {
				return value, fmt.Errorf("patch anchor not found")
			}
			value = value[:index] + text + value[index:]
		case "insert_after":
			anchor, _ := patch["anchor"].(string)
			text, _ := patch["text"].(string)
			index := strings.Index(value, anchor)
			if index < 0 {
				return value, fmt.Errorf("patch anchor not found")
			}
			index += len(anchor)
			value = value[:index] + text + value[index:]
		case "prepend":
			text, _ := patch["text"].(string)
			value = text + value
		case "append":
			text, _ := patch["text"].(string)
			value += text
		case "replace_range":
			from, _ := patch["from"].(string)
			to, _ := patch["to"].(string)
			next, _ := patch["new_string"].(string)
			start := strings.Index(value, from)
			if start < 0 {
				return value, fmt.Errorf("patch range start not found")
			}
			endOffset := strings.Index(value[start+len(from):], to)
			if endOffset < 0 {
				return value, fmt.Errorf("patch range end not found")
			}
			end := start + len(from) + endOffset + len(to)
			value = value[:start] + next + value[end:]
		default:
			return value, fmt.Errorf("unsupported text patch %q", op)
		}
	}
	return value, nil
}

func nullableStringArg(args map[string]any, key string) (string, bool) {
	value, present := args[key]
	if !present {
		return "", false
	}
	if value == nil {
		return "", true
	}
	text, ok := value.(string)
	return strings.TrimSpace(text), ok
}
func hasAnyArg(args map[string]any, keys ...string) bool {
	return slices.ContainsFunc(keys, func(key string) bool { _, ok := args[key]; return ok })
}

func resolveStateID(data domain.Bootstrap, teamID, query string) (string, error) {
	if query == "" {
		return "", nil
	}
	for _, item := range data.States {
		if (item.TeamID == "" || item.TeamID == teamID) && equalFoldAny(query, item.ID, item.Name, item.Type) {
			return item.ID, nil
		}
	}
	return "", fmt.Errorf("issue state not found")
}
func resolveNullableUserID(data domain.Bootstrap, args map[string]any, key string) (*string, error) {
	value, present := nullableStringArg(args, key)
	if !present {
		return nil, nil
	}
	if value == "" {
		return &value, nil
	}
	user, err := mcpFindUser(data, value)
	if err != nil {
		return nil, err
	}
	return &user.ID, nil
}
func resolveNullableProjectID(data domain.Bootstrap, args map[string]any, key string) (*string, error) {
	value, present := nullableStringArg(args, key)
	if !present {
		return nil, nil
	}
	if value == "" {
		return &value, nil
	}
	item, err := mcpFindProject(data, value)
	if err != nil {
		return nil, err
	}
	return &item.ID, nil
}
func resolveNullableIssueID(data domain.Bootstrap, args map[string]any, key string) (*string, error) {
	value, present := nullableStringArg(args, key)
	if !present {
		return nil, nil
	}
	if value == "" {
		return &value, nil
	}
	item, err := mcpFindIssue(data, value)
	if err != nil {
		return nil, err
	}
	return &item.ID, nil
}
func resolveNullableCycleID(data domain.Bootstrap, args map[string]any, key string) (*string, error) {
	value, present := nullableStringArg(args, key)
	if !present {
		return nil, nil
	}
	if value == "" {
		return &value, nil
	}
	for _, item := range data.Cycles {
		if equalFoldAny(value, item.ID, item.Name, fmt.Sprint(item.Number)) {
			return &item.ID, nil
		}
	}
	return nil, fmt.Errorf("cycle not found")
}
func resolveLabelIDs(data domain.Bootstrap, queries []string, resourceType string) ([]string, error) {
	ids := []string{}
	for _, query := range queries {
		found := ""
		for _, item := range data.Labels {
			itemType := item.ResourceType
			if itemType == "" {
				itemType = "issue"
			}
			if itemType == resourceType && equalFoldAny(query, item.ID, item.Name) {
				found = item.ID
				break
			}
		}
		if found == "" {
			return nil, fmt.Errorf("label %q not found", query)
		}
		ids = append(ids, found)
	}
	return normalizedStrings(ids), nil
}
func resolveMilestoneID(data domain.Bootstrap, projectID *string, query string) (string, error) {
	if projectID == nil || *projectID == "" {
		return "", fmt.Errorf("project is required when setting a milestone")
	}
	project, err := mcpFindProject(data, *projectID)
	if err != nil {
		return "", err
	}
	for _, item := range project.Milestones {
		if equalFoldAny(query, item.ID, item.Name) {
			return item.ID, nil
		}
	}
	return "", fmt.Errorf("milestone not found")
}

func mergeResolvedIDs(current []string, args map[string]any, addKey, removeKey, setKey string, resolve func(string) (string, error)) ([]string, error) {
	result := slices.Clone(current)
	if _, present := args[setKey]; present {
		result = []string{}
		for _, value := range stringsArg(args, setKey) {
			id, err := resolve(value)
			if err != nil {
				return nil, err
			}
			result = append(result, id)
		}
	}
	for _, value := range stringsArg(args, addKey) {
		id, err := resolve(value)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(result, id) {
			result = append(result, id)
		}
	}
	for _, value := range stringsArg(args, removeKey) {
		id, err := resolve(value)
		if err != nil {
			return nil, err
		}
		result = slices.DeleteFunc(result, func(item string) bool { return item == id })
	}
	return normalizedStrings(result), nil
}

func (s *server) updateMCPIssueRelations(ctx context.Context, data domain.Bootstrap, issueID string, args map[string]any) error {
	for key, relationType := range map[string]string{"blockedBy": "blocked_by", "blocks": "blocks", "relatedTo": "related"} {
		for _, query := range stringsArg(args, key) {
			related, err := mcpFindIssue(data, query)
			if err != nil {
				return err
			}
			if _, err := invokeJSONHandler(ctx, http.MethodPost, map[string]string{"id": issueID}, map[string]string{"type": relationType, "relatedIssueId": related.ID}, s.createRelation); err != nil {
				return err
			}
		}
	}
	for key, relationType := range map[string]string{"removeBlockedBy": "blocked_by", "removeBlocks": "blocks", "removeRelatedTo": "related"} {
		for _, query := range stringsArg(args, key) {
			related, err := mcpFindIssue(data, query)
			if err != nil {
				return err
			}
			issue, err := mcpFindIssue(data, issueID)
			if err != nil {
				return err
			}
			for _, relation := range issue.Relations {
				if relation.Type == relationType && relation.RelatedIssueID == related.ID {
					if _, err := invokeJSONHandler(ctx, http.MethodDelete, map[string]string{"id": issueID, "relationId": relation.ID}, nil, s.deleteRelation); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func (s *server) updateMCPIssueReleases(ctx context.Context, data domain.Bootstrap, issueID string, args map[string]any) error {
	ids := []string{}
	for _, release := range data.Releases {
		if slices.Contains(release.IssueIDs, issueID) {
			ids = append(ids, release.ID)
		}
	}
	resolve := func(query string) (string, error) {
		for _, item := range data.Releases {
			if equalFoldAny(query, item.ID, item.SlugID, item.Name, item.Version) {
				return item.ID, nil
			}
		}
		return "", fmt.Errorf("release %q not found", query)
	}
	var err error
	ids, err = mergeResolvedIDs(ids, args, "addReleases", "removeReleases", "setReleases", resolve)
	if err != nil {
		return err
	}
	_, err = invokeJSONHandler(ctx, http.MethodPut, map[string]string{"id": issueID}, issueReleasesInput{ReleaseIDs: ids}, s.setIssueReleases)
	return err
}
