package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21SW18IssueSearchMigrationDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	prefix := idgen.New("sw18-index-")
	stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	issues := make([]domain.Issue, 80)
	for i := range issues {
		issues[i] = domain.Issue{
			ID: fmt.Sprintf("%s-%03d", prefix, i), Identifier: fmt.Sprintf("SW18-%d", i),
			Title:       "Unicode 数据\nquotes \" and backslash \\",
			Description: strings.Repeat("完整正文 数据 \n", 1200) + " tail-only-needle",
			Team:        domain.Team{ID: prefix + "-team", Key: "SW18"},
			State:       domain.WorkflowState{ID: prefix + "-state", Type: "started"},
			Creator:     f.data.Viewer, CreatedAt: stamp, UpdatedAt: stamp,
		}
	}
	issues[0].Description = ""
	if f.dialect == "sqlite" {
		issues[2].Title = "embedded\x00null 数据 after"
	}
	issues[1].Description = strings.Repeat("legal large body 数据 ", 30000) + " tail-only-needle"
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21_sw18.seed", "", nil, func(data *domain.Bootstrap) error {
		data.Issues = issues
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// Each store belongs to this test. Limit the startup operation to its
	// uniquely owned workspace on persistent external test databases.
	f.mu.Lock()
	f.workspaces = map[string]domain.Bootstrap{workspace: f.workspaces[workspace]}
	f.mu.Unlock()
	view.mu.Lock()
	view.workspaces = map[string]domain.Bootstrap{workspace: view.workspaces[workspace]}
	view.mu.Unlock()
	hash := func(table, columns string) [32]byte {
		t.Helper()
		rows, err := f.db.QueryContext(t.Context(), `SELECT `+columns+` FROM `+table+` WHERE workspace_key=? ORDER BY id`, workspace)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		h := sha256.New()
		for rows.Next() {
			var id string
			var raw []byte
			if err := rows.Scan(&id, &raw); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(h, "%s:", id)
			_, _ = h.Write(raw)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return [32]byte(h.Sum(nil))
	}
	original := hash("issue_records", "id,data")
	readIndex := func() map[string]string {
		t.Helper()
		rows, err := f.db.QueryContext(t.Context(), `SELECT issue_id,content FROM issue_search_documents WHERE workspace_key=? ORDER BY issue_id`, workspace)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		result := map[string]string{}
		for rows.Next() {
			var id, text string
			if err := rows.Scan(&id, &text); err != nil {
				t.Fatal(err)
			}
			result[id] = text
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return result
	}
	reset := func(last string) {
		t.Helper()
		if _, err := f.db.ExecContext(t.Context(), `DELETE FROM issue_search_documents WHERE workspace_key=?`, workspace); err != nil {
			t.Fatal(err)
		}
		if _, err := f.db.ExecContext(t.Context(), `INSERT INTO issue_search_migrations(workspace_key,last_id,complete) VALUES(?,?,0) ON CONFLICT(workspace_key) DO UPDATE SET last_id=excluded.last_id,complete=0`, workspace, last); err != nil {
			t.Fatal(err)
		}
	}
	var events int
	view.SetRealtimeSink(func(string, domain.RealtimeEvent) { events++ })
	view.SetWebhookSink(func(string, domain.DomainEvent) { events++ })
	for _, limit := range []int{1, 9, 128, 0} {
		t.Run(fmt.Sprintf("characters=%d", limit), func(t *testing.T) {
			f.db.searchMaxChars = limit
			for _, source := range []*SQLiteStore{f.SQLiteStore, view} {
				reset("")
				runtime.GC()
				var before, after runtime.MemStats
				runtime.ReadMemStats(&before)
				statements := 0
				ctx := WithSQLStatementObserver(t.Context(), func(string) { statements++ })
				if err := source.migrateIssueSearchIndex(ctx); err != nil {
					t.Fatal(err)
				}
				runtime.ReadMemStats(&after)
				t.Logf("bounded=%t limit=%d allocated-bytes=%d allocations=%d sql-statements=%d", source.BoundedMetadataEnabled(), limit, after.TotalAlloc-before.TotalAlloc, after.Mallocs-before.Mallocs, statements)
				index := readIndex()
				if len(index) != len(issues) {
					t.Fatalf("indexed records=%d want=%d", len(index), len(issues))
				}
				for _, issue := range issues {
					want := capSearchText(StripNUL(issue.Identifier+"\n"+issue.Title+"\n"+issue.Description), limit)
					if index[issue.ID] != want {
						t.Fatalf("search text differs bounded=%t limit=%d issue=%s got=%q want=%q", source.BoundedMetadataEnabled(), limit, issue.ID, index[issue.ID][:min(170, len(index[issue.ID]))], want[:min(170, len(want))])
					}
				}
				var last string
				var complete int
				if err := f.db.QueryRowContext(t.Context(), `SELECT last_id,complete FROM issue_search_migrations WHERE workspace_key=?`, workspace).Scan(&last, &complete); err != nil || complete != 1 || last != issues[len(issues)-1].ID {
					t.Fatalf("checkpoint last=%q complete=%d err=%v", last, complete, err)
				}
				if err := source.migrateIssueSearchIndex(t.Context()); err != nil {
					t.Fatal("completed migration is not idempotent", err)
				}
				if _, err := f.db.ExecContext(t.Context(), `UPDATE issue_search_documents SET content=LOWER(content) WHERE workspace_key=?`, workspace); err != nil {
					t.Fatal(err)
				}
				if _, err := f.db.ExecContext(t.Context(), `UPDATE issue_search_migrations SET last_id='',complete=0 WHERE workspace_key=?`, workspace); err != nil {
					t.Fatal(err)
				}
				if err := source.migrateIssueSearchIndex(t.Context()); err != nil {
					t.Fatal("repairing case-only index changes failed", err)
				}
				if repaired := readIndex(); len(repaired) != len(index) {
					t.Fatal("case-only repair lost index records")
				} else {
					for id, text := range index {
						if repaired[id] != text {
							t.Fatal("case-only index changes were ignored")
						}
					}
				}
				if got := hash("issue_records", "id,data"); got != original {
					t.Fatal("index migration changed canonical issue data")
				}
			}
		})
	}
	for _, source := range []*SQLiteStore{f.SQLiteStore, view} {
		reset("")
		oldLimit := f.db.maxTransactionBytes
		f.db.maxTransactionBytes = 200 << 10
		err := source.migrateIssueSearchIndex(t.Context())
		f.db.maxTransactionBytes = oldLimit
		if !errors.Is(err, ErrTransactionWriteBudget) {
			t.Fatalf("large index transaction bypassed write budget: %v", err)
		}
		if got := readIndex(); len(got) != 0 {
			t.Fatal("failed index batch was partially committed")
		}
	}
	reset(issues[63].ID)
	if err := view.migrateIssueSearchIndex(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := readIndex(); len(got) != len(issues)-64 {
		t.Fatalf("resumed migration indexed %d records, want %d", len(got), len(issues)-64)
	}
	reset("")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := view.migrateIssueSearchIndex(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled migration: %v", err)
	}
	if got := readIndex(); len(got) != 0 {
		t.Fatal("cancelled migration changed its owned index")
	}
	if err := view.migrateIssueSearchIndex(t.Context()); err != nil {
		t.Fatal("migration did not recover after cancellation", err)
	}
	if view.WorkspaceMetadataDecodes() != 0 || view.metadataResidency.work.used != 0 || events != 0 {
		t.Fatal("search migration decoded metadata, leaked work admission or published an event")
	}
}
