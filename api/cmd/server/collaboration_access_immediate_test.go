package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"flow/api/internal/domain"

	"github.com/coder/websocket"
)

// An issue permission change is in force when its request returns: an edit
// sent right after a downgrade is refused, and one sent right after an
// upgrade is applied, with no window in which the old access still counts.
func TestCollaborationIssuePermissionChangeAppliesBeforeTheResponse(t *testing.T) {
	server, repository := newSingleAuthServerWithStore(t)
	fixture := newIssueAccessFixture(t, server.URL, server.URL, issueAccessSetup{role: "member", privateTeam: true, sharedOnly: true, shareRole: "editor"})
	editor := adminDescriptionSocket(t, server.URL, fixture)
	share := func(role string) {
		authRequest[[]domain.IssuePermission](t, fixture.admin, http.MethodPut, server.URL+"/api/issues/"+fixture.issue.ID+"/permissions", map[string]any{"permissions": []map[string]string{{"subjectType": "user", "subjectId": fixture.memberUser.ID, "role": role}}}, "test-workspace", http.StatusOK)
	}
	for round := range 5 {
		share("viewer")
		// The frame goes out without waiting for the notice: the notice was
		// queued before the response, the refusal follows it.
		updateID := fmt.Sprintf("collab_immediate_viewer_%d", round)
		writeSocket(t, fixture.socket, websocket.MessageBinary, encodeCollaborationFrame(collaborationUpdateFrame, fixture.documentID, updateID, []byte{byte(round)}))
		if notice := expectSocketNotice(t, fixture.socket, "document.access", fixture.documentID); notice["readOnly"] != true {
			t.Fatalf("round %d: downgrade notice = %v", round, notice)
		}
		expectSocketNotice(t, fixture.socket, "document.readonly", fixture.documentID)
		expectUpdateNotStored(t, repository, fixture.documentID, updateID)

		share("editor")
		updateID = fmt.Sprintf("collab_immediate_editor_%d", round)
		writeSocket(t, fixture.socket, websocket.MessageBinary, encodeCollaborationFrame(collaborationUpdateFrame, fixture.documentID, updateID, []byte{byte(round + 10)}))
		if notice := expectSocketNotice(t, fixture.socket, "document.access", fixture.documentID); notice["readOnly"] != false {
			t.Fatalf("round %d: upgrade notice = %v", round, notice)
		}
		assertUpdateFrame(t, fixture.socket, fixture.documentID, updateID, []byte{byte(round + 10)})
		assertUpdateFrame(t, editor, fixture.documentID, updateID, []byte{byte(round + 10)})
	}
}

// Re-parenting a sub-issue changes the grants it inherits: its open
// description follows at once (not on the next stale frame or the sweep), on
// the server that made the change and on another one, whether the parent is
// set through PATCH or a sub-issue relation.
func TestCollaborationSubIssueFollowsReparenting(t *testing.T) {
	run := func(t *testing.T, socketURL, adminURL string) {
		parent := newIssueAccessFixture(t, socketURL, adminURL, issueAccessSetup{role: "member", privateTeam: true, sharedOnly: true, shareRole: "editor"})
		other := authRequest[domain.Issue](t, parent.admin, http.MethodPost, adminURL+"/api/issues", map[string]any{"title": "Viewer parent", "teamId": parent.team.ID}, "test-workspace", http.StatusCreated)
		authRequest[[]domain.IssuePermission](t, parent.admin, http.MethodPut, adminURL+"/api/issues/"+other.ID+"/permissions", map[string]any{"permissions": []map[string]string{{"subjectType": "user", "subjectId": parent.memberUser.ID, "role": "viewer"}}}, "test-workspace", http.StatusOK)
		child := authRequest[domain.Issue](t, parent.admin, http.MethodPost, adminURL+"/api/issues", map[string]any{"title": "Moving child", "teamId": parent.team.ID, "parentId": parent.issue.ID}, "test-workspace", http.StatusCreated)
		documentID := "document_content_" + child.ID
		if child.DocumentContent != nil && child.DocumentContent.ID != "" {
			documentID = child.DocumentContent.ID
		}
		// Read the member's view on the socket's server first (a peer
		// applies the admin's writes as their events arrive).
		deadline := time.Now().Add(5 * time.Second)
		for {
			if _, err := tryAuthRequest(parent.member, http.MethodGet, socketURL+"/api/issue-records/"+child.ID, "test-workspace"); err == nil || time.Now().After(deadline) {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if sync := joinIssueDescription(t, parent.socket, child.ID, documentID); sync.ReadOnly {
			t.Fatal("the inherited editor joined the sub-issue read-only")
		}
		authRequest[domain.Issue](t, parent.admin, http.MethodPatch, adminURL+"/api/issues/"+child.ID, map[string]any{"parentId": other.ID}, "test-workspace", http.StatusOK)
		if notice := expectSocketNoticeWithin(t, parent.socket, "document.access", documentID, 3*time.Second); notice["readOnly"] != true {
			t.Fatalf("re-parent notice = %v", notice)
		}
		authRequest[domain.IssueRelation](t, parent.admin, http.MethodPost, adminURL+"/api/issues/"+child.ID+"/relations", map[string]any{"type": "sub_issue_of", "relatedIssueId": parent.issue.ID}, "test-workspace", http.StatusCreated)
		if notice := expectSocketNoticeWithin(t, parent.socket, "document.access", documentID, 3*time.Second); notice["readOnly"] != false {
			t.Fatalf("relation re-parent notice = %v", notice)
		}
	}
	t.Run("same server", func(t *testing.T) {
		server, _ := newSingleAuthServerWithStore(t)
		run(t, server.URL, server.URL)
	})
	t.Run("other server", func(t *testing.T) {
		peers := newPeerServers(t)
		run(t, peers.first.URL, peers.second.URL)
	})
}

// tryAuthRequest reports a request's failure instead of failing the test.
func tryAuthRequest(client *http.Client, method, url, workspace string) (int, error) {
	request, err := http.NewRequest(method, url, nil)
	if err != nil {
		return 0, err
	}
	request.Header.Set("X-Workspace-Key", workspace)
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return response.StatusCode, fmt.Errorf("status %d", response.StatusCode)
	}
	return response.StatusCode, nil
}

// expectSocketNoticeWithin is expectSocketNotice with a deadline shorter
// than the update-frame re-check and the sweep, skipping other documents'
// notices.
func expectSocketNoticeWithin(t *testing.T, connection *websocket.Conn, kind, documentID string, within time.Duration) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), within)
	defer cancel()
	for {
		messageType, message, err := connection.Read(ctx)
		if err != nil {
			t.Fatalf("no %s within %s: %v", kind, within, err)
		}
		if messageType == websocket.MessageBinary {
			continue
		}
		var notice map[string]any
		if json.Unmarshal(message, &notice) == nil && notice["type"] == kind && notice["documentId"] == documentID {
			return notice
		}
	}
}

// A queued access re-check invalidates the cached access before it returns,
// so an update frame arriving before the check has run is re-checked first;
// a check that read the access before a later invalidation leaves it stale.
func TestQueuedSocketAccessCheckInvalidatesAtOnce(t *testing.T) {
	s := &server{realtime: newRealtimeHub()}
	client := s.realtime.addSocket("test-workspace", "client-1", func() {})
	release, started := make(chan struct{}), make(chan struct{}, 1)
	s.realtime.mu.Lock()
	client.documents["doc_1"] = struct{}{}
	client.issueDocuments["doc_1"] = "issue_1"
	client.issueAccess["doc_1"] = &issueSocketAccess{teamID: "team_1", canEdit: true, checkedAt: time.Now()}
	client.checkIssue = func(issueID string) (domain.Issue, bool, bool, error) {
		started <- struct{}{}
		<-release
		return domain.Issue{ID: issueID, Team: domain.Team{ID: "team_1"}}, true, false, nil
	}
	s.realtime.mu.Unlock()
	if !s.issueDocumentAccessFresh(client, "doc_1") {
		t.Fatal("setup: access is not fresh")
	}
	s.queueSocketAccessCheck("test-workspace", domain.RealtimeEvent{Type: "issue.permission_updated", AggregateID: "issue_parent"})
	if s.issueDocumentAccessFresh(client, "doc_1") {
		t.Fatal("the queued check left the cached access fresh")
	}
	<-started
	// Another change is applied while the check runs: what the check read
	// may predate it.
	s.realtime.socketAccessWork("test-workspace", socketAccessScope{issues: map[string]string{"*": ""}})
	release <- struct{}{}
	waitForCondition(t, func() bool { return !s.issueDocumentEditable(client, "doc_1") })
	if s.issueDocumentAccessFresh(client, "doc_1") {
		t.Fatal("a check overtaken by an invalidation marked the access fresh")
	}
	// The next frame's re-check reads the access again and marks it fresh.
	s.realtime.mu.Lock()
	client.checkIssue = func(issueID string) (domain.Issue, bool, bool, error) {
		return domain.Issue{ID: issueID, Team: domain.Team{ID: "team_1"}}, true, false, nil
	}
	s.realtime.mu.Unlock()
	if allowed, err := s.recheckIssueDocument(client, "doc_1", "issue_1"); err != nil || !allowed {
		t.Fatalf("re-check = %v %v", allowed, err)
	}
	if !s.issueDocumentAccessFresh(client, "doc_1") || s.issueDocumentEditable(client, "doc_1") {
		t.Fatal("the frame's re-check did not record the read-only access")
	}
}

func waitForCondition(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestCollaborationAccessScopeReparenting(t *testing.T) {
	for _, item := range []struct {
		event domain.RealtimeEvent
		every bool
	}{
		{domain.RealtimeEvent{Type: "issue.updated", AggregateID: "issue_1", Payload: json.RawMessage(`{"parentId":"issue_2","entity":{"team":{"id":"team_1"}}}`)}, true},
		{domain.RealtimeEvent{Type: "issue.updated", AggregateID: "issue_1", Payload: json.RawMessage(`{"changes":{"parentId":""},"entity":{"team":{"id":"team_1"}}}`)}, true},
		{domain.RealtimeEvent{Type: "issue.updated", AggregateID: "issue_1", Payload: json.RawMessage(`{"title":"Renamed","entity":{"team":{"id":"team_1"},"parentId":"issue_2"}}`)}, false},
		{domain.RealtimeEvent{Type: "issue.relation_added", AggregateID: "issue_1", Payload: json.RawMessage(`{"type":"sub_issue_of","relatedIssueId":"issue_2"}`)}, true},
		{domain.RealtimeEvent{Type: "issue.relation_removed", AggregateID: "issue_1", Payload: json.RawMessage(`{"relationId":"relation_1"}`)}, true},
	} {
		scope, ok := collaborationAccessScope(item.event)
		_, every := scope.issues["*"]
		if !ok || every != item.every {
			t.Fatalf("scope(%s %s) = %+v %v, every want %v", item.event.Type, item.event.Payload, scope, ok, item.every)
		}
	}
	if _, ok := collaborationAccessScope(domain.RealtimeEvent{Type: "issue.relation_added", Payload: json.RawMessage(`{"type":"related"}`)}); ok {
		t.Fatal("a related-issue link re-checks descriptions")
	}
}
