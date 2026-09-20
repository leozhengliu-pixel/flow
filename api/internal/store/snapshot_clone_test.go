package store

import (
	"path/filepath"
	"testing"
)

func TestWorkspaceSettingsMetadataOmitsTeamsAndStates(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "settings-meta.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	_ = seedBulkTeams(t, repo, 400)
	data, ok := repo.WorkspaceSettingsMetadata("")
	if !ok || len(data.Teams) != 0 || len(data.States) != 0 || data.Workspace.ID == "" {
		t.Fatalf("settings metadata leaked catalog: teams=%d states=%d workspace=%q", len(data.Teams), len(data.States), data.Workspace.ID)
	}
}
