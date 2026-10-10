package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func releaseCICall[T any](t *testing.T, handler http.Handler, method, path, key string, input any, wantStatus int) T {
	t.Helper()
	var body *bytes.Reader
	if input != nil {
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(raw)
	} else {
		body = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, body)
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != wantStatus {
		t.Fatalf("%s %s status %d, want %d: %s", method, path, recorder.Code, wantStatus, recorder.Body.String())
	}
	var result T
	if recorder.Body.Len() > 0 {
		if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
	}
	return result
}

func newReleaseCITestServer(t *testing.T) (http.Handler, *store.SQLiteStore, domain.Bootstrap) {
	t.Helper()
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if len(bootstrap.Issues) < 3 {
		t.Fatal("seed must include at least three issues")
	}
	return handler, repository, bootstrap
}

func TestReleaseAccessKeyRotationGraceAndRevocation(t *testing.T) {
	handler, repository, bootstrap := newReleaseCITestServer(t)
	pipeline := requestJSON[domain.ReleasePipeline](t, handler, http.MethodPost, "/api/release-pipelines", map[string]any{"name": "Keys", "type": "continuous"}, http.StatusCreated)
	first := requestJSON[releasePipelineAccessKey](t, handler, http.MethodPost, "/api/release-pipelines/"+pipeline.ID+"/access-key", nil, http.StatusCreated)
	if first.PreviousKeyExpiresAt != nil {
		t.Fatal("a first key has no predecessor")
	}
	releaseCICall[releaseCIPipeline](t, handler, http.MethodGet, "/api/release-ci/pipeline", first.Secret, nil, http.StatusOK)
	public := requestJSON[domain.ReleasePipeline](t, handler, http.MethodGet, "/api/release-pipelines/"+pipeline.ID, nil, http.StatusOK)
	if public.AccessKeyLastUsedAt == nil || public.PreviousAccessKeyHash != "" || public.AccessKeyHash != "" {
		t.Fatalf("key use was not recorded or hashes leaked: %#v", public)
	}

	second := requestJSON[releasePipelineAccessKey](t, handler, http.MethodPost, "/api/release-pipelines/"+pipeline.ID+"/access-key", map[string]any{}, http.StatusCreated)
	if second.PreviousKeyExpiresAt == nil || second.PreviousKeyExpiresAt.Sub(time.Now()) < 59*time.Minute {
		t.Fatalf("rotation should keep the old key for an hour: %#v", second)
	}
	// Both keys work during the grace period.
	releaseCICall[releaseCIPipeline](t, handler, http.MethodGet, "/api/release-ci/pipeline", first.Secret, nil, http.StatusOK)
	releaseCICall[releaseCIPipeline](t, handler, http.MethodGet, "/api/release-ci/pipeline", second.Secret, nil, http.StatusOK)
	bootstrapData, _ := repository.BootstrapFor(bootstrap.Workspace.URLKey)
	if internal := releasePipelineByID(&bootstrapData, pipeline.ID); internal.PreviousAccessKeyHash == "" || internal.AccessKeyLastUsedAt != nil && internal.AccessKeyCreatedAt.After(*internal.AccessKeyLastUsedAt) {
		t.Fatalf("rotation state not stored: %#v", internal)
	}

	// Soft revoke schedules the end of the current key; it keeps working until then.
	scheduled := requestJSON[domain.ReleasePipeline](t, handler, http.MethodDelete, "/api/release-pipelines/"+pipeline.ID+"/access-key", nil, http.StatusOK)
	if scheduled.AccessKeyRevokedAt == nil || scheduled.AccessKeyPrefix == "" {
		t.Fatalf("revocation was not scheduled: %#v", scheduled)
	}
	releaseCICall[releaseCIPipeline](t, handler, http.MethodGet, "/api/release-ci/pipeline", second.Secret, nil, http.StatusOK)
	// Immediate revoke stops every key at once.
	revoked := requestJSON[domain.ReleasePipeline](t, handler, http.MethodDelete, "/api/release-pipelines/"+pipeline.ID+"/access-key?immediate=true", nil, http.StatusOK)
	if revoked.AccessKeyPrefix != "" || revoked.AccessKeyRevokedAt != nil || revoked.AccessKeyCreatedAt != nil {
		t.Fatalf("immediate revoke left key metadata: %#v", revoked)
	}
	releaseCICall[map[string]string](t, handler, http.MethodGet, "/api/release-ci/pipeline", second.Secret, nil, http.StatusUnauthorized)
	releaseCICall[map[string]string](t, handler, http.MethodGet, "/api/release-ci/pipeline", first.Secret, nil, http.StatusUnauthorized)
	requestJSON[map[string]string](t, handler, http.MethodDelete, "/api/release-pipelines/"+pipeline.ID+"/access-key", nil, http.StatusNotFound)

	// Rotating with revokeImmediately drops the old key right away.
	third := requestJSON[releasePipelineAccessKey](t, handler, http.MethodPost, "/api/release-pipelines/"+pipeline.ID+"/access-key", nil, http.StatusCreated)
	fourth := requestJSON[releasePipelineAccessKey](t, handler, http.MethodPost, "/api/release-pipelines/"+pipeline.ID+"/access-key", map[string]any{"revokeImmediately": true}, http.StatusCreated)
	if fourth.PreviousKeyExpiresAt != nil {
		t.Fatal("immediate rotation kept the previous key")
	}
	releaseCICall[map[string]string](t, handler, http.MethodGet, "/api/release-ci/pipeline", third.Secret, nil, http.StatusUnauthorized)
	releaseCICall[map[string]string](t, handler, http.MethodGet, "/api/release-ci/releases", "", nil, http.StatusUnauthorized)
}

func TestReleaseCIContinuousSyncAttachesIssuesLinksDocumentsAndNotes(t *testing.T) {
	handler, _, bootstrap := newReleaseCITestServer(t)
	pipeline := requestJSON[domain.ReleasePipeline](t, handler, http.MethodPost, "/api/release-pipelines", map[string]any{"name": "Web", "type": "continuous", "pathFilters": []string{"web/**"}}, http.StatusCreated)
	key := requestJSON[releasePipelineAccessKey](t, handler, http.MethodPost, "/api/release-pipelines/"+pipeline.ID+"/access-key", nil, http.StatusCreated)

	settings := releaseCICall[releaseCIPipeline](t, handler, http.MethodGet, "/api/release-ci/pipeline", key.Secret, nil, http.StatusOK)
	if settings.ID != pipeline.ID || !slices.Equal(settings.IncludePathPatterns, []string{"web/**"}) || settings.Type != "continuous" || len(settings.Stages) != 1 || settings.Stages[0].Status != "released" {
		t.Fatalf("pipeline settings = %#v", settings)
	}
	first, second := bootstrap.Issues[0], bootstrap.Issues[1]
	synced := releaseCICall[releaseCIResult](t, handler, http.MethodPost, "/api/release-ci/sync", key.Secret, map[string]any{
		"commitSha":       "0123456789abcdef",
		"issueReferences": []map[string]string{{"identifier": strings.ToLower(first.Identifier), "commitSha": "0123456"}, {"identifier": "NOPE-999"}},
		"links":           []map[string]string{{"url": "https://ci.example.com/run/1", "label": "Pipeline"}},
		"documents":       []map[string]string{{"title": "Changelog", "content": "## 1.0\n- First"}},
		"releaseNotes":    "Shipped the first change",
	}, http.StatusCreated)
	release := synced.Release
	if !synced.Success || release == nil || release.Version != "0123456" || release.Name != "0123456" || release.Status != "released" || release.Stage == nil || release.Stage.Status != "released" || release.IssueCount != 1 || release.ReleasedAt == nil {
		t.Fatalf("continuous sync did not create a completed release: %#v", synced)
	}
	if !slices.Equal(synced.UnknownIssueIdentifiers, []string{"NOPE-999"}) || !strings.Contains(release.URL, "/pipeline/web/release/") {
		t.Fatalf("sync result = %#v", synced)
	}
	// Same version again: adds issues, dedupes links, updates the same-titled document.
	again := releaseCICall[releaseCIResult](t, handler, http.MethodPost, "/api/release-ci/sync", key.Secret, map[string]any{
		"version":         "0123456",
		"issueReferences": []map[string]string{{"identifier": second.Identifier}},
		"links":           []map[string]string{{"url": "https://ci.example.com/run/1"}},
		"documents":       []map[string]string{{"title": "Changelog", "content": "## 1.0\n- Updated"}},
	}, http.StatusOK)
	if again.Release.ID != release.ID || again.Release.IssueCount != 2 {
		t.Fatalf("re-sync should extend the release: %#v", again)
	}
	stored := requestJSON[domain.Release](t, handler, http.MethodGet, "/api/releases/"+release.ID, nil, http.StatusOK)
	if len(stored.Resources) != 2 || stored.ReleaseNotes != "Shipped the first change" || stored.CommitSHA != "0123456789abcdef" {
		t.Fatalf("stored release = %#v", stored)
	}
	document := stored.Resources[slices.IndexFunc(stored.Resources, func(item domain.ReleaseResource) bool { return item.Type == "document" })]
	if document.Content != "## 1.0\n- Updated" || document.DocumentID != "" {
		t.Fatalf("CI document = %#v", document)
	}
	// The UI saves resources back unchanged; CI documents must stay valid.
	requestJSON[domain.Release](t, handler, http.MethodPatch, "/api/releases/"+release.ID, map[string]any{"resources": stored.Resources}, http.StatusOK)
	notes := requestJSON[struct {
		Nodes []domain.ReleaseNote `json:"nodes"`
	}](t, handler, http.MethodGet, "/api/releases/"+release.ID+"/notes", nil, http.StatusOK).Nodes
	if len(notes) != 1 || notes[0].Body != "Shipped the first change" {
		t.Fatalf("CI release notes did not create a note record: %#v", notes)
	}
	recent := releaseCICall[[]releaseCIRelease](t, handler, http.MethodGet, "/api/release-ci/releases?limit=5", key.Secret, nil, http.StatusOK)
	if len(recent) != 1 || recent[0].ID != release.ID || recent[0].CommitSHA != "0123456789abcdef" {
		t.Fatalf("recent releases = %#v", recent)
	}
	releaseCICall[map[string]string](t, handler, http.MethodPost, "/api/release-ci/complete", key.Secret, map[string]any{}, http.StatusBadRequest)
	releaseCICall[map[string]string](t, handler, http.MethodPost, "/api/release-ci/sync", key.Secret, map[string]any{"releaseNotes": 12}, http.StatusBadRequest)
}

func TestReleaseCIScheduledSyncUpdateCompleteRollsOverAndWritesNotes(t *testing.T) {
	handler, _, bootstrap := newReleaseCITestServer(t)
	team := bootstrap.Issues[0].Team.ID
	issues := []domain.Issue{}
	for _, issue := range bootstrap.Issues {
		if issue.Team.ID == team && issue.State.Type != "completed" && issue.State.Type != "canceled" && len(issues) < 3 {
			issues = append(issues, issue)
		}
	}
	if len(issues) < 3 {
		t.Skip("seed needs three open issues in one team")
	}
	doneState := ""
	for _, state := range bootstrap.States {
		if (state.TeamID == team || state.TeamID == "") && state.Type == "completed" {
			doneState = state.ID
			break
		}
	}
	pipeline := requestJSON[domain.ReleasePipeline](t, handler, http.MethodPost, "/api/release-pipelines", map[string]any{
		"name": "Mobile", "type": "scheduled", "production": false,
		"stages":                   []string{"Planned", "In Progress", "Code Freeze", "Released", "Canceled"},
		"stageStatuses":            map[string]string{"Planned": "planned", "In Progress": "inProgress", "Code Freeze": "inProgress", "Released": "released", "Canceled": "canceled"},
		"frozenStages":             []string{"Code Freeze"},
		"autoGenerateReleaseNotes": true,
		"releaseNotesTemplate":     "## What's new\n{{issues}}",
	}, http.StatusCreated)
	key := requestJSON[releasePipelineAccessKey](t, handler, http.MethodPost, "/api/release-pipelines/"+pipeline.ID+"/access-key", nil, http.StatusCreated)

	// No started release yet: sync creates one in the first unfrozen started stage.
	created := releaseCICall[releaseCIResult](t, handler, http.MethodPost, "/api/release-ci/sync", key.Secret, map[string]any{
		"version": "2.0.0", "commitSha": "aaaaaaa", "issueReferences": []map[string]string{{"identifier": issues[0].Identifier}, {"identifier": issues[1].Identifier}},
	}, http.StatusCreated)
	if created.Release.Stage == nil || created.Release.Stage.Name != "In Progress" || created.Release.IssueCount != 2 {
		t.Fatalf("scheduled sync = %#v", created)
	}
	// Without a version, sync targets the latest started release.
	targeted := releaseCICall[releaseCIResult](t, handler, http.MethodPost, "/api/release-ci/sync", key.Secret, map[string]any{"commitSha": "bbbbbbb"}, http.StatusOK)
	if targeted.Release.ID != created.Release.ID {
		t.Fatalf("sync without version should target the started release: %#v", targeted)
	}
	// Stage names match case-insensitively with dashes as spaces; frozen stages take no new issues.
	frozen := releaseCICall[releaseCIResult](t, handler, http.MethodPost, "/api/release-ci/update", key.Secret, map[string]any{"stage": "code-freeze"}, http.StatusOK)
	if frozen.Release.Stage.Name != "Code Freeze" || !frozen.Release.Stage.Frozen {
		t.Fatalf("update = %#v", frozen)
	}
	skipped := releaseCICall[releaseCIResult](t, handler, http.MethodPost, "/api/release-ci/sync", key.Secret, map[string]any{"version": "2.0.0", "issueReferences": []map[string]string{{"identifier": issues[2].Identifier}}}, http.StatusOK)
	if !slices.Equal(skipped.SkippedIssueIdentifiers, []string{issues[2].Identifier}) || skipped.Release.IssueCount != 2 {
		t.Fatalf("frozen stage accepted a synced issue: %#v", skipped)
	}
	releaseCICall[map[string]string](t, handler, http.MethodPost, "/api/release-ci/update", key.Secret, map[string]any{"stage": "Staging"}, http.StatusBadRequest)
	releaseCICall[map[string]string](t, handler, http.MethodPost, "/api/release-ci/update", key.Secret, map[string]any{}, http.StatusBadRequest)
	releaseCICall[map[string]string](t, handler, http.MethodPost, "/api/release-ci/complete", key.Secret, map[string]any{"version": "9.9.9"}, http.StatusNotFound)

	// One issue ships, one stays open: completion moves the open one to a new next release.
	requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+issues[0].ID, map[string]any{"stateId": doneState}, http.StatusOK)
	completed := releaseCICall[releaseCIResult](t, handler, http.MethodPost, "/api/release-ci/complete", key.Secret, map[string]any{}, http.StatusOK)
	if completed.Release.Status != "released" || completed.Release.Stage.Name != "Released" || completed.Release.IssueCount != 1 {
		t.Fatalf("complete = %#v", completed)
	}
	releases := requestJSON[[]domain.Release](t, handler, http.MethodGet, "/api/releases?pipelineId="+pipeline.ID, nil, http.StatusOK)
	next := slices.IndexFunc(releases, func(item domain.Release) bool { return item.ID != created.Release.ID })
	if len(releases) != 2 || next < 0 || releases[next].Version != "2.0.1" || releases[next].Status != "planned" || !slices.Equal(releases[next].IssueIDs, []string{issues[1].ID}) {
		t.Fatalf("open issues were not rolled over to a new release: %#v", releases)
	}
	shipped := requestJSON[domain.Release](t, handler, http.MethodGet, "/api/releases/"+created.Release.ID, nil, http.StatusOK)
	if !strings.HasPrefix(shipped.ReleaseNotes, "## What's new\n- "+issues[0].Identifier) || strings.Contains(shipped.ReleaseNotes, issues[1].Identifier) {
		t.Fatalf("auto-generated notes = %q", shipped.ReleaseNotes)
	}
}

func TestReleaseRolloverPrefersExistingNextReleaseAndRespectsSetting(t *testing.T) {
	open := domain.WorkflowState{ID: "todo", TeamID: "team", Type: "unstarted"}
	data := domain.Bootstrap{Viewer: domain.User{ID: "owner"}, Issues: []domain.Issue{{ID: "a", Identifier: "T-1", Team: domain.Team{ID: "team"}, State: open}}, Activities: map[string][]domain.ActivityEvent{}}
	off := false
	data.ReleasePipelines = []domain.ReleasePipeline{{ID: "p", Type: "scheduled", Stages: []string{"Planned", "Done"}, StageStatuses: map[string]string{"Planned": "planned", "Done": "released"}, MoveOpenIssuesToNextRelease: &off}}
	data.Releases = []domain.Release{
		{ID: "r1", PipelineID: "p", Status: "released", Stage: "Done", IssueIDs: []string{"a"}},
		{ID: "r3", PipelineID: "p", Status: "planned", Stage: "Planned", Position: 3},
		{ID: "r2", PipelineID: "p", Status: "planned", Stage: "Planned", Position: 2},
	}
	if err := applyReleaseSettingAutomations(&data, "inProgress", &data.Releases[0], time.Now()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(data.Releases[0].IssueIDs, []string{"a"}) {
		t.Fatal("rollover ran although the pipeline turned it off")
	}
	on := true
	data.ReleasePipelines[0].MoveOpenIssuesToNextRelease = &on
	if err := applyReleaseSettingAutomations(&data, "inProgress", &data.Releases[0], time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(data.Releases[0].IssueIDs) != 0 || !slices.Equal(data.Releases[2].IssueIDs, []string{"a"}) || len(data.Releases) != 3 {
		t.Fatalf("open issue should move to the next release by position: %#v", data.Releases)
	}
	if nextReleaseVersion("v12") != "v13" || nextReleaseVersion("2026.9") != "2026.10" || nextReleaseVersion("beta") != "" {
		t.Fatal("nextReleaseVersion")
	}
	if matchReleaseStage(&domain.ReleasePipeline{Stages: []string{"QA Ready", "Prod"}, StageStatuses: map[string]string{"QA Ready": "inProgress", "Prod": "released"}}, "qa_ready") != "QA Ready" {
		t.Fatal("stage match should treat underscores as spaces")
	}
}

func TestReleasePipelineNameLimit(t *testing.T) {
	handler, _, _ := newReleaseCITestServer(t)
	requestJSON[map[string]string](t, handler, http.MethodPost, "/api/release-pipelines", map[string]any{"name": strings.Repeat("x", 121)}, http.StatusBadRequest)
	requestJSON[domain.ReleasePipeline](t, handler, http.MethodPost, "/api/release-pipelines", map[string]any{"name": strings.Repeat("界", 120)}, http.StatusCreated)
}
