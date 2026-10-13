package store

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// importPagingIssues stores count issues in one state with colliding sort keys
// (sort order, priority, timestamps and titles repeat) so that the id tie
// break is exercised in every ordering.
func importPagingIssues(t *testing.T, count int) (*SQLiteStore, string) {
	t.Helper()
	repository, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "paging.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	data := repository.Bootstrap()
	workspace := data.Workspace.URLKey
	base := data.Issues[0]
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	issues := make([]domain.Issue, count)
	for i := range issues {
		issue := base
		issue.ID = fmt.Sprintf("back-%05d", i)
		issue.Identifier = fmt.Sprintf("BACK-%d", i)
		issue.Title = fmt.Sprintf("Title %02d", i%17)
		issue.SortOrder = float64(i / 3)
		issue.Priority = i % 5
		issue.State.ID = "back-state"
		issue.CreatedAt = start.Add(time.Duration(i/2) * time.Minute)
		issue.UpdatedAt = start.Add(time.Duration((i*7)%41) * time.Minute)
		issues[i] = issue
	}
	for from := 0; from < len(issues); from += 1000 {
		if err := repository.ImportIssues(context.Background(), workspace, issues[from:min(from+1000, len(issues))]); err != nil {
			t.Fatal(err)
		}
	}
	return repository, workspace
}

func pageIDs(page IssueRecordPage) []string {
	ids := make([]string, len(page.Items))
	for i, issue := range page.Items {
		ids[i] = issue.ID
	}
	return ids
}

func TestIssueRecordsPageBackwardMatchesForward(t *testing.T) {
	const total, size = 233, 37
	repository, workspace := importPagingIssues(t, total)
	ctx := context.Background()
	filter := IssueFilter{Field: "status", Values: []string{"back-state"}}
	for _, sortField := range []string{"sortOrder", "createdAt", "updatedAt", "priority", "title"} {
		for _, direction := range []string{"asc", "desc"} {
			t.Run(sortField+"/"+direction, func(t *testing.T) {
				query := func(cursor string, reverse bool, limit int) IssueRecordPage {
					t.Helper()
					page, err := repository.QueryIssueRecords(ctx, IssueRecordQuery{Workspace: workspace, Filter: filter, Sort: sortField, Direction: direction, Limit: limit, Cursor: cursor, Reverse: reverse, Cursors: true, IncludeTotal: true})
					if err != nil {
						t.Fatal(err)
					}
					if page.Total != total {
						t.Fatalf("total %d", page.Total)
					}
					return page
				}
				// Forward walk: the reference order, with every page's cursors.
				var all []string
				var pages []IssueRecordPage
				cursor := ""
				for {
					page := query(cursor, false, size)
					pages = append(pages, page)
					all = append(all, pageIDs(page)...)
					if page.HasPrevious != (cursor != "") {
						t.Fatalf("forward page %d hasPrevious=%v", len(pages), page.HasPrevious)
					}
					if page.StartCursor == "" || page.EndCursor == "" {
						t.Fatalf("page %d lacks cursors: %#v", len(pages), page)
					}
					if !page.HasMore {
						break
					}
					if page.NextCursor != page.EndCursor {
						t.Fatalf("nextCursor %q differs from endCursor %q", page.NextCursor, page.EndCursor)
					}
					cursor = page.EndCursor
				}
				distinct := map[string]bool{}
				for _, id := range all {
					distinct[id] = true
				}
				if len(all) != total || len(distinct) != total {
					t.Fatalf("forward walk returned %d rows (%d distinct)", len(all), len(distinct))
				}

				// Backward walk from the end reproduces the same order.
				var backward []string
				page := query("", true, size)
				if page.HasMore {
					t.Fatalf("last page claims rows after it: %#v", page)
				}
				if !slices.Equal(pageIDs(page), all[len(all)-len(page.Items):]) {
					t.Fatalf("last page = %v, want the tail of the order", pageIDs(page))
				}
				backward = append(pageIDs(page), backward...)
				for steps := 0; page.HasPrevious; steps++ {
					if steps > total {
						t.Fatal("backward walk does not terminate")
					}
					page = query(page.StartCursor, true, size)
					if !page.HasMore {
						t.Fatalf("backward page lost its following rows: %#v", page)
					}
					backward = append(pageIDs(page), backward...)
				}
				if !slices.Equal(backward, all) {
					t.Fatalf("backward walk differs from forward walk:\n%v\n%v", backward, all)
				}

				// A cursor from a forward page works in both directions: the
				// page before page N+1 is page N.
				for index := 1; index < len(pages); index++ {
					before := query(pages[index].StartCursor, true, size)
					if !slices.Equal(pageIDs(before), pageIDs(pages[index-1])) {
						t.Fatalf("page before %d = %v, want %v", index+1, pageIDs(before), pageIDs(pages[index-1]))
					}
					if before.HasPrevious != (index > 1) || !before.HasMore {
						t.Fatalf("page before %d flags: hasPrevious=%v hasMore=%v", index+1, before.HasPrevious, before.HasMore)
					}
					after := query(pages[index-1].EndCursor, false, size)
					if !slices.Equal(pageIDs(after), pageIDs(pages[index])) {
						t.Fatalf("page after %d differs", index)
					}
				}

				// A short window before a middle row.
				window := query(pages[3].StartCursor, true, 5)
				if len(window.Items) != 5 || !slices.Equal(pageIDs(window), all[3*size-5:3*size]) {
					t.Fatalf("window = %v, want %v", pageIDs(window), all[3*size-5:3*size])
				}
			})
		}
	}
}

func TestIssueRecordsBackwardWithoutRowsAndLegacyShape(t *testing.T) {
	repository, workspace := importPagingIssues(t, 12)
	ctx := context.Background()
	filter := IssueFilter{Field: "status", Values: []string{"back-state"}}

	// The cursors never reach legacy responses: no startCursor, endCursor or hasPrevious.
	legacy, err := repository.QueryIssueRecords(ctx, IssueRecordQuery{Workspace: workspace, Filter: filter, Limit: 5, Sort: "createdAt", Direction: "desc"})
	if err != nil {
		t.Fatal(err)
	}
	if legacy.StartCursor != "" || legacy.EndCursor != "" || legacy.HasPrevious || !legacy.HasMore || legacy.NextCursor == "" {
		t.Fatalf("legacy page = %#v", legacy)
	}
	// The legacy cursor is the same token the connection form hands out.
	connection, err := repository.QueryIssueRecords(ctx, IssueRecordQuery{Workspace: workspace, Filter: filter, Limit: 5, Sort: "createdAt", Direction: "desc", Cursors: true})
	if err != nil || connection.EndCursor != legacy.NextCursor {
		t.Fatalf("connection end cursor %q, legacy next cursor %q (%v)", connection.EndCursor, legacy.NextCursor, err)
	}

	// Before the very first row there is nothing.
	first := connection
	empty, err := repository.QueryIssueRecords(ctx, IssueRecordQuery{Workspace: workspace, Filter: filter, Limit: 5, Sort: "createdAt", Direction: "desc", Cursor: first.StartCursor, Reverse: true, Cursors: true})
	if err != nil || len(empty.Items) != 0 || empty.HasPrevious || !empty.HasMore || empty.StartCursor != "" {
		t.Fatalf("page before the first row = %#v (%v)", empty, err)
	}

	// A cursor is bound to its query: it cannot page another filter backward.
	if _, err := repository.QueryIssueRecords(ctx, IssueRecordQuery{Workspace: workspace, Filter: IssueFilter{Field: "status", Values: []string{"other"}}, Limit: 5, Sort: "createdAt", Direction: "desc", Cursor: first.EndCursor, Reverse: true, Cursors: true}); err == nil {
		t.Fatal("cursor accepted for a different filter")
	}
	// Archived filtering still applies in reverse.
	archived, err := repository.QueryIssueRecords(ctx, IssueRecordQuery{Workspace: workspace, Filter: filter, Limit: 5, Archived: "true", Reverse: true, Cursors: true})
	if err != nil || len(archived.Items) != 0 {
		t.Fatalf("archived backward page = %#v (%v)", archived, err)
	}
}

func TestIssueRecordsBackwardLargeWorkspace(t *testing.T) {
	if testing.Short() {
		t.Skip("large workspace")
	}
	repository, workspace := importPagingIssues(t, 20000)
	ctx := context.Background()
	filter := IssueFilter{Field: "status", Values: []string{"back-state"}}
	started := time.Now()
	last, err := repository.QueryIssueRecords(ctx, IssueRecordQuery{Workspace: workspace, Filter: filter, Sort: "createdAt", Direction: "desc", Limit: 100, Reverse: true, Cursors: true})
	if err != nil || len(last.Items) != 100 || !last.HasPrevious {
		t.Fatalf("last page = %d items, hasPrevious=%v (%v)", len(last.Items), last.HasPrevious, err)
	}
	previous, err := repository.QueryIssueRecords(ctx, IssueRecordQuery{Workspace: workspace, Filter: filter, Sort: "createdAt", Direction: "desc", Limit: 100, Cursor: last.StartCursor, Reverse: true, Cursors: true})
	if err != nil || len(previous.Items) != 100 {
		t.Fatalf("previous page = %d items (%v)", len(previous.Items), err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("two backward pages of a 20k workspace took %s", elapsed)
	}
}
