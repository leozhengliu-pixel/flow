package store

import (
	"flow/api/internal/domain"
	"path/filepath"
	"testing"
	"time"
)

func TestApplicationInstallationAndScopedPrincipal(t *testing.T) {
	s, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	d := s.Bootstrap()
	a, err := s.InstallApplication(t.Context(), d.Workspace.URLKey, domain.ApplicationInstallation{ClientID: "test-agent", Name: "Build agent", InstalledBy: d.Viewer.ID, Scopes: []string{"read", "write", "app:mentionable", "app:assignable"}, TeamIDs: []string{d.Teams[0].ID}, Active: true}, "secret")
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.UserByID(t.Context(), a.UserID)
	if err != nil || !u.App || !u.CanDelegateTo(d.Teams[0].ID) || u.CanDelegateTo("other") {
		t.Fatalf("principal: %+v %v", u, err)
	}
	members, err := s.ListMembers(t.Context(), d.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range members {
		if m.User.ID == a.UserID {
			found = m.Role == "app" && m.User.App
		}
	}
	if !found {
		t.Fatal("missing application member")
	}
	a.Active = false
	a.InstalledBy = d.Viewer.ID
	_, err = s.InstallApplication(t.Context(), d.Workspace.URLKey, a, "")
	if err != nil {
		t.Fatal(err)
	}
	u, err = s.UserByID(t.Context(), a.UserID)
	if err != nil || u.Active {
		t.Fatal("application suspension failed")
	}
}

func TestApplicationOAuthGrantUsesApplicationActor(t *testing.T) {
	s, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	d := s.Bootstrap()
	client := domain.OAuthClient{ClientID: "test", ClientName: "Agent", RedirectURIs: []string{"https://example.test/callback"}}
	if err = s.RegisterOAuthClient(t.Context(), client); err != nil {
		t.Fatal(err)
	}
	grant, err := s.CreateOAuthAuthorizationGrant(t.Context(), "code", domain.OAuthAuthorizationCode{Actor: "app", InstallerID: d.Viewer.ID, UserID: d.Viewer.ID, WorkspaceKey: d.Workspace.URLKey, ClientID: client.ClientID, TeamIDs: []string{d.Teams[0].ID}, Scopes: []string{"read", "app:assignable"}, ExpiresAt: time.Now().Add(time.Hour)}, domain.OAuthAuthorization{ID: "grant"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if grant.UserID == d.Viewer.ID {
		t.Fatal("app token impersonates installer")
	}
	key := domain.APIKey{ID: "app-token", SecretHash: "hash", Scopes: []string{"read"}, CreatedAt: time.Now()}
	_, err = s.ExchangeOAuthGrant(t.Context(), "authorization_code", "code", client.ClientID, "refresh", key, func(domain.OAuthAuthorizationCode) bool { return true }, nil)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := s.AuthenticateAPIKeyRecord(t.Context(), d.Workspace.URLKey, "hash", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !auth.User.App || auth.Key.TeamRestriction != "selected" || len(auth.Key.TeamIDs) != 1 {
		t.Fatalf("unscoped app token: %+v", auth)
	}
}
