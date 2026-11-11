package store

import (
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestIssueRecordMutationPersistsSLAChangesAlongsideIssue(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "sla-record.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	testIssueSLAMetadataMutation(t, repo, repo.Bootstrap())
}

func testIssueSLAMetadataMutation(t *testing.T, repo *SQLiteStore, data domain.Bootstrap) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	issue := domain.Issue{ID: "sla-mutation", Identifier: "SLM-1", Number: 1, Title: "SLA mutation", Team: data.Teams[0], State: domain.WorkflowState{ID: "sla-mutation-state", Type: "unstarted", Name: "Todo", TeamID: data.Teams[0].ID}, CreatedAt: now, UpdatedAt: now}
	if err := repo.ImportIssues(t.Context(), data.Workspace.URLKey, []domain.Issue{issue}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UpdateIssueRecord(t.Context(), data.Workspace.URLKey, issue.ID, nil, IssueMutationScope{}, func(current *domain.Bootstrap, target *domain.Issue) error {
		target.Priority = 1
		current.IssueSLAs = append(current.IssueSLAs, domain.IssueSLA{ID: "sla-record", IssueID: target.ID, Manual: true, StartedAt: now, DueAt: now.Add(time.Hour), HighRiskAt: &now, TargetMinutes: 60, Status: "active"})
		current.SLAEvents = append(current.SLAEvents, domain.SLAEvent{ID: "sla-record-started", IssueID: target.ID, SLAID: "sla-record", Type: "started", CreatedAt: now})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	metadata, _ := repo.WorkspaceMetadata(data.Workspace.URLKey)
	if len(metadata.IssueSLAs) == 0 || metadata.IssueSLAs[len(metadata.IssueSLAs)-1].Status != "active" || len(metadata.SLAEvents) == 0 {
		t.Fatal("bounded issue write discarded SLA metadata")
	}
	if _, err := repo.UpdateIssueRecord(t.Context(), data.Workspace.URLKey, issue.ID, nil, IssueMutationScope{}, func(current *domain.Bootstrap, target *domain.Issue) error {
		target.Priority = 3
		for index := range current.IssueSLAs {
			if current.IssueSLAs[index].ID == "sla-record" {
				current.IssueSLAs[index].Status = "removed"
			}
		}
		current.SLAEvents = append(current.SLAEvents, domain.SLAEvent{ID: "sla-record-removed", IssueID: target.ID, SLAID: "sla-record", Type: "removed", CreatedAt: now})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	metadata, _ = repo.WorkspaceMetadata(data.Workspace.URLKey)
	if metadata.IssueSLAs[len(metadata.IssueSLAs)-1].Status != "removed" || metadata.SLAEvents[len(metadata.SLAEvents)-1].Type != "removed" {
		t.Fatal("bounded issue write discarded SLA removal")
	}
}
