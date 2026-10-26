package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// callFlowTool runs a Flow tool for MCP clients and the built-in agent, and
// logs failures (they don't go through an HTTP route of their own).
func (s *server) callFlowTool(ctx context.Context, actor mcpActor, name string, args map[string]any) (any, error) {
	if _, batch := args["ids"]; batch && mcpBatchDeleteTools()[name] {
		return s.callFlowBatchDelete(ctx, actor, name, args)
	}
	ctx, release, err := s.toolCalls().acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	started := time.Now()
	result, err := s.runFlowTool(ctx, actor, name, args)
	if err != nil {
		slog.WarnContext(ctx, "flow tool failed", "tool", name, "workspace", actor.WorkspaceKey, "actor", actor.User.ID, "args", mcpArgumentSummary(args), "duration_ms", time.Since(started).Milliseconds(), "error", err.Error())
	}
	return result, err
}

// mcpArgumentSummary lists the argument names with short, single-line values
// so failures can be diagnosed without logging whole documents.
func mcpArgumentSummary(args map[string]any) string {
	keys := make([]string, 0, len(args))
	for key := range args {
		if !strings.HasPrefix(key, "__") {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		raw, _ := json.Marshal(args[key])
		value := strings.Join(strings.Fields(string(raw)), " ")
		if len(value) > 120 {
			value = value[:117] + "..."
		}
		parts = append(parts, key+"="+value)
	}
	return strings.Join(parts, " ")
}

func (s *server) runFlowTool(ctx context.Context, actor mcpActor, name string, args map[string]any) (any, error) {
	data, err := s.mcpWorkspaceData(ctx, actor)
	if err != nil {
		return nil, err
	}
	if name != "list_issues" && name != "count_issues" && name != "search_issues" && (strings.Contains(name, "issue") || strings.Contains(name, "comment") || strings.Contains(name, "attachment") || name == "save_release" || name == "create_reminder") {
		if err := s.hydrateMCPIssueArguments(ctx, actor, &data, args); err != nil {
			return nil, err
		}
	}
	if actor.AuthorizationUserID != "" && (name == "save_issue" || strings.Contains(name, "attachment") || name == "save_comment") {
		viewer, err := s.store.UserByID(ctx, actor.AuthorizationUserID)
		if err != nil || !viewer.Active {
			return nil, fmt.Errorf("requester access was revoked")
		}
		authorized := data
		authorized.Viewer = viewer
		authorized.ViewerRole, _, err = s.store.WorkspaceRole(ctx, data.Workspace.ID, viewer.ID)
		if err != nil {
			return nil, err
		}
		rank := issuePermissionRank("editor")
		if name == "save_comment" {
			rank = issuePermissionRank("commenter")
		}
		for _, field := range []string{"id", "issue", "issueId"} {
			query := stringArg(args, field)
			if query == "" {
				continue
			}
			for _, issue := range data.Issues {
				if issueAnswersTo(issue, query) && issuePermissionRank(issueRole(s, authorized, issue)) < rank {
					return nil, fmt.Errorf("requester cannot modify this issue")
				}
			}
		}
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
		if stringArg(args, "orderBy") == "createdAt" {
			// Members in the order they joined the workspace ("the first 40 members").
			joined := map[string]time.Time{}
			for _, member := range data.Members {
				joined[member.User.ID] = member.JoinedAt
			}
			slices.SortStableFunc(items, func(left, right domain.User) int { return joined[left.ID].Compare(joined[right.ID]) })
		}
		return paginate(items, args), nil
	case "get_user":
		if queries := stringsArg(args, "queries"); len(queries) > 0 {
			// Bulk lookup: every reference's outcome in one result.
			if len(queries) > mcpMaxUserQueries {
				return nil, fmt.Errorf("queries accepts at most %d people per call", mcpMaxUserQueries)
			}
			results := make([]mcpUserMatch, 0, len(queries))
			counts := map[string]int{}
			for _, query := range queries {
				match := mcpResolveUser(data, query)
				counts[match.Status]++
				results = append(results, match)
			}
			return map[string]any{"results": results, "found": counts[mcpUserFound], "notFound": counts[mcpUserNotFound], "ambiguous": counts[mcpUserAmbiguous]}, nil
		}
		if stringArg(args, "query") == "" {
			return nil, fmt.Errorf("provide query, or queries to look up several people")
		}
		return mcpFindUser(data, stringArg(args, "query"))
	case "list_issue_statuses":
		team, err := mcpFindTeam(data, stringArg(args, "team"))
		if err != nil {
			return nil, err
		}
		return statesForTeam(&data, team.ID), nil
	case "get_issue_status":
		team, err := mcpFindTeam(data, stringArg(args, "team"))
		if err != nil {
			return nil, err
		}
		id, nameArg := stringArg(args, "id"), stringArg(args, "name")
		if id == "" && nameArg == "" {
			return nil, fmt.Errorf("provide id or name")
		}
		if id != "" && nameArg != "" {
			return nil, fmt.Errorf("provide either id or name, not both")
		}
		for _, state := range statesForTeam(&data, team.ID) {
			if equalFoldAny(id, state.ID) || equalFoldAny(nameArg, state.Name) {
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
			items = slices.DeleteFunc(items, func(item domain.IssueLabel) bool { return !labelScopeIsWorkspace(item.Scope) && item.Scope != team.ID })
		}
		return paginate(items, args), nil
	case "list_issues":
		return s.pagedMCPIssues(ctx, actor, data, args)
	case "count_issues":
		return s.countMCPIssues(ctx, actor, data, args)
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
		items = slices.DeleteFunc(items, func(item domain.Document) bool {
			return !boolArg(args, "includeArchived") && item.ArchivedAt != nil || stringArg(args, "creatorId") != "" && item.Creator.ID != stringArg(args, "creatorId") || stringArg(args, "projectId") != "" && !slices.Contains(item.ProjectIDs, stringArg(args, "projectId")) || stringArg(args, "teamId") != "" && !slices.Contains(item.TeamIDs, stringArg(args, "teamId"))
		})
		if query := lowerArg(args, "query"); query != "" && len(documentSearchTerms(query)) > 0 {
			// Every keyword, anywhere in the title or body, best match first.
			matches, err := s.rankDocumentMatches(ctx, data.Workspace.URLKey, items, query, false)
			if err != nil {
				return nil, err
			}
			items = items[:0]
			for _, match := range matches {
				items = append(items, match.document)
			}
		} else if query != "" {
			items = slices.DeleteFunc(items, func(item domain.Document) bool { return !containsFold(item.Title+" "+item.Content, query) })
		}
		page := paginate(items, args)
		// Documents carry their versions whole: load the page's offloaded bodies.
		if documents, ok := page["items"].([]domain.Document); ok {
			loaded := make([]domain.Document, len(documents))
			for index, document := range documents {
				loaded[index] = s.documentWithVersions(ctx, data.Workspace.URLKey, document)
			}
			page["items"] = loaded
		}
		return page, nil
	case "search_documents":
		return s.searchMCPDocuments(ctx, data, args)
	case "get_document":
		document, err := mcpFindDocument(data, stringArg(args, "id"))
		if err != nil {
			return nil, err
		}
		return s.documentWithVersions(ctx, data.Workspace.URLKey, document), nil
	case "list_document_history":
		return s.listMCPDocumentHistory(ctx, actor, data, args)
	case "get_document_permissions":
		return s.getMCPDocumentPermissions(ctx, actor, data, args)
	case "list_comments":
		return s.listMCPComments(ctx, data, args)
	case "get_status_updates":
		return statusUpdates(data, args)
	case "list_release_pipelines":
		items := slices.Clone(data.ReleasePipelines)
		for i := range items {
			items[i] = publicReleasePipeline(items[i])
		}
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
		items = slices.DeleteFunc(items, func(item domain.ReviewEvent) bool {
			return !slices.Contains([]string{"comment", "commented"}, item.Type)
		})
		if threadID := stringArg(args, "threadId"); threadID != "" {
			root := threadID
			visited := map[string]bool{}
			for !visited[root] {
				visited[root] = true
				index := slices.IndexFunc(items, func(item domain.ReviewEvent) bool { return item.ID == root })
				if index < 0 || items[index].ParentID == "" {
					break
				}
				root = items[index].ParentID
			}
			items = slices.DeleteFunc(items, func(item domain.ReviewEvent) bool { return item.ID != root && item.ParentID != root })
		}
		if hasBoolArg(args, "resolved") {
			resolved := map[string]bool{}
			for _, item := range items {
				if item.ParentID == "" {
					resolved[item.ID] = item.Resolved
				}
			}
			items = slices.DeleteFunc(items, func(item domain.ReviewEvent) bool {
				root := item.ID
				if item.ParentID != "" {
					root = item.ParentID
				}
				return resolved[root] != boolArg(args, "resolved")
			})
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
	case "get_issue":
		return s.getMCPIssue(ctx, actor, data, args)
	case "list_issue_history":
		return s.listMCPIssueHistory(ctx, data, args)
	case "list_project_activity":
		return s.listMCPProjectActivity(ctx, data, args)
	case "search_issues":
		return s.searchMCPIssues(ctx, actor, data, args)
	case "list_notifications":
		return s.listMCPNotifications(ctx, actor, data, args)
	case "list_views":
		return listMCPViews(data, args)
	case "list_templates":
		return listMCPTemplates(data, args)
	case "list_customers":
		return s.listMCPCustomers(ctx, actor, data, args)
	case "get_customer":
		return s.getMCPCustomer(ctx, actor, data, args)
	default:
		return s.callFlowWriteTool(ctx, actor, data, name, args)
	}
}

func (s *server) mcpWorkspaceData(ctx context.Context, actor mcpActor) (domain.Bootstrap, error) {
	userID := actor.User.ID
	if actor.AuthorizationUserID != "" {
		userID = actor.AuthorizationUserID
	}
	data, err := s.store.PagedWorkspaceMetadata(ctx, actor.WorkspaceKey, userID)
	if err != nil {
		return data, fmt.Errorf("workspace access denied")
	}
	filterApplicationScopeProjection(&data, actor.APIKey)
	if actor.AuthorizationUserID != "" {
		appData, err := s.store.PagedWorkspaceMetadata(ctx, actor.WorkspaceKey, actor.User.ID)
		if err != nil {
			return data, err
		}
		data.Documents = slices.DeleteFunc(data.Documents, func(doc domain.Document) bool {
			return !slices.ContainsFunc(appData.Documents, func(other domain.Document) bool { return other.ID == doc.ID })
		})
		data.Viewer, data.ViewerRole = actor.User, "app"
	}
	if data.Viewer.ID == actor.User.ID && data.Viewer.Via == nil {
		// Records the tool writes keep the caller's attribution ("via <App>", "Flow … on behalf of").
		data.Viewer.Via = actor.User.Via
	}
	// Passkeys are account-scoped metadata. MCP callers may inspect workspace
	// resources, but must not enumerate credentials belonging to other users.
	data.Passkeys = slices.DeleteFunc(data.Passkeys, func(item domain.Passkey) bool { return item.UserID != actor.User.ID })
	for index := range data.Passkeys {
		data.Passkeys[index].CredentialJSON = ""
	}
	data.PasskeyRegistrationChallenges = nil
	if !apiKeyTeamRestrictionSelected(actor.APIKey) {
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
	data.Labels = slices.DeleteFunc(data.Labels, func(item domain.IssueLabel) bool { return !labelScopeIsWorkspace(item.Scope) && !allowed(item.Scope) })
	data.Documents = slices.DeleteFunc(data.Documents, func(item domain.Document) bool {
		return len(item.TeamIDs) > 0 && !slices.ContainsFunc(item.TeamIDs, allowed)
	})
	data.TeamMembers = slices.DeleteFunc(data.TeamMembers, func(item domain.TeamMember) bool { return !allowed(item.TeamID) })
	data.Initiatives = slices.DeleteFunc(data.Initiatives, func(item domain.Initiative) bool {
		if item.LeadTeamID != "" && !allowed(item.LeadTeamID) {
			return true
		}
		if item.LeadTeamID != "" && allowed(item.LeadTeamID) {
			return false
		}
		return !slices.ContainsFunc(item.ContributingTeamIDs, allowed)
	})
	visibleInitiatives := map[string]bool{}
	for _, item := range data.Initiatives {
		visibleInitiatives[item.ID] = true
	}
	for i := range data.Initiatives {
		data.Initiatives[i].ParentInitiativeIDs = slices.DeleteFunc(data.Initiatives[i].ParentInitiativeIDs, func(id string) bool { return !visibleInitiatives[id] })
	}
	data.InitiativeRelations = slices.DeleteFunc(data.InitiativeRelations, func(item domain.InitiativeRelation) bool {
		return !visibleInitiatives[item.InitiativeID] || !visibleInitiatives[item.RelatedInitiativeID]
	})
	// Reviews are linked to issues rather than teams directly. Keep only
	// reviews whose linked issues survived the team projection; unlinked
	// reviews cannot be safely attributed to an allowed team.
	query, err := s.mcpIssueQuery(ctx, actor)
	if err != nil {
		return data, err
	}
	var linkedIDs []string
	for _, review := range data.Reviews {
		linkedIDs = append(linkedIDs, review.IssueIDs...)
	}
	visibleIssues, err := s.store.VisibleIssueRecordIDs(ctx, query, linkedIDs)
	if err != nil {
		return data, err
	}
	data.Reviews = slices.DeleteFunc(data.Reviews, func(review domain.CodeReview) bool {
		if len(review.IssueIDs) == 0 {
			return true
		}
		return !slices.ContainsFunc(review.IssueIDs, func(id string) bool { return visibleIssues[id] })
	})
	for index := range data.Reviews {
		data.Reviews[index].IssueIDs = slices.DeleteFunc(data.Reviews[index].IssueIDs, func(id string) bool { return !visibleIssues[id] })
		data.Reviews[index].TeamReviewers = slices.DeleteFunc(data.Reviews[index].TeamReviewers, func(id string) bool { return !allowed(id) })
	}
	return data, nil
}

func (s *server) mcpIssueQuery(ctx context.Context, actor mcpActor) (store.IssueRecordQuery, error) {
	userID := actor.User.ID
	if actor.AuthorizationUserID != "" {
		userID = actor.AuthorizationUserID
	}
	data, access, err := s.store.IssueQueryAccess(ctx, actor.WorkspaceKey, userID)
	q := store.IssueRecordQuery{Workspace: data.Workspace.URLKey, Access: &access, Archived: "all"}
	if apiKeyTeamRestrictionSelected(actor.APIKey) {
		q.AllowedTeamIDs = slices.Clone(actor.APIKey.TeamIDs)
		if q.AllowedTeamIDs == nil {
			q.AllowedTeamIDs = []string{}
		}
	}
	return q, err
}

func (s *server) hydrateMCPIssueArguments(ctx context.Context, actor mcpActor, data *domain.Bootstrap, args map[string]any) error {
	q, err := s.mcpIssueQuery(ctx, actor)
	if err != nil {
		return err
	}
	values := map[string]bool{}
	budget := 1024
	var walk func(any, int)
	walk = func(v any, depth int) {
		budget--
		if budget < 0 || depth > 8 {
			return
		}
		switch x := v.(type) {
		case string:
			if len(x) <= 191 && x != "" {
				values[x] = true
			}
			// Chip text or a link names its issue by the identifier it starts with (see mcpFindIssue).
			if key := agentIssueReferenceKey(x); key != "" && len(key) <= 191 {
				values[key] = true
			}
		case []string:
			for _, v := range x {
				walk(v, depth+1)
			}
		case []any:
			for _, v := range x {
				walk(v, depth+1)
			}
		case map[string]any:
			for _, v := range x {
				walk(v, depth+1)
			}
		}
	}
	// Only resource references need hydration; prose, URLs and patch text do not.
	for _, field := range []string{"id", "issue", "issueId", "parentId", "duplicateOf", "blocks", "blockedBy", "relatedTo", "removeBlocks", "removeBlockedBy", "removeRelatedTo"} {
		walk(args[field], 0)
	}
	if budget < 0 || len(values) > 128 {
		return fmt.Errorf("too many issue references")
	}
	seen := map[string]bool{}
	for value := range values {
		issue, err := s.store.AuthorizedIssueRecord(ctx, q, value)
		if err != nil {
			resource, comment, commentErr := s.store.CommentResource(ctx, q.Workspace, value)
			if commentErr == nil {
				visible, err := s.store.VisibleIssueRecordIDs(ctx, q, []string{resource})
				if err != nil {
					return err
				}
				if visible[resource] || slices.ContainsFunc(data.Documents, func(d domain.Document) bool { return d.ID == resource }) {
					data.Comments[resource] = append(data.Comments[resource], comment)
					if visible[resource] && !seen[resource] {
						parent, err := s.store.IssueRecord(ctx, q.Workspace, resource)
						if err != nil {
							return err
						}
						data.Issues = append(data.Issues, parent)
						seen[resource] = true
					}
				}
			}
			continue
		}
		visible, err := s.store.VisibleIssueRecordIDs(ctx, q, []string{issue.ID})
		if err != nil {
			return err
		}
		if visible[issue.ID] && !seen[issue.ID] {
			seen[issue.ID] = true
			data.Issues = append(data.Issues, issue)
		}
	}
	return nil
}

func (s *server) pagedMCPIssues(ctx context.Context, actor mcpActor, data domain.Bootstrap, args map[string]any) (any, error) {
	q, err := s.mcpIssueQuery(ctx, actor)
	if err != nil {
		return nil, err
	}
	q.Cursor = stringArg(args, "cursor")
	anchor := time.Now().UTC()
	if q.Cursor != "" {
		var cursor mcpIssuePageCursor
		raw, decodeErr := base64.RawURLEncoding.DecodeString(q.Cursor)
		if decodeErr != nil || json.Unmarshal(raw, &cursor) != nil || cursor.Anchor.IsZero() || cursor.Cursor == "" {
			return nil, fmt.Errorf("invalid issue cursor")
		}
		anchor, q.Cursor = cursor.Anchor, cursor.Cursor
	}
	q.Limit = min(max(intArg(args, "limit", 50), 1), 250)
	q.Sort = stringArg(args, "orderBy")
	if q.Sort == "" {
		q.Sort = "createdAt"
	}
	q.Direction = "desc"
	q.Summary = true
	fields := stringsArg(args, "fields")
	q.IncludeDescription = len(fields) == 0 || slices.Contains(fields, "description")
	if err := s.applyMCPIssueFilters(ctx, &q, data, args, anchor); err != nil {
		return nil, err
	}
	page, err := s.store.QueryIssueRecords(ctx, q)
	if err != nil {
		return nil, err
	}
	if page.NextCursor != "" {
		raw, _ := json.Marshal(mcpIssuePageCursor{Cursor: page.NextCursor, Anchor: anchor})
		page.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	if len(fields) > 0 {
		items := make([]map[string]any, 0, len(page.Items))
		for _, issue := range page.Items {
			var raw map[string]any
			_ = jsonClone(issue, &raw)
			raw["status"], raw["statusType"] = issue.State.Name, issue.State.Type
			raw["teamId"], raw["createdBy"], raw["createdById"] = issue.Team.ID, issue.Creator, issue.Creator.ID
			if issue.Assignee != nil {
				raw["assigneeId"] = issue.Assignee.ID
			}
			if issue.Delegate != nil {
				raw["delegateId"] = issue.Delegate.ID
			}
			if issue.Project != nil {
				raw["projectId"] = issue.Project.ID
			}
			if slices.Contains(fields, "triageIntel") {
				raw["triageIntel"] = activeIssueSuggestions(&data, issue.ID)
			}
			// Always carry the human identifier (e.g. ENG-12) so agents never cite internal ids.
			projected := map[string]any{"id": issue.ID, "identifier": issue.Identifier}
			for _, field := range fields {
				switch field {
				case "url":
					projected[field] = mcpIssueURL(data.Workspace.URLKey, issue.Identifier, args)
				default:
					projected[field] = raw[field]
				}
			}
			if slices.Contains(fields, "priority") {
				// The numeric scale (0 = none, 1 = urgent … 4 = low) is easy to misread; ship the label too.
				projected["priorityLabel"] = issue.PriorityLabel
			}
			items = append(items, projected)
		}
		return map[string]any{"items": items, "nextCursor": page.NextCursor}, nil
	}
	return map[string]any{"items": page.Items, "nextCursor": page.NextCursor}, nil
}

// applyMCPIssueFilters adds list_issues' filters (team, state, assignee, project, dates…) to an issue query; dates
// relative to now (-P1D) are resolved against anchor.
func (s *server) applyMCPIssueFilters(ctx context.Context, q *store.IssueRecordQuery, data domain.Bootstrap, args map[string]any, anchor time.Time) error {
	q.SearchText = stringArg(args, "query")
	if !boolArg(args, "includeArchived") {
		q.Archived = "false"
	}
	add := func(field string, values ...string) {
		q.Filter.And = append(q.Filter.And, store.IssueFilter{Field: field, Values: values})
	}
	if value := stringArg(args, "team"); value != "" {
		team, err := mcpFindTeam(data, value)
		if err != nil {
			return err
		}
		add("team", team.ID)
	}
	if value := stringArg(args, "state"); value != "" {
		ids := []string{}
		for _, state := range data.States {
			if equalFoldAny(value, state.ID, state.Name, state.Type) {
				ids = append(ids, state.ID)
			}
		}
		add("status", ids...)
	}
	for _, field := range []string{"assignee", "delegate"} {
		value, present := nullableStringArg(args, field)
		if !present {
			continue
		}
		id := ""
		if value != "" && value != "null" {
			user, err := mcpFindUser(data, value)
			if err != nil {
				return err
			}
			id = user.ID
		}
		column := field
		if field == "delegate" {
			column = "delegateId"
		}
		add(column, id)
	}
	if value := stringArg(args, "project"); value != "" {
		project, err := mcpFindProject(data, value)
		if err != nil {
			return err
		}
		add("project", project.ID)
	}
	if value := stringArg(args, "parentId"); value != "" {
		id, err := s.store.AuthorizedIssueRecordID(ctx, *q, value)
		if err != nil {
			return err
		}
		add("parent", id)
	}
	if value := stringArg(args, "label"); value != "" {
		ids := []string{}
		for _, label := range data.Labels {
			if equalFoldAny(value, label.ID, label.Name) {
				ids = append(ids, label.ID)
			}
		}
		add("labels", ids...)
	}
	if value := stringArg(args, "cycle"); value != "" {
		ids := []string{}
		for _, cycle := range data.Cycles {
			if cycleMatches(data, cycle.ID, value) {
				ids = append(ids, cycle.ID)
			}
		}
		add("cycle", ids...)
	}
	if value := stringArg(args, "release"); value != "" {
		ids := []string{}
		for _, release := range data.Releases {
			if equalFoldAny(value, release.ID, release.SlugID) {
				ids = append(ids, release.IssueIDs...)
			}
		}
		add("id", ids...)
	}
	if hasNumberArg(args, "priority") {
		add("priority", strconv.Itoa(intArg(args, "priority", 0)))
	}
	for _, field := range []string{"createdAt", "updatedAt"} {
		if value := stringArg(args, field); value != "" {
			date, err := mcpDateAt(value, anchor)
			if err != nil {
				return err
			}
			q.Filter.And = append(q.Filter.And, store.IssueFilter{Field: field, Operator: "gte", Values: []string{date.Format(time.RFC3339Nano)}})
		}
	}
	return nil
}

// countMCPIssues counts the issues list_issues would return for the same filters (every page), so an agent
// never counts by paging through lists.
func (s *server) countMCPIssues(ctx context.Context, actor mcpActor, data domain.Bootstrap, args map[string]any) (any, error) {
	q, err := s.mcpIssueQuery(ctx, actor)
	if err != nil {
		return nil, err
	}
	if err := s.applyMCPIssueFilters(ctx, &q, data, args, time.Now().UTC()); err != nil {
		return nil, err
	}
	q.Limit, q.Summary, q.IncludeTotal = 1, true, true
	page, err := s.store.QueryIssueRecords(ctx, q)
	if err != nil {
		return nil, err
	}
	if page.Total < 0 {
		return nil, fmt.Errorf("issues could not be counted")
	}
	return map[string]any{"count": page.Total}, nil
}

func activeIssueSuggestions(data *domain.Bootstrap, issueID string) []domain.IssueSuggestion {
	result := make([]domain.IssueSuggestion, 0)
	for _, suggestion := range data.IssueSuggestions {
		if suggestion.IssueID == issueID && suggestion.State == "active" {
			result = append(result, suggestion)
		}
	}
	return result
}

var mcpDurationPattern = regexp.MustCompile(`^-P(?:(\d+)W)?(?:(\d+)D)?(?:T(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?)?$`)

type mcpIssuePageCursor struct {
	Cursor string    `json:"cursor"`
	Anchor time.Time `json:"anchor"`
}

func mcpDate(value string) (time.Time, error) {
	return mcpDateAt(value, time.Now().UTC())
}

func mcpDateAt(value string, anchor time.Time) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02"} {
		if date, err := time.Parse(layout, value); err == nil {
			return date, nil
		}
	}
	if matches := mcpDurationPattern.FindStringSubmatch(value); matches != nil && strings.Trim(value, "-PT") != "" {
		var duration time.Duration
		for i, unit := range []time.Duration{7 * 24 * time.Hour, 24 * time.Hour, time.Hour, time.Minute, time.Second} {
			if matches[i+1] == "" {
				continue
			}
			n, err := strconv.ParseInt(matches[i+1], 10, 64)
			if err != nil || n > int64((100*365*24*time.Hour-duration)/unit) {
				return time.Time{}, fmt.Errorf("date duration exceeds 100 years")
			}
			duration += time.Duration(n) * unit
		}
		return anchor.Add(-duration), nil
	}
	return time.Time{}, fmt.Errorf("invalid date %q", value)
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

func (s *server) listMCPComments(ctx context.Context, data domain.Bootstrap, args map[string]any) (any, error) {
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
	page := func(resource string) (any, error) {
		items, cursor, err := s.store.ResourceCommentsPage(ctx, data.Workspace.URLKey, resource, stringArg(args, "cursor"), intArg(args, "limit", 50))
		return map[string]any{"items": items, "nextCursor": cursor}, err
	}
	if issue, err := mcpFindIssue(data, id); err == nil {
		return page(issue.ID)
	}
	if project, err := mcpFindProject(data, id); err == nil {
		return paginate(project.Comments, args), nil
	}
	if initiative, err := mcpFindInitiative(data, id); err == nil {
		return paginate(initiative.Comments, args), nil
	}
	if document, err := mcpFindDocument(data, id); err == nil {
		items, cursor, err := s.store.ResourceCommentsPage(ctx, data.Workspace.URLKey, document.ID, stringArg(args, "cursor"), intArg(args, "limit", 50))
		if err != nil {
			return nil, err
		}
		return map[string]any{"items": mcpDocumentCommentViews(items), "nextCursor": cursor}, nil
	}
	for _, project := range data.Projects {
		for _, milestone := range project.Milestones {
			if milestone.ID == id {
				return page(id)
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

func mcpCommentParentVisible(data domain.Bootstrap, id string) bool {
	if _, err := mcpFindIssue(data, id); err == nil {
		return true
	}
	if _, err := mcpFindProject(data, id); err == nil {
		return true
	}
	if _, err := mcpFindInitiative(data, id); err == nil {
		return true
	}
	if _, err := mcpFindDocument(data, id); err == nil {
		return true
	}
	for _, project := range data.Projects {
		for _, milestone := range project.Milestones {
			if milestone.ID == id {
				return true
			}
		}
	}
	for _, update := range appendProjectAndInitiativeUpdates(data) {
		if update.ID == id {
			return true
		}
	}
	return false
}

func mcpCommentVisible(data domain.Bootstrap, id string) bool {
	if id == "" {
		return false
	}
	for parentID, comments := range data.Comments {
		if !mcpCommentParentVisible(data, parentID) {
			continue
		}
		if slices.ContainsFunc(comments, func(comment domain.Comment) bool { return comment.ID == id }) {
			return true
		}
	}
	for _, project := range data.Projects {
		if slices.ContainsFunc(project.Comments, func(comment domain.Comment) bool { return comment.ID == id }) {
			return true
		}
		for _, update := range data.ProjectUpdates[project.ID] {
			if slices.ContainsFunc(update.Comments, func(comment domain.Comment) bool { return comment.ID == id }) {
				return true
			}
		}
	}
	for _, initiative := range data.Initiatives {
		if slices.ContainsFunc(initiative.Comments, func(comment domain.Comment) bool { return comment.ID == id }) {
			return true
		}
		for _, update := range data.InitiativeUpdates[initiative.ID] {
			if slices.ContainsFunc(update.Comments, func(comment domain.Comment) bool { return comment.ID == id }) {
				return true
			}
		}
	}
	return false
}

type mcpStatusUpdate struct {
	ID         string            `json:"id"`
	Type       string            `json:"type"`
	ParentID   string            `json:"parentId"`
	ParentName string            `json:"parentName,omitempty"`
	Body       string            `json:"body"`
	Health     string            `json:"health"`
	User       domain.User       `json:"user"`
	Comments   []domain.Comment  `json:"comments"`
	Diff       *domain.PulseDiff `json:"diff,omitempty"`
	CreatedAt  time.Time         `json:"createdAt"`
	UpdatedAt  time.Time         `json:"updatedAt"`
}

// statusUpdates lists project or initiative updates with the reference app's filters:
// id, project, initiative, user, createdAt/updatedAt (after an ISO date or
// -P duration), includeArchived, and orderBy createdAt|updatedAt (newest
// first, ties broken by id so pages are stable).
func statusUpdates(data domain.Bootstrap, args map[string]any) (any, error) {
	typeName := stringArg(args, "type")
	if typeName != "project" && typeName != "initiative" {
		return nil, fmt.Errorf("type must be project or initiative")
	}
	since := func(name string) (time.Time, error) {
		value := stringArg(args, name)
		if value == "" {
			return time.Time{}, nil
		}
		date, err := mcpDate(value)
		if err != nil {
			return time.Time{}, fmt.Errorf("%s must be an ISO date or an ISO-8601 duration such as -P1D", name)
		}
		return date, nil
	}
	createdAfter, err := since("createdAt")
	if err != nil {
		return nil, err
	}
	updatedAfter, err := since("updatedAt")
	if err != nil {
		return nil, err
	}
	orderBy := stringArg(args, "orderBy")
	if orderBy == "" {
		orderBy = "createdAt"
	}
	if orderBy != "createdAt" && orderBy != "updatedAt" {
		return nil, fmt.Errorf("orderBy must be createdAt or updatedAt")
	}
	parents := map[string]string{}
	if typeName == "project" {
		if value := stringArg(args, "project"); value != "" {
			project, err := mcpFindProject(data, value)
			if err != nil {
				return nil, err
			}
			parents[project.ID] = project.Name
		} else {
			for _, project := range data.Projects {
				if project.ArchivedAt == nil || boolArg(args, "includeArchived") {
					parents[project.ID] = project.Name
				}
			}
		}
		if value := stringArg(args, "initiative"); value != "" {
			initiative, err := mcpFindInitiative(data, value)
			if err != nil {
				return nil, err
			}
			for id := range parents {
				if !slices.Contains(initiative.ProjectIDs, id) && !slices.ContainsFunc(data.Projects, func(project domain.Project) bool {
					return project.ID == id && slices.Contains(project.Initiatives, initiative.ID)
				}) {
					delete(parents, id)
				}
			}
		}
	} else {
		if stringArg(args, "project") != "" {
			return nil, fmt.Errorf("project filters project updates; use initiative for initiative updates")
		}
		if value := stringArg(args, "initiative"); value != "" {
			initiative, err := mcpFindInitiative(data, value)
			if err != nil {
				return nil, err
			}
			parents[initiative.ID] = initiative.Name
		} else {
			for _, initiative := range data.Initiatives {
				parents[initiative.ID] = initiative.Name
			}
		}
	}
	items := []mcpStatusUpdate{}
	keep := func(item mcpStatusUpdate) {
		if id := stringArg(args, "id"); id != "" && item.ID != id {
			return
		}
		if !matchesUser(&item.User, data.Viewer, stringArg(args, "user")) {
			return
		}
		if !createdAfter.IsZero() && item.CreatedAt.Before(createdAfter) || !updatedAfter.IsZero() && item.UpdatedAt.Before(updatedAfter) {
			return
		}
		items = append(items, item)
	}
	updatedAt := func(created time.Time, edited *time.Time) time.Time {
		if edited != nil && edited.After(created) {
			return *edited
		}
		return created
	}
	if typeName == "project" {
		for parentID, name := range parents {
			for _, update := range data.ProjectUpdates[parentID] {
				keep(mcpStatusUpdate{ID: update.ID, Type: "project", ParentID: parentID, ParentName: name, Body: update.Body, Health: update.Health, User: update.User, Comments: update.Comments, Diff: update.Diff, CreatedAt: update.CreatedAt, UpdatedAt: updatedAt(update.CreatedAt, update.EditedAt)})
			}
		}
	} else {
		for parentID, name := range parents {
			for _, update := range data.InitiativeUpdates[parentID] {
				keep(mcpStatusUpdate{ID: update.ID, Type: "initiative", ParentID: parentID, ParentName: name, Body: update.Body, Health: update.Health, User: update.User, Comments: update.Comments, Diff: update.Diff, CreatedAt: update.CreatedAt, UpdatedAt: updatedAt(update.CreatedAt, update.EditedAt)})
			}
		}
	}
	slices.SortFunc(items, func(a, b mcpStatusUpdate) int {
		left, right := a.CreatedAt, b.CreatedAt
		if orderBy == "updatedAt" {
			left, right = a.UpdatedAt, b.UpdatedAt
		}
		if order := right.Compare(left); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
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
	// Personal and team skills too, so skills saved with save_agent_skill are visible to the agent that saved them.
	for _, skill := range data.AgentSkills {
		if skill.UserID == data.Viewer.ID {
			items = append(items, map[string]any{"id": skill.ID, "name": skill.Name, "instructions": skill.Instructions, "scope": "personal", "createdAt": skill.CreatedAt, "updatedAt": skill.UpdatedAt})
		}
	}
	for _, team := range data.Teams {
		for _, skill := range data.TeamSettings[team.ID].AgentSkills {
			items = append(items, map[string]any{"id": skill.ID, "name": skill.Name, "instructions": skill.Instructions, "scope": "team", "teamId": team.ID, "team": team.Name, "enabled": skill.Enabled})
		}
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
		{"title": "Pulse", "url": "/docs/pulse", "content": "Pulse collects project and initiative updates into one feed (For me, Popular, Recent and custom feeds), explains why an update is shown, records what changed since the previous update, and sends daily or weekly summary notifications at 06:00 local time."},
	}
	query = strings.ToLower(strings.TrimSpace(query))
	results := slices.DeleteFunc(docs, func(item map[string]string) bool {
		return query != "" && !containsFold(item["title"]+" "+item["content"], query)
	})
	return map[string]any{"results": results, "page": max(1, page), "hasMore": false}
}

func paginate[T any](items []T, args map[string]any) map[string]any {
	start, err := strconv.Atoi(stringArg(args, "cursor"))
	if start < 0 || start > len(items) || err != nil && stringArg(args, "cursor") != "" {
		start = len(items)
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
	// Chip text or a link copied from the conversation ("DEV-42 Fix login", "[DEV-42](…/issue/DEV-42)").
	key := agentIssueReferenceKey(query)
	if key != "" {
		for _, item := range data.Issues {
			if equalFoldAny(key, item.ID, item.Identifier) {
				return item, nil
			}
		}
	}
	// An identifier the issue had before it moved to another team.
	for _, item := range data.Issues {
		if issueAnswersTo(item, query) || key != "" && issueAnswersTo(item, key) {
			return item, nil
		}
	}
	return domain.Issue{}, fmt.Errorf("issue %q not found", query)
}

// issueAnswersTo reports whether query names the issue: its id, identifier,
// or an identifier it had before it moved to another team.
func issueAnswersTo(issue domain.Issue, query string) bool {
	return equalFoldAny(query, issue.ID, issue.Identifier) || slices.ContainsFunc(issue.PreviousIdentifiers, func(identifier string) bool { return strings.EqualFold(identifier, query) })
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

// mcpUserMatch is what one person reference in a bulk request (save_team members, get_user queries) resolved to.
// Bulk requests resolve every reference and report each outcome instead of failing on the first one they cannot
// resolve, so a model adding 40 people learns in one result who was found, who is not in the workspace, and which
// names fit several people.
type mcpUserMatch struct {
	Query  string `json:"query"`
	Status string `json:"status"` // found, not_found or ambiguous
	// User is the person found; Candidates are the people an ambiguous name fits.
	User       map[string]any   `json:"user,omitempty"`
	Candidates []map[string]any `json:"candidates,omitempty"`
	found      domain.User
}

const (
	mcpUserFound     = "found"
	mcpUserNotFound  = "not_found"
	mcpUserAmbiguous = "ambiguous"
	// mcpMaxUserQueries bounds one bulk lookup.
	mcpMaxUserQueries = 250
)

// mcpResolveUser resolves one person reference: "me", a user ID, an email ("Name <email>" too), a name or display
// name (in either order, "Zhang Wei" for Wei Zhang), or an email's local part. A name several people share is
// ambiguous rather than the first match.
func mcpResolveUser(data domain.Bootstrap, query string) mcpUserMatch {
	match := mcpUserMatch{Query: query, Status: mcpUserNotFound}
	ref := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(query), "@"))
	if lt, gt := strings.LastIndex(ref, "<"), strings.LastIndex(ref, ">"); lt >= 0 && gt > lt && strings.Contains(ref[lt:gt], "@") {
		ref = strings.TrimSpace(ref[lt+1 : gt])
	}
	ref = strings.Join(strings.Fields(ref), " ")
	found := func(user domain.User) mcpUserMatch {
		match.Status, match.found, match.User = mcpUserFound, user, mcpUserRef(&user)
		return match
	}
	if ref == "" {
		return match
	}
	if strings.EqualFold(ref, "me") {
		return found(data.Viewer)
	}
	for _, user := range data.Users {
		if user.ID == ref || strings.Contains(ref, "@") && strings.EqualFold(user.Email, ref) {
			return found(user)
		}
	}
	if strings.Contains(ref, "@") {
		return match
	}
	pick := func(matches func(domain.User) bool) bool {
		hits := []domain.User{}
		for _, user := range data.Users {
			if matches(user) && !slices.ContainsFunc(hits, func(hit domain.User) bool { return hit.ID == user.ID }) {
				hits = append(hits, user)
			}
		}
		switch {
		case len(hits) == 1:
			match = found(hits[0])
		case len(hits) > 1:
			match.Status = mcpUserAmbiguous
			for index := range hits {
				match.Candidates = append(match.Candidates, mcpUserRef(&hits[index]))
			}
		}
		return len(hits) > 0
	}
	sameName := func(name string) func(domain.User) bool {
		return func(user domain.User) bool {
			return strings.EqualFold(strings.Join(strings.Fields(user.Name), " "), name) || strings.EqualFold(strings.Join(strings.Fields(user.DisplayName), " "), name)
		}
	}
	if pick(sameName(ref)) {
		return match
	}
	if words := strings.Fields(ref); len(words) == 2 && pick(sameName(words[1]+" "+words[0])) {
		return match
	}
	pick(func(user domain.User) bool {
		local, _, ok := strings.Cut(user.Email, "@")
		return ok && strings.EqualFold(local, ref)
	})
	return match
}

// mcpResolveUsers resolves bulk person references in order; the same person named twice is listed once.
func mcpResolveUsers(data domain.Bootstrap, queries []string) (found []domain.User, unresolved []mcpUserMatch) {
	for _, query := range queries {
		match := mcpResolveUser(data, query)
		if match.Status != mcpUserFound {
			unresolved = append(unresolved, match)
			continue
		}
		if !slices.ContainsFunc(found, func(user domain.User) bool { return user.ID == match.found.ID }) {
			found = append(found, match.found)
		}
	}
	return found, unresolved
}

func mcpFindProject(data domain.Bootstrap, query string) (domain.Project, error) {
	for _, item := range data.Projects {
		if equalFoldAny(query, item.ID, item.Name, item.SlugID) {
			return item, nil
		}
	}
	// A slug with a stale prefix, a legacy name-only slug, or a project URL.
	if index := domain.ProjectIndexBySlugRef(data.Projects, mcpSlugRef(query, "project")); index >= 0 {
		return data.Projects[index], nil
	}
	return domain.Project{}, fmt.Errorf("project %q not found", query)
}

func mcpFindInitiative(data domain.Bootstrap, query string) (domain.Initiative, error) {
	for _, item := range data.Initiatives {
		if equalFoldAny(query, item.ID, item.Name, item.SlugID) {
			return item, nil
		}
	}
	if index := domain.InitiativeIndexBySlugRef(data.Initiatives, mcpSlugRef(query, "initiative")); index >= 0 {
		return data.Initiatives[index], nil
	}
	return domain.Initiative{}, fmt.Errorf("initiative %q not found", query)
}

// mcpSlugRef is the slug a project / initiative URL (".../project/<slug>/...")
// names, or the query itself when it is not such a URL.
func mcpSlugRef(query, kind string) string {
	query = strings.TrimSpace(query)
	marker := "/" + kind + "/"
	index := strings.Index(query, marker)
	if index < 0 {
		return query
	}
	rest := query[index+len(marker):]
	if end := strings.IndexAny(rest, "/?#"); end >= 0 {
		rest = rest[:end]
	}
	if unescaped, err := url.PathUnescape(rest); err == nil {
		rest = unescaped
	}
	return rest
}

func mcpFindDocument(data domain.Bootstrap, query string) (domain.Document, error) {
	for _, item := range data.Documents {
		if equalFoldAny(query, item.ID, item.SlugID, item.Title) {
			return item, nil
		}
	}
	// Links made before a rename keep the document's earlier slugs.
	for _, item := range data.Documents {
		if equalFoldAny(query, item.PreviousSlugIDs...) {
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

func objectArg(args map[string]any, key string) map[string]any {
	value, _ := args[key].(map[string]any)
	return value
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
