package store

import (
	"bytes"
	"path/filepath"
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
