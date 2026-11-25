package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"runtime"
	"runtime/pprof"
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
}

func sw244Routes(stateID, assigneeID string) []sw244Route {
	return []sw244Route{
		{"comment", "POST", "/comments", `{"body":"A plain comment"}`, 201},
		{"priority", "PATCH", "", `{"priority":2}`, 200},
		{"state", "PATCH", "", fmt.Sprintf(`{"stateId":%q}`, stateID), 200},
		{"assignee", "PATCH", "", fmt.Sprintf(`{"assigneeId":%q}`, assigneeID), 200},
		{"title", "PATCH", "", `{"title":"Changed title"}`, 200},
		{"delete", "DELETE", "", "", 204},
		{"share", "POST", "/share", "", 200},
		{"unshare", "DELETE", "/share", "", 204},
		{"replace-permissions", "PUT", "/permissions", `{"permissions":[{"subjectType":"workspace","role":"viewer"}]}`, 200},
		{"edit-permission", "PATCH", "/permissions/sw244-permission", `{"role":"commenter"}`, 200},
		{"delete-permission", "DELETE", "/permissions/sw244-permission", "", 204},
		{"issue-reaction", "POST", "/reactions", `{"emoji":"thumbsup"}`, 200},
		{"edit-comment", "PATCH", "/comments/@comment", `{"body":"Edited comment"}`, 200},
		{"delete-comment", "DELETE", "/comments/@comment", "", 204},
		{"comment-reaction", "POST", "/comments/@comment/reactions", `{"emoji":"thumbsup"}`, 200},
		{"subscribe", "PUT", "/comments/@comment/subscription", `{"state":"subscribed"}`, 200},
		{"unsubscribe", "DELETE", "/comments/@comment/subscription", "", 204},
		{"delete-relation", "DELETE", "/relations/missing", "", 404},
		{"link", "POST", "/links", `{"title":"Example","url":"https://example.test/"}`, 201},
		{"delete-attachment", "DELETE", "/attachments/sw244-link", "", 204},
		{"reminder", "POST", "/reminders", fmt.Sprintf(`{"remindAt":%q}`, time.Now().Add(time.Hour).UTC().Format(time.RFC3339)), 201},
		{"loop-run", "POST", "/loop-runs", `{"prompt":"Review the issue"}`, 201},
		{"releases", "PUT", "/releases", `{"releaseIds":["sw244-release"]}`, 200},
		{"resolve-action", "POST", "/comment-actions/missing", `{"status":"dismissed"}`, 403},
		{"refresh-suggestions", "POST", "/suggestions/refresh", "", 400},
		{"accept-suggestion", "POST", "/suggestions/missing/accept", "", 404},
		{"dismiss-suggestion", "POST", "/suggestions/missing/dismiss", "", 404},
		{"read-actions", "GET", "/comment-actions", "", 200},
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
	var before []uint64
	for _, projects := range []int{0, 100} {
		var stateID string
		err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw244_seed", "", nil, func(data *domain.Bootstrap) error {
			settings := data.TeamSettings[team.ID]
			settings.IssueSharingEnabled = true
			data.TeamSettings[team.ID] = settings
			data.WorkspaceSettings.FeatureFlags = map[string]bool{"releases": true, "loops": true, "triage-intelligence": true}
			data.Releases = []domain.Release{{ID: "sw244-release", Name: "Release"}}
			stateID = data.States[1].ID
			data.Projects = make([]domain.Project, projects)
			for i := range data.Projects {
				project := &data.Projects[i]
				project.ID, project.Name, project.TeamIDs = fmt.Sprintf("sw244-project-%03d", i), "Unrelated project", []string{team.ID}
				for _, series := range []*[]domain.ProjectProgressHistoryPoint{
					&project.IssueCountHistory, &project.ScopeHistory, &project.CompletedScopeHistory, &project.InProgressScopeHistory, &project.ProgressHistory,
				} {
					*series = make([]domain.ProjectProgressHistoryPoint, 400)
				}
			}
			for route := range sw244Routes(stateID, owner.ID) {
				for sample := range samples {
					id := fmt.Sprintf("sw244-%d-%d-%d", projects, route, sample)
					stamp := time.Now().UTC()
					data.Issues = append(data.Issues, domain.Issue{
						ID: id, Identifier: fmt.Sprintf("SW244-%d", 10000*projects+100*route+sample+1),
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
		// The test measures one authenticated HTTP request on a fresh issue
		// each time. Seeding, fixture cleanup and garbage collection are outside
		// the allocation samples. Use bytes, independent of SQL latency.
		for index, route := range sw244Routes(stateID, owner.ID) {
			var bytes [samples]uint64
			admitted := f.service.projectionGate.served.Load()
			for sample := range samples {
				id := fmt.Sprintf("sw244-%d-%d-%d", projects, index, sample)
				var from, to runtime.MemStats
				profile := projects > 0 && sample == 0 && os.Getenv("FLOW_SW244_PROFILE") == route.name
				if profile {
					runtime.MemProfileRate = 4096
					runtime.GC()
					f, _ := os.Create(os.Getenv("FLOW_SW244_PROFILE_DIR") + "/base.pprof")
					_ = pprof.Lookup("allocs").WriteTo(f, 0)
					_ = f.Close()
				}
				runtime.ReadMemStats(&from)
				response := sw244Send(handler, workspace, cookies, "", route, id)
				runtime.ReadMemStats(&to)
				if profile {
					runtime.GC()
					f, _ := os.Create(os.Getenv("FLOW_SW244_PROFILE_DIR") + "/after.pprof")
					_ = pprof.Lookup("allocs").WriteTo(f, 0)
					_ = f.Close()
				}
				bytes[sample] = to.TotalAlloc - from.TotalAlloc
				if response.Code != route.status {
					t.Errorf("%s: HTTP %d, want %d (%s)", route.name, response.Code, route.status, response.Body.String())
				}
			}
			slices.Sort(bytes[:])
			median := bytes[samples/2]
			uses := f.service.projectionGate.served.Load() - admitted
			t.Logf("%-20s projects=%3d bytes/op=%9d admissions=%d", route.name, projects, median, uses)
			if projects == 0 {
				before = append(before, median)
			} else if os.Getenv("FLOW_SW244_BASELINE") != "1" && median > before[index]+64<<10 {
				t.Errorf("%s allocated %d extra bytes with unrelated projects and histories", route.name, median-before[index])
			}
		}
	}
}
