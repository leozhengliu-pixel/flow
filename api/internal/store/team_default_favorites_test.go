package store

import (
	"bytes"
	"path/filepath"
	"reflect"
	"testing"

	"flow/api/internal/domain"
)

func TestTeamDefaultFavoritesMergeAndOverride(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "defaults.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	data := repo.Bootstrap()
	team, issue := data.Teams[0], data.Issues[0]
	var before []byte
	if err := repo.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_states WHERE workspace_key=?`, data.Workspace.URLKey).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := repo.ReplaceTeamDefaultFavorites(t.Context(), data.Workspace.URLKey, team.ID, []domain.TeamDefaultFavorite{{ResourceType: "issue", ResourceID: issue.ID}, {ResourceType: "team", ResourceID: team.ID}}); err != nil {
		t.Fatal(err)
	}
	metadata, err := repo.PagedWorkspaceMetadata(t.Context(), data.Workspace.URLKey, data.Viewer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(metadata.Favorites); got != 2 {
		t.Fatalf("merged defaults=%d, want 2", got)
	}
	var after []byte
	if err := repo.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_states WHERE workspace_key=?`, data.Workspace.URLKey).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("default favorite configuration rewrote workspace JSON state")
	}
	for _, item := range metadata.Favorites {
		if item.ID[:len("team-default:")] != "team-default:" {
			t.Fatalf("default favorite provenance missing: %#v", item)
		}
	}
	if err := repo.SetTeamDefaultFavoriteOverride(t.Context(), data.Workspace.URLKey, data.Viewer.ID, "issue", issue.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := repo.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_states WHERE workspace_key=?`, data.Workspace.URLKey).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("member override rewrote workspace JSON state")
	}
	metadata, err = repo.PagedWorkspaceMetadata(t.Context(), data.Workspace.URLKey, data.Viewer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(metadata.Favorites) != 1 || metadata.Favorites[0].ResourceType != "team" {
		t.Fatalf("hidden default still visible: %#v", metadata.Favorites)
	}
	if err := repo.SetTeamDefaultFavoriteOverride(t.Context(), data.Workspace.URLKey, data.Viewer.ID, "issue", issue.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := repo.MutateWorkspace(t.Context(), data.Workspace.URLKey, "favorite.added", issue.ID, nil, func(next *domain.Bootstrap) error {
		next.Favorites = append(next.Favorites, domain.Favorite{ID: "personal-issue", UserID: data.Viewer.ID, ResourceType: "issue", ResourceID: issue.ID})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	metadata, err = repo.PagedWorkspaceMetadata(t.Context(), data.Workspace.URLKey, data.Viewer.ID)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, item := range metadata.Favorites {
		if item.ResourceType == "issue" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("personal/default duplicate: %d", count)
	}
}

func TestTeamDefaultFavoritesRepeatedSaveHasNoWritesAndFutureMembersInherit(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "defaults.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	data := repo.Bootstrap()
	ctx := t.Context()
	workspace, teamID := data.Workspace.URLKey, data.Teams[0].ID
	// Recording table mutations catches writes even when stored values are identical.
	for _, statement := range []string{
		`CREATE TABLE default_favorite_writes (operation TEXT)`,
		`CREATE TRIGGER audit_default_insert AFTER INSERT ON team_default_favorites BEGIN INSERT INTO default_favorite_writes VALUES ('insert'); END`,
		`CREATE TRIGGER audit_default_update AFTER UPDATE ON team_default_favorites BEGIN INSERT INTO default_favorite_writes VALUES ('update'); END`,
		`CREATE TRIGGER audit_default_delete AFTER DELETE ON team_default_favorites BEGIN INSERT INTO default_favorite_writes VALUES ('delete'); END`,
	} {
		if _, err := repo.db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	items := []domain.TeamDefaultFavorite{{ResourceType: "project", ResourceID: data.Projects[0].ID}, {ResourceType: "team", ResourceID: teamID}}
	if err := repo.ReplaceTeamDefaultFavorites(ctx, workspace, teamID, items); err != nil {
		t.Fatal(err)
	}
	before, err := repo.ListTeamDefaultFavorites(ctx, workspace, teamID)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := repo.ReplaceTeamDefaultFavorites(ctx, workspace, teamID, items); err != nil {
			t.Fatal(err)
		}
	}
	after, err := repo.ListTeamDefaultFavorites(ctx, workspace, teamID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("unchanged save altered rows: %v %+v", err, after)
	}
	var writes int
	if err := repo.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM default_favorite_writes`).Scan(&writes); err != nil || writes != 2 {
		t.Fatalf("repeated saves wrote rows: %d %v", writes, err)
	}
	// A membership added after configuration gets defaults on read, with no fan-out.
	if _, err := repo.db.ExecContext(ctx, `DELETE FROM team_memberships WHERE workspace_id=? AND team_id=? AND user_id=?`, data.Workspace.ID, teamID, data.Viewer.ID); err != nil {
		t.Fatal(err)
	}
	metadata := domain.Bootstrap{Workspace: data.Workspace}
	if err := repo.ApplyTeamDefaultFavorites(ctx, &metadata, data.Viewer.ID); err != nil || len(metadata.Favorites) != 0 {
		t.Fatalf("non-member inherited: %v %+v", err, metadata.Favorites)
	}
	if _, err := repo.db.ExecContext(ctx, `INSERT INTO team_memberships(workspace_id,team_id,user_id,role,joined_at) VALUES(?,?,?,'member','2026-09-14T00:00:00Z')`, data.Workspace.ID, teamID, data.Viewer.ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.ApplyTeamDefaultFavorites(ctx, &metadata, data.Viewer.ID); err != nil || len(metadata.Favorites) != 2 {
		t.Fatalf("new member defaults: %v %+v", err, metadata.Favorites)
	}
	if err := repo.SetTeamDefaultFavoriteOverride(ctx, workspace, data.Viewer.ID, "project", data.Projects[0].ID, true); err != nil {
		t.Fatal(err)
	}
	metadata.Favorites = nil
	if err := repo.ApplyTeamDefaultFavorites(ctx, &metadata, data.Viewer.ID); err != nil || len(metadata.Favorites) != 1 {
		t.Fatalf("hidden default: %v %+v", err, metadata.Favorites)
	}
	after, err = repo.ListTeamDefaultFavorites(ctx, workspace, teamID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("member hiding changed team defaults")
	}
	if err := repo.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM default_favorite_writes`).Scan(&writes); err != nil || writes != 2 {
		t.Fatalf("membership/read/hide wrote default rows: %d %v", writes, err)
	}
}
