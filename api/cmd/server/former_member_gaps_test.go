package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"

	"github.com/Deln0r/ygo"
	"github.com/coder/websocket"
)

func TestDevelopmentMemberStatusPayloadAndWebhookDelivery(t *testing.T) {
	s, handler, seed, _ := webhookTestServer(t)
	receiver := newWebhookReceiver(t, http.StatusOK)
	hook := requestJSON[map[string]any](t, handler, http.MethodPost, "/api/webhooks?workspace=test-workspace", map[string]any{
		"name": "Members", "url": receiver.server.URL, "resourceTypes": []string{"User"},
	}, http.StatusCreated)
	secret := hook["secret"].(string)
	member := seed.Users[1]
	if member.ID == seed.Viewer.ID {
		member = seed.Users[2]
	}
	var notices []domain.RealtimeEvent
	s.store.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
		if event.AggregateID == member.ID {
			notices = append(notices, event)
		}
	})
	t.Cleanup(func() { s.store.SetRealtimeSink(nil) })
	for _, step := range []struct{ method, suffix, verb, status, action string }{
		{http.MethodPost, "/suspend", "suspended", "suspended", "update"},
		{http.MethodPost, "/resume", "resumed", "active", "update"},
		{http.MethodDelete, "", "removed", "removed", "remove"},
	} {
		requestJSON[any](t, handler, step.method, "/api/workspaces/test-workspace/members/"+member.ID+step.suffix, nil, http.StatusNoContent)
		kind := "workspace_member." + step.verb
		envelope, raw := receiver.waitFor(t, func(body map[string]any, _ http.Header) bool { return body["eventType"] == kind })
		data, _ := envelope["data"].(map[string]any)
		if envelope["type"] != "User" || envelope["action"] != step.action || data["id"] != member.ID || data["userId"] != member.ID || data["status"] != step.status || data["active"] != (step.status == "active") {
			t.Fatalf("member webhook = %+v", envelope)
		}
		if raw.header.Get("Flow-Event") != "User" || !verifyWebhookSignature(secret, raw.body, raw.header.Get("Flow-Signature"), time.Now(), time.Minute) {
			t.Fatal("member delivery was not signed")
		}
		index := slices.IndexFunc(notices, func(event domain.RealtimeEvent) bool { return event.Type == kind })
		if index < 0 {
			t.Fatalf("missing %s notice", kind)
		}
		var payload map[string]any
		if json.Unmarshal(notices[index].Payload, &payload) != nil || payload["userId"] != member.ID || payload["status"] != step.status {
			t.Fatalf("development member payload = %s", notices[index].Payload)
		}
	}
	audit, _ := s.store.WorkspaceMetadataFields("test-workspace", "auditLog")
	for _, action := range []string{"suspended", "resumed", "removed"} {
		if !slices.ContainsFunc(audit.AuditLog, func(entry domain.AuditLogEntry) bool {
			return entry.ResourceType == "workspace_member" && entry.ResourceID == member.ID && entry.Action == action
		}) {
			t.Fatalf("missing %s member audit", action)
		}
	}
}

func TestCollaborationFramesRefuseFormerMemberMentions(t *testing.T) {
	s, handler, seed, _ := webhookTestServer(t)
	host := httptest.NewServer(handler)
	t.Cleanup(host.Close)
	member := seed.Users[1]
	if member.ID == seed.Viewer.ID {
		member = seed.Users[2]
	}
	issue := seed.Issues[0]
	document := requestJSON[domain.Document](t, handler, http.MethodPost, "/api/documents", map[string]any{"title": "Collaboration mention test"}, http.StatusCreated)
	if err := s.store.SuspendMember(t.Context(), seed.Workspace.ID, member.ID); err != nil {
		t.Fatal(err)
	}
	for _, target := range []struct{ issueID, documentID string }{
		{issue.ID, "document_content_" + issue.ID}, {"", document.ID},
	} {
		connection := dialCollaborationSocket(t, host.URL, "mention-check-"+target.documentID)
		defer connection.CloseNow()
		joinDocument(t, connection, target.issueID, target.documentID)
		update, _ := base64.StdEncoding.DecodeString(mentioningCollaborationState(member.ID))
		writeSocket(t, connection, websocket.MessageBinary, encodeCollaborationFrame(collaborationUpdateFrame, target.documentID, "collab_former_"+target.documentID, update))
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		_, message, err := connection.Read(ctx)
		cancel()
		var response map[string]any
		if err != nil || json.Unmarshal(message, &response) != nil || response["type"] != "error" || response["message"] != errFormerMemberMention.Error() {
			t.Fatalf("former mention frame = %s, err = %v", message, err)
		}
		updates, err := s.store.DocumentCollaborationUpdates(t.Context(), seed.Workspace.URLKey, target.documentID)
		if err != nil || len(updates) != 0 {
			t.Fatalf("refused frame was persisted: %+v, %v", updates, err)
		}
	}
}

func TestChatCommentsKeepFormerMentionsAsText(t *testing.T) {
	s, _, seed, _ := webhookTestServer(t)
	member := seed.Users[1]
	if member.ID == seed.Viewer.ID {
		member = seed.Users[2]
	}
	if err := s.store.SuspendMember(t.Context(), seed.Workspace.ID, member.ID); err != nil {
		t.Fatal(err)
	}
	var comment domain.Comment
	err := s.chatAuthorizedMutation(t.Context(), seed.Workspace.URLKey, seed.Viewer.ID, http.MethodPost, "/api/issue-records/"+seed.Issues[0].ID+"/comments", domain.CommentCreateInput{
		Body: "Imported @person", BodyData: mentioningDocument("Imported ", member.ID, seed.Viewer.ID),
	}, &comment)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	collectMentionIDs(comment.BodyData, &ids)
	raw, _ := json.Marshal(comment.BodyData)
	if !slices.Equal(ids, []string{seed.Viewer.ID}) || comment.Body != "Imported @person" || !strings.Contains(string(raw), "@"+member.ID) {
		t.Fatalf("imported reply lost text or kept mention: %+v", comment)
	}
}

func TestFormerMemberStateChecksResultingTree(t *testing.T) {
	data := domain.Bootstrap{FormerMembers: func() map[string]bool { return map[string]bool{"former": true} }}
	doc := ygo.NewDoc()
	fragment := ygo.NewXmlFragment(doc, "prosemirror")
	txn := doc.WriteTxn()
	mention := fragment.InsertXmlElement(txn, 0, "mention")
	mention.SetAttribute(txn, "id", "former")
	mention.SetAttribute(txn, "mentionType", "user")
	txn.Commit()
	state := func() string { return base64.StdEncoding.EncodeToString(ygo.EncodeStateAsUpdate(doc)) }
	if err := refuseFormerMemberStateMentions(&data, nil, "", state(), ""); !errors.Is(err, errFormerMemberMention) {
		t.Fatalf("live former mention = %v", err)
	}
	txn = doc.WriteTxn()
	fragment.Delete(txn, 0, 1)
	txn.Commit()
	if err := refuseFormerMemberStateMentions(&data, nil, "", state(), ""); err != nil {
		t.Fatalf("deleted historical mention rejected: %v", err)
	}
	if err := refuseFormerMemberStateMentions(&data, nil, "", "not base64", ""); !errors.Is(err, errInvalid) {
		t.Fatalf("malformed state = %v", err)
	}
}
