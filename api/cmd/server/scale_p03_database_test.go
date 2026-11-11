package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/coordination"
	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"

	"github.com/alicebob/miniredis/v2"
)

func scaleP03DatabaseConfig(t *testing.T) store.DatabaseConfig {
	t.Helper()
	driver, databaseURL := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL")
	if (driver == "") != (databaseURL == "") {
		t.Fatal("P03 external database tests require both FLOW_TEST_DATABASE_DRIVER and FLOW_TEST_DATABASE_URL")
	}
	if driver != "" && driver != "sqlite" && driver != "mysql" && driver != "postgres" && driver != "postgresql" {
		t.Fatalf("P03 external database driver %q is unsupported", driver)
	}
	config := store.DatabaseConfig{Driver: driver, URL: databaseURL, MaxOpenConns: 4}
	if driver == "" {
		config.Driver, config.Path = "sqlite", filepath.Join(t.TempDir(), "p03.db")
	} else if driver == "sqlite" {
		config.Path = databaseURL
	}
	return config
}

type scaleP03FixtureResources struct {
	repo       *store.SQLiteStore
	workspaces []string
	users      []domain.User
	appUsers   []struct{ workspace, id string }
}

type scaleP03PlatformFixture struct {
	platformFixture
	resources *scaleP03FixtureResources
}

func scaleP03TrackResources(t *testing.T, repo *store.SQLiteStore) *scaleP03FixtureResources {
	t.Helper()
	resources := &scaleP03FixtureResources{repo: repo}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		for _, app := range resources.appUsers {
			if err := repo.ErasePersonFromWorkspace(ctx, app.workspace, app.id); err != nil {
				t.Errorf("clean up owned application membership: %v", err)
			}
			if err := repo.EraseAccount(ctx, app.id); err != nil {
				t.Errorf("clean up owned application account: %v", err)
			}
		}
		for i := len(resources.workspaces) - 1; i >= 0; i-- {
			if err := repo.DeleteWorkspace(ctx, resources.workspaces[i]); err != nil {
				t.Errorf("clean up owned workspace %s: %v", resources.workspaces[i], err)
			}
		}
		for _, user := range resources.users {
			if err := repo.EraseAccount(ctx, user.ID); err != nil {
				t.Errorf("clean up owned account %s: %v", user.ID, err)
			}
		}
	})
	return resources
}

func scaleP03CreateFixture(t *testing.T, repo *store.SQLiteStore, service *server, host *httptest.Server) scaleP03PlatformFixture {
	t.Helper()
	if err := service.stopDeliveryScheduler(t.Context()); err != nil {
		t.Fatal(err)
	}
	resources := scaleP03TrackResources(t, repo)
	admin, user := scaleP03RegisteredClient(t, resources, host.URL, "P03 owner", idgen.New("scale-p03-owner-")+"@example.test")
	data, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), user), "P03 isolated", idgen.New("scale-p03-"), "us")
	if err != nil {
		t.Fatal(err)
	}
	resources.workspaces = append(resources.workspaces, data.Workspace.URLKey)
	return scaleP03PlatformFixture{platformFixture{repo, service, host, admin, data}, resources}
}

func scaleP03RegisteredClient(t *testing.T, resources *scaleP03FixtureResources, baseURL, name, email string) (*http.Client, domain.User) {
	t.Helper()
	const password = "p03-fixture-password"
	// Set up identities without consuming the public registration rate limit;
	// every measured request still authenticates with a real cookie session.
	user, token, err := resources.repo.Register(t.Context(), name, email, password)
	if err != nil {
		t.Fatal(err)
	}
	resources.users = append(resources.users, user)
	if err := resources.repo.VerifyEmail(t.Context(), token); err != nil {
		t.Fatal(err)
	}
	user.EmailVerified = true
	client := authClient(t)
	authRequest[domain.AuthSession](t, client, http.MethodPost, baseURL+"/api/auth/login", map[string]string{"email": email, "password": password}, "", http.StatusOK)
	return client, user
}

func TestScaleP03FixturePreservesExistingAccount(t *testing.T) {
	for driver, repo := range scaleP03ServerTestStores(t) {
		t.Run(driver, func(t *testing.T) {
			resources := scaleP03TrackResources(t, repo)
			const password = "unrelated-existing-password"
			user, token, err := repo.Register(t.Context(), "Existing account", idgen.New("p03-existing-")+"@example.test", password)
			if err != nil {
				t.Fatal(err)
			}
			resources.users = append(resources.users, user)
			if err := repo.VerifyEmail(t.Context(), token); err != nil {
				t.Fatal(err)
			}
			user.EmailVerified = true
			data, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), user), "Existing workspace", idgen.New("p03-existing-"), "us")
			if err != nil {
				t.Fatal(err)
			}
			resources.workspaces = append(resources.workspaces, data.Workspace.URLKey)
			var owned scaleP03PlatformFixture
			t.Run("isolated-fixture", func(t *testing.T) {
				owned = scaleP03Fixture(t, repo)
				if owned.data.Viewer.ID == user.ID || owned.data.Workspace.URLKey == data.Workspace.URLKey {
					t.Fatal("fixture reused existing identity or workspace")
				}
				authRequest[domain.Bootstrap](t, owned.admin, http.MethodGet, owned.host.URL+"/api/bootstrap", nil, owned.data.Workspace.URLKey, http.StatusOK)
			})
			if repo.HasWorkspace(owned.data.Workspace.URLKey) {
				t.Fatal("owned workspace was not cleaned up")
			}
			if _, _, err := repo.Login(t.Context(), owned.data.Viewer.Email, "p03-fixture-password"); err == nil {
				t.Fatal("owned account can still sign in after cleanup")
			}
			if session, _, err := repo.Login(t.Context(), user.Email, password); err != nil || session.User.ID != user.ID {
				t.Fatalf("fixture changed existing credentials: %v", err)
			}
			if preserved, ok := repo.BootstrapFor(data.Workspace.URLKey); !ok || preserved.Workspace.Name != data.Workspace.Name {
				t.Fatal("fixture removed or changed existing workspace")
			}
		})
	}
}

func scaleP03ServerTestStores(t *testing.T) map[string]*store.SQLiteStore {
	t.Helper()
	config := scaleP03DatabaseConfig(t)
	repo, err := store.OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	return map[string]*store.SQLiteStore{config.Driver: repo}
}

func newScaleP03PeerServers(t *testing.T) peerServers {
	t.Helper()
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	config := scaleP03DatabaseConfig(t)
	redisServer := miniredis.RunT(t)
	prefix := fmt.Sprintf("scale-p03-%d", time.Now().UnixNano())
	uploads := t.TempDir()
	var peers peerServers
	for index := range 2 {
		repo, err := store.OpenDatabase(config)
		if err != nil {
			t.Fatal(err)
		}
		coordinator, err := coordination.Open(t.Context(), coordination.Config{Mode: "standalone", Addrs: []string{redisServer.Addr()}, Prefix: prefix, ConnectTimeout: time.Second, LockTTL: 5 * time.Second, LockWait: 5 * time.Second})
		if err != nil {
			_ = repo.Close()
			t.Fatal(err)
		}
		repo.SetWorkspaceCoordinator(coordinator)
		ctx, cancel := context.WithCancel(context.Background())
		service := &server{store: repo, uploadPath: uploads, coordinator: coordinator, coordinationContext: ctx}
		host := httptest.NewServer(newHandler(service))
		if err := service.stopDeliveryScheduler(t.Context()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			host.Close()
			cancel()
			_ = coordinator.Close()
			_ = repo.Close()
		})
		peers.stores[index], peers.servers[index], peers.deafen[index] = repo, service, cancel
		if index == 0 {
			peers.first = host
		} else {
			peers.second = host
		}
	}
	return peers
}
