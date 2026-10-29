package store

import (
	"context"
	"database/sql"
	"errors"
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

	"github.com/go-sql-driver/mysql"
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
	keep, reused := os.Getenv("FLOW_NESTED_COUNT_KEEP") != "", false
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
		data, found := repository.WorkspaceMetadata(workspace)
		// FLOW_NESTED_COUNT_KEEP keeps the generated workspace for the next
		// run, which reuses it.
		if found && keep && countIssueRows(t, repository, workspace) >= n {
			reused = true
		} else {
			_ = repository.DeleteWorkspace(ctx, workspace)
			if data, err = repository.CreateWorkspace(ctx, "Nested count timings", workspace, "us"); err != nil {
				t.Fatal(err)
			}
		}
		if !keep {
			defer repository.DeleteWorkspace(ctx, workspace)
		}
		now := time.Now().UTC()
		workspaceID, base = data.Workspace.ID, domain.Issue{Team: data.Teams[0], State: data.States[0], Creator: data.Viewer, CreatedAt: now, UpdatedAt: now}
	}
	issues, shape := nestedCountIssues(rand.New(rand.NewSource(11)), base, "nct-", n)
	started := time.Now()
	if !reused {
		importNestedCountIssues(t, repository, workspace, issues)
		tx, err := repository.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		for i := 0; i < n; i += 7 {
			if _, err := tx.ExecContext(ctx, `INSERT INTO customer_request_filter_records(workspace_key,request_id,issue_id,customer_id,important,project_id) VALUES(?,?,?,?,0,'')`, workspace, fmt.Sprintf("nct-request-%d", i), issues[i].ID, fmt.Sprintf("nct-customer-%d", i%10)); err != nil {
				t.Fatal(err)
			}
		}
		for i := 0; i < 9; i++ {
			if _, err := tx.ExecContext(ctx, `INSERT INTO customer_filter_records(workspace_key,customer_id,owner_id,status_value,tier_value,revenue,customer_size) VALUES(?,?,'','','',0,0)`, workspace, fmt.Sprintf("nct-customer-%d", i)); err != nil {
				t.Fatal(err)
			}
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
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
		{"team, member, project", IssueRecordQuery{Access: member, TeamIDs: []string{team}, GroupBy: "project"}},
		{"team, member, creator", IssueRecordQuery{Access: member, TeamIDs: []string{team}, GroupBy: "creator"}},
		{"team, member, cycle", IssueRecordQuery{Access: member, TeamIDs: []string{team}, GroupBy: "cycle"}},
		{"team, member, customer", IssueRecordQuery{Access: member, TeamIDs: []string{team}, GroupBy: "customer"}},
		{"team, project filter", IssueRecordQuery{Access: member, TeamIDs: []string{team}, Filter: IssueFilter{Field: "project", Operator: "isNotEmpty"}, GroupBy: "status"}},
		{"team, text filter", IssueRecordQuery{Access: member, TeamIDs: []string{team}, Text: "Issue 1", GroupBy: "status"}},
		{"team, search filter", IssueRecordQuery{Access: member, TeamIDs: []string{team}, SearchText: "Issue", GroupBy: "status"}},
		{"team, subscriber filter", IssueRecordQuery{Access: member, TeamIDs: []string{team}, Filter: IssueFilter{Field: "subscribers", Operator: "isNotEmpty"}, GroupBy: "status"}},
		{"team, attribute filter", IssueRecordQuery{Access: member, TeamIDs: []string{team}, Filter: IssueFilter{Field: "estimate", Operator: "gte", Values: []string{"3"}}, GroupBy: "status"}},
		{"project, member, status", IssueRecordQuery{Access: member, ProjectIDs: []string{shape.projects[0]}, GroupBy: "status"}},
		{"my issues, member, status", IssueRecordQuery{Access: member, Filter: IssueFilter{Field: "assignee", Values: []string{shape.users[0]}}, GroupBy: "status"}},
		{"team, label or priority filter", IssueRecordQuery{Access: member, TeamIDs: []string{team}, Filter: IssueFilter{Or: []IssueFilter{{Field: "priority", Values: []string{"1", "2"}}, {Field: "labels", Values: []string{shape.labels[1]}}}}, GroupBy: "status"}},
		{"team, focus cases", IssueRecordQuery{Access: member, TeamIDs: []string{team}, GroupBy: "cases", GroupCases: []IssueGroupCase{{Value: "urgent", Filter: IssueFilter{Field: "priority", Values: []string{"1"}}}, {Value: "started", Filter: IssueFilter{Field: "statusType", Values: []string{"started"}}}}}},
	}
	if os.Getenv("FLOW_NESTED_COUNT_COMPARE") != "" {
		// A server-side limit stops a pathological old full-text plan too;
		// cancelling only its client context can leave MySQL running it.
		if driver == "mysql" {
			repository.db.SetMaxOpenConns(1)
		}
		lines := []string{fmt.Sprintf("%s, %d issues, 5 paired uncached samples:", driver, n), "| view | before p50/p95 ms | after p50/p95 ms |", "|---|---:|---:|"}
		for _, view := range views {
			if selected := os.Getenv("FLOW_NESTED_COUNT_VIEW"); selected != "" && !strings.Contains(view.name, selected) {
				continue
			}
			if !slices.Contains([]string{"team, member, project", "team, member, creator", "team, member, cycle", "team, member, customer", "team, project filter", "team, text filter", "team, search filter", "team, subscriber filter", "team, attribute filter"}, view.name) {
				continue
			}
			q := view.query
			q.Workspace, q.Archived, q.Nested = workspace, "false", true
			q = withoutRedundantIssueAccess(q)
			samples := [2][]time.Duration{}
			capped := false
			for range 5 {
				var reference []IssueRecordGroup
				referenceOK := false
				for mode, run := range []func(context.Context, IssueRecordQuery) ([]IssueRecordGroup, error){repository.nestedIssueGroupsBeforeSelections, repository.nestedIssueGroups} {
					start := time.Now()
					groups, err := run(ctx, q)
					samples[mode] = append(samples[mode], time.Since(start))
					if err != nil {
						var mysqlErr *mysql.MySQLError
						if mode == 0 && errors.As(err, &mysqlErr) && mysqlErr.Number == 3024 {
							capped = true
							continue
						}
						t.Fatal(err)
					}
					if mode == 0 {
						reference = groups
						referenceOK = true
					} else if referenceOK && !slices.Equal(groups, reference) {
						t.Fatalf("%s counts differ: %v != %v", view.name, groups, reference)
					}
				}
			}
			percentiles := func(values []time.Duration) string {
				slices.Sort(values)
				return fmt.Sprintf("%.1f / %.1f", float64(values[len(values)/2].Microseconds())/1000, float64(values[len(values)-1].Microseconds())/1000)
			}
			before := percentiles(samples[0])
			if capped {
				before = "capped (>5000 ms)"
			}
			lines = append(lines, fmt.Sprintf("| %s | %s | %s |", view.name, before, percentiles(samples[1])))
		}
		t.Log(strings.Join(lines, "\n"))
		return
	}
	explainPlan := func(statement string, args []any) string {
		explain := map[string]string{"sqlite": "EXPLAIN QUERY PLAN ", "mysql": "EXPLAIN ANALYZE ", "postgres": "EXPLAIN (ANALYZE, BUFFERS) "}[driver]
		rows, err := repository.db.QueryContext(ctx, explain+statement, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
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
		return strings.Join(plan, "\n")
	}
	// FLOW_NESTED_COUNT_RECOMPUTE: only the uncached recompute (what the
	// first read after a write costs), 20 samples per view, and the plan of
	// the index read.
	if os.Getenv("FLOW_NESTED_COUNT_RECOMPUTE") != "" {
		lines := []string{fmt.Sprintf("%s, %d issues, uncached recompute:", driver, n), "| view | p50 ms | p95 ms | max ms |", "|---|---:|---:|---:|"}
		for _, view := range views {
			q := view.query
			q.Workspace, q.Archived, q.Nested = workspace, "false", true
			counted := withoutRedundantIssueAccess(q)
			samples := []time.Duration{}
			for i := 0; i < 20; i++ {
				start := time.Now()
				if _, err := repository.nestedIssueGroups(ctx, counted); err != nil {
					t.Fatal(err)
				}
				samples = append(samples, time.Since(start))
				if samples[0] > 2*time.Second {
					break
				}
			}
			sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
			at := func(p float64) float64 {
				return float64(samples[min(len(samples)-1, int(p*float64(len(samples))))].Microseconds()) / 1000
			}
			lines = append(lines, fmt.Sprintf("| %s | %.1f | %.1f | %.1f |", view.name, at(0.5), at(0.95), float64(samples[len(samples)-1].Microseconds())/1000))
		}
		t.Log(strings.Join(lines, "\n"))
		for _, view := range []int{0, 2} {
			q := views[view].query
			q.Workspace, q.Archived, q.Nested = workspace, "false", true
			if statement, args, ok, err := repository.nestedIssueMovesIndexSQL(ctx, withoutRedundantIssueAccess(q)); err != nil {
				t.Fatal(err)
			} else if ok {
				t.Logf("plan of the %s index read:\n%s", views[view].name, explainPlan(statement, args))
			}
		}
		return
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
		for _, view := range []int{2, 0} {
			q := views[view].query
			q.Workspace, q.Archived, q.Nested = workspace, "false", true
			counted := withoutRedundantIssueAccess(q)
			if statement, args, ok, err := repository.nestedIssueMovesIndexSQL(ctx, counted); err != nil {
				t.Fatal(err)
			} else if ok {
				t.Logf("%s plan of the %s index read:\n%s", phase, views[view].name, explainPlan(statement, args))
				continue
			}
			statement, args, err := repository.nestedIssueMovesSQL(counted)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("%s plan of the %s moves query:\n%s", phase, views[view].name, explainPlan(statement, args))
		}
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

// The selected benchmark views could use neither the counters nor the old
// forest plan: they counted in SQL and then walked the matching subtrees.
func (s *SQLiteStore) nestedIssueGroupsBeforeSelections(ctx context.Context, query IssueRecordQuery) ([]IssueRecordGroup, error) {
	if s.dialect == "mysql" && query.SearchText != "" {
		if _, err := s.db.ExecContext(ctx, `SET SESSION max_execution_time=5000`); err != nil {
			return nil, err
		}
		defer s.db.ExecContext(ctx, `SET SESSION max_execution_time=0`)
	}
	groups, err := s.flatIssueGroupsBySQL(ctx, query)
	if err != nil || len(groups) == 0 {
		return groups, err
	}
	moves, err := s.nestedIssueMoves(ctx, query)
	if err != nil {
		return nil, err
	}
	result := []IssueRecordGroup{}
	for _, group := range groups {
		group.Count += moves[group.Value]
		if group.Count > 0 {
			result = append(result, group)
		}
	}
	return result, nil
}
