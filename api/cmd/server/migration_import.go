package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

type linearMigrationClient struct {
	token    string
	endpoint string
	client   *http.Client
}
type linearGraphQLError struct {
	Message string `json:"message"`
}

func newLinearMigrationClient(token string) *linearMigrationClient {
	endpoint := strings.TrimSpace(os.Getenv("FLOW_LINEAR_API_URL"))
	if endpoint == "" {
		endpoint = "https://api.linear.app/graphql"
	}
	return &linearMigrationClient{token: token, endpoint: endpoint, client: &http.Client{Timeout: 30 * time.Second}}
}
func (client *linearMigrationClient) call(ctx context.Context, query string, variables any, result any) error {
	body, _ := json.Marshal(map[string]any{"query": query, "variables": variables})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", client.token)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	var payload struct {
		Data   json.RawMessage      `json:"data"`
		Errors []linearGraphQLError `json:"errors"`
	}
	if json.NewDecoder(response.Body).Decode(&payload) != nil {
		return errors.New("Linear returned an invalid GraphQL response")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || len(payload.Errors) > 0 {
		message := fmt.Sprintf("Linear GraphQL HTTP %d", response.StatusCode)
		if len(payload.Errors) > 0 {
			message = payload.Errors[0].Message
		}
		return errors.New(message)
	}
	return json.Unmarshal(payload.Data, result)
}

type linearTargetSnapshot struct {
	Viewer struct {
		ID string `json:"id"`
	} `json:"viewer"`
	Team struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Key     string `json:"key"`
		Members struct {
			Nodes []struct {
				ID    string `json:"id"`
				Name  string `json:"name"`
				Email string `json:"email"`
			} `json:"nodes"`
		} `json:"members"`
		States struct {
			Nodes []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
				Type string `json:"type"`
			} `json:"nodes"`
		} `json:"states"`
		Labels struct {
			Nodes []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"nodes"`
		} `json:"labels"`
		Projects struct {
			Nodes []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"nodes"`
		} `json:"projects"`
	} `json:"team"`
}

func (s *server) scanLinearMigrationTarget(w http.ResponseWriter, r *http.Request) {
	var input struct {
		APIToken     string `json:"apiToken"`
		TargetTeamID string `json:"targetTeamId"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.APIToken == "" || input.TargetTeamID == "" {
		writeError(w, http.StatusBadRequest, "Linear API token and targetTeamId are required")
		return
	}
	data := s.workspaceData(r)
	job, err := migrationByID(&data, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "migration not found")
		return
	}
	var bundle domain.MigrationBundle
	if json.Unmarshal(job.Bundle, &bundle) != nil {
		writeError(w, http.StatusBadRequest, "migration bundle unavailable")
		return
	}
	client := newLinearMigrationClient(input.APIToken)
	var snapshot linearTargetSnapshot
	query := `query MigrationTarget($teamId:String!){viewer{id} team(id:$teamId){id name key members{nodes{id name email}} states{nodes{id name type}} labels{nodes{id name}} projects{nodes{id name}}}}`
	if err := client.call(r.Context(), query, map[string]any{"teamId": input.TargetTeamID}, &snapshot); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	for _, source := range bundle.Users {
		mapping := findMigrationMapping(job, "user", source.ID)
		mapping.TargetID, mapping.TargetName = "", ""
		for _, target := range snapshot.Team.Members.Nodes {
			if source.Email != "" && strings.EqualFold(source.Email, target.Email) || strings.EqualFold(source.DisplayName, target.Name) {
				mapping.TargetID, mapping.TargetName, mapping.Action = target.ID, target.Name, "map"
				break
			}
		}
		if mapping.TargetID == "" {
			mapping.Action = "review"
		}
		mapping.Status = "pending"
		mapping.Error = ""
	}
	for _, source := range bundle.Projects {
		mapping := findMigrationMapping(job, "project", source.ID)
		mapping.TargetID, mapping.TargetName = "", ""
		mapping.Action = "create"
		for _, target := range snapshot.Team.Projects.Nodes {
			if strings.EqualFold(source.Name, target.Name) {
				mapping.TargetID, mapping.TargetName, mapping.Action = target.ID, target.Name, "map"
				break
			}
		}
		mapping.Status = "pending"
	}
	if len(bundle.Teams) > 0 {
		teamMapping := findMigrationMapping(job, "team", bundle.Teams[0].ID)
		teamMapping.TargetID, teamMapping.TargetName, teamMapping.Action, teamMapping.Status = snapshot.Team.ID, snapshot.Team.Name, "map", "pending"
	}
	job.Target, job.TargetTeamID, job.Status, job.Phase, job.UpdatedAt = "linear", input.TargetTeamID, "mapping", "review", time.Now().UTC()
	updated := *job
	_ = s.store.MutateWorkspace(r.Context(), workspaceKey(r), "migration.linear_scanned", job.ID, nil, func(next *domain.Bootstrap) error {
		current, findErr := migrationByID(next, job.ID)
		if findErr != nil {
			return findErr
		}
		bundleRaw := current.Bundle
		*current = updated
		current.Bundle = bundleRaw
		return nil
	})
	writeJSON(w, http.StatusOK, publicMigration(updated))
}

func (s *server) executeLinearMigration(w http.ResponseWriter, r *http.Request, job domain.MigrationJob, targetTeamID, token string) {
	if strings.TrimSpace(token) == "" || strings.TrimSpace(targetTeamID) == "" {
		writeError(w, http.StatusBadRequest, "Linear API token and targetTeamId are required")
		return
	}
	var bundle domain.MigrationBundle
	if json.Unmarshal(job.Bundle, &bundle) != nil {
		writeError(w, http.StatusBadRequest, "migration bundle is unavailable")
		return
	}
	client := newLinearMigrationClient(token)
	ctx := r.Context()
	var snapshot linearTargetSnapshot
	scanQuery := `query MigrationTarget($teamId:String!){viewer{id} team(id:$teamId){id name key members{nodes{id name email}} states{nodes{id name type}} labels{nodes{id name}} projects{nodes{id name}}}}`
	if err := client.call(ctx, scanQuery, map[string]any{"teamId": targetTeamID}, &snapshot); err != nil {
		s.failLinearMigration(r, job.ID, err)
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	linearMappingsPrepared := job.Target == "linear"
	job.Target = "linear"
	job.TargetTeamID = targetTeamID
	job.Status = "running"
	job.Phase = "users"
	job.Progress = 5
	job.Errors = []string{}
	userMap := map[string]string{}
	for _, source := range bundle.Users {
		targetID := ""
		for _, target := range snapshot.Team.Members.Nodes {
			if source.Email != "" && strings.EqualFold(source.Email, target.Email) || strings.EqualFold(source.DisplayName, target.Name) {
				targetID = target.ID
				break
			}
		}
		mapping := findMigrationMapping(&job, "user", source.ID)
		if linearMappingsPrepared && mapping != nil && mapping.TargetID != "" {
			targetID = mapping.TargetID
		}
		if targetID == "" && mapping != nil && mapping.Action == "invite" && source.Email != "" {
			inviteID, err := linearCreateEntity(ctx, client, "organizationInviteCreate", "OrganizationInviteCreateInput", map[string]any{"email": source.Email, "role": "user", "teamIds": []string{targetTeamID}}, "organizationInvite")
			if err != nil {
				mapping.Status = "failed"
				mapping.Error = err.Error()
				job.Errors = append(job.Errors, "invite "+source.Email+": "+err.Error())
			} else {
				mapping.Status = "invited"
				mapping.TargetID = inviteID
			}
			continue
		}
		if targetID == "" {
			if mapping != nil && mapping.Action == "skip" {
				mapping.Status = "skipped"
				continue
			}
			job.Errors = append(job.Errors, "unmapped user: "+source.DisplayName)
			continue
		}
		userMap[source.ID] = targetID
		mapping.Action = "map"
		completeLinearMapping(&job, "user", source.ID, targetID, source.DisplayName)
	}
	if slices.ContainsFunc(job.Mappings, func(item domain.MigrationEntityMapping) bool {
		return item.EntityType == "user" && item.Action == "review"
	}) {
		s.failLinearMigrationJob(r, job, "user mappings require review")
		writeJSON(w, http.StatusConflict, publicMigration(job))
		return
	}
	stateMap := map[string]string{}
	for _, source := range bundle.States {
		targetID := ""
		for _, target := range snapshot.Team.States.Nodes {
			if strings.EqualFold(target.Name, source.Name) {
				targetID = target.ID
				break
			}
		}
		existing := targetID != ""
		if targetID == "" {
			stateType := source.Type
			if stateType == "duplicate" {
				stateType = "canceled"
			}
			input := map[string]any{"name": source.Name, "teamId": targetTeamID, "color": source.Color, "type": stateType}
			id, err := linearCreateEntity(ctx, client, "workflowStateCreate", "WorkflowStateCreateInput", input, "workflowState")
			if err != nil {
				job.Errors = append(job.Errors, "state "+source.Name+": "+err.Error())
				continue
			}
			targetID = id
		}
		stateMap[source.ID] = targetID
		mapping := findMigrationMapping(&job, "state", source.ID)
		if existing {
			mapping.Action = "map"
		} else {
			mapping.Action = "create"
		}
		completeLinearMapping(&job, "state", source.ID, targetID, source.Name)
	}
	groupMap := map[string]string{}
	for _, source := range bundle.LabelGroups {
		input := map[string]any{"name": source.Name, "color": source.Color, "description": source.Description, "teamId": targetTeamID, "isGroup": true}
		id, err := linearCreateEntity(ctx, client, "issueLabelCreate", "IssueLabelCreateInput", input, "issueLabel")
		if err != nil {
			job.Errors = append(job.Errors, "label group "+source.Name+": "+err.Error())
			continue
		}
		groupMap[source.ID] = id
		completeLinearMapping(&job, "labelGroup", source.ID, id, source.Name)
	}
	labelMap := map[string]string{}
	for _, source := range bundle.Labels {
		targetID := ""
		for _, target := range snapshot.Team.Labels.Nodes {
			if strings.EqualFold(target.Name, source.Name) {
				targetID = target.ID
				break
			}
		}
		existingTarget := targetID != ""
		if targetID == "" {
			input := map[string]any{"name": source.Name, "color": source.Color, "description": source.Description, "teamId": targetTeamID}
			if parent := groupMap[source.GroupID]; parent != "" {
				input["parentId"] = parent
			}
			id, err := linearCreateEntity(ctx, client, "issueLabelCreate", "IssueLabelCreateInput", input, "issueLabel")
			if err != nil {
				job.Errors = append(job.Errors, "label "+source.Name+": "+err.Error())
				continue
			}
			targetID = id
		}
		labelMap[source.ID] = targetID
		mapping := findMigrationMapping(&job, "label", source.ID)
		if existingTarget {
			mapping.Action = "map"
		} else {
			mapping.Action = "create"
		}
		completeLinearMapping(&job, "label", source.ID, targetID, source.Name)
	}
	job.Phase = "planning"
	job.Progress = 20
	projectMap := map[string]string{}
	for _, source := range bundle.Projects {
		targetID := ""
		for _, target := range snapshot.Team.Projects.Nodes {
			if strings.EqualFold(target.Name, source.Name) {
				targetID = target.ID
				break
			}
		}
		existingTarget := targetID != ""
		if targetID == "" {
			input := map[string]any{"name": source.Name, "description": source.Summary, "content": source.Description, "teamIds": []string{targetTeamID}, "priority": source.Priority}
			id, err := linearCreateEntity(ctx, client, "projectCreate", "ProjectCreateInput", input, "project")
			if err != nil {
				job.Errors = append(job.Errors, "project "+source.Name+": "+err.Error())
				continue
			}
			targetID = id
		}
		projectMap[source.ID] = targetID
		mapping := findMigrationMapping(&job, "project", source.ID)
		if existingTarget {
			mapping.Action = "map"
		} else {
			mapping.Action = "create"
		}
		completeLinearMapping(&job, "project", source.ID, targetID, source.Name)
		for index, milestone := range source.Milestones {
			input := map[string]any{"name": milestone.Name, "description": milestone.Description, "projectId": targetID, "sortOrder": index}
			if milestone.TargetDate != nil {
				input["targetDate"] = *milestone.TargetDate
			}
			id, err := linearCreateEntity(ctx, client, "projectMilestoneCreate", "ProjectMilestoneCreateInput", input, "projectMilestone")
			if err != nil {
				job.Errors = append(job.Errors, "milestone "+milestone.Name+": "+err.Error())
			} else {
				completeLinearMapping(&job, "milestone", milestone.ID, id, milestone.Name)
			}
		}
	}
	for sourceID, updates := range bundle.ProjectUpdates {
		targetID := projectMap[sourceID]
		if targetID == "" {
			continue
		}
		for _, update := range updates {
			body := fmt.Sprintf("_Original update from %s_\n\n%s", update.CreatedAt.UTC().Format(time.RFC3339Nano), update.Body)
			input := map[string]any{"projectId": targetID, "body": body}
			if slices.Contains([]string{"onTrack", "atRisk", "offTrack"}, update.Health) {
				input["health"] = update.Health
			}
			id, err := linearCreateEntity(ctx, client, "projectUpdateCreate", "ProjectUpdateCreateInput", input, "projectUpdate")
			if err != nil {
				job.Errors = append(job.Errors, "project update: "+err.Error())
				continue
			}
			completeLinearMapping(&job, "projectUpdate", update.ID, id, update.Body)
			for _, comment := range update.Comments {
				commentInput := map[string]any{"projectUpdateId": id, "body": fmt.Sprintf("**%s** · original comment from %s\n\n%s", comment.User.DisplayName, comment.CreatedAt.UTC().Format(time.RFC3339Nano), comment.Body), "doNotSubscribeToIssue": true}
				if commentID, commentErr := linearCreateEntity(ctx, client, "commentCreate", "CommentCreateInput", commentInput, "comment"); commentErr == nil {
					completeLinearMapping(&job, "comment", comment.ID, commentID, comment.Body)
				} else {
					job.Errors = append(job.Errors, "project update comment: "+commentErr.Error())
				}
			}
		}
	}
	cycleMap := map[string]string{}
	for _, source := range bundle.Cycles {
		input := map[string]any{"teamId": targetTeamID, "name": source.Name, "description": source.Description, "startsAt": source.StartsAt, "endsAt": source.EndsAt}
		if source.Status == "completed" {
			input["completedAt"] = source.EndsAt
		}
		id, err := linearCreateEntity(ctx, client, "cycleCreate", "CycleCreateInput", input, "cycle")
		if err != nil {
			job.Errors = append(job.Errors, "cycle "+source.Name+": "+err.Error())
			continue
		}
		cycleMap[source.ID] = id
		completeLinearMapping(&job, "cycle", source.ID, id, source.Name)
	}
	initiativeMap := map[string]string{}
	for _, source := range bundle.Initiatives {
		input := map[string]any{"name": source.Name, "description": source.Summary, "content": source.Description, "color": source.Color, "priority": source.Priority}
		if source.TargetDate != nil {
			input["targetDate"] = *source.TargetDate
		}
		id, err := linearCreateEntity(ctx, client, "initiativeCreate", "InitiativeCreateInput", input, "initiative")
		if err != nil {
			job.Errors = append(job.Errors, "initiative "+source.Name+": "+err.Error())
			continue
		}
		initiativeMap[source.ID] = id
		completeLinearMapping(&job, "initiative", source.ID, id, source.Name)
		for _, projectID := range source.ProjectIDs {
			if targetProject := projectMap[projectID]; targetProject != "" {
				_, linkErr := linearCreateEntity(ctx, client, "initiativeToProjectCreate", "InitiativeToProjectCreateInput", map[string]any{"initiativeId": id, "projectId": targetProject}, "initiativeToProject")
				if linkErr != nil {
					job.Errors = append(job.Errors, "initiative project link: "+linkErr.Error())
				}
			}
		}
	}
	for sourceID, updates := range bundle.InitiativeUpdates {
		targetID := initiativeMap[sourceID]
		if targetID == "" {
			continue
		}
		for _, update := range updates {
			body := fmt.Sprintf("_Original update from %s_\n\n%s", update.CreatedAt.UTC().Format(time.RFC3339Nano), update.Body)
			input := map[string]any{"initiativeId": targetID, "body": body}
			if slices.Contains([]string{"onTrack", "atRisk", "offTrack"}, update.Health) {
				input["health"] = update.Health
			}
			id, err := linearCreateEntity(ctx, client, "initiativeUpdateCreate", "InitiativeUpdateCreateInput", input, "initiativeUpdate")
			if err != nil {
				job.Errors = append(job.Errors, "initiative update: "+err.Error())
				continue
			}
			completeLinearMapping(&job, "initiativeUpdate", update.ID, id, update.Body)
			for _, comment := range update.Comments {
				commentInput := map[string]any{"initiativeUpdateId": id, "body": fmt.Sprintf("**%s** · original comment from %s\n\n%s", comment.User.DisplayName, comment.CreatedAt.UTC().Format(time.RFC3339Nano), comment.Body), "doNotSubscribeToIssue": true}
				if commentID, commentErr := linearCreateEntity(ctx, client, "commentCreate", "CommentCreateInput", commentInput, "comment"); commentErr == nil {
					completeLinearMapping(&job, "comment", comment.ID, commentID, comment.Body)
				} else {
					job.Errors = append(job.Errors, "initiative update comment: "+commentErr.Error())
				}
			}
		}
	}
	customerMap := map[string]string{}
	for _, source := range bundle.Customers {
		input := map[string]any{"name": source.Name, "domains": source.Domains, "externalIds": []string{"flow:" + source.ID}}
		if source.LogoURL != "" {
			input["logoUrl"] = source.LogoURL
		}
		if source.AnnualRevenue > 0 {
			input["revenue"] = source.AnnualRevenue
		}
		if source.Size > 0 {
			input["size"] = source.Size
		}
		id, err := linearCreateEntity(ctx, client, "customerCreate", "CustomerCreateInput", input, "customer")
		if err != nil {
			job.Errors = append(job.Errors, "customer "+source.Name+": "+err.Error())
			continue
		}
		customerMap[source.ID] = id
		completeLinearMapping(&job, "customer", source.ID, id, source.Name)
	}
	pipelineMap := map[string]string{}
	for _, source := range bundle.ReleasePipelines {
		pipelineType := source.Type
		if pipelineType != "continuous" && pipelineType != "scheduled" {
			pipelineType = "scheduled"
		}
		input := map[string]any{"name": source.Name, "teamIds": []string{targetTeamID}, "type": pipelineType}
		id, err := linearCreateEntity(ctx, client, "releasePipelineCreate", "ReleasePipelineCreateInput", input, "releasePipeline")
		if err != nil {
			job.Errors = append(job.Errors, "release pipeline "+source.Name+": "+err.Error())
			continue
		}
		pipelineMap[source.ID] = id
		completeLinearMapping(&job, "releasePipeline", source.ID, id, source.Name)
	}
	releaseMap := map[string]string{}
	for _, source := range bundle.Releases {
		pipelineID := pipelineMap[source.PipelineID]
		if pipelineID == "" {
			job.Errors = append(job.Errors, "release "+source.Name+": no target pipeline")
			continue
		}
		input := map[string]any{"name": source.Name, "pipelineId": pipelineID, "description": source.Description, "createdAt": source.CreatedAt}
		if source.Version != "" {
			input["version"] = source.Version
		}
		if source.TargetDate != nil {
			input["targetDate"] = *source.TargetDate
		}
		id, err := linearCreateEntity(ctx, client, "releaseCreate", "ReleaseCreateInput", input, "release")
		if err != nil {
			job.Errors = append(job.Errors, "release "+source.Name+": "+err.Error())
			continue
		}
		releaseMap[source.ID] = id
		completeLinearMapping(&job, "release", source.ID, id, source.Name)
	}
	job.Phase = "issues"
	job.Progress = 45
	issueMap := map[string]string{}
	identifierMap := map[string]string{}
	milestoneMap := mappingMap(job.Mappings, "milestone")
	for _, source := range bundle.Issues {
		input := map[string]any{"teamId": targetTeamID, "title": source.Title, "description": linearMigrationDescription(source, job.ID), "priority": source.Priority, "createdAt": source.CreatedAt}
		if source.CompletedAt != nil {
			input["completedAt"] = source.CompletedAt
		}
		if source.DueDate != nil {
			input["dueDate"] = *source.DueDate
		}
		if source.Estimate != nil {
			input["estimate"] = int(*source.Estimate)
		}
		if source.Assignee != nil {
			if id := userMap[source.Assignee.ID]; id != "" {
				input["assigneeId"] = id
			}
		}
		if id := stateMap[source.State.ID]; id != "" {
			input["stateId"] = id
		}
		if source.Project != nil {
			if id := projectMap[source.Project.ID]; id != "" {
				input["projectId"] = id
			}
		}
		if source.ProjectMilestoneID != nil {
			if id := milestoneMap[*source.ProjectMilestoneID]; id != "" {
				input["projectMilestoneId"] = id
			}
		}
		if source.CycleID != nil {
			if id := cycleMap[*source.CycleID]; id != "" {
				input["cycleId"] = id
			}
		}
		releaseIDs := []string{}
		for _, release := range bundle.Releases {
			if slices.Contains(release.IssueIDs, source.ID) {
				if id := releaseMap[release.ID]; id != "" {
					releaseIDs = append(releaseIDs, id)
				}
			}
		}
		if len(releaseIDs) > 0 {
			input["releaseIds"] = releaseIDs
		}
		labels := mapIDs(sourceLabelIDs(source), labelMap, "")
		if len(labels) > 0 {
			input["labelIds"] = labels
		}
		subscriberIDs := mapIDs(source.SubscriberIDs, userMap, "")
		if len(subscriberIDs) > 0 {
			input["subscriberIds"] = subscriberIDs
		}
		if uuidV4Pattern.MatchString(source.ID) {
			input["id"] = source.ID
		}
		id, identifier, err := linearCreateIssue(ctx, client, input)
		if err != nil {
			job.Errors = append(job.Errors, "issue "+source.Identifier+": "+err.Error())
			continue
		}
		issueMap[source.ID] = id
		identifierMap[source.ID] = identifier
		completeLinearMapping(&job, "issue", source.ID, id, identifier)
		if source.ArchivedAt != nil {
			if archiveErr := linearArchiveIssue(ctx, client, id); archiveErr != nil {
				job.Errors = append(job.Errors, "archive "+source.Identifier+": "+archiveErr.Error())
			}
		}
	}
	job.Phase = "relations"
	job.Progress = 70
	for _, source := range bundle.Issues {
		targetID := issueMap[source.ID]
		if targetID == "" {
			continue
		}
		if source.ParentID != nil {
			if parent := issueMap[*source.ParentID]; parent != "" {
				if err := linearUpdateIssue(ctx, client, targetID, map[string]any{"parentId": parent}); err != nil {
					job.Errors = append(job.Errors, "parent "+source.Identifier+": "+err.Error())
				}
			}
		}
		for _, relation := range source.Relations {
			if related := issueMap[relation.RelatedIssueID]; related != "" {
				if relation.Type == "parent_of" || relation.Type == "sub_issue_of" {
					continue
				}
				issueID, relatedID, relationType := targetID, related, linearRelationType(relation.Type)
				if relation.Type == "blocked_by" {
					issueID, relatedID = related, targetID
				}
				input := map[string]any{"issueId": issueID, "relatedIssueId": relatedID, "type": relationType}
				id, err := linearCreateEntity(ctx, client, "issueRelationCreate", "IssueRelationCreateInput", input, "issueRelation")
				if err != nil {
					job.Errors = append(job.Errors, "relation "+source.Identifier+": "+err.Error())
				} else {
					completeLinearMapping(&job, "relation", relation.ID, id, relation.Type)
				}
			}
		}
		for _, comment := range bundle.Comments[source.ID] {
			body := fmt.Sprintf("**%s** · original comment from %s\n\n%s", comment.User.DisplayName, comment.CreatedAt.UTC().Format(time.RFC3339Nano), comment.Body)
			input := map[string]any{"issueId": targetID, "body": body, "createdAt": comment.CreatedAt, "doNotSubscribeToIssue": true}
			if comment.ParentID != nil {
				if parent := mappingMap(job.Mappings, "comment")[*comment.ParentID]; parent != "" {
					input["parentId"] = parent
				}
			}
			id, err := linearCreateEntity(ctx, client, "commentCreate", "CommentCreateInput", input, "comment")
			if err != nil {
				job.Errors = append(job.Errors, "comment "+source.Identifier+": "+err.Error())
			} else {
				completeLinearMapping(&job, "comment", comment.ID, id, comment.Body)
			}
		}
		for _, attachment := range source.Attachments {
			if attachment.URL == "" {
				continue
			}
			attachmentURL := attachment.URL
			if strings.HasPrefix(attachment.URL, "http://") || strings.HasPrefix(attachment.URL, "https://") {
				if copied, copyErr := linearCopyAttachment(r.Context(), client, attachment); copyErr == nil {
					attachmentURL = copied
				} else {
					job.Errors = append(job.Errors, "attachment copy "+attachment.Title+": "+copyErr.Error())
				}
			}
			input := map[string]any{"issueId": targetID, "title": attachment.Title, "url": attachmentURL, "subtitle": "Imported from Flow"}
			id, err := linearCreateEntity(ctx, client, "attachmentCreate", "AttachmentCreateInput", input, "attachment")
			if err != nil {
				job.Errors = append(job.Errors, "attachment "+source.Identifier+": "+err.Error())
			} else {
				completeLinearMapping(&job, "attachment", attachment.ID, id, attachment.Title)
			}
		}
		metadata := linearMigrationMetadata(source, bundle, identifierMap, job.ID)
		if metadata != "" {
			input := map[string]any{"issueId": targetID, "body": metadata, "doNotSubscribeToIssue": true}
			if _, err := linearCreateEntity(ctx, client, "commentCreate", "CommentCreateInput", input, "comment"); err != nil {
				job.Errors = append(job.Errors, "metadata "+source.Identifier+": "+err.Error())
			}
		}
	}
	for _, request := range bundle.CustomerRequests {
		customerID := customerMap[request.CustomerID]
		issueID := issueMap[request.IssueID]
		projectID := projectMap[request.ProjectID]
		if customerID == "" || (issueID == "" && projectID == "") {
			continue
		}
		input := map[string]any{"customerId": customerID, "body": request.Body, "createdAt": request.CreatedAt}
		if issueID != "" {
			input["issueId"] = issueID
		} else {
			input["projectId"] = projectID
		}
		id, err := linearCreateEntity(ctx, client, "customerNeedCreate", "CustomerNeedCreateInput", input, "customerNeed")
		if err != nil {
			job.Errors = append(job.Errors, "customer request: "+err.Error())
		} else {
			completeLinearMapping(&job, "customerRequest", request.ID, id, request.Body)
		}
	}
	now := time.Now().UTC()
	job.Status, job.Phase, job.Progress, job.CompletedAt, job.UpdatedAt = "completed", "complete", 100, &now, now
	s.persistLinearMigration(r, job)
	writeJSON(w, http.StatusOK, publicMigration(job))
}

var uuidV4Pattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func sourceLabelIDs(issue domain.Issue) []string {
	result := make([]string, 0, len(issue.Labels))
	for _, label := range issue.Labels {
		result = append(result, label.ID)
	}
	return result
}
func mappingMap(mappings []domain.MigrationEntityMapping, kind string) map[string]string {
	result := map[string]string{}
	for _, item := range mappings {
		if item.EntityType == kind && item.TargetID != "" {
			result[item.SourceID] = item.TargetID
		}
	}
	return result
}
func findMigrationMapping(job *domain.MigrationJob, kind, sourceID string) *domain.MigrationEntityMapping {
	index := slices.IndexFunc(job.Mappings, func(item domain.MigrationEntityMapping) bool {
		return item.EntityType == kind && item.SourceID == sourceID
	})
	if index < 0 {
		job.Mappings = append(job.Mappings, migrationMapping(kind, sourceID, sourceID, "", "", "review"))
		index = len(job.Mappings) - 1
	}
	return &job.Mappings[index]
}
func completeLinearMapping(job *domain.MigrationJob, kind, sourceID, targetID, targetName string) {
	mapping := findMigrationMapping(job, kind, sourceID)
	if mapping.Action == "review" {
		mapping.Action = "create"
	}
	mapping.TargetID = targetID
	mapping.TargetName = targetName
	mapping.Status = "completed"
}
func linearRelationType(value string) string {
	switch value {
	case "blocked_by":
		return "blocks"
	case "sub_issue_of", "parent_of":
		return "related"
	default:
		return value
	}
}
func linearMigrationDescription(issue domain.Issue, jobID string) string {
	description := issue.Description
	if len(issue.Attachments) > 0 {
		description += "\n\n## Imported attachments\n"
		for _, item := range issue.Attachments {
			if item.URL != "" {
				description += fmt.Sprintf("- [%s](%s)\n", item.Title, item.URL)
			}
		}
	}
	return description
}
func linearMigrationMetadata(issue domain.Issue, bundle domain.MigrationBundle, identifiers map[string]string, jobID string) string {
	parts := []string{"### Import metadata", fmt.Sprintf("- Flow source ID: `%s`", issue.ID), fmt.Sprintf("- Flow identifier: `%s`", issue.Identifier), fmt.Sprintf("- Migration job: `%s`", jobID), fmt.Sprintf("- Original updatedAt: `%s`", issue.UpdatedAt.UTC().Format(time.RFC3339Nano))}
	parts = append(parts, "- Original creator: "+issue.Creator.DisplayName)
	if issue.Assignee != nil {
		parts = append(parts, "- Original assignee: "+issue.Assignee.DisplayName)
	}
	if issue.StartedAt != nil {
		parts = append(parts, fmt.Sprintf("- Original startedAt: `%s`", issue.StartedAt.UTC().Format(time.RFC3339Nano)))
	}
	if issue.TriagedAt != nil {
		parts = append(parts, fmt.Sprintf("- Original triagedAt: `%s`", issue.TriagedAt.UTC().Format(time.RFC3339Nano)))
	}
	if issue.CanceledAt != nil {
		parts = append(parts, fmt.Sprintf("- Original canceledAt: `%s`", issue.CanceledAt.UTC().Format(time.RFC3339Nano)))
	}
	if issue.ArchivedAt != nil {
		parts = append(parts, fmt.Sprintf("- Original archivedAt: `%s`", issue.ArchivedAt.UTC().Format(time.RFC3339Nano)))
	}
	if issue.SLABreachesAt != nil {
		parts = append(parts, fmt.Sprintf("- Original SLA breach time: `%s`", issue.SLABreachesAt.UTC().Format(time.RFC3339Nano)))
	}
	for _, sla := range bundle.IssueSLAs {
		if sla.IssueID == issue.ID {
			ruleName := sla.RuleID
			for _, rule := range bundle.SLARules {
				if rule.ID == sla.RuleID {
					ruleName = rule.Name
					break
				}
			}
			parts = append(parts, fmt.Sprintf("- Flow SLA: `%s` status `%s`, due `%s`", ruleName, sla.Status, sla.DueAt.UTC().Format(time.RFC3339Nano)))
		}
	}
	for _, release := range bundle.Releases {
		if slices.Contains(release.IssueIDs, issue.ID) {
			parts = append(parts, "- Flow release: "+release.Name)
		}
	}
	return strings.Join(parts, "\n")
}

func linearCreateEntity(ctx context.Context, client *linearMigrationClient, mutation, inputType string, input map[string]any, field string) (string, error) {
	query := fmt.Sprintf("mutation Create($input:%s!){%s(input:$input){success %s{id}}}", inputType, mutation, field)
	var result map[string]struct {
		Success bool `json:"success"`
	}
	var raw map[string]any
	if err := client.call(ctx, query, map[string]any{"input": input}, &raw); err != nil {
		return "", err
	}
	payload, ok := raw[mutation].(map[string]any)
	if !ok {
		return "", errors.New("Linear mutation payload missing")
	}
	entity, ok := payload[field].(map[string]any)
	if !ok {
		return "", errors.New("Linear mutation entity missing")
	}
	id, _ := entity["id"].(string)
	if id == "" {
		return "", errors.New("Linear mutation returned no id")
	}
	_ = result
	return id, nil
}
func linearCreateIssue(ctx context.Context, client *linearMigrationClient, input map[string]any) (string, string, error) {
	query := `mutation CreateIssue($input:IssueCreateInput!){issueCreate(input:$input){success issue{id identifier}}}`
	var result struct {
		IssueCreate struct {
			Issue struct {
				ID         string `json:"id"`
				Identifier string `json:"identifier"`
			} `json:"issue"`
		} `json:"issueCreate"`
	}
	if err := client.call(ctx, query, map[string]any{"input": input}, &result); err != nil {
		return "", "", err
	}
	return result.IssueCreate.Issue.ID, result.IssueCreate.Issue.Identifier, nil
}
func linearUpdateIssue(ctx context.Context, client *linearMigrationClient, id string, input map[string]any) error {
	query := `mutation UpdateIssue($id:String!,$input:IssueUpdateInput!){issueUpdate(id:$id,input:$input){success}}`
	return client.call(ctx, query, map[string]any{"id": id, "input": input}, &map[string]any{})
}
func linearArchiveIssue(ctx context.Context, client *linearMigrationClient, id string) error {
	query := `mutation ArchiveIssue($id:String!){issueArchive(id:$id){success}}`
	return client.call(ctx, query, map[string]any{"id": id}, &map[string]any{})
}

func linearCopyAttachment(ctx context.Context, client *linearMigrationClient, attachment domain.Attachment) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, attachment.URL, nil)
	if err != nil {
		return "", err
	}
	response, err := client.client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("source returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 50<<20))
	if err != nil {
		return "", err
	}
	contentType := attachment.ContentType
	if contentType == "" {
		contentType = response.Header.Get("Content-Type")
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	filename := attachment.Title
	if filename == "" {
		filename = "attachment"
	}
	query := `mutation Upload($filename:String!,$contentType:String!,$size:Int!){fileUpload(filename:$filename,contentType:$contentType,size:$size,makePublic:false){success uploadFile{uploadUrl assetUrl headers{key value}}}}`
	var result struct {
		FileUpload struct {
			UploadFile struct {
				UploadURL string `json:"uploadUrl"`
				AssetURL  string `json:"assetUrl"`
				Headers   []struct {
					Key   string `json:"key"`
					Value string `json:"value"`
				} `json:"headers"`
			} `json:"uploadFile"`
		} `json:"fileUpload"`
	}
	if err := client.call(ctx, query, map[string]any{"filename": filename, "contentType": contentType, "size": len(data)}, &result); err != nil {
		return "", err
	}
	if result.FileUpload.UploadFile.UploadURL == "" {
		return "", errors.New("Linear did not return an upload URL")
	}
	uploadRequest, err := http.NewRequestWithContext(ctx, http.MethodPut, result.FileUpload.UploadFile.UploadURL, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	for _, header := range result.FileUpload.UploadFile.Headers {
		uploadRequest.Header.Set(header.Key, header.Value)
	}
	uploadResponse, err := client.client.Do(uploadRequest)
	if err != nil {
		return "", err
	}
	uploadResponse.Body.Close()
	if uploadResponse.StatusCode < 200 || uploadResponse.StatusCode >= 300 {
		return "", fmt.Errorf("Linear upload returned HTTP %d", uploadResponse.StatusCode)
	}
	return result.FileUpload.UploadFile.AssetURL, nil
}

func (s *server) persistLinearMigration(r *http.Request, job domain.MigrationJob) {
	_ = s.store.MutateWorkspace(context.Background(), workspaceKey(r), "migration.linear_completed", job.ID, nil, func(data *domain.Bootstrap) error {
		current, err := migrationByID(data, job.ID)
		if err != nil {
			return err
		}
		bundle := current.Bundle
		*current = job
		current.Bundle = bundle
		appendAudit(data, "completed", "migration", job.ID, migrationCountsMetadata(job.Counts))
		return nil
	})
}
func (s *server) failLinearMigration(r *http.Request, id string, err error) {
	_ = s.store.MutateWorkspace(context.Background(), workspaceKey(r), "migration.linear_failed", id, nil, func(data *domain.Bootstrap) error {
		job, findErr := migrationByID(data, id)
		if findErr != nil {
			return findErr
		}
		migrationErrorSummary(job, err)
		job.Status = "failed"
		return nil
	})
}
func (s *server) failLinearMigrationJob(r *http.Request, job domain.MigrationJob, message string) {
	job.Status = "failed"
	job.Errors = append(job.Errors, message)
	job.UpdatedAt = time.Now().UTC()
	s.persistLinearMigration(r, job)
}

func (s *server) rollbackLinearMigration(w http.ResponseWriter, r *http.Request, job domain.MigrationJob, token string) {
	if strings.TrimSpace(token) == "" {
		writeError(w, http.StatusBadRequest, "Linear API token is required for rollback")
		return
	}
	client := newLinearMigrationClient(token)
	mutationByType := map[string]string{"attachment": "attachmentDelete", "comment": "commentDelete", "projectUpdate": "projectUpdateDelete", "initiativeUpdate": "initiativeUpdateDelete", "relation": "issueRelationDelete", "customerRequest": "customerNeedDelete", "issue": "issueDelete", "milestone": "projectMilestoneDelete", "cycle": "cycleDelete", "initiative": "initiativeDelete", "project": "projectDelete", "release": "releaseDelete", "releasePipeline": "releasePipelineDelete", "customer": "customerDelete", "label": "issueLabelDelete", "labelGroup": "issueLabelDelete", "state": "workflowStateDelete", "user": "organizationInviteDelete"}
	errorsFound := []string{}
	for index := len(job.Mappings) - 1; index >= 0; index-- {
		mapping := &job.Mappings[index]
		if mapping.TargetID == "" || mapping.Status != "completed" && mapping.Status != "invited" || mapping.Action != "create" && mapping.Action != "invite" {
			continue
		}
		mutation := mutationByType[mapping.EntityType]
		if mutation == "" {
			continue
		}
		query := fmt.Sprintf("mutation Delete($id:String!){%s(id:$id){success}}", mutation)
		if err := client.call(r.Context(), query, map[string]any{"id": mapping.TargetID}, &map[string]any{}); err != nil {
			mapping.Error = err.Error()
			mapping.Status = "rollback_failed"
			errorsFound = append(errorsFound, mapping.EntityType+" "+mapping.TargetName+": "+err.Error())
		} else {
			mapping.Status = "rolled_back"
		}
	}
	now := time.Now().UTC()
	job.Status, job.Phase, job.Progress, job.UpdatedAt = "rolled_back", "complete", 100, now
	job.Errors = append(job.Errors, errorsFound...)
	s.persistLinearMigration(r, job)
	status := http.StatusOK
	if len(errorsFound) > 0 {
		status = http.StatusMultiStatus
	}
	writeJSON(w, status, publicMigration(job))
}
