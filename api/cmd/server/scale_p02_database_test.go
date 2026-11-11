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

const scaleP02FixturePassword = "p02-fixture-password"

func scaleP02ServerStore(t *testing.T) *store.SQLiteStore {
	t.Helper()
	driver, databaseURL := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL")
	if (driver == "") != (databaseURL == "") {
		t.Fatal("P02 database tests require both FLOW_TEST_DATABASE_DRIVER and FLOW_TEST_DATABASE_URL")
	}
	config := store.DatabaseConfig{Driver: driver, URL: databaseURL, MaxOpenConns: 4}
	if driver == "" {
		config.Driver, config.Path = "sqlite", filepath.Join(t.TempDir(), "p02.db")
	} else if driver == "sqlite" {
		config.Path = databaseURL
	}
	repo, err := store.OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	return repo
}

type scaleP02ServerFixture struct {
	repo       *store.SQLiteStore
	srv        *server
	host       *httptest.Server
	admin      *http.Client
	data       domain.Bootstrap
	workspaces []string
	users      []domain.User
}

func scaleP02NewServerFixture(t *testing.T, repo *store.SQLiteStore) *scaleP02ServerFixture {
	t.Helper()
	fixture := &scaleP02ServerFixture{repo: repo, srv: &server{store: repo, uploadPath: t.TempDir()}}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		for i := len(fixture.workspaces) - 1; i >= 0; i-- {
			if err := repo.DeleteWorkspace(ctx, fixture.workspaces[i]); err != nil {
				t.Errorf("clean up owned workspace: %v", err)
			}
		}
		for _, user := range fixture.users {
			if err := repo.EraseAccount(ctx, user.ID); err != nil {
				t.Errorf("clean up owned account: %v", err)
			}
		}
	})
	fixture.host = httptest.NewServer(newHandler(fixture.srv))
	t.Cleanup(fixture.host.Close)
	if err := fixture.srv.stopDeliveryScheduler(t.Context()); err != nil {
		t.Fatal(err)
	}
	var owner domain.User
	fixture.admin, owner = fixture.register(t, "P02 owner")
	data, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), owner), "P02 isolated", idgen.New("scale-p02-"), "us")
	if err != nil {
		t.Fatal(err)
	}
	fixture.workspaces = append(fixture.workspaces, data.Workspace.URLKey)
	fixture.data = data
	return fixture
}

func (f *scaleP02ServerFixture) register(t *testing.T, name string) (*http.Client, domain.User) {
	t.Helper()
	// Register directly to avoid the public signup rate limit; requests under
	// test still use a verified identity and a real HTTP cookie login.
	user, verification, err := f.repo.Register(t.Context(), name, idgen.New("scale-p02-user-")+"@example.test", scaleP02FixturePassword)
	if err != nil {
		t.Fatal(err)
	}
	f.users = append(f.users, user)
	if err := f.repo.VerifyEmail(t.Context(), verification); err != nil {
		t.Fatal(err)
	}
	user.EmailVerified = true
	client := authClient(t)
	authRequest[domain.AuthSession](t, client, http.MethodPost, f.host.URL+"/api/auth/login", map[string]string{"email": user.Email, "password": scaleP02FixturePassword}, "", http.StatusOK)
	return client, user
}

func (f *scaleP02ServerFixture) join(t *testing.T, name, role string, teams ...string) (*http.Client, domain.User) {
	t.Helper()
	client, user := f.register(t, name)
	endpoint := f.host.URL + "/api/workspaces/" + f.data.Workspace.URLKey + "/invitations"
	invitations := authRequest[[]domain.Invitation](t, f.admin, http.MethodPost, endpoint, map[string]any{"emails": []string{user.Email}, "role": role, "teamIds": teams}, "", http.StatusCreated)
	authRequest[domain.WorkspaceMembership](t, client, http.MethodPost, f.host.URL+"/api/invitations/accept", map[string]string{"token": invitations[0].Token}, "", http.StatusOK)
	return client, user
}

func TestScaleP02FixturePreservesExistingAccount(t *testing.T) {
	repo := scaleP02ServerStore(t)
	existing := scaleP02NewServerFixture(t, repo)
	var owned *scaleP02ServerFixture
	t.Run("isolated-fixture", func(t *testing.T) {
		owned = scaleP02NewServerFixture(t, repo)
		if owned.data.Viewer.ID == existing.data.Viewer.ID || owned.data.Workspace.ID == existing.data.Workspace.ID {
			t.Fatal("fixture reused an existing identity or workspace")
		}
		authRequest[domain.Bootstrap](t, owned.admin, http.MethodGet, owned.host.URL+"/api/bootstrap", nil, owned.data.Workspace.URLKey, http.StatusOK)
	})
	if repo.HasWorkspace(owned.data.Workspace.URLKey) {
		t.Fatal("owned workspace survived cleanup")
	}
	if _, _, err := repo.Login(t.Context(), owned.data.Viewer.Email, scaleP02FixturePassword); err == nil {
		t.Fatal("owned account survived cleanup")
	}
	if session, _, err := repo.Login(t.Context(), existing.data.Viewer.Email, scaleP02FixturePassword); err != nil || session.User.ID != existing.data.Viewer.ID {
		t.Fatalf("fixture changed existing credentials: %v", err)
	}
	if data, ok := repo.BootstrapFor(existing.data.Workspace.URLKey); !ok || data.Workspace.Name != existing.data.Workspace.Name {
		t.Fatal("fixture changed or deleted an existing workspace")
	}
}
