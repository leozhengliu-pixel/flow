package store

import (
	"context"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func TestScaleP19SummaryInputsPreserveCanonicalBodies(t *testing.T) {
	for driver, repo := range scaleP03Stores(t) {
		t.Run(driver, func(t *testing.T) {
			workspace := repo.data.Workspace.URLKey
			body := strings.Repeat("p19-body", 64<<10)
			if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p19.seeded", "", nil, func(data *domain.Bootstrap) error {
				data.Projects = append(data.Projects, domain.Project{ID: "p19-project", Name: "Summary", Description: body, TeamIDs: []string{data.Teams[0].ID}})
				data.Initiatives = append(data.Initiatives, domain.Initiative{ID: "p19-initiative", ProjectIDs: []string{"p19-project"}, Description: body, Creator: data.Viewer})
				data.ProjectUpdates["p19-project"] = []domain.ProjectUpdate{{ID: "update", ProjectID: "p19-project", Body: body, User: data.Viewer}}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			for _, unfiltered := range []bool{false, true} {
				data, err := repo.ScaleP19Metadata(t.Context(), workspace, repo.data.Viewer.ID, unfiltered)
				if err != nil {
					t.Fatal(err)
				}
				for _, project := range data.Projects {
					if project.Description != "" || len(project.DescriptionRevisions) > 0 {
						t.Fatal("window hydrated a project body")
					}
				}
				for _, initiative := range data.Initiatives {
					if initiative.Description != "" {
						t.Fatal("window hydrated an initiative body")
					}
				}
				if updates := data.ProjectUpdates["p19-project"]; len(updates) != 1 || updates[0].Body != "" {
					t.Fatal("window copied update history")
				}
			}
			access, err := repo.ResolveAccess(context.Background(), workspace, repo.data.Viewer.ID, nil)
			if err != nil {
				t.Fatal(err)
			}
			project, err := repo.AuthorizedProjectRecord(t.Context(), access, "p19-project")
			if err != nil || project.Description != body {
				t.Fatal("summary read modified canonical detail", err)
			}
		})
	}
}
