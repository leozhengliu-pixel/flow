package main

import (
	"encoding/json"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func TestProviderEventsPreservePrivateOwnershipAndRedactCredentials(t *testing.T) {
	data := domain.Bootstrap{Viewer: domain.User{ID: "member"}, ViewerRole: "member"}
	if realtimeEventVisible(data, domain.RealtimeEvent{Type: "agent.message_completed", ActorID: "other"}) || realtimeEventVisible(data, domain.RealtimeEvent{Type: "user_settings.updated", AggregateID: "other"}) {
		t.Fatal("private Agent/settings state was broadcast")
	}
	event := domain.RealtimeEvent{Type: "integration.job_updated", ActorID: "other", AggregateID: "run"}
	if realtimeEventVisible(data, event) {
		t.Fatal("another user's coding run was broadcast")
	}
	event.ActorID = "member"
	if !realtimeEventVisible(data, event) {
		t.Fatal("owner's coding event was hidden")
	}
	event = domain.RealtimeEvent{Type: "application_policy.updated", ActorID: "other", AggregateID: "private"}
	setApplicationPolicies(&data, []applicationPolicy{{ID: "private", OwnerID: "other", Kind: "mcp"}})
	if realtimeEventVisible(data, event) {
		t.Fatal("private connector event leaked")
	}
	data.ViewerRole = "admin"
	if !realtimeEventVisible(data, event) {
		t.Fatal("admin cannot review connector change")
	}
	raw := redactIntegrationEvent(json.RawMessage(`{"entity":{"id":"connection","oauthAccessToken":"private-value","config":{"webhookSecret":"signing-value","organization":"visible"}},"config":{"apiToken":"private-value"}}`))
	if strings.Contains(string(raw), "private-value") || strings.Contains(string(raw), "signing-value") || !strings.Contains(string(raw), "visible") {
		t.Fatalf("bad event redaction: %s", raw)
	}
}

func TestRealtimeVisibilityFiltersHiddenResources(t *testing.T) {
	data := domain.Bootstrap{
		Issues:    []domain.Issue{{ID: "visible"}},
		Documents: []domain.Document{{ID: "doc-visible"}},
	}
	values := []domain.Presence{
		{ClientID: "a", IssueID: "visible"},
		{ClientID: "b", IssueID: "hidden"},
		{ClientID: "c", DocumentID: "doc-visible"},
		{ClientID: "d", DocumentID: "doc-hidden"},
	}
	filtered := filterPresenceForViewer(data, values)
	if len(filtered) != 2 || filtered[0].ClientID != "a" || filtered[1].ClientID != "c" {
		t.Fatalf("filtered presence=%#v", filtered)
	}
	if !realtimeEventVisible(data, domain.RealtimeEvent{Type: "issue.updated", AggregateID: "visible"}) {
		t.Fatal("visible issue event was filtered")
	}
	if realtimeEventVisible(data, domain.RealtimeEvent{Type: "issue.updated", AggregateID: "hidden"}) {
		t.Fatal("hidden issue event leaked")
	}
}

func TestRealtimeVisibilityAllowsVisibleCreateEntity(t *testing.T) {
	data := domain.Bootstrap{
		Teams: []domain.Team{{ID: "team-visible"}},
	}
	payload, _ := json.Marshal(map[string]any{"entity": map[string]any{
		"id": "issue-new", "team": map[string]string{"id": "team-visible"},
	}})
	if !realtimeEventVisible(data, domain.RealtimeEvent{Type: "issue.created", AggregateID: "issue-new", Payload: payload}) {
		t.Fatal("visible issue create was filtered")
	}
	hidden, _ := json.Marshal(map[string]any{"entity": map[string]any{
		"id": "issue-hidden", "team": map[string]string{"id": "team-private"},
	}})
	if realtimeEventVisible(data, domain.RealtimeEvent{Type: "issue.created", AggregateID: "issue-hidden", Payload: hidden}) {
		t.Fatal("hidden issue create leaked")
	}
}
