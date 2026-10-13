//go:build integration

package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// TestExternalDatabaseTeamMembershipOrigin runs the origin migration and an
// origin-scoped, dry-run removal against MySQL or Postgres
// (FLOW_TEST_DATABASE_DRIVER / FLOW_TEST_DATABASE_URL).
func TestExternalDatabaseTeamMembershipOrigin(t *testing.T) {
	driver, databaseURL := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL")
	if driver == "" || databaseURL == "" {
		t.Skip("external database configuration is not set")
	}
	repository, err := OpenDatabase(DatabaseConfig{Driver: driver, URL: databaseURL, MaxOpenConns: 4, MaxIdleConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	ctx := context.Background()
	const workspaceKey = "integration-team-origin"
	_ = repository.DeleteWorkspace(ctx, workspaceKey)
	created, err := repository.CreateWorkspace(ctx, "Team origin", workspaceKey, "us")
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkspace(ctx, workspaceKey)
	// The migration is idempotent on every dialect.
	if err := repository.addTeamMembershipOrigin(ctx); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	email := fmt.Sprintf("origin-%d@example.test", time.Now().UnixNano())
	user, _, err := repository.Register(ctx, "Origin member", email, "integration-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.db.ExecContext(ctx, `INSERT INTO workspace_memberships(workspace_id,user_id,role,status,joined_at,last_seen_at) VALUES(?,?,'member','active',?,?)`, created.Workspace.ID, user.ID, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	teamID := created.Teams[0].ID
	if _, err := repository.ChangeTeamMembership(ctx, TeamMembershipChangeRequest{WorkspaceID: created.Workspace.ID, TeamID: teamID, UserID: user.ID, Member: true, Origin: domain.TeamMemberOriginSync}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ChangeTeamMembership(ctx, TeamMembershipChangeRequest{WorkspaceID: created.Workspace.ID, TeamID: teamID, UserID: user.ID, RequireOrigins: []string{domain.TeamMemberOriginManual}, KeepOwners: true}); !errors.Is(err, ErrTeamMemberOriginMismatch) {
		t.Fatalf("mismatch error = %v", err)
	}
	dry, err := repository.ChangeTeamMembership(ctx, TeamMembershipChangeRequest{WorkspaceID: created.Workspace.ID, TeamID: teamID, UserID: user.ID, RequireOrigins: []string{domain.TeamMemberOriginSync}, KeepOwners: true, DryRun: true})
	if err != nil || len(dry.Changes) != 1 {
		t.Fatalf("dry run = %+v err=%v", dry, err)
	}
	member, found, err := repository.TeamMembership(ctx, created.Workspace.ID, teamID, user.ID)
	if err != nil || !found || member.Origin != domain.TeamMemberOriginSync {
		t.Fatalf("membership after dry run = %+v found=%v err=%v", member, found, err)
	}
	if _, err := repository.ChangeTeamMembership(ctx, TeamMembershipChangeRequest{WorkspaceID: created.Workspace.ID, TeamID: teamID, UserID: user.ID, RequireOrigins: []string{domain.TeamMemberOriginSync}, KeepOwners: true}); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := repository.TeamMembership(ctx, created.Workspace.ID, teamID, user.ID); found {
		t.Fatal("sync membership was not removed")
	}
}
