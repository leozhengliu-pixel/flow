package store

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// TestNestedIssueCountTimings times nested group counts against the full walk
// on a generated workspace of FLOW_NESTED_COUNT_ISSUES issues, right after the
// import (no index statistics) and after ANALYZE. SQLite by default; MySQL or
// Postgres with FLOW_TEST_DATABASE_DRIVER / FLOW_TEST_DATABASE_URL.
func TestNestedIssueCountTimings(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("FLOW_NESTED_COUNT_ISSUES"))
	if n <= 0 {
		t.Skip("set FLOW_NESTED_COUNT_ISSUES to time nested counts")
	}
	ctx := context.Background()
	driver, databaseURL := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL")
	var repository *SQLiteStore
	var err error
	var workspace, workspaceID string
	var base domain.Issue
	if driver == "" {
		driver = "sqlite"
		repository, err = OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer repository.Close()
		data := repository.Bootstrap()
		workspace, workspaceID, base = data.Workspace.URLKey, data.Workspace.ID, data.Issues[0]
	} else {
		repository, err = OpenDatabase(DatabaseConfig{Driver: driver, URL: databaseURL, MaxOpenConns: 4, MaxIdleConns: 2})
		if err != nil {
			t.Fatal(err)
		}
		// Deferred after Close, so the workspace is deleted first.
		defer repository.Close()
		workspace = "nested-count-timings"
		_ = repository.DeleteWorkspace(ctx, workspace)
		data, err := repository.CreateWorkspace(ctx, "Nested count timings", workspace, "us")
		if err != nil {
			t.Fatal(err)
		}
		defer repository.DeleteWorkspace(ctx, workspace)
		now := time.Now().UTC()
		workspaceID, base = data.Workspace.ID, domain.Issue{Team: data.Teams[0], State: data.States[0], Creator: data.Viewer, CreatedAt: now, UpdatedAt: now}
	}
	issues, shape := nestedCountIssues(rand.New(rand.NewSource(11)), base, "nct-", n)
	started := time.Now()
	importNestedCountIssues(t, repository, workspace, issues)
	nested := 0
	for _, issue := range issues {
		if issue.ParentID != nil {
			nested++
		}
	}
	t.Logf("%s: imported %d issues (%d sub-issues) in %s", driver, n, nested, time.Since(started).Round(time.Millisecond))
	issues = nil

	visible := []string{}
	for _, team := range shape.teams[:7] {
		visible = append(visible, team.ID)
	}
	member := &IssueRecordAccess{UserID: "nct-viewer", WorkspaceID: workspaceID, VisibleTeamIDs: visible}
	team := shape.teams[0].ID
	active := IssueFilter{Field: "statusType", Operator: "notIn", Values: []string{"completed", "canceled"}}
	views := []struct {
		name  string
		query IssueRecordQuery
	}{
		{"workspace, admin, status", IssueRecordQuery{GroupBy: "status"}},
		{"workspace, member, status", IssueRecordQuery{Access: member, GroupBy: "status"}},
		{"team, member, status", IssueRecordQuery{Access: member, TeamIDs: []string{team}, GroupBy: "status"}},
		{"team + sub-team, member, status", IssueRecordQuery{Access: member, TeamIDs: []string{team, shape.teams[3].ID}, GroupBy: "status"}},
		{"team active, member, status", IssueRecordQuery{Access: member, TeamIDs: []string{team}, Filter: active, GroupBy: "status"}},
		{"team, member, priority", IssueRecordQuery{Access: member, TeamIDs: []string{team}, GroupBy: "priority"}},
		{"team, member, assignee", IssueRecordQuery{Access: member, TeamIDs: []string{team}, GroupBy: "assignee"}},
		{"team, member, label", IssueRecordQuery{Access: member, TeamIDs: []string{team}, GroupBy: "label"}},
		{"team, member, none", IssueRecordQuery{Access: member, TeamIDs: []string{team}, GroupBy: "none"}},
		{"project, member, status", IssueRecordQuery{Access: member, ProjectIDs: []string{shape.projects[0]}, GroupBy: "status"}},
		{"my issues, member, status", IssueRecordQuery{Access: member, Filter: IssueFilter{Field: "assignee", Values: []string{shape.users[0]}}, GroupBy: "status"}},
		{"team, label or priority filter", IssueRecordQuery{Access: member, TeamIDs: []string{team}, Filter: IssueFilter{Or: []IssueFilter{{Field: "priority", Values: []string{"1", "2"}}, {Field: "labels", Values: []string{shape.labels[1]}}}}, GroupBy: "status"}},
		{"team, focus cases", IssueRecordQuery{Access: member, TeamIDs: []string{team}, GroupBy: "cases", GroupCases: []IssueGroupCase{{Value: "urgent", Filter: IssueFilter{Field: "priority", Values: []string{"1"}}}, {Value: "started", Filter: IssueFilter{Field: "statusType", Values: []string{"started"}}}}}},
	}
	// Up to five samples (fewer once they took 3s); a run over the 5s cap
	// is reported as the cap.
	const limit = 5 * time.Second
	median := func(run func(context.Context) error) string {
		samples := []time.Duration{}
		total := time.Duration(0)
		for i := 0; i < 5 && total < 3*time.Second; i++ {
			runCtx, cancel := context.WithTimeout(ctx, limit)
			start := time.Now()
			err := run(runCtx)
			cancel()
			elapsed := time.Since(start)
			if err != nil && elapsed >= limit {
				return fmt.Sprintf(">%d", limit.Milliseconds())
			}
			if err != nil {
				t.Fatal(err)
			}
			samples = append(samples, elapsed)
			total += elapsed
		}
		sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
		return fmt.Sprintf("%.1f", float64(samples[len(samples)/2].Microseconds())/1000)
	}
	measure := func(phase string) {
		lines := []string{fmt.Sprintf("%s, %d issues, %s:", driver, n, phase), "| view | walk (before) ms | nested, uncached ms | nested, cached ms | flat, uncached ms |", "|---|---:|---:|---:|---:|"}
		for _, view := range views {
			q := view.query
			q.Workspace, q.Archived, q.Nested = workspace, "false", true
			got, err := repository.QueryIssueGroups(ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			walkCtx, cancel := context.WithTimeout(ctx, limit)
			want, err := repository.nestedIssueGroupsByWalk(walkCtx, q)
			cancel()
			if err == nil && !slices.Equal(got, want) {
				t.Fatalf("%s: counts differ\n got %v\nwant %v", view.name, got, want)
			}
			if err != nil {
				// The walk did not finish: check against the corrected counts.
				if want, err = repository.nestedIssueGroupsByMoves(ctx, withoutRedundantIssueAccess(q)); err != nil || !slices.Equal(got, want) {
					t.Fatalf("%s: counts differ\n got %v\nwant %v (%v)", view.name, got, want, err)
				}
			}
			before := median(func(ctx context.Context) error { _, err := repository.nestedIssueGroupsByWalk(ctx, q); return err })
			// Uncached: what the first read after a write costs.
			after := median(func(ctx context.Context) error {
				_, err := repository.nestedIssueGroups(ctx, withoutRedundantIssueAccess(q))
				return err
			})
			cached := median(func(ctx context.Context) error { _, err := repository.QueryIssueGroups(ctx, q); return err })
			flat := q
			flat.Nested = false
			plain := median(func(ctx context.Context) error {
				_, err := repository.flatIssueGroups(ctx, withoutRedundantIssueAccess(flat))
				return err
			})
			lines = append(lines, fmt.Sprintf("| %s | %s | %s | %s | %s |", view.name, before, after, cached, plain))
		}
		t.Log(strings.Join(lines, "\n"))
		q := views[2].query
		q.Workspace, q.Archived, q.Nested = workspace, "false", true
		statement, args, err := repository.nestedIssueMovesSQL(withoutRedundantIssueAccess(q))
		if err != nil {
			t.Fatal(err)
		}
		explain := map[string]string{"sqlite": "EXPLAIN QUERY PLAN ", "mysql": "EXPLAIN ANALYZE ", "postgres": "EXPLAIN ANALYZE "}[driver]
		rows, err := repository.db.QueryContext(ctx, explain+statement, args...)
		if err != nil {
			t.Fatal(err)
		}
		plan := []string{}
		columns, _ := rows.Columns()
		for rows.Next() {
			values := make([]sql.RawBytes, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := rows.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, string(values[len(values)-1]))
		}
		rows.Close()
		t.Logf("%s plan of the team/status moves query:\n%s", phase, strings.Join(plan, "\n"))
	}
	measure("fresh import, no statistics")
	analyze := map[string]string{"sqlite": "ANALYZE", "mysql": "ANALYZE TABLE issue_records, issue_attribute_records, issue_scope_counts, issue_permission_records", "postgres": "ANALYZE"}[driver]
	if rows, err := repository.db.QueryContext(ctx, analyze); err != nil {
		t.Fatal(err)
	} else {
		for rows.Next() {
		}
		rows.Close()
	}
	measure("after ANALYZE")
}
