package store

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

func TestTeamMembershipProtectsOwnersAndManagedMembers(t *testing.T) {
	repository, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "team-membership.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	data := repository.Bootstrap()
	teamID := data.Teams[0].ID
	ctx := t.Context()

	second, _, err := repository.Register(ctx, "Second owner", "second-owner@example.test", "test-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repository.db.ExecContext(ctx, `INSERT INTO workspace_memberships(workspace_id,user_id,role,status,joined_at,last_seen_at) VALUES(?,?,'member','active','2026-01-01','2026-01-01')`, data.Workspace.ID, second.ID); err != nil {
		t.Fatal(err)
	}
	if err = repository.SetTeamMembership(ctx, data.Workspace.ID, teamID, second.ID, "owner", true); err != nil {
		t.Fatal(err)
	}
	if err = repository.SetTeamMembership(ctx, data.Workspace.ID, teamID, data.Viewer.ID, "member", true); err != nil {
		t.Fatalf("demoting one of two owners failed: %v", err)
	}
	if err = repository.SetTeamMembership(ctx, data.Workspace.ID, teamID, second.ID, "member", true); !errors.Is(err, ErrLastTeamOwner) {
		t.Fatalf("last owner demotion error=%v", err)
	}
	if err = repository.SetTeamMembership(ctx, data.Workspace.ID, teamID, second.ID, "member", false); !errors.Is(err, ErrLastTeamOwner) {
		t.Fatalf("last owner removal error=%v", err)
	}
	if err = repository.SetTeamMembership(ctx, data.Workspace.ID, teamID, data.Viewer.ID, "owner", true); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var writers sync.WaitGroup
	for _, userID := range []string{data.Viewer.ID, second.ID} {
		writers.Add(1)
		go func() {
			defer writers.Done()
			<-start
			results <- repository.SetTeamMembership(ctx, data.Workspace.ID, teamID, userID, "member", true)
		}()
	}
	close(start)
	writers.Wait()
	close(results)
	succeeded, protected := 0, 0
	for result := range results {
		if result == nil {
			succeeded++
		} else if errors.Is(result, ErrLastTeamOwner) {
			protected++
		} else {
			t.Fatalf("concurrent demotion error=%v", result)
		}
	}
	if succeeded != 1 || protected != 1 {
		t.Fatalf("concurrent demotions succeeded=%d protected=%d", succeeded, protected)
	}

	if _, err = repository.db.ExecContext(ctx, `INSERT INTO scim_team_memberships(workspace_id,team_id,group_id,user_id,managed,updated_at) VALUES(?,?,?,?,1,'2026-01-01')`, data.Workspace.ID, teamID, "group-managed", data.Viewer.ID); err != nil {
		t.Fatal(err)
	}
	if err = repository.SetTeamMembership(ctx, data.Workspace.ID, teamID, data.Viewer.ID, "member", false); !errors.Is(err, ErrManagedTeamMembership) {
		t.Fatalf("managed membership removal error=%v", err)
	}
	members, err := repository.ListTeamMembers(ctx, data.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	foundManaged := false
	for _, member := range members {
		if member.TeamID == teamID && member.UserID == data.Viewer.ID {
			foundManaged = member.Managed && member.ManagedSource == "scim"
		}
	}
	if !foundManaged {
		t.Fatal("SCIM managed source was not projected with the team membership")
	}
}
