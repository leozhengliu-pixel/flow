package store

import (
	"path/filepath"
	"strings"
	"testing"
)

// The shared-issue visibility CTE finds each shared issue's sub-issues through
// the parent index. Its recursive step once made issue_records the outer loop
// on SQLite and scanned every issue of the workspace once per shared issue, so
// a member with a few hundred issue grants paid seconds per issue request.
func TestSharedIssueVisibilityReadsChildrenThroughTheParentIndex(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "shared.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	prefix, args := issueAccessCTE(IssueRecordQuery{
		Workspace: "test-workspace",
		Access:    &IssueRecordAccess{UserID: "usr_member", WorkspaceID: "workspace", VisibleTeamIDs: []string{"team_test"}},
	})
	rows, err := repo.db.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+prefix+"SELECT COUNT(*) FROM shared_issues", args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	plan, recursive := "", ""
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan += detail + "\n"
		if recursive != "" || detail == "RECURSIVE STEP" {
			recursive += detail + "\n"
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(recursive, "SEARCH child USING COVERING INDEX issue_records_parent_idx (workspace_key=? AND parent_id=?)") {
		t.Fatalf("the recursive step does not look children up by parent:\n%s", plan)
	}
}
