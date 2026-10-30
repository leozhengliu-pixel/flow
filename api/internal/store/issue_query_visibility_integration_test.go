//go:build integration

package store

import (
	"os"
	"strconv"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestExternalHasSubIssuesFilterIgnoresHiddenChildren(t *testing.T) {
	driver, databaseURL := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL")
	if driver == "" || databaseURL == "" {
		t.Skip("isolated external database required")
	}
	repo, err := OpenDatabase(DatabaseConfig{Driver: driver, URL: databaseURL, MaxOpenConns: 4, MaxIdleConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	workspace := "has-sub-visibility-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	data, err := repo.CreateWorkspace(t.Context(), "Has sub-issues visibility", workspace, "us")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.DeleteWorkspace(t.Context(), workspace) })
	if err := repo.MutateWorkspace(t.Context(), workspace, "team.created", "private-team", nil, func(next *domain.Bootstrap) error {
		next.Teams = append(next.Teams, domain.Team{ID: "private-team", Name: "Private", Key: "PRV", Private: true})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	parent := domain.Issue{ID: "visible-parent", Identifier: "VIS-1", Team: data.Teams[0], State: data.States[0], Creator: data.Viewer, CreatedAt: now, UpdatedAt: now, SubIssueIDs: []string{}}
	child := parent
	child.ID, child.Identifier, child.Team = "hidden-child", "PRV-1", domain.Team{ID: "private-team"}
	child.ParentID = &parent.ID
	if err := repo.ImportIssues(t.Context(), workspace, []domain.Issue{parent, child}); err != nil {
		t.Fatal(err)
	}
	access := &IssueRecordAccess{UserID: data.Viewer.ID, WorkspaceID: data.Workspace.ID, VisibleTeamIDs: []string{parent.Team.ID}}
	query := IssueRecordQuery{Workspace: workspace, Access: access, IssueIDs: []string{parent.ID}, RestrictToIssueIDs: true, Filter: IssueFilter{Field: "hasSubIssues", Operator: "isNotEmpty"}}
	page, err := repo.QueryIssueRecords(t.Context(), query)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 0 {
		t.Fatalf("hidden child matched hasSubIssues=true: %#v", page.Items)
	}
}
