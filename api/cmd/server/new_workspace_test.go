package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// A workspace created at runtime with bounded metadata on is served
// like one a flag-on start loaded. Before this change POST /api/workspaces
// answered 201 but installed no logical source for the new workspace, so the
// built-in agent's installation, every later read and write of it (account
// settings, issues, ...) and the background passes that visited it failed
// with "bounded metadata coverage is incomplete".
//
// The same script runs on a flag-off and a flag-on server over copies of one
// seed: two workspaces are created through the HTTP API (onboarding with the
// tutorial issues, and the importer's tutorial-less creation), then each is
// driven through the app's first steps. Every request status, every GET
// response and every table row (generated ids, times and secrets masked, the
// rollback round trip's normalisers) must match; the flag-on database must
// then pass the bounded start's coverage check, and the reads must match
// again after the restart.

const newWorkspaceFresh, newWorkspaceImport = "sw602-fresh", "sw602-import"

type workspaceAccountSettingsRun struct {
	t      *testing.T
	st     *rtStack
	names  *rtRun // id labels and masking
	log    []string
	failed []string
}

func (r *workspaceAccountSettingsRun) do(workspace, method, path string, body any) (int, []byte) {
	r.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	request, err := http.NewRequest(method, r.st.host.URL+path, reader)
	if err != nil {
		r.t.Fatal(err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if workspace != "" {
		request.Header.Set("X-Workspace-Key", workspace)
	}
	response, err := r.st.admin.Do(request)
	if err != nil {
		r.t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	return response.StatusCode, raw
}

func (r *workspaceAccountSettingsRun) write(label, workspace, method, path string, body any, want int) map[string]any {
	r.t.Helper()
	status, raw := r.do(workspace, method, path, body)
	r.log = append(r.log, fmt.Sprintf("%s %s %s %s -> %d", workspace, label, method, r.names.norm(path), status))
	if status != want {
		r.failed = append(r.failed, fmt.Sprintf("%s %s: %s %s status %d want %d: %s", workspace, label, method, path, status, want, rtClip(string(raw))))
		return map[string]any{}
	}
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out
}

func (r *workspaceAccountSettingsRun) name(label, id string) string { return r.names.name(label, id) }

// writes creates the workspaces and drives each through the app's first
// steps.
func newWorkspaceWrites(r *workspaceAccountSettingsRun) {
	created := r.write("create", "", "POST", "/api/workspaces", map[string]any{"name": "SW602 fresh", "urlKey": newWorkspaceFresh, "locale": "en-US"}, 201)
	if workspace, ok := created["workspace"].(map[string]any); ok {
		r.name("freshWorkspace", rtID(workspace))
	}
	created = r.write("create", "", "POST", "/api/workspaces", map[string]any{"name": "SW602 import", "urlKey": newWorkspaceImport, "tutorial": false}, 201)
	if workspace, ok := created["workspace"].(map[string]any); ok {
		r.name("importWorkspace", rtID(workspace))
	}
	for _, workspace := range []string{newWorkspaceFresh, newWorkspaceImport} {
		status, raw := r.do(workspace, "GET", "/api/bootstrap", nil)
		boot := map[string]any{}
		_ = json.Unmarshal(raw, &boot)
		r.log = append(r.log, fmt.Sprintf("%s bootstrap -> %d", workspace, status))
		team := ""
		if teams, _ := boot["teams"].([]any); len(teams) > 0 {
			team = rtString(teams[0].(map[string]any), "id")
		}
		r.name(workspace+"-team", team)
		r.write("account-settings", workspace, "PATCH", "/api/account/settings", map[string]any{"fontSize": "small"}, 200)
		label := r.write("label-create", workspace, "POST", "/api/labels", map[string]any{"name": "SW602 label", "color": "#4cb782"}, 201)
		r.name(workspace+"-label", rtID(label))
		r.write("label-update", workspace, "PATCH", "/api/labels/"+rtID(label), map[string]any{"name": "SW602 label renamed"}, 200)
		project := r.write("project-create", workspace, "POST", "/api/projects", map[string]any{"name": "SW602 project", "teamIds": []string{team}}, 201)
		r.name(workspace+"-project", rtID(project))
		r.write("project-update", workspace, "PATCH", "/api/projects/"+rtID(project), map[string]any{"description": "first project"}, 200)
		issue := r.write("issue-create", workspace, "POST", "/api/issues", map[string]any{"title": "SW602 issue", "teamId": team, "labelIds": []string{rtID(label)}, "projectId": rtID(project)}, 201)
		r.name(workspace+"-issue", rtID(issue))
		if id := rtID(issue); id != "" {
			pinCreatedIssueAges(r.t, r.st.repo, workspace, false, id)
		}
		r.write("issue-update", workspace, "PATCH", "/api/issues/"+rtID(issue), map[string]any{"priority": 2, "title": "SW602 issue edited"}, 200)
		record := r.write("issue-record-create", workspace, "POST", "/api/issue-records", map[string]any{"title": "SW602 record", "teamId": team}, 201)
		r.name(workspace+"-record", rtID(record))
		comment := r.write("comment-create", workspace, "POST", "/api/issue-records/"+rtID(issue)+"/comments", map[string]any{"body": "first comment"}, 201)
		r.name(workspace+"-comment", rtID(comment))
		doc := r.write("document-create", workspace, "POST", "/api/documents", map[string]any{"title": "SW602 document", "content": "# body", "teamIds": []string{team}}, 201)
		r.name(workspace+"-document", rtID(doc))
		r.write("document-update", workspace, "PATCH", "/api/documents/"+rtID(doc), map[string]any{"content": "# body\nsecond"}, 200)
		view := r.write("view-create", workspace, "POST", "/api/views", map[string]any{"name": "SW602 view", "resource": "issues", "scope": "workspace"}, 201)
		r.name(workspace+"-view", rtID(view))
		r.write("view-update", workspace, "PATCH", "/api/views/"+rtID(view), map[string]any{"name": "SW602 view renamed"}, 200)
		loop := r.write("loop-create", workspace, "POST", "/api/loops", map[string]any{"name": "SW602 loop", "status": "published", "instructions": "Summarise the week."}, 201)
		r.name(workspace+"-loop", rtID(loop))
		r.write("loop-update", workspace, "PATCH", "/api/loops/"+rtID(loop), map[string]any{"name": "SW602 loop renamed"}, 200)
	}
}

func newWorkspaceReads(r *workspaceAccountSettingsRun) map[string]string {
	out := map[string]string{}
	for _, workspace := range []string{newWorkspaceFresh, newWorkspaceImport} {
		for _, path := range []string{
			"/api/bootstrap", "/api/account/settings", "/api/issues", "/api/issue-records?limit=50", "/api/documents", "/api/projects",
			"/api/labels", "/api/views", "/api/loops", "/api/application-installations", "/api/agent/sessions", "/api/notifications",
		} {
			status, raw := r.do(workspace, "GET", path, nil)
			out[workspace+" "+path] = fmt.Sprintf("%d %s", status, r.names.norm(rtCanonical(raw)))
		}
	}
	return out
}

// log captures the server log of one run.
type workspaceCreationRequestLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *workspaceCreationRequestLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf.Write(p)
	return os.Stderr.Write(p)
}

func (l *workspaceCreationRequestLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

func newWorkspaceCapture(t *testing.T) *workspaceCreationRequestLog {
	t.Helper()
	captured := &workspaceCreationRequestLog{}
	previous := log.Writer()
	log.SetOutput(captured)
	t.Cleanup(func() { log.SetOutput(previous) })
	return captured
}

// refusals are the log lines of a workspace the bounded store does not
// cover. The log is process-wide: only lines naming an runtime workspace creation workspace count,
// not those of a store another test left running.
func newWorkspaceRefusals(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "sw602-") && (strings.Contains(line, "coverage is incomplete") || strings.Contains(line, "could not be installed")) {
			out = append(out, line)
		}
	}
	return out
}

func newWorkspaceScenario(t *testing.T, seed string, bounded bool) (rtOutcome, map[string]string) {
	t.Helper()
	name := "off"
	if bounded {
		name = "on"
	}
	path := filepath.Join(t.TempDir(), name+".db")
	rtCopy(t, seed, path)
	captured := newWorkspaceCapture(t)
	st := rtOpen(t, path, bounded)
	st.svc.workspaceTutorial = true
	run := &workspaceAccountSettingsRun{t: t, st: st, names: &rtRun{t: t, labels: map[string]string{}}}
	newWorkspaceWrites(run)
	rtSleep()
	reads := newWorkspaceReads(run)
	st.close()
	// The restart: a flag-on start refuses a workspace the coverage check
	// does not certify (rtOpen fails the test).
	st = rtOpen(t, path, bounded)
	run.st = st
	restarted := newWorkspaceReads(run)
	st.close()
	if refusals := newWorkspaceRefusals(captured.String()); len(refusals) > 0 {
		t.Errorf("%s: the server refused the created workspaces:\n%s", name, strings.Join(refusals, "\n"))
	}
	if len(run.failed) > 0 {
		t.Errorf("%s: unexpected statuses:\n%s", name, strings.Join(run.failed, "\n"))
	}
	if diff := rtDiffReads(reads, restarted, name+"-before-restart", name+"-after-restart"); len(diff) > 0 {
		t.Errorf("%s: GET responses changed across the restart:\n%s", name, strings.Join(diff, "\n"))
	}
	return rtOutcome{name: name, log: run.log, failed: run.failed, reads: reads, dump: rtDump(t, path), labels: run.names.labels}, restarted
}

func TestCreatedWorkspaceMatchesLegacy(t *testing.T) {
	seed := rtSeed(t)
	legacy, legacyRestarted := newWorkspaceScenario(t, seed, false)
	bounded, boundedRestarted := newWorkspaceScenario(t, seed, true)
	rtCompare(t, legacy, bounded)
	if diff := rtDiffReads(legacyRestarted, boundedRestarted, "off", "on"); len(diff) > 0 {
		t.Errorf("GET responses after the restart differ:\n%s", strings.Join(diff, "\n"))
	}
	for _, workspace := range []string{newWorkspaceFresh, newWorkspaceImport} {
		if installations := legacy.reads[workspace+" /api/application-installations"]; !strings.HasPrefix(installations, "200 ") || !strings.Contains(installations, `"builtin":true`) {
			t.Errorf("%s: the built-in agent is not installed: %s", workspace, rtClip(installations))
		}
	}
	if !strings.Contains(legacy.reads[newWorkspaceFresh+" /api/issue-records?limit=50"], "Welcome to Flow") || strings.Contains(legacy.reads[newWorkspaceImport+" /api/issue-records?limit=50"], "Welcome to Flow") {
		t.Errorf("the onboarding workspace has no tutorial issues, or the import workspace has them")
	}
}

// A workspace the bounded store does not cover (the state every workspace
// created before this change was left in) fails each read of it; the passes
// over all workspaces skip it, log it once and serve the others.
func TestUncoveredWorkspaceDoesNotStopBackgroundPasses(t *testing.T) {
	seed := rtSeed(t)
	path := filepath.Join(t.TempDir(), "poisoned.db")
	rtCopy(t, seed, path)
	captured := newWorkspaceCapture(t)
	st := rtOpen(t, path, true)
	defer st.close()
	run := &workspaceAccountSettingsRun{t: t, st: st, names: &rtRun{t: t, labels: map[string]string{}}}
	// Sorted before test-workspace, so a pass that stops at it never
	// reaches the seeded workspace.
	const poisoned = "aaa-sw602-poisoned"
	run.write("create", "", "POST", "/api/workspaces", map[string]any{"name": "SW602 poisoned", "urlKey": poisoned}, 201)
	loop := run.write("loop-create", rtWorkspace, "POST", "/api/loops", map[string]any{"name": "SW602 scheduled", "status": "published", "instructions": "Weekly summary.", "triggerType": "schedule"}, 201)
	if len(run.failed) > 0 {
		t.Fatal(strings.Join(run.failed, "\n"))
	}
	if !st.repo.InstalledWorkspaceForTesting(poisoned) {
		t.Fatal("the created workspace has no installed logical source")
	}
	if err := st.repo.WithdrawInstalledWorkspaceForTesting(poisoned); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	now := time.Now().UTC()
	for tick := range 2 {
		if err := st.svc.processDueDeliveries(ctx, now.Add(time.Duration(tick)*time.Second)); err != nil {
			t.Fatalf("tick %d: the delivery pass stopped at the uncovered workspace: %v", tick, err)
		}
	}
	// The settings sweep reached test-workspace: its scheduled loop got its
	// next run.
	status, raw := run.do(rtWorkspace, "GET", "/api/loops/"+rtID(loop), nil)
	var got map[string]any
	_ = json.Unmarshal(raw, &got)
	if status != 200 || got["nextRunAt"] == nil {
		t.Fatalf("the delivery pass did not reach test-workspace after the uncovered workspace: %d %s", status, rtClip(string(raw)))
	}
	apps, release, ok := st.svc.applicationWebhookClients(ctx)
	if !ok {
		t.Fatal("the application index refused every workspace for one uncovered workspace")
	}
	release()
	_ = apps
	st.svc.reconcileAllApplicationWebhooks(ctx)
	st.svc.sweepAgentLiveReplies(ctx, now)
	st.svc.sweepAgentLiveReplies(ctx, now)
	text := captured.String()
	for _, pass := range []string{"Delivery scheduler", "Application webhooks", "sweep worker chat replies", "probe agent replies"} {
		if count := strings.Count(text, pass+": skipping workspace="+poisoned+" "); count != 1 {
			t.Errorf("%s logged the uncovered workspace %d times, want once", pass, count)
		}
	}
}
