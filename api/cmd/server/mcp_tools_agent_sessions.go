package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"time"
	"unicode/utf8"

	"flow/api/internal/domain"
)

// MCP tools for agent sessions on an issue: list_agent_sessions,
// get_agent_session and archive_agent_session. They run the routed REST
// handlers (GET /api/agent-tasks, GET /api/agent-tasks/{id}, POST
// /api/agent-tasks/{id}/archive), so the UI's access and archive rules apply
// unchanged. A session is not a comment (list_comments, delete_comment) and
// one message of it is not the session: archiving hides the whole session.

const (
	mcpAgentSessionBodyLimit     = 8000
	mcpAgentSessionMessagesLimit = 250
	mcpAgentSessionPages         = 50
)

// The tools are offered to MCP clients only (agentMCPOnlyTools): the
// in-process agent and loops act as applications and must not hide a
// person's sessions; worker runs are refused by archiveMCPAgentSession.

// mcpUserRefByID is mcpUserRef for a user ID ({id} alone for someone no
// longer in the directory).
func mcpUserRefByID(data domain.Bootstrap, id string) map[string]any {
	if id == "" {
		return nil
	}
	if index := slices.IndexFunc(data.Users, func(user domain.User) bool { return user.ID == id }); index >= 0 {
		return mcpUserRef(&data.Users[index])
	}
	return map[string]any{"id": id}
}

func mcpAgentSessionSummary(data domain.Bootstrap, task domain.AgentTask, issue *domain.Issue, args map[string]any) map[string]any {
	summary := map[string]any{
		"id": task.ID, "status": task.Status, "trigger": task.Trigger, "agent": mcpUserRefByID(data, task.AppUserID), "initiator": mcpUserRefByID(data, task.CreatorID),
		"createdAt": task.CreatedAt, "updatedAt": task.UpdatedAt, "archived": task.ArchivedAt != nil,
	}
	if prompt := []rune(task.InitialPrompt); len(prompt) > 0 {
		summary["request"] = string(prompt[:min(len(prompt), 500)])
	} else if prompt := []rune(task.Prompt); len(prompt) > 0 {
		summary["request"] = string(prompt[:min(len(prompt), 500)])
	}
	if task.CommentID != "" {
		summary["commentId"] = task.CommentID
	}
	if task.CodingSessionID != "" {
		summary["codingSessionId"] = task.CodingSessionID
	}
	if task.ArchivedAt != nil {
		summary["archivedAt"], summary["archivedBy"] = task.ArchivedAt, mcpUserRefByID(data, task.ArchivedBy)
	}
	if task.IssueID != "" {
		summary["issueId"] = task.IssueID
	}
	if issue != nil {
		summary["issueIdentifier"] = issue.Identifier
		if task.ArchivedAt == nil {
			summary["url"] = mcpIssueURL(data.Workspace.URLKey, issue.Identifier, args) + "#agent-session-" + url.PathEscape(task.ID)
		}
	}
	return summary
}

func (s *server) mcpAgentSessionIssue(ctx context.Context, actor mcpActor, data *domain.Bootstrap, query string) (domain.Issue, error) {
	if query == "" {
		return domain.Issue{}, fmt.Errorf("issueId is required")
	}
	if err := s.hydrateMCPIssueArguments(ctx, actor, data, map[string]any{"issueId": query}); err != nil {
		return domain.Issue{}, err
	}
	return mcpFindIssue(*data, query)
}

func (s *server) listMCPAgentSessions(ctx context.Context, actor mcpActor, data domain.Bootstrap, args map[string]any) (any, error) {
	issue, err := s.mcpAgentSessionIssue(ctx, actor, &data, stringArg(args, "issueId"))
	if err != nil {
		return nil, err
	}
	path := "/api/agent-tasks?resourceType=issue&resourceId=" + url.QueryEscape(issue.ID)
	if boolArg(args, "includeArchived") {
		path += "&includeArchived=true"
	}
	raw, err := s.invokeMCPRoute(ctx, actor, http.MethodGet, path, nil, nil, s.listApplicationAgentTasks)
	if err != nil {
		return nil, err
	}
	var tasks []domain.AgentTask
	if err := jsonClone(raw, &tasks); err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(tasks))
	for _, task := range tasks {
		items = append(items, mcpAgentSessionSummary(data, task, &issue, args))
	}
	return map[string]any{"issue": map[string]any{"id": issue.ID, "identifier": issue.Identifier, "title": issue.Title}, "items": items}, nil
}

func (s *server) mcpAgentSession(ctx context.Context, actor mcpActor, id string, after string) (domain.AgentTask, []domain.AgentActivity, error) {
	raw, err := s.invokeMCPRoute(ctx, actor, http.MethodGet, "/api/agent-tasks/"+url.PathEscape(id)+"?after="+url.QueryEscape(after), map[string]string{"id": id}, nil, s.getApplicationAgentTask)
	if err != nil {
		return domain.AgentTask{}, nil, err
	}
	var page struct {
		Session    domain.AgentTask       `json:"session"`
		Activities []domain.AgentActivity `json:"activities"`
	}
	if err := jsonClone(raw, &page); err != nil {
		return domain.AgentTask{}, nil, err
	}
	return page.Session, page.Activities, nil
}

func (s *server) getMCPAgentSession(ctx context.Context, actor mcpActor, data domain.Bootstrap, args map[string]any) (any, error) {
	id := stringArg(args, "id")
	if id == "" {
		return nil, fmt.Errorf("id is required")
	}
	task, activities, err := s.mcpAgentSession(ctx, actor, id, "")
	if err != nil {
		return nil, err
	}
	for page := 1; len(activities) > 0 && len(activities)%100 == 0 && page < mcpAgentSessionPages; page++ {
		_, next, err := s.mcpAgentSession(ctx, actor, id, activities[len(activities)-1].ID)
		if err != nil {
			return nil, err
		}
		if len(next) == 0 {
			break
		}
		activities = append(activities, next...)
	}
	deleted := map[string]bool{}
	for _, activity := range activities {
		if target, _ := activity.Metadata["activityId"].(string); activity.Type == "deleted" && target != "" {
			deleted[target] = true
		}
	}
	steps := boolArg(args, "includeSteps")
	cursor := stringArg(args, "cursor")
	limit := min(max(intArg(args, "limit", 100), 1), mcpAgentSessionMessagesLimit)
	messages := []map[string]any{}
	nextCursor := ""
	for _, activity := range activities {
		switch activity.Type {
		case "deleted", "feedback":
			continue // markers, not messages
		case "thought", "action", "output":
			if !steps {
				continue
			}
		}
		if cursor != "" && activity.ID <= cursor {
			continue
		}
		if len(messages) == limit {
			nextCursor = messages[len(messages)-1]["id"].(string)
			break
		}
		body := activity.Body
		message := map[string]any{"id": activity.ID, "type": activity.Type, "author": mcpUserRefByID(data, activity.ActorID), "createdAt": activity.CreatedAt}
		if utf8.RuneCountInString(body) > mcpAgentSessionBodyLimit {
			body, message["truncated"] = string([]rune(body)[:mcpAgentSessionBodyLimit]), true
		}
		message["body"] = body
		if activity.URL != "" {
			message["url"] = activity.URL
		}
		if deleted[activity.ID] {
			message["deleted"] = true
		}
		if reason, _ := activity.Metadata["reason"].(string); reason != "" {
			message["reason"] = reason
		}
		messages = append(messages, message)
	}
	var issue *domain.Issue
	if task.IssueID != "" {
		if found, err := s.mcpAgentSessionIssue(ctx, actor, &data, task.IssueID); err == nil {
			issue = &found
		}
	}
	result := map[string]any{"session": mcpAgentSessionSummary(data, task, issue, args), "messages": messages}
	if nextCursor != "" {
		result["nextCursor"] = nextCursor
	}
	return result, nil
}

func (s *server) archiveMCPAgentSession(ctx context.Context, actor mcpActor, data domain.Bootstrap, args map[string]any) (any, error) {
	id := stringArg(args, "id")
	if id == "" {
		return nil, fmt.Errorf("id is required")
	}
	if actor.Run != nil {
		// An agent run acts for a principal; it must not hide their sessions.
		return nil, fmt.Errorf("agent runs cannot archive agent sessions; ask the person who started the session or a workspace admin")
	}
	raw, err := s.invokeMCPRoute(ctx, actor, http.MethodPost, "/api/agent-tasks/"+url.PathEscape(id)+"/archive", map[string]string{"id": id}, map[string]any{}, s.archiveApplicationAgentTask)
	if err != nil {
		return nil, err
	}
	var response agentSessionArchiveResponse
	if err := jsonClone(raw, &response); err != nil {
		return nil, err
	}
	var issue *domain.Issue
	if response.Session.IssueID != "" {
		if found, err := s.mcpAgentSessionIssue(ctx, actor, &data, response.Session.IssueID); err == nil {
			issue = &found
		}
	}
	receipt := map[string]any{
		"archived": true, "alreadyArchived": response.AlreadyArchived, "stopped": response.Stopped, "previousStatus": response.PreviousStatus,
		"session": mcpAgentSessionSummary(data, response.Session, issue, args),
		"note":    "The session and its messages are hidden from the issue's activity. Its log, audit entries, coding session, documents, attachments and linked issues are kept.",
	}
	if response.AlreadyArchived {
		receipt["note"] = "The session was already archived; nothing changed."
	}
	if response.Session.ArchivedAt != nil {
		receipt["archivedAt"] = response.Session.ArchivedAt.UTC().Format(time.RFC3339)
	}
	return receipt, nil
}
