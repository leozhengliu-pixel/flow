package store

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func TestVisibleIssueTeamsMatchesPrivateHierarchyPolicy(t *testing.T) {
	for _, role := range []string{"admin", "owner", "member", "guest"} {
		for _, rootAccess := range []string{"", "public", "private", "restricted"} {
			for _, childAccess := range []string{"", "public", "private", "restricted"} {
				for _, membership := range []string{"", "member", "owner"} {
					data := domain.Bootstrap{Teams: []domain.Team{{ID: "root"}, {ID: "child"}, {ID: "leaf", Private: true}, {ID: "public"}}, TeamSettings: map[string]domain.TeamSettings{
						"root": {Access: rootAccess}, "child": {ParentTeamID: "root", Access: childAccess}, "leaf": {ParentTeamID: "child"},
					}}
					if membership != "" {
						data.TeamMembers = []domain.TeamMember{{TeamID: "root", UserID: "viewer", Role: membership}, {TeamID: "public", UserID: "other", Role: "owner"}}
					}
					want := []string{}
					for _, team := range data.Teams {
						if teamVisibleToUser(data, team.ID, "viewer", role) {
							want = append(want, team.ID)
						}
					}
					got := visibleIssueTeams(data, "viewer", role)
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("role=%s root=%s child=%s membership=%s: got %v want %v", role, rootAccess, childAccess, membership, got, want)
					}
				}
			}
		}
	}
}

func TestIssueAccessMetadataDoesNotCopyUnrelatedEntities(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	ctx := context.Background()
	base := repo.Bootstrap()
	key := base.Workspace.URLKey
	repo.mu.Lock()
	data := repo.workspaces[key]
	data.Documents = []domain.Document{{ID: "large-document", Content: strings.Repeat("x", 8<<20)}}
	repo.workspaces[key] = data
	repo.mu.Unlock()
	metadata, access, err := repo.IssueQueryAccess(ctx, key, base.Viewer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if access.UserID != base.Viewer.ID || len(metadata.Documents) != 0 || len(metadata.Projects) != 0 || len(metadata.Users) != 0 || len(metadata.Issues) != 0 {
		t.Fatal("query authorization retained the full directory")
	}
	for _, member := range metadata.TeamMembers {
		if member.UserID != base.Viewer.ID {
			t.Fatal("loaded another user's memberships")
		}
	}
}

func TestIssueReferenceMetadataRedactsHiddenScopesUsingRecordIDs(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	ctx := context.Background()
	workspace := repo.Bootstrap().Workspace.URLKey
	insert := func(field, id string, value any) {
		raw, _ := json.Marshal(value)
		if _, err := repo.db.ExecContext(ctx, `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,?,?,0,?)`, workspace, field, id, raw); err != nil {
			t.Fatal(err)
		}
	}
	insert("projects", "visible-project", map[string]any{"teamIds": []string{"visible", "hidden"}, "description": strings.Repeat("x", 1<<20)})
	insert("projects", "hidden-project", map[string]any{"teamIds": []string{"hidden"}})
	insert("projects", "unscoped-project", map[string]any{"teamIds": []string{}})
	insert("labels", "visible-label", map[string]any{"scope": "visible"})
	insert("labels", "hidden-label", map[string]any{"scope": "hidden"})
	insert("labels", "workspace-label", map[string]any{"scope": "workspace"})
	issues := []domain.Issue{}
	for _, id := range []string{"visible-project", "hidden-project", "unscoped-project"} {
		issues = append(issues, domain.Issue{Project: &domain.ProjectSummary{ID: id}, Labels: []domain.IssueLabel{{ID: "visible-label"}, {ID: "hidden-label"}, {ID: "workspace-label"}}})
	}
	query := IssueRecordQuery{Workspace: workspace, Access: &IssueRecordAccess{VisibleTeamIDs: []string{"visible"}}}
	metadata, err := repo.IssueReferenceMetadata(ctx, query, issues)
	if err != nil {
		t.Fatal(err)
	}
	if len(metadata.Projects) != 2 || len(metadata.Labels) != 2 {
		t.Fatalf("bad reference projection: %+v", metadata)
	}
	for _, project := range metadata.Projects {
		if project.ID == "hidden-project" || project.Description != "" {
			t.Fatal("copied hidden project or description")
		}
	}
	query.AllowedTeamIDs = []string{"visible"}
	metadata, err = repo.IssueReferenceMetadata(ctx, query, issues)
	if err != nil || len(metadata.Projects) != 1 || metadata.Projects[0].ID != "visible-project" {
		t.Fatal("API key scope exposed unscoped or hidden project", err)
	}
}

func BenchmarkIssueAccessMetadataLargeDirectory(b *testing.B) {
	data := domain.Bootstrap{Workspace: domain.Workspace{ID: "workspace", URLKey: "workspace"}, TeamSettings: map[string]domain.TeamSettings{}}
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("team-%d", i)
		data.Teams = append(data.Teams, domain.Team{ID: id})
		data.TeamSettings[id] = domain.TeamSettings{TeamID: id}
	}
	for i := 0; i < 10000; i++ {
		data.Users = append(data.Users, domain.User{ID: fmt.Sprintf("user-%d", i)})
		data.Projects = append(data.Projects, domain.Project{ID: fmt.Sprintf("project-%d", i), Description: strings.Repeat("x", 1024)})
	}
	for _, full := range []bool{false, true} {
		name := "policy-only"
		if full {
			name = "previous-full-metadata"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if full {
					_ = cloneBootstrap(data)
				} else {
					_ = issueAccessMetadata(data)
				}
			}
		})
	}
}
