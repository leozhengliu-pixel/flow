package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func issuePointReadFixture(t testing.TB) (*SQLiteStore, IssueRecordQuery, domain.Issue) {
	t.Helper()
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "point-read.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	data := repo.Bootstrap()
	issue := data.Issues[0]
	issue.ID, issue.Identifier = "point-target", "POINT-1"
	issue.Team.ID = "point-public"
	issue.Project, issue.ParentID = nil, nil
	issue.Permissions = nil
	issue.Description = strings.Repeat("Detailed body, including final line.\n", 500)
	if err := repo.ImportIssues(context.Background(), data.Workspace.URLKey, []domain.Issue{issue}); err != nil {
		t.Fatal(err)
	}
	query := IssueRecordQuery{Workspace: data.Workspace.URLKey, Access: &IssueRecordAccess{UserID: "point-viewer", WorkspaceID: data.Workspace.ID, VisibleTeamIDs: []string{issue.Team.ID}}}
	return repo, query, issue
}

func TestAuthorizedIssuePointReadScopeAndAliases(t *testing.T) {
	repo, query, issue := issuePointReadFixture(t)
	private := issue
	private.ID = "point-private"
	private.Identifier = "POINT-2"
	private.Team.ID = "point-secret"
	shared := private
	shared.ID = "point-shared"
	shared.Identifier = "POINT-3"
	shared.Permissions = []domain.IssuePermission{{SubjectType: "user", SubjectID: "point-viewer", Role: "viewer"}}
	child := private
	child.ID = "point-child"
	child.Identifier = "POINT-4"
	child.ParentID = &shared.ID
	if err := repo.ImportIssues(t.Context(), query.Workspace, []domain.Issue{private, shared, child}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, id string
		allowed  []string
		want     string
	}{
		{"internal ID", issue.ID, nil, issue.ID}, {"case insensitive identifier", "point-1", nil, issue.ID},
		{"private team", private.ID, nil, ""}, {"private identifier", "point-2", nil, ""},
		{"explicit share", shared.ID, nil, shared.ID}, {"inherited share", child.Identifier, nil, child.ID},
		{"API key cannot bypass share", shared.ID, []string{issue.Team.ID}, ""},
		{"empty API key scope", issue.ID, []string{}, ""}, {"allowed API key", issue.ID, []string{issue.Team.ID}, issue.ID},
		{"missing ID", "point-missing", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := query
			q.AllowedTeamIDs = tc.allowed
			got, err := repo.AuthorizedIssueRecord(t.Context(), q, tc.id)
			id, idErr := repo.AuthorizedIssueRecordID(t.Context(), q, tc.id)
			if tc.want == "" {
				if !errors.Is(err, sql.ErrNoRows) || !errors.Is(idErr, sql.ErrNoRows) {
					t.Fatalf("unauthorized point read: issue=%s err=%v identity=%s err=%v", got.ID, err, id, idErr)
				}
				return
			}
			if err != nil || idErr != nil || got.ID != tc.want || id != tc.want || got.Description != issue.Description {
				t.Fatalf("incomplete or wrong point read: id=%s err=%v identity=%s err=%v body bytes=%d", got.ID, err, id, idErr, len(got.Description))
			}
		})
	}
	// Denied records must fail authorization before malformed private JSON is decoded.
	if _, err := repo.db.ExecContext(t.Context(), `UPDATE issue_records SET data='invalid private JSON' WHERE workspace_key=? AND id=?`, query.Workspace, private.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AuthorizedIssueRecord(t.Context(), query, private.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("decoded denied issue: %v", err)
	}
	query.Workspace = "other-workspace"
	if _, err := repo.AuthorizedIssueRecord(t.Context(), query, issue.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cross-workspace point read: %v", err)
	}
}

func TestIssuePointReadIndependentOfCorruptHistory(t *testing.T) {
	repo, query, issue := issuePointReadFixture(t)
	for _, kind := range []string{"comment", "activity"} {
		for _, id := range []string{issue.ID, "unrelated-issue"} {
			if _, err := repo.db.ExecContext(t.Context(), `INSERT INTO workspace_content_records(workspace_key,kind,resource_id,id,created_at,data) VALUES(?,?,?,?,?,'invalid history JSON')`, query.Workspace, kind, id, kind+"-"+id, "2026-09-10T00:00:00Z"); err != nil {
				t.Fatal(err)
			}
		}
	}
	got, err := repo.AuthorizedIssueRecord(t.Context(), query, issue.Identifier)
	if err != nil || got.Description != issue.Description {
		t.Fatalf("history blocked issue body: %v", err)
	}
	if _, err := repo.IssueHistoryPage(t.Context(), query.Workspace, issue.ID, "", ""); err == nil {
		t.Fatal("corrupt history unexpectedly succeeded")
	}
	if _, err := repo.db.ExecContext(t.Context(), `DELETE FROM workspace_content_records WHERE workspace_key=? AND resource_id=?`, query.Workspace, issue.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.IssueHistoryPage(t.Context(), query.Workspace, issue.ID, "", ""); err != nil {
		t.Fatalf("unrelated history blocked target history: %v", err)
	}
}

func TestAuthorizedIssuePointReadUsesIDAndIdentifierIndexes(t *testing.T) {
	repo, query, issue := issuePointReadFixture(t)
	for _, admin := range []bool{false, true} {
		query.Access.Admin = admin
		statement, args, err := authorizedIssueStatement(query, issue.ID, "i.data")
		if err != nil {
			t.Fatal(err)
		}
		rows, err := repo.db.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+statement, args...)
		if err != nil {
			t.Fatal(err)
		}
		plan := []string{}
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		text := strings.Join(plan, "\n")
		t.Logf("admin=%v query plan:\n%s", admin, text)
		if !strings.Contains(text, "workspace_key=? AND id=?") || !strings.Contains(text, "workspace_key=? AND identifier=?") || strings.Contains(text, "SCAN i") {
			t.Fatalf("point read lost unique lookups:\n%s", text)
		}
	}
}

func BenchmarkAuthorizedIssuePointRead(b *testing.B) {
	for _, count := range []int{0, 75000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			repo, query, issue := issuePointReadFixture(b)
			if _, err := repo.db.ExecContext(context.Background(), `WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<?) INSERT INTO issue_records(workspace_key,id,identifier,team_id,state_id,state_type,priority,assignee_id,project_id,creator_id,cycle_id,parent_id,sort_order,title,archived,version,created_at,updated_at,collection_order,data) SELECT 'test-workspace','scale-'||x,'S-'||x,'point-public','','',0,'','','','','',0,'',0,0,'','',x,'{}' FROM n WHERE x<=?`, count, count); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				got, err := repo.AuthorizedIssueRecord(context.Background(), query, issue.Identifier)
				if err != nil || got.ID != issue.ID {
					b.Fatalf("point read: %v", err)
				}
			}
		})
	}
}
