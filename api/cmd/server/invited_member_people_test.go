package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// joinByInvitation signs a new person up, has the admin invite them to the
// test workspace with the given teams and accepts the invitation.
func joinByInvitation(t *testing.T, baseURL string, admin *http.Client, name, email, role string, teams ...string) (*http.Client, domain.User) {
	t.Helper()
	client, user := verifiedAuthClient(t, baseURL, name, email)
	invitations := authRequest[[]domain.Invitation](t, admin, http.MethodPost, baseURL+"/api/workspaces/test-workspace/invitations", map[string]any{"emails": []string{user.Email}, "role": role, "teamIds": teams}, "", http.StatusCreated)
	authRequest[domain.WorkspaceMembership](t, client, http.MethodPost, baseURL+"/api/invitations/accept", map[string]string{"token": invitations[0].Token}, "", http.StatusOK)
	return client, user
}

func newAuthServerForStore(t *testing.T, repo *store.SQLiteStore) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(newHandler(&server{store: repo, uploadPath: t.TempDir()}))
	t.Cleanup(server.Close)
	return server
}

// A person who joined through an invitation is a workspace member at once,
// before they change anything themselves: they can be made an issue's
// assignee and subscriber or a project's lead and member, and their own
// updates go through. On SQLite and on the database named by
// FLOW_TEST_DATABASE_DRIVER/URL.
func TestInvitedMemberCanBeAssignedAndSubscribed(t *testing.T) {
	for name, repo := range favoriteTabTestStores(t) {
		t.Run(name, func(t *testing.T) { testInvitedMemberCanBeAssignedAndSubscribed(t, repo) })
	}
}

func testInvitedMemberCanBeAssignedAndSubscribed(t *testing.T, repo *store.SQLiteStore) {
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	server := newAuthServerForStore(t, repo)
	suffix := fmt.Sprint(time.Now().UnixNano())
	admin := peerAdmin(t, server.URL)
	team := authRequest[domain.Team](t, admin, http.MethodPost, server.URL+"/api/workspaces/test-workspace/teams", map[string]any{"name": "Invited People " + suffix, "key": "IP" + suffix[len(suffix)-4:]}, "", http.StatusCreated)
	issue := authRequest[domain.Issue](t, admin, http.MethodPost, server.URL+"/api/issues", map[string]any{"title": "Before the invitation", "teamId": team.ID}, "test-workspace", http.StatusCreated)
	member, person := joinByInvitation(t, server.URL, admin, "Invited Person", "invited-person-"+suffix+"@example.com", "member", team.ID)

	subscribed := authRequest[domain.Issue](t, admin, http.MethodPatch, server.URL+"/api/issues/"+issue.ID, map[string]any{"subscriberIds": append(slices.Clone(issue.SubscriberIDs), person.ID)}, "test-workspace", http.StatusOK)
	if !slices.Contains(subscribed.SubscriberIDs, person.ID) {
		t.Fatalf("invited member not subscribed: %v", subscribed.SubscriberIDs)
	}
	assigned := authRequest[domain.Issue](t, admin, http.MethodPatch, server.URL+"/api/issues/"+issue.ID, map[string]any{"assigneeId": person.ID}, "test-workspace", http.StatusOK)
	if assigned.Assignee == nil || assigned.Assignee.ID != person.ID {
		t.Fatalf("invited member not assigned: %+v", assigned.Assignee)
	}
	created := authRequest[domain.Issue](t, admin, http.MethodPost, server.URL+"/api/issues", map[string]any{"title": "Created for them", "teamId": team.ID, "assigneeId": person.ID}, "test-workspace", http.StatusCreated)
	if created.Assignee == nil || created.Assignee.ID != person.ID {
		t.Fatalf("invited member not assigned on create: %+v", created.Assignee)
	}
	project := authRequest[domain.Project](t, admin, http.MethodPost, server.URL+"/api/projects", map[string]any{"name": "Invited lead " + suffix, "teamIds": []string{team.ID}}, "test-workspace", http.StatusCreated)
	led := authRequest[domain.Project](t, admin, http.MethodPatch, server.URL+"/api/projects/"+project.ID, map[string]any{"leadId": person.ID, "memberIds": []string{person.ID}}, "test-workspace", http.StatusOK)
	if led.Lead == nil || led.Lead.ID != person.ID || !slices.Contains(led.MemberIDs, person.ID) {
		t.Fatalf("invited member not the project's lead and member: %+v %v", led.Lead, led.MemberIDs)
	}

	// Being assigned notified them.
	inbox := authRequest[domain.NotificationList](t, member, http.MethodGet, server.URL+"/api/notifications", nil, "test-workspace", http.StatusOK)
	if !slices.ContainsFunc(inbox.Notifications, func(item domain.Notification) bool { return item.IssueID == issue.ID && item.Category == "assignments" }) {
		t.Fatalf("invited member was not notified of the assignment: %+v", inbox.Notifications)
	}

	// The invited member edits the issue, subscribers and all.
	renamed := authRequest[domain.Issue](t, member, http.MethodPatch, server.URL+"/api/issues/"+issue.ID, map[string]any{"title": "Renamed by the invitee", "subscriberIds": subscribed.SubscriberIDs}, "test-workspace", http.StatusOK)
	if renamed.Title != "Renamed by the invitee" {
		t.Fatalf("invitee update not applied: %q", renamed.Title)
	}
	authRequest[domain.Issue](t, member, http.MethodPatch, server.URL+"/api/issues/"+created.ID, map[string]any{"assigneeId": ""}, "test-workspace", http.StatusOK)
}

// With two servers on one database, a member who accepted on one server can
// be subscribed through the other at once.
func TestPeerServerSubscribesMemberWhoJoinedOnAnotherServer(t *testing.T) {
	peers := newPeerServers(t)
	admin := peerAdmin(t, peers.second.URL)
	issue := authRequest[domain.Issue](t, admin, http.MethodPost, peers.second.URL+"/api/issues", map[string]any{"title": "Across servers"}, "test-workspace", http.StatusCreated)
	inviter := peerAdmin(t, peers.first.URL)
	_, person := joinByInvitation(t, peers.first.URL, inviter, "Peer Invitee", "peer-invitee@example.com", "member")
	subscribed := authRequest[domain.Issue](t, admin, http.MethodPatch, peers.second.URL+"/api/issues/"+issue.ID, map[string]any{"subscriberIds": []string{person.ID}, "assigneeId": person.ID}, "test-workspace", http.StatusOK)
	if !slices.Contains(subscribed.SubscriberIDs, person.ID) || subscribed.Assignee == nil || subscribed.Assignee.ID != person.ID {
		t.Fatalf("member who joined on the other server not subscribed and assigned: %v %+v", subscribed.SubscriberIDs, subscribed.Assignee)
	}
}

// Signed in, end to end: invited members subscribed to an issue hear about
// its move to another team only if they can open it there. A member outside
// the private team it moves to is not told (and neither is a guest), members
// of it are; a later move to a public team reaches every member subscriber
// but not a guest who is not on that team. On SQLite and on the database named
// by FLOW_TEST_DATABASE_DRIVER/URL.
func TestIssueMoveNotifiesInvitedSubscribersWhoCanOpenIt(t *testing.T) {
	for name, repo := range favoriteTabTestStores(t) {
		t.Run(name, func(t *testing.T) { testIssueMoveNotifiesInvitedSubscribers(t, repo) })
	}
}

func testIssueMoveNotifiesInvitedSubscribers(t *testing.T, repo *store.SQLiteStore) {
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	server := newAuthServerForStore(t, repo)
	suffix := fmt.Sprint(time.Now().UnixNano())
	key := func(prefix string) string { return prefix + suffix[len(suffix)-5:] }
	admin := peerAdmin(t, server.URL)
	base := server.URL + "/api/workspaces/test-workspace/teams"
	home := authRequest[domain.Team](t, admin, http.MethodPost, base, map[string]any{"name": "Move Home " + suffix, "key": key("MH")}, "", http.StatusCreated)
	private := authRequest[domain.Team](t, admin, http.MethodPost, base, map[string]any{"name": "Move Private " + suffix, "key": key("MV"), "private": true}, "", http.StatusCreated)
	public := authRequest[domain.Team](t, admin, http.MethodPost, base, map[string]any{"name": "Move Public " + suffix, "key": key("MP")}, "", http.StatusCreated)
	insiderClient, insider := joinByInvitation(t, server.URL, admin, "Move Insider", "move-insider-"+suffix+"@example.com", "member", home.ID, private.ID)
	outsiderClient, outsider := joinByInvitation(t, server.URL, admin, "Move Outsider", "move-outsider-"+suffix+"@example.com", "member", home.ID)
	guestClient, guest := joinByInvitation(t, server.URL, admin, "Move Guest", "move-guest-"+suffix+"@example.com", "guest", home.ID)

	issue := authRequest[domain.Issue](t, admin, http.MethodPost, server.URL+"/api/issues", map[string]any{"title": "Moving around", "teamId": home.ID}, "test-workspace", http.StatusCreated)
	issue = authRequest[domain.Issue](t, admin, http.MethodPatch, server.URL+"/api/issues/"+issue.ID, map[string]any{"subscriberIds": []string{insider.ID, outsider.ID, guest.ID}}, "test-workspace", http.StatusOK)
	for _, id := range []string{insider.ID, outsider.ID, guest.ID} {
		if !slices.Contains(issue.SubscriberIDs, id) {
			t.Fatalf("invited member %s not subscribed: %v", id, issue.SubscriberIDs)
		}
	}
	clients := map[string]*http.Client{insider.ID: insiderClient, outsider.ID: outsiderClient, guest.ID: guestClient}
	names := map[string]string{insider.ID: "insider", outsider.ID: "outsider", guest.ID: "guest"}
	// moveNotices counts the move notices each subscriber has for the issue
	// (occurrences, as notices of one issue are grouped).
	moveNotices := func() map[string]int {
		counts := map[string]int{}
		for id, client := range clients {
			inbox := authRequest[domain.NotificationList](t, client, http.MethodGet, server.URL+"/api/notifications?includeArchived=true", nil, "test-workspace", http.StatusOK)
			for _, item := range inbox.Notifications {
				if item.IssueID == issue.ID {
					counts[names[id]] += max(item.OccurrenceCount, 1)
				}
			}
		}
		return counts
	}
	before := moveNotices()

	moved := authRequest[domain.Issue](t, admin, http.MethodPatch, server.URL+"/api/issues/"+issue.ID, map[string]any{"teamId": private.ID}, "test-workspace", http.StatusOK)
	if moved.Team.ID != private.ID {
		t.Fatalf("issue not moved: %s", moved.Team.ID)
	}
	afterPrivate := moveNotices()
	for name, want := range map[string]int{"insider": 1, "outsider": 0, "guest": 0} {
		if got := afterPrivate[name] - before[name]; got != want {
			t.Fatalf("move to the private team: %s got %d notices, want %d (before %v, after %v)", name, got, want, before, afterPrivate)
		}
	}

	authRequest[domain.Issue](t, admin, http.MethodPatch, server.URL+"/api/issues/"+issue.ID, map[string]any{"teamId": public.ID}, "test-workspace", http.StatusOK)
	afterPublic := moveNotices()
	for name, want := range map[string]int{"insider": 1, "outsider": 1, "guest": 0} {
		if got := afterPublic[name] - afterPrivate[name]; got != want {
			t.Fatalf("move to the public team: %s got %d notices, want %d (before %v, after %v)", name, got, want, afterPrivate, afterPublic)
		}
	}
}
