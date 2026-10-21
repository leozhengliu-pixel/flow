package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type gitlabReleaseHookResponse struct {
	Ignored                 bool              `json:"ignored"`
	Reason                  string            `json:"reason"`
	PipelineID              string            `json:"pipelineId"`
	Created                 bool              `json:"created"`
	Release                 *releaseCIRelease `json:"release"`
	SkippedIssueIdentifiers []string          `json:"skippedIssueIdentifiers"`
	UnknownIssueIdentifiers []string          `json:"unknownIssueIdentifiers"`
	RejectedLinks           int               `json:"rejectedLinks"`
}

type gitlabReleaseHookEnv struct {
	t          *testing.T
	handler    http.Handler
	repository *store.SQLiteStore
	bootstrap  domain.Bootstrap
	secret     string
}

// newGitLabReleaseHookEnv connects GitLab (webhook secret) to a workspace
// without release pipelines.
func newGitLabReleaseHookEnv(t *testing.T) *gitlabReleaseHookEnv {
	t.Helper()
	handler, repository, bootstrap := newReleaseCITestServer(t)
	for _, pipeline := range bootstrap.ReleasePipelines {
		requestJSON[any](t, handler, http.MethodDelete, "/api/release-pipelines/"+pipeline.ID, nil, http.StatusNoContent)
	}
	enableConnectorSecrets(t)
	api := newFakeGitLab(t)
	gitlab := requestJSON[gitlabConnectResponse](t, handler, http.MethodPut, "/api/integrations/gitlab?workspace="+bootstrap.Workspace.URLKey, map[string]any{
		"name": "self-hosted", "config": map[string]string{"apiToken": "glpat-fake-api", "host": api.URL},
	}, http.StatusOK)
	if gitlab.WebhookSecret == "" {
		t.Fatal("GitLab connection has no webhook secret")
	}
	return &gitlabReleaseHookEnv{t: t, handler: handler, repository: repository, bootstrap: bootstrap, secret: gitlab.WebhookSecret}
}

func (e *gitlabReleaseHookEnv) pipeline(input map[string]any) domain.ReleasePipeline {
	e.t.Helper()
	return requestJSON[domain.ReleasePipeline](e.t, e.handler, http.MethodPost, "/api/release-pipelines", input, http.StatusCreated)
}

// deliver posts a hook to the GitLab webhook; query is appended to the URL.
func (e *gitlabReleaseHookEnv) deliver(query, event, token string, payload map[string]any, wantStatus int) gitlabReleaseHookResponse {
	e.t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		e.t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/integrations/gitlab/webhook?workspace="+e.bootstrap.Workspace.URLKey+query, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Gitlab-Event", event)
	if token != "" {
		req.Header.Set("X-Gitlab-Token", token)
	}
	recorder := httptest.NewRecorder()
	e.handler.ServeHTTP(recorder, req)
	if recorder.Code != wantStatus {
		e.t.Fatalf("webhook status %d, want %d: %s", recorder.Code, wantStatus, recorder.Body.String())
	}
	var result gitlabReleaseHookResponse
	_ = json.Unmarshal(recorder.Body.Bytes(), &result)
	return result
}

func (e *gitlabReleaseHookEnv) release(query string, payload map[string]any, wantStatus int) gitlabReleaseHookResponse {
	e.t.Helper()
	return e.deliver(query, "Release Hook", e.secret, payload, wantStatus)
}

func (e *gitlabReleaseHookEnv) releases(pipelineID string) []domain.Release {
	e.t.Helper()
	return requestJSON[[]domain.Release](e.t, e.handler, http.MethodGet, "/api/releases?pipelineId="+pipelineID, nil, http.StatusOK)
}

func (e *gitlabReleaseHookEnv) allReleases() []domain.Release {
	e.t.Helper()
	data, _ := e.repository.BootstrapFor(e.bootstrap.Workspace.URLKey)
	return data.Releases
}

func gitlabReleasePayload(action, tag, name, description string) map[string]any {
	return map[string]any{
		"object_kind": "release", "action": action, "tag": tag, "name": name, "description": description,
		"url":     "https://gitlab.example.com/acme/app/-/releases/" + tag,
		"commit":  map[string]any{"id": "0123456789abcdef0123456789abcdef01234567"},
		"project": map[string]any{"path_with_namespace": "acme/app"},
	}
}

var scheduledReleasePipeline = map[string]any{
	"name": "App", "type": "scheduled", "production": false,
	"stages":                      []string{"Planned", "In Progress", "Code Freeze", "Released", "Canceled"},
	"stageStatuses":               map[string]string{"Planned": "planned", "In Progress": "inProgress", "Code Freeze": "inProgress", "Released": "released", "Canceled": "canceled"},
	"frozenStages":                []string{"Code Freeze"},
	"autoGenerateReleaseNotes":    true,
	"releaseNotesTemplate":        "## Shipped\n{{issues}}",
	"moveOpenIssuesToNextRelease": false,
}

func TestGitLabReleaseHookCreateReplayAndUpdate(t *testing.T) {
	e := newGitLabReleaseHookEnv(t)
	pipeline := e.pipeline(scheduledReleasePipeline)
	first, second := e.bootstrap.Issues[0], e.bootstrap.Issues[1]
	// A shipped issue is listed by the "auto-generate on completion" notes.
	done := slices.IndexFunc(e.bootstrap.States, func(state domain.WorkflowState) bool {
		return (state.TeamID == first.Team.ID || state.TeamID == "") && state.Type == "completed"
	})
	if done < 0 {
		t.Skip("seed needs a completed state")
	}
	requestJSON[domain.Issue](t, e.handler, http.MethodPatch, "/api/issues/"+first.ID, map[string]any{"stateId": e.bootstrap.States[done].ID}, http.StatusOK)

	created := e.release("", gitlabReleasePayload("create", "v1.4.0", "Spring release", fmt.Sprintf("Fixes %s and %s.", strings.ToLower(first.Identifier), "NOPE-999")), http.StatusAccepted)
	if !created.Created || created.PipelineID != pipeline.ID || created.Release == nil {
		t.Fatalf("create = %#v", created)
	}
	if !slices.Contains(created.UnknownIssueIdentifiers, "NOPE-999") {
		t.Fatalf("unknown identifiers = %#v", created.UnknownIssueIdentifiers)
	}
	releases := e.releases(pipeline.ID)
	if len(releases) != 1 {
		t.Fatalf("create should make exactly one release: %#v", releases)
	}
	stored := requestJSON[domain.Release](t, e.handler, http.MethodGet, "/api/releases/"+releases[0].ID, nil, http.StatusOK)
	link := slices.IndexFunc(stored.Resources, func(item domain.ReleaseResource) bool { return item.Type == "link" && item.Title == "GitLab release" })
	if stored.Version != "v1.4.0" || stored.Name != "Spring release" || stored.Status != "released" || stored.Stage != "Released" || stored.ReleasedAt == nil ||
		!strings.HasPrefix(stored.Description, "Fixes ") || link < 0 || stored.Resources[link].URL != "https://gitlab.example.com/acme/app/-/releases/v1.4.0" ||
		!slices.Equal(stored.IssueIDs, []string{first.ID}) || stored.CommitSHA != "0123456789abcdef0123456789abcdef01234567" {
		t.Fatalf("created release = %#v", stored)
	}
	// Completion automations ran: notes were generated from the template.
	if !strings.HasPrefix(stored.ReleaseNotes, "## Shipped\n- "+first.Identifier) {
		t.Fatalf("completion automations did not run: notes = %q", stored.ReleaseNotes)
	}
	data, _ := e.repository.BootstrapFor(e.bootstrap.Workspace.URLKey)
	if !slices.ContainsFunc(data.ReleaseHistory, func(item domain.ReleaseHistory) bool {
		return item.ReleaseID == stored.ID && item.Action == "ci_sync" && item.Metadata["source"] == "gitlab"
	}) {
		t.Fatal("release history has no GitLab sync entry")
	}
	if public := requestJSON[domain.ReleasePipeline](t, e.handler, http.MethodGet, "/api/release-pipelines/"+pipeline.ID, nil, http.StatusOK); public.AccessKeyLastUsedAt != nil {
		t.Fatal("a webhook must not record access-key use")
	}

	// GitLab re-delivers the same hook; then the release is edited.
	replayed := e.release("", gitlabReleasePayload("create", "v1.4.0", "Spring release", fmt.Sprintf("Fixes %s and %s.", first.Identifier, "NOPE-999")), http.StatusAccepted)
	updated := e.release("", gitlabReleasePayload("update", "v1.4.0", "Spring release (final)", fmt.Sprintf("Fixes %s and %s.", first.Identifier, second.Identifier)), http.StatusAccepted)
	if replayed.Created || updated.Created || replayed.Release.ID != stored.ID || updated.Release.ID != stored.ID {
		t.Fatalf("replay/update must target the same release: %#v %#v", replayed, updated)
	}
	releases = e.releases(pipeline.ID)
	if len(releases) != 1 {
		t.Fatalf("replay/update duplicated the release: %#v", releases)
	}
	stored = requestJSON[domain.Release](t, e.handler, http.MethodGet, "/api/releases/"+stored.ID, nil, http.StatusOK)
	links := 0
	for _, item := range stored.Resources {
		if item.Type == "link" {
			links++
		}
	}
	if stored.Name != "Spring release (final)" || !strings.Contains(stored.Description, second.Identifier) || !slices.Contains(stored.IssueIDs, second.ID) || links != 1 || stored.Stage != "Released" {
		t.Fatalf("updated release = %#v", stored)
	}
}

func TestGitLabReleaseHookMatchesExistingReleases(t *testing.T) {
	for _, test := range []struct {
		name          string
		existing      map[string]any
		tag, hookName string
		wantVersion   string
	}{
		{name: "stored version without v", existing: map[string]any{"name": "One two", "version": "1.2.0"}, tag: "v1.2.0", hookName: "v1.2.0", wantVersion: "1.2.0"},
		{name: "stored version with v", existing: map[string]any{"name": "One three", "version": "v1.3.0"}, tag: "1.3.0", hookName: "", wantVersion: "v1.3.0"},
		{name: "name match backfills version", existing: map[string]any{"name": "Autumn release"}, tag: "v2.0.0", hookName: "autumn release", wantVersion: "v2.0.0"},
	} {
		t.Run(test.name, func(t *testing.T) {
			e := newGitLabReleaseHookEnv(t)
			pipeline := e.pipeline(scheduledReleasePipeline)
			test.existing["pipelineId"] = pipeline.ID
			existing := requestJSON[domain.Release](t, e.handler, http.MethodPost, "/api/releases", test.existing, http.StatusCreated)
			result := e.release("", gitlabReleasePayload("create", test.tag, test.hookName, "Notes"), http.StatusAccepted)
			if result.Created || result.Release == nil || result.Release.ID != existing.ID {
				t.Fatalf("hook did not match the existing release: %#v", result)
			}
			releases := e.releases(pipeline.ID)
			if len(releases) != 1 || releases[0].Version != test.wantVersion || releases[0].Status != "released" {
				t.Fatalf("releases = %#v", releases)
			}
			// The (backfilled) version keeps matching later deliveries.
			again := e.release("", gitlabReleasePayload("update", test.tag, "Renamed", "Notes 2"), http.StatusAccepted)
			if again.Release.ID != existing.ID || len(e.releases(pipeline.ID)) != 1 {
				t.Fatalf("update after match duplicated the release: %#v", again)
			}
		})
	}
}

func TestGitLabReleaseHookIgnoredDeliveries(t *testing.T) {
	e := newGitLabReleaseHookEnv(t)
	pipeline := e.pipeline(scheduledReleasePipeline)
	e.release("", gitlabReleasePayload("create", "v1.0.0", "One", "Notes"), http.StatusAccepted)
	before := requestJSON[domain.Release](t, e.handler, http.MethodGet, "/api/releases/"+e.releases(pipeline.ID)[0].ID, nil, http.StatusOK)
	tagPush := map[string]any{"object_kind": "tag_push", "ref": "refs/tags/v9.0.0", "checkout_sha": "abc", "project": map[string]any{"path_with_namespace": "acme/app"}}
	for _, test := range []struct {
		name       string
		event      string
		token      string
		payload    map[string]any
		wantStatus int
		wantReason string
	}{
		{name: "delete keeps the release", event: "Release Hook", token: e.secret, payload: gitlabReleasePayload("delete", "v1.0.0", "One", ""), wantStatus: http.StatusAccepted, wantReason: "GitLab release deleted; Flow releases are kept"},
		{name: "tag push is not a release", event: "Tag Push Hook", token: e.secret, payload: tagPush, wantStatus: http.StatusAccepted, wantReason: "event is not a pull/merge request"},
		{name: "missing token", event: "Release Hook", payload: gitlabReleasePayload("create", "v2.0.0", "Two", ""), wantStatus: http.StatusUnauthorized},
		{name: "wrong token", event: "Release Hook", token: "not-the-secret", payload: gitlabReleasePayload("update", "v1.0.0", "Hacked", "Hacked"), wantStatus: http.StatusUnauthorized},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := e.deliver("", test.event, test.token, test.payload, test.wantStatus)
			if test.wantReason != "" && (!result.Ignored || result.Reason != test.wantReason) {
				t.Fatalf("response = %#v", result)
			}
			releases := e.allReleases()
			after := requestJSON[domain.Release](t, e.handler, http.MethodGet, "/api/releases/"+before.ID, nil, http.StatusOK)
			if len(e.releases(pipeline.ID)) != 1 || after.Name != before.Name || after.Description != before.Description || !after.UpdatedAt.Equal(before.UpdatedAt) || slices.ContainsFunc(releases, func(item domain.Release) bool { return item.Version == "v2.0.0" || item.Version == "v9.0.0" }) {
				t.Fatalf("ignored delivery changed releases: %#v", after)
			}
		})
	}
}

func TestGitLabReleaseHookPipelineRouting(t *testing.T) {
	e := newGitLabReleaseHookEnv(t)
	payload := gitlabReleasePayload("create", "v5.0.0", "Five", "Notes")
	none := e.release("", payload, http.StatusAccepted)
	if !none.Ignored || !strings.HasPrefix(none.Reason, "no release pipeline is selected") {
		t.Fatalf("no pipeline: %#v", none)
	}
	web := e.pipeline(map[string]any{"name": "Web", "type": "continuous"})
	mobile := e.pipeline(map[string]any{"name": "Mobile", "type": "continuous"})
	before := len(e.allReleases())
	ambiguous := e.release("", payload, http.StatusAccepted)
	if !ambiguous.Ignored || !strings.HasPrefix(ambiguous.Reason, "no release pipeline is selected") || len(e.allReleases()) != before {
		t.Fatalf("two pipelines without ?pipeline= must write nothing: %#v", ambiguous)
	}
	for _, test := range []struct {
		name, query string
		want        domain.ReleasePipeline
	}{
		{name: "slug", query: "&pipeline=" + mobile.SlugID, want: mobile},
		{name: "id", query: "&pipeline=" + web.ID, want: web},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := e.release(test.query, payload, http.StatusAccepted)
			releases := e.releases(test.want.ID)
			if result.PipelineID != test.want.ID || !result.Created || len(releases) != 1 || releases[0].Version != "v5.0.0" {
				t.Fatalf("routing to %s: %#v %#v", test.want.SlugID, result, releases)
			}
		})
	}
	e.release("&pipeline=nope", payload, http.StatusNotFound)
	if len(e.allReleases()) != before+2 {
		t.Fatal("an unknown pipeline selector wrote a release")
	}
}

func TestGitLabReleaseHookSinglePipelineAndContinuous(t *testing.T) {
	e := newGitLabReleaseHookEnv(t)
	pipeline := e.pipeline(map[string]any{"name": "Web", "type": "continuous"})
	result := e.release("", gitlabReleasePayload("create", "v1.0.0", "", "Ships "+e.bootstrap.Issues[0].Identifier), http.StatusAccepted)
	if result.PipelineID != pipeline.ID || !result.Created || result.Release.Status != "released" || result.Release.Name != "v1.0.0" || result.Release.IssueCount != 1 {
		t.Fatalf("continuous release = %#v", result)
	}
	again := e.release("", gitlabReleasePayload("update", "v1.0.0", "", "Ships "+e.bootstrap.Issues[0].Identifier), http.StatusAccepted)
	if again.Created || again.Release.ID != result.Release.ID || len(e.releases(pipeline.ID)) != 1 {
		t.Fatalf("continuous update = %#v", again)
	}
}

func TestGitLabReleaseHookRespectsFrozenStagesLikeCI(t *testing.T) {
	e := newGitLabReleaseHookEnv(t)
	pipeline := e.pipeline(scheduledReleasePipeline)
	key := requestJSON[releasePipelineAccessKey](t, e.handler, http.MethodPost, "/api/release-pipelines/"+pipeline.ID+"/access-key", nil, http.StatusCreated)
	first, second := e.bootstrap.Issues[0], e.bootstrap.Issues[1]
	releaseCICall[releaseCIResult](t, e.handler, http.MethodPost, "/api/release-ci/sync", key.Secret, map[string]any{"version": "v3.0.0", "issueReferences": []map[string]string{{"identifier": first.Identifier}}}, http.StatusCreated)
	frozen := releaseCICall[releaseCIResult](t, e.handler, http.MethodPost, "/api/release-ci/update", key.Secret, map[string]any{"version": "v3.0.0", "stage": "Code Freeze"}, http.StatusOK)
	if !frozen.Release.Stage.Frozen {
		t.Fatalf("release not frozen: %#v", frozen)
	}
	result := e.release("", gitlabReleasePayload("create", "v3.0.0", "Three", "Adds "+second.Identifier), http.StatusAccepted)
	if !slices.Equal(result.SkippedIssueIdentifiers, []string{second.Identifier}) || result.Release.IssueCount != 1 || result.Release.Status != "released" || result.Release.Stage.Name != "Released" {
		t.Fatalf("frozen release accepted a hook issue or did not complete: %#v", result)
	}
}
