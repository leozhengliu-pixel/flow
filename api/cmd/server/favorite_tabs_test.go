package main

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// favoriteTabTestStores opens the SQLite test fixture and, when
// FLOW_TEST_DATABASE_DRIVER and FLOW_TEST_DATABASE_URL name a throwaway MySQL
// or Postgres database, that database too.
func favoriteTabTestStores(t *testing.T) map[string]*store.SQLiteStore {
	t.Helper()
	stores := map[string]*store.SQLiteStore{}
	sqlite, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "favorite-tabs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlite.Close() })
	stores["sqlite"] = sqlite
	if driver, url := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL"); driver != "" && url != "" {
		external, err := store.OpenDatabase(store.DatabaseConfig{Driver: driver, URL: url, FixtureProfile: "test", FixturePassword: "test-password", MaxOpenConns: 4})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { external.Close() })
		stores[driver] = external
	}
	return stores
}

func TestFavoriteTabsHTTPRoundTrip(t *testing.T) {
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	for name, repo := range favoriteTabTestStores(t) {
		t.Run(name, func(t *testing.T) { testFavoriteTabsHTTPRoundTrip(t, repo) })
	}
}

func testFavoriteTabsHTTPRoundTrip(t *testing.T, repo *store.SQLiteStore) {
	ctx := context.Background()
	const workspace = "test-workspace"
	data := repo.Bootstrap()
	pipelineID := fmt.Sprintf("favorite-tab-pipeline-%d", time.Now().UnixNano())
	if err := repo.MutateWorkspace(ctx, workspace, "test.favorite_tabs", "", nil, func(next *domain.Bootstrap) error {
		next.ReleasePipelines = append(next.ReleasePipelines, domain.ReleasePipeline{ID: pipelineID, SlugID: pipelineID, Name: "Favorite tabs", TeamIDs: []string{data.Teams[0].ID}, Type: "scheduled"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	host := httptest.NewServer(newHandler(&server{store: repo, uploadPath: t.TempDir()}))
	defer host.Close()
	admin := authClient(t)
	authRequest[domain.AuthSession](t, admin, "POST", host.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", 200)
	url := host.URL + "/api/favorites/release_pipeline/" + pipelineID
	list := func() []domain.Favorite {
		preferences := authRequest[struct {
			Favorites []domain.Favorite `json:"favorites"`
		}](t, admin, "GET", host.URL+"/api/resource-preferences", nil, workspace, 200)
		return slices.DeleteFunc(preferences.Favorites, func(item domain.Favorite) bool { return item.ResourceID != pipelineID })
	}
	tabs := func(items []domain.Favorite) []string {
		values := []string{}
		for _, item := range items {
			values = append(values, item.Tab)
		}
		slices.Sort(values)
		return values
	}
	defer func() {
		for _, suffix := range []string{"", "?tab=releases", "?tab=changelog"} {
			authRequest[any](t, admin, "DELETE", url+suffix, nil, workspace, 204)
		}
	}()

	root := authRequest[domain.Favorite](t, admin, "PUT", url, nil, workspace, 200)
	changelog := authRequest[domain.Favorite](t, admin, "PUT", url+"?tab=changelog", nil, workspace, 200)
	releases := authRequest[domain.Favorite](t, admin, "PUT", url+"?tab=releases", nil, workspace, 200)
	if root.Tab != "" || changelog.Tab != "changelog" || releases.Tab != "releases" {
		t.Fatalf("created favorites carry the wrong tabs: root=%q changelog=%q releases=%q", root.Tab, changelog.Tab, releases.Tab)
	}
	if root.ID == changelog.ID || changelog.ID == releases.ID {
		t.Fatal("root and tab favorites must be separate records")
	}
	if again := authRequest[domain.Favorite](t, admin, "PUT", url+"?tab=changelog", nil, workspace, 200); again.ID != changelog.ID {
		t.Fatal("favoriting a tab twice created a duplicate")
	}
	if got := tabs(list()); !slices.Equal(got, []string{"", "changelog", "releases"}) {
		t.Fatalf("listed favorite tabs = %q", got)
	}
	bootstrap := authRequest[domain.Bootstrap](t, admin, "GET", host.URL+"/api/issue-records/bootstrap", nil, workspace, 200)
	if !slices.ContainsFunc(bootstrap.Favorites, func(item domain.Favorite) bool { return item.ID == changelog.ID && item.Tab == "changelog" }) {
		t.Fatal("bootstrap dropped the tab favorite or its tab")
	}

	moved := authRequest[domain.Favorite](t, admin, "PATCH", url+"?tab=changelog", map[string]any{"position": 7.5}, workspace, 200)
	if moved.ID != changelog.ID || moved.Position != 7.5 {
		t.Fatalf("PATCH with tab moved the wrong favorite: %+v", moved)
	}

	authRequest[any](t, admin, "DELETE", url+"?tab=changelog", nil, workspace, 204)
	if got := tabs(list()); !slices.Equal(got, []string{"", "releases"}) {
		t.Fatalf("deleting the changelog tab favorite left %q", got)
	}
	// Without a tab only the root favorite is removed.
	authRequest[any](t, admin, "DELETE", url, nil, workspace, 204)
	if got := tabs(list()); !slices.Equal(got, []string{"releases"}) {
		t.Fatalf("deleting the root favorite left %q", got)
	}

	for _, method := range []string{"PUT", "DELETE"} {
		authRequest[any](t, admin, method, url+"?tab=archive", nil, workspace, 400)
	}
	authRequest[any](t, admin, "PATCH", url+"?tab=archive", map[string]any{"position": 1}, workspace, 400)
	authRequest[any](t, admin, "PUT", host.URL+"/api/favorites/project/"+data.Projects[0].ID+"?tab=changelog", nil, workspace, 400)

	// The store holds the tab favorite with its tab.
	stored, ok := repo.WorkspaceMetadata(workspace)
	if !ok {
		t.Fatal("workspace metadata missing")
	}
	if !slices.ContainsFunc(stored.Favorites, func(item domain.Favorite) bool { return item.ID == releases.ID && item.Tab == "releases" }) {
		t.Fatal("stored favorites lost the releases tab favorite")
	}
}
