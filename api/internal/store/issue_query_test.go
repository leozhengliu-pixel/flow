package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestIssueRecordsMigrateAndPageWithoutWorkspaceArray(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "flow.db")
	repository, err := OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	data := repository.Bootstrap()
	workspace := data.Workspace.URLKey
	base := data.Issues[0]
	var raw []byte
	if err := repository.db.QueryRowContext(ctx, `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var metadata domain.Bootstrap
	if err := json.Unmarshal(raw, &metadata); err != nil {
		t.Fatal(err)
	}
	if len(metadata.Issues) != 0 {
		t.Fatal("workspace JSON still contains issues")
	}
	issues := make([]domain.Issue, 350)
	for i := range issues {
		issue := base
		issue.ID = fmt.Sprintf("page-%04d", i)
		issue.Identifier = fmt.Sprintf("PAGE-%d", i)
		issue.SortOrder = float64(i / 2)
		issue.State.ID = "bulk-state"
		issue.Priority = i % 5
		issues[i] = issue
	}
	if err := repository.ImportIssues(ctx, workspace, issues); err != nil {
		t.Fatal(err)
	}
	filter := IssueFilter{Field: "status", Values: []string{"bulk-state"}}
	groups, err := repository.QueryIssueGroups(ctx, IssueRecordQuery{Workspace: workspace, Filter: filter, GroupBy: "priority"})
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 5 {
		t.Fatalf("groups: %#v", groups)
	}
	for _, group := range groups {
		if group.Count != 70 {
			t.Fatalf("group count is page size: %#v", group)
		}
	}
	for _, direction := range []string{"asc", "desc"} {
		cursor := ""
		seen := map[string]bool{}
		for {
			page, err := repository.QueryIssueRecords(ctx, IssueRecordQuery{Workspace: workspace, Filter: filter, Limit: 100, Direction: direction, Cursor: cursor, IncludeTotal: true})
			if err != nil {
				t.Fatal(err)
			}
			if page.Total != 350 {
				t.Fatalf("total %d", page.Total)
			}
			for _, issue := range page.Items {
				if seen[issue.ID] {
					t.Fatalf("duplicate %s", issue.ID)
				}
				seen[issue.ID] = true
			}
			if !page.HasMore {
				break
			}
			cursor = page.NextCursor
		}
		if len(seen) != 350 {
			t.Fatalf("lost rows: %d", len(seen))
		}
	}
	repository.Close()
	repository, err = OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	page, err := repository.QueryIssueRecords(ctx, IssueRecordQuery{Workspace: workspace, Filter: filter, IncludeTotal: true})
	if err != nil || page.Total != 350 {
		t.Fatalf("restart: %v %#v", err, page)
	}
	if len(repository.workspaces[workspace].Issues) != 0 {
		t.Fatal("startup hydrated all issue records")
	}
}

func TestIssueQueryRejectsCursorAcrossScopeAndBoundsFilters(t *testing.T) {
	repository, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	ctx := context.Background()
	data := repository.Bootstrap()
	workspace := data.Workspace.URLKey
	page, err := repository.QueryIssueRecords(ctx, IssueRecordQuery{Workspace: workspace, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !page.HasMore {
		t.Fatal("fixture should have another page")
	}
	_, err = repository.QueryIssueRecords(ctx, IssueRecordQuery{Workspace: workspace, Cursor: page.NextCursor, TeamIDs: []string{"other-team"}})
	if !errors.Is(err, ErrIssueQuery) {
		t.Fatalf("cursor crossed query scope: %v", err)
	}
	denied, err := repository.QueryIssueRecords(ctx, IssueRecordQuery{Workspace: workspace, AllowedTeamIDs: []string{}, IncludeTotal: true})
	if err != nil || denied.Total != 0 || len(denied.Items) != 0 {
		t.Fatalf("empty permission scope leaked records: %v %#v", err, denied)
	}
	_, err = repository.QueryIssueRecords(ctx, IssueRecordQuery{Workspace: workspace, Sort: "id; DROP TABLE issue_records"})
	if !errors.Is(err, ErrIssueQuery) {
		t.Fatal("invalid sort accepted")
	}
	filter := IssueFilter{Field: "priority", Values: []string{"1"}}
	for i := 0; i < 10; i++ {
		filter = IssueFilter{And: []IssueFilter{filter}}
	}
	_, err = repository.QueryIssueRecords(ctx, IssueRecordQuery{Workspace: workspace, Filter: filter})
	if !errors.Is(err, ErrIssueQuery) {
		t.Fatal("unbounded filter accepted")
	}
}

func TestIssueGroupPagingAndNotLabelFilters(t *testing.T) {
	repository, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	ctx := context.Background()
	data := repository.Bootstrap()
	base := data.Issues[0]
	workspace := data.Workspace.URLKey
	for i := 0; i < 5; i++ {
		issue := base
		issue.ID = fmt.Sprintf("group-%d", i)
		issue.Identifier = fmt.Sprintf("GROUP-%d", i)
		issue.State.ID = "group-state"
		issue.Priority = i % 2
		issue.Labels = nil
		if i == 0 {
			issue.Labels = []domain.IssueLabel{{ID: "label-group-query"}}
		}
		if err := repository.ImportIssues(ctx, workspace, []domain.Issue{issue}); err != nil {
			t.Fatal(err)
		}
	}
	value := "0"
	page, err := repository.QueryIssueRecords(ctx, IssueRecordQuery{Workspace: workspace, GroupBy: "priority", GroupValue: &value, Filter: IssueFilter{And: []IssueFilter{{Field: "status", Values: []string{"group-state"}}, {Field: "labels", Operator: "notIn", Values: []string{"label-group-query"}}}}, IncludeTotal: true})
	if err != nil || page.Total != 2 {
		t.Fatalf("filtered groups: %v %#v", err, page)
	}
}

func TestIssueQueryAccessIncludesInheritedSharesWithoutPrivateTeamLeak(t *testing.T) {
	repository, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	ctx := context.Background()
	data := repository.Bootstrap()
	base := data.Issues[0]
	workspace := data.Workspace.URLKey
	parent := base
	parent.ID = "shared-parent"
	parent.Identifier = "SHARED-1"
	parent.Team.ID = "private-team"
	parent.Permissions = []domain.IssuePermission{{SubjectType: "user", SubjectID: "recipient", Role: "viewer"}}
	child := parent
	child.ID = "shared-child"
	child.Identifier = "SHARED-2"
	child.Permissions = nil
	child.ParentID = &parent.ID
	private := parent
	private.ID = "private"
	private.Identifier = "PRIVATE-1"
	private.Permissions = nil
	if err := repository.ImportIssues(ctx, workspace, []domain.Issue{parent, child, private}); err != nil {
		t.Fatal(err)
	}
	query := IssueRecordQuery{Workspace: workspace, Access: &IssueRecordAccess{UserID: "recipient", WorkspaceID: data.Workspace.ID, VisibleTeamIDs: []string{}}, IncludeTotal: true}
	page, err := repository.QueryIssueRecords(ctx, query)
	if err != nil || page.Total != 2 {
		t.Fatalf("shared ACL: %v %#v", err, page)
	}
	query.GroupBy = "team"
	groups, err := repository.QueryIssueGroups(ctx, query)
	if err != nil || len(groups) != 1 || groups[0].Count != 2 {
		t.Fatalf("group count leaked hidden issues: %v %#v", err, groups)
	}
	query.AllowedTeamIDs = []string{}
	page, err = repository.QueryIssueRecords(ctx, query)
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("API key team limit bypass: %v %#v", err, page)
	}
	query.AllowedTeamIDs = nil
	parent.Permissions = nil
	if err := repository.ImportIssues(ctx, workspace, []domain.Issue{parent}); err != nil {
		t.Fatal(err)
	}
	page, err = repository.QueryIssueRecords(ctx, query)
	if err != nil || page.Total != 0 {
		t.Fatalf("revoked share still readable: %v %#v", err, page)
	}
}

func TestReleaseScopedIssueRecords(t *testing.T) {
	repository, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	ctx := context.Background()
	data := repository.Bootstrap()
	workspace := data.Workspace.URLKey
	base := data.Issues[0]
	now := time.Now().UTC()
	completed := base
	completed.ID, completed.Identifier, completed.Title = "rel-completed", "REL-1", "Completed associated"
	completed.State.ID, completed.State.Type, completed.State.Name = "state_done", "completed", "Done"
	unstarted := base
	unstarted.ID, unstarted.Identifier, unstarted.Title = "rel-unstarted", "REL-2", "Unstarted associated"
	unstarted.State.ID, unstarted.State.Type, unstarted.State.Name = "state_todo", "unstarted", "Todo"
	started := base
	started.ID, started.Identifier, started.Title = "rel-started", "REL-3", "Started associated"
	started.State.ID, started.State.Type, started.State.Name = "state_progress", "started", "In Progress"
	archived := base
	archived.ID, archived.Identifier, archived.Title = "rel-archived", "REL-4", "Archived associated"
	archived.ArchivedAt = &now
	outside := base
	outside.ID, outside.Identifier, outside.Title = "rel-outside", "REL-5", "Not associated"
	hidden := base
	hidden.ID, hidden.Identifier, hidden.Title = "rel-hidden", "REL-6", "Private associated"
	hidden.Team.ID = "private-team"
	if err := repository.ImportIssues(ctx, workspace, []domain.Issue{completed, unstarted, started, archived, outside, hidden}); err != nil {
		t.Fatal(err)
	}
	if err := repository.MutateWorkspace(ctx, workspace, "release.created", "release-1", nil, func(next *domain.Bootstrap) error {
		next.Releases = append(next.Releases, domain.Release{
			ID: "release-1", SlugID: "v1", Name: "v1.0.0", IssueIDs: []string{
				completed.ID, unstarted.ID, started.ID, archived.ID, hidden.ID, "deleted-issue",
			}, CreatedAt: now, UpdatedAt: now,
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	unscoped, err := repository.QueryIssueRecords(ctx, IssueRecordQuery{Workspace: workspace, IncludeTotal: true})
	if err != nil || unscoped.Total < 6 {
		t.Fatalf("unscoped workspace query: %v %#v", err, unscoped)
	}
	unknown, err := repository.QueryIssueRecords(ctx, IssueRecordQuery{Workspace: workspace, ReleaseIDs: []string{"missing-release"}, IncludeTotal: true})
	if err != nil || unknown.Total != 0 || len(unknown.Items) != 0 {
		t.Fatalf("unknown release scanned workspace: %v %#v", err, unknown)
	}
	emptyIDs, err := repository.QueryIssueRecords(ctx, IssueRecordQuery{Workspace: workspace, IssueIDs: []string{}, RestrictToIssueIDs: true, IncludeTotal: true})
	if err != nil || emptyIDs.Total != 0 || len(emptyIDs.Items) != 0 {
		t.Fatalf("empty ID scope scanned workspace: %v %#v", err, emptyIDs)
	}

	page, err := repository.QueryIssueRecords(ctx, IssueRecordQuery{Workspace: workspace, ReleaseIDs: []string{"release-1"}, IncludeTotal: true, Limit: 2, Sort: "title"})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 4 || len(page.Items) != 2 || !page.HasMore || page.NextCursor == "" {
		t.Fatalf("release page: %#v", page)
	}
	seen := map[string]bool{}
	for _, issue := range page.Items {
		seen[issue.ID] = true
	}
	for page.HasMore {
		next, err := repository.QueryIssueRecords(ctx, IssueRecordQuery{Workspace: workspace, ReleaseIDs: []string{"release-1"}, IncludeTotal: true, Limit: 2, Sort: "title", Cursor: page.NextCursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, issue := range next.Items {
			if seen[issue.ID] {
				t.Fatalf("duplicate %s", issue.ID)
			}
			seen[issue.ID] = true
		}
		page = next
	}
	if !seen[completed.ID] || !seen[unstarted.ID] || !seen[started.ID] {
		t.Fatalf("missing associated statuses: %#v", seen)
	}
	if !seen[hidden.ID] {
		t.Fatalf("unrestricted query omitted associated private-team issue: %#v", seen)
	}
	if seen[archived.ID] || seen[outside.ID] || seen["deleted-issue"] {
		t.Fatalf("deleted/archived/unassociated leaked: %#v", seen)
	}

	firstUnscoped, err := repository.QueryIssueRecords(ctx, IssueRecordQuery{Workspace: workspace, Limit: 1})
	if err != nil || firstUnscoped.NextCursor == "" {
		t.Fatalf("unscoped cursor: %v %#v", err, firstUnscoped)
	}
	if _, err := repository.QueryIssueRecords(ctx, IssueRecordQuery{Workspace: workspace, ReleaseIDs: []string{"release-1"}, Cursor: firstUnscoped.NextCursor}); !errors.Is(err, ErrIssueQuery) {
		t.Fatalf("cursor crossed release scope: %v", err)
	}

	restricted, err := repository.QueryIssueRecords(ctx, IssueRecordQuery{
		Workspace: workspace, ReleaseIDs: []string{"release-1"}, IncludeTotal: true,
		Access: &IssueRecordAccess{UserID: "recipient", WorkspaceID: data.Workspace.ID, VisibleTeamIDs: []string{"team_test"}},
	})
	if err != nil || restricted.Total != 3 {
		t.Fatalf("visible team still saw hidden issue: %v %#v", err, restricted)
	}
	denied, err := repository.QueryIssueRecords(ctx, IssueRecordQuery{
		Workspace: workspace, ReleaseIDs: []string{"release-1"}, IncludeTotal: true,
		Access: &IssueRecordAccess{UserID: "recipient", WorkspaceID: data.Workspace.ID, VisibleTeamIDs: []string{}},
	})
	if err != nil || denied.Total != 0 {
		t.Fatalf("empty permission scope leaked release issues: %v %#v", err, denied)
	}

	groups, err := repository.QueryIssueGroups(ctx, IssueRecordQuery{Workspace: workspace, ReleaseIDs: []string{"release-1"}, GroupBy: "none"})
	if err != nil || len(groups) != 1 || groups[0].Count != 4 {
		t.Fatalf("release groups scanned workspace: %v %#v", err, groups)
	}

	if err := repository.MutateWorkspace(ctx, workspace, "release.updated", "release-1", nil, func(next *domain.Bootstrap) error {
		index := slices.IndexFunc(next.Releases, func(item domain.Release) bool { return item.ID == "release-1" })
		next.Releases[index].IssueIDs = []string{started.ID}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	updated, err := repository.QueryIssueRecords(ctx, IssueRecordQuery{Workspace: workspace, ReleaseIDs: []string{"release-1"}, IncludeTotal: true})
	if err != nil || updated.Total != 1 || len(updated.Items) != 1 || updated.Items[0].ID != started.ID {
		t.Fatalf("association change ignored: %v %#v", err, updated)
	}
}

func TestReleaseIssueProgressUsesBoundedRecordQueries(t *testing.T) {
	repository, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	ctx := context.Background()
	data := repository.Bootstrap()
	base := data.Issues[0]
	issues := []domain.Issue{}
	for _, fixture := range []struct {
		id, stateType string
		archived      bool
	}{
		{"progress-completed", "completed", false},
		{"progress-started", "started", false},
		{"progress-unstarted", "unstarted", false},
		{"progress-archived", "completed", true},
	} {
		issue := base
		issue.ID, issue.Identifier = fixture.id, fixture.id
		issue.State.Type = fixture.stateType
		if fixture.archived {
			archivedAt := time.Now().UTC()
			issue.ArchivedAt = &archivedAt
		} else {
			issue.ArchivedAt = nil
		}
		issues = append(issues, issue)
	}
	if err := repository.ImportIssues(ctx, data.Workspace.URLKey, issues); err != nil {
		t.Fatal(err)
	}
	projection := domain.Bootstrap{
		Workspace: data.Workspace,
		Releases: []domain.Release{{
			ID: "progress-release",
			IssueIDs: []string{
				"progress-completed",
				"progress-started",
				"progress-unstarted",
				"progress-archived",
				"missing",
			},
		}},
	}
	if err := repository.PopulateReleaseProgress(ctx, &projection); err != nil {
		t.Fatal(err)
	}
	if projection.Releases[0].IssueCount != 3 || projection.Releases[0].CompletedCount != 1 {
		t.Fatalf("release progress=%#v", projection.Releases[0])
	}
}
