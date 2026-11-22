package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// FLOW_BENCH_SEARCH_DOCS=20000 measures a document list over many matching rows.
// Add FLOW_BENCH_SEARCH_DOC_BYTES=262144 and FLOW_BENCH_SEARCH_LIMIT=120 for
// pages of large documents. FLOW_BENCH_SEARCH_BOUNDED=1 uses the bounded reader.
func TestMetadataSearchDocumentListTiming(t *testing.T) {
	n, bodyBytes, limit := 0, 0, 50
	fmt.Sscan(os.Getenv("FLOW_BENCH_SEARCH_DOCS"), &n)
	if n < 1 {
		t.Skip("set FLOW_BENCH_SEARCH_DOCS")
	}
	fmt.Sscan(os.Getenv("FLOW_BENCH_SEARCH_DOC_BYTES"), &bodyBytes)
	fmt.Sscan(os.Getenv("FLOW_BENCH_SEARCH_LIMIT"), &limit)
	if limit < 1 || limit > 500 {
		t.Fatalf("invalid search limit %d", limit)
	}
	body := "zephyr lorem ipsum dolor sit amet lorem ipsum"
	if bodyBytes > len(body) {
		body = strings.Repeat(body+" ", bodyBytes/(len(body)+1)+1)[:bodyBytes]
	}
	repo := openSearchOrderRepo(t)
	defer repo.Close()
	data := repo.Bootstrap()
	ctx := context.Background()
	policy, access, err := repo.IssueQueryAccess(ctx, data.Workspace.URLKey, data.Viewer.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("bench-doc-%06d", i)
		insertSearchMetadata(t, repo, data.Workspace.URLKey, "documents", id, map[string]any{"id": id, "title": fmt.Sprintf("Doc %d", i), "content": body, "icon": "x", "color": "#fff", "teamIds": []string{data.Teams[0].ID}, "updatedAt": fmt.Sprintf("2026-01-01T00:%02d:%02dZ", i/60%60, i%60), "createdAt": "2026-01-01T00:00:00Z"})
	}
	t.Cleanup(func() {
		_, _ = repo.db.ExecContext(ctx, `DELETE FROM workspace_metadata_records WHERE workspace_key=? AND field='documents' AND record_key LIKE 'bench-doc-%'`, data.Workspace.URLKey)
	})
	analyze := `ANALYZE`
	if repo.dialect == "mysql" {
		analyze = `ANALYZE TABLE workspace_metadata_records`
	}
	if rows, err := repo.db.QueryContext(ctx, analyze); err != nil {
		t.Fatal(err)
	} else {
		rows.Close()
	}
	reader := repo
	if os.Getenv("FLOW_BENCH_SEARCH_BOUNDED") == "1" {
		reader, err = repo.BoundedMetadataViewForTesting(ctx)
		if err != nil {
			t.Fatal(err)
		}
	}
	q := SearchMetadataQuery{Scope: IssueRecordQuery{Workspace: data.Workspace.URLKey, Access: &access}, Types: map[string]bool{"document": true}, Terms: []string{"zephyr"}, Limit: limit}
	times := []time.Duration{}
	for i := 0; i < 5; i++ {
		start := time.Now()
		got, err := reader.SearchMetadata(ctx, policy, q)
		times = append(times, time.Since(start))
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Documents) != min(n, limit) {
			t.Fatalf("returned %d documents, want %d", len(got.Documents), min(n, limit))
		}
		t.Logf("run %d docs=%d %s", i, len(got.Documents), times[i])
	}
	slices.Sort(times)
	t.Logf("median %s", times[len(times)/2])
}

// openSearchOrderRepo uses the external database when FLOW_TEST_DATABASE_* is set.
func openSearchOrderRepo(t *testing.T) *SQLiteStore {
	t.Helper()
	driver, url := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL")
	if driver == "" || url == "" {
		repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "search.db"))
		if err != nil {
			t.Fatal(err)
		}
		return repo
	}
	repo, err := OpenDatabase(DatabaseConfig{Driver: driver, URL: url, FixtureProfile: "test", FixturePassword: "test-password", MaxOpenConns: 4, MaxIdleConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	return repo
}
