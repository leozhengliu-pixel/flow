package main

import (
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// A missing row receives a newly generated timestamp on a legacy reload.
// Persist the fixture preferences so the permission oracles can compare every field.
func seedDifferentialPreferences(t *testing.T, repo *store.SQLiteStore, workspace string, users []domain.User) {
	t.Helper()
	stamp := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	if err := repo.MutateWorkspace(workspaceFixtureContext(t.Context()), workspace, "fixture.preferences", "", nil, func(data *domain.Bootstrap) error {
		for _, user := range users {
			settings, ok := data.UserSettings[user.ID]
			if !ok {
				settings = data.UserSettings[data.Viewer.ID]
			}
			preferences, ok := data.NotificationPreferences[user.ID]
			if !ok {
				preferences = data.NotificationPreferences[data.Viewer.ID]
			}
			settings.UserID, settings.UpdatedAt = user.ID, stamp
			preferences.UserID, preferences.UpdatedAt = user.ID, stamp
			data.UserSettings[user.ID] = settings
			data.NotificationPreferences[user.ID] = preferences
		}
		return nil
	}); err != nil {
		t.Fatal("could not persist differential preferences")
	}
}
