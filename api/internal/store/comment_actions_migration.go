package store

import (
	"context"

	"flow/api/internal/domain"
)

const commentActionsMigrationVersion = 1009

func (s *SQLiteStore) migrateCommentActions(ctx context.Context) error {
	for _, key := range s.WorkspaceKeys() {
		ctx := WithMetadataFields(WithoutIssueRecordMutations(ctx), "userSettings")
		if err := s.MutateWorkspace(ctx, key, "user_settings.comment_actions_migrated", "", nil, func(data *domain.Bootstrap) error {
			changed := false
			for id, settings := range data.UserSettings {
				if settings.SuggestedActionsOnComments == nil {
					enabled := true
					settings.SuggestedActionsOnComments = &enabled
					data.UserSettings[id] = settings
					changed = true
				}
			}
			if !changed {
				return ErrNoMutation
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}
