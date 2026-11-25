package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

const scaleP01FixturePassword = "p01-fixture-password"

type scaleP01OwnedResources struct {
	repo       *store.SQLiteStore
	workspaces []string
	users      []domain.User
}

func scaleP01PermissionConfigs(t *testing.T) map[string]store.DatabaseConfig {
	t.Helper()
	configs := map[string]store.DatabaseConfig{"sqlite": {Driver: "sqlite", Path: filepath.Join(t.TempDir(), "p01-permissions.db"), MaxOpenConns: 1}}
	if driver := os.Getenv("FLOW_TEST_DATABASE_DRIVER"); driver != "" {
		configs[driver] = scaleP01TestDatabaseConfig(t, store.DatabaseConfig{}, 4)
	}
	return configs
}

func scaleP01TrackResources(t *testing.T, repo *store.SQLiteStore) *scaleP01OwnedResources {
	t.Helper()
	owned := &scaleP01OwnedResources{repo: repo}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		for i := len(owned.workspaces) - 1; i >= 0; i-- {
			if err := repo.DeleteWorkspace(ctx, owned.workspaces[i]); err != nil {
				t.Errorf("clean up owned workspace: %v", err)
			}
		}
		for _, user := range owned.users {
			if err := repo.EraseAccount(ctx, user.ID); err != nil {
				t.Errorf("clean up owned account: %v", err)
			}
		}
	})
	return owned
}

func scaleP01RegisteredClient(t *testing.T, owned *scaleP01OwnedResources, base, name string) (*http.Client, domain.User) {
	t.Helper()
	user, token, err := owned.repo.Register(t.Context(), name, idgen.New("scale-p01-")+"@example.test", scaleP01FixturePassword)
	if err != nil {
		t.Fatal(err)
	}
	owned.users = append(owned.users, user)
	if err := owned.repo.VerifyEmail(t.Context(), token); err != nil {
		t.Fatal(err)
	}
	user.EmailVerified = true
	client := authClient(t)
	authRequest[domain.AuthSession](t, client, http.MethodPost, base+"/api/auth/login", map[string]string{"email": user.Email, "password": scaleP01FixturePassword}, "", http.StatusOK)
	return client, user
}

func scaleP01OwnedWorkspace(t *testing.T, repo *store.SQLiteStore) (*server, *httptest.Server, *http.Client, domain.Bootstrap, *scaleP01OwnedResources) {
	t.Helper()
	srv := &server{store: repo, uploadPath: t.TempDir()}
	host := httptest.NewServer(newHandler(srv))
	t.Cleanup(host.Close)
	if err := srv.stopDeliveryScheduler(t.Context()); err != nil {
		t.Fatal(err)
	}
	owned := scaleP01TrackResources(t, repo)
	admin, user := scaleP01RegisteredClient(t, owned, host.URL, "P01 owner")
	data, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), user), "P01 isolated", idgen.New("scale-p01-"), "us")
	if err != nil {
		t.Fatal(err)
	}
	owned.workspaces = append(owned.workspaces, data.Workspace.URLKey)
	return srv, host, admin, data, owned
}

func scaleP01Join(t *testing.T, owned *scaleP01OwnedResources, base, workspace string, admin *http.Client, name, role string, teams ...string) (*http.Client, domain.User) {
	t.Helper()
	client, user := scaleP01RegisteredClient(t, owned, base, name)
	invites := authRequest[[]domain.Invitation](t, admin, http.MethodPost, base+"/api/workspaces/"+workspace+"/invitations", map[string]any{"emails": []string{user.Email}, "role": role, "teamIds": teams}, "", http.StatusCreated)
	authRequest[domain.WorkspaceMembership](t, client, http.MethodPost, base+"/api/invitations/accept", map[string]string{"token": invites[0].Token}, "", http.StatusOK)
	return client, user
}

func TestScaleP01FixturePreservesExistingAccount(t *testing.T) {
	for driver, config := range scaleP01PermissionConfigs(t) {
		t.Run(driver, func(t *testing.T) {
			repo, err := store.OpenDatabase(config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { repo.Close() })
			preserved := scaleP01TrackResources(t, repo)
			user, token, err := repo.Register(t.Context(), "Unrelated account", idgen.New("p01-existing-")+"@example.test", "p01-existing-password")
			if err != nil {
				t.Fatal(err)
			}
			preserved.users = append(preserved.users, user)
			if err := repo.VerifyEmail(t.Context(), token); err != nil {
				t.Fatal(err)
			}
			user.EmailVerified = true
			data, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), user), "Unrelated workspace", idgen.New("p01-existing-"), "us")
			if err != nil {
				t.Fatal(err)
			}
			preserved.workspaces = append(preserved.workspaces, data.Workspace.URLKey)
			var fixture domain.Bootstrap
			t.Run("owned-fixture", func(t *testing.T) {
				_, host, client, created, _ := scaleP01OwnedWorkspace(t, repo)
				fixture = created
				if fixture.Viewer.ID == user.ID || fixture.Workspace.ID == data.Workspace.ID {
					t.Fatal("fixture reused an unrelated identity or workspace")
				}
				authRequest[domain.Bootstrap](t, client, http.MethodGet, host.URL+"/api/bootstrap", nil, fixture.Workspace.URLKey, http.StatusOK)
			})
			if repo.HasWorkspace(fixture.Workspace.URLKey) {
				t.Fatal("owned workspace survived fixture cleanup")
			}
			if _, _, err := repo.Login(t.Context(), fixture.Viewer.Email, scaleP01FixturePassword); err == nil {
				t.Fatal("owned account survived fixture cleanup")
			}
			if session, _, err := repo.Login(t.Context(), user.Email, "p01-existing-password"); err != nil || session.User.ID != user.ID {
				t.Fatal("fixture changed unrelated credentials")
			}
			if current, ok := repo.BootstrapFor(data.Workspace.URLKey); !ok || current.Workspace.Name != data.Workspace.Name {
				t.Fatal("fixture changed an unrelated workspace")
			}
		})
	}
}
