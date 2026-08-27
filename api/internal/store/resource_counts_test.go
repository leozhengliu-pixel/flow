package store

import (
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestRefreshResourceCountsUsesActiveRelationships(t *testing.T) {
	archivedAt := time.Now().UTC()
	issueLabel := domain.IssueLabel{ID: "issue-label", ResourceType: "issue", IssueCount: 99}
	projectLabel := domain.IssueLabel{ID: "project-label", ResourceType: "project", IssueCount: 99}
	initiativeLabel := domain.IssueLabel{ID: "initiative-label", ResourceType: "initiative", IssueCount: 99}
	projectSummary := &domain.ProjectSummary{ID: "project-active"}
	data := domain.Bootstrap{
		Labels: []domain.IssueLabel{issueLabel, projectLabel, initiativeLabel},
		Issues: []domain.Issue{
			{ID: "issue-active", Labels: []domain.IssueLabel{issueLabel, issueLabel}, Project: projectSummary},
			{ID: "issue-archived", Labels: []domain.IssueLabel{issueLabel}, Project: projectSummary, ArchivedAt: &archivedAt},
		},
		Projects: []domain.Project{
			{ID: "project-active", LabelIDs: []string{projectLabel.ID, projectLabel.ID}},
			{ID: "project-archived", LabelIDs: []string{projectLabel.ID}, ArchivedAt: &archivedAt},
		},
		Initiatives: []domain.Initiative{{ID: "initiative-active", LabelIDs: []string{initiativeLabel.ID}}},
	}

	refreshResourceCounts(&data)

	for _, label := range data.Labels {
		if label.IssueCount != 1 {
			t.Fatalf("label %s count = %d, want 1", label.ID, label.IssueCount)
		}
	}
	if data.Issues[0].Labels[0].IssueCount != 1 {
		t.Fatalf("embedded issue label count = %d, want 1", data.Issues[0].Labels[0].IssueCount)
	}
	if data.Projects[0].IssueCount != 1 || data.Projects[1].IssueCount != 0 {
		t.Fatalf("project issue counts = %d, %d, want 1, 0", data.Projects[0].IssueCount, data.Projects[1].IssueCount)
	}
}
