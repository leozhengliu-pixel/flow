package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func agentThreadNotifications(t *testing.T, s *server, workspace, userID string) []domain.Notification {
	t.Helper()
	page, err := s.store.QueryNotifications(t.Context(), store.NotificationQuery{Workspace: workspace, UserID: userID, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	return slices.DeleteFunc(page.Notifications, func(item domain.Notification) bool { return item.Type != agentReplyNotificationType })
}

func mentionAgentInComment(t *testing.T, s *server, handler http.Handler, data domain.Bootstrap, app domain.ApplicationInstallation, issueID string) (domain.Comment, domain.AgentTask) {
	t.Helper()
	comment := requestJSON[domain.Comment](t, handler, "POST", "/api/issues/"+issueID+"/comments", map[string]any{"body": "@Agent what changed?", "bodyData": map[string]any{"type": "doc", "content": []any{map[string]any{"type": "mention", "attrs": map[string]any{"id": app.UserID, "label": "Agent"}}}}}, 201)
	tasks, err := s.store.ListAgentTasks(t.Context(), data.Workspace.URLKey, issueID, app.UserID)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("mention did not start a session: %+v %v", tasks, err)
	}
	return comment, tasks[0]
}

// An agent's replies in a thread reach the session creator and the thread's participants as one Inbox notification
// per thread, updated in place and unread again on each reply; the suggestions block never reaches the Inbox.
func TestAgentRepliesNotifyThreadOncePerThread(t *testing.T) {
	s, handler, data, app := applicationFixture(t)
	issue := requestJSON[domain.Issue](t, handler, "POST", "/api/issues", map[string]any{"title": "Inbox", "teamId": data.Teams[0].ID}, 201)
	comment, task := mentionAgentInComment(t, s, handler, data, app, issue.ID)
	other := ""
	for _, user := range data.Users {
		if user.ID != data.Viewer.ID && !user.App {
			other = user.ID
		}
	}
	if other == "" {
		t.Fatal("fixture has no second member")
	}
	// The second member takes part in the thread with a reply.
	err := s.store.MutateWorkspace(store.WithIssueRecordMutations(t.Context(), issue.ID), data.Workspace.URLKey, "comment.created", issue.ID, nil, func(next *domain.Bootstrap) error {
		next.Comments[issue.ID] = append(next.Comments[issue.ID], domain.Comment{ID: "comment_reply_other", Version: 1, Body: "+1", ParentID: &comment.ID, User: *userByID(next, other), Reactions: map[string][]string{}, CreatedAt: time.Now().UTC()})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	task, err = s.store.AppendAgentActivity(t.Context(), data.Workspace.URLKey, task.ID, task.Version, domain.AgentActivity{ActorID: app.UserID, Type: "response", Body: "**Two fields** changed: [status](https://x.test) and priority.\n\nMore detail.\n\n```suggestions\nShow the history\n```"})
	if err != nil {
		t.Fatal(err)
	}
	for _, recipient := range []string{data.Viewer.ID, other} {
		items := agentThreadNotifications(t, s, data.Workspace.URLKey, recipient)
		if len(items) != 1 {
			t.Fatalf("recipient %s: want one agent notification, got %+v", recipient, items)
		}
		item := items[0]
		if item.CommentID != comment.ID || item.IssueID != issue.ID || item.ReadAt != nil || item.Actor.ID != app.UserID || item.Category != "comments" {
			t.Fatalf("notification: %+v", item)
		}
		if item.Text != "Test agent replied: Two fields changed: status and priority." || item.Payload == nil || item.Payload.AgentActivity != "response" || strings.Contains(item.Text, "suggestions") {
			t.Fatalf("text: %q payload=%+v", item.Text, item.Payload)
		}
	}
	first := agentThreadNotifications(t, s, data.Workspace.URLKey, data.Viewer.ID)[0]
	requestJSON[any](t, handler, "PATCH", "/api/notifications/"+first.ID, map[string]any{"read": true}, 200)
	if items := agentThreadNotifications(t, s, data.Workspace.URLKey, data.Viewer.ID); items[0].ReadAt == nil {
		t.Fatalf("notification not read: %+v", items[0])
	}
	// A follow-up turns into a question: the same notification is updated and unread again.
	task, err = s.store.AppendAgentActivity(t.Context(), data.Workspace.URLKey, task.ID, task.Version, domain.AgentActivity{ActorID: data.Viewer.ID, Type: "prompt", Body: "Fix it"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.AppendAgentActivity(t.Context(), data.Workspace.URLKey, task.ID, task.Version, domain.AgentActivity{ActorID: app.UserID, Type: "elicitation", Body: "Which priority?", Signal: "select", SignalMetadata: map[string]any{"options": []any{map[string]any{"value": "High"}}}}); err != nil {
		t.Fatal(err)
	}
	items := agentThreadNotifications(t, s, data.Workspace.URLKey, data.Viewer.ID)
	if len(items) != 1 || items[0].ID != first.ID || items[0].ReadAt != nil || items[0].OccurrenceCount != 2 || items[0].Text != "Test agent is waiting for your input: Which priority?" || items[0].Payload.AgentActivity != "elicitation" {
		t.Fatalf("updated notification: %+v", items)
	}
	// Muting the thread stops them; the other participant still hears the next reply.
	requestJSON[any](t, handler, "PUT", "/api/issue-records/"+issue.ID+"/comments/"+comment.ID+"/subscription", map[string]any{"state": "muted"}, 200)
	task, err = s.store.AgentTask(t.Context(), data.Workspace.URLKey, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	task, err = s.store.AppendAgentActivity(t.Context(), data.Workspace.URLKey, task.ID, task.Version, domain.AgentActivity{ActorID: data.Viewer.ID, Type: "prompt", Body: "High"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.AppendAgentActivity(t.Context(), data.Workspace.URLKey, task.ID, task.Version, domain.AgentActivity{ActorID: app.UserID, Type: "response", Body: "Set to High."}); err != nil {
		t.Fatal(err)
	}
	if items := agentThreadNotifications(t, s, data.Workspace.URLKey, data.Viewer.ID); len(items) != 1 || items[0].OccurrenceCount != 2 {
		t.Fatalf("muted thread still notified: %+v", items)
	}
	if items := agentThreadNotifications(t, s, data.Workspace.URLKey, other); len(items) != 1 || items[0].OccurrenceCount != 3 || items[0].Text != "Test agent replied: Set to High." {
		t.Fatalf("participant: %+v", items)
	}
}

func TestAgentReplyExcerpt(t *testing.T) {
	cases := map[string]string{
		"## Summary\n- **First** item":                                  "Summary",
		"\n\n> quoted [link](https://a.test) `code`":                    "quoted link code",
		"Answer\n```suggestions\nOne\nTwo\n```":                         "Answer",
		"```suggestions\nOnly suggestions\n```":                         "",
		strings.Repeat("长", 200):                                        strings.Repeat("长", 139) + "…",
		"1. Step one\n2. Step two":                                      "Step one",
		"Done.\n\n```suggestions\nShow the history\nCompare with DEV-2": "Done.",
	}
	for body, want := range cases {
		if got := agentReplyExcerpt(body); got != want {
			t.Errorf("agentReplyExcerpt(%q) = %q, want %q", body, got, want)
		}
	}
	if got := stripAgentSuggestions("Reply\n\n```suggestions\nA\nB\n```\n"); got != "Reply" {
		t.Errorf("stripAgentSuggestions = %q", got)
	}
}

// Comment threads have no suggestion chips: only the chat prompt asks for the ```suggestions block.
func TestSuggestionsRuleIsChatOnly(t *testing.T) {
	if !strings.Contains(agentPromptRules(agentChatToolRule), "```suggestions") {
		t.Fatal("chat prompt lost the suggestions rule")
	}
	if strings.Contains(agentPromptRules(agentProgressRule), "```suggestions ...") {
		t.Fatal("thread prompt asks for suggestions")
	}
	if !strings.Contains(builtinTaskContext(domain.AgentTask{}), "Do not end it with a ```suggestions block") {
		t.Fatal("thread context does not rule out suggestions")
	}
}

// AIG: delegating an issue to an agent moves it to the team's first started status (unless it is already
// started, completed or canceled) and keeps the human assignee.
func TestDelegationMovesIssueToFirstStartedStatus(t *testing.T) {
	_, handler, data, app := applicationFixture(t)
	started := stateForTeamByType(&data, data.Teams[0].ID, "started")
	completed := stateForTeamByType(&data, data.Teams[0].ID, "completed")
	unstarted := stateForTeamByType(&data, data.Teams[0].ID, "unstarted")
	if started == nil || completed == nil || unstarted == nil {
		t.Fatal("fixture states")
	}
	issue := requestJSON[domain.Issue](t, handler, "POST", "/api/issues", map[string]any{"title": "Delegate me", "teamId": data.Teams[0].ID, "stateId": unstarted.ID, "assigneeId": data.Viewer.ID}, 201)
	updated := requestJSON[domain.Issue](t, handler, "PATCH", "/api/issue-records/"+issue.ID, map[string]any{"delegateId": app.UserID}, 200)
	if updated.State.ID != started.ID || updated.Assignee == nil || updated.Assignee.ID != data.Viewer.ID || updated.Delegate == nil || updated.Delegate.ID != app.UserID {
		t.Fatalf("delegation: state=%s assignee=%+v delegate=%+v", updated.State.Name, updated.Assignee, updated.Delegate)
	}
	// The history keeps the delegation as the person's row and credits the status move to the agent, after it.
	bootstrap := requestJSON[domain.Bootstrap](t, handler, "GET", "/api/bootstrap", nil, 200)
	var delegated, moved *domain.ActivityEvent
	for index, item := range bootstrap.Activities[issue.ID] {
		if item.Type != "issue.updated" {
			continue
		}
		if item.Metadata["delegate"] == app.UserID {
			delegated = &bootstrap.Activities[issue.ID][index]
		}
		if item.Metadata["stateId"] == started.ID {
			moved = &bootstrap.Activities[issue.ID][index]
		}
	}
	if delegated == nil || moved == nil || delegated == moved {
		t.Fatalf("delegation history: %+v", bootstrap.Activities[issue.ID])
	}
	if delegated.Actor.ID != data.Viewer.ID || delegated.Metadata["state"] != "" || delegated.Metadata[delegationStatusActorChange] != "" || updated.AgentSessionID == "" || delegated.Metadata["agentSessionId"] != updated.AgentSessionID {
		t.Fatalf("delegation row: %+v", *delegated)
	}
	if moved.Actor.ID != app.UserID || moved.Metadata["stateBefore"] != unstarted.Name || moved.Metadata["delegate"] != "" || !moved.CreatedAt.After(delegated.CreatedAt) || moved.ID == delegated.ID {
		t.Fatalf("status row: %+v (delegation %+v)", *moved, *delegated)
	}
	// Picking the agent in the assignee menu delegates the same way.
	other := requestJSON[domain.Issue](t, handler, "POST", "/api/issues", map[string]any{"title": "Assign agent", "teamId": data.Teams[0].ID, "stateId": unstarted.ID}, 201)
	updated = requestJSON[domain.Issue](t, handler, "PATCH", "/api/issue-records/"+other.ID, map[string]any{"assigneeId": app.UserID}, 200)
	if updated.State.ID != started.ID || updated.Delegate == nil || updated.Assignee == nil || updated.Assignee.ID != data.Viewer.ID {
		t.Fatalf("assignee-menu delegation: state=%s assignee=%+v delegate=%+v", updated.State.Name, updated.Assignee, updated.Delegate)
	}
	done := requestJSON[domain.Issue](t, handler, "POST", "/api/issues", map[string]any{"title": "Done", "teamId": data.Teams[0].ID, "stateId": completed.ID}, 201)
	updated = requestJSON[domain.Issue](t, handler, "PATCH", "/api/issue-records/"+done.ID, map[string]any{"delegateId": app.UserID}, 200)
	if updated.State.ID != completed.ID {
		t.Fatalf("completed issue moved to %s", updated.State.Name)
	}
	explicit := requestJSON[domain.Issue](t, handler, "POST", "/api/issues", map[string]any{"title": "Explicit", "teamId": data.Teams[0].ID, "stateId": unstarted.ID}, 201)
	updated = requestJSON[domain.Issue](t, handler, "PATCH", "/api/issue-records/"+explicit.ID, map[string]any{"delegateId": app.UserID, "stateId": unstarted.ID}, 200)
	if updated.State.ID != unstarted.ID {
		t.Fatalf("explicit status overridden: %s", updated.State.Name)
	}
}

// A delegated built-in session acknowledges at once, tells the model it was delegated the issue (with the live
// status and what it can do in Flow) and does not ask a thread for suggestion chips.
func TestBuiltinDelegationPromptsTheWorkAndAcknowledgesFirst(t *testing.T) {
	s, handler, data, _ := applicationFixture(t)
	var request string
	var acknowledged atomic.Bool
	var taskID atomic.Value
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		request = string(raw)
		if id, _ := taskID.Load().(string); id != "" {
			activities, _ := s.store.AgentActivities(r.Context(), data.Workspace.URLKey, id, "")
			acknowledged.Store(slices.ContainsFunc(activities, func(item domain.AgentActivity) bool {
				return item.Type == "action" && item.Ephemeral && item.Metadata["action"] == "get_issue"
			}))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Planned.\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer provider.Close()
	s.agent = appconfig.AgentConfig{Enabled: true, Protocol: "openai-chat-completions", BaseURL: provider.URL, APIKey: "test", Model: "test", Timeout: 5 * time.Second}
	s.agentClient = provider.Client()
	app, err := s.store.InstallApplication(t.Context(), data.Workspace.URLKey, domain.ApplicationInstallation{ClientID: "builtin-flow-agent", Name: "Flow Agent", Builtin: true, Active: true, InstalledBy: data.Viewer.ID, Scopes: []string{"read", "write", "app:assignable"}, TeamIDs: []string{data.Teams[0].ID}}, "")
	if err != nil {
		t.Fatal(err)
	}
	unstarted := stateForTeamByType(&data, data.Teams[0].ID, "unstarted")
	issue := requestJSON[domain.Issue](t, handler, "POST", "/api/issues", map[string]any{"title": "Export revamp", "teamId": data.Teams[0].ID, "stateId": unstarted.ID}, 201)
	issue = requestJSON[domain.Issue](t, handler, "PATCH", "/api/issue-records/"+issue.ID, map[string]any{"delegateId": app.UserID}, 200)
	task, err := s.store.AgentTask(t.Context(), data.Workspace.URLKey, issue.AgentSessionID)
	if err != nil {
		t.Fatal(err)
	}
	taskID.Store(task.ID)
	s.deliverApplicationTask(t.Context(), task)
	if !acknowledged.Load() {
		t.Fatal("no activity before the model ran")
	}
	var body struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(request), &body); err != nil || len(body.Messages) < 2 {
		t.Fatalf("provider request: %v %s", err, request)
	}
	system, user := body.Messages[0].Content, body.Messages[1].Content
	for _, want := range []string{"delegated issue " + issue.Identifier + " to you", "Its status is now In Progress", "get_issue", "sub-issues", "ask_user", "no access to code", "do not repeat its title"} {
		if !strings.Contains(user, want) {
			t.Errorf("delegation prompt lacks %q:\n%s", want, user)
		}
	}
	if strings.Contains(system, "fenced block tagged suggestions") || !strings.Contains(system, "comment thread") {
		t.Errorf("thread system prompt: %s", system)
	}
	activities, _ := s.store.AgentActivities(t.Context(), data.Workspace.URLKey, task.ID, "")
	if slices.ContainsFunc(activities, func(item domain.AgentActivity) bool { return item.Ephemeral }) || activities[len(activities)-1].Body != "Planned." {
		t.Fatalf("the acknowledgement was not replaced: %+v", activities)
	}
}

// Stale sessions show as "stale" on issue reads and match the agent session filter's stale value only.
func TestAgentSessionStaleStateAndFilter(t *testing.T) {
	s, handler, data, app := applicationFixture(t)
	issue := requestJSON[domain.Issue](t, handler, "POST", "/api/issues", map[string]any{"title": "Stale", "teamId": data.Teams[0].ID}, 201)
	issue = requestJSON[domain.Issue](t, handler, "PATCH", "/api/issue-records/"+issue.ID, map[string]any{"delegateId": app.UserID}, 200)
	task, err := s.store.AgentTask(t.Context(), data.Workspace.URLKey, issue.AgentSessionID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.AppendAgentActivity(t.Context(), data.Workspace.URLKey, task.ID, task.Version, domain.AgentActivity{ActorID: app.UserID, Type: "thought", Body: "Working"}); err != nil {
		t.Fatal(err)
	}
	states, err := s.store.AgentSessionStates(t.Context(), data.Workspace.URLKey, []string{task.ID})
	if err != nil || states[task.ID] != "active" {
		t.Fatalf("fresh session: %v %v", states, err)
	}
	if _, err = s.store.UpdateAgentTaskSession(t.Context(), data.Workspace.URLKey, task.ID, func(current *domain.AgentTask) error {
		old := time.Now().UTC().Add(-domain.AgentStaleAfter - time.Minute)
		current.LastActivityAt = &old
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	states, err = s.store.AgentSessionStates(t.Context(), data.Workspace.URLKey, []string{task.ID})
	if err != nil || states[task.ID] != "stale" {
		t.Fatalf("stale session: %v %v", states, err)
	}
	list := requestJSON[[]domain.AgentTask](t, handler, "GET", "/api/agent-tasks?resourceType=issue&resourceId="+issue.ID, nil, 200)
	if len(list) != 1 || list[0].Status != "stale" {
		t.Fatalf("agent tasks list: %+v", list)
	}
	for values, want := range map[string]bool{`["stale"]`: true, `["pending","active","awaitingInput"]`: false} {
		var filter []string
		_ = json.Unmarshal([]byte(values), &filter)
		page, err := s.store.QueryIssueRecords(t.Context(), store.IssueRecordQuery{Workspace: data.Workspace.URLKey, Filter: store.IssueFilter{Field: "agentSessionState", Operator: "in", Values: filter}, Limit: 50})
		if err != nil {
			t.Fatal(err)
		}
		found := slices.ContainsFunc(page.Items, func(item domain.Issue) bool { return item.ID == issue.ID })
		if found != want {
			t.Errorf("filter %s matched=%v, want %v", values, found, want)
		}
	}
}
