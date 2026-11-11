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

// This baseline also runs without loadtest, including the shared CI dialect
// commands. Compare full and metadata-only projections on the same records.
func TestScaleP01ReadProjectionBaseline(t *testing.T) {
	configs := map[string]DatabaseConfig{"sqlite": {Driver: "sqlite", Path: filepath.Join(t.TempDir(), "p01-projection.db"), MaxOpenConns: 1}}
	if dialect := strings.ToLower(strings.TrimSpace(os.Getenv("FLOW_TEST_DATABASE_DRIVER"))); dialect != "" {
		if os.Getenv("FLOW_TEST_DATABASE_URL") == "" {
			t.Fatal("FLOW_TEST_DATABASE_URL is required")
		}
		configs[dialect] = DatabaseConfig{Driver: dialect, URL: os.Getenv("FLOW_TEST_DATABASE_URL"), MaxOpenConns: 4}
		if dialect == "sqlite" {
			config := configs[dialect]
			config.Path = filepath.Clean(config.URL)
			if !filepath.IsAbs(config.Path) || (!strings.HasPrefix(config.Path, "/tmp/codex-scale-p01/") && !strings.HasPrefix(config.Path, "/private/tmp/codex-scale-p01/")) {
				t.Fatal("explicit SQLite test paths must be inside /tmp/codex-scale-p01")
			}
			config.MaxOpenConns = 1
			configs[dialect] = config
		}
	}
	for name, config := range configs {
		t.Run(name, func(t *testing.T) {
			repo, err := OpenDatabase(config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { repo.Close() })
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
			register := func(name string) domain.User {
				user, token, err := repo.Register(t.Context(), name, idgen.New("scale-p01-store-")+"@example.test", "p01-fixture-password")
				if err != nil {
					t.Fatal(err)
				}
				users = append(users, user)
				if err := repo.VerifyEmail(t.Context(), token); err != nil {
					t.Fatal(err)
				}
				user.EmailVerified = true
				return user
			}
			owner, member := register("P01 owner"), register("P01 member")
			ctx := ContextWithActor(t.Context(), owner)
			key := idgen.New("p01-projection-")
			data, err := repo.CreateWorkspaceWith(ctx, "P01 projection", key, "us", func(data *domain.Bootstrap) error {
				data.Projects = append(data.Projects,
					domain.Project{ID: "p01-nil", Name: "Nil collections", TeamIDs: []string{data.Teams[0].ID}, Description: "Complete detail", Comments: nil},
					domain.Project{ID: "p01-empty", Name: "Empty collections", TeamIDs: []string{data.Teams[0].ID}, Description: "Complete detail", Comments: []domain.Comment{}, Resources: []domain.ProjectResource{}},
				)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			workspace = data.Workspace.URLKey
			if err := repo.EnsureWorkspaceMembership(ctx, data.Workspace.ID, member.ID); err != nil {
				t.Fatal(err)
			}
			for _, userID := range []string{owner.ID, member.ID} {
				full, ok, err := repo.BootstrapForUser(ctx, key, userID)
				if err != nil || !ok {
					t.Fatal("full projection unavailable")
				}
				paged, err := repo.PagedWorkspaceMetadata(ctx, key, userID)
				if err != nil {
					t.Fatal(err)
				}
				want, err := json.Marshal(full.Projects)
				if err != nil {
					t.Fatal(err)
				}
				got, err := json.Marshal(paged.Projects)
				if err != nil {
					t.Fatal(err)
				}
				if string(want) != string(got) {
					t.Fatal("full/metadata project projection differs, including nil/empty/detail fields")
				}
			}
			if _, err := repo.PagedWorkspaceMetadata(ctx, key, "p01-nonmember"); err == nil {
				t.Fatal("nonmember received the projection")
			}
		})
	}
}
