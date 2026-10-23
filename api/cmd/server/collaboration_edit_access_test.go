package main

import (
	"context"
	"encoding/json"
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

// Edit access on the collaboration socket follows the rule that authorizes
// saving an issue description through PATCH /api/issues/{id}: viewers and
// commenters (issue permissions, public team non-members, guests, read-only
// API keys) follow the description live but cannot change it.

type editAccessCase struct {
	name     string
	setup    issueAccessSetup
	readOnly bool
}

var editAccessCases = []editAccessCase{
	{"public team non-member (viewer)", issueAccessSetup{role: "member"}, true},
	{"shared as viewer", issueAccessSetup{role: "member", privateTeam: true, sharedOnly: true}, true},
	{"shared as commenter", issueAccessSetup{role: "member", privateTeam: true, sharedOnly: true, shareRole: "commenter"}, true},
	{"guest shared as viewer", issueAccessSetup{role: "guest", privateTeam: true, sharedOnly: true}, true},
	{"guest shared as commenter", issueAccessSetup{role: "guest", privateTeam: true, sharedOnly: true, shareRole: "commenter"}, true},
	{"shared as editor", issueAccessSetup{role: "member", privateTeam: true, sharedOnly: true, shareRole: "editor"}, false},
	{"guest shared as editor", issueAccessSetup{role: "guest", privateTeam: true, sharedOnly: true, shareRole: "editor"}, false},
	{"team member", issueAccessSetup{role: "member", privateTeam: true, inTeam: true}, false},
}

func newSingleAuthServerWithStore(t *testing.T) (*httptest.Server, *store.SQLiteStore) {
	t.Helper()
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "edit-access.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	server := httptest.NewServer(newHandler(&server{store: repository, uploadPath: t.TempDir()}))
	t.Cleanup(server.Close)
	return server, repository
}

// adminDescriptionSocket joins the admin (an editor) to the fixture's issue
// description on baseURL.
func adminDescriptionSocket(t *testing.T, baseURL string, fixture issueAccessFixture) *websocket.Conn {
	t.Helper()
	socket := dialAuthenticatedCollaborationSocket(t, fixture.admin, baseURL, "admin-"+strings.ToLower(strings.ReplaceAll(t.Name(), "/", "-")))
	t.Cleanup(func() { socket.CloseNow() })
	if sync := joinIssueDescription(t, socket, fixture.issue.ID, fixture.documentID); sync.ReadOnly {
		t.Fatal("the admin joined read-only")
	}
	return socket
}

func joinIssueDescription(t *testing.T, connection *websocket.Conn, issueID, documentID string) collaborationSyncMessage {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"type": "document.join", "issueId": issueID, "documentId": documentID})
	writeSocket(t, connection, websocket.MessageText, raw)
	_, message := readSocketMessage(t, connection)
	var sync collaborationSyncMessage
	if json.Unmarshal(message, &sync) != nil || sync.Type != "document.sync" {
		t.Fatalf("join response = %s", message)
	}
	return sync
}

// expectSocketNotice reads until a text message of the given type for the
// document; no update frame may arrive first.
func expectSocketNotice(t *testing.T, connection *websocket.Conn, kind, documentID string) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		messageType, message, err := connection.Read(ctx)
		if err != nil {
			t.Fatalf("no %s: %v", kind, err)
		}
		if messageType == websocket.MessageBinary {
			if message[0] == collaborationAwarenessFrame {
				continue
			}
			t.Fatalf("received an update frame before %s", kind)
		}
		var notice map[string]any
		if json.Unmarshal(message, &notice) == nil && notice["type"] == kind && notice["documentId"] == documentID {
			return notice
		}
		t.Fatalf("unexpected message before %s: %s", kind, message)
	}
}

func expectUpdateNotStored(t *testing.T, repository *store.SQLiteStore, documentID, updateID string) {
	t.Helper()
	updates, err := repository.DocumentCollaborationUpdates(context.Background(), "test-workspace", documentID)
	if err != nil {
		t.Fatal(err)
	}
	for _, update := range updates {
		if update.ID == updateID {
			t.Fatalf("refused update %s was stored", updateID)
		}
	}
}

func patchDescriptionStatus(t *testing.T, client *http.Client, baseURL, issueID string) int {
	t.Helper()
	request, _ := http.NewRequest(http.MethodPatch, baseURL+"/api/issues/"+issueID, strings.NewReader(`{"description":"Edited through the API"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Workspace-Key", "test-workspace")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	return response.StatusCode
}

// expectReadOnlySocket: the member's edit is refused and never applied,
// stored or relayed, while the editor's edits and awareness still reach it.
func expectReadOnlySocket(t *testing.T, repository *store.SQLiteStore, fixture issueAccessFixture, editor *websocket.Conn, updateID string) {
	t.Helper()
	writeSocket(t, fixture.socket, websocket.MessageBinary, encodeCollaborationFrame(collaborationUpdateFrame, fixture.documentID, updateID, []byte{7}))
	if notice := expectSocketNotice(t, fixture.socket, "document.readonly", fixture.documentID); notice["issueId"] != fixture.issue.ID {
		t.Fatalf("refusal = %v", notice)
	}
	expectUpdateNotStored(t, repository, fixture.documentID, updateID)
	// Awareness still flows, and it is the first thing the editor receives:
	// the refused update was not relayed before it.
	writeSocket(t, fixture.socket, websocket.MessageBinary, encodeCollaborationFrame(collaborationAwarenessFrame, fixture.documentID, "", []byte{1}))
	if messageType, raw := readSocketMessage(t, editor); messageType != websocket.MessageBinary || raw[0] != collaborationAwarenessFrame {
		t.Fatalf("editor received %v %v instead of the read-only member's awareness", messageType, raw)
	}
	editorUpdate := updateID + "_editor"
	writeSocket(t, editor, websocket.MessageBinary, encodeCollaborationFrame(collaborationUpdateFrame, fixture.documentID, editorUpdate, []byte{8}))
	assertUpdateFrame(t, editor, fixture.documentID, editorUpdate, []byte{8})
	assertUpdateFrame(t, fixture.socket, fixture.documentID, editorUpdate, []byte{8})
}

func expectEditableSocket(t *testing.T, fixture issueAccessFixture, editor *websocket.Conn, updateID string) {
	t.Helper()
	writeSocket(t, fixture.socket, websocket.MessageBinary, encodeCollaborationFrame(collaborationUpdateFrame, fixture.documentID, updateID, []byte{9}))
	assertUpdateFrame(t, fixture.socket, fixture.documentID, updateID, []byte{9})
	assertUpdateFrame(t, editor, fixture.documentID, updateID, []byte{9})
}

func TestCollaborationIssueDescriptionFollowsEditPermission(t *testing.T) {
	for _, item := range editAccessCases {
		t.Run(item.name, func(t *testing.T) {
			server, repository := newSingleAuthServerWithStore(t)
			fixture := newIssueAccessFixture(t, server.URL, server.URL, item.setup)
			if fixture.sync.ReadOnly != item.readOnly {
				t.Fatalf("join readOnly = %v, want %v", fixture.sync.ReadOnly, item.readOnly)
			}
			editor := adminDescriptionSocket(t, server.URL, fixture)
			if item.readOnly {
				expectReadOnlySocket(t, repository, fixture, editor, "collab_read_only")
			} else {
				expectEditableSocket(t, fixture, editor, "collab_editor")
			}
			// The socket applies the description API's rule (checked last:
			// a saved description starts a new collaboration generation).
			wantStatus := http.StatusOK
			if item.readOnly {
				wantStatus = http.StatusForbidden
			}
			if status := patchDescriptionStatus(t, fixture.member, server.URL, fixture.issue.ID); status != wantStatus {
				t.Fatalf("PATCH description status = %d, want %d", status, wantStatus)
			}
		})
	}
}

// A read-only API key reads the description and cannot change it, even for
// an admin who could.
func TestCollaborationIssueDescriptionReadOnlyAPIKey(t *testing.T) {
	server, repository := newSingleAuthServerWithStore(t)
	fixture := newIssueAccessFixture(t, server.URL, server.URL, issueAccessSetup{role: "member", inTeam: true})
	key := authRequest[struct {
		Secret string `json:"secret"`
	}](t, fixture.admin, http.MethodPost, server.URL+"/api/api-keys", map[string]any{"name": "Socket reader", "scopes": []string{"read"}}, "test-workspace", http.StatusCreated)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	url := strings.Replace(server.URL, "http://", "ws://", 1) + "/api/realtime/socket?workspace=test-workspace&clientId=reader-key"
	socket, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + key.Secret}}})
	if err != nil {
		t.Fatal(err)
	}
	defer socket.CloseNow()
	if sync := joinIssueDescription(t, socket, fixture.issue.ID, fixture.documentID); !sync.ReadOnly {
		t.Fatal("a read-only API key joined with write access")
	}
	editor := adminDescriptionSocket(t, server.URL, fixture)
	reader := fixture
	reader.socket = socket
	expectReadOnlySocket(t, repository, reader, editor, "collab_read_key")
}

// Downgrading an editor to viewer makes the open socket read-only at once,
// and upgrading it back lets it edit without reconnecting — on the server
// that made the change and on another one.
func TestCollaborationIssueDescriptionFollowsPermissionChanges(t *testing.T) {
	setup := issueAccessSetup{role: "member", privateTeam: true, sharedOnly: true, shareRole: "editor"}
	run := func(t *testing.T, socketURL, adminURL string, repository *store.SQLiteStore) {
		fixture := newIssueAccessFixture(t, socketURL, adminURL, setup)
		if fixture.sync.ReadOnly {
			t.Fatal("the editor joined read-only")
		}
		editor := adminDescriptionSocket(t, socketURL, fixture)
		share := func(role string) {
			authRequest[[]domain.IssuePermission](t, fixture.admin, http.MethodPut, adminURL+"/api/issues/"+fixture.issue.ID+"/permissions", map[string]any{"permissions": []map[string]string{{"subjectType": "user", "subjectId": fixture.memberUser.ID, "role": role}}}, "test-workspace", http.StatusOK)
		}
		share("viewer")
		if notice := expectSocketNotice(t, fixture.socket, "document.access", fixture.documentID); notice["readOnly"] != true {
			t.Fatalf("downgrade notice = %v", notice)
		}
		expectReadOnlySocket(t, repository, fixture, editor, "collab_downgraded")
		// Still read-only: no notice (the next one is the upgrade's).
		share("commenter")
		share("editor")
		if notice := expectSocketNotice(t, fixture.socket, "document.access", fixture.documentID); notice["readOnly"] != false {
			t.Fatalf("upgrade notice = %v", notice)
		}
		expectEditableSocket(t, fixture, editor, "collab_upgraded")
	}
	t.Run("same server", func(t *testing.T) {
		server, repository := newSingleAuthServerWithStore(t)
		run(t, server.URL, server.URL, repository)
	})
	t.Run("other server", func(t *testing.T) {
		peers := newPeerServers(t)
		run(t, peers.first.URL, peers.second.URL, peers.stores[0])
	})
}

// A grant changed on a parent issue reaches the open description of its
// sub-issue, which inherits it.
func TestCollaborationSubIssueDescriptionFollowsParentPermission(t *testing.T) {
	server, _ := newSingleAuthServerWithStore(t)
	parent := newIssueAccessFixture(t, server.URL, server.URL, issueAccessSetup{role: "member", privateTeam: true, sharedOnly: true, shareRole: "editor"})
	child := authRequest[domain.Issue](t, parent.admin, http.MethodPost, server.URL+"/api/issues", map[string]any{"title": "Child", "teamId": parent.team.ID, "parentId": parent.issue.ID}, "test-workspace", http.StatusCreated)
	documentID := "document_content_" + child.ID
	if child.DocumentContent != nil && child.DocumentContent.ID != "" {
		documentID = child.DocumentContent.ID
	}
	if sync := joinIssueDescription(t, parent.socket, child.ID, documentID); sync.ReadOnly {
		t.Fatal("the inherited editor joined the sub-issue read-only")
	}
	authRequest[[]domain.IssuePermission](t, parent.admin, http.MethodPut, server.URL+"/api/issues/"+parent.issue.ID+"/permissions", map[string]any{"permissions": []map[string]string{{"subjectType": "user", "subjectId": parent.memberUser.ID, "role": "viewer"}}}, "test-workspace", http.StatusOK)
	seen := map[string]bool{}
	for len(seen) < 2 {
		_, message := readSocketMessage(t, parent.socket)
		var notice map[string]any
		if json.Unmarshal(message, &notice) != nil || notice["type"] != "document.access" || notice["readOnly"] != true {
			t.Fatalf("unexpected message: %s", message)
		}
		seen[notice["documentId"].(string)] = true
	}
	if !seen[documentID] || !seen[parent.documentID] {
		t.Fatalf("downgrade notices = %v", seen)
	}
}

// Standalone documents: a role change reaches the open socket as
// document.access, so the editor turns read-only or editable live.
func TestCollaborationDocumentFollowsRoleChanges(t *testing.T) {
	server, owner, _, alice, _, document := documentNotificationFixture(t)
	editor := dialAuthenticatedCollaborationSocket(t, alice.client, server.URL, "alice")
	defer editor.CloseNow()
	if sync := joinStandaloneDocument(t, editor, document.ID); sync.ReadOnly {
		t.Fatal("an editor joined read-only")
	}
	grant := func(role string) {
		authRequest[[]domain.DocumentPermission](t, owner, http.MethodPut, server.URL+"/api/documents/"+document.ID+"/permissions", map[string]any{"permissions": []map[string]string{{"subjectType": "user", "subjectId": alice.user.ID, "role": role}}}, "test-workspace", http.StatusOK)
	}
	grant("viewer")
	if notice := expectSocketNotice(t, editor, "document.access", document.ID); notice["readOnly"] != true {
		t.Fatalf("downgrade notice = %v", notice)
	}
	writeSocket(t, editor, websocket.MessageBinary, encodeCollaborationFrame(collaborationUpdateFrame, document.ID, "collab_viewer", []byte{3}))
	expectSocketNotice(t, editor, "document.readonly", document.ID)
	grant("editor")
	if notice := expectSocketNotice(t, editor, "document.access", document.ID); notice["readOnly"] != false {
		t.Fatalf("upgrade notice = %v", notice)
	}
	writeSocket(t, editor, websocket.MessageBinary, encodeCollaborationFrame(collaborationUpdateFrame, document.ID, "collab_editor_again", []byte{4}))
	assertUpdateFrame(t, editor, document.ID, "collab_editor_again", []byte{4})
}
