package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
	"github.com/coder/websocket"
)

func TestDescriptionReplacementInvalidatesRichTextAcrossIssueAPIs(t *testing.T) {
	for _, endpoint := range []string{"/api/issues", "/api/issue-records"} {
		t.Run(endpoint, func(t *testing.T) {
			repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer repository.Close()
			handler := newHandler(&server{store: repository, authDisabled: true, uploadPath: t.TempDir()})
			created := requestJSON[domain.Issue](t, handler, http.MethodPost, endpoint, map[string]any{"title": "Description integrity"}, http.StatusCreated)
			path := endpoint + "/" + created.ID
			first := requestJSON[domain.Issue](t, handler, http.MethodPatch, path, map[string]any{"description": "## First API description"}, http.StatusOK)
			if first.DocumentContent == nil || first.DocumentContent.Version != 1 || first.DocumentContent.Content != first.Description {
				t.Fatalf("initial API description did not establish a versioned document: %+v", first.DocumentContent)
			}
			rich := requestJSON[domain.Issue](t, handler, http.MethodPatch, path, map[string]any{
				"description": "Rich text", "descriptionState": `{"type":"doc","content":[{"type":"paragraph"}]}`,
				"descriptionData": map[string]any{"type": "doc"}, "contentState": "AQID", "expectedDocumentVersion": first.DocumentContent.Version,
			}, http.StatusOK)
			replacement := requestJSON[domain.Issue](t, handler, http.MethodPatch, path, map[string]any{"description": "## Latest API description"}, http.StatusOK)
			if replacement.DescriptionState != "" || replacement.DocumentContent.ContentState != "" || replacement.DocumentContent.ContentData != nil || replacement.DocumentContent.Content != replacement.Description {
				t.Fatalf("replacement retained stale rich-text projections: %+v", replacement.DocumentContent)
			}
			if replacement.DocumentContent.Version != rich.DocumentContent.Version+1 || replacement.DocumentContent.ID == rich.DocumentContent.ID {
				t.Fatalf("replacement did not invalidate old generation: %+v", replacement.DocumentContent)
			}
			requestJSON[any](t, handler, http.MethodPatch, path, map[string]any{
				"description": "Stale tab", "descriptionData": map[string]any{"type": "doc"}, "contentState": "AQID", "expectedDocumentVersion": rich.DocumentContent.Version,
			}, http.StatusConflict)
			requestJSON[any](t, handler, http.MethodPatch, path, map[string]any{"description": "Stale plain text", "expectedDocumentVersion": rich.DocumentContent.Version}, http.StatusConflict)
			stored, err := repository.IssueRecord(context.Background(), "test-workspace", created.ID)
			if err != nil || stored.Description != replacement.Description {
				t.Fatalf("conflict replaced newer text: %+v %v", stored, err)
			}
		})
	}
}

func TestMCPDescriptionReplacementInvalidatesOldEditorState(t *testing.T) {
	repository, actor, ctx := newMCPToolTestContext(t)
	service := &server{store: repository, uploadPath: t.TempDir()}
	data := repository.Bootstrap()
	result, err := service.callFlowTool(ctx, actor, "save_issue", map[string]any{"title": "MCP description", "description": "## Initial MCP body", "team": data.Teams[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	var issue domain.Issue
	if err := jsonClone(result, &issue); err != nil {
		t.Fatal(err)
	}
	handler := newHandler(&server{store: repository, authDisabled: true, uploadPath: t.TempDir()})
	rich := requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issue-records/"+issue.ID, map[string]any{
		"description": "Editor copy", "descriptionState": `{"type":"doc"}`, "descriptionData": map[string]any{"type": "doc"}, "contentState": "AQID",
	}, http.StatusOK)
	_, err = service.callFlowTool(ctx, actor, "save_issue", map[string]any{"id": issue.ID, "description": "## Replaced through MCP"})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := repository.IssueRecord(ctx, actor.WorkspaceKey, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Description != "## Replaced through MCP" || stored.DescriptionState != "" || stored.DocumentContent == nil || stored.DocumentContent.ContentState != "" || stored.DocumentContent.ContentData != nil || stored.DocumentContent.Version != rich.DocumentContent.Version+1 || stored.DocumentContent.ID == rich.DocumentContent.ID {
		t.Fatalf("MCP replacement retained stale editor content: %+v", stored)
	}
}

func TestCollaborationRejectsReplacedDocumentGeneration(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, authDisabled: true, uploadPath: t.TempDir()})
	server := httptest.NewServer(handler)
	defer server.Close()
	issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issue-records", map[string]any{"title": "Offline tab"}, http.StatusCreated)
	oldID := "document_content_" + issue.ID
	socket := dialCollaborationSocket(t, server.URL, "old-tab")
	defer socket.CloseNow()
	joinDocument(t, socket, issue.ID, oldID)
	updated := requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issue-records/"+issue.ID, map[string]any{"description": "New authoritative text"}, http.StatusOK)
	writeSocket(t, socket, websocket.MessageBinary, encodeCollaborationFrame(collaborationUpdateFrame, oldID, "collab_stale", []byte{1, 2, 3}))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, raw, err := socket.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var message struct {
		Type       string `json:"type"`
		DocumentID string `json:"documentId"`
	}
	if json.Unmarshal(raw, &message) != nil || message.Type != "document.conflict" || message.DocumentID != oldID {
		t.Fatalf("stale socket was not rejected: %s", raw)
	}
	updates, err := repository.DocumentCollaborationUpdates(ctx, "test-workspace", oldID)
	if err != nil || len(updates) != 0 {
		t.Fatalf("stale socket persisted updates: %+v %v", updates, err)
	}
	fresh := dialCollaborationSocket(t, server.URL, "new-tab")
	defer fresh.CloseNow()
	joinDocument(t, fresh, issue.ID, updated.DocumentContent.ID)
}
