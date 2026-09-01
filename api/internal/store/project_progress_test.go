package store

import (
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestProjectProgressHistoryReplaysStateTransitions(t *testing.T) {
	start := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
	startedAt := start.AddDate(0, 0, 7).Add(2 * time.Hour)
	completedAt := start.AddDate(0, 0, 14).Add(3 * time.Hour)
	team := domain.Team{ID: "team", Name: "Team", Key: "T"}
	unstarted := domain.WorkflowState{ID: "state_unstarted", TeamID: team.ID, Name: "Todo", Type: "unstarted"}
	started := domain.WorkflowState{ID: "state_started", TeamID: team.ID, Name: "In Progress", Type: "started"}
	completed := domain.WorkflowState{ID: "state_completed", TeamID: team.ID, Name: "Done", Type: "completed"}
	project := domain.Project{ID: "project", Name: "Project", TeamIDs: []string{team.ID}, StartDate: stringPtr("2026-01-05")}
	estimate := 3.0
	issue := domain.Issue{ID: "issue", CreatedAt: start, UpdatedAt: completedAt, Estimate: &estimate, Team: team, State: completed, Project: &domain.ProjectSummary{ID: project.ID}, Labels: []domain.IssueLabel{}}
	data := domain.Bootstrap{
		Projects: []domain.Project{project},
		Issues:   []domain.Issue{issue},
		States:   []domain.WorkflowState{unstarted, started, completed},
		Activities: map[string][]domain.ActivityEvent{"issue": {
			{Type: "issue.updated", CreatedAt: startedAt, Metadata: map[string]string{"stateBeforeId": unstarted.ID, "stateId": started.ID}},
			{Type: "issue.updated", CreatedAt: completedAt, Metadata: map[string]string{"stateBeforeId": started.ID, "stateId": completed.ID}},
		}},
	}

	if !refreshProjectProgressHistories(&data, start.AddDate(0, 0, 21)) {
		t.Fatal("progress history was not generated")
	}
	history := data.Projects[0]
	if len(history.InProgressScopeHistory) != 4 || len(history.CompletedScopeHistory) != 4 {
		t.Fatalf("history length = %d/%d, want four points", len(history.InProgressScopeHistory), len(history.CompletedScopeHistory))
	}
	if got := history.InProgressScopeHistory[0].Value; got != 0 {
		t.Fatalf("started at project start = %v, want 0", got)
	}
	if got := history.InProgressScopeHistory[1].Value; got != estimate {
		t.Fatalf("started after transition = %v, want %v", got, estimate)
	}
	if got := history.CompletedScopeHistory[1].Value; got != 0 || history.CompletedScopeHistory[2].Value != estimate {
		t.Fatalf("completed history = %#v, want 0 then %v", history.CompletedScopeHistory, estimate)
	}
	if got := history.ScopeHistory[0].Value; got != estimate {
		t.Fatalf("scope history = %v, want estimate %v", got, estimate)
	}
}

func stringPtr(value string) *string { return &value }
