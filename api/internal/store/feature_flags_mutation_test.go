package store

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestFeatureFlagsUseOnlySettingsRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flags.db")
	repo, err := OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	data := repo.Bootstrap()
	key := data.Workspace.URLKey
	writes := auditWrites(t, repo)
	for _, enabled := range []bool{false, true, false} {
		err := repo.MutateWorkspace(t.Context(), key, "workspace_preferences.updated", "workspace", map[string]any{"featureFlags": map[string]bool{"pulse": enabled}}, func(next *domain.Bootstrap) error {
			if len(next.Issues)+len(next.Projects)+len(next.Users)+len(next.Documents)+len(next.Comments)+len(next.Activities) != 0 {
				t.Fatal("toggle received unrelated workspace collections")
			}
			next.WorkspaceSettings.FeatureFlags["pulse"] = enabled
			next.WorkspaceSettings.UpdatedAt = time.Now().UTC()
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		changed := writes()
		if changed["workspace_metadata_records"] != 2 || changed["workspace_states"] != 0 || changed["issue_records"] != 0 || changed["workspace_content_records"] != 0 {
			t.Fatalf("toggle rewrote unrelated data: %v", changed)
		}
	}
	failure := errors.New("validation failed")
	err = repo.MutateWorkspace(t.Context(), key, "workspace_preferences.updated", "workspace", map[string]any{"featureFlags": map[string]bool{"pulse": true}}, func(next *domain.Bootstrap) error {
		next.WorkspaceSettings.FeatureFlags["pulse"] = true
		return failure
	})
	if !errors.Is(err, failure) {
		t.Fatalf("missing validation error: %v", err)
	}
	if changes := writes(); len(changes) != 0 {
		t.Fatalf("failed toggle wrote rows: %v", changes)
	}
	repo.Close()
	repo, err = OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	settings, _ := repo.WorkspaceMetadata(key)
	if settings.WorkspaceSettings.FeatureFlags["pulse"] || !settings.WorkspaceSettings.FeatureFlags["customer-requests"] {
		t.Fatal("toggle or unrelated settings changed after restart")
	}
	if len(repo.Bootstrap().Issues) != len(data.Issues) {
		t.Fatal("issue collection was altered")
	}
}

func TestFeatureFlagFastPathIsExplicit(t *testing.T) {
	for _, value := range []string{`{}`, `{"featureFlags":null}`, `{"featureFlags":{}}`, `{"featureFlags":{"pulse":"false"}}`, `{"featureFlags":{"pulse":false},"welcomeMessage":"changed"}`} {
		var payload map[string]json.RawMessage
		if err := json.Unmarshal([]byte(value), &payload); err != nil {
			t.Fatal(err)
		}
		if featureFlagsOnly(payload) {
			t.Fatalf("accepted broader or invalid payload %s", value)
		}
	}
}
