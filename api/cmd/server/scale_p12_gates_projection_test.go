package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func TestScaleP12GatesQueuedLegacyProjection(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_SYNC_V2", "on")
	f := newRealtimePolicyFixture(t)
	project := authRequest[domain.Project](t, f.admin, "POST", f.host.URL+"/api/projects", map[string]any{"name": "Queued private project", "teamIds": []string{f.private.ID}}, "test-workspace", 201)
	viewer := authRequest[domain.Bootstrap](t, f.insider, "GET", f.host.URL+"/api/bootstrap", nil, "test-workspace", 200).Viewer
	request := httptest.NewRequest("GET", "/api/realtime/events?workspace=test-workspace", nil)
	request = request.WithContext(context.WithValue(request.Context(), authUserContextKey{}, viewer))
	event := domain.RealtimeEvent{ID: "p12_gates_queued", Type: "project.updated", AggregateID: project.ID, Payload: json.RawMessage(`{"hiddenReference":"hidden-input-secret","actor":"hidden-actor-secret","count":999}`)}
	for _, revoke := range []bool{false, true} {
		subscriber, stop := f.srv.realtime.subscribeTransport("test-workspace", "", false)
		f.srv.realtime.publish("test-workspace", event)
		queued := <-subscriber.channel
		if revoke {
			authRequest[any](t, f.admin, "PUT", f.host.URL+"/api/workspaces/test-workspace/teams/"+f.private.ID+"/members/"+viewer.ID, map[string]any{"member": false}, "", 204)
		}
		want := httptest.NewRecorder()
		projected, visible, err := f.srv.pagedRealtimeEvent(request, queued)
		if err != nil {
			t.Fatal(err)
		}
		if visible {
			writeSSE(want, projected, f.srv.realtime.node)
		}
		got := httptest.NewRecorder()
		if !f.srv.writeRealtimeBatch(got, request, subscriber, []domain.RealtimeEvent{queued}, false, 0) {
			t.Fatal("budgeted batch failed")
		}
		stop()
		if got.Body.String() != want.Body.String() {
			t.Fatalf("revoked=%v\nwant %s\ngot %s", revoke, want.Body.String(), got.Body.String())
		}
		if strings.Contains(got.Body.String(), "hidden-input-secret") || strings.Contains(got.Body.String(), "hidden-actor-secret") {
			t.Fatal("queue exposed hidden fields")
		}
	}
}

func TestScaleP12GatesLegacyEnvelope(t *testing.T) {
	for _, payload := range []json.RawMessage{nil, json.RawMessage(`{"one":1}`), json.RawMessage("{\n\"one\":1\n}")} {
		event := domain.RealtimeEvent{ID: "p12_envelope", Type: "project.updated", AggregateID: "project", Payload: payload}
		want := httptest.NewRecorder()
		if !writeSSE(want, event, "node") {
			t.Fatal("legacy envelope failed")
		}
		raw, err := marshalLegacyRealtimeEvent(event)
		if err != nil {
			t.Fatal(err)
		}
		frame := EventFrame{encoded: raw, id: event.ID, visible: true}
		got := httptest.NewRecorder()
		if !frame.WriteSSE(got, "node") || got.Body.String() != want.Body.String() {
			t.Fatalf("payload %s: shared envelope differs from legacy", payload)
		}
	}
}
