package main

import (
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestTriageRoundRobinRoutingPersists(t *testing.T) {
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	bootstrap := repository.Bootstrap()
	team := bootstrap.Teams[0]
	if err := repository.MutateWorkspace(t.Context(), bootstrap.Workspace.URLKey, "test.triage_setup", team.ID, nil, func(data *domain.Bootstrap) error {
		data.TeamMembers = []domain.TeamMember{{TeamID: team.ID, UserID: data.Users[0].ID, Role: "owner", JoinedAt: time.Now().UTC()}, {TeamID: team.ID, UserID: data.Users[1].ID, Role: "member", JoinedAt: time.Now().UTC()}}
		settings := teamSettings(data, team.ID)
		settings.TriageEnabled = true
		data.TeamSettings[team.ID] = settings
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	responsibility := requestJSON[domain.TriageResponsibility](t, handler, http.MethodPost, "/api/teams/"+team.ID+"/triage-responsibilities", map[string]any{"name": "On-call", "mode": "roundRobin", "userIds": []string{bootstrap.Users[0].ID, bootstrap.Users[1].ID}}, http.StatusCreated)
	requestJSON[domain.TriageRoutingRule](t, handler, http.MethodPost, "/api/teams/"+team.ID+"/triage-rules", map[string]any{"name": "Bugs", "conditions": map[string]string{"titleContains": "bug"}, "responsibilityId": responsibility.ID, "priority": 1, "labelIds": []string{}}, http.StatusCreated)
	first := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Bug one", "teamId": team.ID}, http.StatusCreated)
	second := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Bug two", "teamId": team.ID}, http.StatusCreated)
	if first.Assignee == nil || second.Assignee == nil || first.Assignee.ID == second.Assignee.ID || first.Priority != 1 || second.Priority != 1 {
		t.Fatalf("round-robin routing failed: first=%#v second=%#v", first.Assignee, second.Assignee)
	}
	persisted := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if len(persisted.TriageAssignments) != 2 || persisted.TriageResponsibilities[0].Cursor != 0 {
		t.Fatalf("triage state was not persisted: %#v %#v", persisted.TriageAssignments, persisted.TriageResponsibilities)
	}
}

func TestWorkflowExecutionHistoryAndRetry(t *testing.T) {
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	created := requestJSON[domain.WorkflowDefinition](t, handler, http.MethodPost, "/api/workflows", map[string]any{"name": "Daily reminder", "trigger": "schedule", "schedule": "0 9 * * *", "actions": []map[string]any{{"type": "notify", "config": map[string]string{}}}, "maxAttempts": 3}, http.StatusCreated)
	if created.NextRunAt == nil {
		t.Fatal("scheduled workflow has no next run")
	}
	run := requestJSON[domain.WorkflowRun](t, handler, http.MethodPost, "/api/workflows/"+created.ID+"/run", nil, http.StatusAccepted)
	if run.Status != "succeeded" || run.CompletedAt == nil {
		t.Fatalf("manual workflow run failed: %#v", run)
	}
	runs := requestJSON[[]domain.WorkflowRun](t, handler, http.MethodGet, "/api/workflow-runs?workflowId="+created.ID, nil, http.StatusOK)
	if len(runs) != 1 || runs[0].ID != run.ID {
		t.Fatalf("workflow history missing: %#v", runs)
	}
}

func TestEmailIntakeVerificationRotationAndIdempotency(t *testing.T) {
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	teamID := repository.Bootstrap().Teams[0].ID
	type createResponse struct {
		Address      domain.EmailIntakeAddress `json:"address"`
		InboundToken string                    `json:"inboundToken"`
		DNSRecord    map[string]string         `json:"dnsRecord"`
	}
	created := requestJSON[createResponse](t, handler, http.MethodPost, "/api/teams/"+teamID+"/email-intake-addresses", map[string]string{"localPart": "issues", "domain": "example.test"}, http.StatusCreated)
	requestJSON[domain.EmailIntakeAddress](t, handler, http.MethodPost, "/api/teams/"+teamID+"/email-intake-addresses/"+created.Address.ID+"/verify", map[string]string{"txtValue": created.DNSRecord["value"]}, http.StatusOK)
	input := map[string]any{"messageId": "mail-1", "from": "customer@example.test", "subject": "Email issue", "text": "Imported body"}
	first := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/email-intake/"+created.InboundToken+"/receive", input, http.StatusCreated)
	second := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/email-intake/"+created.InboundToken+"/receive", input, http.StatusOK)
	if first.ID != second.ID {
		t.Fatalf("duplicate email created two issues: %s %s", first.ID, second.ID)
	}
	requestJSON[any](t, handler, http.MethodPost, "/api/email-intake/"+created.InboundToken+"/receive", map[string]any{"messageId": "mail-2", "from": "customer@example.test", "subject": "Unsafe attachment", "attachments": []string{"javascript:alert(1)"}}, http.StatusBadRequest)
	type rotateResponse struct {
		Address      domain.EmailIntakeAddress `json:"address"`
		InboundToken string                    `json:"inboundToken"`
	}
	rotated := requestJSON[rotateResponse](t, handler, http.MethodPost, "/api/teams/"+teamID+"/email-intake-addresses/"+created.Address.ID+"/rotate", nil, http.StatusOK)
	if rotated.InboundToken == created.InboundToken || len(rotated.Address.Aliases) != 1 {
		t.Fatalf("address rotation failed: %#v", rotated)
	}
}

func TestPushSubscriptionOwnershipAndNotificationBulkLifecycle(t *testing.T) {
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	subscription := requestJSON[domain.PushSubscription](t, handler, http.MethodPost, "/api/push-subscriptions", map[string]string{"endpoint": "https://push.example.test/subscription", "p256dh": "public-key", "auth": "auth-secret"}, http.StatusCreated)
	listed := requestJSON[[]domain.PushSubscription](t, handler, http.MethodGet, "/api/push-subscriptions", nil, http.StatusOK)
	if len(listed) != 1 || listed[0].ID != subscription.ID {
		t.Fatalf("push subscription missing: %#v", listed)
	}
	until := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	result := requestJSON[map[string]int](t, handler, http.MethodPost, "/api/notifications/batch", map[string]any{"action": "snoozeAll", "snoozedUntil": until}, http.StatusOK)
	if result["updated"] == 0 {
		t.Fatal("snooze all changed no notifications")
	}
	requestJSON[map[string]int](t, handler, http.MethodPost, "/api/notifications/batch", map[string]any{"action": "archiveAll"}, http.StatusOK)
	requestJSON[any](t, handler, http.MethodDelete, "/api/push-subscriptions/"+subscription.ID, nil, http.StatusNoContent)
}
