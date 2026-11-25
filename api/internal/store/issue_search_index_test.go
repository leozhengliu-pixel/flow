package store

import (
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestIssueSearchCandidateProjectionDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	issues := issueSearchProjectionIssues(f.data, 7)
	ids := make([]string, len(issues))
	for i, issue := range issues {
		ids[i] = issue.ID
	}
	private := domain.Team{ID: "search-projection-private", Key: "QZV", Private: true}
	issues[1].Team, issues[2].Team, issues[6].Team = private, private, private
	issues[1].Permissions = []domain.IssuePermission{{
		ID: "search-projection-permission", IssueID: ids[1], SubjectType: "user", SubjectID: f.data.Viewer.ID, Role: "viewer",
	}}
	issues[2].ParentID = &ids[1]
	issues[6].UpdatedAt = issues[6].UpdatedAt.Add(time.Minute)
	if err := f.ImportIssues(t.Context(), workspace, issues); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	f.db.SetMaxOpenConns(1)
	args := []any{workspace, 5}
	q := IssueRecordQuery{Workspace: workspace, Access: &IssueRecordAccess{
		UserID: f.data.Viewer.ID, WorkspaceID: f.data.Workspace.ID, VisibleTeamIDs: []string{f.data.Teams[0].ID},
	}}
	checkPage := func(q IssueRecordQuery, wantIDs []string, mysqlQueries int) {
		t.Helper()
		var original []domain.Issue
		for _, repo := range []*SQLiteStore{f.SQLiteStore, view} {
			var got []domain.Issue
			var gotIDs, statements []string
			ctx := WithSQLStatementObserver(t.Context(), func(query string) {
				if strings.Contains(query, "SELECT output.list_data,") {
					statements = append(statements, query)
				}
			})
			if err := repo.SearchIssueCandidates(ctx, q, issueSearchProjectionTerm, nil, 5, func(issue domain.Issue) error {
				got = append(got, issue)
				gotIDs = append(gotIDs, issue.ID)
				return nil
			}); err != nil || !slices.Equal(gotIDs, wantIDs) {
				t.Fatalf("full-text candidate page got %v, want %v, err=%v", gotIDs, wantIDs, err)
			}
			if repo.dialect == "mysql" && (len(statements) != mysqlQueries || !strings.Contains(statements[0], "SELECT output.list_data,NULL FROM")) {
				t.Fatalf("unexpected mysql candidate statements: %v", statements)
			}
			if repo == view && !reflect.DeepEqual(original, got) {
				t.Fatal("candidate projections differ with bounded metadata")
			}
			original = got
		}
	}
	check := func(wantQueries int) {
		t.Helper()
		want, err := f.readIssueSearchProjections(t.Context(), "", issueSearchProjectionTestFrom, args, false)
		if err != nil {
			t.Fatal(err)
		}
		var statements []string
		ctx := WithSQLStatementObserver(t.Context(), func(query string) { statements = append(statements, query) })
		got, err := f.readIssueSearchProjections(ctx, "", issueSearchProjectionTestFrom, args, true)
		if err != nil || !reflect.DeepEqual(want, got) {
			t.Fatalf("list projection and original differ: got %d rows, want %d, err=%v", len(got), len(want), err)
		}
		if len(statements) != wantQueries || !strings.HasPrefix(statements[0], "SELECT output.list_data,NULL FROM") ||
			wantQueries == 2 && !strings.HasPrefix(statements[1], "SELECT output.list_data,output.data FROM") {
			t.Fatalf("unexpected projection statements: %v", statements)
		}
	}
	check(1)
	checkPage(q, ids[:5], 1)
	for i, raw := range [][]byte{nil, []byte(`{`), []byte(`[]`)} {
		if _, err := f.db.ExecContext(t.Context(), `UPDATE issue_records SET list_data=? WHERE workspace_key=? AND id=?`, raw, workspace, ids[i+1]); err != nil {
			t.Fatal(err)
		}
	}
	// Invalid JSON falls back to the canonical row. Valid JSON with the wrong
	// shape is skipped without filling its place from beyond the page limit.
	check(2)
	checkPage(q, []string{ids[0], ids[1], ids[2], ids[4]}, 2)
	q.AllowedTeamIDs, q.AllowedTeamsAppKey = []string{f.data.Teams[0].ID}, true
	checkPage(q, []string{ids[0], ids[4], ids[5]}, 1)
}

const (
	issueSearchProjectionTerm     = "Kestrelprojection"
	issueSearchProjectionTestFrom = ` FROM issue_records output WHERE output.workspace_key=? AND output.id LIKE 'search-projection-%' ORDER BY output.updated_at DESC,output.id LIMIT ?`
)

func issueSearchProjectionIssues(data domain.Bootstrap, count int) []domain.Issue {
	issues := make([]domain.Issue, count)
	stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	body := strings.Repeat("Issue description text. ", 120)
	for i := range issues {
		issues[i] = domain.Issue{
			ID: fmt.Sprintf("search-projection-%03d", i), Identifier: fmt.Sprintf("QZP-%d", i+1), Title: issueSearchProjectionTerm, Description: body,
			Team: data.Teams[0], State: data.States[0], Creator: data.Viewer, CreatedAt: stamp, UpdatedAt: stamp,
		}
	}
	return issues
}

func TestIssueSearchCandidateProjectionTiming(t *testing.T) {
	if os.Getenv("FLOW_BENCH_ISSUE_PROJECTION") != "1" {
		t.Skip("set FLOW_BENCH_ISSUE_PROJECTION=1 to time search projections")
	}
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	f := scaleP09Database(t)
	const size = 120 // The default search request uses 30 results and 4x candidates.
	workspace := f.data.Workspace.URLKey
	if err := f.ImportIssues(t.Context(), workspace, issueSearchProjectionIssues(f.data, 2*size)); err != nil {
		t.Fatal(err)
	}
	q := IssueRecordQuery{
		Workspace: workspace, Archived: "false", Filter: IssueFilter{And: []IssueFilter{{}}},
		Access: &IssueRecordAccess{
			UserID: f.data.Viewer.ID, WorkspaceID: f.data.Workspace.ID, VisibleTeamIDs: []string{f.data.Teams[0].ID},
		},
	}
	var statement string
	ctx := WithSQLStatementObserver(t.Context(), func(query string) {
		if strings.Contains(query, "SELECT output.list_data,") {
			if statement != "" {
				t.Fatal("expected a single candidate projection query")
			}
			statement = query
		}
	})
	if err := f.SearchIssueCandidates(ctx, q, issueSearchProjectionTerm, nil, size, func(domain.Issue) error { return nil }); err != nil {
		t.Fatal(err)
	}
	prefix, projected, _ := strings.Cut(statement, "SELECT output.list_data,")
	body, source, ok := strings.Cut(projected, " FROM (")
	if !ok || !strings.HasPrefix(prefix, "WITH RECURSIVE shared_issues") {
		t.Fatalf("expected the shared-issue search query, got %q", statement)
	}
	if f.dialect == "mysql" {
		if body != "NULL" {
			t.Fatalf("candidate query selected %s instead of NULL", body)
		}
		for _, part := range []string{
			"FORCE INDEX (issue_records_search_candidates_idx)",
			"MATCH(s.content) AGAINST (? IN BOOLEAN MODE)",
			"FORCE INDEX (issue_records_search_visibility_idx)",
		} {
			if !strings.Contains(source, part) {
				t.Fatalf("measured MySQL candidate query is missing %q", part)
			}
		}
	}
	// Bind the observed query using the same scope and single text term.
	_, args := issueAccessCTE(q)
	_, textArgs := f.issueTextVisibleSelection(q, issueSearchProjectionTerm)
	q.Access = nil // The full-text subquery already applies visibility.
	_, whereArgs, err := issueRecordWhere(q)
	if err != nil {
		t.Fatal(err)
	}
	if f.dialect == "postgres" {
		args = append(args, textArgs...)
		textArgs = nil
	}
	args = append(args, whereArgs...)
	args = append(args, textArgs...)
	args = append(args, issueSearchProjectionTerm, size, workspace, issueSearchProjectionTerm)
	from := " FROM (" + source
	want, err := f.readIssueSearchProjections(t.Context(), prefix, from, args, false)
	if err != nil || len(want) != size {
		t.Fatalf("original query returned %d rows, want %d, err=%v", len(want), size, err)
	}
	got, err := f.readIssueSearchProjections(t.Context(), prefix, from, args, true)
	if err != nil || !reflect.DeepEqual(want, got) {
		t.Fatalf("list-only query returned different projections, err=%v", err)
	}
	const samples = 40
	variants := []struct {
		name     string
		listOnly bool
		timings  []time.Duration
	}{{name: "with-body"}, {name: "list-only", listOnly: true}}
	for i := range samples {
		for j := range variants {
			variant := &variants[(i+j)%len(variants)]
			start := time.Now()
			items, err := f.readIssueSearchProjections(t.Context(), prefix, from, args, variant.listOnly)
			variant.timings = append(variant.timings, time.Since(start))
			if err != nil || len(items) != size {
				t.Fatalf("%s returned %d rows, want %d, err=%v", variant.name, len(items), size, err)
			}
		}
	}
	for _, variant := range variants {
		slices.Sort(variant.timings)
		t.Logf("%s %s: %d rows, %d samples, p50=%s p95=%s", f.dialect, variant.name, size, samples,
			variant.timings[samples/2-1], variant.timings[samples*95/100-1])
	}
}
