package main

import (
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Every route under an issue names it by its id, its identifier or an
// identifier it had before it moved to another team.
func TestIssueRoutesResolveCurrentAndPreviousIdentifiers(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, authDisabled: true, uploadPath: t.TempDir()})
	target := requestJSON[domain.Team](t, handler, http.MethodPost, "/api/workspaces/test-workspace/teams", map[string]any{"name": "Route Target", "key": "RTE"}, http.StatusCreated)
	issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Routed"}, http.StatusCreated)
	other := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Related"}, http.StatusCreated)
	oldIdentifier := issue.Identifier
	moved := requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+issue.ID, map[string]any{"teamId": target.ID}, http.StatusOK)
	if moved.Identifier == oldIdentifier {
		t.Fatalf("issue did not move: %s", moved.Identifier)
	}

	for index, reference := range []string{oldIdentifier, strings.ToLower(oldIdentifier), moved.Identifier} {
		for _, prefix := range []string{"/api/issues/", "/api/issue-records/"} {
			base := prefix + reference
			comment := requestJSON[domain.Comment](t, handler, http.MethodPost, base+"/comments", map[string]any{"body": "via " + base}, http.StatusCreated)
			edited := requestJSON[domain.Comment](t, handler, http.MethodPatch, base+"/comments/"+comment.ID, map[string]any{"body": "edited via " + base}, http.StatusOK)
			if edited.ID != comment.ID {
				t.Fatalf("PATCH %s comment = %+v", base, edited)
			}
			requestJSON[any](t, handler, http.MethodPost, base+"/comments/"+comment.ID+"/reactions", map[string]any{"emoji": "👍"}, http.StatusOK)
			requestJSON[any](t, handler, http.MethodPut, base+"/comments/"+comment.ID+"/subscription", map[string]any{"state": "subscribed"}, http.StatusOK)
			requestJSON[any](t, handler, http.MethodPost, base+"/reactions", map[string]any{"emoji": "🎉"}, http.StatusOK)
			link := requestJSON[domain.Attachment](t, handler, http.MethodPost, base+"/links", map[string]any{"url": "https://example.com/" + strings.Trim(prefix, "/") + "/" + reference}, http.StatusCreated)
			if link.IssueID != issue.ID {
				t.Fatalf("POST %s/links attached to %q", base, link.IssueID)
			}
			requestJSON[any](t, handler, http.MethodDelete, base+"/attachments/"+link.ID, nil, http.StatusNoContent)
			requestJSON[any](t, handler, http.MethodDelete, base+"/comments/"+comment.ID, nil, http.StatusNoContent)
			if index == 0 {
				relation := requestJSON[domain.IssueRelation](t, handler, http.MethodPost, base+"/relations", map[string]any{"type": "related", "relatedIssueId": other.ID}, http.StatusCreated)
				if relation.IssueID != issue.ID {
					t.Fatalf("POST %s/relations related %q", base, relation.IssueID)
				}
				requestJSON[any](t, handler, http.MethodDelete, base+"/relations/"+relation.ID, nil, http.StatusNoContent)
			}
			permissions := requestJSON[[]domain.IssuePermission](t, handler, http.MethodPut, base+"/permissions", map[string]any{"permissions": []map[string]string{{"subjectType": "workspace", "role": "viewer"}}}, http.StatusOK)
			if len(permissions) != 1 || permissions[0].IssueID != issue.ID {
				t.Fatalf("PUT %s/permissions = %+v", base, permissions)
			}
			if listed := requestJSON[[]domain.IssuePermission](t, handler, http.MethodGet, base+"/permissions", nil, http.StatusOK); len(listed) != 1 {
				t.Fatalf("GET %s/permissions = %+v", base, listed)
			}
		}
		history := requestJSON[store.IssueHistory](t, handler, http.MethodGet, "/api/issue-records/"+reference+"/history", nil, http.StatusOK)
		if !slices.ContainsFunc(history.Activities, func(activity domain.ActivityEvent) bool { return activity.Type == "issue.link_created" }) {
			t.Fatalf("history by %s has no links", reference)
		}
	}
	stored := requestJSON[domain.Issue](t, handler, http.MethodGet, "/api/issue-records/"+issue.ID, nil, http.StatusOK)
	if len(stored.Attachments) != 0 || stored.Identifier != moved.Identifier {
		t.Fatalf("issue after the routes = %s, %d attachments", stored.Identifier, len(stored.Attachments))
	}
	// An identifier no issue has stays unknown.
	requestJSON[any](t, handler, http.MethodPost, "/api/issues/NOPE-424242/comments", map[string]any{"body": "lost"}, http.StatusNotFound)
}

func TestIssuePathReference(t *testing.T) {
	for path, want := range map[string][3]string{
		"/api/issues/ENG-1":                          {"/api/issues/", "ENG-1", ""},
		"/api/issues/ENG-1/comments/c1/reactions":    {"/api/issues/", "ENG-1", "/comments/c1/reactions"},
		"/api/issue-records/eng-12/permissions/p1":   {"/api/issue-records/", "eng-12", "/permissions/p1"},
		"/api/issue-records/issue_1/attachments/a/b": {"/api/issue-records/", "issue_1", "/attachments/a/b"},
	} {
		prefix, reference, rest, ok := issuePathReference(path)
		if !ok || [3]string{prefix, reference, rest} != want {
			t.Fatalf("issuePathReference(%q) = %q %q %q %v", path, prefix, reference, rest, ok)
		}
	}
	if _, _, _, ok := issuePathReference("/api/issues"); ok {
		t.Fatal("the collection path names no issue")
	}
}

// Signed in, the old identifier resolves only for members who can read the
// issue.
func TestIssueMoveIdentifiersFollowVisibility(t *testing.T) {
	server := newSingleAuthServer(t)
	admin := peerAdmin(t, server.URL)
	home := authRequest[domain.Team](t, admin, http.MethodPost, server.URL+"/api/workspaces/test-workspace/teams", map[string]any{"name": "Alias Home", "key": "AHOM"}, "", http.StatusCreated)
	private := authRequest[domain.Team](t, admin, http.MethodPost, server.URL+"/api/workspaces/test-workspace/teams", map[string]any{"name": "Alias Private", "key": "APRV", "private": true}, "", http.StatusCreated)
	join := func(name, email string, teams ...string) (*http.Client, domain.User) {
		client, user := verifiedAuthClient(t, server.URL, name, email)
		invitations := authRequest[[]domain.Invitation](t, admin, http.MethodPost, server.URL+"/api/workspaces/test-workspace/invitations", map[string]any{"emails": []string{user.Email}, "role": "member", "teamIds": teams}, "", http.StatusCreated)
		authRequest[domain.WorkspaceMembership](t, client, http.MethodPost, server.URL+"/api/invitations/accept", map[string]string{"token": invitations[0].Token}, "", http.StatusOK)
		return client, user
	}
	outsider, _ := join("Alias Outsider", "alias-outsider@example.com", home.ID)
	insider, _ := join("Alias Insider", "alias-insider@example.com", home.ID, private.ID)
	issue := authRequest[domain.Issue](t, admin, http.MethodPost, server.URL+"/api/issues", map[string]any{"title": "Moving away", "teamId": home.ID}, "test-workspace", http.StatusCreated)
	moved := authRequest[domain.Issue](t, admin, http.MethodPatch, server.URL+"/api/issues/"+issue.Identifier, map[string]any{"teamId": private.ID}, "test-workspace", http.StatusOK)
	if moved.Team.ID != private.ID {
		t.Fatalf("issue not moved: %s", moved.Team.ID)
	}

	// The insider reaches it by the old identifier; the outsider does not.
	authRequest[domain.Comment](t, insider, http.MethodPost, server.URL+"/api/issues/"+issue.Identifier+"/comments", map[string]any{"body": "still here"}, "test-workspace", http.StatusCreated)
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/api/issues/"+issue.Identifier+"/comments", strings.NewReader(`{"body":"lost"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Workspace-Key", "test-workspace")
	response, err := outsider.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden && response.StatusCode != http.StatusNotFound {
		t.Fatalf("outsider comment by the old identifier = %d", response.StatusCode)
	}

	// The old identifier keeps naming it in the insider's reads too.
	if found := authRequest[domain.Issue](t, insider, http.MethodGet, server.URL+"/api/issue-records/"+issue.Identifier, nil, "test-workspace", http.StatusOK); found.ID != issue.ID {
		t.Fatalf("insider read by the old identifier = %s", found.ID)
	}
}

// The notification of a move reaches the subscribers who can open the issue
// in its new team: members of the private team and holders of a grant on the
// issue or an ancestor; not members outside it, guests, or an admin who has
// not joined the private team (admins manage it without reading its issues).
func TestIssueMoveNotifiesOnlySubscribersWhoCanSeeTheNewTeam(t *testing.T) {
	user := func(id string) domain.User {
		return domain.User{ID: id, Name: id, DisplayName: id, Email: id + "@example.com", Active: true}
	}
	actor, insider, outsider, granted, inherited, admin, guest := user("usr_actor"), user("usr_insider"), user("usr_outsider"), user("usr_granted"), user("usr_inherited"), user("usr_admin_viewer"), user("usr_guest")
	public := domain.Team{ID: "team_public", Key: "PUB", Name: "Public"}
	private := domain.Team{ID: "team_private", Key: "PRV", Name: "Private", Private: true}
	parentID := "issue_parent"
	parent := domain.Issue{ID: parentID, Identifier: "PRV-1", Team: private, Permissions: []domain.IssuePermission{{SubjectType: "user", SubjectID: inherited.ID, Role: "viewer"}}}
	issue := domain.Issue{ID: "issue_moved", Identifier: "PRV-2", Team: private, ParentID: &parentID, Creator: actor,
		SubscriberIDs: []string{insider.ID, outsider.ID, granted.ID, inherited.ID, admin.ID, guest.ID},
		Permissions:   []domain.IssuePermission{{SubjectType: "user", SubjectID: granted.ID, Role: "commenter"}}}
	members := []domain.WorkspaceMember{}
	for _, item := range []struct {
		user domain.User
		role string
	}{{actor, "member"}, {insider, "member"}, {outsider, "member"}, {granted, "member"}, {inherited, "member"}, {admin, "admin"}, {guest, "guest"}} {
		members = append(members, domain.WorkspaceMember{User: item.user, Role: item.role, Status: "active"})
	}
	data := &domain.Bootstrap{
		Users:        []domain.User{actor, insider, outsider, granted, inherited, admin, guest},
		Members:      members,
		Teams:        []domain.Team{public, private},
		TeamMembers:  []domain.TeamMember{{TeamID: private.ID, UserID: insider.ID, Role: "member"}, {TeamID: public.ID, UserID: outsider.ID, Role: "member"}, {TeamID: public.ID, UserID: guest.ID, Role: "member"}, {TeamID: private.ID, UserID: actor.ID, Role: "owner"}},
		TeamSettings: map[string]domain.TeamSettings{public.ID: {}, private.ID: {Access: "private"}},
		Issues:       []domain.Issue{parent, issue},
	}
	activity := domain.ActivityEvent{ID: "activity_move", Type: "issue.updated", Actor: actor, CreatedAt: time.Now().UTC(), Metadata: map[string]string{"teamBefore": public.ID, "team": private.ID, "identifierBefore": "PUB-2", "identifier": "PRV-2"}}
	appendIssueNotifications(data, issue, activity, nil)
	notified := []string{}
	for _, notification := range data.Notifications {
		notified = append(notified, notification.RecipientID)
	}
	slices.Sort(notified)
	if want := []string{granted.ID, inherited.ID, insider.ID}; !slices.Equal(notified, sortedCopy(want)) {
		t.Fatalf("move notified %v, want %v", notified, sortedCopy(want))
	}
	// Other updates are not filtered this way.
	data.Notifications = nil
	appendIssueNotifications(data, issue, domain.ActivityEvent{ID: "activity_title", Type: "issue.updated", Actor: actor, CreatedAt: time.Now().UTC(), Metadata: map[string]string{"title": "Renamed"}}, nil)
	if len(data.Notifications) != len(issue.SubscriberIDs) {
		t.Fatalf("a title change notified %d of %d subscribers", len(data.Notifications), len(issue.SubscriberIDs))
	}
}
