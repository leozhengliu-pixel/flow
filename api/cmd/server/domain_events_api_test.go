package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// GET /api/events serves the events one workspace recorded for an aggregate to
// that workspace's administrators, a page at a time. Aggregate IDs repeat across
// workspaces (each numbers its issues from issue_1), so the aggregate alone
// must not expose another workspace's events.
func TestEventsAreScopedPagedAndAdminOnly(t *testing.T) {
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	api := httptest.NewServer(newHandler(&server{store: repository, uploadPath: t.TempDir()}))
	defer api.Close()

	admin := authClient(t)
	authRequest[domain.AuthSession](t, admin, http.MethodPost, api.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	bootstrap := authRequest[domain.Bootstrap](t, admin, http.MethodGet, api.URL+"/api/bootstrap", nil, "test-workspace", http.StatusOK)
	register := func(name, email string) *http.Client {
		client := authClient(t)
		registered := authRequest[struct {
			VerificationToken string `json:"verificationToken"`
		}](t, client, http.MethodPost, api.URL+"/api/auth/register", map[string]string{"name": name, "email": email, "password": "initial-pass"}, "", http.StatusCreated)
		authRequest[any](t, client, http.MethodPost, api.URL+"/api/auth/verify-email", map[string]string{"token": registered.VerificationToken}, "", http.StatusOK)
		authRequest[domain.AuthSession](t, client, http.MethodPost, api.URL+"/api/auth/login", map[string]string{"email": email, "password": "initial-pass"}, "", http.StatusOK)
		return client
	}
	member := register("Member", "member@example.com")
	invites := authRequest[[]domain.Invitation](t, admin, http.MethodPost, api.URL+"/api/workspaces/test-workspace/invitations", map[string]any{"emails": []string{"member@example.com"}, "role": "member", "teamIds": []string{bootstrap.Teams[0].ID}}, "", http.StatusCreated)
	authRequest[domain.WorkspaceMembership](t, member, http.MethodPost, api.URL+"/api/invitations/accept", map[string]string{"token": invites[0].Token}, "", http.StatusOK)
	outsider := register("Outsider", "outsider@example.com")
	authRequest[domain.Bootstrap](t, outsider, http.MethodPost, api.URL+"/api/workspaces", map[string]string{"name": "Outside", "urlKey": "outside"}, "", http.StatusCreated)
	outside := authRequest[domain.Bootstrap](t, outsider, http.MethodGet, api.URL+"/api/bootstrap", nil, "outside", http.StatusOK)
	foreign := authRequest[domain.Issue](t, outsider, http.MethodPost, api.URL+"/api/issues", map[string]any{"title": "Outside plans", "teamId": outside.Teams[0].ID}, "outside", http.StatusCreated)

	issue := authRequest[domain.Issue](t, admin, http.MethodPost, api.URL+"/api/issues", map[string]any{"title": "Event history", "teamId": bootstrap.Teams[0].ID}, "test-workspace", http.StatusCreated)
	for _, title := range []string{"Second", "Third", "Fourth", "Fifth"} {
		authRequest[domain.Issue](t, admin, http.MethodPatch, api.URL+"/api/issue-records/"+issue.ID, map[string]any{"title": title}, "test-workspace", http.StatusOK)
	}
	stored, err := repository.Events(t.Context(), issue.ID)
	if err != nil || len(stored) < 5 {
		t.Fatalf("stored issue events = %d, %v", len(stored), err)
	}

	getIn := func(workspace string, client *http.Client, query url.Values, want int) ([]domain.DomainEvent, string) {
		t.Helper()
		request, _ := http.NewRequest(http.MethodGet, api.URL+"/api/events?"+query.Encode(), nil)
		request.Header.Set("X-Workspace-Key", workspace)
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, _ := io.ReadAll(response.Body)
		if response.StatusCode != want {
			t.Fatalf("GET /api/events?%s status %d, want %d: %s", query.Encode(), response.StatusCode, want, raw)
		}
		var events []domain.DomainEvent
		if want == http.StatusOK {
			if err := json.Unmarshal(raw, &events); err != nil {
				t.Fatalf("events response %s: %v", raw, err)
			}
		}
		return events, response.Header.Get("X-Next-Cursor")
	}
	get := func(client *http.Client, query url.Values, want int) ([]domain.DomainEvent, string) {
		t.Helper()
		return getIn("test-workspace", client, query, want)
	}
	hasEvent := func(events []domain.DomainEvent, id string) bool {
		return slices.ContainsFunc(events, func(event domain.DomainEvent) bool { return event.ID == id })
	}

	get(admin, url.Values{}, http.StatusBadRequest)
	foreignEvents, err := repository.Events(t.Context(), foreign.ID)
	if err != nil {
		t.Fatal(err)
	}
	created := ""
	for _, event := range foreignEvents {
		if event.Type == "issue.created" && strings.Contains(string(event.Payload), "Outside plans") {
			created = event.ID
		}
	}
	if created == "" {
		t.Fatalf("no creation event for the outside issue in %v", foreignEvents)
	}
	if events, _ := getIn("outside", outsider, url.Values{"aggregateId": {foreign.ID}}, http.StatusOK); !hasEvent(events, created) {
		t.Fatalf("outside administrator cannot read their issue's events: %v", events)
	}
	if events, _ := get(admin, url.Values{"aggregateId": {foreign.ID}}, http.StatusOK); hasEvent(events, created) {
		t.Fatalf("aggregate %s returned another workspace's event: %v", foreign.ID, events)
	}
	getIn("outside", admin, url.Values{"aggregateId": {foreign.ID}}, http.StatusForbidden)
	get(admin, url.Values{"aggregateId": {issue.ID}, "cursor": {"not-a-cursor"}}, http.StatusBadRequest)
	get(member, url.Values{"aggregateId": {issue.ID}}, http.StatusForbidden)

	whole, next := get(admin, url.Values{"aggregateId": {issue.ID}}, http.StatusOK)
	if next != "" || !slices.EqualFunc(whole, stored, func(a, b domain.DomainEvent) bool { return a.ID == b.ID }) {
		t.Fatalf("unpaged events = %v (next %q), want %v", whole, next, stored)
	}
	var paged []domain.DomainEvent
	cursor := ""
	for pages := 0; ; pages++ {
		if pages > len(stored) {
			t.Fatal("pagination did not end")
		}
		query := url.Values{"aggregateId": {issue.ID}, "limit": {"2"}}
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		page, next := get(admin, query, http.StatusOK)
		if len(page) > 2 || (next != "" && len(page) != 2) {
			t.Fatalf("page of %d events with next %q", len(page), next)
		}
		paged = append(paged, page...)
		if cursor = next; cursor == "" {
			break
		}
	}
	ids := func(events []domain.DomainEvent) []string {
		result := make([]string, len(events))
		for i, event := range events {
			if event.AggregateID != issue.ID {
				t.Fatalf("event %s belongs to %s", event.ID, event.AggregateID)
			}
			result[i] = event.ID
		}
		slices.Sort(result)
		return result
	}
	if got, want := ids(paged), ids(stored); !slices.Equal(got, want) {
		t.Fatalf("paged events = %v, want %v", got, want)
	}

	workspaceEvents, _ := get(admin, url.Values{"aggregateId": {bootstrap.Workspace.ID}}, http.StatusOK)
	for _, event := range workspaceEvents {
		if event.AggregateID != bootstrap.Workspace.ID {
			t.Fatalf("workspace events include %s", event.AggregateID)
		}
	}
}
