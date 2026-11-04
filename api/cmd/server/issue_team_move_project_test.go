package main

import (
	"net/http"
	"path/filepath"
	"strconv"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestTeamMoveProjectMembership(t *testing.T) {
	for _, included := range []bool{false, true} {
		t.Run(strconv.FormatBool(included), func(t *testing.T) {
			target := domain.Team{ID: "target", Key: "DST"}
			project := domain.Project{ID: "project", TeamIDs: []string{"source"}}
			if included {
				project.TeamIDs = append(project.TeamIDs, target.ID)
			}
			data := domain.Bootstrap{Projects: []domain.Project{project}}
			milestone := "milestone"
			issue := domain.Issue{ID: "issue", Identifier: "SRC-1", Team: domain.Team{ID: "source"}, Project: &domain.ProjectSummary{ID: project.ID}, ProjectMilestoneID: &milestone}
			changes := map[string]string{}
			moveIssueToTeam(&data, &issue, target, nil, changes)
			if included {
				if issue.Project == nil || issue.ProjectMilestoneID == nil {
					t.Fatal("move dropped a project shared with the destination")
				}
			} else if issue.Project != nil || issue.ProjectMilestoneID != nil || changes["projectBefore"] != project.ID || changes["projectMilestoneBefore"] != milestone {
				t.Fatalf("project = %+v, milestone = %v, changes = %v", issue.Project, issue.ProjectMilestoneID, changes)
			}
		})
	}
}

func TestIssueTeamMoveProjectMembership(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, authDisabled: true, uploadPath: t.TempDir()})
	data := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	source := data.Teams[0]
	target := requestJSON[domain.Team](t, handler, http.MethodPost, "/api/workspaces/test-workspace/teams", map[string]any{"name": "Destination", "key": "DST"}, http.StatusCreated)
	for _, shared := range []bool{false, true} {
		t.Run(strconv.FormatBool(shared), func(t *testing.T) {
			teams := []string{source.ID}
			if shared {
				teams = append(teams, target.ID)
			}
			project := requestJSON[domain.Project](t, handler, http.MethodPost, "/api/projects", map[string]any{"name": "Project " + strconv.FormatBool(shared), "teamIds": teams}, http.StatusCreated)
			issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Project move", "teamId": source.ID, "projectId": project.ID}, http.StatusCreated)
			moved := requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+issue.ID, map[string]any{"teamId": target.ID}, http.StatusOK)
			if shared && (moved.Project == nil || moved.Project.ID != project.ID) || !shared && moved.Project != nil {
				t.Fatalf("shared=%v: project = %+v", shared, moved.Project)
			}
			stored := requestJSON[domain.Issue](t, handler, http.MethodGet, "/api/issue-records/"+issue.Identifier, nil, http.StatusOK)
			if (stored.Project != nil) != shared {
				t.Fatalf("stored project = %+v", stored.Project)
			}
		})
	}
}
