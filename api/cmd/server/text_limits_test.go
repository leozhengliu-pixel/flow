package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// limitCall sends one JSON request and returns the status and decoded body.
func limitCall(t *testing.T, handler http.Handler, method, path string, body any, header map[string]string) (int, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	request := httptest.NewRequest(method, path, bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	for key, value := range header {
		request.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	decoded := map[string]any{}
	_ = json.Unmarshal(recorder.Body.Bytes(), &decoded)
	return recorder.Code, decoded
}

func expectLimitStatus(t *testing.T, what string, status int, body map[string]any, want int, mention string) {
	t.Helper()
	message := fmt.Sprint(body["error"], body["detail"])
	if status != want || mention != "" && !strings.Contains(message, mention) {
		t.Fatalf("%s = %d %v, want %d mentioning %q", what, status, body, want, mention)
	}
}

// Over-long issue titles are a 400 on every write path, on SQLite and on the
// MySQL or Postgres database named by FLOW_TEST_DATABASE_DRIVER/URL, and
// nothing is written; the boundary length (characters, not bytes) is kept.
func TestOverlongIssueTitlesAreRefusedOnEveryPath(t *testing.T) {
	for name, repo := range favoriteTabTestStores(t) {
		t.Run(name, func(t *testing.T) { testOverlongIssueTitles(t, repo) })
	}
}

func testOverlongIssueTitles(t *testing.T, repo *store.SQLiteStore) {
	service := &server{store: repo, uploadPath: t.TempDir(), authDisabled: true}
	service.agent.ToolsEnabled, service.agent.WriteTools = true, true
	handler := newHandler(service)
	data := repo.Bootstrap()
	team := data.Teams[0].ID
	long, edge := strings.Repeat("界", domain.IssueTitleMaxLength+1), strings.Repeat("界", domain.IssueTitleMaxLength)
	before := len(repo.Bootstrap().Issues)
	limit := fmt.Sprint(domain.IssueTitleMaxLength)

	status, body := limitCall(t, handler, http.MethodPost, "/api/issues", map[string]any{"title": long, "teamId": team}, nil)
	expectLimitStatus(t, "REST create", status, body, http.StatusBadRequest, limit)
	status, body = limitCall(t, handler, http.MethodPost, "/api/issue-records", map[string]any{"title": long, "teamId": team}, nil)
	expectLimitStatus(t, "issue-records create", status, body, http.StatusBadRequest, limit)

	status, created := limitCall(t, handler, http.MethodPost, "/api/issues", map[string]any{"title": edge, "teamId": team}, nil)
	expectLimitStatus(t, "boundary create", status, created, http.StatusCreated, "")
	id, _ := created["id"].(string)
	if created["title"] != edge {
		t.Fatalf("boundary title stored as %d characters", len([]rune(fmt.Sprint(created["title"]))))
	}

	status, body = limitCall(t, handler, http.MethodPatch, "/api/issues/"+id, map[string]any{"title": long}, nil)
	expectLimitStatus(t, "REST update", status, body, http.StatusBadRequest, limit)
	status, body = limitCall(t, handler, http.MethodPost, "/api/issues/batch", map[string]any{"issueIds": []string{id}, "update": map[string]any{"title": long}}, nil)
	expectLimitStatus(t, "batch update", status, body, http.StatusBadRequest, limit)

	// MCP save_issue, as the built-in agent and MCP clients call it.
	ctx := context.WithValue(context.Background(), authUserContextKey{}, data.Viewer)
	ctx = store.ContextWithActor(ctx, data.Viewer)
	request := httptest.NewRequest(http.MethodPost, "/api/agent?workspace="+data.Workspace.URLKey, nil).WithContext(ctx)
	for _, args := range []map[string]any{{"title": long, "team": team}, {"id": id, "title": long}} {
		raw, _ := json.Marshal(args)
		if _, err := service.executeAgentTool(request, repo.Bootstrap(), domain.AgentToolCall{Name: "save_issue", Arguments: raw}); err == nil || !strings.Contains(err.Error(), limit) {
			t.Fatalf("save_issue %v = %v", args["id"], err)
		}
	}

	// Templates and recurring issues.
	status, body = limitCall(t, handler, http.MethodPost, "/api/teams/"+team+"/templates", map[string]any{"name": "Limits", "title": long}, nil)
	expectLimitStatus(t, "template title", status, body, http.StatusBadRequest, limit)
	status, body = limitCall(t, handler, http.MethodPost, "/api/teams/"+team+"/templates", map[string]any{"name": "Limits", "subIssues": []map[string]any{{"title": long, "priority": 0}}}, nil)
	expectLimitStatus(t, "template sub-issue", status, body, http.StatusBadRequest, limit)
	due := time.Now().UTC().AddDate(0, 0, 7).Format("2006-01-02")
	status, body = limitCall(t, handler, http.MethodPost, "/api/teams/"+team+"/recurring-issues", map[string]any{"title": long, "recurrence": "weekly", "dueDate": due}, nil)
	expectLimitStatus(t, "recurring issue", status, body, http.StatusBadRequest, limit)
	status, body = limitCall(t, handler, http.MethodPost, "/api/teams/"+team+"/recurring-issues", map[string]any{"title": "Weekly", "recurrence": "weekly", "dueDate": due, "subIssues": []map[string]any{{"title": long}}}, nil)
	expectLimitStatus(t, "recurring sub-issue", status, body, http.StatusBadRequest, limit)

	// CSV import: the long row is reported and skipped, the other row lands.
	job := previewImportRequest(t, handler, "limits.csv", "Title\n"+long+"\nShort imported row\n")
	requestJSON[domain.ImportJob](t, handler, http.MethodPost, "/api/imports/"+job.ID+"/commit", map[string]any{"teamId": team, "mapping": map[string]string{"title": "Title"}}, http.StatusAccepted)
	job = waitForImport(t, handler, job.ID)
	if job.Imported != 1 || len(job.Errors) != 1 || !strings.Contains(job.Errors[0], limit) {
		t.Fatalf("CSV import = %d imported, errors %v", job.Imported, job.Errors)
	}

	after := repo.Bootstrap()
	if len(after.Issues) != before+2 {
		t.Fatalf("issues = %d, want %d (the boundary issue and one CSV row)", len(after.Issues), before+2)
	}
	for _, issue := range after.Issues {
		if len([]rune(issue.Title)) > domain.IssueTitleMaxLength {
			t.Fatalf("an over-long title was written: %d characters", len([]rune(issue.Title)))
		}
		if issue.ID == id && issue.Title != edge {
			t.Fatal("the refused update changed the title")
		}
	}

	// MCP uploads: title and subtitle are VARCHAR(512).
	actor := mcpActor{WorkspaceKey: data.Workspace.URLKey, User: data.Viewer}
	uploadArgs := func(field, value string) map[string]any {
		return map[string]any{"issue": id, "filename": "notes.txt", "contentType": "text/plain", "size": float64(4), field: value}
	}
	for _, field := range []string{"title", "subtitle"} {
		if _, err := service.prepareMCPAttachmentUpload(ctx, actor, after, uploadArgs(field, strings.Repeat("ü", domain.UploadTitleMaxLength+1))); err == nil || !strings.Contains(err.Error(), "512") {
			t.Fatalf("upload %s = %v", field, err)
		}
		if _, err := service.prepareMCPAttachmentUpload(ctx, actor, after, uploadArgs(field, strings.Repeat("ü", domain.UploadTitleMaxLength))); err != nil {
			t.Fatalf("boundary upload %s refused: %v", field, err)
		}
	}
}

// SCIM answers an over-long userName, externalId or group displayName with
// a SCIM 400 invalidValue instead of a database error.
func TestSCIMOverlongValuesAreInvalidValue(t *testing.T) {
	for name, repo := range favoriteTabTestStores(t) {
		t.Run(name, func(t *testing.T) {
			handler := newHandler(&server{store: repo, uploadPath: t.TempDir(), authDisabled: true})
			workspace := repo.Bootstrap().Workspace.URLKey
			token := requestJSON[store.SCIMToken](t, handler, http.MethodPost, "/api/scim/tokens?workspace="+workspace, map[string]any{"name": "idp"}, http.StatusCreated)
			auth := map[string]string{"Authorization": "Bearer " + token.Secret}
			base := "/scim/v2/" + workspace
			suffix := fmt.Sprint(time.Now().UnixNano())
			exact := suffix + strings.Repeat("ß", domain.SCIMValueMaxLength-len(suffix))
			for _, user := range []map[string]any{
				{"userName": exact + "x"},
				{"userName": "limits-" + suffix, "externalId": exact + "x"},
			} {
				user["schemas"] = []string{scimSchemaUser}
				status, body := limitCall(t, handler, http.MethodPost, base+"/Users", user, auth)
				if status != http.StatusBadRequest || body["scimType"] != "invalidValue" || !strings.Contains(fmt.Sprint(body["detail"]), "320") {
					t.Fatalf("SCIM user %v = %d %v", user, status, body)
				}
			}
			status, body := limitCall(t, handler, http.MethodPost, base+"/Users", map[string]any{"schemas": []string{scimSchemaUser}, "userName": exact, "externalId": exact}, auth)
			if status != http.StatusCreated || body["userName"] != exact {
				t.Fatalf("boundary SCIM user = %d %v", status, body)
			}
			status, body = limitCall(t, handler, http.MethodPost, base+"/Groups", map[string]any{"schemas": []string{scimSchemaGroup}, "displayName": exact + "x"}, auth)
			if status != http.StatusBadRequest || body["scimType"] != "invalidValue" {
				t.Fatalf("SCIM group = %d %v", status, body)
			}
			status, _ = limitCall(t, handler, http.MethodPost, base+"/Groups", map[string]any{"schemas": []string{scimSchemaGroup}, "displayName": exact}, auth)
			if status != http.StatusCreated {
				t.Fatalf("boundary SCIM group = %d", status)
			}
		})
	}
}
