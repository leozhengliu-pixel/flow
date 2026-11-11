package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type scaleP02Response struct {
	status int
	body   any
}

func scaleP02Difference(left, right any) string {
	if reflect.DeepEqual(left, right) {
		return ""
	}
	switch value := left.(type) {
	case map[string]any:
		other, ok := right.(map[string]any)
		if ok {
			for key, item := range value {
				if difference := scaleP02Difference(item, other[key]); difference != "" {
					return "." + key + difference
				}
			}
		}
	case []any:
		other, ok := right.([]any)
		if ok && len(value) == len(other) {
			for i, item := range value {
				if difference := scaleP02Difference(item, other[i]); difference != "" {
					return fmt.Sprintf("[%d]%s", i, difference)
				}
			}
		}
	}
	return " (value or shape)"
}

func scaleP02Read(t *testing.T, client *http.Client, endpoint, workspace string) scaleP02Response {
	t.Helper()
	r, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("X-Workspace-Key", workspace)
	r.Header.Set("X-Request-ID", "scale-p02-differential")
	response, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	var body any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("non-JSON response from %s: %v", endpoint, err)
	}
	return scaleP02Response{status: response.StatusCode, body: body}
}

// Compare every response field against interval=0 on the same canonical data.
// Session activity is absent from these resource responses by contract.
func TestScaleP02SessionTouchPermissionDifferential(t *testing.T) {
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	for name, repo := range favoriteTabTestStores(t) {
		t.Run(name, func(t *testing.T) { testScaleP02SessionTouchPermissionDifferential(t, repo) })
	}
}

func testScaleP02SessionTouchPermissionDifferential(t *testing.T, repo *store.SQLiteStore) {
	suffix := fmt.Sprint(time.Now().UnixNano())
	srv := &server{store: repo, uploadPath: t.TempDir()}
	host := httptest.NewServer(newHandler(srv))
	t.Cleanup(host.Close)
	admin := peerAdmin(t, host.URL)
	const workspace = "test-workspace"
	base := host.URL
	private := authRequest[domain.Team](t, admin, "POST", base+"/api/workspaces/"+workspace+"/teams", map[string]any{"name": "P02 private", "key": "PTP" + suffix[len(suffix)-5:], "private": true}, "", http.StatusCreated)
	child := authRequest[domain.Team](t, admin, "POST", base+"/api/workspaces/"+workspace+"/teams", map[string]any{"name": "P02 child", "key": "PTC" + suffix[len(suffix)-5:], "parentTeamId": private.ID}, "", http.StatusCreated)
	member, memberUser := joinByInvitation(t, base, admin, "P02 member", "p02-member-"+suffix+"@example.test", "member", "team_test", private.ID)
	authRequest[any](t, admin, "PUT", base+"/api/workspaces/"+workspace+"/teams/"+private.ID+"/members/"+memberUser.ID, map[string]any{"member": true, "role": "owner"}, "", http.StatusNoContent)
	outsider, _ := joinByInvitation(t, base, admin, "P02 outsider", "p02-outsider-"+suffix+"@example.test", "member", "team_test")
	outsideAdmin, _ := joinByInvitation(t, base, admin, "P02 outside admin", "p02-admin-"+suffix+"@example.test", "admin", "team_test")
	guest, guestUser := joinByInvitation(t, base, admin, "P02 guest", "p02-guest-"+suffix+"@example.test", "guest", "team_test")
	// Persist new viewers' defaults: synthesized preferences carry the current
	// time on every read, independently of session activity.
	for _, client := range []*http.Client{member, outsider, outsideAdmin, guest} {
		authRequest[domain.UserSettings](t, client, "PATCH", base+"/api/account/settings", map[string]any{"language": "en-US"}, workspace, http.StatusOK)
		authRequest[domain.NotificationPreferences](t, client, "PATCH", base+"/api/notification-preferences", map[string]any{"emailFormat": "immediate"}, workspace, http.StatusOK)
	}
	public := repo.Bootstrap().Issues[0]
	hidden := authRequest[domain.Issue](t, admin, "POST", base+"/api/issue-records", map[string]any{"title": "P02 hidden", "teamId": private.ID}, workspace, http.StatusCreated)
	shared := authRequest[domain.Issue](t, admin, "POST", base+"/api/issue-records", map[string]any{"title": "P02 shared", "teamId": private.ID}, workspace, http.StatusCreated)
	inherited := authRequest[domain.Issue](t, admin, "POST", base+"/api/issue-records", map[string]any{"title": "P02 inherited", "teamId": child.ID}, workspace, http.StatusCreated)
	authRequest[any](t, admin, "PUT", base+"/api/issues/"+shared.ID+"/permissions", map[string]any{"permissions": []map[string]string{{"subjectType": "user", "subjectId": guestUser.ID, "role": "viewer"}}}, workspace, http.StatusOK)
	project := authRequest[domain.Project](t, admin, "POST", base+"/api/projects", map[string]any{"name": "P02 old slug", "teamIds": []string{private.ID}}, workspace, http.StatusCreated)
	authRequest[domain.Project](t, admin, "PATCH", base+"/api/projects/"+project.ID, map[string]string{"name": "P02 new slug"}, workspace, http.StatusOK)
	other := authRequest[domain.Bootstrap](t, admin, "POST", base+"/api/workspaces", map[string]string{"name": "P02 other", "urlKey": "p02-other-" + suffix}, "", http.StatusCreated)
	if err := repo.MutateWorkspace(t.Context(), workspace, "issue.updated", public.ID, nil, func(data *domain.Bootstrap) error {
		for i := range data.Issues {
			if data.Issues[i].ID == public.ID {
				data.Issues[i].SubIssueIDs = append(data.Issues[i].SubIssueIDs, hidden.ID)
				data.Issues[i].Relations = append(data.Issues[i].Relations, domain.IssueRelation{ID: "p02-hidden-reference", Type: "related", IssueID: public.ID, RelatedIssueID: hidden.ID})
			}
		}
		data.Activities[public.ID] = append(data.Activities[public.ID], domain.ActivityEvent{ID: "p02-hidden-actor", Type: "issue.updated", Actor: domain.User{ID: "p02-actor-outside-workspace", Name: "P02 actor"}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	keyClients := map[string]*http.Client{}
	keyIDs := []string{}
	for _, item := range []struct {
		name, restriction string
		teams             []string
	}{{"unrestricted", "all", nil}, {"selected-empty", "selected", []string{}}, {"selected-set", "selected", []string{"team_test"}}} {
		teams := item.teams
		if item.name == "selected-empty" {
			teams = []string{"team_test"}
		}
		key := authRequest[struct {
			Secret string        `json:"secret"`
			Key    domain.APIKey `json:"key"`
		}](t, admin, "POST", base+"/api/api-keys", map[string]any{"name": "P02 " + item.name, "scopes": []string{"read"}, "teamRestriction": item.restriction, "teamIds": teams}, workspace, http.StatusCreated)
		if item.name == "selected-empty" {
			// Existing credentials can lose all selected teams. Creation of an
			// empty selection is rejected by the HTTP API, so seed that state.
			if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "apiKeys"), workspace, "api_key.updated", key.Key.ID, nil, func(data *domain.Bootstrap) error {
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
		client := &http.Client{Transport: issueReadKeyTransport{secret: key.Secret}}
		keyClients[item.name] = client
		keyIDs = append(keyIDs, key.Key.ID)
		_ = scaleP02Read(t, client, base+"/api/issue-records", workspace)
	}
	// Let the existing API-key telemetry worker finish its first use before
	// comparing responses that include key metadata.
	waitForEvents(t, func() bool {
		data, _ := repo.BootstrapFor(workspace)
		return len(keyIDs) == 3 && slices.ContainsFunc(data.APIKeys, func(key domain.APIKey) bool { return key.ID == keyIDs[0] && key.LastUsedAt != nil }) && slices.ContainsFunc(data.APIKeys, func(key domain.APIKey) bool { return key.ID == keyIDs[1] && key.LastUsedAt != nil }) && slices.ContainsFunc(data.APIKeys, func(key domain.APIKey) bool { return key.ID == keyIDs[2] && key.LastUsedAt != nil })
	})
	type principal struct {
		name                                     string
		client                                   *http.Client
		privateStatus, sharedStatus, childStatus int
	}
	principals := []principal{
		{"admin", admin, 200, 200, 200},
		{"admin-outside-private", outsideAdmin, 404, 404, 404},
		{"member-and-subteam-inheritance", member, 200, 200, 200},
		{"guest-explicit-grant", guest, 404, 200, 404},
		{"private-outsider", outsider, 404, 404, 404},
		{"api-key-unrestricted", keyClients["unrestricted"], 200, 200, 200},
		{"api-key-selected-empty", keyClients["selected-empty"], 404, 404, 404},
		{"api-key-selected-set", keyClients["selected-set"], 404, 404, 404},
	}
	for _, who := range principals {
		t.Run(who.name, func(t *testing.T) {
			for _, endpoint := range []string{"/api/bootstrap", "/api/issue-records", "/api/issue-records/" + public.ID, "/api/issue-records/" + public.ID + "/history", "/api/issue-records/" + hidden.ID, "/api/issue-records/" + shared.ID, "/api/issue-records/" + inherited.ID, "/api/projects/" + project.SlugID, "/api/realtime/changes?since=0"} {
				t.Setenv("FLOW_SESSION_TOUCH_INTERVAL", "0")
				legacy := scaleP02Read(t, who.client, base+endpoint, workspace)
				t.Setenv("FLOW_SESSION_TOUCH_INTERVAL", "")
				current := scaleP02Read(t, who.client, base+endpoint, workspace)
				if !reflect.DeepEqual(current, legacy) {
					t.Fatalf("response differs at %s%s: legacy status=%d current status=%d", endpoint, scaleP02Difference(legacy.body, current.body), legacy.status, current.status)
				}
				want := 0
				switch endpoint {
				case "/api/issue-records/" + hidden.ID:
					want = who.privateStatus
				case "/api/issue-records/" + shared.ID:
					want = who.sharedStatus
				case "/api/issue-records/" + inherited.ID:
					want = who.childStatus
				}
				if want != 0 && current.status != want {
					t.Fatalf("%s status=%d, want %d", endpoint, current.status, want)
				}
			}
			t.Setenv("FLOW_SESSION_TOUCH_INTERVAL", "0")
			legacy := scaleP02Read(t, who.client, base+"/api/issue-records/"+hidden.ID, other.Workspace.URLKey)
			t.Setenv("FLOW_SESSION_TOUCH_INTERVAL", "")
			current := scaleP02Read(t, who.client, base+"/api/issue-records/"+hidden.ID, other.Workspace.URLKey)
			if !reflect.DeepEqual(current, legacy) || current.status == http.StatusOK {
				t.Fatal("cross-workspace response changed or exposed the issue")
			}
		})
	}

	// Hold the authenticated request as if its event were still queued. A team
	// revocation must affect both that projection and a durable replay.
	var queued *http.Request
	u, _ := url.Parse(base)
	request := httptest.NewRequest("GET", base+"/api/realtime/events", nil)
	request.Header.Set("X-Workspace-Key", workspace)
	for _, cookie := range member.Jar.Cookies(u) {
		request.AddCookie(cookie)
	}
	srv.authenticate(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { queued = r })).ServeHTTP(httptest.NewRecorder(), request)
	if queued == nil {
		t.Fatal("queue request was not authenticated")
	}
	event := domain.RealtimeEvent{Type: "issue.updated", AggregateID: hidden.ID, Payload: json.RawMessage(`{}`)}
	if _, visible, err := srv.pagedRealtimeEvent(queued, event); err != nil || !visible {
		t.Fatalf("member did not see queued issue before revoke: %v", err)
	}
	authRequest[any](t, admin, "PUT", base+"/api/workspaces/"+workspace+"/teams/"+private.ID+"/members/"+memberUser.ID, map[string]any{"member": false}, "", http.StatusNoContent)
	for _, mode := range []string{"0", ""} {
		t.Setenv("FLOW_SESSION_TOUCH_INTERVAL", mode)
		if _, visible, err := srv.pagedRealtimeEvent(queued, event); err != nil || visible {
			t.Fatalf("queued event after revoke: visible=%v error=%v", visible, err)
		}
		page := authRequest[changesResponse](t, member, "GET", base+"/api/realtime/changes?since=0", nil, workspace, http.StatusOK)
		if slicesContainsAggregate(page.Events, hidden.ID) {
			t.Fatal("durable replay exposed revoked issue")
		}
	}
	var token string
	for _, cookie := range member.Jar.Cookies(u) {
		if cookie.Name == sessionCookieName {
			token = cookie.Value
		}
	}
	if err := repo.Logout(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"0", ""} {
		t.Setenv("FLOW_SESSION_TOUCH_INTERVAL", mode)
		response := scaleP02Read(t, member, base+"/api/realtime/changes?since=0", workspace)
		if response.status != http.StatusUnauthorized {
			t.Fatalf("revoked session replay status=%d", response.status)
		}
	}
}

func TestScaleP02SessionTouchExistingSecurityRegressions(t *testing.T) {
	for _, mode := range []string{"0", ""} {
		t.Run("interval="+mode, func(t *testing.T) {
			t.Setenv("FLOW_SESSION_TOUCH_INTERVAL", mode)
			t.Run("password-reset", TestPasswordResetRevokesSessions)
			t.Run("authentication-policy", TestAuthenticationPolicyEnforcesCurrentSessionAndMFA)
			t.Run("collaboration-revoke", TestCollaborationIssueAccessRevocationReachesOpenSockets)
		})
	}
}
