package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func commentActionFixture(t *testing.T) (*server, http.Handler, domain.Bootstrap, domain.Issue, domain.Comment, *atomic.Int32) {
	t.Helper()
	calls := &atomic.Int32{}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var input map[string]any
		_ = json.NewDecoder(r.Body).Decode(&input)
		tools, _ := input["tools"].([]any)
		if len(tools) != 1 {
			t.Errorf("proposal must be the only tool: %v", tools)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(chatToolCallSSE("proposal", "propose_comment_action", map[string]any{"action": "priority", "arguments": map[string]any{"priority": 1}})))
	}))
	t.Cleanup(provider.Close)
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	s := &server{store: repo, uploadPath: t.TempDir(), authDisabled: true, agent: appconfig.AgentConfig{Enabled: false, ToolsEnabled: true, WriteTools: true, BaseURL: provider.URL, Model: "scripted", MaxOutputTokens: 256}, agentClient: provider.Client()}
	h := newHandler(s)
	data := repo.Bootstrap()
	issue := requestJSON[domain.Issue](t, h, "POST", "/api/issues", domain.IssueCreateInput{Title: "Comment actions", TeamID: data.Teams[0].ID}, 201)
	comment := requestJSON[domain.Comment](t, h, "POST", "/api/issues/"+issue.ID+"/comments", domain.CommentCreateInput{Body: "Please make this urgent."}, 201)
	s.agent.Enabled = true
	return s, h, data, issue, comment, calls
}

func generateTestCommentActions(t *testing.T, s *server, data domain.Bootstrap, issue domain.Issue, comment domain.Comment) []domain.CommentAction {
	t.Helper()
	s.generateCommentActions(store.ContextWithActor(t.Context(), data.Viewer), data.Workspace.URLKey, issue.ID, comment.ID, data.Viewer)
	raw, _ := s.store.WorkspaceMetadata(data.Workspace.URLKey)
	return raw.CommentActions
}

func TestCommentActionsSettingAndGenerationGates(t *testing.T) {
	for _, gate := range []string{"off", "agent", "workspace", "writeTools", "noPermission", "app"} {
		t.Run(gate, func(t *testing.T) {
			s, h, data, issue, comment, calls := commentActionFixture(t)
			switch gate {
			case "off":
				settings := requestJSON[domain.UserSettings](t, h, "PATCH", "/api/account/settings", map[string]bool{"suggestedActionsOnComments": false}, 200)
				if settings.CommentActionsEnabled() {
					t.Fatal("setting did not persist")
				}
				got := requestJSON[domain.UserSettings](t, h, "GET", "/api/account/settings", nil, 200)
				if got.CommentActionsEnabled() {
					t.Fatal("setting read differs")
				}
			case "agent":
				s.agent.Enabled = false
			case "writeTools":
				s.agent.WriteTools = false
			case "workspace":
				if err := s.store.MutateWorkspace(t.Context(), data.Workspace.URLKey, "test.disabled", "", nil, func(d *domain.Bootstrap) error { d.WorkspaceSettings.FeatureFlags["ai"] = false; return nil }); err != nil {
					t.Fatal(err)
				}
			case "noPermission":
				s.authDisabled = false
				data.Viewer = data.Users[1]
				if data.Viewer.ID == issue.Creator.ID {
					t.Fatal("fixture needs a different actor")
				}
			case "app":
				data.Viewer.App = true
			}
			if actions := generateTestCommentActions(t, s, data, issue, comment); len(actions) != 0 {
				t.Fatalf("generated behind %s gate: %v", gate, actions)
			}
			if calls.Load() != 0 {
				t.Fatalf("provider called behind %s gate", gate)
			}
		})
	}
}

func TestCommentActionsGenerateAcceptDismissAndActivity(t *testing.T) {
	s, h, data, issue, comment, calls := commentActionFixture(t)
	actions := generateTestCommentActions(t, s, data, issue, comment)
	if len(actions) != 1 || actions[0].TargetUserID != data.Viewer.ID {
		t.Fatalf("proposals: %#v", actions)
	}
	generateTestCommentActions(t, s, data, issue, comment)
	if calls.Load() != 1 {
		t.Fatal("generation is not idempotent")
	}
	before, _ := s.store.IssueRecord(t.Context(), data.Workspace.URLKey, issue.ID)
	if before.Priority != issue.Priority {
		t.Fatal("inference mutated issue")
	}
	path := "/api/issues/" + issue.ID + "/comment-actions/" + actions[0].ID
	result := requestJSON[domain.CommentActionResult](t, h, "POST", path, domain.CommentActionResolveInput{Status: "accepted"}, 200)
	if result.Issue.Priority != 1 || result.Suggestion.Status != "accepted" || result.Suggestion.ResolvedBy != data.Viewer.ID {
		t.Fatalf("accept: %+v", result)
	}
	requestJSON[domain.CommentActionResult](t, h, "POST", path, domain.CommentActionResolveInput{Status: "accepted"}, 200)
	requestJSON[any](t, h, "POST", path, domain.CommentActionResolveInput{Status: "dismissed"}, 409)
	history, err := s.store.IssueHistoryPage(t.Context(), data.Workspace.URLKey, issue.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, event := range history.Activities {
		if event.Type == "issue.updated" && event.Metadata["priority"] == "Urgent" {
			count++
			if event.Metadata["commentId"] != comment.ID {
				t.Fatal("activity missing comment attribution")
			}
		}
	}
	if count != 1 {
		t.Fatalf("accept activity count %d", count)
	}
	if history.Comments[0].Body != comment.Body {
		t.Fatal("accept changed comment")
	}
	// A second proposal is dismissed without touching the issue.
	second := actions[0]
	second.ID = "dismiss_me"
	second.Status = "pending"
	priority := 2
	second.Arguments.Priority = &priority
	if err := s.store.MutateWorkspace(t.Context(), data.Workspace.URLKey, "test.proposal", issue.ID, nil, func(d *domain.Bootstrap) error { d.CommentActions = append(d.CommentActions, second); return nil }); err != nil {
		t.Fatal(err)
	}
	path = "/api/issues/" + issue.ID + "/comment-actions/" + second.ID
	requestJSON[domain.CommentActionResult](t, h, "POST", path, domain.CommentActionResolveInput{Status: "dismissed"}, 200)
	requestJSON[domain.CommentActionResult](t, h, "POST", path, domain.CommentActionResolveInput{Status: "dismissed"}, 200)
	listed := requestJSON[[]domain.CommentAction](t, h, "GET", "/api/issues/"+issue.ID+"/comment-actions", nil, 200)
	if len(listed) != 1 || listed[0].Status != "accepted" {
		t.Fatalf("dismiss visibility: %+v", listed)
	}
	actor := mcpActor{WorkspaceKey: data.Workspace.URLKey, User: data.Viewer, APIKey: domain.APIKey{Scopes: []string{"read", "write"}}}
	if result, err := s.callFlowTool(t.Context(), actor, "list_comment_actions", map[string]any{"issueId": issue.ID}); err != nil || result == nil {
		t.Fatalf("MCP list: %v %v", result, err)
	}
	if result, err := s.callFlowTool(t.Context(), actor, "resolve_comment_action", map[string]any{"issueId": issue.ID, "id": actions[0].ID, "status": "accepted"}); err != nil || result == nil {
		t.Fatalf("MCP idempotent resolution: %v %v", result, err)
	}
	updated, _ := s.store.IssueRecord(t.Context(), data.Workspace.URLKey, issue.ID)
	if updated.Priority != 1 {
		t.Fatal("dismiss changed issue")
	}
}

func TestCommentActionsTwoEditorsAcceptOnce(t *testing.T) {
	f := newOutcomeFixture(t)
	for _, client := range []*http.Client{f.admin, f.member} {
		authRequest[domain.UserSettings](t, client, "PATCH", f.host.URL+"/api/account/settings", map[string]bool{"agentEnabled": true, "suggestedActionsOnComments": true}, outcomeWorkspace, 200)
	}
	issue := f.home[0]
	priority := 1
	a := domain.CommentAction{ID: "race_action", IssueID: issue.ID, CommentID: "race_comment", TargetUserID: issue.Creator.ID, Status: "pending", Action: "priority", Arguments: domain.CommentActionArguments{Priority: &priority}, CreatedAt: time.Now().UTC()}
	if err := f.repository.MutateWorkspace(t.Context(), outcomeWorkspace, "test.proposal", issue.ID, nil, func(d *domain.Bootstrap) error {
		d.CommentActions = append(d.CommentActions, a)
		d.Comments[issue.ID] = append(d.Comments[issue.ID], domain.Comment{ID: a.CommentID, Body: "Make this urgent", User: issue.Creator, CreatedAt: time.Now().UTC()})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for _, client := range []*http.Client{f.admin, f.member} {
		wg.Add(1)
		go func(client *http.Client) {
			defer wg.Done()
			r, _ := http.NewRequestWithContext(context.Background(), "POST", f.host.URL+"/api/issues/"+issue.ID+"/comment-actions/"+a.ID, bytes.NewBufferString(`{"status":"accepted"}`))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-Workspace-Key", outcomeWorkspace)
			response, err := client.Do(r)
			if err != nil {
				codes <- 0
				return
			}
			defer response.Body.Close()
			codes <- response.StatusCode
		}(client)
	}
	wg.Wait()
	close(codes)
	for code := range codes {
		if code != 200 {
			t.Fatalf("concurrent accept status %d", code)
		}
	}
	history, err := f.repository.IssueHistoryPage(t.Context(), outcomeWorkspace, issue.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, event := range history.Activities {
		if event.Type == "issue.updated" && event.Metadata["priority"] == "Urgent" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("race applied %d times", count)
	}
}

func TestCommentActionValidation(t *testing.T) {
	s, _, data, issue, comment, _ := commentActionFixture(t)
	_ = s
	_ = comment
	priority := 99
	unknown := "missing"
	labels := []string{"missing"}
	for _, a := range []domain.CommentAction{
		{Action: "priority", Arguments: domain.CommentActionArguments{Priority: &priority}},
		{Action: "status", Arguments: domain.CommentActionArguments{StateID: &unknown}},
		{Action: "assignee", Arguments: domain.CommentActionArguments{AssigneeID: &unknown}},
		{Action: "labels", Arguments: domain.CommentActionArguments{LabelIDs: &labels}},
		{Action: "link", Arguments: domain.CommentActionArguments{URL: "javascript:alert(1)"}},
		{Action: "subIssue", Arguments: domain.CommentActionArguments{}},
		{Action: "deleteIssue"},
	} {
		if validateCommentAction(&data, issue, a) == nil {
			t.Fatalf("accepted invalid proposal %+v", a)
		}
	}
}

func TestCommentActionsApplySupportedMutations(t *testing.T) {
	for _, kind := range []string{"status", "assignee", "labels", "subIssue", "link"} {
		t.Run(kind, func(t *testing.T) {
			s, h, data, issue, comment, _ := commentActionFixture(t)
			a := domain.CommentAction{ID: "supported", IssueID: issue.ID, CommentID: comment.ID, CommentVersion: comment.Version, TargetUserID: data.Viewer.ID, Status: "pending", Action: kind, CreatedAt: time.Now().UTC()}
			switch kind {
			case "status":
				for _, state := range statesForTeam(&data, issue.Team.ID) {
					if state.ID != issue.State.ID {
						id := state.ID
						a.Arguments.StateID = &id
						break
					}
				}
				if a.Arguments.StateID == nil {
					t.Fatal("fixture has no alternate status")
				}
			case "assignee":
				a.Arguments.AssigneeID = &data.Viewer.ID
			case "labels":
				for _, label := range data.Labels {
					if labelsAvailableToTeam(&data, []domain.IssueLabel{label}, issue.Team.ID) {
						ids := []string{label.ID}
						a.Arguments.LabelIDs = &ids
						break
					}
				}
				if a.Arguments.LabelIDs == nil {
					t.Fatal("fixture has no applicable label")
				}
			case "subIssue":
				a.Arguments.Title = "Follow up on this comment"
				a.Arguments.Description = "Investigate the requested work."
			case "link":
				a.Arguments.URL = "https://example.test/design"
				a.Arguments.Title = "Design"
			}
			if err := s.store.MutateWorkspace(t.Context(), data.Workspace.URLKey, "test.proposal", issue.ID, nil, func(d *domain.Bootstrap) error { d.CommentActions = append(d.CommentActions, a); return nil }); err != nil {
				t.Fatal(err)
			}
			result := requestJSON[domain.CommentActionResult](t, h, "POST", "/api/issues/"+issue.ID+"/comment-actions/"+a.ID, domain.CommentActionResolveInput{Status: "accepted"}, 200)
			switch kind {
			case "status":
				if result.Issue.State.ID != *a.Arguments.StateID {
					t.Fatal("status not applied")
				}
			case "assignee":
				if result.Issue.Assignee == nil || result.Issue.Assignee.ID != data.Viewer.ID {
					t.Fatal("assignee not applied")
				}
			case "labels":
				if len(result.Issue.Labels) != 1 || result.Issue.Labels[0].ID != (*a.Arguments.LabelIDs)[0] {
					t.Fatal("labels not applied")
				}
			case "subIssue":
				child, err := s.store.IssueRecord(t.Context(), data.Workspace.URLKey, result.Suggestion.ResultIssueID)
				if err != nil || child.ParentID == nil || *child.ParentID != issue.ID || child.Title != a.Arguments.Title {
					t.Fatalf("sub-issue: %+v %v", child, err)
				}
				if len(result.Issue.SubIssueIDs) != 1 || result.Issue.SubIssueIDs[0] != child.ID {
					t.Fatal("parent not updated")
				}
			case "link":
				if len(result.Issue.Attachments) != 1 || result.Issue.Attachments[0].URL != a.Arguments.URL {
					t.Fatal("link not applied")
				}
			}
			history, err := s.store.IssueHistoryPage(t.Context(), data.Workspace.URLKey, issue.ID, "", "")
			if err != nil || len(history.Activities) < 3 {
				t.Fatalf("normal activity missing: %v", err)
			}
		})
	}
}

func TestCommentActionsRefuseEditedSourcesAndDisabledPreference(t *testing.T) {
	for _, gate := range []string{"edited", "disabled"} {
		t.Run(gate, func(t *testing.T) {
			s, h, data, issue, comment, _ := commentActionFixture(t)
			actions := generateTestCommentActions(t, s, data, issue, comment)
			if len(actions) != 1 {
				t.Fatal("expected proposal")
			}
			if gate == "edited" {
				requestJSON[domain.Comment](t, h, "PATCH", "/api/issues/"+issue.ID+"/comments/"+comment.ID, map[string]string{"body": "Never mind, keep the priority."}, 200)
			} else {
				requestJSON[domain.UserSettings](t, h, "PATCH", "/api/account/settings", map[string]bool{"suggestedActionsOnComments": false}, 200)
			}
			status := 409
			if gate == "disabled" {
				status = 403
			}
			requestJSON[any](t, h, "POST", "/api/issues/"+issue.ID+"/comment-actions/"+actions[0].ID, domain.CommentActionResolveInput{Status: "accepted"}, status)
			listed := requestJSON[[]domain.CommentAction](t, h, "GET", "/api/issues/"+issue.ID+"/comment-actions", nil, 200)
			if len(listed) != 0 {
				t.Fatal("ineligible proposal is still offered")
			}
			current, _ := s.store.IssueRecord(t.Context(), data.Workspace.URLKey, issue.ID)
			if current.Priority != issue.Priority {
				t.Fatal("ineligible action executed")
			}
		})
	}
}
