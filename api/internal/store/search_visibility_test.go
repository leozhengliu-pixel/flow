package store

import (
	"context"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// TestSearchCandidateVisibilityMatchesPerHitFilter compares the new candidate
// selection (visibility pushed into the full-text selection) with
// SearchIssueIDs, which still applies the per-hit visibility predicate.
func TestSearchCandidateVisibilityMatchesPerHitFilter(t *testing.T) {
	ctx := context.Background()
	repository, workspace := searchVisibilityWorkspace(t)
	key := workspace.Workspace.URLKey
	visibility := &IssueRecordAccess{
		UserID:          workspace.Viewer.ID,
		WorkspaceID:     workspace.Workspace.ID,
		VisibleTeamIDs:  []string{"team_search_public"},
		ArchivedTeamIDs: []string{"team_search_archived"},
	}
	for _, test := range []struct {
		name   string
		access *IssueRecordAccess
		want   []string
	}{
		{"member", visibility, []string{"search_public", "search_shared", "search_shared_child"}},
		{"admin", &IssueRecordAccess{Admin: true, ArchivedTeamIDs: []string{"team_search_archived"}}, []string{"search_private", "search_private_hidden", "search_public", "search_shared", "search_shared_child"}},
	} {
		query := IssueRecordQuery{Workspace: key, Access: test.access, Archived: "false"}
		got := []string{}
		err := repository.SearchIssueCandidateTerms(ctx, query, []string{"Needle"}, nil, 100, func(issue domain.Issue) error {
			got = append(got, issue.ID)
			return nil
		})
		if err != nil {
			t.Fatalf("%s: candidates: %v", test.name, err)
		}
		want, _, err := repository.SearchIssueIDs(ctx, query, "Needle", 5000)
		if err != nil {
			t.Fatalf("%s: search ids: %v", test.name, err)
		}
		sort.Strings(got)
		sort.Strings(want)
		if len(got) != len(want) {
			t.Fatalf("%s: candidates %v != per-hit filter %v", test.name, got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("%s: candidates %v != per-hit filter %v", test.name, got, want)
			}
		}
		for _, id := range test.want {
			if !containsString(got, id) {
				t.Fatalf("%s: missing %s in %v", test.name, id, got)
			}
		}
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func searchVisibilityWorkspace(t *testing.T) (*SQLiteStore, domain.Bootstrap) {
	t.Helper()
	repository, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	now := time.Now().UTC()
	created, err := repository.CreateWorkspaceWith(context.Background(), "Search visibility", "search-visibility", "us", func(data *domain.Bootstrap) error {
		data.Teams = []domain.Team{
			{ID: "team_search_public", Name: "Public", Key: "SPU", Color: "#5E6AD2", CreatedAt: &now},
			{ID: "team_search_private", Name: "Private", Key: "SPR", Color: "#5E6AD2", Private: true, CreatedAt: &now},
			{ID: "team_search_archived", Name: "Archived", Key: "SAR", Color: "#5E6AD2", ArchivedAt: &now, CreatedAt: &now},
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	key := created.Workspace.URLKey
	viewer := created.Viewer
	issues := []domain.Issue{
		{ID: "search_public", Identifier: "SPU-1", Title: "Needle public", Team: created.Teams[0], State: created.States[0], Creator: viewer, CreatedAt: now, UpdatedAt: now},
		{ID: "search_private", Identifier: "SPR-1", Title: "Needle private", Team: created.Teams[1], State: created.States[0], Creator: viewer, CreatedAt: now, UpdatedAt: now},
		{ID: "search_private_hidden", Identifier: "SPR-2", Title: "Needle hidden", Team: created.Teams[1], State: created.States[0], Creator: viewer, CreatedAt: now, UpdatedAt: now},
		{ID: "search_shared", Identifier: "SPR-3", Title: "Needle shared", Team: created.Teams[1], State: created.States[0], Creator: viewer, CreatedAt: now, UpdatedAt: now,
			Permissions: []domain.IssuePermission{{ID: "perm_search_shared", IssueID: "search_shared", SubjectType: "user", SubjectID: viewer.ID, Role: "viewer"}}},
		{ID: "search_archived_team", Identifier: "SAR-1", Title: "Needle archived team", Team: created.Teams[2], State: created.States[0], Creator: viewer, CreatedAt: now, UpdatedAt: now},
	}
	child := domain.Issue{ID: "search_shared_child", Identifier: "SPR-4", Title: "Needle shared child", Team: created.Teams[1], State: created.States[0], Creator: viewer, CreatedAt: now, UpdatedAt: now}
	parent := "search_shared"
	child.ParentID = &parent
	issues = append(issues, child)
	if err := repository.ImportIssues(context.Background(), key, issues); err != nil {
		t.Fatal(err)
	}
	return repository, repository.Bootstrap()
}
