package main

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestScaleP12JournalFallbackKeepsDeliveryAndFreshPermissions(t *testing.T) {
	t.Setenv("FLOW_SYNC_V2", "on")
	for _, mode := range []string{"", "off", "shadow", "on"} {
		t.Run("unready-"+mode, func(t *testing.T) {
			// A store opened without journal support also represents an enabled
			// deployment whose journal initialization could not complete.
			t.Setenv("FLOW_RECORD_JOURNAL", "off")
			config := scaleP03DatabaseConfig(t)
			writer, err := store.OpenDatabase(config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = writer.Close() })
			f := newScaleP12Fixture(t, writer)
			reader, err := store.OpenDatabase(config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = reader.Close() })
			t.Setenv("FLOW_RECORD_JOURNAL", mode)
			if reader.RealtimeEpochsReady() {
				t.Fatal("an uninitialized journal enabled the epoch cache")
			}
			if _, err := reader.AuthzEpoch(t.Context(), f.data.Workspace.ID); !errors.Is(err, store.ErrRecordJournalUnavailable) {
				t.Fatalf("expected unavailable journal: %v", err)
			}
			peer := &server{store: reader, realtime: newRealtimeHub()}
			request := f.principals[4].request(f.data.Workspace.URLKey)
			for range 2 {
				_, stop := peer.realtime.subscribe(f.data.Workspace.URLKey)
				t.Cleanup(stop)
			}
			event := domain.RealtimeEvent{ID: "p12-journal-fallback", Type: "issue.updated", AggregateID: f.secret.ID, Payload: json.RawMessage(`{}`)}
			scaleP12EqualEvent(t, peer, request, event)
			frame, handled, err := peer.sharedEventFrame(request, event)
			defer frame.Close()
			if err != nil || !handled || !frame.Visible() {
				t.Fatalf("journal unavailability dropped a visible event: handled=%v visible=%v err=%v", handled, frame.Visible(), err)
			}
			writerResponse := httptest.NewRecorder()
			if !peer.writeProjectedRealtime(writerResponse, request, event) || !strings.Contains(writerResponse.Body.String(), f.secret.ID) {
				t.Fatal("journal unavailability dropped the transport write")
			}
			if _, err := peer.resolveSyncAccess(request); err != nil {
				t.Fatalf("journal unavailability blocked sync access: %v", err)
			}
			cache := &peer.realtime.rules.accesses
			if len(cache.entries) != 0 || cache.bytes != 0 {
				t.Fatal("fallback retained principal policy in the epoch cache")
			}
			if err := writer.SetTeamMembership(t.Context(), f.data.Workspace.ID, f.private, f.principals[4].user.ID, "member", false); err != nil {
				t.Fatal(err)
			}
			queuedResponse := httptest.NewRecorder()
			if frame.WriteSSE(queuedResponse, "") || queuedResponse.Body.Len() != 0 {
				t.Fatal("a queued fallback frame survived a peer's revocation")
			}
			fresh, handled, err := peer.sharedEventFrame(request, event)
			defer fresh.Close()
			if err != nil || !handled || fresh.Visible() {
				t.Fatalf("fallback reused revoked access: handled=%v visible=%v err=%v", handled, fresh.Visible(), err)
			}
		})
	}
}
