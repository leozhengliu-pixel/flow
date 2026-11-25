package main

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW187IssueRecordAuthorizationDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		other := scaleP03Fixture(t, repo)
		f := scaleP21M2Setup(t, repo)
		workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw187-")
		publicID, childID := prefix+"issue", prefix+"child"
		projectID, cycleID := prefix+"project", prefix+"cycle"
		var privateIssue domain.Issue
		var publicTeam domain.Team
		restore := store.ForceFullMutationsForTesting(true)
		err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw187_seed", "", nil, func(data *domain.Bootstrap) error {
			for _, issue := range data.Issues {
				if issue.Team.Private {
					privateIssue = issue
					break
				}
			}
			for _, team := range data.Teams {
				if team.ID == f.public {
					publicTeam = team
				}
			}
			public := domain.Issue{
				ID: publicID, Identifier: "SW187-701", PreviousIdentifiers: []string{"SW187OLD-701"},
				Team: publicTeam, Creator: f.principals[2].user, State: data.States[0],
				Description: strings.Repeat("unneeded authorization body ", 12000),
			}
			child := public
			child.ID, child.Identifier, child.PreviousIdentifiers = childID, "SW187-702", nil
			child.Team, child.ParentID = privateIssue.Team, &privateIssue.ID
			data.Issues = append(data.Issues, public, child)
			data.Projects = append(data.Projects, domain.Project{ID: projectID, TeamIDs: []string{f.public}, Description: strings.Repeat("unneeded project body ", 18000)})
			data.Cycles = append(data.Cycles, domain.Cycle{ID: cycleID, TeamID: f.public})
			data.ReleasePipelines = append(data.ReleasePipelines,
				domain.ReleasePipeline{ID: prefix + "public-pipeline", TeamIDs: []string{f.public}},
				domain.ReleasePipeline{ID: prefix + "private-pipeline", TeamIDs: []string{privateIssue.Team.ID}},
				domain.ReleasePipeline{ID: prefix + "mixed-pipeline", TeamIDs: []string{privateIssue.Team.ID, f.public}},
				domain.ReleasePipeline{ID: prefix + "unscoped-pipeline"},
			)
			for _, name := range []string{"public", "private", "mixed", "unscoped", "missing"} {
				data.Releases = append(data.Releases, domain.Release{
					ID: prefix + name + "-release", PipelineID: prefix + name + "-pipeline",
					Description: strings.Repeat("unneeded release body ", 16000),
				})
			}
			data.Releases = append(data.Releases, domain.Release{ID: prefix + "standalone-release"})
			return nil
		})
		restore()
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			request := scaleP21SW47Request(f, f.data.Viewer, nil, http.MethodDelete, "/api/issue-records/"+publicID, publicID, "", nil)
			request.Header.Set("X-Workspace-Key", other.data.Workspace.URLKey)
			outcome, release := scaleP21SW47Call(t, source, request, other.data.Workspace.URLKey, f.data.Viewer, "")
			if outcome.Allowed {
				t.Fatal("authorization crossed a workspace membership boundary")
			}
			release()
		}
		tests := []struct {
			name, method, ref, tail string
			body                    any
		}{
			{"delete", http.MethodDelete, publicID, "", nil},
			{"share", http.MethodPost, publicID, "/share", nil},
			{"unshare", http.MethodDelete, publicID, "/share", nil},
			{"permissions", http.MethodPut, privateIssue.ID, "/permissions", map[string]any{"permissions": []any{}}},
			{"inherited-reminder", http.MethodPost, childID, "/reminders", nil},
			{"loop", http.MethodPost, publicID, "/loop-runs", nil},
			{"refresh", http.MethodPost, publicID, "/suggestions/refresh", nil},
			{"accept", http.MethodPost, publicID, "/suggestions/missing/accept", nil},
			{"comment-action", http.MethodPost, publicID, "/comment-actions/missing", nil},
			{"update-project", http.MethodPatch, publicID, "", map[string]any{"projectId": projectID}},
			{"update-private-project", http.MethodPatch, publicID, "", map[string]any{"projectId": "m2-unrelated"}},
			{"update-cycle", http.MethodPatch, publicID, "", map[string]any{"cycleId": cycleID}},
			{"update-assignee", http.MethodPatch, publicID, "", map[string]any{"assigneeId": f.principals[2].user.ID}},
			{"update-foreign", http.MethodPatch, publicID, "", map[string]any{"projectId": prefix + "foreign", "cycleId": prefix + "foreign", "assigneeId": prefix + "foreign"}},
			{"batch-targets", http.MethodPost, "batch", "", domain.BatchIssueUpdateInput{IssueIDs: []string{publicID}, Update: domain.IssueUpdateInput{ProjectID: &projectID, CycleID: &cycleID}}},
			{"batch-private", http.MethodPost, "batch", "", domain.BatchIssueUpdateInput{IssueIDs: []string{privateIssue.ID, childID}, Update: domain.IssueUpdateInput{AssigneeID: &f.principals[2].user.ID}}},
			{"old-identifier", http.MethodDelete, "sw187old-701", "", nil},
			{"missing", http.MethodDelete, prefix + "missing", "", nil},
		}
		for _, name := range []string{"public", "private", "mixed", "unscoped", "missing", "standalone"} {
			tests = append(tests, struct {
				name, method, ref, tail string
				body                    any
			}{"release-" + name, http.MethodPut, publicID, "/releases", map[string]any{"releaseIds": []string{prefix + name + "-release"}}})
		}
		for _, reload := range []bool{false, true} {
			if reload {
				if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
				t.Fatal(err)
			}
			t.Run(fmt.Sprintf("reload=%t", reload), func(t *testing.T) {
				f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
					for _, test := range tests {
						path := "/api/issue-records/" + test.ref + test.tail
						oldRequest := scaleP21SW47Request(f, user, key, test.method, path, test.ref, "", test.body)
						newRequest := scaleP21SW47Request(f, user, key, test.method, path, test.ref, "", test.body)
						if !reload && user.ID == f.data.Viewer.ID && key == nil && test.name == "update-project" {
							oldRequest.Header.Set("X-SW47-Measure", "sw187-project")
							newRequest.Header.Set("X-SW47-Measure", "sw187-project")
						}
						old, closeOld := scaleP21SW47Call(t, repo, oldRequest, workspace, user, "")
						bounded, closeNew := scaleP21SW47Call(t, f.probe, newRequest, workspace, user, "")
						if !reflect.DeepEqual(old, bounded) {
							t.Fatalf("%s authorization differs: old=%+v bounded=%+v", test.name, old, bounded)
						}
						closeOld()
						closeNew()
						if f.probe.MetadataWorkUsedForTesting() != 0 || f.probe.WorkspaceMetadataDecodes() != 0 {
							t.Fatal("general authorization leaked work or decoded complete metadata")
						}
					}
				})
			})
		}
		// A generation read must be retained until the request closes, even
		// when middleware's one-shot handoff is never consumed.
		request := scaleP21SW47Request(f, f.data.Viewer, nil, http.MethodPatch, "/api/issue-records/"+publicID, publicID, "", map[string]any{"assigneeId": f.principals[2].user.ID})
		request, closeOutput := beginRequestResourceReads(request)
		service := &server{store: f.probe}
		if !service.resourceAllowedLegacy(request, workspace, f.data.Viewer.ID) || f.probe.MetadataWorkUsedForTesting() == 0 {
			t.Fatal("general authorization lacks a retained output owner")
		}
		cached := request.Context().Value(issueRecordAuthorizationKey{}).(*issueRecordAuthorization)
		closeOutput()
		closeOutput()
		if f.probe.MetadataWorkUsedForTesting() != 0 || !cached.taken.Load() || !reflect.DeepEqual(cached.data, domain.Bootstrap{}) {
			t.Fatal("general authorization retained its closed output")
		}
		canceled, cancel := context.WithCancel(t.Context())
		cancel()
		request = scaleP21SW47Request(f, f.data.Viewer, nil, http.MethodPatch, "/api/issue-records/"+publicID, publicID, "", map[string]any{"projectId": projectID})
		request = request.WithContext(context.WithValue(canceled, authUserContextKey{}, f.data.Viewer))
		request, closeOutput = beginRequestResourceReads(request)
		if service.resourceAllowedLegacy(request, workspace, f.data.Viewer.ID) {
			t.Fatal("canceled general authorization succeeded")
		}
		closeOutput()
		if f.probe.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("canceled general authorization leaked its owner")
		}
		unowned := scaleP21SW47Request(f, f.data.Viewer, nil, http.MethodPatch, "/api/issue-records/"+publicID, publicID, "", map[string]any{"projectId": projectID})
		if _, err := service.buildIssueRecordAuthorizationData(unowned); err == nil || f.probe.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("general authorization accepted or leaked an unowned output", err)
		}
		for _, test := range tests {
			if test.name != "update-project" && test.name != "update-cycle" && test.name != "update-assignee" && test.name != "release-missing" {
				continue
			}
			path := "/api/issue-records/" + test.ref + test.tail
			var expected []scaleP21SW47IssueHeader
			for _, source := range []*store.SQLiteStore{repo, f.probe} {
				request := scaleP21SW47Request(f, f.data.Viewer, nil, test.method, path, test.ref, "", test.body)
				request, release := beginRequestResourceReads(request)
				development := &server{store: source, authDisabled: true}
				actual, err := development.buildIssueRecordAuthorizationData(request)
				if err != nil {
					release()
					t.Fatal("development authorization", test.name, err)
				}
				if source == repo {
					expected = scaleP21SW47Headers(development, actual)
				} else if !reflect.DeepEqual(expected, scaleP21SW47Headers(development, actual)) {
					release()
					t.Fatal("development authorization headers differ", test.name)
				}
				release()
			}
		}
		if f.probe.MetadataWorkUsedForTesting() != 0 || f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("development authorization leaked work or decoded complete metadata")
		}
		// Exact release membership is an existence projection. Payload fields
		// and hidden references never enter the authorization result.
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			expected, err := repo.PagedWorkspaceMetadata(t.Context(), workspace, user.ID)
			if err != nil {
				t.Fatal(err)
			}
			filterBootstrapForAPIKey(&expected, f.literalRequest(user, key, nil))
			installed, found, err := f.probe.BeginInstalledMetadataRead(t.Context(), workspace)
			if err != nil || !found {
				t.Fatal("release generation", found, err)
			}
			defer installed.Close()
			scope, err := installed.ViewerProjectionScope(t.Context(), user.ID)
			if err != nil {
				t.Fatal(err)
			}
			var allows func(string) bool
			if key != nil && apiKeyTeamRestrictionSelected(*key) {
				allows = func(id string) bool { return slices.Contains(key.TeamIDs, id) }
			}
			for _, name := range []string{"public", "private", "mixed", "unscoped", "missing", "standalone"} {
				id := prefix + name + "-release"
				actual, err := installed.IssueAuthorizationReleaseVisible(t.Context(), &scope, id, allows)
				want := slices.ContainsFunc(expected.Releases, func(release domain.Release) bool { return release.ID == id })
				if err != nil || actual != want {
					t.Fatal("release existence projection", name, actual, want, err)
				}
			}
		})
	}
}
