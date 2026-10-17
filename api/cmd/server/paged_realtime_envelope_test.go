package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// Paged clients get the real event with the ids it affects instead of a
// payload-less workspace.resync_required, so they patch or refetch just those.
func TestPagedRealtimeEnvelopesCarryAffectedIssueIDs(t *testing.T) {
	repo := pulseTestRepo(t)
	host := httptest.NewServer(newHandler(&server{store: repo, uploadPath: t.TempDir()}))
	t.Cleanup(host.Close)
	admin := authClient(t)
	authRequest[domain.AuthSession](t, admin, "POST", host.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	stream := collectSSE(t, admin, host.URL+"/api/realtime/events")
	issueID := repo.Bootstrap().Issues[0].ID
	emit := func(eventType, aggregate string, payload any) {
		t.Helper()
		if err := repo.MutateWorkspace(t.Context(), "test-workspace", eventType, aggregate, payload, func(*domain.Bootstrap) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	teamID := repo.Bootstrap().Issues[0].Team.ID
	emit("issue.deleted", issueID, map[string]any{"id": issueID, "issueIds": []string{"child_1", issueID}, "issueAccess": []deletedIssueAccess{{ID: "child_1", TeamID: teamID}, {ID: issueID, TeamID: teamID}}})
	emit("issue.batch_updated", "issue_batch_1", map[string]any{"issueIds": []string{issueID, "missing_issue"}, "update": map[string]any{"priority": 1}})
	emit("attachment.created", issueID, map[string]string{"name": "a.png"})
	emit("issue.permissions_updated", issueID, map[string]any{"permissions": []map[string]string{{"subjectType": "user", "subjectId": "secret_user"}}})
	emit("document.permissions_updated", "document_x", map[string]any{"permissions": []string{"secret_user"}})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !stream.has("document.permissions_updated", "document_x") {
		time.Sleep(20 * time.Millisecond)
	}
	payload := func(eventType string) map[string]any {
		t.Helper()
		events := stream.find(eventType)
		if len(events) != 1 {
			t.Fatalf("%s events = %+v", eventType, stream.events)
		}
		var decoded map[string]any
		_ = json.Unmarshal(events[0].Payload, &decoded)
		return decoded
	}
	ids := func(value any) []string {
		out := []string{}
		for _, item := range value.([]any) {
			out = append(out, item.(string))
		}
		return out
	}
	if got := stream.find("workspace.resync_required"); len(got) != 0 {
		t.Fatalf("unexpected resync events: %+v", got)
	}
	deleted := payload("issue.deleted")
	if !slices.Equal(ids(deleted["issueIds"]), []string{"child_1", issueID}) || deleted["entity"] != nil {
		t.Fatalf("issue.deleted payload = %v", deleted)
	}
	batch := payload("issue.batch_updated")
	if !slices.Equal(ids(batch["issueIds"]), []string{issueID}) || !slices.Equal(ids(batch["removedIssueIds"]), []string{"missing_issue"}) || len(batch["issues"].([]any)) != 1 || batch["update"] != nil {
		t.Fatalf("issue.batch_updated payload = %v", batch)
	}
	attachment := payload("attachment.created")
	if entity, _ := attachment["entity"].(map[string]any); entity["id"] != issueID || entity["identifier"] == nil {
		t.Fatalf("attachment.created entity = %v", attachment)
	}
	permission := payload("issue.permissions_updated")
	if permission["permissions"] != nil || permission["entity"] == nil || !slices.Equal(ids(permission["issueIds"]), []string{issueID}) {
		t.Fatalf("issue.permissions_updated payload = %v", permission)
	}
	if events := stream.find("document.permissions_updated"); len(events) != 1 || len(events[0].Payload) != 0 {
		t.Fatalf("document permission event leaked its subjects: %+v", events)
	}
}

func TestPagedBatchEventCapsProjection(t *testing.T) {
	repo := pulseTestRepo(t)
	s := &server{store: repo, authDisabled: true}
	r := httptest.NewRequest("GET", "/api/realtime/events?workspace=test-workspace", nil)
	ids := make([]string, 0, maxBatchProjection+1)
	for i := 0; i <= maxBatchProjection; i++ {
		ids = append(ids, repo.Bootstrap().Issues[0].ID)
	}
	raw, _ := json.Marshal(map[string]any{"issueIds": ids})
	event, visible, err := s.pagedRealtimeEvent(r, domain.RealtimeEvent{Type: "issue.batch_updated", AggregateID: "issue_batch_2", Payload: raw})
	if err != nil || !visible {
		t.Fatalf("visible=%v err=%v", visible, err)
	}
	var payload map[string]any
	_ = json.Unmarshal(event.Payload, &payload)
	if payload["issues"] != nil || len(payload["issueIds"].([]any)) != len(ids) {
		t.Fatalf("large batch should list ids only: %v", payload)
	}
}

// A deleted issue's ids reach only viewers who could read it: the rows are
// gone by then, so the delete records the team and grants it is judged by.
func TestPagedIssueDeletedOnlyReachesViewersWhoCouldSeeIt(t *testing.T) {
	repo := pulseTestRepo(t)
	host := httptest.NewServer(newHandler(&server{store: repo, uploadPath: t.TempDir()}))
	t.Cleanup(host.Close)
	admin := authClient(t)
	authRequest[domain.AuthSession](t, admin, "POST", host.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	bootstrap := authRequest[domain.Bootstrap](t, admin, http.MethodGet, host.URL+"/api/bootstrap", nil, "test-workspace", http.StatusOK)
	publicTeam := bootstrap.Teams[0]
	member, memberUser := verifiedAuthClient(t, host.URL, "Delete member", "delete-member@example.com")
	invites := authRequest[[]domain.Invitation](t, admin, http.MethodPost, host.URL+"/api/workspaces/test-workspace/invitations", map[string]any{
		"emails": []string{memberUser.Email}, "role": "member", "teamIds": []string{publicTeam.ID},
	}, "", http.StatusCreated)
	authRequest[domain.WorkspaceMembership](t, member, http.MethodPost, host.URL+"/api/invitations/accept", map[string]string{"token": invites[0].Token}, "", http.StatusOK)
	privateTeam := authRequest[domain.Team](t, admin, http.MethodPost, host.URL+"/api/workspaces/test-workspace/teams", map[string]any{"name": "Delete private", "key": "DPRV", "private": true}, "", http.StatusCreated)
	create := func(title, teamID, parentID string) domain.Issue {
		input := map[string]any{"title": title, "teamId": teamID}
		if parentID != "" {
			input["parentId"] = parentID
		}
		return authRequest[domain.Issue](t, admin, http.MethodPost, host.URL+"/api/issues", input, "test-workspace", http.StatusCreated)
	}
	hidden := create("Hidden parent", privateTeam.ID, "")
	hiddenChild := create("Hidden child", privateTeam.ID, hidden.ID)
	shown := create("Shown issue", publicTeam.ID, "")

	adminStream := collectSSE(t, admin, host.URL+"/api/realtime/events")
	memberStream := collectSSE(t, member, host.URL+"/api/realtime/events")
	authRequest[any](t, admin, http.MethodDelete, host.URL+"/api/issues/"+hidden.ID, nil, "test-workspace", http.StatusNoContent)
	authRequest[any](t, admin, http.MethodDelete, host.URL+"/api/issues/"+shown.ID, nil, "test-workspace", http.StatusNoContent)
	deadline := time.Now().Add(5 * time.Second)
	// The two streams are consumed independently; wait for both admin events before inspecting its IDs.
	for time.Now().Before(deadline) && !(memberStream.has("issue.deleted", shown.ID) && adminStream.has("issue.deleted", hidden.ID) && adminStream.has("issue.deleted", shown.ID)) {
		time.Sleep(20 * time.Millisecond)
	}
	deletedIDs := func(stream *sseCollector) []string {
		out := []string{}
		for _, event := range stream.find("issue.deleted") {
			var payload struct {
				IssueIDs    []string `json:"issueIds"`
				IssueAccess any      `json:"issueAccess"`
			}
			_ = json.Unmarshal(event.Payload, &payload)
			if payload.IssueAccess != nil {
				t.Fatalf("issue.deleted leaked access metadata: %s", event.Payload)
			}
			out = append(out, payload.IssueIDs...)
		}
		return out
	}
	memberGot := deletedIDs(memberStream)
	if !slices.Equal(memberGot, []string{shown.ID}) {
		t.Fatalf("member got %v, want only %s (hidden %s, %s)", memberGot, shown.ID, hidden.ID, hiddenChild.ID)
	}
	if got := deletedIDs(adminStream); !slices.Contains(got, hidden.ID) || !slices.Contains(got, hiddenChild.ID) || !slices.Contains(got, shown.ID) {
		t.Fatalf("admin got %v", got)
	}
}

// Label and status renames carry the updated entity so clients patch embedded
// copies instead of refetching the bootstrap.
func TestLabelAndStateUpdatedEventsCarryEntity(t *testing.T) {
	repo := pulseTestRepo(t)
	host := httptest.NewServer(newHandler(&server{store: repo, uploadPath: t.TempDir()}))
	t.Cleanup(host.Close)
	admin := authClient(t)
	authRequest[domain.AuthSession](t, admin, "POST", host.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	bootstrap := authRequest[domain.Bootstrap](t, admin, http.MethodGet, host.URL+"/api/bootstrap", nil, "test-workspace", http.StatusOK)
	stream := collectSSE(t, admin, host.URL+"/api/realtime/events")
	label := authRequest[domain.IssueLabel](t, admin, http.MethodPost, host.URL+"/api/labels", map[string]any{"name": "Before"}, "test-workspace", http.StatusCreated)
	authRequest[domain.IssueLabel](t, admin, http.MethodPatch, host.URL+"/api/labels/"+label.ID, map[string]any{"name": "After"}, "test-workspace", http.StatusOK)
	var state domain.WorkflowState
	for _, candidate := range bootstrap.States {
		if !candidate.Reserved {
			state = candidate
			break
		}
	}
	authRequest[domain.WorkflowState](t, admin, http.MethodPatch, host.URL+"/api/teams/"+bootstrap.Teams[0].ID+"/states/"+state.ID, map[string]any{"name": "Renamed state"}, "test-workspace", http.StatusOK)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !stream.has("workflow_state.updated", state.ID) {
		time.Sleep(20 * time.Millisecond)
	}
	var labelPayload struct {
		Label domain.IssueLabel `json:"label"`
	}
	events := stream.find("label.updated")
	if len(events) != 1 || json.Unmarshal(events[0].Payload, &labelPayload) != nil || labelPayload.Label.ID != label.ID || labelPayload.Label.Name != "After" {
		t.Fatalf("label.updated events = %+v", events)
	}
	var statePayload struct {
		TeamID string               `json:"teamId"`
		State  domain.WorkflowState `json:"state"`
	}
	events = stream.find("workflow_state.updated")
	if len(events) != 1 || json.Unmarshal(events[0].Payload, &statePayload) != nil || statePayload.State.Name != "Renamed state" || statePayload.TeamID == "" {
		t.Fatalf("workflow_state.updated events = %+v", events)
	}
}
