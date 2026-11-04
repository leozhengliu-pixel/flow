package store

import (
	"path/filepath"
	"testing"

	"flow/api/internal/domain"
)

func TestCommentActionsPreferenceMigration(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "actions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	data := repo.Bootstrap()
	id := data.Viewer.ID
	for _, explicit := range []*bool{nil, new(bool)} {
		settings := data.UserSettings[id]
		settings.SuggestedActionsOnComments = explicit
		if err := repo.MutateWorkspace(t.Context(), data.Workspace.URLKey, "test.legacy", id, nil, func(d *domain.Bootstrap) error { d.UserSettings[id] = settings; return nil }); err != nil {
			t.Fatal(err)
		}
		if err := repo.migrateCommentActions(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := repo.migrateCommentActions(t.Context()); err != nil {
			t.Fatal(err)
		}
		got, _ := repo.WorkspaceMetadata(data.Workspace.URLKey)
		if got.UserSettings[id].SuggestedActionsOnComments == nil {
			t.Fatal("migration did not persist an explicit choice")
		}
		want := explicit == nil
		if got.UserSettings[id].CommentActionsEnabled() != want {
			t.Fatalf("explicit=%v preference=%v", explicit, got.UserSettings[id].SuggestedActionsOnComments)
		}
	}
}
