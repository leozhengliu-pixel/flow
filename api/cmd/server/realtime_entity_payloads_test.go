package main

import (
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

type entityPayloadFixture struct {
	host     *httptest.Server
	admin    *http.Client
	private  domain.Team
	insider  *sseCollector
	outsider *sseCollector
	adminSSE *sseCollector
}

// A private team with one member (insider) and a member outside it
// (outsider), each holding a realtime stream.
func newEntityPayloadFixture(t *testing.T, seed func(data *domain.Bootstrap, privateTeamID string)) entityPayloadFixture {
	t.Helper()
	repo := pulseTestRepo(t)
	key := "test-workspace"
	host := httptest.NewServer(newHandler(&server{store: repo, uploadPath: t.TempDir()}))
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
	if seed != nil {
		if err := repo.MutateWorkspace(context.Background(), key, "test.entity_payloads", "", nil, func(data *domain.Bootstrap) error {
			seed(data, private.ID)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	outsider := authClient(t)
	authRequest[domain.AuthSession](t, outsider, "POST", host.URL+"/api/auth/login", map[string]string{"email": "member@example.test", "password": "test-password"}, "", http.StatusOK)
	return entityPayloadFixture{
		host: host, admin: admin, private: private,
		insider:  collectSSE(t, insiderClient, host.URL+"/api/realtime/events"),
		outsider: collectSSE(t, outsider, host.URL+"/api/realtime/events"),
		adminSSE: collectSSE(t, admin, host.URL+"/api/realtime/events"),
	}
}

func waitForEvents(t *testing.T, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !done() {
		time.Sleep(20 * time.Millisecond)
	}
	if !done() {
		t.Fatal("realtime events did not arrive")
	}
	// Let anything that should not arrive have its chance.
	time.Sleep(100 * time.Millisecond)
}

// lastPayload decodes the payload of the stream's last eventType event for aggregate.
func lastPayload(t *testing.T, stream *sseCollector, eventType, aggregate string) map[string]any {
	t.Helper()
	var payload map[string]any
	found := false
	for _, event := range stream.find(eventType) {
		if event.AggregateID == aggregate {
			payload, found = map[string]any{}, true
			_ = json.Unmarshal(event.Payload, &payload)
		}
	}
	if !found {
		t.Fatalf("no %s for %s in %+v", eventType, aggregate, stream.events)
	}
	return payload
}

func stringList(value any) []string {
	out := []string{}
	items, _ := value.([]any)
	for _, item := range items {
		if text, ok := item.(string); ok {
			out = append(out, text)
		}
	}
	return out
}

// Project and initiative events carry the viewer's projection of the entity:
// links into a private team are cut for those outside it, the issue count is
// left out, and the mutation input (which can name hidden teams) is dropped.
func TestPagedProjectEventsCarryTheViewersProjection(t *testing.T) {
	fixture := newEntityPayloadFixture(t, func(data *domain.Bootstrap, privateTeamID string) {
		status := data.Projects[0].Status
		data.Projects = append(data.Projects,
			domain.Project{ID: "project_hidden", Name: "Hidden", SlugID: "hidden-000000000001", TeamIDs: []string{privateTeamID}, MemberIDs: []string{}, Status: status},
			domain.Project{ID: "project_shared", Name: "Shared", SlugID: "shared-000000000002", TeamIDs: []string{"team_test", privateTeamID}, DependencyIDs: []string{"project_hidden"}, MemberIDs: []string{}, IssueCount: 42, Status: status},
		)
		data.Initiatives = append(data.Initiatives, domain.Initiative{ID: "initiative_mixed", Name: "Mixed", SlugID: "mixed-000000000003", ContributingTeamIDs: []string{"team_test", privateTeamID}, ProjectIDs: []string{"project_shared", "project_hidden"}, LabelIDs: []string{}, ParentInitiativeIDs: []string{}})
	})
	key, base := "test-workspace", fixture.host.URL
	authRequest[domain.Comment](t, fixture.admin, http.MethodPost, base+"/api/projects/project_shared/comments", map[string]any{"body": "Shared note"}, key, http.StatusCreated)
	authRequest[domain.Comment](t, fixture.admin, http.MethodPost, base+"/api/projects/project_hidden/comments", map[string]any{"body": "Hidden note"}, key, http.StatusCreated)
	authRequest[domain.Comment](t, fixture.admin, http.MethodPost, base+"/api/initiatives/initiative_mixed/comments", map[string]any{"body": "Initiative note"}, key, http.StatusCreated)
	authRequest[domain.ProjectUpdate](t, fixture.admin, http.MethodPost, base+"/api/projects/project_shared/updates", map[string]any{"body": "Shared update", "health": "atRisk"}, key, http.StatusCreated)
	waitForEvents(t, func() bool {
		return fixture.outsider.has("initiative.commented", "initiative_mixed") && fixture.insider.has("project.commented", "project_hidden") && fixture.insider.has("initiative.commented", "initiative_mixed") && fixture.outsider.has("project.update_created", "project_shared")
	})
	posted := lastPayload(t, fixture.outsider, "project.update_created", "project_shared")
	if updates, _ := posted["updates"].([]any); len(updates) != 1 || !strings.Contains(string(mustJSON(t, updates)), "Shared update") || strings.Contains(string(mustJSON(t, updates)), `"snapshot"`) {
		t.Fatalf("project.update_created updates = %v", posted["updates"])
	}
	if entity, _ := posted["entity"].(map[string]any); entity["health"] != "atRisk" {
		t.Fatalf("project.update_created entity = %v", posted["entity"])
	}

	if fixture.outsider.has("project.commented", "project_hidden") {
		t.Fatal("a member outside the private team received its project's comment")
	}
	outside := lastPayload(t, fixture.outsider, "project.commented", "project_shared")
	entity, _ := outside["entity"].(map[string]any)
	if !slices.Equal(stringList(entity["teamIds"]), []string{"team_test"}) || len(stringList(entity["dependencyIds"])) != 0 || entity["issueCount"] != float64(0) {
		t.Fatalf("outsider's project projection leaks the private team: %v", entity)
	}
	if comments, _ := entity["comments"].([]any); len(comments) != 1 || !strings.Contains(string(mustJSON(t, comments)), "Shared note") {
		t.Fatalf("project comments = %v", entity["comments"])
	}
	if outside["body"] != nil {
		t.Fatalf("mutation input kept beside the entity: %v", outside)
	}
	inside := lastPayload(t, fixture.insider, "project.commented", "project_shared")
	insideEntity, _ := inside["entity"].(map[string]any)
	if !slices.Contains(stringList(insideEntity["teamIds"]), fixture.private.ID) || !slices.Equal(stringList(insideEntity["dependencyIds"]), []string{"project_hidden"}) {
		t.Fatalf("insider's projection = %v", insideEntity)
	}
	initiative, _ := lastPayload(t, fixture.outsider, "initiative.commented", "initiative_mixed")["entity"].(map[string]any)
	if !slices.Equal(stringList(initiative["projectIds"]), []string{"project_shared"}) || !slices.Equal(stringList(initiative["contributingTeamIds"]), []string{"team_test"}) {
		t.Fatalf("outsider's initiative projection = %v", initiative)
	}
	if insiderInitiative, _ := lastPayload(t, fixture.insider, "initiative.commented", "initiative_mixed")["entity"].(map[string]any); len(stringList(insiderInitiative["projectIds"])) != 2 {
		t.Fatalf("insider's initiative projection = %v", insiderInitiative)
	}

	// Moving the shared project into the private team only: the outsider loses it and is told to reload, without the entity.
	authRequest[domain.Project](t, fixture.admin, http.MethodPatch, base+"/api/projects/project_shared", map[string]any{"teamIds": []string{fixture.private.ID}}, key, http.StatusOK)
	// Deleting the hidden project reaches everyone as a bare signal.
	authRequest[any](t, fixture.admin, http.MethodDelete, base+"/api/projects/project_hidden", nil, key, http.StatusNoContent)
	waitForEvents(t, func() bool {
		return fixture.outsider.has("project.updated", "project_shared") && fixture.outsider.has("project.deleted", "project_hidden") && fixture.insider.has("project.updated", "project_shared")
	})
	moved := lastPayload(t, fixture.outsider, "project.updated", "project_shared")
	if moved["scopeChanged"] != true || moved["entity"] != nil || moved["teamIds"] != nil {
		t.Fatalf("outsider's scope change = %v", moved)
	}
	if insideMoved := lastPayload(t, fixture.insider, "project.updated", "project_shared"); insideMoved["scopeChanged"] != true || insideMoved["entity"] == nil {
		t.Fatalf("insider's scope change = %v", insideMoved)
	}
	if deleted := lastPayload(t, fixture.outsider, "project.deleted", "project_hidden"); deleted["deleted"] != true || len(deleted) != 1 {
		t.Fatalf("project.deleted = %v", deleted)
	}
}

// Document events carry the stored document to everyone who may open it (and
// its comments for a comment event), never to anyone else; a document moved
// out of a viewer's reach reaches them as a bare removal; drafts stay with
// their author until published.
func TestPagedDocumentEventsCarryTheDocument(t *testing.T) {
	fixture := newEntityPayloadFixture(t, nil)
	key, base := "test-workspace", fixture.host.URL
	public := authRequest[domain.Document](t, fixture.admin, http.MethodPost, base+"/api/documents", map[string]any{"title": "Public", "content": "start", "teamIds": []string{"team_test"}}, key, http.StatusCreated)
	hidden := authRequest[domain.Document](t, fixture.admin, http.MethodPost, base+"/api/documents", map[string]any{"title": "Hidden", "content": "secret", "teamIds": []string{fixture.private.ID}}, key, http.StatusCreated)
	authRequest[domain.Document](t, fixture.admin, http.MethodPatch, base+"/api/documents/"+public.ID, map[string]any{"content": "edited body", "favorite": true}, key, http.StatusOK)
	authRequest[domain.Document](t, fixture.admin, http.MethodPatch, base+"/api/documents/"+hidden.ID, map[string]any{"content": "secret edit"}, key, http.StatusOK)
	authRequest[domain.Comment](t, fixture.admin, http.MethodPost, base+"/api/documents/"+public.ID+"/comments", map[string]any{"body": "Looks good"}, key, http.StatusCreated)
	waitForEvents(t, func() bool {
		return fixture.outsider.has("document.comment_created", public.ID) && fixture.insider.has("document.updated", hidden.ID)
	})
	if fixture.outsider.has("document.created", hidden.ID) || fixture.outsider.has("document.updated", hidden.ID) {
		t.Fatal("a member outside the private team received its document's events")
	}
	edited := lastPayload(t, fixture.outsider, "document.updated", public.ID)
	document, _ := edited["entity"].(map[string]any)
	if document["content"] != "edited body" || document["favorite"] != false || edited["content"] != nil || edited["documentId"] != public.ID {
		t.Fatalf("document.updated payload = %v", edited)
	}
	commented := lastPayload(t, fixture.outsider, "document.comment_created", public.ID)
	if comments, _ := commented["comments"].([]any); len(comments) != 1 || !strings.Contains(string(mustJSON(t, comments)), "Looks good") {
		t.Fatalf("document comment payload = %v", commented)
	}

	// Drafts: only the author's streams hear of them; publishing reaches every reader with the new body.
	draft := authRequest[domain.DocumentContentDraft](t, fixture.admin, http.MethodPost, base+"/api/documents/"+public.ID+"/drafts", map[string]any{"content": "draft body"}, key, http.StatusCreated)
	authRequest[domain.DocumentContentDraft](t, fixture.admin, http.MethodPatch, base+"/api/documents/"+public.ID+"/drafts/"+draft.ID, map[string]any{"content": "draft body 2"}, key, http.StatusOK)
	authRequest[domain.Document](t, fixture.admin, http.MethodPost, base+"/api/documents/"+public.ID+"/drafts/"+draft.ID+"/publish", nil, key, http.StatusOK)
	// Moving the public document into the private team: the outsider drops it.
	authRequest[domain.Document](t, fixture.admin, http.MethodPatch, base+"/api/documents/"+public.ID, map[string]any{"teamIds": []string{fixture.private.ID}}, key, http.StatusOK)
	waitForEvents(t, func() bool {
		return fixture.outsider.has("document.draft_published", draft.ID) && fixture.adminSSE.has("document.draft_updated", draft.ID) && len(fixture.outsider.find("document.updated")) >= 2
	})
	if len(fixture.outsider.find("document.draft_created")) != 0 || len(fixture.outsider.find("document.draft_updated")) != 0 || len(fixture.insider.find("document.draft_updated")) != 0 {
		t.Fatal("another member received the author's draft events")
	}
	published := lastPayload(t, fixture.outsider, "document.draft_published", draft.ID)
	if entity, _ := published["entity"].(map[string]any); entity["content"] != "draft body 2" || published["documentId"] != public.ID || published["draftId"] != draft.ID {
		t.Fatalf("document.draft_published payload = %v", published)
	}
	removed := lastPayload(t, fixture.outsider, "document.updated", public.ID)
	if removed["removed"] != true || removed["entity"] != nil || removed["documentId"] != public.ID {
		t.Fatalf("outsider's removal = %v", removed)
	}
}

// A draft (an issue, project update or loop being written) and its content stay with its author: other members never
// receive its events, which used to carry the body and made every client resync.
func TestPagedDraftEventsReachOnlyTheirAuthor(t *testing.T) {
	fixture := newEntityPayloadFixture(t, nil)
	key, base := "test-workspace", fixture.host.URL
	draft := authRequest[domain.Draft](t, fixture.admin, http.MethodPost, base+"/api/drafts", map[string]any{"type": "issue", "title": "Secret plan", "body": "Draft body"}, key, http.StatusCreated)
	authRequest[domain.Draft](t, fixture.admin, http.MethodPatch, base+"/api/drafts/"+draft.ID, map[string]any{"body": "Draft body, longer"}, key, http.StatusOK)
	waitForEvents(t, func() bool { return fixture.adminSSE.has("draft.updated", draft.ID) })
	for _, stream := range []*sseCollector{fixture.outsider, fixture.insider} {
		if len(stream.find("draft.created")) != 0 || len(stream.find("draft.updated")) != 0 {
			t.Fatalf("another member received the author's draft: %+v", stream.events)
		}
	}
	if entity, _ := lastPayload(t, fixture.adminSSE, "draft.updated", draft.ID)["entity"].(map[string]any); entity["body"] != "Draft body, longer" {
		t.Fatalf("author's draft.updated entity = %v", entity)
	}
}

func TestRealtimePayloadCacheIsBounded(t *testing.T) {
	var cache realtimePayloadCache
	builds := 0
	for index := range realtimePayloadCacheEntries + 10 {
		cache.load(strings.Repeat("k", index+1), func() json.RawMessage { builds++; return json.RawMessage(`{}`) })
	}
	cache.load("k", func() json.RawMessage { builds++; return json.RawMessage(`{}`) })
	last := strings.Repeat("k", realtimePayloadCacheEntries+10)
	cache.load(last, func() json.RawMessage { builds++; return json.RawMessage(`{}`) })
	if len(cache.entries) != realtimePayloadCacheEntries || builds != realtimePayloadCacheEntries+11 {
		t.Fatalf("entries=%d builds=%d", len(cache.entries), builds)
	}
}
