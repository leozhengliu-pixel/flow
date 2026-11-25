package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// With bounded metadata, team routes resolve the viewer's access and then
// check the team against it. A write elsewhere in the workspace (loop
// schedule bookkeeping here; API-key usage and run bookkeeping in practice)
// can land between the two and retire the resolved access. That must not turn
// a valid request into 403 "This resource is outside your teams": the check
// resolves access again. A real revocation in the same window (a member
// removed from the team, an admin demoted, a key revoked) is still refused.
// On SQLite and on the database named by FLOW_TEST_DATABASE_DRIVER/URL.
func TestTeamAccessSurvivesConcurrentRevisionChange(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	for name, repo := range favoriteTabTestStores(t) {
		t.Run(name, func(t *testing.T) { testTeamAccessSurvivesConcurrentRevisionChange(t, repo) })
	}
}

func testTeamAccessSurvivesConcurrentRevisionChange(t *testing.T, repository *store.SQLiteStore) {
	serving, err := repository.BoundedMetadataViewForTesting(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	host := httptest.NewServer(newHandler(&server{store: serving, uploadPath: t.TempDir()}))
	defer host.Close()
	t.Cleanup(func() { store.SetTeamAccessReadHookForTesting(nil) })
	const workspace = "test-workspace"
	url := func(path string) string { return host.URL + path }
	suffix := fmt.Sprint(time.Now().UnixNano())

	admin := authClient(t)
	authRequest[domain.AuthSession](t, admin, http.MethodPost, url("/api/auth/login"), map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	authRequest[domain.WorkspaceSettings](t, admin, http.MethodPatch, url("/api/workspace/preferences"), map[string]any{"featureFlags": map[string]bool{"loops": true}}, workspace, http.StatusOK)
	join := func(name, role string) (*http.Client, domain.User) {
		client, user := verifiedAuthClient(t, host.URL, name, "team-race-"+strings.ToLower(name)+"-"+suffix+"@example.com")
		invitation := authRequest[[]domain.Invitation](t, admin, http.MethodPost, url("/api/workspaces/"+workspace+"/invitations"), map[string]any{"emails": []string{user.Email}, "role": role}, "", http.StatusCreated)
		authRequest[domain.WorkspaceMembership](t, client, http.MethodPost, url("/api/invitations/accept"), map[string]string{"token": invitation[0].Token}, "", http.StatusOK)
		return client, user
	}
	member, memberUser := join("Member", "member")
	_, joinerUser := join("Joiner", "member")
	deputy, deputyUser := join("Deputy", "admin")
	bootstrap := authRequest[domain.Bootstrap](t, admin, http.MethodGet, url("/api/bootstrap"), nil, workspace, http.StatusOK)
	workspaceID := bootstrap.Workspace.ID
	key := "TR" + suffix[len(suffix)-4:]
	private := authRequest[domain.Team](t, admin, http.MethodPost, url("/api/workspaces/"+workspace+"/teams"), map[string]any{"name": "Race private " + suffix, "key": key, "private": true}, "", http.StatusCreated)
	authRequest[any](t, admin, http.MethodPut, url("/api/workspaces/"+workspace+"/teams/"+private.ID+"/members/"+memberUser.ID), map[string]any{"member": true}, "", http.StatusNoContent)
	loop := authRequest[domain.Loop](t, admin, http.MethodPost, url("/api/loops"), map[string]any{"name": "Race bookkeeping " + suffix, "instructions": "Report.", "status": "published"}, workspace, http.StatusCreated)

	// arm runs write once, at the nth team access read of the next request.
	// Every case checks that the read was reached, so each one really races.
	var reads atomic.Int32
	arm := func(nth int32, write func()) {
		reads.Store(0)
		var once sync.Once
		store.SetTeamAccessReadHookForTesting(func() {
			if reads.Add(1) == nth {
				once.Do(write)
			}
		})
	}
	disarm := func(label string, nth int32) {
		t.Helper()
		store.SetTeamAccessReadHookForTesting(nil)
		if got := reads.Load(); got < nth {
			t.Fatalf("%s: the team access read ran %d times, want at least %d", label, got, nth)
		}
	}
	// Loop schedule bookkeeping: an unrelated write that advances the
	// workspace revision and authorization epoch.
	bookkeeping := func() {
		next := time.Now().UTC().Add(time.Hour)
		if err := serving.MutateLoopNextRun(context.Background(), workspace, loop.ID, func(data *domain.Bootstrap) error {
			for index := range data.Loops {
				if data.Loops[index].ID == loop.ID {
					data.Loops[index].NextRunAt = &next
				}
			}
			return nil
		}); err != nil {
			t.Errorf("loop bookkeeping: %v", err)
		}
	}
	send := func(client *http.Client, bearer, method, path string, input any, workspaceKey string) (int, string) {
		t.Helper()
		var body io.Reader
		if input != nil {
			body = bytes.NewReader(mustJSON(t, input))
		}
		request, err := http.NewRequest(method, url(path), body)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		if workspaceKey != "" {
			request.Header.Set("X-Workspace-Key", workspaceKey)
		}
		if bearer != "" {
			request.Header.Set("Authorization", "Bearer "+bearer)
		}
		if client == nil {
			client = http.DefaultClient
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, _ := io.ReadAll(response.Body)
		return response.StatusCode, string(raw)
	}

	// A workspace admin in the private team: every team-scoped route answers
	// as it does without the concurrent write. nth 2 reaches the handler's
	// own team read after authorization (the public members API).
	for _, item := range []struct {
		method, path string
		input        any
		workspaceKey string
		want         int
		nth          int32
		// rejoin puts the joiner back in the team before each call.
		rejoin bool
	}{
		{http.MethodPatch, "/api/teams/" + private.ID + "/settings", map[string]any{"loopPermission": "allMembers"}, workspace, http.StatusOK, 1, false},
		{http.MethodGet, "/api/teams/" + private.ID + "/settings", nil, workspace, http.StatusOK, 1, false},
		{http.MethodPatch, "/api/teams/" + private.ID + "/cycle-settings", map[string]any{"cyclesEnabled": false}, workspace, http.StatusOK, 1, false},
		{http.MethodPut, "/api/workspaces/" + workspace + "/teams/" + private.ID + "/members/" + joinerUser.ID, map[string]any{"member": true}, "", http.StatusNoContent, 1, false},
		{http.MethodPut, "/api/workspaces/" + workspace + "/teams/" + private.ID + "/members/" + joinerUser.ID, map[string]any{"member": false}, "", http.StatusNoContent, 1, false},
		{http.MethodPatch, "/api/workspaces/" + workspace + "/teams/" + private.ID, map[string]any{"name": "Race private renamed " + suffix}, "", http.StatusOK, 1, false},
		{http.MethodGet, "/api/teams/" + private.ID + "/members", nil, workspace, http.StatusOK, 1, false},
		{http.MethodGet, "/api/teams/" + private.ID + "/members", nil, workspace, http.StatusOK, 2, false},
		{http.MethodPut, "/api/teams/" + private.ID + "/members/" + joinerUser.ID, map[string]any{"role": "member"}, workspace, http.StatusOK, 2, false},
		{http.MethodDelete, "/api/teams/" + private.ID + "/members/" + joinerUser.ID, nil, workspace, http.StatusOK, 1, true},
		{http.MethodDelete, "/api/teams/" + private.ID + "/members/" + joinerUser.ID, nil, workspace, http.StatusOK, 2, true},
		{http.MethodGet, "/api/teams/" + private.ID + "/resources", nil, workspace, http.StatusOK, 1, false},
	} {
		label := fmt.Sprintf("%s %s (read %d)", item.method, item.path, item.nth)
		rejoin := func() {
			if item.rejoin {
				authRequest[any](t, admin, http.MethodPut, url("/api/workspaces/"+workspace+"/teams/"+private.ID+"/members/"+joinerUser.ID), map[string]any{"member": true}, "", http.StatusNoContent)
			}
		}
		// Without the race the request answers want.
		rejoin()
		if code, raw := send(admin, "", item.method, item.path, item.input, item.workspaceKey); code != item.want {
			t.Fatalf("%s without a concurrent write = %d %s, want %d", label, code, raw, item.want)
		}
		rejoin()
		arm(item.nth, bookkeeping)
		code, raw := send(admin, "", item.method, item.path, item.input, item.workspaceKey)
		disarm(label, item.nth)
		if code != item.want {
			t.Fatalf("%s with a concurrent unrelated write = %d %s, want %d", label, code, raw, item.want)
		}
	}

	// The race does not hide a real revocation. A membership or role change
	// answers as the same request does afterwards, without a race. A key
	// revoked after it authenticated this request is refused with 403; later
	// requests fail authentication (401).
	refused := func(label string, client *http.Client, bearer, method, path string, input any, workspaceKey string, revoke func()) {
		t.Helper()
		arm(1, revoke)
		code, raw := send(client, bearer, method, path, input, workspaceKey)
		disarm(label, 1)
		after, _ := send(client, bearer, method, path, input, workspaceKey)
		if bearer != "" {
			if code != http.StatusForbidden || after != http.StatusUnauthorized {
				t.Fatalf("%s: raced = %d %s, afterwards = %d; want 403, then 401", label, code, raw, after)
			}
			return
		}
		if code < 400 || code >= 500 || code != after {
			t.Fatalf("%s: raced = %d %s, afterwards = %d; want the same refusal", label, code, raw, after)
		}
	}
	// A member removed from the private team.
	authRequest[any](t, member, http.MethodGet, url("/api/teams/"+private.ID+"/settings"), nil, workspace, http.StatusOK)
	refused("member removed from the team", member, "", http.MethodGet, "/api/teams/"+private.ID+"/settings", nil, workspace, func() {
		if err := serving.SetTeamMembership(context.Background(), workspaceID, private.ID, memberUser.ID, "member", false); err != nil {
			t.Errorf("remove member: %v", err)
		}
	})
	// A workspace admin outside the private team, demoted to member.
	authRequest[any](t, deputy, http.MethodGet, url("/api/teams/"+private.ID+"/settings"), nil, workspace, http.StatusOK)
	refused("admin demoted", deputy, "", http.MethodGet, "/api/teams/"+private.ID+"/settings", nil, workspace, func() {
		if err := serving.UpdateMemberRole(context.Background(), workspaceID, deputyUser.ID, "member"); err != nil {
			t.Errorf("demote admin: %v", err)
		}
	})
	// An admin's API key, revoked.
	created := authRequest[struct {
		Secret string        `json:"secret"`
		Key    domain.APIKey `json:"key"`
	}](t, admin, http.MethodPost, url("/api/api-keys"), map[string]any{"name": "Race key " + suffix, "scopes": []string{"read", "write"}}, workspace, http.StatusCreated)
	if code, raw := send(nil, created.Secret, http.MethodGet, "/api/teams/"+private.ID+"/members", nil, workspace); code != http.StatusOK {
		t.Fatalf("key reads the team's members = %d %s", code, raw)
	}
	keyID := created.Key.ID
	refused("key revoked", nil, created.Secret, http.MethodGet, "/api/teams/"+private.ID+"/members", nil, workspace, func() {
		request, _ := http.NewRequest(http.MethodDelete, url("/api/api-keys/"+keyID), nil)
		request.Header.Set("X-Workspace-Key", workspace)
		response, err := admin.Do(request)
		if err != nil {
			t.Errorf("revoke key: %v", err)
			return
		}
		response.Body.Close()
		if response.StatusCode >= 300 {
			t.Errorf("revoke key = %d", response.StatusCode)
		}
	})
}
