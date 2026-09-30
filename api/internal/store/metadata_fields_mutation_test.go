package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"flow/api/internal/domain"
)

// A field-scoped metadata mutation hands the callback only the named fields,
// persists its change to them, and leaves every other field exactly as
// stored (in memory and after a reopen).
func TestFieldScopedMetadataMutationKeepsOtherFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fields.db")
	repo, err := OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	workspace := repo.Bootstrap().Workspace.URLKey
	before, _ := repo.WorkspaceMetadata(workspace)
	ctx := WithMetadataFields(context.Background(), "settings")
	err = repo.MutateWorkspace(ctx, workspace, "pulse.summary_scheduled", "usr_admin", nil, func(data *domain.Bootstrap) error {
		if len(data.Teams) != 0 || len(data.Projects) != 0 {
			t.Errorf("callback saw unlisted fields: %d teams, %d projects", len(data.Teams), len(data.Projects))
		}
		if data.Settings == nil {
			data.Settings = map[string]any{}
		}
		data.Settings["pulseDeliveryCursors"] = map[string]string{"usr_admin": "2026-01-01T00:00:00Z"}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	check := func(label string, data domain.Bootstrap) {
		t.Helper()
		if _, ok := data.Settings["pulseDeliveryCursors"]; !ok {
			t.Fatalf("%s: settings change was not kept", label)
		}
		// Project issue counts are derived on read, not stored.
		projects := func(values []domain.Project) []domain.Project {
			result := make([]domain.Project, len(values))
			for index, project := range values {
				project.IssueCount = 0
				result[index] = project
			}
			return result
		}
		for name, pair := range map[string][2]any{"teams": {before.Teams, data.Teams}, "projects": {projects(before.Projects), projects(data.Projects)}, "users": {before.Users, data.Users}, "states": {before.States, data.States}} {
			left, _ := json.Marshal(pair[0])
			right, _ := json.Marshal(pair[1])
			if string(left) != string(right) {
				t.Fatalf("%s: %s changed:\nbefore %.500s\nafter  %.500s", label, name, left, right)
			}
		}
	}
	after, _ := repo.WorkspaceMetadata(workspace)
	check("memory", after)
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	stored, _ := reopened.WorkspaceMetadata(workspace)
	check("reopened", stored)
}
