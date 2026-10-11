package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// Root and tab favorites of one release pipeline are separate records that
// survive a reopen in every dialect (SQLite always; MySQL or Postgres when
// FLOW_TEST_DATABASE_DRIVER and FLOW_TEST_DATABASE_URL are set), and removing
// one tab favorite leaves the root and the other tabs alone.
func TestFavoriteTabsPersistAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flow.db")
	configs := map[string]DatabaseConfig{
		"sqlite": {Driver: "sqlite", Path: path, FixtureProfile: "test", FixturePassword: "test-password", MaxOpenConns: 1},
	}
	if driver, url := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL"); driver != "" && url != "" {
		configs[driver] = DatabaseConfig{Driver: driver, URL: url, FixtureProfile: "test", FixturePassword: "test-password", MaxOpenConns: 4}
	}
	for name, config := range configs {
		t.Run(name, func(t *testing.T) { testFavoriteTabsPersistAcrossReopen(t, config) })
	}
}

func testFavoriteTabsPersistAcrossReopen(t *testing.T, config DatabaseConfig) {
	ctx := context.Background()
	const workspace = "test-workspace"
	repo, err := OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	viewer := repo.Bootstrap().Viewer.ID
	pipelineID := fmt.Sprintf("favorite-tab-pipeline-%d", time.Now().UnixNano())
	now := time.Now().UTC().Truncate(time.Millisecond)
	add := func(favorite domain.Favorite) {
		t.Helper()
		payload := map[string]string{"type": "release_pipeline"}
		if favorite.Tab != "" {
			payload["tab"] = favorite.Tab
		}
		if err := repo.MutateWorkspace(ctx, workspace, "favorite.added", pipelineID, payload, func(next *domain.Bootstrap) error {
			next.Favorites = append(next.Favorites, favorite)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	root := domain.Favorite{ID: pipelineID + "-root", UserID: viewer, ResourceType: "release_pipeline", ResourceID: pipelineID, Position: -1, CreatedAt: now}
	releases := domain.Favorite{ID: pipelineID + "-releases", UserID: viewer, ResourceType: "release_pipeline", ResourceID: pipelineID, Tab: "releases", Position: -2, CreatedAt: now.Add(time.Millisecond)}
	changelog := domain.Favorite{ID: pipelineID + "-changelog", UserID: viewer, ResourceType: "release_pipeline", ResourceID: pipelineID, Tab: "changelog", Position: -3, CreatedAt: now.Add(2 * time.Millisecond)}
	add(root)
	add(releases)
	add(changelog)
	if err := repo.MutateWorkspace(ctx, workspace, "favorite.removed", pipelineID, map[string]string{"type": "release_pipeline", "tab": "changelog"}, func(next *domain.Bootstrap) error {
		next.Favorites = slices.DeleteFunc(next.Favorites, func(item domain.Favorite) bool { return item.ID == changelog.ID })
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	loaded, ok := reopened.WorkspaceMetadata(workspace)
	if !ok {
		t.Fatal("workspace missing after reopen")
	}
	got := map[string]string{}
	for _, item := range loaded.Favorites {
		if item.ResourceID == pipelineID {
			got[item.ID] = item.Tab
		}
	}
	want := map[string]string{root.ID: "", releases.ID: "releases"}
	if len(got) != len(want) || got[root.ID] != "" || got[releases.ID] != "releases" {
		t.Fatalf("persisted pipeline favorites = %v, want %v", got, want)
	}
	if err := reopened.MutateWorkspace(ctx, workspace, "favorite.removed", pipelineID, map[string]string{"type": "release_pipeline"}, func(next *domain.Bootstrap) error {
		next.Favorites = slices.DeleteFunc(next.Favorites, func(item domain.Favorite) bool { return item.ResourceID == pipelineID })
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
