package store

import (
	"errors"
	"testing"

	"flow/api/internal/domain"
)

func TestScaleP03CredentialReadDetectsMetadataDrift(t *testing.T) {
	for name, repo := range scaleP03Stores(t) {
		t.Run(name, func(t *testing.T) {
			workspace, principal := repo.data.Workspace.URLKey, repo.data.Viewer.ID
			if err := repo.MutateWorkspace(t.Context(), workspace, "test.scope_fixture", "", nil, func(data *domain.Bootstrap) error {
				data.Projects = append(data.Projects, domain.Project{ID: "scope-drift-project"})
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			access, err := repo.ResolveAccess(t.Context(), workspace, principal, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := repo.MutateWorkspace(t.Context(), workspace, "test.scope_changed", "", nil, func(data *domain.Bootstrap) error {
				for i := range data.Projects {
					if data.Projects[i].ID == "scope-drift-project" {
						data.Projects[i].Name = "Changed during read"
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if _, _, err := repo.ResourceReadMetadata(t.Context(), access, "projects", "scope-drift-project"); !errors.Is(err, ErrAuthForbidden) || !errors.Is(err, ErrResourceAccessChanged) {
				t.Fatalf("stale read error = %v", err)
			}
			access, err = repo.ResolveAccess(t.Context(), workspace, principal, nil)
			if err != nil {
				t.Fatal(err)
			}
			if item, err := repo.AuthorizedProjectRecord(t.Context(), access, "scope-drift-project"); err != nil || item.Name != "Changed during read" {
				t.Fatalf("fresh read = %q, %v", item.Name, err)
			}
		})
	}
}
