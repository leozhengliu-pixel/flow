package store

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func scaleP04OwnedStore(t *testing.T) (*SQLiteStore, DatabaseConfig) {
	t.Helper()
	driver, databaseURL := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL")
	if (driver == "") != (databaseURL == "") {
		t.Fatal("P04 tests require both database driver and URL")
	}
	config := DatabaseConfig{Driver: driver, URL: databaseURL, MaxOpenConns: 4}
	switch driver {
	case "":
		config.Driver, config.Path, config.MaxOpenConns = "sqlite", filepath.Join(t.TempDir(), "p04.db"), 1
	case "sqlite":
		config.Path, config.MaxOpenConns = databaseURL, 1
	case "mysql", "postgres", "postgresql":
	default:
		t.Fatalf("unsupported P04 test driver %q", driver)
	}
	repo, err := OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	var users []domain.User
	workspace := ""
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if workspace != "" {
			if err := repo.DeleteWorkspace(ctx, workspace); err != nil {
				t.Errorf("clean up owned workspace: %v", err)
			}
		}
		for _, user := range users {
			if err := repo.EraseAccount(ctx, user.ID); err != nil {
				t.Errorf("clean up owned account: %v", err)
			}
		}
	})
	fixture := localSQLiteFixture()
	var replacements []string
	for _, original := range fixture.Users {
		user, token, err := repo.Register(t.Context(), original.Name, idgen.New("p04-store-")+"@example.test", "p04-fixture-password")
		if err != nil {
			t.Fatal(err)
		}
		users = append(users, user)
		if err := repo.VerifyEmail(t.Context(), token); err != nil {
			t.Fatal(err)
		}
		replacements = append(replacements, original.ID, user.ID, original.Email, user.Email)
	}
	raw, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(strings.NewReplacer(replacements...).Replace(string(raw))), &fixture); err != nil {
		t.Fatal(err)
	}
	ctx := ContextWithActor(t.Context(), fixture.Viewer)
	data, err := repo.CreateWorkspaceWith(ctx, "P04 owned", idgen.New("p04-store-"), "us", func(data *domain.Bootstrap) error {
		ownedWorkspace := data.Workspace
		*data = fixture
		data.Workspace = ownedWorkspace
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	workspace = data.Workspace.URLKey
	for _, user := range users {
		if err := repo.EnsureWorkspaceMembership(ctx, data.Workspace.ID, user.ID); err != nil {
			t.Fatal(err)
		}
		role := "member"
		if user.ID == data.Viewer.ID {
			role = "owner"
		}
		if err := repo.SetTeamMembership(ctx, data.Workspace.ID, data.Teams[0].ID, user.ID, role, true); err != nil {
			t.Fatal(err)
		}
	}
	return repo, config
}

func TestScaleP04FixturePreservesUnrelatedAccount(t *testing.T) {
	repo, config := scaleP04OwnedStore(t)
	anchor := repo.Bootstrap()
	if config.Driver == "sqlite" {
		t.Setenv("FLOW_TEST_DATABASE_DRIVER", "sqlite")
		t.Setenv("FLOW_TEST_DATABASE_URL", config.Path)
	}
	var owned domain.Bootstrap
	t.Run("second-fixture", func(t *testing.T) {
		second := scaleP04Store(t)
		owned = second.Bootstrap()
		if owned.Viewer.ID == anchor.Viewer.ID || owned.Workspace.URLKey == anchor.Workspace.URLKey {
			t.Fatal("fixture reused an unrelated identity or workspace")
		}
	})
	if repo.HasWorkspace(owned.Workspace.URLKey) {
		t.Fatal("owned workspace was not cleaned up")
	}
	if _, _, err := repo.Login(t.Context(), owned.Viewer.Email, "p04-fixture-password"); err == nil {
		t.Fatal("owned account still authenticates after cleanup")
	}
	if session, _, err := repo.Login(t.Context(), anchor.Viewer.Email, "p04-fixture-password"); err != nil || session.User.ID != anchor.Viewer.ID {
		t.Fatalf("fixture changed unrelated credentials: %v", err)
	}
	if preserved, ok := repo.BootstrapFor(anchor.Workspace.URLKey); !ok || preserved.Workspace.Name != anchor.Workspace.Name {
		t.Fatal("fixture changed an unrelated workspace")
	}
}
