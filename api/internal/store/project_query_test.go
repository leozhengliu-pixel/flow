package store

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestProjectDirectoryPagesAndStripsDetailPayloads(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "projects.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	ctx := context.Background()
	data := repo.Bootstrap()
	teamID := data.Teams[0].ID
	if err := repo.MutateWorkspace(WithoutIssueRecordMutations(ctx), data.Workspace.URLKey, "projects.seeded", "query-test", nil, func(next *domain.Bootstrap) error {
		next.Projects = nil
		for index := 0; index < 205; index++ {
			project := domain.Project{
				ID: fmt.Sprintf("project-%03d", index), Name: fmt.Sprintf("Project %03d", index), SlugID: fmt.Sprintf("project-%03d", index),
				Summary: "Directory summary", Description: strings.Repeat("detail body ", 1000), TeamIDs: []string{teamID},
				Resources: []domain.ProjectResource{{ID: "resource", Title: "Runbook"}}, Comments: []domain.Comment{{ID: "comment", Body: "hidden"}},
				DescriptionRevisions: []domain.ProjectDescriptionRevision{{ID: "revision", Description: "hidden"}},
				Milestones:           []domain.ProjectMilestone{{ID: "milestone", Name: "Public milestone", Description: "hidden detail"}},
				CreatedAt:            time.Now().UTC(), UpdatedAt: time.Now().UTC(),
			}
			next.Projects = append(next.Projects, project)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	page, err := repo.QueryProjectDirectory(ctx, ProjectRecordQuery{Workspace: data.Workspace.URLKey, Limit: 100, IncludeTotal: true, Admin: true})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 205 || len(page.Items) != 100 || !page.HasMore || page.NextCursor == "" {
		t.Fatalf("first page=%#v", page)
	}
	for _, project := range page.Items {
		if project.Description != "" || project.Resources != nil || project.Comments != nil || project.DescriptionRevisions != nil || len(project.Milestones) != 1 || project.Milestones[0].Description != "" {
			t.Fatalf("project detail leaked into directory: %#v", project)
		}
	}
	seen := map[string]bool{}
	for page.HasMore {
		for _, project := range page.Items {
			if seen[project.ID] {
				t.Fatalf("duplicate project %s", project.ID)
			}
			seen[project.ID] = true
		}
		page, err = repo.QueryProjectDirectory(ctx, ProjectRecordQuery{Workspace: data.Workspace.URLKey, Limit: 100, IncludeTotal: true, Cursor: page.NextCursor, Admin: true})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, project := range page.Items {
		seen[project.ID] = true
	}
	if len(seen) != 205 {
		t.Fatalf("paged projects=%d", len(seen))
	}
	search, err := repo.QueryProjectDirectory(ctx, ProjectRecordQuery{Workspace: data.Workspace.URLKey, Search: "Project 204", IncludeTotal: true, Admin: true})
	if err != nil || search.Total != 1 || len(search.Items) != 1 || search.Items[0].ID != "project-204" {
		t.Fatalf("search=%#v err=%v", search, err)
	}
	filtered, err := repo.QueryProjectDirectory(ctx, ProjectRecordQuery{Workspace: data.Workspace.URLKey, IncludeTotal: true, Admin: true, Filters: []ProjectDirectoryFilter{{Field: "project", Operator: "is", Values: []string{"project-204"}}}})
	if err != nil || filtered.Total != 1 || len(filtered.Items) != 1 || filtered.Items[0].ID != "project-204" {
		t.Fatalf("filtered=%#v err=%v", filtered, err)
	}
	excluded, err := repo.QueryProjectDirectory(ctx, ProjectRecordQuery{Workspace: data.Workspace.URLKey, IncludeTotal: true, Admin: true, Filters: []ProjectDirectoryFilter{{Field: "project", Operator: "isNot", Values: []string{"project-204"}}}})
	if err != nil || excluded.Total != 204 {
		t.Fatalf("excluded=%#v err=%v", excluded, err)
	}
	denied, err := repo.QueryProjectDirectory(ctx, ProjectRecordQuery{Workspace: data.Workspace.URLKey, AllowedTeamIDs: []string{}, IncludeTotal: true})
	if err != nil || denied.Total != 0 {
		t.Fatalf("permission scope=%#v err=%v", denied, err)
	}
}

func TestProjectListBootstrapProjectionRemovesUnrelatedLargeCollections(t *testing.T) {
	data := domain.Bootstrap{
		Projects:       []domain.Project{{ID: "project", Description: "detail", Resources: []domain.ProjectResource{{ID: "resource"}}}},
		Issues:         []domain.Issue{{ID: "issue", Description: "large body"}},
		Comments:       map[string][]domain.Comment{"issue": {{ID: "comment"}}},
		Activities:     map[string][]domain.ActivityEvent{"issue": {{ID: "event"}}},
		ProjectUpdates: map[string][]domain.ProjectUpdate{"project": {{ID: "update"}}},
	}
	ProjectListBootstrapProjection(&data)
	if len(data.Projects) != 0 || len(data.Issues) != 0 || len(data.Comments) != 0 || len(data.Activities) != 0 || len(data.ProjectUpdates) != 0 || !data.IssueCollectionPaged {
		t.Fatalf("projection retained detail payloads: %#v", data)
	}
}
