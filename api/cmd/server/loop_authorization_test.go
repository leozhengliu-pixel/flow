package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Loops run as their owner, so editing, publishing, running and reading a
// loop all check the loop's team: a member cannot hand a loop to an admin,
// act in a team they are not in, run a loop on what they cannot see, or read
// a private team's loops and runs.
func TestLoopAuthorizationFollowsTeamAccess(t *testing.T) {
	for _, bounded := range []string{"off", "on"} {
		t.Run("bounded metadata "+bounded, func(t *testing.T) {
			t.Setenv("FLOW_RECORD_HYDRATION", "on")
			runLoopAuthorizationScenario(t, bounded == "on")
		})
	}
}

func runLoopAuthorizationScenario(t *testing.T, bounded bool) {
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "loop-authorization.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	serving := repository
	if bounded {
		if serving, err = repository.BoundedMetadataViewForTesting(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	host := httptest.NewServer(newHandler(&server{store: serving, uploadPath: t.TempDir()}))
	defer host.Close()
	const workspace = "test-workspace"
	url := func(path string) string { return host.URL + path }

	admin := authClient(t)
	adminSession := authRequest[domain.AuthSession](t, admin, http.MethodPost, url("/api/auth/login"), map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	authRequest[domain.WorkspaceSettings](t, admin, http.MethodPatch, url("/api/workspace/preferences"), map[string]any{"featureFlags": map[string]bool{"loops": true}}, workspace, http.StatusOK)
	join := func(name, email string) (*http.Client, domain.User) {
		client, user := verifiedAuthClient(t, host.URL, name, email)
		invitation := authRequest[[]domain.Invitation](t, admin, http.MethodPost, url("/api/workspaces/"+workspace+"/invitations"), map[string]any{"emails": []string{user.Email}, "role": "member"}, "", http.StatusCreated)
		authRequest[domain.WorkspaceMembership](t, client, http.MethodPost, url("/api/invitations/accept"), map[string]string{"token": invitation[0].Token}, "", http.StatusOK)
		return client, user
	}
	insider, insiderUser := join("Insider", "loop-insider@example.com")
	outsider, outsiderUser := join("Outsider", "loop-outsider@example.com")
	bootstrap := authRequest[domain.Bootstrap](t, admin, http.MethodGet, url("/api/bootstrap"), nil, workspace, http.StatusOK)
	adminID := bootstrap.Viewer.ID
	if adminID == "" {
		adminID = adminSession.User.ID
	}
	publicTeam := bootstrap.Teams[0]
	private := authRequest[domain.Team](t, admin, http.MethodPost, url("/api/workspaces/"+workspace+"/teams"), map[string]any{"name": "Loop private", "key": "LPV", "private": true}, "", http.StatusCreated)
	authRequest[any](t, admin, http.MethodPut, url("/api/workspaces/"+workspace+"/teams/"+private.ID+"/members/"+insiderUser.ID), map[string]any{"member": true}, "", http.StatusNoContent)
	authRequest[any](t, admin, http.MethodPut, url("/api/workspaces/"+workspace+"/teams/"+publicTeam.ID+"/members/"+outsiderUser.ID), map[string]any{"member": true}, "", http.StatusNoContent)

	call := func(client *http.Client, method, path string, input any, want int) domain.Loop {
		t.Helper()
		return authRequest[domain.Loop](t, client, method, url(path), input, workspace, want)
	}
	teamLoop := map[string]any{"name": "Private weekly", "level": "team", "teamId": private.ID, "instructions": "Summarize the week.", "status": "published"}

	// Positive: a team member and an admin create team loops.
	mine := call(insider, http.MethodPost, "/api/loops", teamLoop, http.StatusCreated)
	if mine.OwnerID != insiderUser.ID {
		t.Fatalf("owner = %q, want the creator", mine.OwnerID)
	}
	adminLoop := call(admin, http.MethodPost, "/api/loops", teamLoop, http.StatusCreated)

	// Someone outside the team cannot create, edit, publish, duplicate, restore or run in it.
	call(outsider, http.MethodPost, "/api/loops", teamLoop, http.StatusForbidden)
	call(outsider, http.MethodPost, "/api/loops", map[string]any{"name": "Draft", "level": "team", "teamId": private.ID}, http.StatusForbidden)
	// A loop hidden from the caller is not found.
	call(outsider, http.MethodPatch, "/api/loops/"+mine.ID, map[string]any{"name": "Taken over"}, http.StatusNotFound)
	call(outsider, http.MethodPatch, "/api/loops/"+adminLoop.ID, map[string]any{"instructions": "Do something else."}, http.StatusNotFound)
	call(outsider, http.MethodPost, "/api/loops/"+mine.ID+"/duplicate", nil, http.StatusNotFound)
	call(outsider, http.MethodPost, "/api/loops/"+mine.ID+"/versions/loop_v1/restore", nil, http.StatusNotFound)
	authRequest[any](t, outsider, http.MethodPost, url("/api/loops/"+mine.ID+"/runs"), nil, workspace, http.StatusNotFound)
	authRequest[any](t, outsider, http.MethodDelete, url("/api/loops/"+mine.ID), nil, workspace, http.StatusNotFound)
	// Moving a loop into a team the actor is not in is the same refusal.
	own := call(outsider, http.MethodPost, "/api/loops", map[string]any{"name": "Own", "instructions": "Hello."}, http.StatusCreated)
	call(outsider, http.MethodPatch, "/api/loops/"+own.ID, map[string]any{"level": "team", "teamId": private.ID}, http.StatusForbidden)

	// The owner changes only by an admin, or to the actor themselves.
	call(insider, http.MethodPatch, "/api/loops/"+mine.ID, map[string]any{"ownerId": adminID}, http.StatusForbidden)
	call(outsider, http.MethodPatch, "/api/loops/"+own.ID, map[string]any{"ownerId": adminID}, http.StatusForbidden)
	call(outsider, http.MethodPost, "/api/loops", map[string]any{"name": "As admin", "instructions": "Hello.", "ownerId": adminID}, http.StatusForbidden)
	call(outsider, http.MethodPatch, "/api/loops/"+own.ID, map[string]any{"ownerId": outsiderUser.ID, "name": "Own renamed"}, http.StatusOK)
	if got := call(admin, http.MethodPatch, "/api/loops/"+mine.ID, map[string]any{"ownerId": insiderUser.ID, "name": "Weekly"}, http.StatusOK); got.OwnerID != insiderUser.ID {
		t.Fatalf("admin edit changed the owner to %q", got.OwnerID)
	}
	if got := call(admin, http.MethodPatch, "/api/loops/"+mine.ID, map[string]any{"ownerId": adminID}, http.StatusOK); got.OwnerID != adminID {
		t.Fatalf("admin could not set the owner, got %q", got.OwnerID)
	}
	call(admin, http.MethodPatch, "/api/loops/"+mine.ID, map[string]any{"ownerId": insiderUser.ID}, http.StatusOK)

	// Team members edit and run their team's loop.
	call(insider, http.MethodPatch, "/api/loops/"+mine.ID, map[string]any{"name": "Weekly again"}, http.StatusOK)
	call(insider, http.MethodPost, "/api/loops/"+mine.ID+"/duplicate", nil, http.StatusCreated)
	if code := authRequestStatus(t, insider, http.MethodPost, url("/api/loops/"+mine.ID+"/runs"), nil, workspace); code == http.StatusForbidden || code == http.StatusNotFound {
		t.Fatalf("team member manual run = %d", code)
	}

	// Reading: a private team's loops, runs and events are hidden from people outside it.
	ids := func(client *http.Client) []string {
		t.Helper()
		var found []string
		for _, loop := range authRequest[[]domain.Loop](t, client, http.MethodGet, url("/api/loops"), nil, workspace, http.StatusOK) {
			found = append(found, loop.ID)
		}
		return found
	}
	bootstrapIDs := func(client *http.Client) []string {
		t.Helper()
		var found []string
		for _, loop := range authRequest[domain.Bootstrap](t, client, http.MethodGet, url("/api/bootstrap"), nil, workspace, http.StatusOK).Loops {
			found = append(found, loop.ID)
		}
		return found
	}
	for name, client := range map[string]*http.Client{"insider": insider, "admin": admin} {
		if !slices.Contains(ids(client), mine.ID) || !slices.Contains(bootstrapIDs(client), mine.ID) {
			t.Fatalf("%s does not see the private team's loop", name)
		}
		call(client, http.MethodGet, "/api/loops/"+mine.ID, nil, http.StatusOK)
	}
	if slices.Contains(ids(outsider), mine.ID) || slices.Contains(bootstrapIDs(outsider), mine.ID) {
		t.Fatal("a non-member lists the private team's loop")
	}
	if !slices.Contains(ids(outsider), own.ID) {
		t.Fatal("a workspace loop is hidden from its owner")
	}
	call(outsider, http.MethodGet, "/api/loops/"+mine.ID, nil, http.StatusNotFound)
	authRequest[any](t, outsider, http.MethodGet, url("/api/loops/"+mine.ID+"/runs"), nil, workspace, http.StatusNotFound)
	authRequest[any](t, outsider, http.MethodGet, url("/api/loops/"+mine.ID+"/runs/nope"), nil, workspace, http.StatusNotFound)
	authRequest[any](t, outsider, http.MethodGet, url("/api/loops/"+mine.ID+"/runs/nope/events"), nil, workspace, http.StatusNotFound)
	authRequest[any](t, outsider, http.MethodGet, url("/api/loops/"+mine.ID+"/versions"), nil, workspace, http.StatusNotFound)
	// Workspace loops stay visible to every member.
	workspaceLoop := call(admin, http.MethodPost, "/api/loops", map[string]any{"name": "Everyone", "instructions": "Hello.", "status": "published"}, http.StatusCreated)
	call(outsider, http.MethodGet, "/api/loops/"+workspaceLoop.ID, nil, http.StatusOK)

	// The team's loop permission: with "owners" a plain team member can no longer edit.
	authRequest[any](t, admin, http.MethodPatch, url("/api/teams/"+private.ID+"/settings"), map[string]any{"loopPermission": "owners"}, workspace, http.StatusOK)
	call(insider, http.MethodPatch, "/api/loops/"+mine.ID, map[string]any{"name": "Blocked"}, http.StatusForbidden)
	authRequest[any](t, insider, http.MethodPost, url("/api/loops/"+mine.ID+"/runs"), nil, workspace, http.StatusForbidden)
	call(admin, http.MethodPatch, "/api/loops/"+mine.ID, map[string]any{"name": "Admin still edits"}, http.StatusOK)

	// A manual run starts on what the caller can see: an event loop run on an issue of a team the caller is not in.
	privateIssue := authRequest[domain.Issue](t, admin, http.MethodPost, url("/api/issues"), map[string]any{"title": "Secret", "teamId": private.ID}, workspace, http.StatusCreated)
	publicIssue := authRequest[domain.Issue](t, admin, http.MethodPost, url("/api/issues"), map[string]any{"title": "Open", "teamId": publicTeam.ID}, workspace, http.StatusCreated)
	eventLoop := call(outsider, http.MethodPost, "/api/loops", map[string]any{"name": "On issues", "instructions": "Review it.", "triggerType": "issue", "status": "published"}, http.StatusCreated)
	if code := authRequestStatus(t, outsider, http.MethodPost, url("/api/loops/"+eventLoop.ID+"/runs"), map[string]any{"entityType": "issue", "entityId": privateIssue.ID}, workspace); code != http.StatusNotFound && code != http.StatusForbidden {
		t.Fatalf("run on an issue the caller cannot see = %d, want 403 or 404", code)
	}
	if code := authRequestStatus(t, outsider, http.MethodPost, url("/api/loops/"+eventLoop.ID+"/runs"), map[string]any{"entityType": "issue", "entityId": publicIssue.ID}, workspace); code == http.StatusForbidden || code == http.StatusNotFound {
		t.Fatalf("run on a visible issue = %d", code)
	}

	// A member's edit of an admin-owned workspace loop bounds its run to what the member can read too.
	adminOwned := call(admin, http.MethodPost, "/api/loops", map[string]any{"name": "Admin owned", "instructions": "Report.", "status": "published"}, http.StatusCreated)
	edited := call(outsider, http.MethodPatch, "/api/loops/"+adminOwned.ID, map[string]any{"instructions": "Report everything."}, http.StatusOK)
	if edited.BehaviorEditorID != outsiderUser.ID {
		t.Fatalf("behavior editor = %q, want the member", edited.BehaviorEditorID)
	}
	if renamed := call(outsider, http.MethodPatch, "/api/loops/"+adminOwned.ID, map[string]any{"name": "Admin owned 2"}, http.StatusOK); renamed.BehaviorEditorID != outsiderUser.ID {
		t.Fatal("a rename cleared the behavior editor")
	}
	// The run's view of the owner is read through the store that serves the requests.
	ownerView := func(userID string) domain.Bootstrap {
		t.Helper()
		data, _, err := serving.IssueQueryAccess(context.Background(), workspace, userID)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	restrict := func(loop domain.Loop) (domain.Bootstrap, error) {
		data := ownerView(adminID)
		err := (&server{store: serving}).restrictToBehaviorEditor(context.Background(), workspace, &data, loop, data.Viewer)
		return data, err
	}
	narrowed, err := restrict(edited)
	if err != nil {
		t.Fatal(err)
	}
	for _, team := range narrowed.Teams {
		if team.ID == private.ID {
			t.Fatal("a run edited by a member can read a private team only the admin is in")
		}
	}
	if !slices.ContainsFunc(narrowed.Teams, func(team domain.Team) bool { return team.ID == publicTeam.ID }) {
		t.Fatal("the shared team was removed from the run")
	}
	if _, err := restrict(domain.Loop{TeamID: private.ID, BehaviorEditorID: outsiderUser.ID}); err == nil {
		t.Fatal("a team loop edited by someone outside its team is runnable")
	}
	if resaved := call(admin, http.MethodPatch, "/api/loops/"+adminOwned.ID, map[string]any{"name": "Admin owned 3"}, http.StatusOK); resaved.BehaviorEditorID != "" {
		t.Fatalf("an admin's save kept the behavior editor %q", resaved.BehaviorEditorID)
	}
	if full, err := restrict(domain.Loop{}); err != nil || !slices.ContainsFunc(full.Teams, func(team domain.Team) bool { return team.ID == private.ID }) {
		t.Fatalf("a loop without a behavior editor lost the admin's teams: %v", err)
	}

	// A scheduled run re-checks its owner: after leaving the team the loop is unavailable.
	s := &server{store: serving}
	asOwner := func() error {
		data := ownerView(insiderUser.ID)
		return s.checkLoopOwnerForRun(&data, domain.Loop{TeamID: private.ID}, insiderUser)
	}
	authRequest[any](t, admin, http.MethodPatch, url("/api/teams/"+private.ID+"/settings"), map[string]any{"loopPermission": "allMembers"}, workspace, http.StatusOK)
	if err := asOwner(); err != nil {
		t.Fatalf("a team member's loop is unavailable: %v", err)
	}
	authRequest[any](t, admin, http.MethodPut, url("/api/workspaces/"+workspace+"/teams/"+private.ID+"/members/"+insiderUser.ID), map[string]any{"member": false}, "", http.StatusNoContent)
	if err := asOwner(); err == nil {
		t.Fatal("a loop whose owner left the team is still runnable")
	}
}
