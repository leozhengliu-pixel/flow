package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func TestAuthorizationBodyRouteFamilies(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_METADATA_CATALOG", "on")
	for _, mode := range []string{"off", "on"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("FLOW_BOUNDED_METADATA", mode)
			f := newMCPContractFixture(t)
			if f.repository.BoundedMetadataEnabled() != (mode == "on") {
				t.Fatal("unexpected metadata mode")
			}
			workspace := f.data.Workspace.URLKey
			teams := "/api/workspaces/" + workspace + "/teams"
			team := f.data.Teams[0].ID
			const member = "usr_member"
			if err := f.repository.UpdateMemberRole(t.Context(), f.data.Workspace.ID, member, "member"); err != nil {
				t.Fatal(err)
			}
			if err := f.repository.SetTeamMembership(t.Context(), f.data.Workspace.ID, team, member, "member", true); err != nil {
				t.Fatal(err)
			}
			secret := f.addMCPKey(t, "body-member", member, []string{"read", "write"}, []string{team})
			admin := f.secret
			adminReorder := f.addMCPKey(t, "body-admin", f.data.Viewer.ID, []string{"admin", "read", "write"}, nil)
			const stageSecret = "body-release-stage"
			if err := f.repository.MutateWorkspace(workspaceFixtureContext(t.Context()), workspace, "api_key.created", "body-stage", nil, func(data *domain.Bootstrap) error {
				data.APIKeys = append(data.APIKeys, domain.APIKey{ID: "body-stage", SecretHash: secretHash(stageSecret),
					CreatorID: data.Viewer.ID, Actor: "app", Scopes: []string{"read", "releaseStage:update"}, CreatedAt: data.Workspace.CreatedAt})
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			var hiddenTeam domain.Team
			var issue, hiddenIssue domain.Issue
			var project, hiddenProject domain.Project
			var initiative domain.Initiative
			var document domain.Document
			var view domain.SavedView
			var dashboard domain.Dashboard
			var customerRequest domain.CustomerRequest
			var parent, otherParent domain.Comment
			var release, hiddenRelease domain.Release
			var pipeline, hiddenPipeline domain.ReleasePipeline
			seed := func(method, path string, fields map[string]any, status int, out any) {
				t.Helper()
				raw := applicationBearerRequest(t, f.host, admin, method, path, string(mustJSON(t, fields)), status)
				if out != nil && json.Unmarshal(raw, out) != nil {
					t.Fatalf("could not decode fixture for %s", path)
				}
			}
			seed("PATCH", "/api/teams/"+team+"/settings", map[string]any{"settingsPermission": "allMembers", "memberPermission": "owners"}, 200, nil)
			seed("POST", teams, map[string]any{"name": "Restricted", "key": "RSTR", "private": true}, 201, &hiddenTeam)
			seed("POST", "/api/issues", map[string]any{"title": "Visible", "teamId": team}, 201, &issue)
			seed("POST", "/api/issues", map[string]any{"title": "Restricted", "teamId": hiddenTeam.ID}, 201, &hiddenIssue)
			seed("POST", "/api/issues/"+issue.ID+"/comments", map[string]any{"body": "Visible comment"}, 201, &parent)
			seed("POST", "/api/issues/"+hiddenIssue.ID+"/comments", map[string]any{"body": "Restricted comment"}, 201, &otherParent)
			seed("POST", "/api/projects", map[string]any{"name": "Visible", "teamIds": []string{team}}, 201, &project)
			seed("POST", "/api/projects", map[string]any{"name": "Restricted", "teamIds": []string{hiddenTeam.ID}}, 201, &hiddenProject)
			seed("POST", "/api/initiatives", map[string]any{"name": "Visible", "leadTeamId": team}, 201, &initiative)
			seed("POST", "/api/documents", map[string]any{"title": "Visible", "teamIds": []string{team}}, 201, &document)
			seed("POST", "/api/release-pipelines", map[string]any{"name": "Visible", "teamIds": []string{team}}, 201, &pipeline)
			seed("POST", "/api/release-pipelines", map[string]any{"name": "Restricted", "teamIds": []string{hiddenTeam.ID}}, 201, &hiddenPipeline)
			seed("POST", "/api/releases", map[string]any{"name": "Visible", "pipelineId": pipeline.ID}, 201, &release)
			seed("POST", "/api/releases", map[string]any{"name": "Restricted", "pipelineId": hiddenPipeline.ID}, 201, &hiddenRelease)
			raw := applicationBearerRequest(t, f.host, secret, http.MethodPost, "/api/views", `{"name":"Owned by member"}`, http.StatusCreated)
			if err := json.Unmarshal(raw, &view); err != nil {
				t.Fatal(err)
			}
			raw = applicationBearerRequest(t, f.host, secret, http.MethodPost, "/api/dashboards", `{"name":"Owned by member"}`, http.StatusCreated)
			if err := json.Unmarshal(raw, &dashboard); err != nil {
				t.Fatal(err)
			}
			seed("POST", "/api/customer-requests", map[string]any{"body": "Visible request", "issueId": issue.ID}, 201, &customerRequest)
			issueIDs := func(id string) map[string]any {
				return map[string]any{"issueIds": []string{id}, "update": map[string]any{"title": "Edited"}}
			}
			field := func(name string, value any) map[string]any { return map[string]any{name: value} }
			cases := []struct {
				method, path, credential string
				allow, deny              any
			}{
				{"POST", "/api/issues", secret, map[string]any{"title": "Create", "teamId": team}, map[string]any{"title": "Create", "teamId": hiddenTeam.ID}},
				{"POST", "/api/issue-records", secret, map[string]any{"title": "Create", "parentId": issue.ID, "teamId": team}, map[string]any{"title": "Create", "parentId": hiddenIssue.ID, "teamId": team}},
				{"PATCH", "/api/issues/" + issue.ID, secret, field("projectId", project.ID), field("projectId", hiddenProject.ID)},
				{"PATCH", "/api/issue-records/" + issue.ID, secret, field("projectId", project.ID), field("projectId", hiddenProject.ID)},
				{"POST", "/api/issues/batch", secret, issueIDs(issue.ID), issueIDs(hiddenIssue.ID)},
				{"POST", "/api/issue-records/batch", secret, issueIDs(issue.ID), issueIDs(hiddenIssue.ID)},
				{"POST", "/api/issues/" + issue.ID + "/relations", secret, field("relatedIssueId", issue.ID), field("relatedIssueId", hiddenIssue.ID)},
				{"POST", "/api/issue-records/" + issue.ID + "/relations", secret, field("relatedIssueId", issue.ID), field("relatedIssueId", hiddenIssue.ID)},
				{"PUT", "/api/issues/" + issue.ID + "/releases", secret, field("releaseIds", []string{release.ID}), field("releaseIds", []string{hiddenRelease.ID})},
				{"PUT", "/api/issue-records/" + issue.ID + "/releases", secret, field("releaseIds", []string{release.ID}), field("releaseIds", []string{hiddenRelease.ID})},
				{"POST", "/api/projects", secret, field("teamIds", []string{team}), field("teamIds", []string{hiddenTeam.ID})},
				{"PATCH", "/api/projects/" + project.ID, secret, field("dependencyIds", []string{project.ID}), field("dependencyIds", []string{hiddenProject.ID})},
				{"POST", "/api/projects/" + project.ID + "/relations", secret, field("relatedProjectId", project.ID), field("relatedProjectId", hiddenProject.ID)},
				{"POST", "/api/initiatives", secret, field("leadTeamId", team), field("leadTeamId", hiddenTeam.ID)},
				{"PATCH", "/api/initiatives/" + initiative.ID, secret, field("projectIds", []string{project.ID}), field("projectIds", []string{hiddenProject.ID})},
				{"POST", "/api/initiatives/" + initiative.ID + "/relations", secret, field("relatedInitiativeId", initiative.ID), field("relatedInitiativeId", "absent")},
				{"POST", "/api/views", secret, field("projectId", project.ID), field("projectId", hiddenProject.ID)},
				{"PATCH", "/api/views/" + view.ID, secret, field("projectId", project.ID), field("ownerId", f.data.Viewer.ID)},
				{"POST", "/api/dashboards", secret, field("teamIds", []string{team}), field("teamIds", []string{hiddenTeam.ID})},
				{"PATCH", "/api/dashboards/" + dashboard.ID, secret, field("teamIds", []string{team}), field("ownerId", f.data.Viewer.ID)},
				{"POST", "/api/customer-requests", secret, field("issueId", issue.ID), field("issueId", hiddenIssue.ID)},
				{"PATCH", "/api/customer-requests/" + customerRequest.ID, secret, field("issueId", issue.ID), field("issueId", hiddenIssue.ID)},
				{"POST", "/api/asks", secret, field("teamId", team), field("teamId", hiddenTeam.ID)},
				{"POST", "/api/documents", secret, field("projectIds", []string{project.ID}), field("projectIds", []string{hiddenProject.ID})},
				{"PATCH", "/api/documents/" + document.ID, secret, field("projectIds", []string{project.ID}), field("projectIds", []string{hiddenProject.ID})},
				{"POST", "/api/releases", secret, field("pipelineId", pipeline.ID), field("pipelineId", hiddenPipeline.ID)},
				{"PATCH", "/api/releases/" + release.ID, secret, field("projectIds", []string{project.ID}), field("projectIds", []string{hiddenProject.ID})},
				{"PATCH", "/api/releases/" + release.ID, stageSecret, field("stageFrozen", false), map[string]any{"stageFrozen": false, "name": "Outside app scope"}},
				{"POST", "/api/releases/reorder", secret, field("ids", []string{release.ID}), field("ids", []string{hiddenRelease.ID})},
				{"POST", "/api/release-pipelines", secret, field("teamIds", []string{team}), field("teamIds", []string{hiddenTeam.ID})},
				{"PATCH", "/api/release-pipelines/" + pipeline.ID, secret, field("teamIds", []string{team}), field("teamIds", []string{hiddenTeam.ID})},
				{"POST", "/api/release-pipelines/reorder", adminReorder, field("ids", []string{pipeline.ID}), field("ids", []string{"absent"})},
				{"PATCH", teams + "/" + team, secret, field("name", "Edited"), field("Private", true)},
				{"PATCH", "/api/teams/" + team + "/settings", secret, field("description", "Edited"), field("SettingsPermission", "owners")},
				{"PUT", teams + "/" + team + "/members/" + member, secret, field("member", true), map[string]any{"member": true, "role": "owner"}},
			}
			// A sentinel after the real authentication middleware distinguishes
			// a permission refusal from a handler validation error.
			handler := requestBodyBudget(f.service.documentRequestBodies(boundedLegacyIssueWrites(f.service.authenticate(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			})))))
			padding := strings.Repeat(" ", 1<<20)
			for _, tc := range cases {
				t.Run(tc.method+" "+tc.path, func(t *testing.T) {
					serve, allowedStatus := handler, http.StatusNoContent
					if tc.path == "/api/issues" || tc.path == "/api/issue-records" {
						// Create checks its destinations in the record handler.
						serve, allowedStatus = f.host.Config.Handler, http.StatusCreated
					}
					check := func(body, contentType string, status int) {
						t.Helper()
						r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(body))
						r.Header.Set("Authorization", "Bearer "+tc.credential)
						r.Header.Set("X-Workspace-Key", workspace)
						r.Header.Set("Content-Type", contentType)
						w := httptest.NewRecorder()
						serve.ServeHTTP(w, r)
						if w.Code != status {
							t.Fatalf("size=%d: status %d, want %d: %s", len(body), w.Code, status, w.Body)
						}
						if status == http.StatusBadRequest {
							assertInvalidAuthorizationBody(t, w.Body.Bytes())
						}
					}
					allowed, denied := string(mustJSON(t, tc.allow)), string(mustJSON(t, tc.deny))
					check(allowed, "application/json", allowedStatus)
					check(padding+allowed, "application/json", allowedStatus)
					denyStatus := http.StatusForbidden
					if tc.credential == stageSecret {
						denyStatus = http.StatusUnauthorized // Credential scope refusal.
					}
					check(padding+denied, "text/plain", denyStatus)
					check(`{"title":`, "application/json", http.StatusBadRequest)
				})
			}
			t.Run("comment parent scope", func(t *testing.T) {
				path := "/api/issue-records/" + issue.ID + "/comments"
				payload := func(id string) string {
					return string(mustJSON(t, map[string]any{"body": "Reply", "parentId": id}))
				}
				for _, prefix := range []string{"", padding} {
					raw := applicationBearerRequest(t, f.host, secret, http.MethodPost, path, prefix+payload(parent.ID), http.StatusCreated)
					var reply domain.Comment
					if json.Unmarshal(raw, &reply) != nil || reply.ParentID == nil || *reply.ParentID != parent.ID {
						t.Fatal("the comment handler lost the authorized parent")
					}
				}
				applicationBearerRequest(t, f.host, secret, http.MethodPost, path, padding+payload(otherParent.ID), http.StatusNotFound)
				assertInvalidAuthorizationBody(t, applicationBearerRequest(t, f.host, secret, http.MethodPost, path, `{"body":"Reply","parentId":`, http.StatusBadRequest))
			})
			for _, path := range []string{"/api/cycles/" + f.data.Cycles[0].ID, "/api/reviews/" + f.data.Reviews[0].ID, "/api/initiatives/" + initiative.ID, "/api/documents/" + document.ID} {
				t.Run("favorite "+path, func(t *testing.T) {
					credential := secret
					if strings.HasPrefix(path, "/api/initiatives/") {
						credential = admin
					}
					for _, tc := range []struct {
						body   string
						status int
					}{{`{"favorite":true}`, 204}, {padding + `{"favorite":true}`, 204}, {`{"favorite":`, 400}, {`{"favorite":123}`, 400}} {
						r := httptest.NewRequest(http.MethodPatch, path, strings.NewReader(tc.body))
						r.Header.Set("Authorization", "Bearer "+credential)
						r.Header.Set("X-Workspace-Key", workspace)
						w := httptest.NewRecorder()
						handler.ServeHTTP(w, r)
						if w.Code != tc.status {
							t.Fatalf("size=%d: status %d, want %d: %s", len(tc.body), w.Code, tc.status, w.Body)
						}
					}
				})
			}
		})
	}
}

func assertInvalidAuthorizationBody(t *testing.T, body []byte) {
	t.Helper()
	var result struct{ Error string }
	if json.Unmarshal(body, &result) != nil || result.Error != "invalid JSON" {
		t.Fatalf("unexpected invalid-body response: %s", body)
	}
}

type authorizationReadError struct{}

func (authorizationReadError) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func TestAuthorizationBodyFramingAndReuse(t *testing.T) {
	defer jsonRequestLimitOverride.Store(jsonRequestLimitOverride.Load())
	const limit = 128
	jsonRequestLimitOverride.Store(limit)
	type input struct {
		TeamID string `json:"teamId"`
	}
	const valid = `{"teamId":"team\u0000-id"}`
	var gz bytes.Buffer
	stream := gzip.NewWriter(&gz)
	if _, err := io.WriteString(stream, valid); err != nil {
		t.Fatal(err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, encoding string
		body           io.Reader
		status         int
	}{
		{"normal", "", strings.NewReader(valid), 204},
		{"exact-chunked-limit", "", io.LimitReader(strings.NewReader(valid+strings.Repeat(" ", limit)), limit), 204},
		{"oversized", "", strings.NewReader(valid + strings.Repeat(" ", limit)), 413},
		{"oversized-chunked", "", io.LimitReader(strings.NewReader(valid+strings.Repeat(" ", limit)), limit+1), 413},
		{"invalid", "", strings.NewReader("{"), 400},
		{"invalid-field-type", "", strings.NewReader(`{"teamId":7}`), 400},
		{"empty", "", http.NoBody, 400},
		{"null", "", strings.NewReader("null"), 400},
		{"second-value", "", strings.NewReader(valid + `{}`), 400},
		{"invalid-trailer", "", strings.NewReader(valid + `x`), 400},
		{"partial-read-error", "", io.MultiReader(strings.NewReader(valid), authorizationReadError{}), 400},
		{"gzip", "gzip", &gz, 400},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/issues", tc.body)
			r.Header.Set("Content-Encoding", tc.encoding)
			w := httptest.NewRecorder()
			var permitted input
			if peekStrictRequestJSON(r, &permitted) {
				var decoded input
				if !decodeStrictJSON(w, r, &decoded) || decoded != permitted || decoded.TeamID != "team-id" {
					t.Fatal("the handler did not receive the same normalized input")
				}
				w.WriteHeader(http.StatusNoContent)
			} else if !writeAuthorizationBodyError(w, r) {
				t.Fatal("a failed peek did not refuse the request")
			}
			if w.Code != tc.status {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.status, w.Body)
			}
			if tc.status == http.StatusBadRequest {
				assertInvalidAuthorizationBody(t, w.Body.Bytes())
			}
		})
	}
}

func TestAuthorizationBodyMCP(t *testing.T) {
	f := newMCPContractFixture(t)
	projectID := f.data.Projects[0].ID
	content := strings.Repeat("x", (1<<20)+1)
	args := map[string]any{"name": "Large view", "description": content, "project": projectID}
	view := mcpObject(t, f, "save_view", args)
	if view["projectId"] != projectID {
		t.Fatal("the tool did not preserve its project binding")
	}

	raw := string(mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "save_view", "arguments": map[string]any{"name": "Malformed view"}}}))
	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(raw+"{"))
	request.Header.Set("Authorization", "Bearer "+f.secret)
	response := httptest.NewRecorder()
	f.service.mcpHTTP(false)(response, request)
	var rpc mcpRPCResponse
	if response.Code != http.StatusBadRequest || json.Unmarshal(response.Body.Bytes(), &rpc) != nil ||
		rpc.Error == nil || rpc.Error.Code != -32700 || rpc.Error.Message != "Parse error" {
		t.Fatal("malformed tool request did not produce a parse error")
	}
	t.Run("oversized chunked envelope", func(t *testing.T) {
		defer jsonRequestLimitOverride.Store(jsonRequestLimitOverride.Load())
		jsonRequestLimitOverride.Store(512)
		request := httptest.NewRequest(http.MethodPost, "/mcp", io.MultiReader(strings.NewReader(raw), strings.NewReader(strings.Repeat(" ", 512))))
		request.Header.Set("Authorization", "Bearer "+f.secret)
		request.Header.Set("Content-Type", "text/plain")
		response := httptest.NewRecorder()
		f.service.mcpHTTP(false)(response, request)
		if response.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("oversized tool request got %d, want 413", response.Code)
		}
	})

	// The in-process adapter must also enforce the requester and actor's
	// permissions, and their checks must leave the complete body available.
	actor := mcpActor{WorkspaceKey: f.data.Workspace.URLKey, User: f.data.Viewer, AuthorizationUserID: "usr_member",
		APIKey: domain.APIKey{ID: "route-key", Scopes: []string{"read", "write"}}}
	ctx := context.WithValue(t.Context(), internalRequesterAuthorizationKey{}, internalRequesterAuthorization{service: f.service, actor: actor})
	for _, tc := range []struct {
		name    string
		project any
		wantErr string
	}{
		{"allowed", projectID, ""},
		{"unauthorized", "outside", "This resource is outside your teams"},
		{"wrong-type", true, "invalid JSON"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			_, err := f.service.invokeMCPRoute(ctx, actor, http.MethodPost, "/api/views", nil,
				map[string]any{"name": "MCP view", "description": content, "projectId": tc.project},
				func(w http.ResponseWriter, r *http.Request) {
					called = true
					var input domain.SavedViewMutationInput
					if !decodeJSON(w, r, &input) || input.Description == nil || *input.Description != content ||
						input.ProjectID == nil || *input.ProjectID != projectID {
						t.Error("the tool handler lost fields from the authorized input")
					}
				})
			if tc.wantErr == "" && (err != nil || !called) || tc.wantErr != "" && (err == nil || err.Error() != tc.wantErr || called) {
				t.Fatalf("handler reached=%t, error=%v, want %q", called, err, tc.wantErr)
			}
		})
	}
}

// A retry that rebuilds the request from the context saved before the first
// read (issueRecordAuthorizationData on a policy change) still sees the body.
func TestAuthorizationBodySurvivesContextRebuild(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/api/issue-records/i1/relations", strings.NewReader(`{"relatedIssueId":"i2","type":"blocks"}`))
	prepareAuthorizationBody(r)
	ctx := r.Context()
	var first domain.RelationCreateInput
	if !peekRequestJSON(r, &first) {
		t.Fatal("first read failed")
	}
	*r = *r.WithContext(context.WithValue(ctx, issueRequestAccessKey{}, nil))
	var again domain.RelationCreateInput
	if !peekRequestJSON(r, &again) || again != first {
		t.Fatalf("the rebuilt request lost the body: %v", authorizationBodyError(r))
	}
}

// Authorization's strict decode answers an unsupported field the way the
// handler's own decode does, for signed-in callers too.
func TestAuthorizationStrictDecodeNamesUnsupportedField(t *testing.T) {
	server := newSingleAuthServer(t)
	admin := peerAdmin(t, server.URL)
	team := authRequest[domain.Team](t, admin, http.MethodPost, server.URL+"/api/workspaces/test-workspace/teams", map[string]any{"name": "Strict", "key": "STR"}, "", http.StatusCreated)
	issue := authRequest[domain.Issue](t, admin, http.MethodPost, server.URL+"/api/issues", map[string]any{"title": "S", "teamId": team.ID}, "test-workspace", http.StatusCreated)
	for _, path := range []string{"/api/issues/" + issue.ID, "/api/issue-records/" + issue.ID} {
		response := authRequest[map[string]any](t, admin, http.MethodPatch, server.URL+path, map[string]any{"title": "x", "team": "MOV"}, "test-workspace", http.StatusBadRequest)
		if response["error"] != `Unsupported field "team"` {
			t.Fatalf("PATCH %s answered %v", path, response)
		}
	}
}
