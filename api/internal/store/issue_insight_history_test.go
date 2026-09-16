package store

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestInsightHistoryReadsAllTransitionsWithoutUnrelatedContent(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "insight.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	data := repo.Bootstrap()
	issue := data.Issues[0]
	issue.ID = "insight-history"
	issue.CreatedAt = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	states := []domain.WorkflowState{{ID: "a", Type: "backlog"}, {ID: "b", Type: "started"}}
	tx, err := repo.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := 0; i < 150; i++ {
		before, next := "a", "b"
		if i%2 == 1 {
			before, next = "b", "a"
		}
		event := domain.ActivityEvent{ID: fmt.Sprintf("transition-%03d", i), Type: "issue.updated", CreatedAt: issue.CreatedAt.Add(time.Duration(i+1) * time.Hour), Metadata: map[string]string{"stateBeforeId": before, "stateId": next}}
		if err := writeContentRecord(t.Context(), tx, data.Workspace.URLKey, "activity", issue.ID, event); err != nil {
			t.Fatal(err)
		}
		if err := writeContentRecord(t.Context(), tx, data.Workspace.URLKey, "activity", "other-issue", event); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	result, err := repo.IssueInsightIntervals(t.Context(), data.Workspace.URLKey, []domain.Issue{issue}, states)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 || len(result[issue.ID]) != 151 {
		t.Fatalf("lost transitions or leaked unrelated issue: %d/%d", len(result), len(result[issue.ID]))
	}
	intervals := result[issue.ID]
	if intervals[0].StateID != "a" || intervals[0].StateType != "backlog" || intervals[150].ExitedAt != nil {
		t.Fatalf("wrong interval endpoints: %+v", intervals)
	}
	for i := 0; i < 150; i++ {
		if intervals[i].ExitedAt.Sub(intervals[i].EnteredAt) != time.Hour {
			t.Fatalf("invalid duration at %d", i)
		}
	}
}

func TestInsightHistoryUsesStatusChangedAtWithoutFabricatingOldStates(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "insight.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	data := repo.Bootstrap()
	issue := data.Issues[0]
	issue.ID = "no-history"
	changed := issue.CreatedAt.Add(time.Hour)
	issue.StatusChangedAt = &changed
	result, err := repo.IssueInsightIntervals(t.Context(), data.Workspace.URLKey, []domain.Issue{issue}, data.States)
	if err != nil {
		t.Fatal(err)
	}
	if len(result[issue.ID]) != 1 || !result[issue.ID][0].EnteredAt.Equal(changed) {
		t.Fatalf("fabricated history: %+v", result)
	}
}
