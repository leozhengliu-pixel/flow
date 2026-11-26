package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type sw244Route struct {
	name, method, tail, body string
	status                   int
	// scoped: neither authorization nor the handler copies an unrelated
	// project, so allocation stays flat as projects are added.
	scoped bool
}

func sw244Routes(stateID, assigneeID string) []sw244Route {
	return []sw244Route{
		{"comment", "POST", "/comments", `{"body":"A plain comment"}`, 201, true},
		{"priority", "PATCH", "", `{"priority":2}`, 200, false},
		{"state", "PATCH", "", fmt.Sprintf(`{"stateId":%q}`, stateID), 200, false},
		{"assignee", "PATCH", "", fmt.Sprintf(`{"assigneeId":%q}`, assigneeID), 200, false},
		{"title", "PATCH", "", `{"title":"Changed title"}`, 200, false},
		{"delete", "DELETE", "", "", 204, false},
		{"share", "POST", "/share", "", 200, false},
		{"unshare", "DELETE", "/share", "", 204, false},
		{"replace-permissions", "PUT", "/permissions", `{"permissions":[{"subjectType":"workspace","role":"viewer"}]}`, 200, false},
		{"edit-permission", "PATCH", "/permissions/sw244-permission", `{"role":"commenter"}`, 200, false},
		{"delete-permission", "DELETE", "/permissions/sw244-permission", "", 204, false},
		{"issue-reaction", "POST", "/reactions", `{"emoji":"thumbsup"}`, 200, false},
		{"edit-comment", "PATCH", "/comments/@comment", `{"body":"Edited comment"}`, 200, true},
		{"delete-comment", "DELETE", "/comments/@comment", "", 204, true},
		{"comment-reaction", "POST", "/comments/@comment/reactions", `{"emoji":"thumbsup"}`, 200, true},
		{"subscribe", "PUT", "/comments/@comment/subscription", `{"state":"subscribed"}`, 200, true},
		{"unsubscribe", "DELETE", "/comments/@comment/subscription", "", 204, true},
		{"delete-relation", "DELETE", "/relations/missing", "", 404, false},
		{"link", "POST", "/links", `{"title":"Example","url":"https://example.test/"}`, 201, false},
		{"delete-attachment", "DELETE", "/attachments/sw244-link", "", 204, false},
		{"reminder", "POST", "/reminders", fmt.Sprintf(`{"remindAt":%q}`, time.Now().Add(time.Hour).UTC().Format(time.RFC3339)), 201, false},
		{"loop-run", "POST", "/loop-runs", `{"prompt":"Review the issue"}`, 201, false},
		{"releases", "PUT", "/releases", `{"releaseIds":["sw244-release"]}`, 200, false},
		{"resolve-action", "POST", "/comment-actions/missing", `{"status":"dismissed"}`, 403, false},
		{"refresh-suggestions", "POST", "/suggestions/refresh", "", 400, false},
		{"accept-suggestion", "POST", "/suggestions/missing/accept", "", 404, false},
		{"dismiss-suggestion", "POST", "/suggestions/missing/dismiss", "", 404, false},
		{"read-actions", "GET", "/comment-actions", "", 200, true},
	}
}

func sw244Send(handler http.Handler, workspace string, cookies []*http.Cookie, bearer string, route sw244Route, id string) *httptest.ResponseRecorder {
	tail := strings.ReplaceAll(route.tail, "@comment", id+"-comment")
	r := httptest.NewRequest(route.method, "/api/issues/"+id+tail, strings.NewReader(route.body))
	r.Header.Set("X-Workspace-Key", workspace)
	if route.body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	for _, cookie := range cookies {
		r.AddCookie(cookie)
	}
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestScaleP21SW244IssueWriteAllocations(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_NARROW_MUTATION_INPUTS", "on")
	// Measure the production write path, without the test-only comparison of
	// narrowed callback inputs against full copies.
	t.Setenv("FLOW_NARROW_MUTATION_VERIFY", "off")
	repo, err := store.OpenSQLite(t.TempDir() + "/sw244.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	f := scaleP03Fixture(t, repo)
	workspace, owner, team := f.data.Workspace.URLKey, f.data.Viewer, f.data.Teams[0]
	base, err := url.Parse(f.host.URL)
	if err != nil {
		t.Fatal(err)
	}
	handler, cookies := f.host.Config.Handler, f.admin.Jar.Cookies(base)
	const samples = 7
	// Three phases on the same fixture: no projects; 100 unrelated projects
	// without progress history; the same projects with 400 points in each of
	// their five histories (200,000 points).
	type phase struct {
		name             string
		projects, points int
	}
	phases := []phase{{"empty", 0, 0}, {"projects", 100, 0}, {"histories", 100, 400}}
	measured := map[string][]uint64{}
	for phaseIndex, current := range phases {
		var stateID string
		err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw244_seed", "", nil, func(data *domain.Bootstrap) error {
			settings := data.TeamSettings[team.ID]
			settings.IssueSharingEnabled = true
			data.TeamSettings[team.ID] = settings
			data.WorkspaceSettings.FeatureFlags = map[string]bool{"releases": true, "loops": true, "triage-intelligence": true}
			data.Releases = []domain.Release{{ID: "sw244-release", Name: "Release"}}
			stateID = data.States[1].ID
			data.Projects = make([]domain.Project, current.projects)
			for i := range data.Projects {
				project := &data.Projects[i]
				project.ID, project.Name, project.TeamIDs = fmt.Sprintf("sw244-project-%03d", i), "Unrelated project", []string{team.ID}
				for _, series := range []*[]domain.ProjectProgressHistoryPoint{
					&project.IssueCountHistory, &project.ScopeHistory, &project.CompletedScopeHistory, &project.InProgressScopeHistory, &project.ProgressHistory,
				} {
					if current.points > 0 {
						*series = make([]domain.ProjectProgressHistoryPoint, current.points)
					}
				}
			}
			for route := range sw244Routes(stateID, owner.ID) {
				for sample := range samples {
					id := fmt.Sprintf("sw244-%d-%d-%d", phaseIndex, route, sample)
					stamp := time.Now().UTC()
					data.Issues = append(data.Issues, domain.Issue{
						ID: id, Identifier: fmt.Sprintf("SW244-%d", 10000*phaseIndex+100*route+sample+1),
						Title: "Initial title", Team: team, Creator: owner, State: data.States[0],
						Version: 1, CreatedAt: stamp, UpdatedAt: stamp,
						Attachments: []domain.Attachment{{ID: "sw244-link", IssueID: id, URL: "https://example.test/"}},
						Permissions: []domain.IssuePermission{{ID: "sw244-permission", IssueID: id, SubjectType: "workspace", Role: "viewer"}},
					})
					data.Comments[id] = []domain.Comment{{ID: id + "-comment", Body: "Initial comment", User: owner, Version: 1, CreatedAt: stamp}}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		// One authenticated HTTP request on a fresh issue per sample. Seeding
		// and garbage collection are outside the samples; bytes, not time.
		for index, route := range sw244Routes(stateID, owner.ID) {
			var bytes [samples]uint64
			admitted := f.service.projectionGate.served.Load()
			for sample := range samples {
				id := fmt.Sprintf("sw244-%d-%d-%d", phaseIndex, index, sample)
				var from, to runtime.MemStats
				// Two collections empty every sync.Pool (the first moves its
				// objects to the victim cache, the second drops them): each
				// sample then allocates its pooled buffers itself. After a
				// single collection a sample reused them or not depending on
				// the collections the previous request triggered, which moved
				// a route's median by about 48 KiB between runs.
				runtime.GC()
				runtime.GC()
				runtime.ReadMemStats(&from)
				response := sw244Send(handler, workspace, cookies, "", route, id)
				runtime.ReadMemStats(&to)
				bytes[sample] = to.TotalAlloc - from.TotalAlloc
				if response.Code != route.status {
					t.Errorf("%s: HTTP %d, want %d (%s)", route.name, response.Code, route.status, response.Body.String())
				}
			}
			slices.Sort(bytes[:])
			median := bytes[samples/2]
			measured[route.name] = append(measured[route.name], median)
			t.Logf("%-20s %-9s bytes/op=%10d admissions=%d", route.name, current.name, median, f.service.projectionGate.served.Load()-admitted)
		}
	}
	if os.Getenv("FLOW_SW244_BASELINE") == "1" {
		return
	}
	// A copy of the histories is 28 MiB here; the slack absorbs allocator
	// and map growth noise between phases.
	const slack, historySlack = 64 << 10, 256 << 10
	for _, route := range sw244Routes("", "") {
		empty, projects, histories := measured[route.name][0], measured[route.name][1], measured[route.name][2]
		// No route copies the unrelated progress histories.
		if histories > projects+historySlack {
			t.Errorf("%s allocated %d more bytes once unrelated projects had progress histories", route.name, histories-projects)
		}
		// Scoped routes copy no unrelated project at all.
		if route.scoped && histories > empty+slack {
			t.Errorf("%s allocated %d more bytes with 100 unrelated projects", route.name, histories-empty)
		}
	}
}
