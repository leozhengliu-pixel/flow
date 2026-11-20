package main

import (
	"net/http"
	"testing"

	"flow/api/internal/domain"
)

type bearerTransport struct {
	secret string
	next   http.RoundTripper
}

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.secret)
	return b.next.RoundTrip(r)
}

// TestKeyTeamScopeRealtimeDocuments: a key restricted to the public team,
// owned by the admin who created a document in a private team, receives no
// event of that document over the realtime stream (created, edited,
// commented), while the admin's session and an unrestricted key of the same
// owner receive them all. The public team's document reaches all three.
func TestKeyTeamScopeRealtimeDocuments(t *testing.T) {
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	// The bounded-store frames (the shared event source) are covered through
	// the sync change pages of TestSyncChangesPulsePagesDifferential, whose
	// store pair this HTTP fixture cannot build.
	for _, mode := range []string{"off"} {
		t.Run("bounded="+mode, func(t *testing.T) {
			t.Setenv("FLOW_BOUNDED_METADATA", mode)
			fixture := newEntityPayloadFixture(t, nil)
			workspace, base := "test-workspace", fixture.host.URL
			type created struct {
				Secret string `json:"secret"`
			}
			restrictedKey := authRequest[created](t, fixture.admin, http.MethodPost, base+"/api/api-keys", map[string]any{"name": "Restricted", "scopes": []string{"read"}, "teamIds": []string{"team_test"}}, workspace, http.StatusCreated)
			openKey := authRequest[created](t, fixture.admin, http.MethodPost, base+"/api/api-keys", map[string]any{"name": "Unrestricted", "scopes": []string{"read"}}, workspace, http.StatusCreated)
			restricted := collectSSE(t, &http.Client{Transport: bearerTransport{restrictedKey.Secret, http.DefaultTransport}}, base+"/api/realtime/events")
			unrestricted := collectSSE(t, &http.Client{Transport: bearerTransport{openKey.Secret, http.DefaultTransport}}, base+"/api/realtime/events")
			public := authRequest[domain.Document](t, fixture.admin, http.MethodPost, base+"/api/documents", map[string]any{"title": "Public", "content": "start", "teamIds": []string{"team_test"}}, workspace, http.StatusCreated)
			hidden := authRequest[domain.Document](t, fixture.admin, http.MethodPost, base+"/api/documents", map[string]any{"title": "Hidden", "content": "secret", "teamIds": []string{fixture.private.ID}}, workspace, http.StatusCreated)
			authRequest[domain.Document](t, fixture.admin, http.MethodPatch, base+"/api/documents/"+public.ID, map[string]any{"content": "edited"}, workspace, http.StatusOK)
			authRequest[domain.Document](t, fixture.admin, http.MethodPatch, base+"/api/documents/"+hidden.ID, map[string]any{"content": "secret edit"}, workspace, http.StatusOK)
			authRequest[domain.Comment](t, fixture.admin, http.MethodPost, base+"/api/documents/"+hidden.ID+"/comments", map[string]any{"body": "secret comment"}, workspace, http.StatusCreated)
			waitForEvents(t, func() bool {
				return restricted.has("document.updated", public.ID) && unrestricted.has("document.comment_created", hidden.ID) && fixture.adminSSE.has("document.comment_created", hidden.ID)
			})
			for _, eventType := range []string{"document.created", "document.updated", "document.comment_created"} {
				if len(restricted.findFor(eventType, hidden.ID)) != 0 {
					t.Fatalf("restricted key received %s of a document outside its teams", eventType)
				}
			}
			for _, name := range []struct {
				who    string
				stream *sseCollector
			}{{"session", fixture.adminSSE}, {"unrestricted key", unrestricted}} {
				for _, eventType := range []string{"document.created", "document.updated", "document.comment_created"} {
					if len(name.stream.findFor(eventType, hidden.ID)) == 0 {
						t.Fatalf("%s lost %s of the owner's private document", name.who, eventType)
					}
				}
			}
		})
	}
}

func (c *sseCollector) findFor(eventType, aggregate string) []domain.RealtimeEvent {
	out := []domain.RealtimeEvent{}
	for _, event := range c.find(eventType) {
		if event.AggregateID == aggregate {
			out = append(out, event)
		}
	}
	return out
}
