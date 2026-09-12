package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func expectMCPFailure(t *testing.T, response map[string]any) {
	t.Helper()
	if response["error"] != nil {
		return
	}
	result, ok := response["result"].(map[string]any)
	if !ok || result["isError"] != true {
		t.Fatalf("expected a tool error: %v", response)
	}
}

func TestMCPMissingReferencesAndInvalidPatchesDoNotWrite(t *testing.T) {
	f := newMCPContractFixture(t)
	before := f.repository.Bootstrap()
	for _, scenario := range []struct {
		name string
		args map[string]any
	}{
		{"get_team", map[string]any{"query": "missing"}}, {"get_user", map[string]any{"query": "missing"}},
		{"get_project", map[string]any{"query": "missing"}}, {"get_initiative", map[string]any{"query": "missing"}},
		{"get_document", map[string]any{"id": "missing"}}, {"get_diff", map[string]any{"urlOrId": "missing"}},
		{"get_agent_skill", map[string]any{"id": "missing"}}, {"get_issue_status", map[string]any{"team": f.data.Teams[0].ID, "name": "missing"}},
		{"save_project", map[string]any{"name": "No team"}}, {"save_initiative", map[string]any{}},
		{"save_milestone", map[string]any{"project": "missing", "name": "milestone"}}, {"save_release", map[string]any{"id": "missing"}},
		{"save_comment", map[string]any{"issueId": "missing", "body": "body"}}, {"save_comment", map[string]any{"body": "body"}},
		{"save_comment", map[string]any{"issueId": f.data.Issues[0].ID, "projectId": f.data.Projects[0].ID, "body": "body"}},
		{"save_comment", map[string]any{"id": "missing", "body": "body"}}, {"save_comment", map[string]any{"parentId": "missing", "body": "body"}},
		{"delete_comment", map[string]any{"id": "missing"}}, {"delete_attachment", map[string]any{"id": "missing"}},
		{"resolve_diff_thread", map[string]any{"threadId": "missing"}}, {"delete_diff_comment", map[string]any{}},
		{"submit_diff_review", map[string]any{"urlOrId": "missing", "decision": "approved"}},
		{"create_attachment_from_upload", map[string]any{"issue": f.data.Issues[0].ID, "assetUrl": "http://["}},
		{"create_attachment_from_upload", map[string]any{"issue": f.data.Issues[0].ID, "assetUrl": "/api/mcp/uploads/missing"}},
	} {
		t.Run(scenario.name+"-"+stringArg(scenario.args, "id"), func(t *testing.T) { expectMCPFailure(t, f.call(t, scenario.name, scenario.args)) })
	}
	for _, field := range []string{"state", "assignee", "delegate", "project", "parentId", "cycle", "milestone"} {
		args := map[string]any{"team": f.data.Teams[0].ID, "title": "Must not exist", field: "missing"}
		expectMCPFailure(t, f.call(t, "save_issue", args))
	}
	for _, tool := range []string{"save_project", "save_initiative"} {
		for _, field := range []string{"id", "lead", "owner", "state", "leadTeam"} {
			if tool == "save_project" && (field == "owner" || field == "leadTeam") || tool == "save_initiative" && (field == "lead" || field == "state") {
				continue
			}
			expectMCPFailure(t, f.call(t, tool, map[string]any{"name": "Invalid reference", field: "missing"}))
		}
		for _, field := range []string{"labels", "patch"} {
			args := map[string]any{"name": "Invalid contents", field: []string{"missing"}}
			if field == "patch" {
				args[field] = []map[string]any{{"op": "replace", "old_string": "absent", "new_string": "new"}}
			}
			expectMCPFailure(t, f.call(t, tool, args))
		}
	}
	after := f.repository.Bootstrap()
	if len(after.Issues) != len(before.Issues) || len(after.Projects) != len(before.Projects) || len(after.Initiatives) != len(before.Initiatives) {
		t.Fatal("invalid MCP operation partially persisted")
	}
}

func TestMCPProjectInitiativeAndReleasePropertyRoundTrips(t *testing.T) {
	f := newMCPContractFixture(t)
	call := func(name string, args map[string]any) map[string]any {
		t.Helper()
		return mcpSuccess(t, f.call(t, name, args)).(map[string]any)
	}
	label := call("create_initiative_label", map[string]any{"name": "Strategy", "description": "Scope", "color": "#112233"})
	parent := call("save_initiative", map[string]any{"name": "Parent"})
	initiative := call("save_initiative", map[string]any{"name": "Child", "summary": "Summary", "description": "Old description", "priority": 2, "status": "planned", "owner": "me", "leadTeam": f.data.Teams[0].ID, "labels": []any{label["id"]}, "parentInitiatives": []any{parent["id"]}, "targetDate": "2026-12-31", "icon": "Flag", "color": "#334455"})
	updated := call("save_initiative", map[string]any{"id": initiative["id"], "owner": nil, "leadTeam": nil, "labels": []string{}, "parentInitiatives": []string{}, "patch": []map[string]any{{"op": "replace", "old_string": "Old", "new_string": "New"}}})
	if updated["description"] != "New description" || updated["owner"] != nil || len(updated["labelIds"].([]any)) != 0 {
		t.Fatal("initiative clearing/patching failed")
	}
	project := call("save_project", map[string]any{"name": "Property project", "summary": "Summary", "description": "Old project", "setTeams": []string{f.data.Teams[0].Key}, "setInitiatives": []any{parent["id"]}, "lead": "me", "state": f.data.ProjectStatuses[0].ID, "priority": 3, "labels": []string{"label_product"}, "startDate": "2026-09-01", "startDateResolution": "month", "targetDate": "2026-12-31", "targetDateResolution": "quarter", "icon": "Project", "color": "#123456", "links": []map[string]string{{"title": "Reference", "url": "https://example.test/reference"}}})
	project = call("save_project", map[string]any{"id": project["id"], "lead": nil, "labels": []string{}, "removeInitiatives": []any{parent["id"]}, "patch": []map[string]any{{"op": "replace", "old_string": "Old", "new_string": "New"}}})
	if project["description"] != "New project" || project["lead"] != nil || len(project["labelIds"].([]any)) != 0 {
		t.Fatal("project clearing/patching failed")
	}
	milestone := call("save_milestone", map[string]any{"project": project["id"], "name": "Milestone", "description": "Scope", "targetDate": "2026-12-15"})
	call("save_milestone", map[string]any{"project": project["id"], "id": milestone["id"], "name": "Renamed", "targetDate": nil})
	if err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "test.pipeline", "", nil, func(d *domain.Bootstrap) error {
		d.ReleasePipelines = []domain.ReleasePipeline{{ID: "risk-pipeline", Name: "Pipeline", Type: "scheduled", Stages: []string{"Planned", "Shipping"}, StageStatuses: map[string]string{"Planned": "planned", "Shipping": "inProgress"}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	release := call("save_release", map[string]any{"name": "Release", "pipeline": "risk-pipeline", "version": "1.2.3", "description": "Details", "stage": "started", "commitSha": "abc123", "createdAt": "2026-09-01T12:00:00Z", "startDate": "2026-09-02", "startedAt": "2026-09-02T12:00:00Z", "completedAt": "2026-09-03T12:00:00Z", "targetDate": "2026-09-04"})
	if release["stage"] != "Shipping" || release["createdAt"] != "2026-09-01T12:00:00Z" {
		t.Fatal("release import lost timestamps/stage")
	}
	release = call("save_release", map[string]any{"id": release["id"], "stage": "Planned", "startDate": nil, "startedAt": nil, "completedAt": nil})
	if release["startDate"] != nil || release["startedAt"] != nil || release["releasedAt"] != nil {
		t.Fatal("release timestamp clearing failed")
	}
	for _, field := range []string{"createdAt", "startDate", "startedAt", "completedAt"} {
		before := len(f.repository.Bootstrap().Releases)
		expectMCPFailure(t, f.call(t, "save_release", map[string]any{"name": "Invalid date", "pipeline": "risk-pipeline", field: "not-a-date"}))
		if len(f.repository.Bootstrap().Releases) != before {
			t.Errorf("invalid %s partially created release", field)
		}
	}
}

func TestMCPOAuthRejectsUnsafeRegistrationAndAuthorization(t *testing.T) {
	f := newMCPContractFixture(t)
	for _, override := range []map[string]any{
		{"redirect_uris": []string{}}, {"redirect_uris": []string{"https://example.test/callback#fragment"}},
		{"redirect_uris": []string{"http://example.test/callback"}}, {"client_uri": "javascript:alert(1)"},
		{"logo_uri": "http://example.test/logo"}, {"client_name": strings.Repeat("x", 201)},
		{"grant_types": []string{"password"}}, {"response_types": []string{"token"}}, {"token_endpoint_auth_method": "client_secret_basic"},
	} {
		body := map[string]any{"client_name": "Boundary client", "redirect_uris": []string{"http://127.0.0.1:48111/callback"}}
		for k, v := range override {
			body[k] = v
		}
		raw, _ := json.Marshal(body)
		resp, err := http.Post(f.host.URL+"/oauth/register", "application/json", bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 400 {
			t.Fatalf("unsafe registration accepted: %v (%d)", override, resp.StatusCode)
		}
	}
	client := authClient(t)
	authRequest[domain.AuthSession](t, client, "POST", f.host.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", 200)
	registered := authRequest[domain.OAuthClient](t, client, "POST", f.host.URL+"/oauth/register", map[string]any{"client_name": "Boundary client", "redirect_uris": []string{"http://127.0.0.1:48111/callback"}}, "", 201)
	for _, override := range []map[string]string{{"client_id": "missing"}, {"redirect_uri": "https://example.test/callback"}, {"response_type": "token"}, {"code_challenge": ""}, {"code_challenge_method": "plain"}, {"scope": "admin:everything"}, {"resource": f.host.URL + "/mcp/readonly", "scope": "write"}} {
		query := url.Values{"response_type": {"code"}, "client_id": {registered.ClientID}, "redirect_uri": {registered.RedirectURIs[0]}, "code_challenge": {strings.Repeat("a", 43)}, "code_challenge_method": {"S256"}, "scope": {"read"}}
		for k, v := range override {
			query.Set(k, v)
		}
		authRequest[map[string]any](t, client, "GET", f.host.URL+"/api/oauth/authorization-request?"+query.Encode(), nil, "", 400)
	}
	denied := authRequest[map[string]string](t, client, "POST", f.host.URL+"/api/oauth/authorization-request", map[string]any{"clientId": registered.ClientID, "redirectUri": registered.RedirectURIs[0], "responseType": "code", "codeChallenge": strings.Repeat("a", 43), "codeChallengeMethod": "S256", "scope": "read", "state": "deny-state", "approve": false}, "", 200)
	redirect, err := url.Parse(denied["redirect"])
	if err != nil || redirect.Query().Get("error") != "access_denied" || redirect.Query().Get("code") != "" || redirect.Query().Get("state") != "deny-state" {
		t.Fatal("denial minted a code or lost state")
	}
}

type failedMCPObjectStore struct{ deleted int }

func (*failedMCPObjectStore) Put(context.Context, string, io.Reader, string) (int64, error) {
	return 1, errors.New("storage write interrupted")
}
func (*failedMCPObjectStore) Open(context.Context, string) (io.ReadCloser, string, int64, error) {
	return nil, "", 0, errors.New("missing")
}
func (s *failedMCPObjectStore) Delete(context.Context, string) error { s.deleted++; return nil }

func TestMCPAttachmentStorageFailureCleansPartialObject(t *testing.T) {
	f := newMCPContractFixture(t)
	objects := &failedMCPObjectStore{}
	f.service.objectStore = objects
	digest := sha256.Sum256([]byte("content"))
	args := map[string]any{"issue": f.data.Issues[0].ID, "filename": "test.txt", "contentType": "text/plain", "base64Content": base64.StdEncoding.EncodeToString([]byte("content")), "sha256": hex.EncodeToString(digest[:])}
	expectMCPFailure(t, f.call(t, "create_attachment", args))
	if objects.deleted != 1 {
		t.Error("partial uploaded object was not deleted")
	}
	issue, err := f.repository.IssueRecord(t.Context(), f.data.Workspace.URLKey, f.data.Issues[0].ID)
	if err != nil || len(issue.Attachments) != 0 {
		t.Fatal("failed upload persisted attachment")
	}
	args["base64Content"] = "invalid base64"
	expectMCPFailure(t, f.call(t, "create_attachment", args))
	args["base64Content"] = base64.StdEncoding.EncodeToString([]byte("content"))
	args["sha256"] = strings.Repeat("0", 64)
	expectMCPFailure(t, f.call(t, "create_attachment", args))
	f.service.mcpUploads = map[string]*mcpPendingUpload{"expired": {ExpiresAt: time.Now().Add(-time.Hour)}}
	f.service.cleanupMCPUploads(t.Context())
	if len(f.service.mcpUploads) != 0 {
		t.Fatal("expired unused upload retained")
	}
}
