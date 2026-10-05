package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// sseCollector reads one client's /api/realtime/events stream.
type sseCollector struct {
	mu     sync.Mutex
	events []domain.RealtimeEvent
	ready  chan struct{}
}

func collectSSE(t *testing.T, client *http.Client, url string) *sseCollector {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	request.Header.Set("X-Workspace-Key", "test-workspace")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("realtime stream status %d", response.StatusCode)
	}
	collector := &sseCollector{ready: make(chan struct{})}
	go func() {
		defer response.Body.Close()
		scanner := bufio.NewScanner(response.Body)
		scanner.Buffer(make([]byte, 1<<20), 1<<22)
		once := sync.Once{}
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var event domain.RealtimeEvent
			if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event) == nil {
				collector.mu.Lock()
				collector.events = append(collector.events, event)
				collector.mu.Unlock()
				once.Do(func() { close(collector.ready) })
			}
		}
	}()
	select {
	case <-collector.ready:
	case <-time.After(5 * time.Second):
		t.Fatal("realtime stream never connected")
	}
	return collector
}

func (c *sseCollector) has(eventType, aggregate string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, event := range c.events {
		if event.Type == eventType && event.AggregateID == aggregate {
			return true
		}
	}
	return false
}

// B3: paged realtime delivers project and initiative update events to
// members who can see the project (the "New updates available" pill and the
// sidebar badge depend on them) and never to members of other teams.
func TestPagedRealtimeDeliversPulseEventsByProjectVisibility(t *testing.T) {
	repo := pulseTestRepo(t)
	key := "test-workspace"
	host := httptest.NewServer(newHandler(&server{store: repo, uploadPath: t.TempDir()}))
	// Registered before the streams, so their cancellation runs first.
	t.Cleanup(host.Close)
	admin := authClient(t)
	authRequest[domain.AuthSession](t, admin, "POST", host.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	authRequest[domain.WorkspaceMember](t, admin, "PATCH", host.URL+"/api/workspaces/"+key+"/members/usr_member", map[string]string{"role": "member"}, "", http.StatusOK)
	private := authRequest[domain.Team](t, admin, "POST", host.URL+"/api/workspaces/"+key+"/teams", map[string]any{"name": "Private", "key": "PRV", "private": true}, "", http.StatusCreated)
	insiderClient, insider := verifiedAuthClient(t, host.URL, "Insider", "insider@example.test")
	if err := repo.EnsureWorkspaceMembership(context.Background(), "workspace_test", insider.ID); err != nil {
		t.Fatal(err)
	}
	authRequest[any](t, admin, "PUT", host.URL+"/api/workspaces/"+key+"/teams/"+private.ID+"/members/"+insider.ID, map[string]any{"member": true, "role": "member"}, "", http.StatusNoContent)
	err := repo.MutateWorkspace(context.Background(), key, "test.pulse", "", nil, func(data *domain.Bootstrap) error {
		data.Projects = append(data.Projects, domain.Project{ID: "project_private", Name: "Private project", SlugID: "private-project", TeamIDs: []string{private.ID}, MemberIDs: []string{}, Status: data.Projects[0].Status})
		data.Initiatives = append(data.Initiatives, domain.Initiative{ID: "initiative_private", Name: "Private initiative", LeadTeamID: private.ID, ProjectIDs: []string{"project_private"}, ContributingTeamIDs: []string{}, LabelIDs: []string{}, ParentInitiativeIDs: []string{}})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	outsider := authClient(t)
	authRequest[domain.AuthSession](t, outsider, "POST", host.URL+"/api/auth/login", map[string]string{"email": "member@example.test", "password": "test-password"}, "", http.StatusOK)
	insiderStream := collectSSE(t, insiderClient, host.URL+"/api/realtime/events")
	outsiderStream := collectSSE(t, outsider, host.URL+"/api/realtime/events")

	authRequest[domain.ProjectUpdate](t, admin, "POST", host.URL+"/api/projects/project_aut/updates", map[string]any{"body": "Public update"}, key, http.StatusCreated)
	authRequest[domain.ProjectUpdate](t, admin, "POST", host.URL+"/api/projects/project_private/updates", map[string]any{"body": "Secret update"}, key, http.StatusCreated)
	authRequest[domain.InitiativeUpdate](t, admin, "POST", host.URL+"/api/initiatives/initiative_private/updates", map[string]any{"body": "Secret initiative update"}, key, http.StatusCreated)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !(insiderStream.has("project.update_created", "project_aut") && insiderStream.has("project.update_created", "project_private") && insiderStream.has("initiative.update_created", "initiative_private") && outsiderStream.has("project.update_created", "project_aut")) {
		time.Sleep(20 * time.Millisecond)
	}
	if !insiderStream.has("project.update_created", "project_aut") || !insiderStream.has("project.update_created", "project_private") || !insiderStream.has("initiative.update_created", "initiative_private") {
		t.Fatalf("private team member missed pulse events: %+v", insiderStream.events)
	}
	if !outsiderStream.has("project.update_created", "project_aut") {
		t.Fatalf("member missed the public project's update event: %+v", outsiderStream.events)
	}
	time.Sleep(100 * time.Millisecond)
	if outsiderStream.has("project.update_created", "project_private") || outsiderStream.has("initiative.update_created", "initiative_private") {
		t.Fatal("a member outside the private team received its update events")
	}
}

func (c *sseCollector) find(eventType string) []domain.RealtimeEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := []domain.RealtimeEvent{}
	for _, event := range c.events {
		if event.Type == eventType {
			result = append(result, event)
		}
	}
	return result
}

// Creating a notification publishes notification.created {id, recipientId}
// that reaches only the recipient's paged realtime stream, so the paged
// inbox can refresh.
func TestNotificationCreatedReachesOnlyTheRecipient(t *testing.T) {
	repo := pulseTestRepo(t)
	key := "test-workspace"
	host := httptest.NewServer(newHandler(&server{store: repo, uploadPath: t.TempDir()}))
	t.Cleanup(host.Close)
	admin := authClient(t)
	authRequest[domain.AuthSession](t, admin, "POST", host.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	authRequest[domain.WorkspaceMember](t, admin, "PATCH", host.URL+"/api/workspaces/"+key+"/members/usr_member", map[string]string{"role": "member"}, "", http.StatusOK)
	member := authClient(t)
	authRequest[domain.AuthSession](t, member, "POST", host.URL+"/api/auth/login", map[string]string{"email": "member@example.test", "password": "test-password"}, "", http.StatusOK)
	update := authRequest[domain.ProjectUpdate](t, member, "POST", host.URL+"/api/projects/project_aut/updates", map[string]any{"body": "Member update"}, key, http.StatusCreated)
	memberStream := collectSSE(t, member, host.URL+"/api/realtime/events")
	adminStream := collectSSE(t, admin, host.URL+"/api/realtime/events")
	authRequest[domain.ProjectUpdate](t, admin, "POST", host.URL+"/api/projects/project_aut/updates/"+update.ID+"/comments", map[string]any{"body": "Nice"}, key, http.StatusCreated)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && len(memberStream.find("notification.created")) == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	events := memberStream.find("notification.created")
	if len(events) != 1 {
		t.Fatalf("recipient notification.created events = %+v", memberStream.events)
	}
	var payload map[string]any
	_ = json.Unmarshal(events[0].Payload, &payload)
	if payload["id"] != events[0].AggregateID || payload["recipientId"] != "usr_member" || payload["entity"] == nil {
		t.Fatalf("notification.created payload = %s", events[0].Payload)
	}
	time.Sleep(100 * time.Millisecond)
	if got := adminStream.find("notification.created"); len(got) != 0 {
		t.Fatalf("another member received notification.created: %+v", got)
	}
}

// Pulse summary batches publish notification.created for each summary.
func TestPulseSummaryBatchPublishesNotificationCreated(t *testing.T) {
	repo := pulseTestRepo(t)
	var mu sync.Mutex
	created := map[string]string{}
	repo.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
		if event.Type == "notification.created" {
			var payload map[string]string
			_ = json.Unmarshal(event.Payload, &payload)
			mu.Lock()
			created[payload["recipientId"]] = event.AggregateID
			mu.Unlock()
		}
	})
	now := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	addPulseUpdates(t, repo, "project_aut", domain.ProjectUpdate{ID: "batched", Body: "Shipped.", User: domain.User{ID: "usr_writer", Name: "Writer"}, CreatedAt: now.Add(-5 * time.Hour)})
	if err := (&server{store: repo}).preparePulseSummaries(t.Context(), "test-workspace", now); err != nil {
		t.Fatal(err)
	}
	summaries := pulseSummaries(repo)
	mu.Lock()
	defer mu.Unlock()
	if len(summaries) == 0 || len(created) != len(summaries) {
		t.Fatalf("summaries %v, notification.created %v", summaries, created)
	}
	for recipient, summary := range summaries {
		if created[recipient] != summary.ID {
			t.Fatalf("notification.created for %s = %q, want %q", recipient, created[recipient], summary.ID)
		}
	}
}
