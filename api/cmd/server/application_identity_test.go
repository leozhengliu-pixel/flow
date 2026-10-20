package main

import (
	"encoding/json"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Renaming an OAuth application or changing its logo updates its installed
// agent everywhere: the workspace user, issue delegate, comment
// and activity authors, and the installation itself.
func TestOAuthApplicationEditUpdatesItsAgentIdentity(t *testing.T) {
	s, host, admin, data := applicationHTTPFixture(t)
	workspace := data.Workspace.URLKey
	app := authRequest[domain.OAuthApplication](t, admin, "POST", host.URL+"/api/oauth-applications", map[string]any{"name": "Old agent", "logoUrl": "/uploads/oauth_app_icon_old.png", "scopes": domain.ApplicationScopes()}, workspace, 201)
	token := authRequest[struct {
		Token   string `json:"access_token"`
		Subject string `json:"subject_user_id"`
	}](t, admin, "POST", host.URL+"/api/oauth-applications/"+app.ID+"/developer-token", map[string]any{"actor": "app", "scopes": []string{"read", "write", "app:assignable", "app:mentionable"}}, workspace, 200)
	appUser := token.Subject
	issue := authRequest[domain.Issue](t, admin, "POST", host.URL+"/api/issues", map[string]any{"title": "Agent work", "teamId": data.Teams[0].ID, "delegateId": appUser}, workspace, 201)
	bearerJSON(t, "POST", host.URL+"/api/issue-records/"+issue.ID+"/comments", token.Token, map[string]any{"body": "On it"}, 201)
	bearerJSON(t, "PATCH", host.URL+"/api/issue-records/"+issue.ID, token.Token, map[string]any{"priority": 2}, 200)

	authRequest[domain.OAuthApplication](t, admin, "PATCH", host.URL+"/api/oauth-applications/"+app.ID, map[string]any{"name": "New agent", "logoUrl": "/uploads/oauth_app_icon_new.png"}, workspace, 200)

	check := func(where string, user domain.User) {
		t.Helper()
		if user.ID != appUser || user.Name != "New agent" || user.DisplayName != "New agent" || user.AvatarURL != "/uploads/oauth_app_icon_new.png" {
			t.Fatalf("%s = %+v", where, user)
		}
	}
	checkAll := func() {
		t.Helper()
		bootstrap := authRequest[domain.Bootstrap](t, admin, "GET", host.URL+"/api/bootstrap", nil, workspace, 200)
		found := false
		for _, user := range bootstrap.Users {
			if user.ID == appUser {
				check("bootstrap user", user)
				found = true
			}
		}
		if !found {
			t.Fatal("app user missing from bootstrap")
		}
		record := authRequest[domain.Issue](t, admin, "GET", host.URL+"/api/issue-records/"+issue.ID, nil, workspace, 200)
		if record.Delegate == nil {
			t.Fatalf("issue = %+v", record)
		}
		check("delegate", *record.Delegate)
		history := authRequest[store.IssueHistory](t, admin, "GET", host.URL+"/api/issue-records/"+issue.ID+"/history", nil, workspace, 200)
		comments, activities := 0, 0
		for _, comment := range history.Comments {
			if comment.User.ID == appUser {
				check("comment author", comment.User)
				comments++
			}
		}
		for _, activity := range history.Activities {
			if activity.Actor.ID == appUser {
				check("activity actor", activity.Actor)
				activities++
			}
		}
		if comments == 0 || activities == 0 {
			raw, _ := json.Marshal(history)
			t.Fatalf("app comments=%d activities=%d in %s", comments, activities, raw)
		}
		installations := authRequest[[]domain.ApplicationInstallation](t, admin, "GET", host.URL+"/api/application-installations", nil, workspace, 200)
		for _, installation := range installations {
			if installation.UserID == appUser && (installation.Name != "New agent" || installation.AvatarURL != "/uploads/oauth_app_icon_new.png") {
				t.Fatalf("installation = %+v", installation)
			}
		}
		installation, err := s.store.ApplicationByUser(t.Context(), appUser)
		if err != nil {
			t.Fatal(err)
		}
		check("installation user", installation.User())
	}
	checkAll()

	// An app user left stale by an earlier edit is repaired at startup.
	if _, err := s.store.InstallApplication(t.Context(), workspace, func() domain.ApplicationInstallation {
		installation, _ := s.store.ApplicationByUser(t.Context(), appUser)
		installation.Name, installation.AvatarURL, installation.InstalledBy = "Stale agent", "/uploads/oauth_app_icon_old.png", data.Viewer.ID
		return installation
	}(), ""); err != nil {
		t.Fatal(err)
	}
	if err := s.store.SyncAllApplicationIdentities(t.Context()); err != nil {
		t.Fatal(err)
	}
	checkAll()
}
