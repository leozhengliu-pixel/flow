package main

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Pinning, moving, editing and removing team overview resources and sections
// writes a few metadata records: none of them may read issue, comment or
// activity records, whose count grows with the workspace.
func TestTeamResourceWritesSkipIssueAndDiscussionRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "team-resource-writes.db")
	repo, err := store.OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	initial := repo.Bootstrap()
	key, team := initial.Workspace.URLKey, initial.Teams[0].ID
	host := httptest.NewServer(newHandler(&server{store: repo, uploadPath: t.TempDir()}))
	defer host.Close()
	admin := authClient(t)
	authRequest[domain.AuthSession](t, admin, "POST", host.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	other := authRequest[domain.Team](t, admin, "POST", host.URL+"/api/workspaces/"+key+"/teams", map[string]any{"name": "Other", "key": "OTH"}, "", http.StatusCreated).ID
	document := authRequest[domain.Document](t, admin, "POST", host.URL+"/api/documents", map[string]any{"title": "Runbook", "teamIds": []string{team}}, key, http.StatusCreated)
	// Another team's pins must not push this team's new pins down the list.
	for range 3 {
		authRequest[domain.TeamPinnedResource](t, admin, "POST", host.URL+"/api/teams/"+other+"/resources", map[string]any{"resourceType": "link", "url": "https://example.test/other", "title": "Other"}, key, http.StatusCreated)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{`UPDATE issue_records SET data=?`, `UPDATE workspace_content_records SET data=? WHERE kind IN ('comment','activity')`} {
		if _, err := db.Exec(statement, []byte("invalid-unrelated-payload")); err != nil {
			t.Fatal(err)
		}
	}

	base := host.URL + "/api/teams/" + team
	pinned := authRequest[domain.TeamPinnedResource](t, admin, "POST", base+"/resources", map[string]any{"resourceType": "document", "resourceId": document.ID, "title": "Runbook"}, key, http.StatusCreated)
	link := authRequest[domain.TeamPinnedResource](t, admin, "POST", base+"/resources", map[string]any{"resourceType": "link", "url": "https://example.test/a", "title": "A"}, key, http.StatusCreated)
	if pinned.Position != 0 || link.Position != 1 {
		t.Fatalf("new pins go to the end of the team's list: got %v and %v", pinned.Position, link.Position)
	}
	section := authRequest[domain.TeamResourceSection](t, admin, "POST", base+"/resource-sections", map[string]any{"name": "Guides"}, key, http.StatusCreated)
	moved := authRequest[domain.TeamPinnedResource](t, admin, "PATCH", base+"/resources/"+link.ID, map[string]any{"sectionId": section.ID, "url": "https://example.test/b", "title": "B"}, key, http.StatusOK)
	if moved.SectionID != section.ID || moved.URL != "https://example.test/b" || moved.Title != "B" {
		t.Fatalf("link edit not applied: %+v", moved)
	}
	authRequest[any](t, admin, "PATCH", base+"/resources/"+pinned.ID, map[string]any{"url": "https://example.test/c"}, key, http.StatusBadRequest)
	authRequest[any](t, admin, "PATCH", base+"/resources/"+link.ID, map[string]any{"url": "javascript:alert(1)"}, key, http.StatusBadRequest)
	authRequest[any](t, admin, "DELETE", base+"/resource-sections/"+section.ID, nil, key, http.StatusNoContent)
	authRequest[any](t, admin, "DELETE", base+"/resources/"+pinned.ID, nil, key, http.StatusNoContent)
	result := authRequest[teamResourcesRead](t, admin, "GET", base+"/resources", nil, key, http.StatusOK)
	if len(result.Sections) != 0 || len(result.Resources) != 1 || result.Resources[0].ID != link.ID || result.Resources[0].SectionID != "" {
		t.Fatalf("unexpected resources after writes: %+v", result)
	}
}

func TestTeamResourcesAvoidIssueAndDiscussionHydration(t *testing.T) {
	for _, populated := range []bool{false, true} {
		name := "empty"
		if populated {
			name = "populated"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "team-resources.db")
			repo, err := store.OpenSQLiteTestFixture(path)
			if err != nil {
				t.Fatal(err)
			}
			defer repo.Close()
			initial := repo.Bootstrap()
			key, team := initial.Workspace.URLKey, initial.Teams[0].ID
			s := &server{store: repo, uploadPath: t.TempDir()}
			host := httptest.NewServer(newHandler(s))
			defer host.Close()
			admin, member := authClient(t), authClient(t)
			for email, client := range map[string]*http.Client{"admin@example.test": admin, "member@example.test": member} {
				authRequest[domain.AuthSession](t, client, "POST", host.URL+"/api/auth/login", map[string]string{"email": email, "password": "test-password"}, "", http.StatusOK)
			}
			// Fixture users initially have administrator memberships despite the
			// member display name. Exercise a real non-admin visibility projection.
			authRequest[domain.WorkspaceMember](t, admin, "PATCH", host.URL+"/api/workspaces/"+key+"/members/usr_member", map[string]string{"role": "member"}, "", http.StatusOK)
			private := authRequest[domain.Team](t, admin, "POST", host.URL+"/api/workspaces/"+key+"/teams", map[string]any{"name": "Private", "key": "PRV", "private": true}, "", http.StatusCreated)
			err = repo.MutateWorkspace(t.Context(), key, "test.resources", team, nil, func(data *domain.Bootstrap) error {
				data.Documents = nil
				data.TeamResourceSections = []domain.TeamResourceSection{{ID: "private-section", TeamID: private.ID, Name: "Private section"}}
				data.TeamPinnedResources = []domain.TeamPinnedResource{{ID: "private-link", TeamID: private.ID, Title: "Private link", ResourceType: "link", URL: "https://example.test/private"}}
				if populated {
					data.TeamResourceSections = append(data.TeamResourceSections, domain.TeamResourceSection{ID: "later", TeamID: team, Name: "Later", Position: 2}, domain.TeamResourceSection{ID: "first", TeamID: team, Name: "First", Position: 1})
					data.TeamPinnedResources = append(data.TeamPinnedResources, domain.TeamPinnedResource{ID: "public-link", TeamID: team, Title: "Public link", ResourceType: "link", URL: "https://example.test/public"})
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			// Ordinary indexed metadata reads succeed, but any attempt to decode
			// an issue/comment/activity collection now fails immediately.
			for _, statement := range []string{`UPDATE issue_records SET data=?`, `UPDATE workspace_content_records SET data=? WHERE kind IN ('comment','activity')`} {
				if _, err := db.Exec(statement, []byte("invalid-unrelated-payload")); err != nil {
					t.Fatal(err)
				}
			}
			if _, ok := repo.BootstrapForContext(t.Context(), key); ok {
				t.Fatal("poisoned fixture did not block full hydration")
			}
			endpoint := host.URL + "/api/teams/" + team + "/resources"
			for _, client := range []*http.Client{admin, member} {
				result := authRequest[teamResourcesRead](t, client, "GET", endpoint, nil, key, http.StatusOK)
				if result.Sections == nil || result.Resources == nil {
					t.Fatal("empty resources must encode as arrays")
				}
				if populated {
					if len(result.Sections) != 2 || result.Sections[0].ID != "first" || len(result.Resources) != 1 || result.Resources[0].ID != "public-link" {
						t.Fatalf("incorrect resource projection: %+v", result)
					}
				} else if len(result.Sections) != 0 || len(result.Resources) != 0 {
					t.Fatal("another team's resources leaked")
				}
				authRequest[teamResourcesRead](t, client, "GET", endpoint, nil, "", http.StatusOK)
			}
			authRequest[any](t, member, "GET", host.URL+"/api/teams/"+private.ID+"/resources", nil, key, http.StatusForbidden)
			authRequest[any](t, authClient(t), "GET", endpoint, nil, key, http.StatusUnauthorized)
			// Explicitly exercise the authorization-to-handler handoff: only
			// team resources, not a workspace snapshot, should be retained.
			r := httptest.NewRequest("GET", "/api/teams/"+team+"/resources", nil)
			if !s.resourceAllowed(r, key, initial.Viewer.ID) {
				t.Fatal("resource authorization failed")
			}
			if rows, ok := r.Context().Value(teamResourcesReadKey{}).(teamResourcesRead); !ok || rows.TeamID != team {
				t.Fatal("authorized resource rows were not reused")
			}
		})
	}
}
