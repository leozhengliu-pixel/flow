package store

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// An issue edit reads the activity and comment heads of the issue whose
// timeline it appends to, never those of the rest of the loaded family or
// workspace: its SQL statement count is the same at 1,000 and 23,000 issues
// and with 900 loaded siblings. FLOW_ACTIVITY_MEASURE=1 adds the 23,000 case.
func TestIssueActivityWriteSQLBudget(t *testing.T) {
	sizes := []int{1000}
	if os.Getenv("FLOW_ACTIVITY_MEASURE") == "1" {
		sizes = append(sizes, 23000)
	}
	const siblings = 900
	baseline := map[string]int{}
	for _, count := range sizes {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			repo, err := OpenSQLiteTestFixture(t.TempDir() + "/activity-cost.db")
			if err != nil {
				t.Fatal(err)
			}
			defer repo.Close()
			data := repo.Bootstrap()
			workspace, issue := data.Workspace.URLKey, data.Issues[0]
			issue.Project, issue.ParentID, issue.SubIssueIDs = nil, nil, nil
			issue.CreatedAt = time.Now().UTC().Add(-time.Hour)
			if _, err := repo.UpdateIssueRecord(t.Context(), workspace, issue.ID, nil, IssueMutationScope{}, func(_ *domain.Bootstrap, item *domain.Issue) error {
				*item = issue
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			// The filler issues are sub-issues of the edited issue's family
			// parent (the first siblings) or unrelated issues (the rest).
			parent := "activity-scale-parent"
			fill := count - len(data.Issues)
			if _, err := repo.db.ExecContext(t.Context(), `WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<?) INSERT INTO issue_records(workspace_key,id,identifier,team_id,state_id,state_type,priority,assignee_id,project_id,creator_id,cycle_id,parent_id,sort_order,title,archived,version,created_at,updated_at,collection_order,data) SELECT ?,'activity-scale-'||x,'ACT-SCALE-'||x,'','','',0,'','','','',CASE WHEN x<=? THEN ? ELSE '' END,0,'',0,0,'','',x,CASE WHEN x<=? THEN json_object('id','activity-scale-'||x,'identifier','ACT-SCALE-'||x,'title','Sibling','parentId',?) ELSE '{}' END FROM n WHERE x<=?`, fill, workspace, siblings, parent, siblings, parent, fill); err != nil {
				t.Fatal(err)
			}
			var seeded int
			if err := repo.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM issue_records WHERE workspace_key=?`, workspace).Scan(&seeded); err != nil || seeded != count {
				t.Fatalf("seeded issues=%d want=%d err=%v", seeded, count, err)
			}
			measure := func(name string, target string, scope IssueMutationScope, prepare func(*domain.Issue)) {
				statements, heads := 0, 0
				ctx := WithSQLStatementObserver(t.Context(), func(query string) {
					statements++
					if strings.Contains(query, "workspace_content_records") && strings.Contains(query, "ORDER BY created_at DESC,id DESC LIMIT 1") {
						heads++
					}
				})
				_, err := repo.UpdateIssueRecord(ctx, workspace, target, nil, scope, func(input *domain.Bootstrap, item *domain.Issue) error {
					prepare(item)
					before := item.Title
					item.Title = "Activity measured " + name
					RecordIssueActivity(input, item.ID, domain.ActivityEvent{ID: "activity-cost-" + name, Type: "issue.updated", Actor: input.Viewer, CreatedAt: time.Now().UTC(), Metadata: map[string]string{"titleBefore": before, "title": item.Title}})
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("ACTIVITY_WRITE_MEASURE scenario=%s issues=%d statements=%d head_lookups=%d", name, count, statements, heads)
				if heads != 2 {
					t.Fatalf("%s: one touched issue read %d history heads, want 2", name, heads)
				}
				if previous, ok := baseline[name]; ok && previous != statements {
					t.Fatalf("%s: write cost grew with the workspace: %d statements, %d at the smaller size", name, statements, previous)
				}
				baseline[name] = statements
			}
			measure("plain", issue.ID, IssueMutationScope{}, func(*domain.Issue) {})
			// A state edit of a sub-issue loads its parent's whole family.
			measure("family", "activity-scale-1", IssueMutationScope{IncludeFamily: true}, func(item *domain.Issue) {
				item.CreatedAt = time.Now().UTC().Add(-time.Hour)
			})
		})
	}
}
