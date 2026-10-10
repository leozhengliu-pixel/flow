package main

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"

	"github.com/coder/websocket"
)

func TestDocumentRevisionCadence(t *testing.T) {
	ann, ben := domain.User{ID: "ann"}, domain.User{ID: "ben"}
	start := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	document := &domain.Document{ID: "doc", Title: "Plan", Content: "one"}
	if !recordDocumentRevision(document, ann, start, false) || len(document.Revisions) != 1 {
		t.Fatal("the first save did not start a version")
	}
	// Saves in the same burst update the newest version in place: it always
	// holds the current state.
	document.Content = "one two"
	if recordDocumentRevision(document, ann, start.Add(2*time.Minute), false) || len(document.Revisions) != 1 || document.Revisions[0].Content != "one two" || !document.Revisions[0].CreatedAt.Equal(start.Add(2*time.Minute)) {
		t.Fatalf("a save within the idle window started a version: %#v", document.Revisions)
	}
	// Five idle minutes start a new version.
	document.Content = "one two three"
	if !recordDocumentRevision(document, ann, start.Add(8*time.Minute), false) || len(document.Revisions) != 2 || document.Revisions[1].Content != "one two" {
		t.Fatalf("an idle gap did not start a version: %#v", document.Revisions)
	}
	// Another author starts a new version, unless both edit together.
	document.Content = "ben's edit"
	if !recordDocumentRevision(document, ben, start.Add(10*time.Minute), false) || len(document.Revisions) != 3 {
		t.Fatalf("an author change did not start a version: %#v", document.Revisions)
	}
	document.Content = "ann joins"
	if recordDocumentRevision(document, ann, start.Add(10*time.Minute+10*time.Second), false) || !slices.Equal(document.Revisions[0].AuthorIDs, []string{"ben", "ann"}) {
		t.Fatalf("co-editing did not share a version: %#v", document.Revisions[0])
	}
	// An all-day burst still checkpoints hourly.
	for minute := 11; minute <= 75; minute += 4 {
		document.Content = strings.Repeat("x", minute)
		recordDocumentRevision(document, ann, start.Add(time.Duration(minute)*time.Minute), false)
	}
	if len(document.Revisions) != 4 {
		t.Fatalf("a long burst did not checkpoint: %d versions", len(document.Revisions))
	}
	// Versions written before post-update snapshots are never overwritten.
	legacy := &domain.Document{Content: "now", Revisions: []domain.DocumentRevision{{ID: "old", Content: "before", Author: ann, CreatedAt: start}}}
	if !recordDocumentRevision(legacy, ann, start.Add(time.Minute), false) || legacy.Revisions[1].Content != "before" {
		t.Fatalf("a legacy version was overwritten: %#v", legacy.Revisions)
	}
}

func newDocumentLifecycleServer(t *testing.T) (*store.SQLiteStore, http.Handler) {
	t.Helper()
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	return repository, newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
}

func TestDocumentSlugsVersionsAndReads(t *testing.T) {
	_, handler := newDocumentLifecycleServer(t)
	document := requestJSON[domain.Document](t, handler, http.MethodPost, "/api/documents", map[string]any{}, http.StatusCreated)
	if document.Title != "" || !regexp.MustCompile(`^untitled-[0-9a-f]{12}$`).MatchString(document.SlugID) {
		t.Fatalf("untitled document = %q slug %q", document.Title, document.SlugID)
	}
	suffix := strings.TrimPrefix(document.SlugID, "untitled-")
	renamed := requestJSON[domain.Document](t, handler, http.MethodPatch, "/api/documents/"+document.ID, map[string]any{"title": "Q3 Roadmap"}, http.StatusOK)
	if renamed.SlugID != "q3-roadmap-"+suffix || !slices.Contains(renamed.PreviousSlugIDs, document.SlugID) || renamed.Version != document.Version+1 {
		t.Fatalf("renamed slug = %q previous %v version %d", renamed.SlugID, renamed.PreviousSlugIDs, renamed.Version)
	}
	// Old links keep resolving, by GET and by every document route.
	if got := requestJSON[domain.Document](t, handler, http.MethodGet, "/api/documents/"+document.SlugID, nil, http.StatusOK); got.ID != document.ID || got.SlugID != renamed.SlugID {
		t.Fatalf("old slug resolved to %#v", got)
	}
	requestJSON[domain.Comment](t, handler, http.MethodPost, "/api/documents/"+document.SlugID+"/comments", map[string]any{"body": "Via the old link"}, http.StatusCreated)
	if comments := requestJSON[[]domain.Comment](t, handler, http.MethodGet, "/api/documents/"+document.ID+"/comments", nil, http.StatusOK); len(comments) != 1 {
		t.Fatalf("comment through an old slug was not stored on the document: %#v", comments)
	}
	requestJSON[any](t, handler, http.MethodGet, "/api/documents/missing-000000000000", nil, http.StatusNotFound)

	// Optimistic concurrency.
	stale := document.Version
	conflict := requestJSON[map[string]any](t, handler, http.MethodPatch, "/api/documents/"+document.ID, map[string]any{"title": "Lost update", "expectedVersion": stale}, http.StatusConflict)
	if conflict["code"] != "VERSION_CONFLICT" {
		t.Fatalf("conflict body = %#v", conflict)
	}
	current := requestJSON[domain.Document](t, handler, http.MethodPatch, "/api/documents/"+document.ID, map[string]any{"title": "Q3 Roadmap v2", "expectedVersion": renamed.Version}, http.StatusOK)
	// Clearing the title is allowed and the slug becomes untitled again.
	cleared := requestJSON[domain.Document](t, handler, http.MethodPatch, "/api/documents/"+document.ID, map[string]any{"title": ""}, http.StatusOK)
	if cleared.Title != "" || cleared.SlugID != document.SlugID || slices.Contains(cleared.PreviousSlugIDs, cleared.SlugID) || !slices.Contains(cleared.PreviousSlugIDs, current.SlugID) {
		t.Fatalf("cleared title = %q slug %q previous %v", cleared.Title, cleared.SlugID, cleared.PreviousSlugIDs)
	}
}

func TestDocumentDeleteRestoreAndPurge(t *testing.T) {
	repository, handler := newDocumentLifecycleServer(t)
	ctx := context.Background()
	document := requestJSON[domain.Document](t, handler, http.MethodPost, "/api/documents", map[string]any{"title": "Retired plan"}, http.StatusCreated)
	requestJSON[domain.Comment](t, handler, http.MethodPost, "/api/documents/"+document.ID+"/comments", map[string]any{"body": "Keep me"}, http.StatusCreated)
	if _, err := repository.AppendDocumentCollaborationUpdate(ctx, "test-workspace", store.DocumentCollaborationUpdate{ID: "collab_keep", DocumentID: document.ID, Data: []byte{1}, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}

	requestJSON[any](t, handler, http.MethodDelete, "/api/documents/"+document.ID, nil, http.StatusNoContent)
	deleted := requestJSON[domain.Document](t, handler, http.MethodGet, "/api/documents/"+document.SlugID, nil, http.StatusOK)
	if deleted.DeletedAt == nil || deleted.ID != document.ID {
		t.Fatalf("deleted document read = %#v", deleted)
	}
	requestJSON[any](t, handler, http.MethodPost, "/api/documents/missing/restore", nil, http.StatusNotFound)
	restored := requestJSON[domain.Document](t, handler, http.MethodPost, "/api/documents/"+document.ID+"/restore", nil, http.StatusOK)
	if restored.DeletedAt != nil || restored.ID != document.ID {
		t.Fatalf("restored = %#v", restored)
	}
	requestJSON[any](t, handler, http.MethodPost, "/api/documents/"+document.ID+"/restore", nil, http.StatusConflict)
	if comments := requestJSON[[]domain.Comment](t, handler, http.MethodGet, "/api/documents/"+document.ID+"/comments", nil, http.StatusOK); len(comments) != 1 {
		t.Fatalf("restoring lost the comment thread: %#v", comments)
	}
	if updates, _ := repository.DocumentCollaborationUpdates(ctx, "test-workspace", document.ID); len(updates) != 1 {
		t.Fatalf("restoring lost the collaboration log: %#v", updates)
	}

	// Purging from "Recently deleted" drops the thread and the update log.
	requestJSON[any](t, handler, http.MethodDelete, "/api/documents/"+document.ID, nil, http.StatusNoContent)
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	index := slices.IndexFunc(bootstrap.Trash, func(item domain.TrashEntry) bool { return item.ResourceID == document.ID })
	if index < 0 {
		t.Fatal("deleted document missing from the trash")
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/trash/"+bootstrap.Trash[index].ID, nil, http.StatusNoContent)
	requestJSON[any](t, handler, http.MethodGet, "/api/documents/"+document.ID, nil, http.StatusNotFound)
	if updates, _ := repository.DocumentCollaborationUpdates(ctx, "test-workspace", document.ID); len(updates) != 0 {
		t.Fatalf("purging left the collaboration log: %#v", updates)
	}
	if comments, _, err := repository.IssueContent(ctx, "test-workspace", document.ID); err != nil || len(comments) != 0 {
		t.Fatalf("purging left comments: %#v err=%v", comments, err)
	}
}

func TestDocumentCollaborationCompactionAndRestoreGeneration(t *testing.T) {
	repository, handler := newDocumentLifecycleServer(t)
	ctx := context.Background()
	document := requestJSON[domain.Document](t, handler, http.MethodPost, "/api/documents", map[string]any{"title": "Shared notes", "content": "v1"}, http.StatusCreated)
	for _, id := range []string{"collab_a", "collab_b", "collab_c"} {
		if _, err := repository.AppendDocumentCollaborationUpdate(ctx, "test-workspace", store.DocumentCollaborationUpdate{ID: id, DocumentID: document.ID, Data: []byte(id), CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	saved := requestJSON[domain.Document](t, handler, http.MethodPatch, "/api/documents/"+document.ID, map[string]any{"content": "v2", "contentState": "AQID", "expectedContentVersion": 0, "documentUpdateIds": []string{"collab_a", "collab_b"}}, http.StatusOK)
	if saved.ContentState != "AQID" || saved.ContentVersion != 1 {
		t.Fatalf("compacting save = state %q version %d", saved.ContentState, saved.ContentVersion)
	}
	updates, _ := repository.DocumentCollaborationUpdates(ctx, "test-workspace", document.ID)
	if len(updates) != 1 || updates[0].ID != "collab_c" {
		t.Fatalf("compaction left %#v", updates)
	}
	// A save against a stale base keeps its content but not its state, and
	// prunes nothing: the stale snapshot may lack pruned updates.
	stale := requestJSON[domain.Document](t, handler, http.MethodPatch, "/api/documents/"+document.ID, map[string]any{"content": "v3", "contentState": "BAUG", "expectedContentVersion": 0, "documentUpdateIds": []string{"collab_c"}}, http.StatusOK)
	if stale.Content != "v3" || stale.ContentState != "AQID" || stale.ContentVersion != 1 {
		t.Fatalf("stale save = %#v", stale)
	}
	if updates, _ := repository.DocumentCollaborationUpdates(ctx, "test-workspace", document.ID); len(updates) != 1 {
		t.Fatalf("a stale save pruned updates: %#v", updates)
	}
	// Legacy writers cannot replace the state once compaction happened.
	legacy := requestJSON[domain.Document](t, handler, http.MethodPatch, "/api/documents/"+document.ID, map[string]any{"contentState": "CQoL"}, http.StatusOK)
	if legacy.ContentState != "AQID" {
		t.Fatalf("a legacy write replaced the compacted state: %q", legacy.ContentState)
	}

	// Restoring a version starts a new collaboration generation and drops
	// the replaced update log.
	revision := legacy.Revisions[len(legacy.Revisions)-1]
	restored := requestJSON[domain.Document](t, handler, http.MethodPost, "/api/documents/"+document.ID+"/restore/"+revision.ID, nil, http.StatusOK)
	if restored.CollaborationID == "" || restored.CollaborationID == document.ID || restored.ContentState != "" || restored.Content != revision.Content || restored.Revisions[0].Content != revision.Content {
		t.Fatalf("restored = collab %q state %q content %q newest %q", restored.CollaborationID, restored.ContentState, restored.Content, restored.Revisions[0].Content)
	}
	if updates, _ := repository.DocumentCollaborationUpdates(ctx, "test-workspace", document.ID); len(updates) != 0 {
		t.Fatalf("restoring left the replaced update log: %#v", updates)
	}
}

func dialAuthenticatedCollaborationSocket(t *testing.T, client *http.Client, serverURL, clientID string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	url := strings.Replace(serverURL, "http://", "ws://", 1) + "/api/realtime/socket?workspace=test-workspace&clientId=" + clientID
	connection, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	return connection
}

func readSocketMessage(t *testing.T, connection *websocket.Conn) (websocket.MessageType, []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	messageType, message, err := connection.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return messageType, message
}

func joinStandaloneDocument(t *testing.T, connection *websocket.Conn, documentID string) collaborationSyncMessage {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"type": "document.join", "documentId": documentID})
	writeSocket(t, connection, websocket.MessageText, raw)
	_, message := readSocketMessage(t, connection)
	var sync collaborationSyncMessage
	if json.Unmarshal(message, &sync) != nil || sync.Type != "document.sync" {
		t.Fatalf("join response = %s", message)
	}
	return sync
}

func TestCollaborationSocketEnforcesDocumentRoles(t *testing.T) {
	server, owner, _, alice, bob, document := documentNotificationFixture(t)
	editor := dialAuthenticatedCollaborationSocket(t, alice.client, server.URL, "alice")
	defer editor.CloseNow()
	commenter := dialAuthenticatedCollaborationSocket(t, bob.client, server.URL, "bob")
	defer commenter.CloseNow()
	if sync := joinStandaloneDocument(t, editor, document.ID); sync.ReadOnly {
		t.Fatal("an editor joined read-only")
	}
	if sync := joinStandaloneDocument(t, commenter, document.ID); !sync.ReadOnly {
		t.Fatal("a commenter joined with write access")
	}

	// The commenter's edit is rejected and never reaches the editor.
	writeSocket(t, commenter, websocket.MessageBinary, encodeCollaborationFrame(collaborationUpdateFrame, document.ID, "collab_bob", []byte{7}))
	_, message := readSocketMessage(t, commenter)
	if !strings.Contains(string(message), `"document.readonly"`) {
		t.Fatalf("commenter update response = %s", message)
	}
	// Awareness (cursors, presence) still flows for read-only members.
	writeSocket(t, commenter, websocket.MessageBinary, encodeCollaborationFrame(collaborationAwarenessFrame, document.ID, "", []byte{1}))
	if messageType, raw := readSocketMessage(t, editor); messageType != websocket.MessageBinary || raw[0] != collaborationAwarenessFrame {
		t.Fatalf("editor received %v %v instead of the commenter's awareness", messageType, raw)
	}
	writeSocket(t, editor, websocket.MessageBinary, encodeCollaborationFrame(collaborationUpdateFrame, document.ID, "collab_alice", []byte{8}))
	assertUpdateFrame(t, editor, document.ID, "collab_alice", []byte{8})
	assertUpdateFrame(t, commenter, document.ID, "collab_alice", []byte{8})

	// Downgrading the editor takes effect on the open socket.
	authRequest[[]domain.DocumentPermission](t, owner, http.MethodPut, server.URL+"/api/documents/"+document.ID+"/permissions", map[string]any{"permissions": []map[string]string{
		{"subjectType": "user", "subjectId": alice.user.ID, "role": "viewer"},
		{"subjectType": "user", "subjectId": bob.user.ID, "role": "commenter"},
	}}, "test-workspace", http.StatusOK)
	writeSocket(t, editor, websocket.MessageBinary, encodeCollaborationFrame(collaborationUpdateFrame, document.ID, "collab_alice_2", []byte{9}))
	if _, message := readSocketMessage(t, editor); !strings.Contains(string(message), `"document.readonly"`) {
		t.Fatalf("downgraded editor update response = %s", message)
	}
}

func TestUntitledDocumentsPinAndLinkAsResources(t *testing.T) {
	_, handler := newDocumentLifecycleServer(t)
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	team := bootstrap.Teams[0]
	document := requestJSON[domain.Document](t, handler, http.MethodPost, "/api/documents", map[string]any{"teamIds": []string{team.ID}}, http.StatusCreated)
	pinned := requestJSON[domain.TeamPinnedResource](t, handler, http.MethodPost, "/api/teams/"+team.ID+"/resources", map[string]any{"resourceType": "document", "resourceId": document.ID}, http.StatusCreated)
	if pinned.Title != untitledDocumentTitle {
		t.Fatalf("pinned untitled document title = %q", pinned.Title)
	}
	requestJSON[domain.Document](t, handler, http.MethodPatch, "/api/documents/"+document.ID, map[string]any{"title": "Named later"}, http.StatusOK)
	again := requestJSON[domain.TeamPinnedResource](t, handler, http.MethodPost, "/api/teams/"+team.ID+"/resources", map[string]any{"resourceType": "document", "resourceId": document.ID, "title": "  "}, http.StatusCreated)
	if again.Title != "Named later" {
		t.Fatalf("pinned document title = %q, want the live title", again.Title)
	}
}
