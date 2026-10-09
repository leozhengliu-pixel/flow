package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Tools that bring the Flow agent to parity with Linear's agent: issue detail
// and history, project activity, multi-query issue search, the inbox, saved
// views, templates, customers, status updates, drafts, and reminders. Each
// reads the same records and reuses the same handlers as the Flow UI.

func mcpUserRef(user *domain.User) map[string]any {
	if user == nil || user.ID == "" {
		return nil
	}
	name := user.DisplayName
	if strings.TrimSpace(name) == "" {
		name = user.Name
	}
	ref := map[string]any{"id": user.ID, "name": name, "email": user.Email}
	if user.App {
		ref["app"] = true
	}
	return ref
}

func mcpUserName(data domain.Bootstrap, id string) string {
	if id == "" {
		return ""
	}
	if user, err := mcpFindUser(data, id); err == nil {
		if ref := mcpUserRef(&user); ref != nil {
			return ref["name"].(string)
		}
	}
	return ""
}

func (s *server) getMCPIssue(ctx context.Context, actor mcpActor, data domain.Bootstrap, args map[string]any) (any, error) {
	issue, err := mcpFindIssue(data, stringArg(args, "id"))
	if err != nil {
		return nil, err
	}
	q, err := s.mcpIssueQuery(ctx, actor)
	if err != nil {
		return nil, err
	}
	// Related issues are only named when the viewer can see them.
	ids := slices.Clone(issue.SubIssueIDs)
	if issue.ParentID != nil {
		ids = append(ids, *issue.ParentID)
	}
	for _, relation := range issue.Relations {
		ids = append(ids, relation.RelatedIssueID, relation.IssueID)
	}
	related := map[string]domain.Issue{}
	if len(ids) > 0 {
		if len(ids) > 500 {
			ids = ids[:500]
		}
		q.Filter = store.IssueFilter{Field: "id", Values: ids}
		q.Limit, q.Summary = 500, true
		page, err := s.store.QueryIssueRecords(ctx, q)
		if err != nil {
			return nil, err
		}
		for _, item := range page.Items {
			related[item.ID] = item
		}
	}
	ref := func(id string) map[string]any {
		item, ok := related[id]
		if !ok {
			return nil
		}
		return map[string]any{"id": item.ID, "identifier": item.Identifier, "title": item.Title, "status": item.State.Name}
	}
	result := map[string]any{
		"id": issue.ID, "identifier": issue.Identifier, "title": issue.Title, "description": issue.Description,
		"url": mcpIssueURL(data.Workspace.URLKey, issue.Identifier, args), "status": issue.State.Name, "statusType": issue.State.Type,
		"priority": issue.Priority, "priorityLabel": issue.PriorityLabel, "estimate": issue.Estimate, "dueDate": issue.DueDate,
		"team":     map[string]any{"id": issue.Team.ID, "key": issue.Team.Key, "name": issue.Team.Name},
		"assignee": mcpUserRef(issue.Assignee), "delegate": mcpUserRef(issue.Delegate), "createdBy": mcpUserRef(&issue.Creator),
		"createdAt": issue.CreatedAt, "updatedAt": issue.UpdatedAt, "startedAt": issue.StartedAt, "completedAt": issue.CompletedAt,
		"canceledAt": issue.CanceledAt, "archivedAt": issue.ArchivedAt, "recurrence": issue.Recurrence,
	}
	if issue.Icon != "" {
		result["icon"] = issue.Icon
	}
	if issue.NextOccurrenceAt != nil {
		result["nextOccurrenceAt"] = issue.NextOccurrenceAt
	}
	if issue.Project != nil {
		result["project"] = map[string]any{"id": issue.Project.ID, "name": issue.Project.Name}
		if issue.ProjectMilestoneID != nil {
			if project, err := mcpFindProject(data, issue.Project.ID); err == nil {
				for _, milestone := range project.Milestones {
					if milestone.ID == *issue.ProjectMilestoneID {
						result["projectMilestone"] = map[string]any{"id": milestone.ID, "name": milestone.Name}
					}
				}
			}
		}
	}
	if issue.CycleID != nil {
		for _, cycle := range data.Cycles {
			if cycle.ID == *issue.CycleID {
				result["cycle"] = map[string]any{"id": cycle.ID, "number": cycle.Number, "name": cycle.Name, "startsAt": cycle.StartsAt, "endsAt": cycle.EndsAt}
			}
		}
	}
	labels := []string{}
	for _, label := range issue.Labels {
		labels = append(labels, label.Name)
	}
	result["labels"] = labels
	if issue.ParentID != nil {
		result["parent"] = ref(*issue.ParentID)
	}
	children := []map[string]any{}
	for _, id := range issue.SubIssueIDs {
		if child := ref(id); child != nil {
			children = append(children, child)
		}
	}
	result["subIssues"] = children
	relations := map[string][]string{"blocks": {}, "blockedBy": {}, "relatedTo": {}, "duplicateOf": {}}
	for _, relation := range issue.Relations {
		other, kind := relation.RelatedIssueID, relation.Type
		if other == issue.ID {
			other, kind = relation.IssueID, inverseRelation(kind)
		}
		target := ref(other)
		if target == nil {
			continue
		}
		key := map[string]string{"blocks": "blocks", "blocked_by": "blockedBy", "related": "relatedTo", "duplicate": "duplicateOf"}[kind]
		if key != "" {
			relations[key] = append(relations[key], target["identifier"].(string))
		}
	}
	result["relations"] = relations
	attachments := []map[string]any{}
	for _, attachment := range issue.Attachments {
		attachments = append(attachments, map[string]any{"id": attachment.ID, "title": attachment.Title, "url": attachment.URL})
	}
	result["attachments"] = attachments
	return result, nil
}

func (s *server) listMCPIssueHistory(ctx context.Context, data domain.Bootstrap, args map[string]any) (any, error) {
	issue, err := mcpFindIssue(data, stringArg(args, "issueId"))
	if err != nil {
		return nil, err
	}
	events, next, err := s.store.ResourceActivitiesPage(ctx, data.Workspace.URLKey, issue.ID, stringArg(args, "cursor"), min(max(intArg(args, "limit", 50), 1), 100))
	if err != nil {
		return nil, err
	}
	slices.Reverse(events)
	items := make([]map[string]any, 0, len(events))
	for _, event := range events {
		items = append(items, mcpActivityView(data, event))
	}
	return map[string]any{"identifier": issue.Identifier, "title": issue.Title, "items": items, "nextCursor": next}, nil
}

// mcpActivityView turns a stored activity event into readable output. IDs in
// the change set are named, and bulky description snapshots are dropped.
func mcpActivityView(data domain.Bootstrap, event domain.ActivityEvent) map[string]any {
	changes := map[string]any{}
	for key, value := range event.Metadata {
		switch key {
		case "descriptionBefore", "descriptionStateBefore", "documentContent", "sortOrder", "stateBeforeId", "stateId":
			continue
		case "description":
			changes["descriptionEdited"] = true
			continue
		}
		changes[key] = value
		if value == "" {
			continue
		}
		name := ""
		switch key {
		case "assignee", "delegate", "previousAssignee":
			name = mcpUserName(data, value)
		case "project":
			if project, err := mcpFindProject(data, value); err == nil {
				name = project.Name
			}
		case "cycle":
			for _, cycle := range data.Cycles {
				if cycle.ID == value {
					name = cycle.Name
					if name == "" {
						name = "Cycle " + strconv.Itoa(cycle.Number)
					}
				}
			}
		case "team", "teamBefore":
			if team, err := mcpFindTeam(data, value); err == nil {
				name = team.Name
			}
		case "labels":
			names := []string{}
			for _, id := range strings.Split(value, ",") {
				for _, label := range data.Labels {
					if label.ID == id {
						names = append(names, label.Name)
					}
				}
			}
			name = strings.Join(names, ", ")
		}
		if name != "" {
			changes[key+"Name"] = name
		}
	}
	return map[string]any{"id": event.ID, "type": event.Type, "createdAt": event.CreatedAt, "actor": mcpUserRef(&event.Actor), "changes": changes, "summary": mcpActivitySummary(event.Type, changes)}
}

func mcpActivitySummary(kind string, changes map[string]any) string {
	text := func(key string) string {
		if value, ok := changes[key+"Name"].(string); ok && value != "" {
			return value
		}
		value, _ := changes[key].(string)
		return value
	}
	switch kind {
	case "issue.created", "issue.created_from_ask":
		return "created the issue"
	case "issue.imported":
		return "imported the issue"
	case "issue.archived":
		return "archived the issue"
	case "comment.created":
		return "commented"
	case "comment.deleted":
		return "deleted a comment"
	case "attachment.created", "issue.link_created":
		return "added attachment " + text("title")
	case "attachment.deleted":
		return "removed an attachment"
	case "issue.relation_added":
		return "added a " + strings.ReplaceAll(text("type"), "_", " ") + " relation"
	case "issue.relation_removed":
		return "removed a relation"
	case "issue.reminder_created", "project.reminder_created", "initiative.reminder_created":
		return "set a reminder for " + text("remindAt")
	case "issue.updated":
	default:
		return strings.ReplaceAll(strings.TrimPrefix(kind, "issue."), "_", " ")
	}
	set := func(key, label, cleared string) string {
		value := text(key)
		if value == "" {
			return cleared
		}
		return label + " " + value
	}
	parts := []string{}
	for _, key := range []string{"title", "state", "priority", "assignee", "delegate", "project", "projectMilestone", "cycle", "labels", "dueDate", "estimate", "parent", "team", "recurrence", "archived", "descriptionEdited"} {
		if _, ok := changes[key]; !ok {
			continue
		}
		switch key {
		case "title":
			parts = append(parts, "renamed the issue to "+text(key))
		case "state":
			parts = append(parts, "changed status to "+text(key))
		case "priority":
			parts = append(parts, "set priority to "+text(key))
		case "assignee":
			parts = append(parts, set(key, "assigned to", "removed the assignee"))
		case "delegate":
			parts = append(parts, set(key, "delegated to", "removed the delegate"))
		case "project":
			parts = append(parts, set(key, "moved to project", "removed from the project"))
		case "projectMilestone":
			parts = append(parts, set(key, "set milestone", "removed the milestone"))
		case "cycle":
			parts = append(parts, set(key, "moved to", "removed from the cycle"))
		case "labels":
			parts = append(parts, set(key, "set labels to", "removed all labels"))
		case "dueDate":
			parts = append(parts, set(key, "set due date to", "removed the due date"))
		case "estimate":
			parts = append(parts, set(key, "set estimate to", "removed the estimate"))
		case "parent":
			parts = append(parts, set(key, "set parent issue", "removed the parent issue"))
		case "team":
			parts = append(parts, set(key, "moved to team", "changed team"))
		case "recurrence":
			parts = append(parts, set(key, "set to repeat", "stopped repeating"))
		case "archived":
			if text(key) == "true" {
				parts = append(parts, "archived the issue")
			} else {
				parts = append(parts, "restored the issue")
			}
		case "descriptionEdited":
			parts = append(parts, "edited the description")
		}
	}
	if len(parts) == 0 {
		return "updated the issue"
	}
	return strings.Join(parts, "; ")
}

type mcpProjectEvent struct {
	ID        string         `json:"id"`
	Type      string         `json:"type"`
	Summary   string         `json:"summary"`
	Actor     map[string]any `json:"actor"`
	Changes   any            `json:"changes,omitempty"`
	CreatedAt time.Time      `json:"createdAt"`
}

func (s *server) listMCPProjectActivity(ctx context.Context, data domain.Bootstrap, args map[string]any) (any, error) {
	project, err := mcpFindProject(data, stringArg(args, "project"))
	if err != nil {
		return nil, err
	}
	events := []mcpProjectEvent{}
	creator := project.Creator
	if creator == nil {
		creator = project.Lead
	}
	events = append(events, mcpProjectEvent{ID: project.ID + ":created", Type: "project.created", Summary: "created the project", Actor: mcpUserRef(creator), CreatedAt: project.CreatedAt})
	// Property history lives in the workspace audit log, which the metadata
	// projection only keeps for admins (the same rule as GET /projects/{id}/history).
	for _, entry := range data.AuditLog {
		if entry.ResourceType != "project" || entry.ResourceID != project.ID {
			continue
		}
		summary := strings.ReplaceAll(entry.Action, "_", " ") + " the project"
		if changes, ok := entry.Metadata["changes"].([]projectPropertyChange); ok {
			summary = mcpProjectChangeSummary(changes)
		} else if raw, ok := entry.Metadata["changes"]; ok {
			var decoded []projectPropertyChange
			if jsonClone(raw, &decoded) == nil && len(decoded) > 0 {
				summary = mcpProjectChangeSummary(decoded)
			}
		} else if entry.Action == "relation_created" {
			summary = "added a project relation"
		}
		events = append(events, mcpProjectEvent{ID: entry.ID, Type: "project." + entry.Action, Summary: summary, Actor: mcpUserRef(&entry.Actor), Changes: entry.Metadata["changes"], CreatedAt: entry.CreatedAt})
	}
	for _, revision := range project.DescriptionRevisions {
		events = append(events, mcpProjectEvent{ID: revision.ID, Type: "project.description_edited", Summary: "edited the description", Actor: mcpUserRef(&revision.Author), CreatedAt: revision.CreatedAt})
	}
	for _, update := range data.ProjectUpdates[project.ID] {
		events = append(events, mcpProjectEvent{ID: update.ID, Type: "project.update_posted", Summary: "posted a project update (" + update.Health + ")", Actor: mcpUserRef(&update.User), Changes: map[string]string{"health": update.Health}, CreatedAt: update.CreatedAt})
	}
	cursor := ""
	for range 5 {
		page, next, err := s.store.ResourceActivitiesPage(ctx, data.Workspace.URLKey, project.ID, cursor, 100)
		if err != nil {
			return nil, err
		}
		for _, event := range page {
			view := mcpActivityView(data, event)
			events = append(events, mcpProjectEvent{ID: event.ID, Type: event.Type, Summary: view["summary"].(string), Actor: mcpUserRef(&event.Actor), Changes: view["changes"], CreatedAt: event.CreatedAt})
		}
		if cursor = next; cursor == "" {
			break
		}
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].CreatedAt.After(events[j].CreatedAt) })
	result := paginate(events, args)
	result["name"] = project.Name
	return result, nil
}

func mcpProjectChangeSummary(changes []projectPropertyChange) string {
	parts := []string{}
	for _, change := range changes {
		label := map[string]string{"startDate": "start date", "targetDate": "target date"}[change.Field]
		if label == "" {
			label = change.Field
		}
		if change.To == "" {
			parts = append(parts, "removed the "+label)
		} else {
			parts = append(parts, "changed "+label+" to "+change.To)
		}
	}
	if len(parts) == 0 {
		return "updated the project"
	}
	return strings.Join(parts, "; ")
}

func (s *server) searchMCPIssues(ctx context.Context, actor mcpActor, data domain.Bootstrap, args map[string]any) (any, error) {
	queries := stringsArg(args, "queries")
	if len(queries) == 0 || len(queries) > 5 {
		return nil, fmt.Errorf("provide 1 to 5 queries")
	}
	q, err := s.mcpIssueQuery(ctx, actor)
	if err != nil {
		return nil, err
	}
	if !boolArg(args, "includeArchived") {
		q.Archived = "false"
	}
	if value := stringArg(args, "team"); value != "" {
		team, err := mcpFindTeam(data, value)
		if err != nil {
			return nil, err
		}
		q.Filter.And = append(q.Filter.And, store.IssueFilter{Field: "team", Values: []string{team.ID}})
	}
	if value := stringArg(args, "project"); value != "" {
		project, err := mcpFindProject(data, value)
		if err != nil {
			return nil, err
		}
		q.Filter.And = append(q.Filter.And, store.IssueFilter{Field: "project", Values: []string{project.ID}})
	}
	limit := min(max(intArg(args, "limit", 20), 1), 50)
	type hit struct {
		issue   domain.Issue
		score   int
		queries []string
	}
	hits := map[string]*hit{}
	for _, query := range queries {
		// The same term expansion and scoring as GET /api/search/semantic.
		terms := semanticTerms(query)
		if len(terms) == 0 {
			continue
		}
		if len(terms) > 32 {
			return nil, fmt.Errorf("query %q has too many terms", query)
		}
		err := s.store.SearchIssueCandidateTerms(ctx, q, terms, nil, min(max(limit*4, 40), 200), func(issue domain.Issue) error {
			labels := []string{}
			for _, label := range issue.Labels {
				labels = append(labels, label.Name)
			}
			score, _ := semanticTextScore(query, issue.Title, issue.Description, issue.Identifier, issue.Team.Name, issue.State.Name, strings.Join(labels, " "))
			if score == 0 {
				score = 1
			}
			current := hits[issue.ID]
			if current == nil {
				current = &hit{issue: issue}
				hits[issue.ID] = current
			}
			if !slices.Contains(current.queries, query) {
				current.queries = append(current.queries, query)
				current.score += score
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	ranked := make([]*hit, 0, len(hits))
	for _, item := range hits {
		ranked = append(ranked, item)
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].issue.UpdatedAt.After(ranked[j].issue.UpdatedAt)
	})
	items := []map[string]any{}
	for _, item := range ranked[:min(limit, len(ranked))] {
		issue := item.issue
		result := map[string]any{"id": issue.ID, "identifier": issue.Identifier, "title": issue.Title, "status": issue.State.Name, "statusType": issue.State.Type, "priority": issue.Priority, "priorityLabel": issue.PriorityLabel, "team": issue.Team.Key, "assignee": mcpUserRef(issue.Assignee), "url": mcpIssueURL(data.Workspace.URLKey, issue.Identifier, args), "score": item.score, "matchedQueries": item.queries, "updatedAt": issue.UpdatedAt}
		if issue.Project != nil {
			result["project"] = issue.Project.Name
		}
		items = append(items, result)
	}
	return map[string]any{"queries": queries, "items": items, "total": len(ranked)}, nil
}

func (s *server) listMCPNotifications(ctx context.Context, actor mcpActor, data domain.Bootstrap, args map[string]any) (any, error) {
	query := store.NotificationQuery{Workspace: data.Workspace.URLKey, UserID: actor.User.ID, IncludeArchived: boolArg(args, "includeArchived"), IncludeSnoozed: boolArg(args, "includeSnoozed"), Cursor: stringArg(args, "cursor"), Limit: min(max(intArg(args, "limit", 50), 1), 100)}
	if boolArg(args, "unreadOnly") {
		unread := false
		query.Read = &unread
	}
	page, err := s.store.QueryNotifications(ctx, query)
	if err != nil {
		return nil, err
	}
	issueQuery, err := s.mcpIssueQuery(ctx, actor)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for _, item := range page.Notifications {
		if item.IssueID != "" {
			ids = append(ids, item.IssueID)
		}
	}
	issues := map[string]domain.Issue{}
	if len(ids) > 0 {
		issueQuery.Filter, issueQuery.Limit, issueQuery.Summary = store.IssueFilter{Field: "id", Values: ids}, 500, true
		result, err := s.store.QueryIssueRecords(ctx, issueQuery)
		if err != nil {
			return nil, err
		}
		for _, issue := range result.Items {
			issues[issue.ID] = issue
		}
	}
	items := []map[string]any{}
	for _, item := range page.Notifications {
		entry := map[string]any{"id": item.ID, "type": item.Type, "category": item.Category, "actor": mcpUserRef(&item.Actor), "read": item.ReadAt != nil, "readAt": item.ReadAt, "snoozedUntil": item.SnoozedUntil, "archivedAt": item.ArchivedAt, "occurrenceCount": item.OccurrenceCount, "createdAt": item.CreatedAt, "updatedAt": item.UpdatedAt}
		switch {
		case item.IssueID != "":
			// Skip notifications about issues the viewer (or a team-restricted key) can no longer see.
			issue, ok := issues[item.IssueID]
			if !ok {
				continue
			}
			entry["entity"] = map[string]any{"type": "issue", "id": issue.ID, "identifier": issue.Identifier, "title": issue.Title, "status": issue.State.Name, "url": mcpIssueURL(data.Workspace.URLKey, issue.Identifier, args)}
		case item.ProjectID != "":
			project, err := mcpFindProject(data, item.ProjectID)
			if err != nil {
				continue
			}
			entry["entity"] = map[string]any{"type": "project", "id": project.ID, "name": project.Name}
		case item.ReviewID != "":
			review, err := mcpFindReview(data, item.ReviewID)
			if err != nil {
				continue
			}
			entry["entity"] = map[string]any{"type": "review", "id": review.ID, "title": review.Title, "url": review.URL}
		default:
			entry["entity"] = map[string]any{"type": item.SourceType, "id": item.SourceID}
		}
		if item.CommentID != "" {
			entry["commentId"] = item.CommentID
		}
		items = append(items, entry)
	}
	return map[string]any{"items": items, "unreadCount": page.UnreadCount, "nextCursor": page.NextCursor}, nil
}

func mcpTeamVisible(data domain.Bootstrap, id string) bool {
	return id == "" || slices.ContainsFunc(data.Teams, func(team domain.Team) bool { return team.ID == id })
}

func listMCPViews(data domain.Bootstrap, args map[string]any) (any, error) {
	teamID := ""
	if value := stringArg(args, "team"); value != "" {
		team, err := mcpFindTeam(data, value)
		if err != nil {
			return nil, err
		}
		teamID = team.ID
	}
	query, resource := stringArg(args, "query"), stringArg(args, "resource")
	items := []map[string]any{}
	for _, view := range data.SavedViews {
		// The metadata projection already hides other members' personal views and
		// private teams; team-restricted API keys narrow it further.
		if !mcpTeamVisible(data, view.TeamID) || view.Scope == "personal" && view.OwnerID != data.Viewer.ID {
			continue
		}
		if view.ProjectID != "" {
			if _, err := mcpFindProject(data, view.ProjectID); err != nil {
				continue
			}
		}
		if teamID != "" && view.TeamID != teamID || query != "" && !containsFold(view.Name+" "+view.Description, query) || resource != "" && !strings.EqualFold(resource, view.Resource) {
			continue
		}
		item := map[string]any{"id": view.ID, "slugId": view.SlugID, "name": view.Name, "description": view.Description, "resource": view.Resource, "scope": view.Scope, "layout": view.View, "filters": view.Filters, "favorite": view.Favorite, "createdAt": view.CreatedAt, "updatedAt": view.UpdatedAt}
		if view.TeamID != "" {
			if team, err := mcpFindTeam(data, view.TeamID); err == nil {
				item["team"] = map[string]any{"id": team.ID, "key": team.Key, "name": team.Name}
			}
		}
		if view.ProjectID != "" {
			item["projectId"] = view.ProjectID
		}
		if view.OwnerID != "" {
			item["owner"] = mcpUserName(data, view.OwnerID)
		}
		items = append(items, item)
	}
	return paginate(items, args), nil
}

func listMCPTemplates(data domain.Bootstrap, args map[string]any) (any, error) {
	teamID := ""
	if value := stringArg(args, "team"); value != "" {
		team, err := mcpFindTeam(data, value)
		if err != nil {
			return nil, err
		}
		teamID = team.ID
	}
	kind, query := stringArg(args, "type"), stringArg(args, "query")
	matches := func(itemKind, name string, teamIDs ...string) bool {
		if kind != "" && kind != itemKind || query != "" && !containsFold(name, query) {
			return false
		}
		visible := len(teamIDs) == 0
		for _, id := range teamIDs {
			visible = visible || mcpTeamVisible(data, id)
		}
		return visible && (teamID == "" || slices.Contains(teamIDs, teamID))
	}
	labelNames := func(ids []string) []string {
		names := []string{}
		for _, id := range ids {
			for _, label := range data.Labels {
				if label.ID == id {
					names = append(names, label.Name)
				}
			}
		}
		return names
	}
	items := []map[string]any{}
	for _, item := range data.IssueTemplates {
		teams := []string{}
		for _, id := range []string{item.TeamID, item.VisibilityTeamID} {
			if id != "" {
				teams = append(teams, id)
			}
		}
		if !matches("issue", item.Name, teams...) {
			continue
		}
		items = append(items, map[string]any{"id": item.ID, "type": "issue", "name": item.Name, "description": item.Description, "teamId": item.TeamID, "title": item.Title, "body": item.Body, "priority": item.Priority, "labels": labelNames(item.LabelIDs), "subIssueCount": len(item.SubIssues), "updatedAt": item.UpdatedAt})
	}
	for _, item := range data.ProjectTemplates {
		if !matches("project", item.Name, item.TeamIDs...) {
			continue
		}
		milestones := []string{}
		for _, milestone := range item.Milestones {
			milestones = append(milestones, milestone.Name)
		}
		items = append(items, map[string]any{"id": item.ID, "type": "project", "name": item.Name, "description": item.TemplateDescription, "projectName": item.ProjectName, "summary": item.Summary, "teamIds": item.TeamIDs, "priority": item.Priority, "labels": labelNames(item.LabelIDs), "milestones": milestones, "updatedAt": item.UpdatedAt})
	}
	for _, item := range data.DocumentTemplates {
		teams := []string{}
		if item.TeamID != "" {
			teams = append(teams, item.TeamID)
		}
		if !matches("document", item.Name, teams...) {
			continue
		}
		items = append(items, map[string]any{"id": item.ID, "type": "document", "name": item.Name, "description": item.Description, "teamId": item.TeamID, "title": item.Title, "updatedAt": item.UpdatedAt})
	}
	return paginate(items, args), nil
}

func (s *server) listMCPCustomers(ctx context.Context, actor mcpActor, data domain.Bootstrap, args map[string]any) (any, error) {
	query := stringArg(args, "query")
	// The paged metadata projection cannot see issue records, so it drops every
	// issue-linked request. Read the workspace's requests and apply the same
	// rule (no guests; linked issue and project must be visible) here.
	all := data.CustomerRequests
	if raw, ok := s.store.WorkspaceMetadata(data.Workspace.URLKey); ok && data.ViewerRole != "guest" {
		all = raw.CustomerRequests
	}
	issueIDs := []string{}
	for _, request := range all {
		if request.IssueID != "" {
			issueIDs = append(issueIDs, request.IssueID)
		}
	}
	issues := map[string]domain.Issue{}
	if len(issueIDs) > 0 {
		q, err := s.mcpIssueQuery(ctx, actor)
		if err != nil {
			return nil, err
		}
		q.Filter, q.Limit, q.Summary = store.IssueFilter{Field: "id", Values: issueIDs}, 500, true
		page, err := s.store.QueryIssueRecords(ctx, q)
		if err != nil {
			return nil, err
		}
		for _, issue := range page.Items {
			issues[issue.ID] = issue
		}
	}
	requests := map[string][]domain.CustomerRequest{}
	for _, request := range all {
		if request.ArchivedAt != nil {
			continue
		}
		if _, ok := issues[request.IssueID]; request.IssueID != "" && !ok {
			continue
		}
		if request.ProjectID != "" {
			if _, err := mcpFindProject(data, request.ProjectID); err != nil {
				continue
			}
		}
		requests[request.CustomerID] = append(requests[request.CustomerID], request)
	}
	items := []map[string]any{}
	for _, customer := range data.Customers {
		if query != "" && !containsFold(customer.Name+" "+strings.Join(customer.Domains, " "), query) {
			continue
		}
		item := map[string]any{"id": customer.ID, "name": customer.Name, "status": customer.Status, "tier": customer.Tier, "domains": customer.Domains, "annualRevenue": customer.AnnualRevenue, "size": customer.Size, "owner": mcpUserName(data, customer.OwnerID), "requestCount": len(requests[customer.ID]), "createdAt": customer.CreatedAt, "updatedAt": customer.UpdatedAt}
		if boolArg(args, "includeRequests") {
			needs := []map[string]any{}
			for _, request := range requests[customer.ID] {
				need := map[string]any{"id": request.ID, "body": request.Body, "source": request.Source, "priority": request.Priority, "creator": mcpUserRef(&request.Creator), "createdAt": request.CreatedAt}
				if issue, ok := issues[request.IssueID]; ok {
					need["issue"] = map[string]any{"id": issue.ID, "identifier": issue.Identifier, "title": issue.Title, "status": issue.State.Name}
				}
				if project, err := mcpFindProject(data, request.ProjectID); request.ProjectID != "" && err == nil {
					need["project"] = map[string]any{"id": project.ID, "name": project.Name}
				}
				needs = append(needs, need)
			}
			item["requests"] = needs
		}
		items = append(items, item)
	}
	return paginate(items, args), nil
}

func (s *server) saveMCPStatusUpdate(ctx context.Context, actor mcpActor, data domain.Bootstrap, args map[string]any) (any, error) {
	kind, id := stringArg(args, "type"), stringArg(args, "id")
	var parentID, parentName string
	var authorID string
	richBody := false // the update holds an editor document for its body
	switch kind {
	case "project":
		project, err := mcpFindProject(data, stringArg(args, "project"))
		if err != nil {
			return nil, err
		}
		parentID, parentName = project.ID, project.Name
		for _, update := range data.ProjectUpdates[project.ID] {
			if id != "" && update.ID == id {
				authorID, richBody = update.User.ID, update.BodyData != nil
			}
		}
	case "initiative":
		initiative, err := mcpFindInitiative(data, stringArg(args, "initiative"))
		if err != nil {
			return nil, err
		}
		parentID, parentName = initiative.ID, initiative.Name
		for _, update := range data.InitiativeUpdates[initiative.ID] {
			if id != "" && update.ID == id {
				authorID, richBody = update.User.ID, update.BodyData != nil
			}
		}
	default:
		return nil, fmt.Errorf("type must be project or initiative")
	}
	body, health := stringArg(args, "body"), stringArg(args, "health")
	var result any
	var err error
	if id == "" {
		if body == "" {
			return nil, fmt.Errorf("body is required when creating a status update")
		}
		input := domain.ProjectUpdateCreateInput{Body: body, BodyData: s.mcpMentionResolver(ctx, actor, &data, body).companionDocument(body), Health: health}
		if kind == "project" {
			result, err = invokeJSONHandler(ctx, http.MethodPost, map[string]string{"id": parentID}, input, s.createProjectUpdate)
		} else {
			result, err = invokeJSONHandler(ctx, http.MethodPost, map[string]string{"id": parentID}, domain.InitiativeUpdateCreateInput(input), s.createInitiativeUpdate)
		}
	} else {
		if authorID == "" {
			return nil, fmt.Errorf("status update %q not found", id)
		}
		if authorID != actor.User.ID && !workspaceAdminRole(data.ViewerRole) {
			return nil, fmt.Errorf("only the author or a workspace admin can edit this status update")
		}
		input := domain.ProjectUpdateMutationInput{}
		if _, ok := args["body"]; ok {
			mentions := s.mcpMentionResolver(ctx, actor, &data, body)
			input.Body, input.BodyData = &body, mentions.companionDocument(body)
			if input.BodyData == nil && richBody {
				// The stored document would otherwise keep showing the old body.
				input.BodyData = mentions.markdownDocument(body)
			}
		}
		if health != "" {
			input.Health = &health
		}
		if input.Body == nil && input.Health == nil {
			return nil, fmt.Errorf("provide body or health to update")
		}
		handler := s.updateProjectUpdate
		if kind == "initiative" {
			handler = s.updateInitiativeUpdate
		}
		result, err = invokeJSONHandler(ctx, http.MethodPatch, map[string]string{"id": parentID, "updateId": id}, input, handler)
	}
	if err != nil {
		return nil, err
	}
	var saved map[string]any
	if err := jsonClone(result, &saved); err != nil {
		return nil, err
	}
	return map[string]any{"id": saved["id"], "type": kind, "parentId": parentID, "name": parentName, "health": saved["health"], "body": saved["body"], "createdAt": saved["createdAt"], "editedAt": saved["editedAt"]}, nil
}

func (s *server) saveMCPDraft(ctx context.Context, actor mcpActor, data domain.Bootstrap, args map[string]any) (any, error) {
	id := stringArg(args, "id")
	kind := normalizeDraftType(stringArg(args, "type"))
	var current domain.Draft
	if id != "" {
		index := slices.IndexFunc(data.Drafts, func(item domain.Draft) bool { return item.ID == id && item.UserID == data.Viewer.ID })
		if index < 0 {
			return nil, fmt.Errorf("draft %q not found", id)
		}
		current = data.Drafts[index]
		kind = current.Type
	}
	if kind == "" {
		kind = "issue"
	}
	metadata := map[string]any{}
	for key, value := range current.Metadata {
		metadata[key] = value
	}
	body, bodyProvided := args["body"].(string)
	if !bodyProvided {
		body = current.Body
	}
	title := current.Title
	switch kind {
	case "issue":
		if value, ok := args["title"].(string); ok {
			title = strings.TrimSpace(value)
			metadata["title"] = title
		}
		teamID, _ := metadata["teamId"].(string)
		if value := stringArg(args, "team"); value != "" {
			team, err := mcpFindTeam(data, value)
			if err != nil {
				return nil, err
			}
			teamID = team.ID
		}
		if teamID == "" {
			if len(data.Teams) == 0 {
				return nil, fmt.Errorf("team is required")
			}
			teamID = data.Teams[0].ID
		}
		metadata["teamId"] = teamID
		if value := stringArg(args, "state"); value != "" {
			stateID, err := resolveStateID(data, teamID, value)
			if err != nil {
				return nil, err
			}
			metadata["stateId"] = stateID
		}
		if hasNumberArg(args, "priority") {
			metadata["priority"] = intArg(args, "priority", 0)
		}
		if value := stringArg(args, "assignee"); value != "" {
			user, err := mcpFindUser(data, value)
			if err != nil {
				return nil, err
			}
			metadata["assigneeId"] = user.ID
		}
		if value := stringArg(args, "project"); value != "" {
			project, err := mcpFindProject(data, value)
			if err != nil {
				return nil, err
			}
			metadata["projectId"] = project.ID
		}
		if _, ok := args["labels"]; ok {
			labelIDs, err := resolveLabelIDs(data, stringsArg(args, "labels"), "issue")
			if err != nil {
				return nil, err
			}
			metadata["labelIds"] = labelIDs
		}
		if value := stringArg(args, "dueDate"); value != "" {
			if _, err := time.Parse("2006-01-02", value); err != nil {
				return nil, fmt.Errorf("dueDate must be YYYY-MM-DD")
			}
			metadata["dueDate"] = value
		}
		if bodyProvided {
			// The composer restores its editor from metadata.description.
			metadata["description"] = mcpComposerDescription(body, s.mcpMentionResolver(ctx, actor, &data, body))
		}
		if title == "" {
			title = "Untitled issue"
		}
	case "project_update":
		if value := stringArg(args, "project"); value != "" {
			project, err := mcpFindProject(data, value)
			if err != nil {
				return nil, err
			}
			current.ResourceID = project.ID
		}
		if current.ResourceID == "" {
			return nil, fmt.Errorf("project is required for a project update draft")
		}
		if value := stringArg(args, "health"); value != "" {
			metadata["health"] = value
		}
		if bodyProvided {
			delete(metadata, "bodyData")
		}
	default:
		return nil, fmt.Errorf("drafts of type %q cannot be saved by the agent", kind)
	}
	input := draftInput{Type: &kind, Title: &title, Body: &body, Metadata: metadata}
	if current.ResourceID != "" {
		input.ResourceID = &current.ResourceID
	}
	var result any
	var err error
	if id == "" {
		result, err = invokeJSONHandler(ctx, http.MethodPost, nil, input, s.createDraft)
	} else {
		result, err = invokeJSONHandler(ctx, http.MethodPatch, map[string]string{"id": id}, input, s.updateDraft)
	}
	if err != nil {
		return nil, err
	}
	var saved domain.Draft
	if err := jsonClone(result, &saved); err != nil {
		return nil, err
	}
	return map[string]any{"id": saved.ID, "type": saved.Type, "title": saved.Title, "body": saved.Body, "resourceId": saved.ResourceID, "metadata": saved.Metadata, "updatedAt": saved.UpdatedAt}, nil
}

// mcpComposerDescription mirrors the create-issue composer's stored
// description: Markdown plus a paragraph-per-line editor document, with inline
// formatting as marks and references as mention nodes.
func mcpComposerDescription(markdown string, mentions *mentionResolver) map[string]any {
	content := []any{}
	for _, line := range strings.Split(markdown, "\n") {
		paragraph := map[string]any{"type": "paragraph"}
		if inline := mcpMarkdownInlineNodes(line); len(inline) > 0 {
			paragraph["content"] = inline
		}
		content = append(content, paragraph)
	}
	document := map[string]any{"type": "doc", "content": content}
	if mentions != nil {
		document = mentions.convert(document)
	}
	raw, _ := json.Marshal(document)
	return map[string]any{"markdown": markdown, "document": document, "documentJSON": string(raw), "contentState": ""}
}

func (s *server) createMCPReminder(ctx context.Context, data domain.Bootstrap, args map[string]any) (any, error) {
	remindAt, err := mcpFutureDate(stringArg(args, "remindAt"))
	if err != nil {
		return nil, err
	}
	targets := 0
	for _, key := range []string{"issue", "project", "initiative"} {
		if stringArg(args, key) != "" {
			targets++
		}
	}
	if targets != 1 {
		return nil, fmt.Errorf("provide exactly one of issue, project, or initiative")
	}
	input := domain.IssueReminderInput{RemindAt: remindAt.UTC().Format(time.RFC3339)}
	target := map[string]any{}
	var handler http.HandlerFunc
	var targetID string
	switch {
	case stringArg(args, "issue") != "":
		issue, err := mcpFindIssue(data, stringArg(args, "issue"))
		if err != nil {
			return nil, err
		}
		targetID, handler = issue.ID, s.createIssueReminder
		target = map[string]any{"type": "issue", "id": issue.ID, "identifier": issue.Identifier, "title": issue.Title}
	case stringArg(args, "project") != "":
		project, err := mcpFindProject(data, stringArg(args, "project"))
		if err != nil {
			return nil, err
		}
		targetID, handler = project.ID, s.createProjectReminder
		target = map[string]any{"type": "project", "id": project.ID, "name": project.Name}
	default:
		initiative, err := mcpFindInitiative(data, stringArg(args, "initiative"))
		if err != nil {
			return nil, err
		}
		targetID, handler = initiative.ID, s.createInitiativeReminder
		target = map[string]any{"type": "initiative", "id": initiative.ID, "name": initiative.Name}
	}
	if target["type"] == "issue" {
		ctx = store.WithIssueRecordMutations(ctx, targetID)
	}
	result, err := invokeJSONHandler(ctx, http.MethodPost, map[string]string{"id": targetID}, input, handler)
	if err != nil {
		return nil, err
	}
	var saved domain.Notification
	if err := jsonClone(result, &saved); err != nil {
		return nil, err
	}
	return map[string]any{"id": saved.ID, "remindAt": input.RemindAt, "target": target}, nil
}

// mcpFutureDate accepts an ISO timestamp/date or a forward ISO-8601 duration (P1D, PT3H).
func mcpFutureDate(value string) (time.Time, error) {
	now := time.Now().UTC()
	if strings.HasPrefix(value, "P") {
		past, err := mcpDateAt("-"+value, now)
		if err != nil {
			return time.Time{}, err
		}
		return now.Add(now.Sub(past)), nil
	}
	date, err := mcpDateAt(value, now)
	if err != nil {
		return time.Time{}, err
	}
	if !date.After(now) {
		return time.Time{}, fmt.Errorf("remindAt must be in the future")
	}
	return date, nil
}
