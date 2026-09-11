package main

import (
	"bufio"
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
)

func TestCredentialTelemetryIsFilteredBeforeProjectionAndOldReplay(t *testing.T) {
	service := &server{}
	r := httptest.NewRequest("GET", "/api/realtime/events?workspace=missing", nil)
	for _, kind := range []string{"oauth_token.created", "api_key.used", "oauth_authorization.reused"} {
		if _, visible, err := service.pagedRealtimeEvent(r, domain.RealtimeEvent{Type: kind}); err != nil || visible {
			t.Fatalf("credential event attempted workspace projection: %s %v", kind, err)
		}
	}
	hub := newRealtimeHub()
	hub.history["workspace"] = []domain.RealtimeEvent{{ID: "cursor", Type: "issue.updated"}, {ID: "token", Type: "oauth_token.created"}, {ID: "used", Type: "api_key.used"}, {ID: "reuse", Type: "oauth_authorization.reused"}, {ID: "real", Type: "oauth_authorization.revoked"}}
	channel, unsubscribe := hub.subscribeSince("workspace", "cursor")
	defer unsubscribe()
	if len(channel) != 1 {
		t.Fatalf("old telemetry replayed or caused overflow: %d", len(channel))
	}
	if event := <-channel; event.ID != "real" {
		t.Fatalf("real revoke missing: %+v", event)
	}
}

func TestActualSSECredentialBurstKeepsRealConsentAndRevocationEvents(t *testing.T) {
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "realtime.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	service := &server{store: repo, authDisabled: true, uploadPath: t.TempDir()}
	host := httptest.NewServer(newHandler(service))
	defer host.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", host.URL+"/api/realtime/events?workspace=test-workspace&issues=paged", nil)
	response, err := host.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("SSE handshake %d", response.StatusCode)
	}
	scanner := bufio.NewScanner(response.Body)
	readEvent := func() domain.RealtimeEvent {
		t.Helper()
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data: ") {
				var event domain.RealtimeEvent
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
					t.Fatal(err)
				}
				return event
			}
		}
		t.Fatalf("SSE ended: %v", scanner.Err())
		return domain.RealtimeEvent{}
	}
	if event := readEvent(); event.Type != "connected" {
		t.Fatalf("handshake: %+v", event)
	}
	for index := 0; index < 100; index++ {
		for _, kind := range []string{"oauth_token.created", "api_key.used", "oauth_authorization.reused"} {
			service.publishRealtime("test-workspace", domain.RealtimeEvent{ID: fmt.Sprintf("%s-%d", kind, index), Type: kind, CreatedAt: time.Now().UTC()})
		}
	}
	for _, kind := range []string{"oauth_authorization.created", "oauth_authorization.revoked", "application_policy.updated"} {
		service.publishRealtime("test-workspace", domain.RealtimeEvent{ID: kind, Type: kind, ActorID: repo.Bootstrap().Viewer.ID, CreatedAt: time.Now().UTC()})
		if event := readEvent(); event.Type != kind {
			t.Fatalf("credential burst leaked or swallowed %s: %+v", kind, event)
		}
	}
	if len(service.realtime.history["test-workspace"]) != 3 {
		t.Fatal("routine telemetry consumed realtime replay history")
	}
}
