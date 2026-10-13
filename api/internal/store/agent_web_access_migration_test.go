package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// The web access migration keeps web access off only where the stored record shows an admin turned it off, and leaves
// never-set workspaces unset (default on).
func TestAgentWebAccessMigration(t *testing.T) {
	enabledAt := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	on := true
	for _, test := range []struct {
		name string
		// enableEvent adds a settings save that turned web search on.
		enableEvent bool
		settings    func(*domain.WorkspaceSettings)
		// want is nil when the setting stays unset.
		want *bool
	}{
		{name: "never set", settings: func(s *domain.WorkspaceSettings) { s.UpdatedAt = enabledAt.Add(time.Hour) }},
		{name: "never set, settings saved before anyone enabled web search", enableEvent: true, settings: func(s *domain.WorkspaceSettings) { s.UpdatedAt = enabledAt.Add(-time.Hour) }},
		{name: "opt-in era: enabled, then turned off", enableEvent: true, settings: func(s *domain.WorkspaceSettings) { s.UpdatedAt = enabledAt.Add(time.Hour) }, want: new(bool)},
		{name: "turned off after web access became default-on", settings: func(s *domain.WorkspaceSettings) { s.AgentWebSearchDisabled = true }, want: new(bool)},
		{name: "explicitly on", enableEvent: true, settings: func(s *domain.WorkspaceSettings) { s.AgentWebSearch = true; s.UpdatedAt = enabledAt.Add(time.Hour) }, want: &on},
		{name: "already explicit", enableEvent: true, settings: func(s *domain.WorkspaceSettings) { s.AgentWebAccess = &on; s.UpdatedAt = enabledAt.Add(time.Hour) }, want: &on},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "flow.db")
			repo, err := OpenSQLiteTestFixture(path)
			if err != nil {
				t.Fatal(err)
			}
			var updatedAt time.Time
			if err := repo.MutateWorkspace(ctx, accessWorkspace, "test.seeded", "workspace", nil, func(next *domain.Bootstrap) error {
				next.WorkspaceSettings.AgentWebSearch, next.WorkspaceSettings.AgentWebSearchDisabled, next.WorkspaceSettings.AgentWebAccess = false, false, nil
				test.settings(&next.WorkspaceSettings)
				updatedAt = next.WorkspaceSettings.UpdatedAt
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if test.enableEvent {
				if _, err := repo.db.ExecContext(ctx, `INSERT INTO domain_events(id,event_type,aggregate_id,payload,created_at) VALUES(?,?,?,?,?)`, "evt_web_on", "workspace_preferences.updated", "workspace", []byte(`{"agentWebSearch":true}`), enabledAt.Format(time.RFC3339Nano)); err != nil {
					t.Fatal(err)
				}
			}
			// Unrelated saves that echoed the default (false) are not evidence of a choice.
			if _, err := repo.db.ExecContext(ctx, `INSERT INTO domain_events(id,event_type,aggregate_id,payload,created_at) VALUES(?,?,?,?,?)`, "evt_web_echo", "workspace_preferences.updated", "workspace", []byte(`{"agentWebSearch":false,"preventGuestAgents":true}`), enabledAt.Add(-2*time.Hour).Format(time.RFC3339Nano)); err != nil {
				t.Fatal(err)
			}
			if _, err := repo.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version=?`, agentWebAccessMigrationVersion); err != nil {
				t.Fatal(err)
			}
			if err := repo.Close(); err != nil {
				t.Fatal(err)
			}
			check := func(stage string, repo *SQLiteStore) {
				t.Helper()
				data, ok := repo.WorkspaceSettingsMetadata(accessWorkspace)
				if !ok {
					t.Fatal("workspace missing")
				}
				settings := data.WorkspaceSettings
				switch {
				case test.want == nil && settings.AgentWebAccess != nil:
					t.Fatalf("%s: web access = %v, want unset", stage, *settings.AgentWebAccess)
				case test.want != nil && (settings.AgentWebAccess == nil || *settings.AgentWebAccess != *test.want):
					t.Fatalf("%s: web access = %v, want %v", stage, settings.AgentWebAccess, *test.want)
				}
				if enabled := test.want == nil || *test.want; settings.AgentWebAccessEnabled() != enabled || settings.AgentWebSearchDisabled == enabled {
					t.Fatalf("%s: enabled = %v disabled flag = %v, want enabled %v", stage, settings.AgentWebAccessEnabled(), settings.AgentWebSearchDisabled, enabled)
				}
				// The migration records an existing choice; it is not an edit.
				if !settings.UpdatedAt.Equal(updatedAt) {
					t.Fatalf("%s: updatedAt = %v, want %v", stage, settings.UpdatedAt, updatedAt)
				}
			}
			repo, err = OpenSQLiteTestFixture(path)
			if err != nil {
				t.Fatal(err)
			}
			check("migrated", repo)
			if applied, err := repo.migrationApplied(ctx, agentWebAccessMigrationVersion); err != nil || !applied {
				t.Fatalf("migration not recorded: %v %v", applied, err)
			}
			if err := repo.Close(); err != nil {
				t.Fatal(err)
			}
			// The result is stored: a reopen (migration already applied) reads the same value.
			repo, err = OpenSQLiteTestFixture(path)
			if err != nil {
				t.Fatal(err)
			}
			defer repo.Close()
			check("reopened", repo)
		})
	}
}
