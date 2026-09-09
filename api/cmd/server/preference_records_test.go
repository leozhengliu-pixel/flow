package main

import (
	"context"
	"database/sql"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestPreferenceLifecycleNeverHydratesIssueOrDiscussionCollections(t *testing.T) {
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	path := filepath.Join(t.TempDir(), "preferences.db")
	repo, err := store.OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	data := repo.Bootstrap()
	s := &server{store: repo, uploadPath: t.TempDir()}
	host := httptest.NewServer(newHandler(s))
	defer host.Close()
	admin := authClient(t)
	authRequest[domain.AuthSession](t, admin, "POST", host.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", 200)
	member, memberUser := verifiedAuthClient(t, host.URL, "Preference member", "preferences@example.test")
	invites := authRequest[[]domain.Invitation](t, admin, "POST", host.URL+"/api/workspaces/test-workspace/invitations", map[string]any{"emails": []string{memberUser.Email}, "role": "member"}, "", 201)
	authRequest[domain.WorkspaceMembership](t, member, "POST", host.URL+"/api/invitations/accept", map[string]string{"token": invites[0].Token}, "", 200)
	document := authRequest[domain.Document](t, admin, "POST", host.URL+"/api/documents", map[string]any{"title": "Preference document"}, "test-workspace", 201)
	view := authRequest[domain.SavedView](t, admin, "POST", host.URL+"/api/views", map[string]any{"name": "Preference view", "scope": "workspace", "resource": "issues"}, "test-workspace", 201)
	initiative := authRequest[domain.Initiative](t, admin, "POST", host.URL+"/api/initiatives", map[string]string{"name": "Preference initiative"}, "test-workspace", 201)
	privateTeam := authRequest[domain.Team](t, admin, "POST", host.URL+"/api/workspaces/test-workspace/teams", map[string]any{"name": "Private preferences", "key": "PRV", "private": true}, "test-workspace", 201)
	privateIssue := authRequest[domain.Issue](t, admin, "POST", host.URL+"/api/issues", map[string]any{"title": "Private issue", "teamId": privateTeam.ID}, "test-workspace", 201)
	if err := repo.MutateWorkspace(context.Background(), "test-workspace", "test.preferences", "", nil, func(next *domain.Bootstrap) error {
		next.Notifications = append(next.Notifications, domain.Notification{ID: "preference-notification", RecipientID: data.Viewer.ID, IssueID: data.Issues[0].ID, CreatedAt: time.Now().UTC()})
		next.Reviews = append(next.Reviews, domain.CodeReview{ID: "preference-review", SlugID: "preference-review", Title: "Preference review", IssueIDs: []string{data.Issues[0].ID}})
		next.ReleasePipelines = append(next.ReleasePipelines, domain.ReleasePipeline{ID: "preference-pipeline", TeamIDs: []string{data.Issues[0].Team.ID}})
		next.Releases = append(next.Releases, domain.Release{ID: "preference-release", PipelineID: "preference-pipeline", IssueIDs: []string{data.Issues[0].ID}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Even target payloads are invalid: preferences require indexed visibility,
	// not deserialization of issue text or discussion histories.
	if _, err := db.Exec(`UPDATE issue_records SET data=?`, []byte("invalid-issue-json")); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE workspace_content_records SET data=? WHERE kind IN ('comment','activity')`, []byte("invalid-discussion-json")); err != nil {
		t.Fatal(err)
	}
	folder := authRequest[domain.FavoriteFolder](t, admin, "POST", host.URL+"/api/favorite-folders", map[string]string{"name": "Planning"}, "test-workspace", 201)
	authRequest[any](t, member, "PATCH", host.URL+"/api/favorite-folders/"+folder.ID, map[string]string{"name": "Forbidden"}, "test-workspace", 404)
	authRequest[any](t, member, "DELETE", host.URL+"/api/favorite-folders/"+folder.ID, nil, "test-workspace", 404)
	for _, resource := range []struct{ kind, id string }{{"issue", data.Issues[0].ID}, {"project", data.Projects[0].ID}, {"team", data.Teams[0].ID}, {"document", document.ID}, {"view", view.ID}, {"cycle", data.Cycles[0].ID}, {"label", data.Labels[0].ID}, {"initiative", initiative.ID}, {"review", "preference-review"}, {"release", "preference-release"}, {"release_pipeline", "preference-pipeline"}} {
		t.Run(resource.kind, func(t *testing.T) {
			url := host.URL + "/api/favorites/" + resource.kind + "/" + resource.id
			first := authRequest[domain.Favorite](t, admin, "PUT", url, nil, "test-workspace", 200)
			again := authRequest[domain.Favorite](t, admin, "PUT", url, nil, "test-workspace", 200)
			if first.ID != again.ID {
				t.Fatal("duplicate favorite")
			}
			moved := authRequest[domain.Favorite](t, admin, "PATCH", url, map[string]any{"folderId": folder.ID, "position": 2.5}, "test-workspace", 200)
			if moved.FolderID != folder.ID || moved.Position != 2.5 {
				t.Fatal("favorite move not saved")
			}
			subURL := host.URL + "/api/subscriptions/" + resource.kind + "/" + resource.id
			sub := authRequest[domain.Subscription](t, admin, "PUT", subURL, map[string]any{"events": []string{"updated"}}, "test-workspace", 200)
			updated := authRequest[domain.Subscription](t, admin, "PUT", subURL, map[string]any{"events": []string{"created"}}, "test-workspace", 200)
			if sub.ID != updated.ID || !slices.Equal(updated.Events, []string{"created"}) {
				t.Fatal("subscription not updated")
			}
			authRequest[any](t, admin, "DELETE", subURL, nil, "test-workspace", 204)
			authRequest[any](t, admin, "DELETE", url, nil, "test-workspace", 204)
			authRequest[domain.Favorite](t, admin, "PUT", url, nil, "test-workspace", 200)
		})
	}
	for _, path := range []string{"/api/cycles/" + data.Cycles[0].ID, "/api/initiatives/" + initiative.ID, "/api/documents/" + document.ID, "/api/reviews/preference-review"} {
		for _, value := range []bool{false, true} {
			authRequest[any](t, admin, "PATCH", host.URL+path, map[string]bool{"favorite": value}, "test-workspace", 200)
		}
	}
	authRequest[any](t, member, "PUT", host.URL+"/api/favorites/issue/"+privateIssue.ID, nil, "test-workspace", 403)
	authRequest[any](t, member, "PUT", host.URL+"/api/subscriptions/issue/"+privateIssue.ID, nil, "test-workspace", 403)
	authRequest[any](t, member, "PUT", host.URL+"/api/favorites/team/"+privateTeam.ID, nil, "test-workspace", 403)
	authRequest[any](t, admin, "PUT", host.URL+"/api/favorites/issue/missing", nil, "test-workspace", 403)
	notificationURL := host.URL + "/api/notifications/preference-notification"
	favorited := authRequest[domain.Notification](t, admin, "PATCH", notificationURL, map[string]bool{"favorite": true}, "test-workspace", 200)
	if !favorited.Favorite {
		t.Fatal("notification favorite missing")
	}
	authRequest[any](t, member, "PATCH", notificationURL, map[string]bool{"favorite": false}, "test-workspace", 403)
	authRequest[domain.Notification](t, admin, "PATCH", notificationURL, map[string]bool{"favorite": false}, "test-workspace", 200)
	authRequest[any](t, admin, "DELETE", host.URL+"/api/favorite-folders/"+folder.ID, nil, "test-workspace", 204)
	paged := authRequest[domain.Bootstrap](t, admin, "GET", host.URL+"/api/issue-records/bootstrap", nil, "test-workspace", 200)
	if !slices.ContainsFunc(paged.Favorites, func(f domain.Favorite) bool { return f.ResourceType == "issue" && f.ResourceID == data.Issues[0].ID }) {
		t.Fatal("paged bootstrap discarded issue favorite")
	}
	memberData := authRequest[domain.Bootstrap](t, member, "GET", host.URL+"/api/issue-records/bootstrap", nil, "test-workspace", 200)
	for _, doc := range memberData.Documents {
		if doc.ID == document.ID && doc.Favorite {
			t.Fatal("another member inherited the owner's favorite flag")
		}
	}
	r := httptest.NewRequest("GET", "/api/realtime/events?workspace=test-workspace", nil)
	r = r.WithContext(context.WithValue(r.Context(), authUserContextKey{}, data.Viewer))
	if _, visible, err := s.pagedRealtimeEvent(r, domain.RealtimeEvent{Type: "favorite.added", ActorID: memberUser.ID}); err != nil || visible {
		t.Fatal("private favorite event reached another user")
	}
	if _, visible, err := s.pagedRealtimeEvent(r, domain.RealtimeEvent{Type: "notification.favorited", AggregateID: "preference-notification"}); err != nil || !visible {
		t.Fatalf("own notification event was lost: %v", err)
	}
	otherRequest := r.WithContext(context.WithValue(r.Context(), authUserContextKey{}, memberUser))
	if _, visible, err := s.pagedRealtimeEvent(otherRequest, domain.RealtimeEvent{Type: "notification.favorited", AggregateID: "preference-notification"}); err != nil || visible {
		t.Fatal("notification was sent to another user")
	}
	paged.Favorites = append(paged.Favorites, domain.Favorite{ID: "restricted", UserID: data.Viewer.ID, ResourceType: "issue", ResourceID: privateIssue.ID})
	keyRequest := r.WithContext(context.WithValue(r.Context(), apiKeyContextKey{}, domain.APIKey{TeamRestriction: "selected", TeamIDs: []string{data.Issues[0].Team.ID}}))
	if err := s.filterPreferenceIssueTeams(keyRequest, &paged); err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(paged.Favorites, func(f domain.Favorite) bool { return f.ResourceID == privateIssue.ID }) {
		t.Fatal("API-key scope leaked a private favorite")
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM issue_records WHERE data=?`, []byte("invalid-issue-json")).Scan(&count); err != nil || count != len(data.Issues)+1 {
		t.Fatalf("preference write replaced issue rows: count=%d err=%v", count, err)
	}
}
