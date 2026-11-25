package main

import (
	"fmt"
	"net/http"
	"slices"
	"testing"

	"flow/api/internal/domain"
)

// A CI credential acts inside its pipeline's scope: an issue of a team the
// pipeline does not cover (or a closed team when it has no teams) is reported
// as unknown, never linked or unlinked, and never as "skipped".
func TestReleaseCIOutOfScopeIssuesAreUnknown(t *testing.T) {
	for _, bounded := range []string{"off", "on"} {
		t.Run("bounded_"+bounded, func(t *testing.T) {
			t.Setenv("FLOW_BOUNDED_METADATA", bounded)
			t.Setenv("FLOW_RECORD_HYDRATION", "on")
			t.Setenv("FLOW_RECORD_JOURNAL", "on")
			for _, repo := range scaleP03ServerTestStores(t) {
				f := scaleP03Fixture(t, repo)
				workspace := f.data.Workspace.URLKey
				teams := f.host.URL + "/api/workspaces/" + workspace + "/teams"
				open := authRequest[domain.Team](t, f.admin, http.MethodPost, teams, map[string]any{"name": "Open", "key": "RCO"}, "", http.StatusCreated).ID
				closed := authRequest[domain.Team](t, f.admin, http.MethodPost, teams, map[string]any{"name": "Closed", "key": "RCC", "private": true}, "", http.StatusCreated).ID
				openIssue := authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records", map[string]any{"title": "Open issue", "teamId": open}, workspace, http.StatusCreated)
				secretIssue := authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records", map[string]any{"title": "Closed issue", "teamId": closed}, workspace, http.StatusCreated)
				ci := func(key, path string, input any, status int) releaseCIResult {
					t.Helper()
					return releaseCICall[releaseCIResult](t, f.host.Config.Handler, http.MethodPost, path, key, input, status)
				}
				for _, teamIDs := range [][]string{nil, {open}} {
					body := map[string]any{"name": fmt.Sprintf("Scoped %d", len(teamIDs)), "type": "scheduled"}
					if teamIDs != nil {
						body["teamIds"] = teamIDs
					}
					pipeline := authRequest[domain.ReleasePipeline](t, f.admin, http.MethodPost, f.host.URL+"/api/release-pipelines", body, workspace, http.StatusCreated)
					if teamIDs != nil && !slices.Equal(pipeline.TeamIDs, teamIDs) {
						authRequest[domain.ReleasePipeline](t, f.admin, http.MethodPatch, f.host.URL+"/api/release-pipelines/"+pipeline.ID, map[string]any{"teamIds": teamIDs}, workspace, http.StatusOK)
					}
					key := authRequest[releasePipelineAccessKey](t, f.admin, http.MethodPost, f.host.URL+"/api/release-pipelines/"+pipeline.ID+"/access-key", nil, workspace, http.StatusCreated)
					result := ci(key.Secret, "/api/release-ci/sync", map[string]any{
						"version":         "1.0.0",
						"issueReferences": []map[string]string{{"identifier": secretIssue.Identifier}, {"identifier": openIssue.Identifier}, {"identifier": "NOPE-999"}},
					}, http.StatusCreated)
					if result.Release.IssueCount != 1 || !slices.Equal(result.UnknownIssueIdentifiers, []string{secretIssue.Identifier, "NOPE-999"}) || len(result.SkippedIssueIdentifiers) != 0 {
						t.Fatalf("teams %v sync = %#v", teamIDs, result)
					}
					done := ci(key.Secret, "/api/release-ci/complete", map[string]any{"version": "1.0.0", "issueReferences": []map[string]string{{"identifier": secretIssue.Identifier}}}, http.StatusOK)
					if slices.Contains(authRequest[domain.Release](t, f.admin, http.MethodGet, f.host.URL+"/api/releases/"+done.Release.ID, nil, workspace, http.StatusOK).IssueIDs, secretIssue.ID) || !slices.Equal(done.UnknownIssueIdentifiers, []string{secretIssue.Identifier}) {
						t.Fatalf("teams %v complete = %#v", teamIDs, done)
					}
				}
				// A revert naming an out-of-scope issue unlinks nothing.
				pipeline := authRequest[domain.ReleasePipeline](t, f.admin, http.MethodPost, f.host.URL+"/api/release-pipelines", map[string]any{"name": "Revert", "type": "scheduled"}, workspace, http.StatusCreated)
				key := authRequest[releasePipelineAccessKey](t, f.admin, http.MethodPost, f.host.URL+"/api/release-pipelines/"+pipeline.ID+"/access-key", nil, workspace, http.StatusCreated)
				created := ci(key.Secret, "/api/release-ci/sync", map[string]any{"version": "2.0.0"}, http.StatusCreated)
				authRequest[domain.Release](t, f.admin, http.MethodPatch, f.host.URL+"/api/releases/"+created.Release.ID, map[string]any{"issueIds": []string{secretIssue.ID}}, workspace, http.StatusOK)
				reverted := ci(key.Secret, "/api/release-ci/sync", map[string]any{"version": "2.0.0", "revertedIssueReferences": []map[string]string{{"identifier": secretIssue.Identifier}}}, http.StatusOK)
				if reverted.Release.IssueCount != 1 {
					t.Fatalf("an out-of-scope revert changed the release: %#v", reverted)
				}
			}
		})
	}
}
