package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// One pulse tick serves every due member with a single write, and the shared
// per-visibility projection still keeps a private team's updates out of the
// summaries of members who cannot see that team.
func TestPulseTickCountsPerVisibilityInOneWrite(t *testing.T) {
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	key := repo.Bootstrap().Workspace.URLKey
	s := &server{store: repo, uploadPath: t.TempDir()}
	host := httptest.NewServer(newHandler(s))
	defer host.Close()
	admin := authClient(t)
	authRequest[domain.AuthSession](t, admin, "POST", host.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	authRequest[domain.WorkspaceMember](t, admin, "PATCH", host.URL+"/api/workspaces/"+key+"/members/usr_member", map[string]string{"role": "member"}, "", http.StatusOK)
	private := authRequest[domain.Team](t, admin, "POST", host.URL+"/api/workspaces/"+key+"/teams", map[string]any{"name": "Private", "key": "PRV", "private": true}, "", http.StatusCreated)

	now := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	err = repo.MutateWorkspace(t.Context(), key, "test.pulse", "", nil, func(data *domain.Bootstrap) error {
		data.WorkspaceSettings.FeatureSettings.PulseWorkspaceSchedule = "daily"
		data.WorkspaceSettings.FeatureFlags["pulse"] = true
		for id, settings := range data.TeamSettings {
			settings.Timezone = "UTC"
			data.TeamSettings[id] = settings
		}
		data.Projects = append(data.Projects, domain.Project{ID: "project_private", Name: "Private project", TeamIDs: []string{private.ID}})
		data.ProjectUpdates["project_private"] = []domain.ProjectUpdate{{ID: "private-update", ProjectID: "project_private", Body: "Secret", User: data.Viewer, CreatedAt: now.Add(-2 * time.Hour)}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	keys, err := repo.ViewerVisibilityKeys(t.Context(), key)
	if err != nil || keys["usr_admin"] == keys["usr_member"] {
		t.Fatalf("visibility keys = %v (%v), want admin and member apart", keys, err)
	}
	events := func() int {
		count := 0
		all, _ := repo.Events(t.Context(), "")
		for _, event := range all {
			if event.Type == "pulse.summary_scheduled" {
				count++
			}
		}
		return count
	}
	before := events()
	if err := s.preparePulseSummaries(t.Context(), key, now); err != nil {
		t.Fatal(err)
	}
	if written := events() - before; written != 1 {
		t.Fatalf("pulse tick wrote %d times, want 1", written)
	}
	summaries := map[string]int{}
	for _, notification := range repo.Bootstrap().Notifications {
		if notification.Type == "pulseSummary" {
			summaries[notification.RecipientID] = notification.OccurrenceCount
		}
	}
	if summaries["usr_admin"] != 1 || summaries["usr_member"] != 0 {
		t.Fatalf("summaries = %v, want only the admin to hear about the private update", summaries)
	}
	settings, _ := repo.WorkspaceMetadataFields(key, "settings")
	cursors := pulseCursors(&settings)
	if !cursors["usr_admin"].Equal(time.Date(2026, 9, 14, 9, 0, 0, 0, time.UTC)) || !cursors["usr_member"].Equal(cursors["usr_admin"]) {
		t.Fatalf("cursors = %v, want both advanced to 09:00", cursors)
	}
}
