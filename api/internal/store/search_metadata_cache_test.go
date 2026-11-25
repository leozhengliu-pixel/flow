package store

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"flow/api/internal/coordination"
	"flow/api/internal/domain"

	"github.com/alicebob/miniredis/v2"
)

func TestMetadataSearchCacheRetiresOnIndexOnlyRepair(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "search.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	redis := miniredis.RunT(t)
	coordinator, err := coordination.Open(t.Context(), coordination.Config{Mode: "standalone", Addrs: []string{redis.Addr()}, Cache: coordination.SharedCacheConfig{OpTimeout: time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = coordinator.Close() })
	repo.SetWorkspaceCoordinator(coordinator)
	data := repo.Account()
	workspace := repo.lastWorkspaceKey
	policy, access, err := repo.IssueQueryAccess(t.Context(), workspace, data.Viewer.ID)
	if err != nil {
		t.Fatal(err)
	}
	project := domain.Project{ID: "search-index-repair", Name: "repairneedle"}
	insertSearchMetadata(t, repo, workspace, "projects", project.ID, project)
	if _, err := repo.db.ExecContext(t.Context(), `DELETE FROM metadata_search_documents WHERE workspace_key=? AND field='projects' AND record_key=?`, workspace, project.ID); err != nil {
		t.Fatal(err)
	}
	query := SearchMetadataQuery{Scope: IssueRecordQuery{Workspace: workspace, Access: &access}, Types: map[string]bool{"project": true}, Terms: []string{project.Name}, Limit: 10}
	stampReads := 0
	ctx := WithSQLStatementObserver(t.Context(), func(statement string) {
		if strings.Contains(statement, "m.generation") && strings.Contains(statement, "record_versions") {
			stampReads++
		}
	})
	search := func(want int) {
		t.Helper()
		got, err := repo.SearchMetadata(ctx, policy, query)
		if err != nil || len(got.Projects) != want {
			t.Fatalf("search returned %d projects, want %d: %v", len(got.Projects), want, err)
		}
	}
	misses := coordination.SharedCacheCounters()["redisCacheSearchMisses"]
	search(0)
	search(0)
	if stampReads != 0 || coordination.SharedCacheCounters()["redisCacheSearchMisses"] != misses {
		t.Fatal("first two searches should bypass stamp SQL and Redis lookups")
	}
	search(0)
	hits := coordination.SharedCacheCounters()["redisCacheSearchHits"]
	search(0)
	if coordination.SharedCacheCounters()["redisCacheSearchHits"] != hits+1 {
		t.Fatal("the empty index result did not hit the cache")
	}
	if err := repo.writeMetadataSearchPage(t.Context(), workspace, "projects", []string{project.ID}); err != nil {
		t.Fatal(err)
	}
	search(1)
}
