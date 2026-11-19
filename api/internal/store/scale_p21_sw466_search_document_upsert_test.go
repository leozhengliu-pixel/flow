package store

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

// MySQL 8.4 drops the rows that a multi-row INSERT ... ON DUPLICATE KEY
// UPDATE changes from an InnoDB FULLTEXT index: MATCH no longer finds them.
// Triage moves renumber issues, so their search documents change in one
// multi-row write, and later candidate searches missed them (SW440/441/442/
// 463 on MySQL: a later generation scored fewer candidates than an earlier
// one). Every dialect must still find each issue after its document is
// rewritten by the issue index sync (several documents in one write, next to
// unchanged and new ones) and by the bounded search-index migration.
func TestScaleP21SW466SearchDocumentsStayFindableAfterRewrites(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	prefix, workspace := idgen.New("sw466-"), f.data.Workspace.URLKey
	at := time.Date(2026, 11, 18, 1, 2, 3, 0, time.UTC)
	var issues []domain.Issue
	for i := range 12 {
		issues = append(issues, domain.Issue{
			ID: fmt.Sprintf("%s%02d", prefix, i), Identifier: fmt.Sprintf("SWA-%d", 5000+i), Number: 5000 + i, Version: 1,
			Title: fmt.Sprintf("Kestrel01 Falcon01 note%04d", i), Team: f.data.Teams[0],
			State: domain.WorkflowState{ID: "state_backlog", Name: "Backlog", Type: "backlog"}, Creator: f.data.Viewer,
			CreatedAt: at, UpdatedAt: at.Add(time.Duration(i) * time.Second),
		})
	}
	if err := f.ImportIssues(t.Context(), workspace, issues); err != nil {
		t.Fatal(err)
	}
	found := func(stage string) {
		t.Helper()
		var ids []string
		if err := f.SearchIssueCandidateTerms(t.Context(), IssueRecordQuery{Workspace: workspace, Archived: "all"}, []string{"Kestrel01"}, nil, 100, func(issue domain.Issue) error {
			ids = append(ids, issue.ID)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		slices.Sort(ids)
		var want []string
		for _, issue := range issues {
			want = append(want, issue.ID)
		}
		if !slices.Equal(ids, want) {
			t.Fatalf("%s: the search found %d of %d issues: %v", stage, len(ids), len(want), ids)
		}
	}
	found("imported")
	// Three documents change in one write, the rest stay, as a triage team
	// move renumbers some issues of a batch.
	for _, index := range []int{2, 5, 9} {
		issues[index].Identifier = fmt.Sprintf("SWB-%d", 8200+index)
	}
	tx, err := f.db.BeginTx(t.Context(), f.recordTxOptions())
	if err != nil {
		t.Fatal(err)
	}
	if err := writeIssueSearchIndex(t.Context(), tx, workspace, issues); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	found("renumbered by the index sync")
	// The bounded migration rewrites documents that differ from their rows.
	for _, index := range []int{1, 4, 7, 10} {
		issues[index].Identifier = fmt.Sprintf("SWC-%d", 9100+index)
		raw, err := json.Marshal(issues[index])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.db.ExecContext(t.Context(), `UPDATE issue_records SET data=?,identifier=? WHERE workspace_key=? AND id=?`, raw, issues[index].Identifier, workspace, issues[index].ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.db.ExecContext(t.Context(), `UPDATE issue_search_migrations SET last_id='',complete=0 WHERE workspace_key=?`, workspace); err != nil {
		t.Fatal(err)
	}
	if err := f.scaleP21SW18MigrateIssueSearchIndex(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, index := range []int{1, 4, 7, 10} {
		var content string
		if err := f.db.QueryRowContext(t.Context(), `SELECT content FROM issue_search_documents WHERE workspace_key=? AND issue_id=?`, workspace, issues[index].ID).Scan(&content); err != nil {
			t.Fatal(err)
		}
		if want := issues[index].Identifier + "\n" + issues[index].Title + "\n"; content != want {
			t.Fatalf("migration left %q, want %q", content, want)
		}
	}
	found("rewritten by the migration")
}
