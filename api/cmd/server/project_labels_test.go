package main

import (
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestProjectLabelScopeAndGroupSelection(t *testing.T) {
	archivedAt := time.Now()
	data := domain.Bootstrap{
		Teams:       []domain.Team{{ID: "team-a"}, {ID: "team-b"}},
		LabelGroups: []domain.LabelGroup{{ID: "delivery", ResourceType: "project"}},
		Labels: []domain.IssueLabel{
			{ID: "archived", ResourceType: "project", ArchivedAt: &archivedAt},
			{ID: "workspace", ResourceType: "project", Scope: "Workspace"},
			{ID: "alpha", ResourceType: "project", Scope: "team-a", GroupID: "delivery"},
			{ID: "beta", ResourceType: "project", Scope: "team-a", GroupID: "delivery"},
			{ID: "other-team", ResourceType: "project", Scope: "team-b"},
		},
	}
	for _, tc := range []struct {
		name  string
		ids   []string
		teams []string
		valid bool
	}{
		{"workspace and project team", []string{"workspace", "alpha"}, nil, true},
		{"same-group conflict", []string{"alpha", "beta"}, nil, false},
		{"foreign team", []string{"other-team"}, nil, false},
		{"new project teams", []string{"other-team"}, []string{"team-b"}, true},
		{"keep existing archived", []string{"archived", "workspace"}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project := domain.Project{ID: "project", TeamIDs: []string{"team-a"}, LabelIDs: []string{"archived"}}
			err := applyProjectUpdate(&data, &project, domain.ProjectMutationInput{LabelIDs: tc.ids, TeamIDs: tc.teams})
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, got %v", tc.valid, err)
			}
			if tc.valid && !slices.Equal(project.LabelIDs, tc.ids) {
				t.Fatalf("labels were not retained: %v", project.LabelIDs)
			}
		})
	}
	project := domain.Project{ID: "project", TeamIDs: []string{"team-a"}}
	if err := applyProjectUpdate(&data, &project, domain.ProjectMutationInput{LabelIDs: []string{"archived"}}); err == nil {
		t.Fatal("unapplied archived labels must not be assignable")
	}
}
