package main

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
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

func TestFlowMigrationBundleExecuteAndRollback(t *testing.T) {
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	before := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	bundleRecorder := httptest.NewRecorder()
	handler.ServeHTTP(bundleRecorder, httptest.NewRequest(http.MethodGet, "/api/migrations/bundle", nil))
	if bundleRecorder.Code != http.StatusOK {
		t.Fatalf("bundle export failed: %d %s", bundleRecorder.Code, bundleRecorder.Body.String())
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "flow-bundle.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(bundleRecorder.Body.Bytes()); err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	previewRequest := httptest.NewRequest(http.MethodPost, "/api/migrations/preview", &body)
	previewRequest.Header.Set("Content-Type", writer.FormDataContentType())
	previewRecorder := httptest.NewRecorder()
	handler.ServeHTTP(previewRecorder, previewRequest)
	if previewRecorder.Code != http.StatusCreated {
		t.Fatalf("migration preview failed: %d %s", previewRecorder.Code, previewRecorder.Body.String())
	}
	var job domain.MigrationJob
	if json.Unmarshal(previewRecorder.Body.Bytes(), &job) != nil || job.ID == "" || job.Bundle != nil {
		t.Fatalf("invalid public migration job: %#v", job)
	}
	job = requestJSON[domain.MigrationJob](t, handler, http.MethodPost, "/api/migrations/"+job.ID+"/execute", map[string]any{"target": "flow", "targetTeamId": before.Teams[0].ID}, http.StatusOK)
	if job.Status != "completed" || job.Progress != 100 {
		t.Fatalf("migration did not complete: %#v", job)
	}
	after := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if len(after.Issues) <= len(before.Issues) {
		t.Fatalf("migration did not restore issues: before=%d after=%d", len(before.Issues), len(after.Issues))
	}
	rolledBack := requestJSON[domain.MigrationJob](t, handler, http.MethodPost, "/api/migrations/"+job.ID+"/rollback", nil, http.StatusOK)
	if rolledBack.Status != "rolled_back" {
		t.Fatalf("migration rollback failed: %#v", rolledBack)
	}
	final := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if len(final.Issues) != len(before.Issues) {
		t.Fatalf("rollback left migrated issues: before=%d final=%d", len(before.Issues), len(final.Issues))
	}
}

func TestLinearMigrationScanBuildsInteractiveMappings(t *testing.T) {
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	bundleRecorder := httptest.NewRecorder()
	handler.ServeHTTP(bundleRecorder, httptest.NewRequest(http.MethodGet, "/api/migrations/bundle", nil))
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "flow-bundle.json")
	_, _ = part.Write(bundleRecorder.Body.Bytes())
	_ = writer.Close()
	request := httptest.NewRequest(http.MethodPost, "/api/migrations/preview", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	var job domain.MigrationJob
	_ = json.Unmarshal(recorder.Body.Bytes(), &job)
	fakeLinear := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"viewer": map[string]any{"id": "linear_viewer"}, "team": map[string]any{"id": "linear_team", "name": "Linear team", "key": "LIN", "members": map[string]any{"nodes": []map[string]any{{"id": "linear_user", "name": "zheng liu", "email": "leo.zheng.liu@example.com"}}}, "states": map[string]any{"nodes": []any{}}, "labels": map[string]any{"nodes": []any{}}, "projects": map[string]any{"nodes": []map[string]any{{"id": "linear_project", "name": "Test project"}}}}}})
	}))
	defer fakeLinear.Close()
	t.Setenv("FLOW_LINEAR_API_URL", fakeLinear.URL)
	job = requestJSON[domain.MigrationJob](t, handler, http.MethodPost, "/api/migrations/"+job.ID+"/linear/scan", map[string]any{"apiToken": "test-token", "targetTeamId": "linear_team"}, http.StatusOK)
	if job.Target != "linear" || job.TargetTeamID != "linear_team" {
		t.Fatalf("Linear scan did not set target: %#v", job)
	}
	if !slices.ContainsFunc(job.Mappings, func(item domain.MigrationEntityMapping) bool {
		return item.EntityType == "user" && item.TargetID == "linear_user" && item.Action == "map"
	}) {
		t.Fatalf("Linear user was not mapped: %#v", job.Mappings)
	}
}

func TestLinearMigrationExecutesIssueAndMetadataPhases(t *testing.T) {
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	bundle := domain.MigrationBundle{Version: 1, Source: "flow", ExportedAt: time.Now().UTC(), Workspace: domain.Workspace{ID: "source_workspace", Name: "Source", URLKey: "source"}, Users: []domain.User{bootstrap.Viewer}, Teams: []domain.Team{{ID: "source_team", Name: "Source team", Key: "SRC"}}, Issues: []domain.Issue{{ID: "source_issue", Identifier: "SRC-1", Title: "Migrated issue", Description: "Body", Priority: 2, PriorityLabel: "High", CreatedAt: time.Now().UTC().Add(-time.Hour), UpdatedAt: time.Now().UTC().Add(-30 * time.Minute), Team: domain.Team{ID: "source_team", Name: "Source team", Key: "SRC"}, State: domain.WorkflowState{ID: "source_state", Name: "Todo", Type: "unstarted"}, Creator: bootstrap.Viewer, SubscriberIDs: []string{bootstrap.Viewer.ID}, Labels: []domain.IssueLabel{}, Relations: []domain.IssueRelation{}, Attachments: []domain.Attachment{}}}, Comments: map[string][]domain.Comment{"source_issue": []domain.Comment{{ID: "source_comment", Body: "Original comment", CreatedAt: time.Now().UTC().Add(-20 * time.Minute), User: bootstrap.Viewer}}}}
	raw, _ := json.Marshal(bundle)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "bundle.json")
	_, _ = part.Write(raw)
	_ = writer.Close()
	request := httptest.NewRequest(http.MethodPost, "/api/migrations/preview", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	var job domain.MigrationJob
	_ = json.Unmarshal(recorder.Body.Bytes(), &job)
	fakeLinear := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		query, _ := payload["query"].(string)
		data := map[string]any{}
		switch {
		case strings.Contains(query, "MigrationTarget"):
			data = map[string]any{"viewer": map[string]any{"id": "linear_viewer"}, "team": map[string]any{"id": "linear_team", "name": "Linear team", "key": "LIN", "members": map[string]any{"nodes": []map[string]any{{"id": "linear_user", "name": bootstrap.Viewer.DisplayName, "email": bootstrap.Viewer.Email}}}, "states": map[string]any{"nodes": []map[string]any{{"id": "linear_state", "name": "Todo", "type": "unstarted"}}}, "labels": map[string]any{"nodes": []any{}}, "projects": map[string]any{"nodes": []any{}}}}
		case strings.Contains(query, "CreateIssue"):
			data = map[string]any{"issueCreate": map[string]any{"success": true, "issue": map[string]any{"id": "linear_issue", "identifier": "LIN-1"}}}
		case strings.Contains(query, "commentCreate"):
			data = map[string]any{"commentCreate": map[string]any{"success": true, "comment": map[string]any{"id": "linear_comment"}}}
		default:
			t.Fatalf("unexpected Linear query: %s", query)
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": data})
	}))
	defer fakeLinear.Close()
	t.Setenv("FLOW_LINEAR_API_URL", fakeLinear.URL)
	job = requestJSON[domain.MigrationJob](t, handler, http.MethodPost, "/api/migrations/"+job.ID+"/execute", map[string]any{"target": "linear", "targetTeamId": "linear_team", "apiToken": "temporary"}, http.StatusOK)
	if job.Status != "completed" || !slices.ContainsFunc(job.Mappings, func(item domain.MigrationEntityMapping) bool {
		return item.EntityType == "issue" && item.TargetID == "linear_issue"
	}) {
		t.Fatalf("Linear migration failed: %#v", job)
	}
	persisted := requestJSON[domain.MigrationJob](t, handler, http.MethodGet, "/api/migrations/"+job.ID, nil, http.StatusOK)
	if persisted.Bundle != nil {
		t.Fatal("migration API leaked raw bundle")
	}
}
