package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"

	"github.com/coder/websocket"
)

func TestRealtimeOverflowCollapsesToResync(t *testing.T) {
	hub := newRealtimeHub()
	channel, unsubscribe := hub.subscribe("workspace")
	defer unsubscribe()
	for index := 0; index < 65; index++ {
		hub.publish("workspace", domain.RealtimeEvent{ID: fmt.Sprintf("event_%d", index), Type: "issue.updated", CreatedAt: time.Now().UTC()})
	}
	select {
	case event := <-channel:
		if event.Type != "workspace.resync_required" {
			t.Fatalf("overflow event = %q", event.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("overflow did not emit resync event")
	}
}

func TestRealtimeHubReplaysEventsAfterCursor(t *testing.T) {
	hub := newRealtimeHub()
	hub.publish("workspace", domain.RealtimeEvent{ID: "event_1", Type: "issue.updated"})
	hub.publish("workspace", domain.RealtimeEvent{ID: "event_2", Type: "project.updated"})
	channel, unsubscribe := hub.subscribeSince("workspace", "event_1")
	defer unsubscribe()
	select {
	case event := <-channel:
		if event.ID != "event_2" || event.Type != "project.updated" {
			t.Fatalf("replayed event = %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("replay did not deliver the event after the cursor")
	}
}

func TestRealtimeHubRequestsResyncWhenCursorExpired(t *testing.T) {
	hub := newRealtimeHub()
	channel, unsubscribe := hub.subscribeSince("workspace", "missing")
	defer unsubscribe()
	select {
	case event := <-channel:
		if event.Type != "workspace.resync_required" {
			t.Fatalf("expired cursor event = %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("expired cursor did not request a resync")
	}
}

func TestCollaborationSocketBroadcastsAndPersistsDocumentUpdates(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	server := httptest.NewServer(handler)
	defer server.Close()

	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	issue := bootstrap.Issues[0]
	documentID := "document_content_" + issue.ID
	first := dialCollaborationSocket(t, server.URL, "first")
	defer first.CloseNow()
	second := dialCollaborationSocket(t, server.URL, "second")
	defer second.CloseNow()
	joinDocument(t, first, issue.ID, documentID)
	joinDocument(t, second, issue.ID, documentID)

	payload := []byte{1, 2, 3, 4, 5}
	frame := encodeCollaborationFrame(collaborationUpdateFrame, documentID, "collab_test_update", payload)
	writeSocket(t, first, websocket.MessageBinary, frame)
	assertUpdateFrame(t, first, documentID, "collab_test_update", payload)
	assertUpdateFrame(t, second, documentID, "collab_test_update", payload)

	updates, err := repository.DocumentCollaborationUpdates(context.Background(), bootstrap.Workspace.URLKey, documentID)
	if err != nil || len(updates) != 1 || string(updates[0].Data) != string(payload) {
		t.Fatalf("persisted updates=%#v err=%v", updates, err)
	}

	requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+issue.ID, map[string]any{
		"description": "Collaborative copy", "descriptionState": `{"type":"doc","content":[{"type":"paragraph"}]}`,
		"descriptionData": map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph"}}},
		"contentState":    "AQID", "documentUpdateIds": []string{"collab_test_update"},
	}, http.StatusOK)
	updates, err = repository.DocumentCollaborationUpdates(context.Background(), bootstrap.Workspace.URLKey, documentID)
	if err != nil || len(updates) != 0 {
		t.Fatalf("compacted updates=%#v err=%v", updates, err)
	}

	writeSocket(t, first, websocket.MessageBinary, encodeCollaborationFrame(collaborationUpdateFrame, documentID, "collab_delete_cleanup", []byte{9}))
	assertUpdateFrame(t, first, documentID, "collab_delete_cleanup", []byte{9})
	assertUpdateFrame(t, second, documentID, "collab_delete_cleanup", []byte{9})
	requestJSON[any](t, handler, http.MethodDelete, "/api/issues/"+issue.ID, nil, http.StatusNoContent)
	updates, err = repository.DocumentCollaborationUpdates(context.Background(), bootstrap.Workspace.URLKey, documentID)
	if err != nil || len(updates) != 0 {
		t.Fatalf("deleted issue left collaboration updates=%#v err=%v", updates, err)
	}
}

func TestCollaborationFrameRoundTrip(t *testing.T) {
	want := []byte{9, 8, 7}
	encoded := encodeCollaborationFrame(collaborationUpdateFrame, "document_1", "collab_1", want)
	kind, documentID, updateID, payload, err := decodeCollaborationFrame(encoded)
	if err != nil || kind != collaborationUpdateFrame || documentID != "document_1" || updateID != "collab_1" || string(payload) != string(want) {
		t.Fatalf("decoded kind=%d document=%q update=%q payload=%v err=%v", kind, documentID, updateID, payload, err)
	}
}

func TestCollaborationSocketStandaloneDocumentJoin(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	server := httptest.NewServer(handler)
	defer server.Close()
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if len(bootstrap.Documents) == 0 {
		t.Skip("seed workspace has no standalone documents")
	}
	connection := dialCollaborationSocket(t, server.URL, "standalone")
	defer connection.CloseNow()
	raw, _ := json.Marshal(map[string]string{"type": "document.join", "documentId": bootstrap.Documents[0].ID})
	writeSocket(t, connection, websocket.MessageText, raw)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	messageType, message, err := connection.Read(ctx)
	if err != nil || messageType != websocket.MessageText || !strings.Contains(string(message), `"type":"document.sync"`) {
		t.Fatalf("standalone join response type=%v message=%s err=%v", messageType, message, err)
	}
}

func dialCollaborationSocket(t *testing.T, serverURL, clientID string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	url := strings.Replace(serverURL, "http://", "ws://", 1) + "/api/realtime/socket?workspace=test-workspace&clientId=" + clientID
	connection, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	return connection
}

func joinDocument(t *testing.T, connection *websocket.Conn, issueID, documentID string) {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"type": "document.join", "issueId": issueID, "documentId": documentID})
	writeSocket(t, connection, websocket.MessageText, raw)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	messageType, message, err := connection.Read(ctx)
	if err != nil || messageType != websocket.MessageText || !strings.Contains(string(message), `"type":"document.sync"`) {
		t.Fatalf("join response type=%v message=%s err=%v", messageType, message, err)
	}
}

func writeSocket(t *testing.T, connection *websocket.Conn, messageType websocket.MessageType, message []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := connection.Write(ctx, messageType, message); err != nil {
		t.Fatal(err)
	}
}

func assertUpdateFrame(t *testing.T, connection *websocket.Conn, documentID, updateID string, payload []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	messageType, raw, err := connection.Read(ctx)
	if err != nil || messageType != websocket.MessageBinary {
		t.Fatalf("update response type=%v err=%v", messageType, err)
	}
	kind, gotDocumentID, gotUpdateID, gotPayload, err := decodeCollaborationFrame(raw)
	if err != nil || kind != collaborationUpdateFrame || gotDocumentID != documentID || gotUpdateID != updateID || string(gotPayload) != string(payload) {
		t.Fatalf("update frame kind=%d document=%q update=%q payload=%v err=%v", kind, gotDocumentID, gotUpdateID, gotPayload, err)
	}
}
