package store

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// The fixture pool holds exactly one connection, so any query issued while a
// previous result set is still open would block until the deadline.
func TestResourceCommentConnectionWorksWithOneConnection(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "comments.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	if stats := repo.db.Stats(); stats.MaxOpenConnections != 1 {
		t.Fatalf("fixture pool allows %d connections, want 1", stats.MaxOpenConnections)
	}
	workspace := repo.Bootstrap().Workspace.URLKey
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for index := 0; index < 5; index++ {
		created := base.Add(time.Duration(index) * time.Minute).Format(time.RFC3339Nano)
		raw, _ := json.Marshal(domain.Comment{ID: fmt.Sprintf("comment-%d", index), Body: fmt.Sprintf("body %d", index), CreatedAt: base.Add(time.Duration(index) * time.Minute)})
		if _, err := repo.db.ExecContext(t.Context(), `INSERT INTO workspace_content_records(workspace_key,kind,resource_id,id,created_at,data) VALUES(?,?,?,?,?,?)`, workspace, "comment", "issue-1", fmt.Sprintf("comment-%d", index), created, raw); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	first, err := repo.ResourceCommentConnection(ctx, workspace, "issue-1", nil, nil, 2, false)
	if err != nil || len(first.Comments) != 2 || first.Comments[0].ID != "comment-4" || !first.HasNext || first.HasPrevious {
		t.Fatalf("first page = %+v, %v", first, err)
	}
	next, err := repo.ResourceCommentConnection(ctx, workspace, "issue-1", &first.Keys[1], nil, 2, false)
	if err != nil || len(next.Comments) != 2 || next.Comments[0].ID != "comment-2" || !next.HasNext || !next.HasPrevious {
		t.Fatalf("second page = %+v, %v", next, err)
	}
	last, err := repo.ResourceCommentConnection(ctx, workspace, "issue-1", nil, nil, 2, true)
	if err != nil || len(last.Comments) != 2 || last.Comments[0].ID != "comment-1" || last.Comments[1].ID != "comment-0" || !last.HasPrevious || last.HasNext {
		t.Fatalf("backward page = %+v, %v", last, err)
	}
	everything, err := repo.ResourceCommentConnection(ctx, workspace, "issue-1", nil, nil, 50, false)
	if err != nil || len(everything.Comments) != 5 || everything.HasNext || everything.HasPrevious {
		t.Fatalf("single page = %+v, %v", everything, err)
	}
	ids, err := repo.AttachmentIssueIDs(ctx, IssueRecordQuery{Workspace: workspace}, "https://example.com/none", 10)
	if err != nil || len(ids) != 0 {
		t.Fatalf("attachment lookup = %v, %v", ids, err)
	}
	if _, err := repo.ExpiredFileUploads(ctx, time.Now(), 10); err != nil {
		t.Fatal(err)
	}
}
