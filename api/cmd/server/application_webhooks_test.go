package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

type applicationWebhookSettingsResponse struct {
	applicationWebhookSettings
	Secret string `json:"secret"`
}

// installableApplication registers an OAuth application with a webhook.
func installableApplication(t *testing.T, handler http.Handler, receiverURL string, types []string) (domain.OAuthApplication, string) {
	t.Helper()
	app := requestJSON[domain.OAuthApplication](t, handler, http.MethodPost, "/api/oauth-applications?workspace=test-workspace", map[string]any{
		"name": "Deploy bot", "redirectUris": []string{"https://bot.example/callback"}, "scopes": []string{"read", "write", "app:assignable", "app:mentionable"},
	}, http.StatusCreated)
	settings := requestJSON[applicationWebhookSettingsResponse](t, handler, http.MethodPut, "/api/oauth-applications/"+app.ID+"/webhook?workspace=test-workspace", map[string]any{
		"enabled": true, "url": receiverURL, "resourceTypes": types,
	}, http.StatusOK)
	if settings.Secret == "" || !settings.SigningEnabled || !settings.Enabled || settings.URL != receiverURL {
		t.Fatalf("application webhook settings: %#v", settings)
	}
	again := requestJSON[applicationWebhookSettingsResponse](t, handler, http.MethodGet, "/api/oauth-applications/"+app.ID+"/webhook?workspace=test-workspace", nil, http.StatusOK)
	if again.Secret != "" || again.SecretPrefix == "" {
		t.Fatalf("secret shown twice: %#v", again)
	}
	return app, settings.Secret
}

func managedWebhook(s *server, clientID string) (domain.Webhook, bool) {
	hooks, _ := s.store.WorkspaceMetadataFields("test-workspace", "webhooks")
	index := slices.IndexFunc(hooks.Webhooks, func(item domain.Webhook) bool {
		return item.ManagedBy == "application" && item.ApplicationClientID == clientID
	})
	if index < 0 {
		return domain.Webhook{}, false
	}
	return hooks.Webhooks[index], true
}

func TestApplicationWebhookLifecycle(t *testing.T) {
	s, handler, seed, _ := webhookTestServer(t)
	receiver := newWebhookReceiver(t, http.StatusOK)
	app, secret := installableApplication(t, handler, receiver.server.URL, []string{"Issue", "OAuthApp", "PermissionChange", "AppUserNotification"})
	if _, ok := managedWebhook(s, app.ClientID); ok {
		t.Fatal("managed webhook exists before installation")
	}
	// Installing the application creates the workspace's delivery subscription.
	installed := requestJSON[struct {
		Application domain.ApplicationInstallation `json:"application"`
	}](t, handler, http.MethodPost, "/api/application-installations?workspace=test-workspace", map[string]any{
		"clientId": app.ClientID, "teamIds": []string{seed.Teams[0].ID}, "scopes": []string{"read", "write", "app:assignable"}, "active": true,
	}, http.StatusOK).Application
	s.reconcileApplicationWebhooks(context.Background(), "test-workspace")
	hook, ok := managedWebhook(s, app.ClientID)
	if !ok || hook.URL != receiver.server.URL || !slices.Equal(hook.TeamIDs, []string{seed.Teams[0].ID}) || !slices.Contains(hook.ResourceTypes, "issues") {
		t.Fatalf("managed webhook: %#v", hook)
	}
	listed := requestJSON[[]map[string]any](t, handler, http.MethodGet, "/api/webhooks?workspace=test-workspace", nil, http.StatusOK)
	if !slices.ContainsFunc(listed, func(item map[string]any) bool { return item["managedBy"] == "application" }) {
		t.Fatalf("managed webhook hidden from admins: %#v", listed)
	}
	// Workspace admins cannot repoint or delete it, only pause it.
	requestJSON[any](t, handler, http.MethodPatch, "/api/webhooks/"+hook.ID+"?workspace=test-workspace", map[string]any{"url": "https://elsewhere.example/hook"}, http.StatusBadRequest)
	requestJSON[any](t, handler, http.MethodDelete, "/api/webhooks/"+hook.ID+"?workspace=test-workspace", nil, http.StatusBadRequest)

	// Data events are signed with the application's secret.
	issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues?workspace=test-workspace", map[string]any{"title": "For the app", "teamId": seed.Teams[0].ID}, http.StatusCreated)
	_, raw := receiver.waitFor(t, func(envelope map[string]any, _ http.Header) bool {
		data, _ := envelope["data"].(map[string]any)
		return envelope["type"] == "Issue" && data != nil && data["id"] == issue.ID
	})
	if raw.header.Get("Flow-Signature") != webhookSignature(secret, raw.body) {
		t.Fatal("application webhook not signed with the application secret")
	}

	// Delegating to the app user is an inbox notification for the app.
	requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issue-records/"+issue.ID+"?workspace=test-workspace", map[string]any{"delegateId": installed.UserID}, http.StatusOK)
	notification, _ := receiver.waitFor(t, func(envelope map[string]any, _ http.Header) bool {
		return envelope["type"] == "AppUserNotification" && envelope["action"] == "issueAssignedToYou"
	})
	if notification["appUserId"] != installed.UserID || notification["oauthClientId"] != app.ClientID {
		t.Fatalf("app user notification: %#v", notification)
	}
	requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issue-records/"+issue.ID+"?workspace=test-workspace", map[string]any{"delegateId": ""}, http.StatusOK)
	receiver.waitFor(t, func(envelope map[string]any, _ http.Header) bool {
		return envelope["type"] == "AppUserNotification" && envelope["action"] == "issueUnassignedFromYou"
	})

	// A change of team access is a PermissionChange event.
	other := requestJSON[domain.Team](t, handler, http.MethodPost, "/api/workspaces/test-workspace/teams?workspace=test-workspace", map[string]any{"name": "Platform", "key": "PLT"}, http.StatusCreated)
	requestJSON[any](t, handler, http.MethodPatch, "/api/application-installations/"+installed.ID+"?workspace=test-workspace", map[string]any{
		"teamIds": []string{seed.Teams[0].ID, other.ID}, "scopes": []string{"read", "write", "app:assignable"}, "active": true,
	}, http.StatusOK)
	s.reconcileApplicationWebhooks(context.Background(), "test-workspace")
	permission, _ := receiver.waitFor(t, func(envelope map[string]any, _ http.Header) bool { return envelope["type"] == "PermissionChange" })
	if permission["action"] != "teamAccessChanged" || !slices.Contains(toStrings(permission["addedTeamIds"]), other.ID) {
		t.Fatalf("permission change: %#v", permission)
	}

	// Uninstalling removes the subscription and sends OAuthApp revoked.
	requestJSON[any](t, handler, http.MethodPatch, "/api/application-installations/"+installed.ID+"?workspace=test-workspace", map[string]any{
		"teamIds": []string{seed.Teams[0].ID}, "scopes": []string{"read"}, "active": false,
	}, http.StatusOK)
	s.reconcileApplicationWebhooks(context.Background(), "test-workspace")
	if _, ok := managedWebhook(s, app.ClientID); ok {
		t.Fatal("managed webhook kept after uninstall")
	}
	revoked, raw := receiver.waitFor(t, func(envelope map[string]any, _ http.Header) bool { return envelope["type"] == "OAuthApp" })
	if revoked["action"] != "revoked" || revoked["oauthClientId"] != app.ClientID || revoked["organizationId"] != seed.Workspace.ID || raw.header.Get("Flow-Signature") != webhookSignature(secret, raw.body) {
		t.Fatalf("revoked event: %#v", revoked)
	}

	// Rotating the application secret signs later deliveries with it.
	rotated := requestJSON[applicationWebhookSettingsResponse](t, handler, http.MethodPost, "/api/oauth-applications/"+app.ID+"/webhook/rotate-secret?workspace=test-workspace", nil, http.StatusOK)
	if rotated.Secret == "" || rotated.Secret == secret || !strings.HasPrefix(rotated.Secret, rotated.SecretPrefix) {
		t.Fatalf("rotation: %#v", rotated)
	}
	_ = time.Second
}

func toStrings(value any) []string {
	items, _ := value.([]any)
	result := []string{}
	for _, item := range items {
		if text, ok := item.(string); ok {
			result = append(result, text)
		}
	}
	return result
}

func TestApplicationWebhookReceivesAgentSessionEventsWithPromptContext(t *testing.T) {
	s, handler, seed, _ := webhookTestServer(t)
	receiver := newWebhookReceiver(t, http.StatusOK)
	app, secret := installableApplication(t, handler, receiver.server.URL, []string{"AgentSessionEvent"})
	installed := requestJSON[struct {
		Application domain.ApplicationInstallation `json:"application"`
	}](t, handler, http.MethodPost, "/api/application-installations?workspace=test-workspace", map[string]any{
		"clientId": app.ClientID, "teamIds": []string{seed.Teams[0].ID}, "scopes": []string{"read", "write", "app:assignable", "app:mentionable"}, "active": true,
	}, http.StatusOK).Application
	s.reconcileApplicationWebhooks(context.Background(), "test-workspace")
	requestJSON[any](t, handler, http.MethodPatch, "/api/workspace/agent-guidance?workspace=test-workspace", map[string]any{"instructions": "Always open draft pull requests."}, http.StatusOK)
	issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues?workspace=test-workspace", map[string]any{"title": "Fix <login>", "description": "Steps & details", "teamId": seed.Teams[0].ID}, http.StatusCreated)
	requestJSON[domain.Comment](t, handler, http.MethodPost, "/api/issues/"+issue.ID+"/comments?workspace=test-workspace", map[string]any{"body": "Earlier context"}, http.StatusCreated)
	mention := map[string]any{"body": "Please take this", "bodyData": map[string]any{"type": "doc", "content": []any{map[string]any{"type": "mention", "attrs": map[string]any{"id": installed.UserID, "label": "Deploy bot"}}}}}
	requestJSON[domain.Comment](t, handler, http.MethodPost, "/api/issues/"+issue.ID+"/comments?workspace=test-workspace", mention, http.StatusCreated)
	tasks, err := s.store.ListAgentTasks(context.Background(), "test-workspace", issue.ID, installed.UserID)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("mention session: %v %v", tasks, err)
	}
	s.deliverApplicationTask(context.Background(), tasks[0])
	event, raw := receiver.waitFor(t, func(envelope map[string]any, _ http.Header) bool { return envelope["type"] == "AgentSessionEvent" })
	if raw.header.Get("Flow-Signature") != webhookSignature(secret, raw.body) || event["action"] != "created" || event["webhookTimestamp"] == nil {
		t.Fatalf("agent session event: %#v", event)
	}
	promptContext := event["promptContext"].(string)
	for _, fragment := range []string{`<issue identifier="` + issue.Identifier + `">`, "<title>Fix &lt;login&gt;</title>", "Steps &amp; details", "<primary-directive-thread", "Please take this", `<guidance-rule origin="workspace">Always open draft pull requests.</guidance-rule>`} {
		if !strings.Contains(promptContext, fragment) {
			t.Fatalf("promptContext missing %q:\n%s", fragment, promptContext)
		}
	}
	session, _ := event["agentSession"].(map[string]any)
	if session["issue"] == nil || session["comment"] == nil || event["appUserId"] != installed.UserID || event["organizationId"] != seed.Workspace.ID {
		t.Fatalf("agentSession: %#v", event)
	}
	if guidance, _ := event["guidance"].([]any); len(guidance) != 1 {
		t.Fatalf("guidance: %#v", event["guidance"])
	}
	if _, ok := event["previousComments"].([]any); !ok {
		t.Fatalf("previousComments: %#v", event["previousComments"])
	}
	// The delivery was logged on the managed webhook, and the session waits
	// for the agent's acknowledgement.
	hook, _ := managedWebhook(s, app.ClientID)
	waitForDelivery(t, s, "test-workspace", hook.ID, func(item domain.WebhookDelivery) bool {
		return item.EventType == "AgentSessionEvent" && item.Status == domain.WebhookDeliverySucceeded
	})
	task, err := s.store.AgentTask(context.Background(), "test-workspace", tasks[0].ID)
	if err != nil || task.DeliveredAt == nil {
		t.Fatalf("delivered at: %#v %v", task, err)
	}
	future := task
	future.DeriveTimedState(task.DeliveredAt.Add(11 * time.Second))
	if !future.Unresponsive {
		t.Fatal("session not unresponsive after 10 seconds without activity")
	}
	encoded, _ := json.Marshal(event)
	if strings.Contains(string(encoded), "webhookSecret") {
		t.Fatal("secret leaked into payload")
	}
}

func TestAgentSessionTimedStates(t *testing.T) {
	delivered := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	task := domain.AgentTask{Status: "pending", DeliveredAt: &delivered, UpdatedAt: delivered}
	early := task
	early.DeriveTimedState(delivered.Add(5 * time.Second))
	if early.Unresponsive || early.Status != "pending" {
		t.Fatalf("too early: %#v", early)
	}
	late := task
	late.DeriveTimedState(delivered.Add(11 * time.Second))
	if !late.Unresponsive {
		t.Fatal("not unresponsive")
	}
	acknowledged := task
	ack := delivered.Add(3 * time.Second)
	acknowledged.LastActivityAt, acknowledged.Status = &ack, "active"
	acknowledged.DeriveTimedState(delivered.Add(20 * time.Minute))
	if acknowledged.Unresponsive || acknowledged.Status != "active" {
		t.Fatalf("acknowledged session: %#v", acknowledged)
	}
	stale := acknowledged
	stale.DeriveTimedState(ack.Add(31 * time.Minute))
	if stale.Status != "stale" {
		t.Fatalf("not stale: %#v", stale)
	}
	recovered := acknowledged
	again := ack.Add(40 * time.Minute)
	recovered.LastActivityAt = &again
	recovered.DeriveTimedState(again.Add(time.Minute))
	if recovered.Status != "active" {
		t.Fatalf("not recovered: %#v", recovered)
	}
	done := domain.AgentTask{Status: "complete", DeliveredAt: &delivered}
	done.DeriveTimedState(delivered.Add(time.Hour))
	if done.Status != "complete" || done.Unresponsive {
		t.Fatal("finished session changed")
	}
}

func TestAgentSessionExternalURLsPlanEphemeralAndProactiveSessions(t *testing.T) {
	s, handler, data, app := applicationFixture(t)
	issue := requestJSON[domain.Issue](t, handler, "POST", "/api/issues", map[string]any{"title": "Agent work", "teamId": data.Teams[0].ID, "delegateId": app.UserID}, 201)
	other := requestJSON[domain.Issue](t, handler, "POST", "/api/issues", map[string]any{"title": "Proactive", "teamId": data.Teams[0].ID}, 201)
	comment := requestJSON[domain.Comment](t, handler, "POST", "/api/issues/"+other.ID+"/comments", map[string]any{"body": "Thread"}, 201)
	task, err := s.store.AgentTask(t.Context(), data.Workspace.URLKey, issue.AgentSessionID)
	if err != nil {
		t.Fatal(err)
	}
	// Only the session's application may update it (the dev viewer is a person).
	requestJSON[any](t, handler, "PATCH", "/api/agent-tasks/"+task.ID, map[string]any{"plan": []any{}}, http.StatusForbidden)
	requestJSON[any](t, handler, "POST", "/api/agent-tasks", map[string]any{"issueId": other.ID}, http.StatusForbidden)
	client, _ := s.store.OAuthClient(t.Context(), app.ClientID)
	if _, err := s.store.CreateOAuthAuthorizationGrant(t.Context(), "session-code", domain.OAuthAuthorizationCode{ClientID: client.ClientID, Actor: "app", InstallerID: data.Viewer.ID, UserID: data.Viewer.ID, WorkspaceKey: data.Workspace.URLKey, TeamIDs: app.TeamIDs, Scopes: []string{"read", "write", "app:assignable", "app:mentionable"}, ExpiresAt: time.Now().Add(time.Hour)}, domain.OAuthAuthorization{ID: "session-grant"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.ExchangeOAuthGrant(t.Context(), "authorization_code", "session-code", client.ClientID, "session-refresh", domain.APIKey{ID: "session-access", SecretHash: secretHash("session-token"), CreatedAt: time.Now()}, func(domain.OAuthAuthorizationCode) bool { return true }, nil); err != nil {
		t.Fatal(err)
	}
	s.authDisabled = false
	appRequest := func(method, path string, body any, want int) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(method, path, bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer session-token")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("%s %s = %d want %d: %s", method, path, rec.Code, want, rec.Body.String())
		}
		result := map[string]any{}
		_ = json.Unmarshal(rec.Body.Bytes(), &result)
		return result
	}
	updated := appRequest("PATCH", "/api/agent-tasks/"+task.ID, map[string]any{
		"addedExternalUrls": []map[string]any{{"label": "Pull request", "url": "https://git.example/pr/1"}},
		"plan":              []map[string]any{{"content": "Reproduce", "status": "completed"}, {"content": "Fix", "status": "inProgress"}},
	}, http.StatusOK)
	if urls, _ := updated["externalUrls"].([]any); len(urls) != 1 || updated["lastActivityAt"] == nil {
		t.Fatalf("external urls: %#v", updated)
	}
	if plan, _ := updated["plan"].([]any); len(plan) != 2 || updated["version"] != float64(task.Version) {
		t.Fatalf("plan or version: %#v", updated)
	}
	appRequest("PATCH", "/api/agent-tasks/"+task.ID, map[string]any{"addedExternalUrls": []map[string]any{{"url": "http://insecure.example"}}}, http.StatusBadRequest)
	appRequest("PATCH", "/api/agent-tasks/"+task.ID, map[string]any{"plan": []map[string]any{{"content": "x", "status": "unknown"}}}, http.StatusBadRequest)
	removed := appRequest("PATCH", "/api/agent-tasks/"+task.ID, map[string]any{"removedExternalUrls": []string{"https://git.example/pr/1"}}, http.StatusOK)
	if removed["externalUrls"] != nil {
		t.Fatalf("url not removed: %#v", removed)
	}
	// An ephemeral thought is replaced by the next activity.
	thought := appRequest("POST", "/api/agent-tasks/"+task.ID+"/activities", map[string]any{"expectedVersion": task.Version, "type": "thought", "body": "Searching", "ephemeral": true}, http.StatusOK)
	appRequest("POST", "/api/agent-tasks/"+task.ID+"/activities", map[string]any{"expectedVersion": thought["version"], "type": "response", "body": "Done", "ephemeral": true}, http.StatusBadRequest)
	appRequest("POST", "/api/agent-tasks/"+task.ID+"/activities", map[string]any{"expectedVersion": thought["version"], "type": "response", "body": "Done"}, http.StatusOK)
	activities, err := s.store.AgentActivities(t.Context(), data.Workspace.URLKey, task.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(activities, func(item domain.AgentActivity) bool { return item.Type == "thought" }) || !slices.ContainsFunc(activities, func(item domain.AgentActivity) bool { return item.Type == "response" }) {
		t.Fatalf("ephemeral thought kept: %#v", activities)
	}
	// An agent opens a session on an issue itself; no created event is due.
	created := appRequest("POST", "/api/agent-tasks", map[string]any{"issueId": other.ID, "externalUrls": []map[string]any{{"label": "Run", "url": "https://agent.example/run/1"}}}, http.StatusCreated)
	if created["trigger"] != "agent" || created["issueId"] != other.ID || created["status"] != "pending" {
		t.Fatalf("proactive session: %#v", created)
	}
	pending, err := s.store.PendingApplicationTasks(t.Context())
	if err != nil || slices.ContainsFunc(pending, func(item domain.AgentTask) bool { return item.ID == created["id"] }) {
		t.Fatalf("agent-initiated session queued for delivery: %v", err)
	}
	onComment := appRequest("POST", "/api/agent-tasks", map[string]any{"commentId": comment.ID}, http.StatusCreated)
	if onComment["commentId"] != comment.ID || onComment["issueId"] != other.ID {
		t.Fatalf("session on comment: %#v", onComment)
	}
	appRequest("POST", "/api/agent-tasks", map[string]any{"issueId": other.ID, "commentId": comment.ID}, http.StatusBadRequest)
}
