//go:build integration

package store

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/coordination"
	"flow/api/internal/domain"

	"github.com/alicebob/miniredis/v2"
)

func TestExternalMetadataSearchIndexLifecycle(t *testing.T) {
	driver, databaseURL := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL")
	if driver == "" || databaseURL == "" {
		t.Skip("isolated external database required")
	}
	repo, err := OpenDatabase(DatabaseConfig{Driver: driver, URL: databaseURL, MaxOpenConns: 4, MaxIdleConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithColdWork(context.Background(), "search integration fixture")
	workspace := "search-index-integration"
	_ = repo.DeleteWorkspace(ctx, workspace)
	data, err := repo.CreateWorkspace(ctx, "Search index test", workspace, "us")
	if err != nil {
		repo.Close()
		t.Fatal(err)
	}
	project := domain.Project{ID: "indexed-project", Name: "NeedleProject", TeamIDs: []string{data.Teams[0].ID}, Status: domain.ProjectStatus{Type: "started"}}
	if err := repo.MutateWorkspace(ctx, workspace, "search.integration", project.ID, nil, func(next *domain.Bootstrap) error {
		next.Projects = append(next.Projects, project)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	q := SearchMetadataQuery{Scope: IssueRecordQuery{Workspace: workspace, Filter: IssueFilter{Field: "statusType", Values: []string{"started"}}}, Types: map[string]bool{"project": true}, Terms: []string{"NeedleProject"}, Limit: 10}
	result, err := repo.SearchMetadata(ctx, data, q)
	if err != nil || len(result.Projects) != 1 {
		repo.Close()
		t.Fatalf("insert index missing: %+v %v", result.Projects, err)
	}
	if _, err := repo.db.ExecContext(ctx, `UPDATE metadata_search_documents SET content='stale'
		WHERE workspace_key=? AND field='projects' AND record_key=?`, workspace, project.ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	repo, err = OpenDatabase(DatabaseConfig{Driver: driver, URL: databaseURL, MaxOpenConns: 4, MaxIdleConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	result, err = repo.SearchMetadata(ctx, data, q)
	if err != nil || len(result.Projects) != 1 {
		t.Fatalf("startup failed to repair an existing search document: %v", err)
	}
	project.Name = "ChangedNeedle"
	if err := repo.MutateWorkspace(ctx, workspace, "search.integration", project.ID, nil, func(next *domain.Bootstrap) error {
		for i := range next.Projects {
			if next.Projects[i].ID == project.ID {
				next.Projects[i] = project
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	result, err = repo.SearchMetadata(ctx, data, q)
	if err != nil || len(result.Projects) != 0 {
		t.Fatalf("update retained old index: %+v %v", result.Projects, err)
	}
	q.Terms = []string{"ChangedNeedle"}
	result, err = repo.SearchMetadata(ctx, data, q)
	if err != nil || len(result.Projects) != 1 {
		t.Fatalf("update index missing: %+v %v", result.Projects, err)
	}
	if err := repo.MutateWorkspace(ctx, workspace, "search.integration", project.ID, nil, func(next *domain.Bootstrap) error {
		next.Projects = slices.DeleteFunc(next.Projects, func(item domain.Project) bool { return item.ID == project.ID })
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	result, err = repo.SearchMetadata(ctx, data, q)
	if err != nil || len(result.Projects) != 0 {
		t.Fatalf("delete index retained: %+v %v", result.Projects, err)
	}
}

func TestExternalMetadataSearchCacheDifferential(t *testing.T) {
	if os.Getenv("FLOW_TEST_DATABASE_DRIVER") == "" || os.Getenv("FLOW_TEST_DATABASE_URL") == "" {
		t.Skip("isolated external database required")
	}
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	repo := openPreparationDatabase(t, preparationDatabase(t))
	defer repo.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	const workspace = "search-batched-integration"
	policy, err := repo.CreateWorkspace(ctx, "Batched search", workspace, "us")
	if err != nil {
		t.Fatal(err)
	}
	team := policy.Teams[0].ID
	stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	err = repo.MutateWorkspace(ctx, workspace, "search.integration", "", nil, func(next *domain.Bootstrap) error {
		next.Teams = append(next.Teams, domain.Team{ID: "private", Key: "PRV", Name: "Private", Private: true})
		for i := range 70 {
			next.Projects = append(next.Projects,
				domain.Project{ID: fmt.Sprintf("private-newer-%03d", i), Name: "A zephyr", TeamIDs: []string{"private"}, UpdatedAt: stamp.Add(time.Hour)},
				domain.Project{ID: fmt.Sprintf("private-older-%03d", i), Name: "ZZ zephyr", TeamIDs: []string{"private"}, UpdatedAt: stamp.Add(-time.Hour)})
		}
		next.Projects = append(next.Projects,
			domain.Project{ID: "shared-key", Name: "Matches only in documents", TeamIDs: []string{team}},
			domain.Project{ID: "Visible-1", Name: "Z ZEPHYR", TeamIDs: []string{team}, UpdatedAt: stamp},
			domain.Project{ID: "visible-2", Name: "Z zephyr", TeamIDs: []string{team}, UpdatedAt: stamp},
			domain.Project{ID: "archived", Name: "B zephyr", TeamIDs: []string{team}, ArchivedAt: &stamp})
		next.Documents = append(next.Documents, domain.Document{
			ID: "shared-key", Title: "Zephyr", Content: strings.Repeat("界", 2048) + "truncated",
			Color: "null", Creator: next.Viewer, TeamIDs: []string{team}, UpdatedAt: stamp,
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := repo.metadataCatalogPass(ctx, catalogRevisit)
	if err != nil || len(report.Skipped) != 0 {
		t.Fatalf("catalog preparation: %v, skipped: %v", err, report.Skipped)
	}
	view, err := repo.BoundedMetadataViewForTesting(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantColor := "null"
	if repo.dialect == "mysql" {
		wantColor = ""
		if hits, err := view.metadataSearchHits(ctx, workspace, "zephyr", []string{"projects", "documents"}, 1); err != nil || hits != nil {
			t.Fatalf("over-limit search retained a partial hit set: %v", err)
		}
	}
	redis := miniredis.RunT(t)
	coordinator, err := coordination.Open(ctx, coordination.Config{Mode: "standalone", Addrs: []string{redis.Addr()}, Cache: coordination.SharedCacheConfig{OpTimeout: time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	defer coordinator.Close()
	repo.SetWorkspaceCoordinator(coordinator)
	view.SetWorkspaceCoordinator(coordinator)
	// Validate cache fills while the candidate snapshot owns the only connection.
	repo.db.SetMaxOpenConns(1)

	for _, order := range []struct{ sort, direction string }{{"", ""}, {"", "asc"}, {"title", "asc"}} {
		q := SearchMetadataQuery{
			Scope: IssueRecordQuery{Workspace: workspace, Sort: order.sort, Direction: order.direction, AllowedTeamIDs: []string{team}, Access: &IssueRecordAccess{VisibleTeamIDs: []string{team}, Admin: true}},
			Types: map[string]bool{"project": true, "document": true, "view": true}, Terms: []string{"zephyr"}, Limit: 2, SkipSharedCache: true,
		}
		want, err := repo.SearchMetadata(ctx, policy, q)
		if err != nil {
			t.Fatal(err)
		}
		if len(want.Projects) != 2 || want.Projects[0].ID != "Visible-1" || want.Projects[1].ID != "visible-2" || len(want.Documents) != 1 {
			t.Fatalf("bad reference results for order %+v: %d projects and %d documents", order, len(want.Projects), len(want.Documents))
		}
		if want.Documents[0].Content != strings.Repeat("界", 2048) || want.Documents[0].Color != wantColor {
			t.Fatalf("unexpected reference projection for order %+v", order)
		}
		// Observe this query before testing the version validation on a fill.
		q.SkipSharedCache = false
		for range 2 {
			if got, err := repo.SearchMetadata(ctx, policy, q); err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("observing search for order %+v changed the result: %v", order, err)
			}
		}
		for _, reader := range []*SQLiteStore{repo, view} {
			redis.FlushAll()
			for _, phase := range []struct {
				name string
				skip bool
				hits int64
			}{{"disabled", true, 0}, {"fill", false, 0}, {"hit", false, 3}} {
				q.SkipSharedCache = phase.skip
				hits := coordination.SharedCacheCounters()["redisCacheSearchHits"]
				matches, pages := 0, 0
				phaseCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				queryCtx := WithSQLStatementObserver(phaseCtx, func(statement string) {
					if strings.Contains(statement, mysqlMetadataTermMatch) {
						matches++
					}
					if strings.HasPrefix(strings.TrimSpace(statement), "SELECT p.record_key,p.sort_value,") {
						pages++
					}
				})
				got, err := reader.SearchMetadata(queryCtx, policy, q)
				cancel()
				if err != nil || !reflect.DeepEqual(got, want) {
					t.Fatalf("search differs for order %+v bounded=%t cache=%s: %v", order, reader.BoundedMetadataEnabled(), phase.name, err)
				}
				if got := coordination.SharedCacheCounters()["redisCacheSearchHits"] - hits; got != phase.hits {
					t.Fatalf("order %+v bounded=%t cache=%s: %d cache hits, want %d", order, reader.BoundedMetadataEnabled(), phase.name, got, phase.hits)
				}
				if repo.dialect == "mysql" && reader.BoundedMetadataEnabled() {
					wantMatches := 1
					if phase.hits > 0 {
						wantMatches = 0
					}
					if matches != wantMatches {
						t.Fatalf("order %+v cache=%s: %d MySQL candidate lookups, want %d", order, phase.name, matches, wantMatches)
					}
					if phase.hits == 0 && pages != 2 || phase.hits > 0 && pages != 0 {
						t.Fatalf("order %+v cache=%s: %d MySQL catalog pages", order, phase.name, pages)
					}
				}
			}
		}
	}
}
