package store

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func applicationCredentialFixture(t *testing.T) (*SQLiteStore, domain.Bootstrap, domain.OAuthApplication) {
	t.Helper()
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "credentials.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	data := repo.Bootstrap()
	app := domain.OAuthApplication{ID: "automation", ClientID: "flow_client_automation", ClientSecret: "client-secret", Name: "Automation", CreatorID: data.Viewer.ID, Scopes: []string{"read", "write", "app:mentionable", "app:assignable"}, SupportsClientCredentials: true}
	if err := repo.MutateWorkspace(t.Context(), data.Workspace.URLKey, "oauth_application.created", app.ID, nil, func(next *domain.Bootstrap) error {
		next.OAuthApplications = append(next.OAuthApplications, app)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return repo, data, app
}

func issueFixtureApplicationCredential(t *testing.T, repo *SQLiteStore, data domain.Bootstrap, app domain.OAuthApplication, id string) domain.APIKey {
	t.Helper()
	expires := time.Now().Add(time.Hour)
	key, err := repo.IssueApplicationCredential(t.Context(), data.Workspace.URLKey, app.ClientID, app.ClientSecret, "", "app", []string{"read"}, domain.APIKey{ID: id, SecretHash: id, GrantType: "client_credentials", CreatedAt: time.Now(), ExpiresAt: &expires}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func TestApplicationClientCredentialsSeparateSubjectAndIssuer(t *testing.T) {
	repo, data, app := applicationCredentialFixture(t)
	key := issueFixtureApplicationCredential(t, repo, data, app, "first")
	if key.SubjectID() == data.Viewer.ID || key.IssuedByUserID != data.Viewer.ID || key.Actor != "app" {
		t.Fatalf("invalid actor: %+v", key)
	}
	auth, err := repo.AuthenticateAPIKeyRecord(t.Context(), data.Workspace.URLKey, key.SecretHash, nil)
	if err != nil || !auth.User.App || auth.Role != "app" || auth.Key.TeamRestriction != "selected" {
		t.Fatalf("application principal: %+v %v", auth, err)
	}
	second := issueFixtureApplicationCredential(t, repo, data, app, "second")
	if second.SubjectID() != key.SubjectID() {
		t.Fatal("each token created a different application identity")
	}
}

func TestApplicationCredentialsFollowLiveAccessAndRemainRevokedAfterResume(t *testing.T) {
	repo, data, app := applicationCredentialFixture(t)
	key := issueFixtureApplicationCredential(t, repo, data, app, "first")
	installation, err := repo.ApplicationByUser(t.Context(), key.SubjectID())
	if err != nil {
		t.Fatal(err)
	}
	newTeam := domain.Team{ID: "new-public", Name: "Public", Key: "PUB"}
	privateTeam := domain.Team{ID: "new-private", Name: "Private", Key: "PRI", Private: true}
	privateChild := domain.Team{ID: "private-child", Name: "Child", Key: "CHD"}
	if err := repo.MutateWorkspace(t.Context(), data.Workspace.URLKey, "team.created", newTeam.ID, nil, func(next *domain.Bootstrap) error {
		next.Teams = append(next.Teams, newTeam, privateTeam, privateChild)
		next.TeamSettings[privateChild.ID] = domain.TeamSettings{TeamID: privateChild.ID, ParentTeamID: privateTeam.ID}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	auth, err := repo.AuthenticateAPIKeyRecord(t.Context(), "", key.SecretHash, nil)
	if err != nil || !slices.Contains(auth.Key.TeamIDs, newTeam.ID) || slices.Contains(auth.Key.TeamIDs, privateTeam.ID) || slices.Contains(auth.Key.TeamIDs, privateChild.ID) {
		t.Fatalf("public team policy: %+v %v", auth.Key, err)
	}
	installation.AllPublicTeams = false
	installation.TeamIDs = []string{data.Teams[0].ID}
	installation.InstalledBy = data.Viewer.ID
	if _, err := repo.InstallApplication(t.Context(), data.Workspace.URLKey, installation, ""); err != nil {
		t.Fatal(err)
	}
	auth, err = repo.AuthenticateAPIKeyRecord(t.Context(), "", key.SecretHash, nil)
	if err != nil || len(auth.Key.TeamIDs) != 1 {
		t.Fatal("old token ignored narrowed team access", err)
	}
	installation.Active = false
	if _, err := repo.InstallApplication(t.Context(), data.Workspace.URLKey, installation, ""); err != nil {
		t.Fatal(err)
	}
	installation.Active = true
	if _, err := repo.InstallApplication(t.Context(), data.Workspace.URLKey, installation, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AuthenticateAPIKeyRecord(t.Context(), "", key.SecretHash, nil); !errors.Is(err, ErrAuthForbidden) {
		t.Fatal("suspended token resurrected", err)
	}
}

func TestApplicationCredentialLimitsAndRotation(t *testing.T) {
	repo, data, app := applicationCredentialFixture(t)
	key := issueFixtureApplicationCredential(t, repo, data, app, "first")
	for _, input := range []struct {
		secret string
		scopes []string
	}{{"wrong", []string{"read"}}, {app.ClientSecret, []string{"read", "admin"}}} {
		_, err := repo.IssueApplicationCredential(t.Context(), data.Workspace.URLKey, app.ClientID, input.secret, "", "app", input.scopes, domain.APIKey{ID: "rejected", GrantType: "client_credentials"}, nil)
		if !errors.Is(err, ErrAuthForbidden) {
			t.Fatal("invalid credential accepted", err)
		}
	}
	if err := repo.MutateWorkspace(t.Context(), data.Workspace.URLKey, "oauth_application.updated", app.ID, nil, func(next *domain.Bootstrap) error {
		for index := range next.OAuthApplications {
			if next.OAuthApplications[index].ID == app.ID {
				next.OAuthApplications[index].ClientSecret = "rotated"
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AuthenticateAPIKeyRecord(t.Context(), "", key.SecretHash, nil); !errors.Is(err, ErrAuthForbidden) {
		t.Fatal("secret rotation did not invalidate machine token", err)
	}
}

func TestApplicationCredentialIssuanceIsAtomic(t *testing.T) {
	repo, data, app := applicationCredentialFixture(t)
	if _, err := repo.db.ExecContext(t.Context(), `CREATE TRIGGER fail_credential BEFORE INSERT ON domain_events BEGIN SELECT RAISE(ABORT,'failure'); END`); err != nil {
		t.Fatal(err)
	}
	_, err := repo.IssueApplicationCredential(t.Context(), data.Workspace.URLKey, app.ClientID, app.ClientSecret, "", "app", []string{"read"}, domain.APIKey{ID: "rollback", SecretHash: "rollback", GrantType: "client_credentials"}, nil)
	if err == nil {
		t.Fatal("expected database failure")
	}
	items, err := repo.ListApplications(t.Context(), data.Workspace.URLKey)
	if err != nil || len(items) != 0 {
		t.Fatal("failed transaction left an installed principal", err)
	}
	if _, err := repo.AuthenticateAPIKeyRecord(t.Context(), "", "rollback", nil); !errors.Is(err, ErrAuthForbidden) {
		t.Fatal("failed transaction published a credential", err)
	}
}
