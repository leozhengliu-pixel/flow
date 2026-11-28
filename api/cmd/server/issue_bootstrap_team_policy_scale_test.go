package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestIssueRecordsBootstrapUsesCanonicalTeamPolicyPages(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_METADATA_WORK_BYTES", strconv.FormatInt(2<<30, 10))
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")

	for driver, repository := range scaleServerTestStores(t) {
		t.Run(driver, func(t *testing.T) {
			legacyService := &server{store: repository, uploadPath: t.TempDir()}
			legacyServer := httptest.NewServer(newTestHandler(t, legacyService))
			t.Cleanup(legacyServer.Close)
			fixture := scaleCreateFixture(t, repository, legacyService, legacyServer)
			admin, workspace := fixture.admin, fixture.data.Workspace.URLKey
			const teamCount = 1100
			if err := repository.MutateWorkspace(store.WithColdWork(t.Context(), "issue bootstrap team policy seed"), workspace, "test.issue_bootstrap_team_policy_seed", "", nil, func(data *domain.Bootstrap) error {
				for index := range teamCount {
					suffix := fmt.Sprintf("%04d", index)
					id := "bt-" + suffix
					data.Teams = append(data.Teams, domain.Team{
						ID: id, Name: "Bootstrap team " + suffix,
						Key: "BT" + suffix, Private: false,
					})
					if index%8 == 0 {
						data.TeamSettings[id] = domain.TeamSettings{
							TeamID: id, Access: "public", SettingsPermission: "allMembers",
						}
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}

			// Capture the installed source before an ordinary user-settings write
			// advances the canonical SQL revision.
			view, err := repository.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			authRequest[domain.UserSettings](t, admin, http.MethodPatch, legacyServer.URL+"/api/account/settings",
				map[string]any{"fontSize": "small"}, "", http.StatusOK)
			authRequest[domain.Bootstrap](t, admin, http.MethodGet, legacyServer.URL+"/api/issue-records/bootstrap",
				nil, workspace, http.StatusOK)

			boundedServer := httptest.NewServer(newTestHandler(t, &server{store: view, uploadPath: t.TempDir()}))
			t.Cleanup(boundedServer.Close)
			sourceURL, err := url.Parse(legacyServer.URL)
			if err != nil {
				t.Fatal(err)
			}
			boundedURL, err := url.Parse(boundedServer.URL)
			if err != nil {
				t.Fatal(err)
			}
			admin.Jar.SetCookies(boundedURL, admin.Jar.Cookies(sourceURL))
			got := authRequest[domain.Bootstrap](t, admin, http.MethodGet, boundedServer.URL+"/api/issue-records/bootstrap",
				nil, workspace, http.StatusOK)
			source, ok := repository.BootstrapFor(workspace)
			if !ok || len(got.Teams) != len(source.Teams) || len(got.Teams) < teamCount {
				t.Fatalf("canonical policy teams = %d, want at least %d and equal to source", len(got.Teams), teamCount)
			}
			role, status, err := repository.WorkspaceRole(t.Context(), fixture.data.Workspace.ID, fixture.data.Viewer.ID)
			if err != nil {
				t.Fatal(err)
			}
			if status != "active" || got.ViewerRole != role || got.Workspace.URLKey != workspace {
				t.Fatalf("bootstrap lost authenticated workspace binding: workspace=%q role=%q", got.Workspace.URLKey, got.ViewerRole)
			}
		})
	}
}
