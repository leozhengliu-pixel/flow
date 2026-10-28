package main

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Signed in, end to end: people removed from or suspended in the workspace
// stay on the records that name them, but a write cannot newly assign,
// subscribe or lead with them (400 "This person is no longer a member of the
// workspace"), a write that keeps them as they are goes through, and they get
// no new notifications. On SQLite and on the database named by
// FLOW_TEST_DATABASE_DRIVER/URL.
func TestFormerMembersAreNotNewlyAssignedOrNotified(t *testing.T) {
	for name, repo := range favoriteTabTestStores(t) {
		t.Run(name, func(t *testing.T) { testFormerMembersAreNotNewlyAssignedOrNotified(t, repo) })
	}
}

func testFormerMembersAreNotNewlyAssignedOrNotified(t *testing.T, repo *store.SQLiteStore) {
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	server := newAuthServerForStore(t, repo)
	suffix := fmt.Sprint(time.Now().UnixNano())
	admin := peerAdmin(t, server.URL)
	api := func(path string) string { return server.URL + path }
	team := authRequest[domain.Team](t, admin, http.MethodPost, api("/api/workspaces/test-workspace/teams"), map[string]any{"name": "Former People " + suffix, "key": "FP" + suffix[len(suffix)-4:]}, "", http.StatusCreated)
	_, gone := joinByInvitation(t, server.URL, admin, "Gone Person", "gone-"+suffix+"@example.com", "member", team.ID)
	_, paused := joinByInvitation(t, server.URL, admin, "Paused Person", "paused-"+suffix+"@example.com", "member", team.ID)
	_, stays := joinByInvitation(t, server.URL, admin, "Staying Person", "stays-"+suffix+"@example.com", "member", team.ID)

	issue := authRequest[domain.Issue](t, admin, http.MethodPost, api("/api/issues"), map[string]any{"title": "Named before they left", "teamId": team.ID, "assigneeId": gone.ID}, "test-workspace", http.StatusCreated)
	issue = authRequest[domain.Issue](t, admin, http.MethodPatch, api("/api/issues/"+issue.ID), map[string]any{"subscriberIds": []string{gone.ID, paused.ID}}, "test-workspace", http.StatusOK)
	other := authRequest[domain.Issue](t, admin, http.MethodPost, api("/api/issues"), map[string]any{"title": "Nobody yet", "teamId": team.ID}, "test-workspace", http.StatusCreated)
	project := authRequest[domain.Project](t, admin, http.MethodPost, api("/api/projects"), map[string]any{"name": "Former lead " + suffix, "teamIds": []string{team.ID}}, "test-workspace", http.StatusCreated)
	project = authRequest[domain.Project](t, admin, http.MethodPatch, api("/api/projects/"+project.ID), map[string]any{"leadId": paused.ID, "memberIds": []string{gone.ID}}, "test-workspace", http.StatusOK)

	authRequest[map[string]any](t, admin, http.MethodPost, api("/api/workspaces/test-workspace/members/"+paused.ID+"/suspend"), nil, "", http.StatusNoContent)
	authRequest[map[string]any](t, admin, http.MethodDelete, api("/api/workspaces/test-workspace/members/"+gone.ID), nil, "", http.StatusNoContent)

	refused := func(method, path string, body map[string]any) {
		t.Helper()
		result := authRequest[map[string]any](t, admin, method, api(path), body, "test-workspace", http.StatusBadRequest)
		if result["error"] != errFormerMember.Error() {
			t.Fatalf("%s %s %v: error %q", method, path, body, result["error"])
		}
	}
	for _, person := range []domain.User{gone, paused} {
		refused(http.MethodPatch, "/api/issues/"+other.ID, map[string]any{"assigneeId": person.ID})
		refused(http.MethodPatch, "/api/issues/"+other.ID, map[string]any{"subscriberIds": []string{person.ID}})
		refused(http.MethodPost, "/api/issues", map[string]any{"title": "Assigned on create", "teamId": team.ID, "assigneeId": person.ID})
	}
	document := authRequest[domain.Document](t, admin, http.MethodPost, api("/api/documents"), map[string]any{"title": "Former subscribers " + suffix, "subscriberIds": []string{stays.ID}}, "test-workspace", http.StatusCreated)
	refused(http.MethodPatch, "/api/documents/"+document.ID, map[string]any{"subscriberIds": []string{stays.ID, paused.ID}})
	// gone is a member already (kept); paused would be a new one.
	refused(http.MethodPatch, "/api/projects/"+project.ID, map[string]any{"memberIds": []string{gone.ID, paused.ID}})
	refused(http.MethodPatch, "/api/projects/"+project.ID, map[string]any{"leadId": gone.ID})

	// Writes that keep the references they had go through, and history reads them.
	kept := authRequest[domain.Issue](t, admin, http.MethodPatch, api("/api/issues/"+issue.ID), map[string]any{"title": "Renamed after they left", "assigneeId": gone.ID, "subscriberIds": []string{gone.ID, paused.ID, stays.ID}}, "test-workspace", http.StatusOK)
	if kept.Assignee == nil || kept.Assignee.ID != gone.ID || !slices.Contains(kept.SubscriberIDs, paused.ID) || !slices.Contains(kept.SubscriberIDs, stays.ID) {
		t.Fatalf("kept references changed: %+v %v", kept.Assignee, kept.SubscriberIDs)
	}
	keptProject := authRequest[domain.Project](t, admin, http.MethodPatch, api("/api/projects/"+project.ID), map[string]any{"name": "Renamed project " + suffix, "leadId": paused.ID, "memberIds": []string{gone.ID, stays.ID}}, "test-workspace", http.StatusOK)
	if keptProject.Lead == nil || keptProject.Lead.ID != paused.ID || !slices.Equal(keptProject.MemberIDs, []string{gone.ID, stays.ID}) {
		t.Fatalf("kept project references changed: %+v %v", keptProject.Lead, keptProject.MemberIDs)
	}
	read := authRequest[domain.Issue](t, admin, http.MethodGet, api("/api/issue-records/"+issue.ID), nil, "test-workspace", http.StatusOK)
	if read.Assignee == nil || read.Assignee.ID != gone.ID {
		t.Fatalf("a former member's assignment is not readable: %+v", read.Assignee)
	}

	// A comment notifies the subscriber who stays, not the former members.
	notices := func(userID string) int {
		page, err := repo.QueryNotifications(context.Background(), store.NotificationQuery{Workspace: "test-workspace", UserID: userID, IncludeArchived: true, Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, item := range page.Notifications {
			if item.IssueID == issue.ID {
				count += max(item.OccurrenceCount, 1)
			}
		}
		return count
	}
	before := map[string]int{"gone": notices(gone.ID), "paused": notices(paused.ID), "stays": notices(stays.ID)}
	authRequest[domain.Comment](t, admin, http.MethodPost, api("/api/issues/"+issue.ID+"/comments"), map[string]any{"body": "Anyone still here?"}, "test-workspace", http.StatusCreated)
	after := map[string]int{"gone": notices(gone.ID), "paused": notices(paused.ID), "stays": notices(stays.ID)}
	for name, want := range map[string]int{"gone": 0, "paused": 0, "stays": 1} {
		if got := after[name] - before[name]; got != want {
			t.Fatalf("comment notices: %s got %d, want %d (before %v, after %v)", name, got, want, before, after)
		}
	}

	// Resumed, the suspended member can be assigned again.
	authRequest[map[string]any](t, admin, http.MethodPost, api("/api/workspaces/test-workspace/members/"+paused.ID+"/resume"), nil, "", http.StatusNoContent)
	if resumed := authRequest[domain.Issue](t, admin, http.MethodPatch, api("/api/issues/"+other.ID), map[string]any{"assigneeId": paused.ID}, "test-workspace", http.StatusOK); resumed.Assignee == nil || resumed.Assignee.ID != paused.ID {
		t.Fatalf("resumed member not assigned: %+v", resumed.Assignee)
	}
}
