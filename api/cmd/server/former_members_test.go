package main

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
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

// mentioningDocument is an editor document that mentions the given people.
func mentioningDocument(text string, userIDs ...string) map[string]any {
	content := []any{map[string]any{"type": "text", "text": text}}
	for _, id := range userIDs {
		content = append(content, map[string]any{"type": "mention", "attrs": map[string]any{"id": id, "mentionType": "user", "label": id}})
	}
	return map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph", "content": content}}}
}

// Signed in, end to end: a mention of someone who was removed from or
// suspended in the workspace stays in the text it was written in, but new
// content that mentions them is refused (400), and suspending, removing and
// resuming a member sends the realtime notice open sessions refresh their
// people on. On SQLite and on the database named by
// FLOW_TEST_DATABASE_DRIVER/URL.
func TestFormerMembersAreNotNewlyMentioned(t *testing.T) {
	for name, repo := range favoriteTabTestStores(t) {
		t.Run(name, func(t *testing.T) { testFormerMembersAreNotNewlyMentioned(t, repo) })
	}
}

func testFormerMembersAreNotNewlyMentioned(t *testing.T, repo *store.SQLiteStore) {
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	server := newAuthServerForStore(t, repo)
	// The member notices are what this test reads from the realtime channel.
	var mu sync.Mutex
	notices := []domain.RealtimeEvent{}
	repo.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
		if strings.HasPrefix(event.Type, "workspace_member.") {
			mu.Lock()
			notices = append(notices, event)
			mu.Unlock()
		}
	})
	t.Cleanup(func() { repo.SetRealtimeSink(nil) })
	noticed := func(eventType, userID string) bool {
		mu.Lock()
		defer mu.Unlock()
		return slices.ContainsFunc(notices, func(event domain.RealtimeEvent) bool {
			return event.Type == eventType && event.AggregateID == userID && strings.Contains(string(event.Payload), userID)
		})
	}
	suffix := fmt.Sprint(time.Now().UnixNano())
	admin := peerAdmin(t, server.URL)
	api := func(path string) string { return server.URL + path }
	team := authRequest[domain.Team](t, admin, http.MethodPost, api("/api/workspaces/test-workspace/teams"), map[string]any{"name": "Mentions " + suffix, "key": "MN" + suffix[len(suffix)-4:]}, "", http.StatusCreated)
	_, gone := joinByInvitation(t, server.URL, admin, "Gone Mention", "gone-mention-"+suffix+"@example.com", "member", team.ID)
	_, paused := joinByInvitation(t, server.URL, admin, "Paused Mention", "paused-mention-"+suffix+"@example.com", "member", team.ID)
	_, stays := joinByInvitation(t, server.URL, admin, "Staying Mention", "stays-mention-"+suffix+"@example.com", "member", team.ID)

	issue := authRequest[domain.Issue](t, admin, http.MethodPost, api("/api/issues"), map[string]any{"title": "Mentions before they left", "teamId": team.ID}, "test-workspace", http.StatusCreated)
	written := mentioningDocument("Ping ", gone.ID, paused.ID)
	comment := authRequest[domain.Comment](t, admin, http.MethodPost, api("/api/issues/"+issue.ID+"/comments"), map[string]any{"body": "Ping", "bodyData": written}, "test-workspace", http.StatusCreated)
	document := authRequest[domain.Document](t, admin, http.MethodPost, api("/api/documents"), map[string]any{"title": "Mentions " + suffix, "content": "Ping", "contentData": written}, "test-workspace", http.StatusCreated)

	authRequest[map[string]any](t, admin, http.MethodPost, api("/api/workspaces/test-workspace/members/"+paused.ID+"/suspend"), nil, "", http.StatusNoContent)
	authRequest[map[string]any](t, admin, http.MethodDelete, api("/api/workspaces/test-workspace/members/"+gone.ID), nil, "", http.StatusNoContent)
	if !noticed("workspace_member.suspended", paused.ID) || !noticed("workspace_member.removed", gone.ID) {
		t.Fatalf("suspend and remove sent no member notice: %+v", notices)
	}

	refused := func(method, path string, body map[string]any) {
		t.Helper()
		result := authRequest[map[string]any](t, admin, method, api(path), body, "test-workspace", http.StatusBadRequest)
		if result["error"] != errFormerMemberMention.Error() {
			t.Fatalf("%s %s: error %q", method, path, result["error"])
		}
	}
	bare := authRequest[domain.Comment](t, admin, http.MethodPost, api("/api/issues/"+issue.ID+"/comments"), map[string]any{"body": "No mentions"}, "test-workspace", http.StatusCreated)
	plain := authRequest[domain.Document](t, admin, http.MethodPost, api("/api/documents"), map[string]any{"title": "No mentions " + suffix, "content": "Plain"}, "test-workspace", http.StatusCreated)
	project := authRequest[domain.Project](t, admin, http.MethodPost, api("/api/projects"), map[string]any{"name": "Mentions " + suffix, "teamIds": []string{team.ID}}, "test-workspace", http.StatusCreated)
	for _, person := range []domain.User{gone, paused} {
		mention := mentioningDocument("Hello ", person.ID)
		refused(http.MethodPost, "/api/issues/"+issue.ID+"/comments", map[string]any{"body": "Hello", "bodyData": mention})
		refused(http.MethodPatch, "/api/issues/"+issue.ID+"/comments/"+bare.ID, map[string]any{"body": "Ping", "bodyData": mentioningDocument("Ping ", stays.ID, person.ID)})
		refused(http.MethodPatch, "/api/issues/"+issue.ID, map[string]any{"description": "Hello", "descriptionData": mention})
		refused(http.MethodPost, "/api/issues", map[string]any{"title": "New mention", "teamId": team.ID, "description": "Hello", "descriptionData": mention})
		refused(http.MethodPost, "/api/documents", map[string]any{"title": "New mention", "content": "Hello", "contentData": mention})
		refused(http.MethodPatch, "/api/documents/"+plain.ID, map[string]any{"content": "Hello", "contentData": mention})
		refused(http.MethodPost, "/api/documents/"+plain.ID+"/comments", map[string]any{"body": "Hello", "bodyData": mention})
		refused(http.MethodPost, "/api/projects/"+project.ID+"/comments", map[string]any{"body": "Hello", "bodyData": mention})
		refused(http.MethodPost, "/api/projects/"+project.ID+"/updates", map[string]any{"body": "Hello", "bodyData": mention})
	}

	// Text written while they were members keeps its mentions: edits that
	// keep them save, a plain-text @name is text, and members can be mentioned.
	edited := authRequest[domain.Comment](t, admin, http.MethodPatch, api("/api/issues/"+issue.ID+"/comments/"+comment.ID), map[string]any{"body": "Ping again", "bodyData": mentioningDocument("Ping again ", paused.ID, gone.ID, stays.ID)}, "test-workspace", http.StatusOK)
	if edited.Body != "Ping again" || len(commentMentionIDs(&domain.Bootstrap{}, edited)) != 3 {
		t.Fatalf("an edit keeping former mentions was not saved: %+v", edited)
	}
	authRequest[domain.Document](t, admin, http.MethodPatch, api("/api/documents/"+document.ID), map[string]any{"content": "Ping all", "contentData": mentioningDocument("Ping all ", gone.ID, paused.ID, stays.ID)}, "test-workspace", http.StatusOK)
	authRequest[domain.Comment](t, admin, http.MethodPost, api("/api/issues/"+issue.ID+"/comments"), map[string]any{"body": "@Paused Mention was here"}, "test-workspace", http.StatusCreated)
	authRequest[domain.Comment](t, admin, http.MethodPost, api("/api/issues/"+issue.ID+"/comments"), map[string]any{"body": "Hello", "bodyData": mentioningDocument("Hello ", stays.ID)}, "test-workspace", http.StatusCreated)

	// Resumed, the member gets a notice and can be mentioned again.
	authRequest[map[string]any](t, admin, http.MethodPost, api("/api/workspaces/test-workspace/members/"+paused.ID+"/resume"), nil, "", http.StatusNoContent)
	if !noticed("workspace_member.resumed", paused.ID) {
		t.Fatalf("resume sent no member notice: %+v", notices)
	}
	authRequest[domain.Comment](t, admin, http.MethodPost, api("/api/issues/"+issue.ID+"/comments"), map[string]any{"body": "Welcome back", "bodyData": mentioningDocument("Welcome back ", paused.ID)}, "test-workspace", http.StatusCreated)
}
