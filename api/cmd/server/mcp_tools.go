package main

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/domain"
)

func (s *server) callFlowTool(ctx context.Context, actor mcpActor, name string, args map[string]any) (any, error) {
	data, err := s.mcpWorkspaceData(ctx, actor)
	if err != nil {
		return nil, err
	}
	switch name {
	case "get_workspace":
		return map[string]any{"id": data.Workspace.ID, "name": data.Workspace.Name, "urlKey": data.Workspace.URLKey, "viewer": data.Viewer}, nil
	case "list_teams":
		items := slices.Clone(data.Teams)
		query := lowerArg(args, "query")
		items = slices.DeleteFunc(items, func(item domain.Team) bool { return query != "" && !containsFold(item.Name+" "+item.Key, query) })
		return paginate(items, args), nil
	case "get_team":
		return mcpFindTeam(data, stringArg(args, "query"))
	case "list_users":
		items := slices.Clone(data.Users)
		query := lowerArg(args, "query")
		if teamQuery := stringArg(args, "team"); teamQuery != "" {
			team, err := mcpFindTeam(data, teamQuery)
			if err != nil {
				return nil, err
			}
			allowed := map[string]bool{}
			for _, member := range data.TeamMembers {
				if member.TeamID == team.ID {
					allowed[member.UserID] = true
				}
			}
			items = slices.DeleteFunc(items, func(item domain.User) bool { return !allowed[item.ID] })
		}
		items = slices.DeleteFunc(items, func(item domain.User) bool {
			return query != "" && !containsFold(item.Name+" "+item.DisplayName+" "+item.Email, query)
		})
		return paginate(items, args), nil
	case "get_user":
		return mcpFindUser(data, stringArg(args, "query"))
	case "list_issue_statuses":
		team, err := mcpFindTeam(data, stringArg(args, "team"))
		if err != nil {
			return nil, err
		}
		items := slices.Clone(data.States)
		items = slices.DeleteFunc(items, func(item domain.WorkflowState) bool { return item.TeamID != "" && item.TeamID != team.ID })
		sort.SliceStable(items, func(i, j int) bool { return items[i].Position < items[j].Position })
		return items, nil
	case "get_issue_status":
		team, err := mcpFindTeam(data, stringArg(args, "team"))
		if err != nil {
			return nil, err
		}
		id, nameArg := stringArg(args, "id"), stringArg(args, "name")
		for _, state := range data.States {
			if (state.TeamID == "" || state.TeamID == team.ID) && (equalFoldAny(id, state.ID) || equalFoldAny(nameArg, state.Name)) {
				return state, nil
			}
		}
		return nil, fmt.Errorf("issue status not found")
	case "list_issue_labels", "list_project_labels", "list_initiative_labels":
		items := slices.Clone(data.Labels)
		resourceType := strings.TrimSuffix(strings.TrimPrefix(name, "list_"), "_labels")
		items = slices.DeleteFunc(items, func(item domain.IssueLabel) bool {
			if resourceType == "issue" && item.ResourceType != "" && item.ResourceType != "issue" {
				return true
			}
			if resourceType != "issue" && item.ResourceType != resourceType {
				return true
			}
			return item.ArchivedAt != nil || lowerArg(args, "name") != "" && !containsFold(item.Name, lowerArg(args, "name"))
		})
		if teamQuery := stringArg(args, "team"); teamQuery != "" {
			team, err := mcpFindTeam(data, teamQuery)
			if err != nil {
				return nil, err
			}
			items = slices.DeleteFunc(items, func(item domain.IssueLabel) bool { return item.Scope != "" && item.Scope != team.ID })
		}
		return paginate(items, args), nil
	case "list_issues":
		items := slices.Clone(data.Issues)
		items = filterIssues(data, items, args)
		return paginate(items, args), nil
	case "list_cycles":
		team, err := mcpFindTeam(data, stringArg(args, "teamId"))
		if err != nil {
			return nil, err
		}
		items := slices.Clone(data.Cycles)
		filter := stringArg(args, "type")
		items = slices.DeleteFunc(items, func(item domain.Cycle) bool {
			if item.TeamID != team.ID {
				return true
			}
			if filter == "current" {
				return item.Status != "current" && item.Status != "active"
			}
			if filter == "previous" {
				return item.Status != "completed"
			}
			if filter == "next" {
				return item.Status != "upcoming"
			}
			return false
		})
		return items, nil
	case "list_projects":
		items := slices.Clone(data.Projects)
		items = filterProjects(data, items, args)
		return paginate(items, args), nil
	case "get_project":
		project, err := mcpFindProject(data, stringArg(args, "query"))
		if err != nil {
			return nil, err
		}
		return project, nil
	case "list_milestones":
		project, err := mcpFindProject(data, stringArg(args, "project"))
		if err != nil {
			return nil, err
		}
		return project.Milestones, nil
	case "list_initiatives":
		items := slices.Clone(data.Initiatives)
		query := lowerArg(args, "query")
		items = slices.DeleteFunc(items, func(item domain.Initiative) bool {
			return query != "" && !containsFold(item.Name+" "+item.Summary+" "+item.Description, query) || stringArg(args, "status") != "" && !equalFoldAny(stringArg(args, "status"), item.Status) || !matchesUser(item.Owner, data.Viewer, stringArg(args, "owner")) || !matchesTeamID(data, item.LeadTeamID, stringArg(args, "leadTeam")) || stringArg(args, "parentInitiative") != "" && !slices.ContainsFunc(item.ParentInitiativeIDs, func(id string) bool {
				parent, _ := mcpFindInitiative(data, id)
				return equalFoldAny(stringArg(args, "parentInitiative"), parent.ID, parent.Name, parent.SlugID)
			})
		})
		return paginate(items, args), nil
	case "get_initiative":
		return mcpFindInitiative(data, stringArg(args, "query"))
	case "list_documents":
		items := slices.Clone(data.Documents)
		query := lowerArg(args, "query")
		items = slices.DeleteFunc(items, func(item domain.Document) bool {
			return !boolArg(args, "includeArchived") && item.ArchivedAt != nil || query != "" && !containsFold(item.Title+" "+item.Content, query) || stringArg(args, "creatorId") != "" && item.Creator.ID != stringArg(args, "creatorId") || stringArg(args, "projectId") != "" && !slices.Contains(item.ProjectIDs, stringArg(args, "projectId")) || stringArg(args, "teamId") != "" && !slices.Contains(item.TeamIDs, stringArg(args, "teamId"))
		})
		return paginate(items, args), nil
	case "get_document":
		return mcpFindDocument(data, stringArg(args, "id"))
	case "list_comments":
		return s.listMCPComments(data, args)
	case "get_status_updates":
		return statusUpdates(data, args)
	case "list_release_pipelines":
		items := slices.Clone(data.ReleasePipelines)
		query := lowerArg(args, "query")
		items = slices.DeleteFunc(items, func(item domain.ReleasePipeline) bool {
			return query != "" && !containsFold(item.Name, query) || hasBoolArg(args, "isProduction") && item.Production != boolArg(args, "isProduction") || stringArg(args, "type") != "" && item.Type != stringArg(args, "type")
		})
		return paginate(items, args), nil
	case "list_releases":
		items := slices.Clone(data.Releases)
		query := lowerArg(args, "query")
		pipeline := stringArg(args, "pipeline")
		items = slices.DeleteFunc(items, func(item domain.Release) bool {
			return !boolArg(args, "includeArchived") && item.ArchivedAt != nil || query != "" && !containsFold(item.Name+" "+item.Version, query) || stringArg(args, "version") != "" && item.Version != stringArg(args, "version") || pipeline != "" && !releasePipelineMatches(data, item.PipelineID, pipeline) || stringArg(args, "stage") != "" && !equalFoldAny(stringArg(args, "stage"), item.Stage, item.Status) || hasBoolArg(args, "hasReleaseNotes") && (item.ReleaseNotes != "") != boolArg(args, "hasReleaseNotes")
		})
		return paginate(items, args), nil
	case "list_release_notes":
		items := []map[string]any{}
		for _, release := range data.Releases {
			if release.ReleaseNotes == "" && !boolArg(args, "includeArchived") {
				continue
			}
			items = append(items, map[string]any{"id": release.ID + "_notes", "title": release.Name + " release notes", "content": release.ReleaseNotes, "release": release, "createdAt": release.CreatedAt, "updatedAt": release.UpdatedAt})
		}
		return paginate(items, args), nil
	case "list_diffs":
		items := slices.Clone(data.Reviews)
		query := lowerArg(args, "query")
		items = slices.DeleteFunc(items, func(item domain.CodeReview) bool {
			return query != "" && !containsFold(item.Title+" "+item.HeadBranch+" "+item.SlugID+" "+strconv.Itoa(item.Number), query) || stringArg(args, "owner") != "" && !equalFoldAny(stringArg(args, "owner"), item.RepositoryOwner) || stringArg(args, "repo") != "" && !equalFoldAny(stringArg(args, "repo"), item.RepositoryName) || stringArg(args, "status") != "" && !equalFoldAny(stringArg(args, "status"), item.Status)
		})
		return paginate(items, args), nil
	case "get_diff":
		return mcpFindReview(data, stringArg(args, "urlOrId"))
	case "get_diff_threads":
		review, err := mcpFindReview(data, stringArg(args, "urlOrId"))
		if err != nil {
			return nil, err
		}
		items := slices.Clone(review.Events)
		if threadID := stringArg(args, "threadId"); threadID != "" {
			items = slices.DeleteFunc(items, func(item domain.ReviewEvent) bool { return item.ID != threadID })
		}
		return items, nil
	case "extract_images":
		return extractMarkdownImages(stringArg(args, "markdown")), nil
	case "list_agent_skills":
		return s.listAgentSkills(data, args), nil
	case "get_agent_skill":
		for _, item := range s.listAgentSkills(data, map[string]any{})["items"].([]map[string]any) {
			if item["id"] == stringArg(args, "id") {
				return item, nil
			}
		}
		return nil, fmt.Errorf("agent skill not found")
	case "search_documentation":
		return searchFlowDocumentation(stringArg(args, "query"), intArg(args, "page", 1)), nil
	default:
		return s.callFlowWriteTool(ctx, actor, data, name, args)
	}
}

func (s *server) mcpWorkspaceData(ctx context.Context, actor mcpActor) (domain.Bootstrap, error) {
	data, ok, err := s.store.BootstrapForUser(ctx, actor.WorkspaceKey, actor.User.ID)
	if err != nil || !ok {
		return data, fmt.Errorf("workspace access denied")
	}
	if len(actor.APIKey.TeamIDs) == 0 {
		return data, nil
	}
	allowed := func(id string) bool { return slices.Contains(actor.APIKey.TeamIDs, id) }
	data.Teams = slices.DeleteFunc(data.Teams, func(item domain.Team) bool { return !allowed(item.ID) })
	data.States = slices.DeleteFunc(data.States, func(item domain.WorkflowState) bool { return item.TeamID != "" && !allowed(item.TeamID) })
	data.Issues = slices.DeleteFunc(data.Issues, func(item domain.Issue) bool { return !allowed(item.Team.ID) })
	data.Cycles = slices.DeleteFunc(data.Cycles, func(item domain.Cycle) bool { return !allowed(item.TeamID) })
	data.Projects = slices.DeleteFunc(data.Projects, func(item domain.Project) bool {
		return len(item.TeamIDs) > 0 && !slices.ContainsFunc(item.TeamIDs, allowed)
	})
	return data, nil
}

func filterIssues(data domain.Bootstrap, items []domain.Issue, args map[string]any) []domain.Issue {
	query := lowerArg(args, "query")
	return slices.DeleteFunc(items, func(item domain.Issue) bool {
		if !boolArg(args, "includeArchived") && item.ArchivedAt != nil || query != "" && !containsFold(item.Title+" "+item.Description+" "+item.Identifier, query) || hasNumberArg(args, "priority") && item.Priority != intArg(args, "priority", 0) || stringArg(args, "parentId") != "" && (item.ParentID == nil || !equalFoldAny(stringArg(args, "parentId"), *item.ParentID)) {
			return true
		}
		if team := stringArg(args, "team"); team != "" && !equalFoldAny(team, item.Team.ID, item.Team.Key, item.Team.Name) {
			return true
		}
		if state := stringArg(args, "state"); state != "" && !equalFoldAny(state, item.State.ID, item.State.Name, item.State.Type) {
			return true
		}
		if assignee := stringArg(args, "assignee"); assignee != "" && !matchesUser(item.Assignee, data.Viewer, assignee) {
			return true
		}
		if project := stringArg(args, "project"); project != "" && (item.Project == nil || !equalFoldAny(project, item.Project.ID, item.Project.Name)) {
			return true
		}
		if cycle := stringArg(args, "cycle"); cycle != "" && (item.CycleID == nil || !cycleMatches(data, *item.CycleID, cycle)) {
			return true
		}
		if label := stringArg(args, "label"); label != "" && !slices.ContainsFunc(item.Labels, func(item domain.IssueLabel) bool { return equalFoldAny(label, item.ID, item.Name) }) {
			return true
		}
		return false
	})
}

func filterProjects(data domain.Bootstrap, items []domain.Project, args map[string]any) []domain.Project {
	query := lowerArg(args, "query")
	return slices.DeleteFunc(items, func(item domain.Project) bool {
		if query != "" && !containsFold(item.Name+" "+item.Summary+" "+item.Description, query) || stringArg(args, "state") != "" && !equalFoldAny(stringArg(args, "state"), item.Status.ID, item.Status.Name, item.Status.Type) {
			return true
		}
		if team := stringArg(args, "team"); team != "" && !slices.ContainsFunc(item.TeamIDs, func(id string) bool { return matchesTeamID(data, id, team) }) {
			return true
		}
		if member := stringArg(args, "member"); member != "" && !slices.ContainsFunc(item.MemberIDs, func(id string) bool { user, _ := mcpFindUser(data, id); return matchesUser(&user, data.Viewer, member) }) {
			return true
		}
		if initiative := stringArg(args, "initiative"); initiative != "" && !slices.ContainsFunc(item.Initiatives, func(id string) bool {
			found, _ := mcpFindInitiative(data, id)
			return equalFoldAny(initiative, found.ID, found.Name, found.SlugID)
		}) {
			return true
		}
		return false
	})
}

func (s *server) listMCPComments(data domain.Bootstrap, args map[string]any) (any, error) {
	parents := []string{"issueId", "projectId", "initiativeId", "documentId", "milestoneId", "statusUpdateId"}
	provided := []string{}
	for _, key := range parents {
		if value := stringArg(args, key); value != "" {
			provided = append(provided, value)
		}
	}
	if len(provided) != 1 {
		return nil, fmt.Errorf("provide exactly one comment parent")
	}
	id := provided[0]
	if issue, err := mcpFindIssue(data, id); err == nil {
		return paginate(data.Comments[issue.ID], args), nil
	}
	if project, err := mcpFindProject(data, id); err == nil {
		return paginate(project.Comments, args), nil
	}
	if initiative, err := mcpFindInitiative(data, id); err == nil {
		return paginate(initiative.Comments, args), nil
	}
	if document, err := mcpFindDocument(data, id); err == nil {
		return paginate(data.Comments[document.ID], args), nil
	}
	for _, project := range data.Projects {
		for _, milestone := range project.Milestones {
			if milestone.ID == id {
				return paginate(data.Comments[id], args), nil
			}
		}
	}
	for _, updates := range appendProjectAndInitiativeUpdates(data) {
		if updates.ID == id {
			return paginate(updates.Comments, args), nil
		}
	}
	return nil, fmt.Errorf("comment parent not found")
}

type mcpStatusUpdate struct {
	ID        string           `json:"id"`
	Type      string           `json:"type"`
	ParentID  string           `json:"parentId"`
	Body      string           `json:"body"`
	Health    string           `json:"health"`
	User      domain.User      `json:"user"`
	Comments  []domain.Comment `json:"comments"`
	CreatedAt time.Time        `json:"createdAt"`
}

func statusUpdates(data domain.Bootstrap, args map[string]any) (any, error) {
	typeName := stringArg(args, "type")
	items := []mcpStatusUpdate{}
	if typeName == "project" {
		for parentID, updates := range data.ProjectUpdates {
			for _, update := range updates {
				items = append(items, mcpStatusUpdate{ID: update.ID, Type: "project", ParentID: parentID, Body: update.Body, Health: update.Health, User: update.User, Comments: update.Comments, CreatedAt: update.CreatedAt})
			}
		}
	} else if typeName == "initiative" {
		for parentID, updates := range data.InitiativeUpdates {
			for _, update := range updates {
				items = append(items, mcpStatusUpdate{ID: update.ID, Type: "initiative", ParentID: parentID, Body: update.Body, Health: update.Health, User: update.User, Comments: update.Comments, CreatedAt: update.CreatedAt})
			}
		}
	} else {
		return nil, fmt.Errorf("type must be project or initiative")
	}
	if id := stringArg(args, "id"); id != "" {
		items = slices.DeleteFunc(items, func(item mcpStatusUpdate) bool { return item.ID != id })
	}
	return paginate(items, args), nil
}

func appendProjectAndInitiativeUpdates(data domain.Bootstrap) []mcpStatusUpdate {
	items := []mcpStatusUpdate{}
	for parentID, updates := range data.ProjectUpdates {
		for _, update := range updates {
			items = append(items, mcpStatusUpdate{ID: update.ID, Type: "project", ParentID: parentID, Comments: update.Comments})
		}
	}
	for parentID, updates := range data.InitiativeUpdates {
		for _, update := range updates {
			items = append(items, mcpStatusUpdate{ID: update.ID, Type: "initiative", ParentID: parentID, Comments: update.Comments})
		}
	}
	return items
}

func (s *server) listAgentSkills(data domain.Bootstrap, args map[string]any) map[string]any {
	items := []map[string]any{}
	settings := data.UserSettings[data.Viewer.ID]
	if strings.TrimSpace(settings.AgentInstructions) != "" {
		items = append(items, map[string]any{"id": "flow-workspace-guidance", "name": "Flow workspace guidance", "instructions": settings.AgentInstructions, "createdAt": settings.UpdatedAt, "updatedAt": settings.UpdatedAt})
	}
	return paginate(items, args)
}

var markdownImagePattern = regexp.MustCompile(`!\[[^\]]*\]\(([^\s)]+)(?:\s+"[^"]*")?\)|<img[^>]+src=["']([^"']+)["']`)

func extractMarkdownImages(markdown string) map[string]any {
	images := []map[string]string{}
	for _, match := range markdownImagePattern.FindAllStringSubmatch(markdown, -1) {
		url := match[1]
		if url == "" {
			url = match[2]
		}
		images = append(images, map[string]string{"url": url, "markdown": match[0]})
	}
	return map[string]any{"images": images}
}

func searchFlowDocumentation(query string, page int) map[string]any {
	docs := []map[string]string{
		{"title": "Flow MCP", "url": "/docs/mcp", "content": "Connect MCP clients to /mcp for read-write access or /mcp/readonly for read-only access. Flow supports OAuth 2.1 with PKCE and scoped API keys."},
		{"title": "Issues", "url": "/docs/issues", "content": "Issues belong to a team and can have status, priority, assignee, project, cycle, labels, due date, parent, relations, comments, and attachments."},
		{"title": "Projects and initiatives", "url": "/docs/projects", "content": "Projects group issues, milestones, resources, updates, members, teams, and initiatives. Initiatives group projects for workspace-level planning."},
	}
	query = strings.ToLower(strings.TrimSpace(query))
	results := slices.DeleteFunc(docs, func(item map[string]string) bool {
		return query != "" && !containsFold(item["title"]+" "+item["content"], query)
	})
	return map[string]any{"results": results, "page": max(1, page), "hasMore": false}
}

func paginate[T any](items []T, args map[string]any) map[string]any {
	start, _ := strconv.Atoi(stringArg(args, "cursor"))
	if start < 0 || start > len(items) {
		start = 0
	}
	limit := intArg(args, "limit", 50)
	limit = min(max(limit, 1), 250)
	end := min(start+limit, len(items))
	next := ""
	if end < len(items) {
		next = strconv.Itoa(end)
	}
	return map[string]any{"items": items[start:end], "nextCursor": next}
}

func mcpFindIssue(data domain.Bootstrap, query string) (domain.Issue, error) {
	for _, item := range data.Issues {
		if equalFoldAny(query, item.ID, item.Identifier) {
			return item, nil
		}
	}
	return domain.Issue{}, fmt.Errorf("issue %q not found", query)
}

func mcpFindTeam(data domain.Bootstrap, query string) (domain.Team, error) {
	for _, item := range data.Teams {
		if equalFoldAny(query, item.ID, item.Key, item.Name) {
			return item, nil
		}
	}
	return domain.Team{}, fmt.Errorf("team %q not found", query)
}

func mcpFindUser(data domain.Bootstrap, query string) (domain.User, error) {
	if strings.EqualFold(query, "me") {
		return data.Viewer, nil
	}
	for _, item := range data.Users {
		if equalFoldAny(query, item.ID, item.Name, item.DisplayName, item.Email) {
			return item, nil
		}
	}
	return domain.User{}, fmt.Errorf("user %q not found", query)
}

func mcpFindProject(data domain.Bootstrap, query string) (domain.Project, error) {
	for _, item := range data.Projects {
		if equalFoldAny(query, item.ID, item.Name, item.SlugID) {
			return item, nil
		}
	}
	return domain.Project{}, fmt.Errorf("project %q not found", query)
}

func mcpFindInitiative(data domain.Bootstrap, query string) (domain.Initiative, error) {
	for _, item := range data.Initiatives {
		if equalFoldAny(query, item.ID, item.Name, item.SlugID) {
			return item, nil
		}
	}
	return domain.Initiative{}, fmt.Errorf("initiative %q not found", query)
}

func mcpFindDocument(data domain.Bootstrap, query string) (domain.Document, error) {
	for _, item := range data.Documents {
		if equalFoldAny(query, item.ID, item.SlugID, item.Title) {
			return item, nil
		}
	}
	return domain.Document{}, fmt.Errorf("document %q not found", query)
}

func mcpFindReview(data domain.Bootstrap, query string) (domain.CodeReview, error) {
	for _, item := range data.Reviews {
		if equalFoldAny(query, item.ID, item.SlugID, item.ExternalID, item.URL, strconv.Itoa(item.Number)) || strings.HasSuffix(strings.TrimRight(query, "/"), "/"+item.SlugID) {
			return item, nil
		}
	}
	return domain.CodeReview{}, fmt.Errorf("diff %q not found", query)
}

func stringArg(args map[string]any, key string) string {
	value, _ := args[key].(string)
	return strings.TrimSpace(value)
}
func lowerArg(args map[string]any, key string) string   { return strings.ToLower(stringArg(args, key)) }
func boolArg(args map[string]any, key string) bool      { value, _ := args[key].(bool); return value }
func hasBoolArg(args map[string]any, key string) bool   { _, ok := args[key].(bool); return ok }
func hasNumberArg(args map[string]any, key string) bool { _, ok := args[key].(float64); return ok }
func intArg(args map[string]any, key string, fallback int) int {
	value, ok := args[key].(float64)
	if !ok {
		return fallback
	}
	return int(value)
}
func stringsArg(args map[string]any, key string) []string {
	raw, _ := args[key].([]any)
	result := make([]string, 0, len(raw))
	for _, item := range raw {
		if value, ok := item.(string); ok && strings.TrimSpace(value) != "" {
			result = append(result, strings.TrimSpace(value))
		}
	}
	return result
}
func containsFold(value, query string) bool {
	return strings.Contains(strings.ToLower(value), strings.ToLower(query))
}
func equalFoldAny(query string, values ...string) bool {
	if query == "" {
		return false
	}
	return slices.ContainsFunc(values, func(value string) bool { return strings.EqualFold(strings.TrimSpace(query), strings.TrimSpace(value)) })
}
func matchesUser(user *domain.User, viewer domain.User, query string) bool {
	if query == "" {
		return true
	}
	if user == nil {
		return strings.EqualFold(query, "null")
	}
	if strings.EqualFold(query, "me") {
		return user.ID == viewer.ID
	}
	return equalFoldAny(query, user.ID, user.Name, user.DisplayName, user.Email)
}
func matchesTeamID(data domain.Bootstrap, id, query string) bool {
	if query == "" {
		return true
	}
	team, err := mcpFindTeam(data, query)
	return err == nil && team.ID == id
}
func cycleMatches(data domain.Bootstrap, id, query string) bool {
	return slices.ContainsFunc(data.Cycles, func(item domain.Cycle) bool {
		return item.ID == id && equalFoldAny(query, item.ID, item.Name, strconv.Itoa(item.Number))
	})
}
func releasePipelineMatches(data domain.Bootstrap, id, query string) bool {
	return slices.ContainsFunc(data.ReleasePipelines, func(item domain.ReleasePipeline) bool {
		return item.ID == id && equalFoldAny(query, item.ID, item.SlugID, item.Name)
	})
}

func jsonClone[T any](value any, target *T) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, target)
}
