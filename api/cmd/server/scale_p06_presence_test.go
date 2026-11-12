package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func scaleP06PresenceRequest(t *testing.T, client *http.Client, base, workspace string, input map[string]any, minimal bool, secret string) (int, []byte) {
	t.Helper()
	raw, _ := json.Marshal(input)
	r, err := http.NewRequest(http.MethodPost, base+"/api/realtime/presence", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Workspace-Key", workspace)
	if minimal {
		r.Header.Set("X-Flow-Presence", "minimal")
	}
	if secret != "" {
		r.Header.Set("Authorization", "Bearer "+secret)
	}
	response, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, body
}

func TestScaleP06QueuedAndReplayPermissionDifferential(t *testing.T) {
	f := newRealtimePolicyFixture(t)
	project := authRequest[domain.Project](t, f.admin, "POST", f.host.URL+"/api/projects", map[string]any{"name": "Queued private project", "teamIds": []string{f.private.ID}}, "test-workspace", 201)
	viewer := authRequest[domain.Bootstrap](t, f.insider, "GET", f.host.URL+"/api/bootstrap", nil, "test-workspace", 200).Viewer
	request := httptest.NewRequest("GET", "/api/realtime/events?workspace=test-workspace", nil)
	request = request.WithContext(context.WithValue(request.Context(), authUserContextKey{}, viewer))
	event := domain.RealtimeEvent{ID: "p06_queued", Type: "project.updated", AggregateID: project.ID, Payload: json.RawMessage(`{"hiddenReference":"hidden-input-secret","actor":"hidden-actor-secret","count":999}`)}
	for _, revoke := range []bool{false, true} {
		subscriber, stop := f.srv.realtime.subscribeTransport("test-workspace", "", false)
		f.srv.realtime.publish("test-workspace", event)
		queued := <-subscriber.channel
		if revoke {
			authRequest[any](t, f.admin, "PUT", f.host.URL+"/api/workspaces/test-workspace/teams/"+f.private.ID+"/members/"+viewer.ID, map[string]any{"member": false}, "", http.StatusNoContent)
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
		if got.Body.String() != want.Body.String() || strings.Contains(got.Body.String(), "hidden-input-secret") || strings.Contains(got.Body.String(), "hidden-actor-secret") {
			t.Fatal("queue changed permission projection or exposed hidden fields")
		}
		stop()
		f.srv.realtime.publish("test-workspace", domain.RealtimeEvent{ID: "p06_replay_cursor"})
		f.srv.realtime.publish("test-workspace", event)
		replay, stopReplay := f.srv.realtime.subscribeTransport("test-workspace", "p06_replay_cursor", false)
		replayed := <-replay.channel
		got = httptest.NewRecorder()
		if !f.srv.writeRealtimeBatch(got, request, replay, []domain.RealtimeEvent{replayed}, false, 0) || got.Body.String() != want.Body.String() {
			t.Fatal("replay reused pre-revocation permissions")
		}
		stopReplay()
	}
}

func TestScaleP06MinimalPresenceCompatibilityAndNoRosterScan(t *testing.T) {
	s := &server{store: pulseTestRepo(t), uploadPath: t.TempDir(), authDisabled: true}
	host := httptest.NewServer(newHandler(s))
	defer host.Close()
	input := map[string]any{"clientId": "minimal", "route": "/test-workspace/inbox"}
	t.Setenv("FLOW_PRESENCE_MINIMAL", "off")
	if status, body := scaleP06PresenceRequest(t, host.Client(), host.URL, "test-workspace", input, true, ""); status != 200 || len(body) == 0 {
		t.Fatalf("flag-off response status=%d bytes=%d", status, len(body))
	}
	t.Setenv("FLOW_PRESENCE_MINIMAL", "on")
	if status, body := scaleP06PresenceRequest(t, host.Client(), host.URL, "test-workspace", input, false, ""); status != 200 || len(body) == 0 {
		t.Fatal("old client lost its roster")
	}
	s.realtime.mu.Lock()
	s.realtime.presence["test-workspace"]["expired-sentinel"] = domain.Presence{ClientID: "expired-sentinel", LastSeenAt: time.Now().Add(-2 * presenceTTL)}
	s.realtime.mu.Unlock()
	for range 100 {
		if status, body := scaleP06PresenceRequest(t, host.Client(), host.URL, "test-workspace", input, true, ""); status != 204 || len(body) != 0 {
			t.Fatalf("minimal response status=%d bytes=%d", status, len(body))
		}
	}
	s.realtime.mu.Lock()
	_, stillPresent := s.realtime.presence["test-workspace"]["expired-sentinel"]
	s.realtime.mu.Unlock()
	if !stillPresent {
		t.Fatal("stable minimal heartbeats scanned/cleaned the full roster")
	}
	input["active"] = false
	if status, _ := scaleP06PresenceRequest(t, host.Client(), host.URL, "test-workspace", input, true, ""); status != 204 {
		t.Fatal("minimal leave failed")
	}
}

func TestScaleP06MinimalPresenceDefaultsOn(t *testing.T) {
	t.Setenv("FLOW_PRESENCE_MINIMAL", "")
	r := httptest.NewRequest("POST", "/api/realtime/presence", nil)
	if minimalPresenceRequest(r) {
		t.Fatal("legacy caller negotiated no header")
	}
	r.Header.Set("X-Flow-Presence", "minimal")
	if !minimalPresenceRequest(r) {
		t.Fatal("minimal negotiation is off by default")
	}
	t.Setenv("FLOW_PRESENCE_MINIMAL", "off")
	if minimalPresenceRequest(r) {
		t.Fatal("rollback ignored")
	}
}

func TestScaleP06PresencePermissionDifferential(t *testing.T) {
	t.Setenv("FLOW_PRESENCE_MINIMAL", "on")
	f := newRealtimePolicyFixture(t)
	base, workspace := f.host.URL, "test-workspace"
	issue := authRequest[domain.Issue](t, f.admin, "POST", base+"/api/issues", map[string]any{"title": "Private presence", "teamId": f.private.ID}, workspace, 201)
	guest := authRequest[domain.Bootstrap](t, f.guest, "GET", base+"/api/bootstrap", nil, workspace, 200).Viewer
	authRequest[[]domain.IssuePermission](t, f.admin, "PUT", base+"/api/issues/"+issue.ID+"/permissions", map[string]any{"permissions": []map[string]string{{"subjectType": "user", "subjectId": guest.ID, "role": "viewer"}}}, workspace, 200)
	adminOutside, adminUser := verifiedAuthClient(t, base, "Outside admin", "p06-admin@example.test")
	if err := f.repo.EnsureWorkspaceMembership(t.Context(), "workspace_test", adminUser.ID); err != nil {
		t.Fatal(err)
	}
	authRequest[domain.WorkspaceMember](t, f.admin, "PATCH", base+"/api/workspaces/"+workspace+"/members/"+adminUser.ID, map[string]string{"role": "admin"}, "", 200)
	child := authRequest[domain.Team](t, f.admin, "POST", base+"/api/workspaces/"+workspace+"/teams", map[string]any{"name": "Presence child", "key": "PCH", "parentTeamId": f.private.ID}, "", 201)
	childIssue := authRequest[domain.Issue](t, f.admin, "POST", base+"/api/issues", map[string]any{"title": "Inherited presence", "teamId": child.ID}, workspace, 201)
	document := authRequest[domain.Document](t, f.admin, "POST", base+"/api/documents", map[string]any{"title": "Original presence title", "content": "body"}, workspace, 201)
	authRequest[domain.Document](t, f.admin, "PATCH", base+"/api/documents/"+document.ID, map[string]string{"title": "Renamed presence title"}, workspace, 200)
	authRequest[domain.Bootstrap](t, f.admin, "POST", base+"/api/workspaces", map[string]string{"name": "Other presence workspace", "urlKey": "p06-other"}, "", 201)

	type scenario struct {
		name       string
		client     *http.Client
		workspace  string
		issueID    string
		documentID string
		secret     string
	}
	cases := []scenario{
		{name: "admin", client: f.admin, issueID: issue.ID},
		{name: "admin outside private team", client: adminOutside, issueID: issue.ID},
		{name: "member", client: f.insider, issueID: issue.ID},
		{name: "guest explicit grant", client: f.guest, issueID: issue.ID},
		{name: "private outsider", client: f.outsider, issueID: issue.ID},
		{name: "sub-team inheritance", client: f.insider, issueID: childIssue.ID},
		{name: "cross workspace", client: f.outsider, workspace: "p06-other", issueID: issue.ID},
		{name: "old document slug", client: f.admin, documentID: document.SlugID},
	}
	for _, policy := range []struct {
		name, restriction string
		teams             []string
	}{
		{"unrestricted", "all", []string{}},
		{"selected empty", "selected", []string{}},
		{"selected set", "selected", []string{f.private.ID}},
	} {
		teams := policy.teams
		if policy.restriction == "selected" && len(teams) == 0 {
			teams = []string{f.private.ID}
		}
		key := authRequest[struct {
			Key    domain.APIKey `json:"key"`
			Secret string        `json:"secret"`
		}](t, f.admin, "POST", base+"/api/api-keys", map[string]any{"name": "Presence " + policy.name, "scopes": []string{"read", "write"}, "teamRestriction": policy.restriction, "teamIds": teams}, workspace, 201)
		if policy.restriction == "selected" && len(policy.teams) == 0 {
			if err := f.repo.MutateWorkspace(t.Context(), workspace, "api_key.updated", key.Key.ID, nil, func(data *domain.Bootstrap) error {
				for i := range data.APIKeys {
					if data.APIKeys[i].ID == key.Key.ID {
						data.APIKeys[i].TeamIDs = []string{}
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
		cases = append(cases, scenario{name: "key " + policy.name, client: &http.Client{}, secret: key.Secret, issueID: issue.ID})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key := tc.workspace
			if key == "" {
				key = workspace
			}
			input := map[string]any{"clientId": "p06-differential", "issueId": tc.issueID, "documentId": tc.documentID, "route": "/presence"}
			legacy, legacyBody := scaleP06PresenceRequest(t, tc.client, base, key, input, false, tc.secret)
			minimal, body := scaleP06PresenceRequest(t, tc.client, base, key, input, true, tc.secret)
			if legacy != 200 {
				if minimal != legacy {
					t.Fatalf("authorization changed: old=%d minimal=%d", legacy, minimal)
				}
				return
			}
			if minimal != 204 || len(body) != 0 {
				t.Fatalf("old=%d minimal=%d bytes=%d", legacy, minimal, len(body))
			}
			// The old adapter must still return exactly the same fields after
			// a minimal touch. Only lastSeenAt is expected to advance.
			_, updated := scaleP06PresenceRequest(t, tc.client, base, key, input, false, tc.secret)
			normalize := func(raw []byte) []byte {
				var roster []domain.Presence
				if json.Unmarshal(raw, &roster) != nil {
					t.Fatal("invalid legacy roster")
				}
				for i := range roster {
					roster[i].LastSeenAt = time.Time{}
				}
				slices.SortFunc(roster, func(a, b domain.Presence) int { return bytes.Compare([]byte(a.ClientID), []byte(b.ClientID)) })
				encoded, _ := json.Marshal(roster)
				return encoded
			}
			if !bytes.Equal(normalize(legacyBody), normalize(updated)) {
				t.Fatal("legacy roster fields changed after minimal heartbeat")
			}
		})
	}
}
