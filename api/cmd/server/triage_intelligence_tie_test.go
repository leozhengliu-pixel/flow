package main

import (
	"testing"

	"flow/api/internal/domain"
)

// Equal scores pick the target scored first on every call, never one chosen
// by map iteration order.
func TestTriageInferenceBreaksTiesByScoringOrder(t *testing.T) {
	data := &domain.Bootstrap{
		Users: []domain.User{{ID: "tie-user-b", Active: true}, {ID: "tie-user-a", Active: true}},
		Teams: []domain.Team{{ID: "tie-team-own"}, {ID: "tie-team-b"}, {ID: "tie-team-a"}},
		Projects: []domain.Project{
			{ID: "tie-project-b", Name: "Preview crash"}, {ID: "tie-project-a", Name: "Preview crash"},
			{ID: "tie-project-own", Name: "Own", TeamIDs: []string{"tie-team-own", "tie-team-b", "tie-team-a"}},
		},
		Labels: []domain.IssueLabel{{ID: "tie-label-b", Name: "Preview crash"}, {ID: "tie-label-a", Name: "Preview crash"}},
	}
	issue := &domain.Issue{ID: "tie-issue", Title: "Preview crash", Team: domain.Team{ID: "tie-team-own"}}
	candidate := func(assignee, team string) triageSuggestionCandidate {
		user := domain.User{ID: assignee}
		return triageSuggestionCandidate{issue: domain.Issue{ID: "tie-" + assignee + team, Title: "Other", Assignee: &user, Team: domain.Team{ID: team}}, score: 0.4}
	}
	candidates := []triageSuggestionCandidate{candidate("tie-user-b", "tie-team-b"), candidate("tie-user-a", "tie-team-a")}
	projectIssue := *issue
	projectIssue.Project = &domain.ProjectSummary{ID: "tie-project-own"}
	for run := 0; run < 64; run++ {
		if id, _, _ := inferProject(data, issue, nil); id != "tie-project-b" {
			t.Fatalf("run %d: project %q", run, id)
		}
		if id, _, _ := inferLabel(data, issue, nil); id != "tie-label-b" {
			t.Fatalf("run %d: label %q", run, id)
		}
		if id, _, _ := inferAssignee(data, issue, candidates); id != "tie-user-b" {
			t.Fatalf("run %d: assignee %q", run, id)
		}
		if id, _, _ := inferTeam(data, issue, candidates); id != "tie-team-b" {
			t.Fatalf("run %d: team from candidates %q", run, id)
		}
		if id, _, _ := inferTeam(data, &projectIssue, nil); id != "tie-team-b" {
			t.Fatalf("run %d: team from project %q", run, id)
		}
	}
}
