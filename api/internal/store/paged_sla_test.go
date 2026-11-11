package store

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestPagedWorkspaceMetadataKeepsVisibleSLAsAndThresholds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "paged-sla.db")
	repo, err := OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	testPagedSLAMetadata(t, repo, repo.Bootstrap())
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	data := reopened.Bootstrap()
	if len(data.IssueSLAs) != 3 || data.IssueSLAs[0].HighRiskAt == nil || data.IssueSLAs[0].TargetMinutes != 1440 || !data.IssueSLAs[0].BusinessHours {
		t.Fatalf("SLA metadata was not persisted across reopen: %+v", data.IssueSLAs)
	}
}

func testPagedSLAMetadata(t *testing.T, repo *SQLiteStore, data domain.Bootstrap) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	visibleIssue := domain.Issue{ID: "sla_visible", Identifier: "SLA-1", Number: 1, Title: "Visible SLA", Team: data.Teams[0], State: domain.WorkflowState{ID: "sla_unstarted", Type: "unstarted", Name: "Todo", TeamID: data.Teams[0].ID}, CreatedAt: now, UpdatedAt: now}
	privateTeam := domain.Team{ID: "sla_private", Name: "Private", Key: "PSL", Private: true}
	hiddenIssue := visibleIssue
	hiddenIssue.ID, hiddenIssue.Identifier, hiddenIssue.Team = "sla_hidden", "PSL-1", privateTeam
	hiddenIssue.State.ID, hiddenIssue.State.TeamID = "sla_private_state", privateTeam.ID
	if err := repo.MutateWorkspace(t.Context(), data.Workspace.URLKey, "test.sla_metadata", "", nil, func(current *domain.Bootstrap) error {
		current.Teams = append(current.Teams, privateTeam)
		current.States = append(current.States, visibleIssue.State, hiddenIssue.State)
		current.Issues = append(current.Issues, visibleIssue, hiddenIssue)
		current.SLARules = []domain.SLARule{{ID: "sla_rule", Enabled: true, TargetMinutes: 1440, BusinessHours: true}}
		for _, issueID := range []string{visibleIssue.ID, hiddenIssue.ID, "sla_missing"} {
			current.IssueSLAs = append(current.IssueSLAs, domain.IssueSLA{ID: issueID, IssueID: issueID, RuleID: "sla_rule", StartedAt: now, DueAt: now.Add(24 * time.Hour), HighRiskAt: &now, MediumRiskAt: &now, TargetMinutes: 1440, BusinessHours: true, Status: "active"})
			current.SLAEvents = append(current.SLAEvents, domain.SLAEvent{ID: issueID, SLAID: issueID, IssueID: issueID, Type: "started", CreatedAt: now})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	metadata, err := repo.PagedWorkspaceMetadata(t.Context(), data.Workspace.URLKey, data.Viewer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(metadata.IssueSLAs, func(sla domain.IssueSLA) bool {
		return sla.IssueID == visibleIssue.ID && sla.HighRiskAt != nil && sla.HighRiskAt.Equal(now)
	}) {
		t.Fatalf("paged metadata dropped a visible SLA: %+v", metadata.IssueSLAs)
	}
	if len(metadata.IssueSLAs) != 1 || len(metadata.SLAEvents) != 1 || metadata.SLAEvents[0].IssueID != visibleIssue.ID {
		t.Fatalf("paged SLA metadata disclosed inaccessible issues: %+v %+v", metadata.IssueSLAs, metadata.SLAEvents)
	}
}
