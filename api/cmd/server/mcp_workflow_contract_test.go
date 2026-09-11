package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func mcpSuccess(t *testing.T, response map[string]any) any {
	t.Helper()
	result, ok := response["result"].(map[string]any)
	if !ok || result["isError"] == true {
		t.Fatalf("MCP request failed: %v", response)
	}
	return result["structuredContent"]
}

func TestMCPAllToolsThroughRealSDK(t *testing.T) {
	f := newMCPContractFixture(t)
	// Allow the local provider stub; MCP still enforces its bearer credentials.
	f.service.authDisabled = true
	var merged atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "PUT" && strings.HasSuffix(r.URL.Path, "/merge"):
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["merge_method"] != "rebase" {
				t.Errorf("merge method not forwarded: %v", body)
			}
			merged.Add(1)
			writeJSON(w, 200, map[string]any{"merged": true})
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/reviews"):
			writeJSON(w, 200, map[string]any{"id": 12})
		default:
			t.Errorf("unexpected provider request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer upstream.Close()
	if err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "test.contract", "", nil, func(d *domain.Bootstrap) error {
		d.Documents = append(d.Documents, domain.Document{ID: "contract-doc", Title: "Contract document", Content: "Detailed content", Creator: d.Viewer, TeamIDs: []string{d.Teams[0].ID}})
		d.ReleasePipelines = append(d.ReleasePipelines, domain.ReleasePipeline{ID: "contract-pipeline", Name: "Contract pipeline", Type: "scheduled", TeamIDs: []string{d.Teams[0].ID}, Stages: []string{"Planned"}, StageStatuses: map[string]string{"Planned": "planned"}})
		settings := d.UserSettings[d.Viewer.ID]
		settings.AgentInstructions = "Use the workspace conventions."
		d.UserSettings[d.Viewer.ID] = settings
		d.Reviews[0].Events = append(d.Reviews[0].Events, domain.ReviewEvent{ID: "contract-thread", Type: "commented", Body: "Check this line", Actor: d.Viewer})
		d.IntegrationConnections = append(d.IntegrationConnections, domain.IntegrationConnection{ID: "contract-github", Provider: "github", Status: "connected", OAuthAccessToken: "local-test-token", Config: map[string]string{"apiUrl": upstream.URL, "organization": d.Reviews[0].RepositoryOwner}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "All tools integration", Version: "1"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: f.host.URL + "/mcp", HTTPClient: &http.Client{Transport: mcpAuthTransport{f.secret}, Timeout: 10 * time.Second}, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	seen := map[string]bool{}
	call := func(name string, args map[string]any) any {
		t.Helper()
		result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if result.IsError {
			raw, _ := json.Marshal(result.Content)
			t.Fatalf("%s: %s", name, raw)
		}
		seen[name] = true
		return result.StructuredContent
	}
	object := func(name string, args map[string]any) map[string]any {
		t.Helper()
		value := call(name, args)
		var result map[string]any
		if err := jsonClone(value, &result); err != nil {
			t.Fatalf("%s result: %v", name, err)
		}
		return result
	}
	team, project, issue, review := f.data.Teams[0].ID, f.data.Projects[0].ID, f.data.Issues[0].ID, f.data.Reviews[0].ID
	for _, scenario := range []struct {
		name string
		args map[string]any
	}{
		{"get_workspace", nil}, {"list_teams", nil}, {"get_team", map[string]any{"query": team}},
		{"list_users", map[string]any{"team": team}}, {"get_user", map[string]any{"query": "me"}},
		{"list_issue_statuses", map[string]any{"team": team}}, {"get_issue_status", map[string]any{"team": team, "name": "Todo"}},
		{"list_issue_labels", map[string]any{"team": team}}, {"list_project_labels", nil}, {"list_initiative_labels", nil},
		{"list_issues", map[string]any{"team": team, "limit": 2}}, {"list_cycles", map[string]any{"teamId": team}},
		{"list_projects", nil}, {"get_project", map[string]any{"query": project}}, {"list_milestones", map[string]any{"project": project}},
		{"list_initiatives", nil}, {"list_documents", nil}, {"get_document", map[string]any{"id": "contract-doc"}},
		{"list_comments", map[string]any{"issueId": issue}}, {"get_status_updates", map[string]any{"type": "project"}},
		{"list_release_pipelines", nil}, {"list_releases", nil}, {"list_release_notes", nil},
		{"list_diffs", nil}, {"get_diff", map[string]any{"urlOrId": review}}, {"get_diff_threads", map[string]any{"urlOrId": review}},
		{"extract_images", map[string]any{"markdown": "![image](https://example.test/image.png)"}},
		{"list_agent_skills", nil}, {"get_agent_skill", map[string]any{"id": "flow-workspace-guidance"}}, {"search_documentation", map[string]any{"query": "issues"}},
	} {
		call(scenario.name, scenario.args)
	}
	call("create_issue_label", map[string]any{"name": "SDK label"})
	call("create_initiative_label", map[string]any{"name": "SDK initiative label"})
	created := object("save_issue", map[string]any{"team": team, "title": "SDK created", "description": "Line one\n\n**Markdown**"})
	call("save_issue", map[string]any{"id": created["id"], "title": "SDK updated"})
	call("save_project", map[string]any{"name": "SDK project", "setTeams": []string{team}})
	initiative := object("save_initiative", map[string]any{"name": "SDK initiative", "leadTeam": team})
	call("get_initiative", map[string]any{"query": initiative["id"]})
	call("save_milestone", map[string]any{"project": project, "name": "SDK milestone"})
	call("save_release", map[string]any{"name": "SDK release", "pipeline": "contract-pipeline"})
	comment := object("save_comment", map[string]any{"issueId": issue, "body": "SDK comment"})
	call("save_comment", map[string]any{"id": comment["id"], "body": "Edited comment"})
	call("delete_comment", map[string]any{"id": comment["id"]})
	body := []byte("local attachment")
	digest := sha256.Sum256(body)
	attachment := object("create_attachment", map[string]any{"issue": issue, "filename": "contract.txt", "contentType": "text/plain", "base64Content": base64.StdEncoding.EncodeToString(body), "sha256": hex.EncodeToString(digest[:]), "size": len(body)})
	call("delete_attachment", map[string]any{"id": attachment["id"]})
	prepared := object("prepare_attachment_upload", map[string]any{"issue": issue, "filename": "uploaded.txt", "contentType": "text/plain", "size": len(body)})
	req, _ := http.NewRequest("PUT", prepared["uploadUrl"].(string), bytes.NewReader(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 204 {
		t.Fatalf("upload status=%d", resp.StatusCode)
	}
	uploaded := object("create_attachment_from_upload", map[string]any{"issue": issue, "assetUrl": prepared["assetUrl"]})
	call("delete_attachment", map[string]any{"id": uploaded["id"]})
	call("resolve_diff_thread", map[string]any{"threadId": "contract-thread", "resolved": true})
	call("delete_diff_comment", map[string]any{"commentId": "contract-thread"})
	call("submit_diff_review", map[string]any{"urlOrId": review, "decision": "approved"})
	call("merge_diff", map[string]any{"urlOrId": review, "mergeMethod": "REBASE"})
	if merged.Load() != 1 {
		t.Fatal("merge did not reach fake provider")
	}
	listed, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range listed.Tools {
		if !seen[tool.Name] {
			t.Errorf("tool lacks a successful SDK scenario: %s", tool.Name)
		}
	}
	latest, err := f.repository.IssueRecord(t.Context(), f.data.Workspace.URLKey, created["id"].(string))
	if err != nil || latest.Title != "SDK updated" || latest.Description != "Line one\n\n**Markdown**" {
		t.Fatalf("write/read integrity: %v %v", latest, err)
	}
	t.Logf("%d advertised tools successfully exercised through official SDK", len(seen))
}

func TestMCPUploadOwnershipRetryExpiryAndConcurrency(t *testing.T) {
	repo, actor, ctx := newMCPToolTestContext(t)
	s := &server{store: repo, uploadPath: t.TempDir()}
	data := repo.Bootstrap()
	body := "upload content"
	prepare := func() (string, string) {
		t.Helper()
		value, err := s.prepareMCPAttachmentUpload(ctx, actor, data, map[string]any{"issue": data.Issues[0].ID, "filename": "test.txt", "contentType": "text/plain", "size": float64(len(body))})
		if err != nil {
			t.Fatal(err)
		}
		url := value.(map[string]any)["assetUrl"].(string)
		return url, filepath.Base(url)
	}
	put := func(token, body string) int {
		r := httptest.NewRequest("PUT", "/", strings.NewReader(body))
		r.SetPathValue("token", token)
		w := httptest.NewRecorder()
		s.putMCPUpload(w, r)
		return w.Code
	}
	url, token := prepare()
	var successes atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code := put(token, body)
			if code == 204 {
				successes.Add(1)
			} else if code != 409 {
				t.Errorf("concurrent upload status %d", code)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatalf("token accepted %d PUTs", successes.Load())
	}
	args := map[string]any{"assetUrl": url, "issue": data.Issues[0].ID}
	wrong := actor
	wrong.User = data.Users[1]
	if _, err := s.finalizeMCPAttachmentUpload(ctx, wrong, data, args); err == nil {
		t.Fatal("wrong owner accepted")
	}
	wrong = actor
	wrong.WorkspaceKey = "other-workspace"
	if _, err := s.finalizeMCPAttachmentUpload(ctx, wrong, data, args); err == nil {
		t.Fatal("wrong workspace accepted")
	}
	if _, err := s.finalizeMCPAttachmentUpload(ctx, actor, data, map[string]any{"assetUrl": url, "issue": data.Issues[1].ID}); err == nil {
		t.Fatal("wrong issue accepted")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.finalizeMCPAttachmentUpload(canceled, actor, data, args); err == nil {
		t.Fatal("canceled persistence succeeded")
	}
	if _, err := s.finalizeMCPAttachmentUpload(ctx, actor, data, args); err != nil {
		t.Fatalf("failed finalize consumed upload: %v", err)
	}
	if _, err := s.finalizeMCPAttachmentUpload(ctx, actor, data, args); err == nil {
		t.Fatal("replayed finalize accepted")
	}
	url, token = prepare()
	if put(token, body) != 204 {
		t.Fatal("PUT failed")
	}
	s.mcpUploadMu.Lock()
	s.mcpUploads[token].ExpiresAt = time.Now().Add(-time.Second)
	s.mcpUploadMu.Unlock()
	if _, err := s.finalizeMCPAttachmentUpload(ctx, actor, data, map[string]any{"assetUrl": url, "issue": data.Issues[0].ID}); err == nil {
		t.Fatal("expired upload finalized")
	}
	_, token = prepare()
	s.mcpUploadMu.Lock()
	s.mcpUploads[token].ExpectedSize = 0
	s.mcpUploadMu.Unlock()
	if put(token, body) != 400 {
		t.Fatal("nonempty content accepted for zero-byte upload")
	}
}

func TestMCPScopedReviewAndLabelIsolation(t *testing.T) {
	repo, actor, ctx := newMCPToolTestContext(t)
	data := repo.Bootstrap()
	actor.APIKey.TeamIDs = []string{data.Teams[0].ID}
	actor.APIKey.TeamRestriction = "selected"
	ctx = context.WithValue(ctx, apiKeyContextKey{}, actor.APIKey)
	if err := repo.MutateWorkspace(ctx, actor.WorkspaceKey, "test.reviews", "", nil, func(d *domain.Bootstrap) error {
		d.Reviews[0].Events = append(d.Reviews[0].Events, domain.ReviewEvent{ID: "visible-thread", Type: "commented", Actor: actor.User})
		d.Reviews = append(d.Reviews, domain.CodeReview{ID: "unlinked-review", Events: []domain.ReviewEvent{{ID: "hidden-thread", Type: "commented", Actor: actor.User}}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	s := &server{store: repo}
	visible, err := s.mcpWorkspaceData(ctx, actor)
	if err != nil {
		t.Fatal(err)
	}
	if len(visible.Reviews) != 1 || visible.Reviews[0].ID != data.Reviews[0].ID {
		t.Fatalf("scoped reviews missing: %v", visible.Reviews)
	}
	if len(visible.Labels) == 0 {
		t.Fatal("workspace labels hidden")
	}
	for _, name := range []string{"resolve_diff_thread", "delete_diff_comment"} {
		if _, err := s.callFlowTool(ctx, actor, name, map[string]any{"threadId": "hidden-thread", "commentId": "hidden-thread", "resolved": true}); err == nil {
			t.Fatalf("%s mutated hidden review", name)
		}
	}
	if _, err := s.callFlowTool(ctx, actor, "resolve_diff_thread", map[string]any{"threadId": "visible-thread"}); err != nil {
		t.Fatal(err)
	}
	latest := repo.Bootstrap()
	if !latest.Reviews[0].Events[len(latest.Reviews[0].Events)-1].Resolved {
		t.Fatal("resolve default did not resolve")
	}
	for _, resolved := range []bool{true, false} {
		result, err := s.callFlowTool(ctx, actor, "get_diff_threads", map[string]any{"urlOrId": data.Reviews[0].ID, "resolved": resolved})
		if err != nil {
			t.Fatal(err)
		}
		threads := result.([]domain.ReviewEvent)
		if resolved && (len(threads) != 1 || threads[0].ID != "visible-thread") || !resolved && len(threads) != 0 {
			t.Fatalf("thread filtering mixed activity or resolved comments: %v", threads)
		}
	}
}

func TestMCPIssueIndexedPaginationAndProjection(t *testing.T) {
	f := newMCPContractFixture(t)
	issues := make([]domain.Issue, 513)
	for i := range issues {
		issues[i] = f.data.Issues[0]
		issues[i].ID = fmt.Sprintf("paged-%04d", i)
		issues[i].Identifier = fmt.Sprintf("TST-%d", 900000+i)
		issues[i].Number = 900000 + i
		issues[i].Description = "needle only in description"
		issues[i].DescriptionState = strings.Repeat("internal-editor-state", 1024)
		issues[i].Priority = i % 5
	}
	if err := f.repository.ImportIssues(t.Context(), f.data.Workspace.URLKey, issues); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	cursor := ""
	for pages := 0; pages < 10; pages++ {
		value := mcpSuccess(t, f.call(t, "list_issues", map[string]any{"query": "needle only", "team": f.data.Teams[0].Key, "limit": 100, "cursor": cursor, "fields": []string{"title", "description"}})).(map[string]any)
		items := value["items"].([]any)
		for _, item := range items {
			row := item.(map[string]any)
			id := row["id"].(string)
			if seen[id] {
				t.Fatal("duplicate cursor result")
			}
			seen[id] = true
			if len(row) != 3 || row["description"] != "needle only in description" {
				t.Fatalf("field projection ignored: %v", row)
			}
		}
		cursor = value["nextCursor"].(string)
		if cursor == "" {
			break
		}
	}
	if len(seen) != len(issues) {
		t.Fatalf("pagination lost issues: %d/%d", len(seen), len(issues))
	}
	value := mcpSuccess(t, f.call(t, "list_issues", map[string]any{"query": "needle only", "priority": 3, "limit": 250})).(map[string]any)
	for _, item := range value["items"].([]any) {
		row := item.(map[string]any)
		if row["priority"] != float64(3) || row["descriptionState"] != nil || row["documentContent"] != nil {
			t.Fatal("filter/internal projection failed")
		}
	}
	value = mcpSuccess(t, f.call(t, "list_issues", map[string]any{"createdAt": "2099-01-01"})).(map[string]any)
	if len(value["items"].([]any)) != 0 {
		t.Fatal("date filter ignored")
	}
	result := f.call(t, "list_issues", map[string]any{"cursor": "garbage"})
	if result["result"].(map[string]any)["isError"] != true {
		t.Fatal("invalid cursor restarted pagination")
	}
}
