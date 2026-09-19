package store

import (
	"testing"
	"time"

	"flow/api/internal/coordination"
	"flow/api/internal/domain"

	"github.com/alicebob/miniredis/v2"
)

func TestHotCacheIssueRecordHitAndInvalidation(t *testing.T) {
	repo := openCachedStore(t)
	ctx := t.Context()
	data := repo.Bootstrap()
	key, issue := data.Workspace.URLKey, data.Issues[0]
	first, err := repo.IssueRecord(ctx, key, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	cache := repo.hotCache()
	if cache == nil {
		t.Fatal("redis cache was not attached")
	}
	raw, err := cache.CacheGet(ctx, cache.CacheKey(key, "issue", issue.ID))
	if err != nil || len(raw) == 0 {
		t.Fatal("issue record was not written to Redis")
	}
	cached, err := repo.IssueRecord(ctx, key, issue.ID)
	if err != nil || cached.Title != first.Title {
		t.Fatalf("cache miss after first read: %+v %v", cached, err)
	}
	if err := repo.MutateWorkspace(ctx, key, "issue.updated", issue.ID, map[string]string{"title": "cached-new"}, func(next *domain.Bootstrap) error {
		for i := range next.Issues {
			if next.Issues[i].ID == issue.ID {
				next.Issues[i].Title = "cached-new"
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	updated, err := repo.IssueRecord(ctx, key, issue.ID)
	if err != nil || updated.Title != "cached-new" {
		t.Fatalf("write did not invalidate issue cache: %+v %v", updated, err)
	}
}

func TestHotCacheIssueQueryInvalidation(t *testing.T) {
	repo := openCachedStore(t)
	ctx := t.Context()
	data := repo.Bootstrap()
	query := IssueRecordQuery{Workspace: data.Workspace.URLKey, Limit: 50, Summary: true}
	first, err := repo.QueryIssueRecords(ctx, query)
	if err != nil || len(first.Items) == 0 {
		t.Fatalf("query = %+v %v", first, err)
	}
	if err := repo.MutateWorkspace(ctx, query.Workspace, "issue.updated", first.Items[0].ID, map[string]string{"title": "query-new"}, func(next *domain.Bootstrap) error {
		for i := range next.Issues {
			if next.Issues[i].ID == first.Items[0].ID {
				next.Issues[i].Title = "query-new"
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	second, err := repo.QueryIssueRecords(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range second.Items {
		if item.ID == first.Items[0].ID && item.Title == "query-new" {
			found = true
		}
	}
	if !found {
		t.Fatalf("issue query cache was not invalidated: %+v", second.Items)
	}
}

func TestHotCacheProjectDirectoryUsesSnapshot(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(t.TempDir() + "/projects.db")
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	data := repo.Bootstrap()
	page, err := repo.QueryProjectDirectory(t.Context(), ProjectRecordQuery{Workspace: data.Workspace.URLKey, Limit: 20, IncludeTotal: true})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total >= 0 && int(page.Total) < len(page.Items) {
		t.Fatalf("project directory total=%d items=%d", page.Total, len(page.Items))
	}
}

func TestHotCacheDisabledStoreStillReads(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(t.TempDir() + "/nocache.db")
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	data := repo.Bootstrap()
	if _, err := repo.IssueRecord(t.Context(), data.Workspace.URLKey, data.Issues[0].ID); err != nil {
		t.Fatal(err)
	}
}

func openCachedStore(t *testing.T) *SQLiteStore {
	t.Helper()
	repo, err := OpenSQLiteTestFixture(t.TempDir() + "/cache.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	server := miniredis.RunT(t)
	coordinator, err := coordination.Open(t.Context(), coordination.Config{Mode: "standalone", Addrs: []string{server.Addr()}, Prefix: "cache-test", ConnectTimeout: time.Second, LockTTL: time.Second, LockWait: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { coordinator.Close() })
	repo.SetWorkspaceCoordinator(coordinator)
	return repo
}

func TestHotCacheMetadataReloadUsesRedis(t *testing.T) {
	repo := openCachedStore(t)
	ctx := t.Context()
	data := repo.Bootstrap()
	if err := repo.ReloadWorkspace(ctx, data.Workspace.URLKey); err != nil {
		t.Fatal(err)
	}
	if err := repo.ReloadWorkspace(ctx, data.Workspace.URLKey); err != nil {
		t.Fatal(err)
	}
	reloaded, ok := repo.WorkspaceMetadata(data.Workspace.URLKey)
	if !ok || len(reloaded.Teams) == 0 {
		t.Fatal("metadata cache reload lost teams")
	}
}

func TestHotCacheTouchClassification(t *testing.T) {
	issue, query, project, meta := hotCacheTouch("issue.updated")
	if !issue || !query || project || meta {
		t.Fatalf("issue.updated touch = %v %v %v %v", issue, query, project, meta)
	}
	_, query, project, meta = hotCacheTouch("team.created")
	if !query || !project || meta {
		t.Fatalf("team.created should keep metadata hash ready")
	}
}

func TestHotCacheIssueIdentifierInvalidation(t *testing.T) {
	repo := openCachedStore(t)
	ctx := t.Context()
	data := repo.Bootstrap()
	key, issue := data.Workspace.URLKey, data.Issues[0]
	if issue.Identifier == "" {
		t.Fatal("fixture issue has no identifier")
	}
	if _, err := repo.IssueRecord(ctx, key, issue.Identifier); err != nil {
		t.Fatal(err)
	}
	cache := repo.hotCache()
	raw, err := cache.CacheGet(ctx, cache.CacheKey(key, "issue", issue.Identifier))
	if err != nil || len(raw) == 0 {
		t.Fatal("identifier alias was not cached")
	}
	if err := repo.MutateWorkspace(ctx, key, "issue.updated", issue.ID, map[string]string{"title": "ident-new"}, func(next *domain.Bootstrap) error {
		for i := range next.Issues {
			if next.Issues[i].ID == issue.ID {
				next.Issues[i].Title = "ident-new"
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	updated, err := repo.IssueRecord(ctx, key, issue.Identifier)
	if err != nil || updated.Title != "ident-new" {
		t.Fatalf("identifier cache survived UUID write: %+v %v", updated, err)
	}
}

func TestHotCacheCreateIssueBumpsQueryGeneration(t *testing.T) {
	repo := openCachedStore(t)
	ctx := t.Context()
	data := repo.Bootstrap()
	workspace := data.Workspace.URLKey
	query := IssueRecordQuery{Workspace: workspace, Limit: 50, Summary: true}
	if _, err := repo.QueryIssueRecords(ctx, query); err != nil {
		t.Fatal(err)
	}
	before := repo.cacheGeneration(ctx, repo.issueQueryGenKey(workspace))
	base := data.Issues[0]
	id := "cache-created-issue"
	err := repo.MutateWorkspaceWithAggregate(WithIssueRecordMutations(ctx), workspace, "issue.created", map[string]any{}, func(next *domain.Bootstrap) (string, error) {
		issue := base
		issue.ID = id
		issue.Identifier = "CACHE-1"
		issue.Number = next.NextIssueNumber
		issue.Title = "created-through-records"
		issue.SubIssueIDs = []string{}
		next.Issues = append(next.Issues, issue)
		return id, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	after := repo.cacheGeneration(ctx, repo.issueQueryGenKey(workspace))
	if after <= before {
		t.Fatalf("createIssueRecords did not bump query generation: before=%d after=%d", before, after)
	}
	page, err := repo.QueryIssueRecords(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range page.Items {
		if item.ID == id {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("created issue missing from query after gen bump: %+v", page.Items)
	}
}

func TestHotCacheMetadataHashReplaceDropsDeletedFields(t *testing.T) {
	repo := openCachedStore(t)
	ctx := t.Context()
	workspace := repo.Bootstrap().Workspace.URLKey
	repo.fillMetadataRecordsCache(ctx, workspace, []metadataCacheRow{
		{Field: "teams", Key: "keep", Order: 1, Data: []byte(`{"id":"keep"}`)},
		{Field: "teams", Key: "gone", Order: 2, Data: []byte(`{"id":"gone"}`)},
	})
	repo.fillMetadataRecordsCache(ctx, workspace, []metadataCacheRow{
		{Field: "teams", Key: "keep", Order: 1, Data: []byte(`{"id":"keep"}`)},
	})
	values, err := repo.hotCache().CacheHGetAll(ctx, repo.metadataFieldKey(workspace, "teams"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := values["gone"]; ok {
		t.Fatalf("deleted metadata hash field survived refill: %#v", values)
	}
	if _, ok := values["keep"]; !ok {
		t.Fatalf("kept metadata hash field was dropped: %#v", values)
	}
}

func TestHotCacheTeamCreatePreservesCollectionOrder(t *testing.T) {
	repo := openCachedStore(t)
	ctx := t.Context()
	data := repo.Bootstrap()
	workspace := data.Workspace.URLKey
	if err := repo.ReloadWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	createScopedTeam(t, repo, workspace, "ordered-team")
	values, err := repo.hotCache().CacheHGetAll(ctx, repo.metadataFieldKey(workspace, "teams"))
	if err != nil {
		t.Fatal(err)
	}
	order, _ := decodeMetadataCacheValue(values["ordered-team"])
	if _, ok := values["ordered-team"]; !ok {
		t.Fatalf("created team missing from metadata hash: %#v", values)
	}
	if order == 0 && len(values) > 1 {
		t.Fatalf("created team was cached at collection_order=0 among %d teams", len(values))
	}
}

func TestHotCacheQueryGenerationBumpsOnWrite(t *testing.T) {
	repo := openCachedStore(t)
	ctx := t.Context()
	data := repo.Bootstrap()
	query := IssueRecordQuery{Workspace: data.Workspace.URLKey, Limit: 50, Summary: true}
	if _, err := repo.QueryIssueRecords(ctx, query); err != nil {
		t.Fatal(err)
	}
	before := repo.cacheGeneration(ctx, repo.issueQueryGenKey(query.Workspace))
	if err := repo.MutateWorkspace(ctx, query.Workspace, "issue.updated", data.Issues[0].ID, map[string]string{"title": "bump"}, func(next *domain.Bootstrap) error {
		next.Issues[0].Title = "bump"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	after := repo.cacheGeneration(ctx, repo.issueQueryGenKey(query.Workspace))
	if after <= before {
		t.Fatalf("query generation was not bumped: before=%d after=%d", before, after)
	}
}
