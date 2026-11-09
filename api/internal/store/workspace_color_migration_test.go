package store

import (
	"path/filepath"
	"testing"

	"flow/api/internal/domain"
)

// The colour every workspace used to be created with is cleared (any letter
// case); a colour set on purpose stays; a reopened store keeps the result.
func TestWorkspaceInitialsColorMigration(t *testing.T) {
	for _, tc := range []struct{ stored, want string }{
		{"#5E6AD2", ""},
		{"#5e6ad2", ""},
		{"lch(70 60 90)", "lch(70 60 90)"},
		{"", ""},
	} {
		path := filepath.Join(t.TempDir(), "color.db")
		repo, err := OpenSQLiteTestFixture(path)
		if err != nil {
			t.Fatal(err)
		}
		key := repo.Bootstrap().Workspace.URLKey
		if err := repo.MutateWorkspace(WithMetadataFields(t.Context(), "workspace"), key, "test.legacy", "", nil, func(data *domain.Bootstrap) error {
			data.Workspace.Color = tc.stored
			return nil
		}); err != nil && tc.stored != "" {
			t.Fatal(err)
		}
		for range 2 {
			if err := repo.migrateWorkspaceInitialsColor(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
		if err := repo.Close(); err != nil {
			t.Fatal(err)
		}
		reopened, err := OpenSQLiteTestFixture(path)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := reopened.WorkspaceMetadata(key)
		reopened.Close()
		if got.Workspace.Color != tc.want {
			t.Errorf("stored %q: colour after migration = %q, want %q", tc.stored, got.Workspace.Color, tc.want)
		}
	}
}

// A new workspace stores no colour, so its initials use the colour its id maps to.
func TestEmptyWorkspaceHasNoInitialsColor(t *testing.T) {
	if color := EmptyWorkspace("Acme", "acme", "", domain.User{ID: "usr_a"}).Workspace.Color; color != "" {
		t.Fatalf("new workspace colour = %q, want none", color)
	}
}
