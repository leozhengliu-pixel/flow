package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// The authorization's projection reaches only the same viewer and workspace,
// and only once.
func TestPagedMetadataHandoffIsPerViewerAndSingleUse(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/projects/p", nil)
	handOffPagedMetadata(request, "acme", "user-a", domain.Bootstrap{ViewerRole: "member", Projects: []domain.Project{{ID: "p"}}})
	if _, ok := takePagedMetadata(request, "acme", "user-b"); ok {
		t.Fatal("another user's projection was handed over")
	}
	if _, ok := takePagedMetadata(request, "other", "user-a"); ok {
		t.Fatal("another workspace's projection was handed over")
	}
	data, ok := takePagedMetadata(request, "acme", "user-a")
	if !ok || len(data.Projects) != 1 {
		t.Fatalf("projection not handed over: %v %+v", ok, data)
	}
	if _, ok := takePagedMetadata(request, "acme", "user-a"); ok {
		t.Fatal("the projection was handed over twice")
	}
}

// Signed in, end to end and concurrently: a project of a private team stays
// hidden from a member outside it while the admin and a team member read it,
// on the routes that now take the authorization's projection.
func TestResourceProjectionHandoffKeepsPrivateTeamsPrivate(t *testing.T) {
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	server := newAuthServerForStore(t, repo)
	suffix := fmt.Sprint(time.Now().UnixNano() % 100000)
	admin := peerAdmin(t, server.URL)
	teams := server.URL + "/api/workspaces/test-workspace/teams"
	open := authRequest[domain.Team](t, admin, http.MethodPost, teams, map[string]any{"name": "Handoff Open " + suffix, "key": "HO" + suffix[len(suffix)-3:]}, "", http.StatusCreated)
	private := authRequest[domain.Team](t, admin, http.MethodPost, teams, map[string]any{"name": "Handoff Private " + suffix, "key": "HP" + suffix[len(suffix)-3:], "private": true}, "", http.StatusCreated)
	insider, _ := joinByInvitation(t, server.URL, admin, "Handoff Insider", "handoff-insider-"+suffix+"@example.com", "member", open.ID, private.ID)
	outsider, _ := joinByInvitation(t, server.URL, admin, "Handoff Outsider", "handoff-outsider-"+suffix+"@example.com", "member", open.ID)
	secret := authRequest[domain.Project](t, admin, http.MethodPost, server.URL+"/api/projects", map[string]any{"name": "Secret " + suffix, "teamIds": []string{private.ID}}, "test-workspace", http.StatusCreated)
	public := authRequest[domain.Project](t, admin, http.MethodPost, server.URL+"/api/projects", map[string]any{"name": "Public " + suffix, "teamIds": []string{open.ID}}, "test-workspace", http.StatusCreated)

	var wg sync.WaitGroup
	for range 4 {
		for _, check := range []struct {
			client  *http.Client
			project string
			status  int
		}{
			{admin, secret.ID, http.StatusOK}, {insider, secret.ID, http.StatusOK}, {outsider, secret.ID, http.StatusForbidden},
			{outsider, public.ID, http.StatusOK},
		} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				request, _ := http.NewRequest(http.MethodGet, server.URL+"/api/projects/"+check.project, nil)
				request.Header.Set("X-Workspace-Key", "test-workspace")
				response, err := check.client.Do(request)
				if err != nil {
					t.Error(err)
					return
				}
				response.Body.Close()
				if response.StatusCode != check.status {
					t.Errorf("GET project %s = %d, want %d", check.project, response.StatusCode, check.status)
				}
			}()
		}
	}
	wg.Wait()
}
