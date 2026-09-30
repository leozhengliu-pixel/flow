package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"runtime/metrics"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// TestCommonMutationsAtScale times everyday edits (projects, milestones,
// project updates, resources, comments, documents, views, labels, cycles,
// initiatives, team settings, favorites and issues) through the HTTP handler
// against a workspace with 100,000 issues. Opt-in:
//
//	FLOW_SCALE_TEST=1 go test ./cmd/server -run TestCommonMutationsAtScale -v
//
// FLOW_SCALE_ISSUES overrides the issue count; FLOW_SCALE_AUTH=1 also runs the
// authenticated pass.
func TestCommonMutationsAtScale(t *testing.T) {
	if os.Getenv("FLOW_SCALE_TEST") != "1" {
		t.Skip("set FLOW_SCALE_TEST=1 to run the 100k-issue mutation timing test")
	}
	count := 100000
	if value, err := strconv.Atoi(os.Getenv("FLOW_SCALE_ISSUES")); err == nil && value > 0 {
		count = value
	}
	modes := []bool{false}
	if os.Getenv("FLOW_SCALE_AUTH") == "1" {
		modes = append(modes, true)
	}
	for _, auth := range modes {
		name := "authDisabled"
		if auth {
			name = "authEnabled"
		}
		t.Run(name, func(t *testing.T) { runMutationScale(t, count, auth) })
	}
}

type scaleTiming struct {
	label     string
	elapsed   time.Duration
	status    int
	allocated uint64
	peak      uint64
}

// heapObjectBytes reads the live heap object size without stopping the world.
func heapObjectBytes() uint64 {
	sample := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
	metrics.Read(sample)
	return sample[0].Value.Uint64()
}

func totalAllocBytes() uint64 {
	sample := []metrics.Sample{{Name: "/gc/heap/allocs:bytes"}}
	metrics.Read(sample)
	return sample[0].Value.Uint64()
}

// measureRequest runs call while sampling the heap, returning the bytes
// allocated during the call and the peak heap growth above the pre-call level.
func measureRequest(call func()) (time.Duration, uint64, uint64) {
	runtime.GC()
	base := heapObjectBytes()
	allocBefore := totalAllocBytes()
	var peak atomic.Uint64
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			if value := heapObjectBytes(); value > peak.Load() {
				peak.Store(value)
			}
			select {
			case <-done:
				return
			case <-ticker.C:
			}
		}
	}()
	begin := time.Now()
	call()
	elapsed := time.Since(begin)
	close(done)
	<-stopped
	if value := heapObjectBytes(); value > peak.Load() {
		peak.Store(value)
	}
	grown := uint64(0)
	if peak.Load() > base {
		grown = peak.Load() - base
	}
	return elapsed, totalAllocBytes() - allocBefore, grown
}

func runMutationScale(t *testing.T, count int, auth bool) {
	path := filepath.Join(t.TempDir(), "flow.db")
	repository, err := store.OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	fixture := repository.Bootstrap()
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	seedScaleIssues(t, path, count)
	// Put a fifth of the issues in the fixture's first project so project
	// progress maintenance is measured against a large project.
	project := fixture.Projects[0]
	func() {
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		summary := fmt.Sprintf(`{"id":%q,"name":%q,"color":%q}`, project.ID, project.Name, project.Color)
		if _, err := db.Exec(`UPDATE issue_records SET project_id=?, data=json_set(data,'$.project',json(?)) WHERE workspace_key='test-workspace' AND id LIKE 'scale_%' AND CAST(substr(id,7) AS INTEGER) <= ?`, project.ID, summary, count/5); err != nil {
			t.Fatal(err)
		}
	}()
	repository, err = store.OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	t.Logf("seeded %d issues and reopened in %s", count, time.Since(started).Round(time.Millisecond))
	if auth {
		t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	}
	api := httptest.NewServer(newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: !auth}))
	defer api.Close()
	client := authClient(t)
	if auth {
		authRequest[domain.AuthSession](t, client, http.MethodPost, api.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	}
	timings := []scaleTiming{}
	timed := func(label, method, url string, input any) map[string]any {
		t.Helper()
		var body io.Reader
		if input != nil {
			raw, _ := json.Marshal(input)
			body = bytes.NewReader(raw)
		}
		request, _ := http.NewRequest(method, api.URL+url, body)
		request.Header.Set("X-Workspace-Key", "test-workspace")
		if input != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		var response *http.Response
		var raw []byte
		elapsed, allocated, peak := measureRequest(func() {
			var err error
			response, err = client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ = io.ReadAll(response.Body)
			response.Body.Close()
		})
		timings = append(timings, scaleTiming{label: label, elapsed: elapsed, status: response.StatusCode, allocated: allocated, peak: peak})
		if response.StatusCode >= 300 {
			t.Errorf("%s: status %d: %.300s", label, response.StatusCode, raw)
			return map[string]any{}
		}
		var decoded any
		_ = json.Unmarshal(raw, &decoded)
		if object, ok := decoded.(map[string]any); ok {
			return object
		}
		return map[string]any{"_list": decoded}
	}
	id := func(value map[string]any) string {
		text, _ := value["id"].(string)
		return text
	}

	team := fixture.Teams[0]
	user := fixture.Users[0]
	projectPath := "/api/projects/" + project.ID
	status := project.Status.ID
	if len(fixture.ProjectStatuses) > 1 {
		status = fixture.ProjectStatuses[1].ID
	}
	labelIDs := []string{}
	for _, label := range fixture.Labels {
		if len(labelIDs) < 1 && (label.ResourceType == "" || label.ResourceType == "project") {
			labelIDs = append(labelIDs, label.ID)
		}
	}
	stateID := ""
	if states, ok := timed("GET team states", http.MethodGet, "/api/teams/"+team.ID+"/states", nil)["_list"].([]any); ok {
		for _, item := range states {
			if state, ok := item.(map[string]any); ok && state["type"] == "started" && stateID == "" {
				stateID, _ = state["id"].(string)
			}
		}
	}
	issueLabel := ""
	for _, label := range fixture.Labels {
		if label.ResourceType == "" || label.ResourceType == "issue" {
			issueLabel = label.ID
			break
		}
	}

	// Projects.
	timed("project icon", http.MethodPatch, projectPath, map[string]any{"icon": "Rocket"})
	timed("project color", http.MethodPatch, projectPath, map[string]any{"color": "#4ea7fc"})
	timed("project name", http.MethodPatch, projectPath, map[string]any{"name": project.Name + " renamed"})
	timed("project status", http.MethodPatch, projectPath, map[string]any{"statusId": status})
	timed("project lead", http.MethodPatch, projectPath, map[string]any{"leadId": user.ID})
	timed("project dates", http.MethodPatch, projectPath, map[string]any{"startDate": "2026-10-01", "targetDate": "2026-12-01"})
	timed("project labels", http.MethodPatch, projectPath, map[string]any{"labelIds": labelIDs})
	timed("project description", http.MethodPatch, projectPath, map[string]any{"description": "Updated description"})
	timed("project priority", http.MethodPatch, projectPath, map[string]any{"priority": 2})
	timed("project start date", http.MethodPatch, projectPath, map[string]any{"startDate": time.Now().UTC().AddDate(0, -2, 0).Format("2006-01-02")})

	// Milestones.
	milestone := timed("milestone create", http.MethodPost, projectPath+"/milestones", map[string]any{"name": "Scale milestone"})
	second := timed("milestone create (2)", http.MethodPost, projectPath+"/milestones", map[string]any{"name": "Scale milestone 2"})
	timed("milestone update", http.MethodPatch, projectPath+"/milestones/"+id(milestone), map[string]any{"name": "Scale milestone renamed", "targetDate": "2026-11-01"})
	ids := []string{id(second), id(milestone)}
	for index := len(project.Milestones) - 1; index >= 0; index-- {
		ids = append(ids, project.Milestones[index].ID)
	}
	timed("milestone reorder", http.MethodPost, projectPath+"/milestones/reorder", map[string]any{"ids": ids})
	timed("milestone delete", http.MethodDelete, projectPath+"/milestones/"+id(second), nil)

	// Project updates.
	update := timed("project update create", http.MethodPost, projectPath+"/updates", map[string]any{"body": "On track this week", "health": "onTrack"})
	timed("project update edit", http.MethodPatch, projectPath+"/updates/"+id(update), map[string]any{"body": "On track this week (edited)"})
	timed("project update comment", http.MethodPost, projectPath+"/updates/"+id(update)+"/comments", map[string]any{"body": "Nice"})
	timed("project update reaction", http.MethodPost, projectPath+"/updates/"+id(update)+"/reactions", map[string]any{"emoji": "👍"})
	throwaway := timed("project update create (2)", http.MethodPost, projectPath+"/updates", map[string]any{"body": "Temporary", "health": "atRisk"})
	timed("project update delete", http.MethodDelete, projectPath+"/updates/"+id(throwaway), nil)

	// Resources.
	resource := timed("resource create", http.MethodPost, projectPath+"/resources", map[string]any{"url": "https://example.com/spec", "title": "Spec"})
	timed("resource update", http.MethodPatch, projectPath+"/resources/"+id(resource), map[string]any{"title": "Spec v2"})
	timed("resource delete", http.MethodDelete, projectPath+"/resources/"+id(resource), nil)

	// Project comments.
	comment := timed("project comment", http.MethodPost, projectPath+"/comments", map[string]any{"body": "Looks good"})
	timed("project comment edit", http.MethodPatch, projectPath+"/comments/"+id(comment), map[string]any{"body": "Looks great"})
	timed("project comment reaction", http.MethodPost, projectPath+"/comments/"+id(comment)+"/reactions", map[string]any{"emoji": "🎉"})

	// Documents, views, labels.
	document := timed("document create", http.MethodPost, "/api/documents", map[string]any{"title": "Scale doc", "projectIds": []string{project.ID}})
	timed("document update", http.MethodPatch, "/api/documents/"+id(document), map[string]any{"title": "Scale doc v2", "content": "Hello"})
	view := timed("view create", http.MethodPost, "/api/views", map[string]any{"name": "Scale view", "resource": "issues", "scope": "workspace"})
	timed("view update", http.MethodPatch, "/api/views/"+id(view), map[string]any{"name": "Scale view 2"})
	label := timed("label create", http.MethodPost, "/api/labels", map[string]any{"name": "scale-label", "color": "#ff0000"})
	timed("label update", http.MethodPatch, "/api/labels/"+id(label), map[string]any{"color": "#00ff00"})

	// Cycles, initiatives, teams, favorites.
	if len(fixture.Cycles) > 0 {
		timed("cycle update", http.MethodPatch, "/api/cycles/"+fixture.Cycles[0].ID, map[string]any{"description": "Scale cycle"})
	}
	initiative := timed("initiative create", http.MethodPost, "/api/initiatives", map[string]any{"name": "Scale initiative"})
	timed("initiative update", http.MethodPatch, "/api/initiatives/"+id(initiative), map[string]any{"summary": "Scale initiative summary"})
	timed("team settings update", http.MethodPatch, "/api/teams/"+team.ID+"/settings", map[string]any{"description": "Scale team"})
	timed("account settings", http.MethodPatch, "/api/account/settings", map[string]any{"fontSize": "small"})
	timed("account settings (2)", http.MethodPatch, "/api/account/settings", map[string]any{"homeView": "inbox"})
	timed("notification preferences", http.MethodPatch, "/api/notification-preferences", map[string]any{"soundEnabled": false})
	timed("favorite add", http.MethodPut, "/api/favorites/project/"+project.ID, nil)
	timed("favorite remove", http.MethodDelete, "/api/favorites/project/"+project.ID, nil)

	// Issues (legacy /api/issues path used when paged issues are off).
	issue := timed("issue create", http.MethodPost, "/api/issues", map[string]any{"title": "Scale issue created", "teamId": team.ID})
	issuePath := "/api/issues/" + id(issue)
	timed("issue title", http.MethodPatch, issuePath, map[string]any{"title": "Scale issue renamed"})
	if stateID != "" {
		timed("issue status", http.MethodPatch, issuePath, map[string]any{"stateId": stateID})
	}
	timed("issue assignee", http.MethodPatch, issuePath, map[string]any{"assigneeId": user.ID})
	if issueLabel != "" {
		timed("issue labels", http.MethodPatch, issuePath, map[string]any{"labelIds": []string{issueLabel}})
	}
	timed("issue project", http.MethodPatch, issuePath, map[string]any{"projectId": project.ID})
	if len(fixture.Cycles) > 0 {
		timed("issue cycle", http.MethodPatch, issuePath, map[string]any{"cycleId": fixture.Cycles[0].ID})
	}
	timed("issue estimate", http.MethodPatch, issuePath, map[string]any{"estimate": 3})
	timed("issue priority", http.MethodPatch, issuePath, map[string]any{"priority": 2})
	timed("issue comment", http.MethodPost, issuePath+"/comments", map[string]any{"body": "Scale comment"})
	timed("issue-record title", http.MethodPatch, "/api/issue-records/"+id(issue), map[string]any{"title": "Scale issue renamed again"})
	timed("issue-record comment", http.MethodPost, "/api/issue-records/"+id(issue)+"/comments", map[string]any{"body": "Scale comment 2"})
	if stateID != "" {
		timed("issue-record status (big project)", http.MethodPatch, "/api/issue-records/scale_1", map[string]any{"stateId": stateID})
	}
	timed("issue-record estimate (big project)", http.MethodPatch, "/api/issue-records/scale_2", map[string]any{"estimate": 5})

	// Releases.
	pipeline := timed("pipeline create", http.MethodPost, "/api/release-pipelines", map[string]any{"name": "Scale pipeline"})
	release := timed("release create", http.MethodPost, "/api/releases", map[string]any{"name": "Scale release", "pipelineId": id(pipeline)})
	releasePath := "/api/releases/" + id(release)
	timed("release name", http.MethodPatch, releasePath, map[string]any{"name": "Scale release 1.0"})
	timed("release target date", http.MethodPatch, releasePath, map[string]any{"targetDate": "2026-12-15"})
	timed("release notes", http.MethodPatch, releasePath, map[string]any{"releaseNotes": "Fixes and features", "description": "First scale release"})
	timed("release stage", http.MethodPatch, releasePath, map[string]any{"stage": "In Progress"})
	timed("release add issues", http.MethodPatch, releasePath, map[string]any{"issueIds": []string{"scale_1", "scale_2", id(issue)}})
	timed("release remove issue", http.MethodPatch, releasePath, map[string]any{"issueIds": []string{"scale_1", id(issue)}})
	timed("issue set releases", http.MethodPut, issuePath+"/releases", map[string]any{"releaseIds": []string{id(release)}})
	timed("release note create", http.MethodPost, releasePath+"/notes", map[string]any{"title": "Notes", "body": "Body"})
	timed("GET releases", http.MethodGet, "/api/releases", nil)
	timed("GET release", http.MethodGet, releasePath, nil)
	timed("GET release history", http.MethodGet, releasePath+"/history", nil)
	timed("GET release notes", http.MethodGet, releasePath+"/notes", nil)
	timed("GET release pipelines", http.MethodGet, "/api/release-pipelines", nil)
	other := timed("release create (2)", http.MethodPost, "/api/releases", map[string]any{"name": "Scale release 2", "pipelineId": id(pipeline)})
	timed("release reorder", http.MethodPost, "/api/releases/reorder", map[string]any{"pipelineId": id(pipeline), "ids": []string{id(other), id(release)}})
	timed("release released", http.MethodPatch, releasePath, map[string]any{"stage": "Released"})
	timed("release delete", http.MethodDelete, "/api/releases/"+id(other), nil)
	timed("GET issue-records/bootstrap", http.MethodGet, "/api/issue-records/bootstrap", nil)
	if os.Getenv("FLOW_SCALE_FULL_BOOTSTRAP") == "1" {
		timed("GET bootstrap (full)", http.MethodGet, "/api/bootstrap", nil)
	}

	var summary strings.Builder
	for _, timing := range timings {
		fmt.Fprintf(&summary, "\n  %-30s %9.1f ms %9.1f MB alloc %9.1f MB peak  %d", timing.label, float64(timing.elapsed.Microseconds())/1000, float64(timing.allocated)/(1<<20), float64(timing.peak)/(1<<20), timing.status)
	}
	t.Logf("mutation timings at %d issues:%s", count, summary.String())
}
