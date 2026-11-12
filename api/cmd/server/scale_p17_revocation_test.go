package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP17QueuedAndReplayedDocumentRevocation(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_DOCUMENT_RECORDS", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace := f.data.Workspace.URLKey
		member, user := scaleP03Join(t, f, "P17 revoked", idgen.New("p17-revoked-")+"@example.test", "member")
		if err := repo.MutateWorkspace(t.Context(), workspace, "p17.fixture", "", nil, func(data *domain.Bootstrap) error {
			data.Documents = append(data.Documents, domain.Document{ID: "p17-revoked", SlugID: "p17-revoked", Title: "p17-hidden-title", Content: "p17-hidden-body", Creator: data.Viewer, Permissions: []domain.DocumentPermission{{ID: "p17-owner", SubjectType: "user", SubjectID: data.Viewer.ID, Role: "owner"}, {ID: "p17-grant", SubjectType: "user", SubjectID: user.ID, Role: "editor"}}})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		authRequest[domain.Document](t, member, "GET", f.host.URL+"/api/documents/p17-revoked", nil, workspace, http.StatusOK)
		subscriber, stop := f.service.realtime.subscribeTransport(workspace, "", false)
		defer stop()
		event := domain.RealtimeEvent{ID: "p17-queued", Type: "document.updated", AggregateID: "p17-revoked", Payload: json.RawMessage(`{"hiddenReference":"p17-hidden-reference","actor":"p17-hidden-actor","count":999}`)}
		f.service.realtime.publish(workspace, event)
		queued := <-subscriber.channel
		authRequest[any](t, f.admin, "DELETE", f.host.URL+"/api/documents/p17-revoked/permissions/p17-grant", nil, workspace, http.StatusNoContent)
		request := httptest.NewRequest("GET", "/api/realtime/events?workspace="+workspace, nil)
		request = request.WithContext(context.WithValue(request.Context(), authUserContextKey{}, user))
		t.Setenv("FLOW_DOCUMENT_RECORDS", "off")
		projected, visible, err := f.service.pagedRealtimeEvent(request, queued)
		if err != nil {
			t.Fatal(err)
		}
		want := httptest.NewRecorder()
		if visible {
			writeSSE(want, projected, f.service.realtime.node)
		}
		t.Setenv("FLOW_DOCUMENT_RECORDS", "on")
		got := httptest.NewRecorder()
		if !f.service.writeRealtimeBatch(got, request, subscriber, []domain.RealtimeEvent{queued}, false, 0) || !bytes.Equal(got.Body.Bytes(), want.Body.Bytes()) || bytes.Contains(got.Body.Bytes(), []byte("p17-hidden")) {
			t.Fatal("queued document event reused revoked permissions")
		}
		stop()
		f.service.realtime.publish(workspace, domain.RealtimeEvent{ID: "p17-cursor"})
		f.service.realtime.publish(workspace, event)
		replay, stopReplay := f.service.realtime.subscribeTransport(workspace, "p17-cursor", false)
		defer stopReplay()
		replayed := <-replay.channel
		got = httptest.NewRecorder()
		if !f.service.writeRealtimeBatch(got, request, replay, []domain.RealtimeEvent{replayed}, false, 0) || !bytes.Equal(got.Body.Bytes(), want.Body.Bytes()) {
			t.Fatal("replay reused pre-revocation document permissions")
		}
		for _, mode := range []string{"off", "on"} {
			t.Setenv("FLOW_DOCUMENT_RECORDS", mode)
			authRequest[any](t, member, "GET", f.host.URL+"/api/documents/p17-revoked", nil, workspace, http.StatusNotFound)
		}
	}
}
