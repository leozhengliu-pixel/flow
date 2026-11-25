package main

import (
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Raw SQL used to poison fixture payloads is captured as an old writer.
// Repair those journal keys while leaving the deliberately unreadable bodies.
func repairFixtureJournal(t *testing.T, repo *store.SQLiteStore) {
	t.Helper()
	if !repo.RecordJournalReady() {
		return
	}
	report, err := repo.RepairRecordJournalPass(t.Context())
	if err != nil || len(report.Incomplete) != 0 {
		t.Fatal("fixture journal repair did not complete")
	}
}

// A missing row receives a newly generated timestamp on a legacy reload.
// Persist the fixture preferences so the permission oracles can compare every field.
func seedDifferentialPreferences(t *testing.T, repo *store.SQLiteStore, workspace string, users []domain.User) {
	t.Helper()
	ctx := t.Context()
	stamp := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	for _, user := range users {
		if err := repo.MutateUserSettings(ctx, workspace, user.ID, map[string]any{"language": "en-US"}, func(data *domain.Bootstrap) error {
			settings := data.UserSettings[user.ID]
			settings.UserID, settings.UpdatedAt = user.ID, stamp
			data.UserSettings[user.ID] = settings
			return nil
		}); err != nil {
			t.Fatal("could not persist differential user settings")
		}
		scope := store.MetadataRecordScope{Write: []store.RecordKey{{Kind: "notificationPreferences", ID: user.ID}}}
		if err := repo.MutateWorkspaceScoped(ctx, workspace, "notification_preferences.updated", user.ID, nil, scope, func(data *domain.Bootstrap) error {
			preferences := data.NotificationPreferences[user.ID]
			if preferences.UserID == "" {
				preferences = defaultPreferences(user.ID)
			}
			preferences.UpdatedAt = stamp
			if data.NotificationPreferences == nil {
				data.NotificationPreferences = map[string]domain.NotificationPreferences{}
			}
			data.NotificationPreferences[user.ID] = preferences
			return nil
		}); err != nil {
			t.Fatal("could not persist differential notification preferences")
		}
		scope.Read = append(scope.Write, store.RecordKey{Kind: "userSettings", ID: user.ID})
		scope.Write = nil
		loaded, err := repo.HydrateWorkspaceMetadata(ctx, workspace, scope)
		if err != nil || !loaded.Data.UserSettings[user.ID].UpdatedAt.Equal(stamp) || !loaded.Data.NotificationPreferences[user.ID].UpdatedAt.Equal(stamp) {
			t.Fatalf("fixture preference persistence: read=%t user_settings_stamp=%t notification_preferences_stamp=%t",
				err == nil, loaded.Data.UserSettings[user.ID].UpdatedAt.Equal(stamp), loaded.Data.NotificationPreferences[user.ID].UpdatedAt.Equal(stamp))
		}
		installed, ok, err := repo.WorkspaceMetadataScoped(ctx, workspace, scope)
		if err != nil || !ok || !installed.UserSettings[user.ID].UpdatedAt.Equal(stamp) || !installed.NotificationPreferences[user.ID].UpdatedAt.Equal(stamp) {
			t.Fatal("differential preferences were not installed")
		}
	}
}
