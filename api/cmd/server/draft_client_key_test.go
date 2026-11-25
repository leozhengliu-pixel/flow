package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func draftClientKeyFixture(t *testing.T) (*httptest.Server, func(email string) *http.Client) {
	t.Helper()
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	host := httptest.NewServer(newHandler(&server{store: repository, uploadPath: t.TempDir()}))
	t.Cleanup(host.Close)
	login := func(email string) *http.Client {
		client := authClient(t)
		authRequest[domain.AuthSession](t, client, http.MethodPost, host.URL+"/api/auth/login", map[string]string{"email": email, "password": "test-password"}, "", http.StatusOK)
		return client
	}
	return host, login
}

func countDraftsWithKey(drafts []domain.Draft, key string) int {
	count := 0
	for _, draft := range drafts {
		if draft.ClientKey == key {
			count++
		}
	}
	return count
}

// A composer retries a create whose answer it never got (timeout, dropped connection): with the draft's client key the
// server returns the draft it already made, updated to the retried content, instead of inserting a copy.
func TestDraftCreateWithClientKeyIsIdempotent(t *testing.T) {
	host, login := draftClientKeyFixture(t)
	admin := login("admin@example.test")
	const workspace = "test-workspace"
	drafts := host.URL + "/api/drafts"
	bootstrap := authRequest[domain.Bootstrap](t, admin, http.MethodGet, host.URL+"/api/bootstrap", nil, workspace, http.StatusOK)
	if len(bootstrap.Issues) == 0 {
		t.Fatal("fixture has no issues")
	}
	issueID := bootstrap.Issues[0].ID
	input := map[string]any{"type": "comment", "resourceId": issueID, "title": "Issue", "body": "@", "metadata": map[string]any{"resourceType": "issue"}, "clientKey": "draft-key:one"}

	first := authRequest[domain.Draft](t, admin, http.MethodPost, drafts, input, workspace, http.StatusCreated)
	if first.ClientKey != "draft-key:one" {
		t.Fatalf("created draft key = %q", first.ClientKey)
	}
	// The same create again (the response was lost): the same draft, unchanged.
	again := authRequest[domain.Draft](t, admin, http.MethodPost, drafts, input, workspace, http.StatusOK)
	if again.ID != first.ID || again.Body != "@" {
		t.Fatalf("retried create = %+v, want draft %s", again, first.ID)
	}
	// The retry carries newer content: the same draft holds it.
	input["body"] = "@ build agent"
	newer := authRequest[domain.Draft](t, admin, http.MethodPost, drafts, input, workspace, http.StatusOK)
	if newer.ID != first.ID || newer.Body != "@ build agent" {
		t.Fatalf("retried create with newer content = %+v", newer)
	}
	// An update the server applied but whose answer was lost is retried as the same update.
	for range 2 {
		updated := authRequest[domain.Draft](t, admin, http.MethodPatch, drafts+"/"+first.ID, map[string]any{"body": "@ build agent please", "clientKey": "draft-key:one"}, workspace, http.StatusOK)
		if updated.ID != first.ID || updated.Body != "@ build agent please" {
			t.Fatalf("retried update = %+v", updated)
		}
	}
	after := authRequest[domain.Bootstrap](t, admin, http.MethodGet, host.URL+"/api/bootstrap", nil, workspace, http.StatusOK)
	if count := countDraftsWithKey(after.Drafts, "draft-key:one"); count != 1 {
		t.Fatalf("drafts with the key = %d, want 1: %+v", count, after.Drafts)
	}

	// Old clients send no key: every create is a new draft, exactly as before.
	plain := map[string]any{"type": "comment", "resourceId": issueID, "body": "@", "metadata": map[string]any{"resourceType": "issue"}}
	one := authRequest[domain.Draft](t, admin, http.MethodPost, drafts, plain, workspace, http.StatusCreated)
	two := authRequest[domain.Draft](t, admin, http.MethodPost, drafts, plain, workspace, http.StatusCreated)
	if one.ID == two.ID || one.ID == first.ID || one.ClientKey != "" || two.ClientKey != "" {
		t.Fatalf("creates without a key = %+v, %+v", one, two)
	}

	// A key on a deleted draft makes a new one (the composer's draft was discarded elsewhere).
	authStatus(t, admin, http.MethodDelete, drafts+"/"+first.ID, http.StatusNoContent)
	recreated := authRequest[domain.Draft](t, admin, http.MethodPost, drafts, input, workspace, http.StatusCreated)
	if recreated.ID == first.ID {
		t.Fatal("a deleted draft came back under its old id")
	}
	authRequest[any](t, admin, http.MethodPost, drafts, map[string]any{"type": "issue", "title": "Long key", "clientKey": strings.Repeat("k", 201)}, workspace, http.StatusBadRequest)
}

// The client key never merges drafts that are legitimately distinct: other users, workspaces, resources, parent kinds,
// draft types and composers each keep their own draft even when they send the same key.
func TestDraftClientKeyKeepsDistinctDraftsApart(t *testing.T) {
	host, login := draftClientKeyFixture(t)
	admin := login("admin@example.test")
	member := login("member@example.test")
	const workspace = "test-workspace"
	drafts := host.URL + "/api/drafts"
	bootstrap := authRequest[domain.Bootstrap](t, admin, http.MethodGet, host.URL+"/api/bootstrap", nil, workspace, http.StatusOK)
	if len(bootstrap.Issues) < 2 || len(bootstrap.Projects) == 0 {
		t.Fatalf("fixture has %d issues and %d projects", len(bootstrap.Issues), len(bootstrap.Projects))
	}
	issueA, issueB, project := bootstrap.Issues[0].ID, bootstrap.Issues[1].ID, bootstrap.Projects[0].ID
	const key = "draft-key:shared"
	post := func(client *http.Client, workspaceKey string, input map[string]any) domain.Draft {
		t.Helper()
		input["clientKey"] = key
		return authRequest[domain.Draft](t, client, http.MethodPost, drafts, input, workspaceKey, http.StatusCreated)
	}
	comment := func(resourceID, resourceType string) map[string]any {
		return map[string]any{"type": "comment", "resourceId": resourceID, "body": "@", "metadata": map[string]any{"resourceType": resourceType}}
	}

	created := map[string]domain.Draft{
		"admin issue A":      post(admin, workspace, comment(issueA, "issue")),
		"member issue A":     post(member, workspace, comment(issueA, "issue")),
		"admin issue B":      post(admin, workspace, comment(issueB, "issue")),
		"admin project":      post(admin, workspace, comment(project, "project")),
		"admin update":       post(admin, workspace, map[string]any{"type": "project_update", "resourceId": project, "body": "@"}),
		"admin issue draft":  post(admin, workspace, map[string]any{"type": "issue", "title": "@"}),
		"admin loop draft":   post(admin, workspace, map[string]any{"type": "loop", "title": "@"}),
		"admin reply":        authRequest[domain.Draft](t, admin, http.MethodPost, drafts, map[string]any{"type": "comment", "resourceId": issueA, "body": "@", "metadata": map[string]any{"resourceType": "issue"}, "clientKey": "draft-key:reply"}, workspace, http.StatusCreated),
		"admin without key":  authRequest[domain.Draft](t, admin, http.MethodPost, drafts, comment(issueA, "issue"), workspace, http.StatusCreated),
		"member issue draft": post(member, workspace, map[string]any{"type": "issue", "title": "@"}),
	}
	// Another workspace of the same user, with an issue of its own.
	other := authRequest[domain.Bootstrap](t, admin, http.MethodPost, host.URL+"/api/workspaces", map[string]string{"name": "Other space", "urlKey": "other-space"}, "", http.StatusCreated)
	if len(other.Teams) == 0 {
		t.Fatal("new workspace has no team")
	}
	otherIssue := authRequest[domain.Issue](t, admin, http.MethodPost, host.URL+"/api/issues", map[string]any{"title": "Elsewhere", "teamId": other.Teams[0].ID}, "other-space", http.StatusCreated)
	created["admin other workspace"] = post(admin, "other-space", comment(otherIssue.ID, "issue"))
	created["admin other workspace issue draft"] = post(admin, "other-space", map[string]any{"type": "issue", "title": "@"})

	ids := map[string]string{}
	for name, draft := range created {
		if previous, seen := ids[draft.ID]; seen {
			t.Fatalf("%s and %s were merged into draft %s", name, previous, draft.ID)
		}
		ids[draft.ID] = name
	}

	// Each draft is still found by its own retry, and only its own.
	again := authRequest[domain.Draft](t, member, http.MethodPost, drafts, map[string]any{"type": "comment", "resourceId": issueA, "body": "@ member", "metadata": map[string]any{"resourceType": "issue"}, "clientKey": key}, workspace, http.StatusOK)
	if again.ID != created["member issue A"].ID {
		t.Fatalf("member retry = %s, want %s", again.ID, created["member issue A"].ID)
	}
	adminView := authRequest[domain.Bootstrap](t, admin, http.MethodGet, host.URL+"/api/bootstrap", nil, workspace, http.StatusOK)
	adminDraft := adminView.Drafts[slices.IndexFunc(adminView.Drafts, func(item domain.Draft) bool { return item.ID == created["admin issue A"].ID })]
	if adminDraft.Body != "@" {
		t.Fatalf("the member's retry changed the admin's draft: %+v", adminDraft)
	}
	if slices.ContainsFunc(adminView.Drafts, func(item domain.Draft) bool { return item.UserID != adminView.Viewer.ID }) {
		t.Fatal("the admin sees another user's draft")
	}
	// A key reused across workspaces stays in its own workspace.
	elsewhere := authRequest[domain.Draft](t, admin, http.MethodPost, drafts, map[string]any{"type": "issue", "title": "@ again", "clientKey": key}, "other-space", http.StatusOK)
	if elsewhere.ID != created["admin other workspace issue draft"].ID {
		t.Fatalf("other-workspace retry = %s, want %s", elsewhere.ID, created["admin other workspace issue draft"].ID)
	}
}
