package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func scaleP18Fixture(t *testing.T) (*store.SQLiteStore, mcpActor, context.Context) {
	t.Helper()
	repo, err := store.OpenDatabase(scaleP03DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	resources := scaleP03TrackResources(t, repo)
	user, token, err := repo.Register(t.Context(), "P18 owner", idgen.New("p18-")+"@example.test", "p18-fixture-password")
	if err != nil {
		t.Fatal(err)
	}
	resources.users = append(resources.users, user)
	if err := repo.VerifyEmail(t.Context(), token); err != nil {
		t.Fatal(err)
	}
	user.EmailVerified = true
	ctx := store.ContextWithActor(t.Context(), user)
	data, err := repo.CreateWorkspace(ctx, "P18 isolated", idgen.New("p18-"), "us")
	if err != nil {
		t.Fatal(err)
	}
	resources.workspaces = append(resources.workspaces, data.Workspace.URLKey)
	actor := mcpActor{WorkspaceKey: data.Workspace.URLKey, User: user, APIKey: domain.APIKey{Scopes: []string{"read", "write"}}}
	ctx = context.WithValue(ctx, authUserContextKey{}, user)
	ctx = context.WithValue(ctx, workspaceKeyContextKey{}, actor.WorkspaceKey)
	return repo, actor, ctx
}

func TestScaleP18ReadCosts(t *testing.T) {
	if os.Getenv("FLOW_SCALE_P18_MEASURE") != "1" {
		t.Skip("set FLOW_SCALE_P18_MEASURE=1 for the before/after allocation measurement")
	}
	for _, count := range []int{100, 1000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			repo, actor, ctx := scaleP18Fixture(t)
			if err := repo.MutateWorkspace(ctx, actor.WorkspaceKey, "scale_p18.seeded", "", nil, func(data *domain.Bootstrap) error {
				for i := range count {
					data.Projects = append(data.Projects, domain.Project{ID: fmt.Sprintf("p18-project-%04d", i), Name: fmt.Sprintf("Project %04d", i), Description: strings.Repeat("body ", 1600), TeamIDs: []string{data.Teams[0].ID}, Resources: []domain.ProjectResource{{ID: "resource", Title: "Context"}}})
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			service := &server{store: repo}
			args := map[string]any{"limit": float64(2)}
			queries := 0
			page, err := service.callFlowTool(store.WithSQLStatementObserver(ctx, func(string) { queries++ }), actor, "list_projects", args)
			if err != nil {
				t.Fatal(err)
			}
			if len(page.(map[string]any)["items"].([]domain.Project)) != 2 {
				t.Fatal("measurement must return exactly two projects")
			}
			result := testing.Benchmark(func(b *testing.B) {
				for range b.N {
					if _, err := service.callFlowTool(ctx, actor, "list_projects", args); err != nil {
						b.Fatal(err)
					}
				}
			})
			t.Logf("projects=%d sql/op=%d %s %s", count, queries, result.String(), result.MemString())
			if internalRecordsEnabled("FLOW_MCP_PROJECT_RECORDS") && result.AllocedBytesPerOp() > 2<<20 {
				t.Fatalf("small project page allocated %d bytes", result.AllocedBytesPerOp())
			}
		})
	}
}
