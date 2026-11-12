package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type internalRequesterAuthorizationKey struct{}

type internalRequesterAuthorization struct {
	service *server
	actor   mcpActor
}

func (s *server) mutateInternalConversation(r *http.Request, input any, mutate func(*domain.Bootstrap) error) error {
	ctx := r.Context()
	scoped := internalRecordsEnabled("FLOW_INTERNAL_CONVERSATION_RECORDS") && store.RecordHydrationEnabled()
	if scoped {
		ctx = store.WithMetadataRecords(ctx, store.MetadataRecordScope{Write: []store.RecordKey{{Kind: "aiConversations", ID: r.PathValue("id")}}})
	}
	err := s.store.MutateWorkspace(ctx, workspaceKey(r), "ai.conversation_updated", r.PathValue("id"), input, mutate)
	if scoped && errors.Is(err, store.ErrMetadataPageBudget) {
		// The bounded transaction rolled back. Large legal contexts retain
		// the complete legacy mutation rather than failing or truncating.
		return s.store.MutateWorkspace(r.Context(), workspaceKey(r), "ai.conversation_updated", r.PathValue("id"), input, mutate)
	}
	return err
}

// The older in-process JSON adapter has no URL. Register its actual HTTP
// routes explicitly so delegated calls still check both principals before
// the handler mutates anything.
func authorizeInternalJSONHandler(ctx context.Context, method string, values map[string]string, input any, handler http.HandlerFunc) error {
	auth, enabled := ctx.Value(internalRequesterAuthorizationKey{}).(internalRequesterAuthorization)
	if !enabled || auth.actor.AuthorizationUserID == "" {
		return nil
	}
	s := auth.service
	routes := []struct {
		handler http.HandlerFunc
		path    string
	}{
		{s.createIssueRecord, "/api/issue-records"}, {s.updateIssueRecord, "/api/issue-records/{id}"},
		{s.createProject, "/api/projects"}, {s.updateProject, "/api/projects/{id}"},
		{s.createInitiative, "/api/initiatives"}, {s.updateInitiative, "/api/initiatives/{id}"},
		{s.createProjectMilestone, "/api/projects/{id}/milestones"}, {s.updateProjectMilestone, "/api/projects/{id}/milestones/{milestoneId}"},
		{s.createProjectResource, "/api/projects/{id}/resources"},
		{s.createProjectUpdate, "/api/projects/{id}/updates"}, {s.updateProjectUpdate, "/api/projects/{id}/updates/{updateId}"},
		{s.createInitiativeUpdate, "/api/initiatives/{id}/updates"}, {s.updateInitiativeUpdate, "/api/initiatives/{id}/updates/{updateId}"},
		{s.createRelease, "/api/releases"}, {s.updateRelease, "/api/releases/{id}"},
		{s.createDraft, "/api/drafts"}, {s.updateDraft, "/api/drafts/{id}"},
		{s.createWorkspaceLabel, "/api/labels"}, {s.createLabelGroup, "/api/label-groups"},
		{s.createIssueLink, "/api/issue-records/{id}/links"}, {s.createRelation, "/api/issue-records/{id}/relations"},
		{s.deleteRelation, "/api/issue-records/{id}/relations/{relationId}"}, {s.deleteAttachment, "/api/issue-records/{id}/attachments/{attachmentId}"},
		{s.setIssueReleases, "/api/issue-records/{id}/releases"},
		{s.updateReview, "/api/reviews/{id}"}, {s.submitReview, "/api/reviews/{id}/submit"},
	}
	for _, route := range routes {
		if reflect.ValueOf(route.handler).Pointer() != reflect.ValueOf(handler).Pointer() {
			continue
		}
		path := route.path
		for name, value := range values {
			path = strings.ReplaceAll(path, "{"+name+"}", value)
		}
		raw, err := json.Marshal(input)
		if err != nil {
			return err
		}
		request, err := http.NewRequestWithContext(ctx, method, "http://flow.internal"+path, bytes.NewReader(raw))
		if err != nil {
			return err
		}
		request.Header.Set("Content-Type", "application/json")
		for name, value := range values {
			request.SetPathValue(name, value)
		}
		return s.internalAuthorizeRequester(request, auth.actor)
	}
	return fmt.Errorf("internal route requirements are not registered")
}

func (s *server) internalAuthorizeRequester(r *http.Request, actor mcpActor) error {
	if _, enabled := r.Context().Value(internalRequesterAuthorizationKey{}).(internalRequesterAuthorization); !enabled || actor.AuthorizationUserID == "" {
		return nil
	}
	user, err := s.store.UserByID(r.Context(), actor.AuthorizationUserID)
	if err != nil || !user.Active {
		return fmt.Errorf("requester access was revoked")
	}
	ctx := context.WithValue(r.Context(), authUserContextKey{}, user)
	request := r.Clone(ctx)
	if r.GetBody != nil {
		request.Body, err = r.GetBody()
		if err != nil {
			return err
		}
		defer request.Body.Close()
	} else if r.Body != nil && r.Body != http.NoBody {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			return err
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		request.Body = io.NopCloser(bytes.NewReader(raw))
	}
	response := httptest.NewRecorder()
	if !s.authorizeWorkspaceRequest(response, request, user) {
		if _, err := mcpHandlerResult(response); err != nil {
			return err
		}
		return fmt.Errorf("requester cannot perform this action")
	}
	return nil
}

func internalRecordsEnabled(flag string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(flag))) {
	case "1", "true", "on":
		return true
	}
	return false
}

// Each family keeps its established adapter when disabled. Unknown tools
// retain that adapter as well; absence of a requirement never means empty.
func internalToolRecordFamily(name string) string {
	switch name {
	case "list_projects", "get_project", "list_milestones":
		return "FLOW_MCP_PROJECT_RECORDS"
	case "get_issue", "list_issues", "count_issues", "search_issues":
		return "FLOW_MCP_ISSUE_RECORDS"
	case "list_agent_sessions", "get_agent_session", "archive_agent_session":
		return "FLOW_MCP_AGENT_RECORDS"
	case "save_issue", "save_comment":
		return "FLOW_MCP_WRITE_RECORDS"
	}
	if tool, ok := flowToolByName(name); ok && tool.Access == "write" {
		return "FLOW_MCP_WRITE_RECORDS"
	}
	return ""
}

func (s *server) internalAgentConversation(ctx context.Context, workspace, userID, ref string, bodies bool) (domain.AgentSession, error) {
	access, err := s.store.ResolveAccess(ctx, workspace, userID, nil)
	if err != nil {
		return domain.AgentSession{}, err
	}
	return s.store.AgentConversationByID(ctx, access, ref, bodies)
}

func (s *server) internalRequestConversation(r *http.Request, ref string) (domain.AgentSession, error) {
	userID := authUser(r).ID
	workspace := workspaceKey(r)
	if s.authDisabled && (userID == "" || workspace == "") {
		data, ok := s.store.WorkspaceMetadataFields(workspace, "viewer")
		if !ok {
			return domain.AgentSession{}, errNotFound
		}
		workspace = data.Workspace.URLKey
		if userID == "" {
			userID = data.Viewer.ID
		}
	}
	access, err := s.store.ResolveAccess(r.Context(), workspace, userID, nil)
	if err != nil {
		return domain.AgentSession{}, err
	}
	session, err := s.store.AgentConversationRecord(r.Context(), access, ref, false)
	if err != nil {
		return domain.AgentSession{}, err
	}
	return s.withAgentBodies(r, session), nil
}

func (s *server) internalToolActor(ctx context.Context, actor mcpActor) (mcpActor, error) {
	if actor.AuthorizationUserID == "" {
		return actor, nil
	}
	// The application and the person must both have current access. Do not
	// replace the application's attribution with the requesting person.
	application, access, err := s.store.IssueQueryAccess(ctx, actor.WorkspaceKey, actor.User.ID)
	if err != nil || !application.Viewer.Active {
		return actor, fmt.Errorf("application access was revoked")
	}
	requester, _, err := s.store.IssueQueryAccess(ctx, actor.WorkspaceKey, actor.AuthorizationUserID)
	if err != nil || !requester.Viewer.Active {
		return actor, fmt.Errorf("requester access was revoked")
	}
	teams := slices.Clone(access.VisibleTeamIDs)
	teams = slices.DeleteFunc(teams, func(id string) bool {
		return apiKeyTeamRestrictionSelected(actor.APIKey) && !slices.Contains(actor.APIKey.TeamIDs, id)
	})
	actor.APIKey.TeamRestriction, actor.APIKey.TeamIDs = "selected", teams
	return actor, nil
}

func (s *server) internalProjectTool(ctx context.Context, actor mcpActor, name string, args map[string]any) (any, bool, error) {
	if internalToolRecordFamily(name) != "FLOW_MCP_PROJECT_RECORDS" || !internalRecordsEnabled("FLOW_MCP_PROJECT_RECORDS") {
		return nil, false, nil
	}
	access, err := s.store.ResolveAccess(ctx, actor.WorkspaceKey, firstNonEmpty(actor.AuthorizationUserID, actor.User.ID), &actor.APIKey)
	if err != nil {
		return nil, true, err
	}
	// Friendly names and stale slugs keep MCP's matching precedence through
	// the compatibility adapter. Exact IDs can use the HTTP point reader.
	if name != "list_projects" {
		ref := stringArg(args, "query")
		if name == "list_milestones" {
			ref = stringArg(args, "project")
		}
		if !s.store.InternalProjectIDUnambiguous(access, ref) {
			return nil, false, nil
		}
		project, err := s.store.AuthorizedProjectRecord(ctx, access, ref)
		if err != nil || project.ID != strings.TrimSpace(ref) {
			return nil, false, nil
		}
		if name == "list_milestones" {
			return project.Milestones, true, nil
		}
		return project, true, nil
	}
	start, err := strconv.Atoi(stringArg(args, "cursor"))
	if err != nil && stringArg(args, "cursor") != "" {
		start = -1
	}
	var match func(domain.Project) bool
	if slices.ContainsFunc([]string{"query", "state", "team", "member", "initiative"}, func(key string) bool { return stringArg(args, key) != "" }) {
		// Directory filters need only these small identity collections.
		data, err := s.store.InternalProjectFilterMetadata(ctx, access)
		if errors.Is(err, store.ErrResourceAccessChanged) {
			return nil, false, nil
		}
		if err != nil {
			return nil, true, err
		}
		match = func(project domain.Project) bool {
			return len(filterProjects(data, []domain.Project{project}, args)) != 0
		}
	}
	items, next, err := s.store.InternalProjectPage(ctx, access, start, intArg(args, "limit", 50), match)
	if errors.Is(err, store.ErrResourceAccessChanged) {
		return nil, false, nil
	}
	return map[string]any{"items": items, "nextCursor": next}, true, err
}

func internalIssueToolFields(name string, args map[string]any, fields []string) []string {
	if !internalRecordsEnabled(internalToolRecordFamily(name)) {
		return fields
	}
	if internalToolRecordFamily(name) == "FLOW_MCP_ISSUE_RECORDS" {
		if stringArg(args, "project") == "" {
			fields = slices.DeleteFunc(fields, func(field string) bool { return field == "projects" })
		}
		if stringArg(args, "release") == "" {
			fields = slices.DeleteFunc(fields, func(field string) bool { return field == "releases" })
		}
	}
	return fields
}
