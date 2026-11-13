package store

import (
	"context"
	"strings"

	"flow/api/internal/domain"
)

// workspaceInitialsColorMigrationVersion records the one-time removal of the
// colour every workspace used to be created with.
const workspaceInitialsColorMigrationVersion = 1010

// legacyWorkspaceColor is the colour EmptyWorkspace stored on every new
// workspace before a workspace without a logo painted its initials in the
// colour its id maps to.
const legacyWorkspaceColor = "#5E6AD2"

// migrateWorkspaceInitialsColor clears the stored legacy default so the
// workspace initials use the colour derived from the workspace id, like a
// workspace created now. Any other stored colour was set on purpose through
// the API and is kept. Workspaces without the default are skipped, so the
// migration is idempotent.
func (s *SQLiteStore) migrateWorkspaceInitialsColor(ctx context.Context) error {
	for _, key := range s.WorkspaceKeys() {
		ctx := WithMetadataFields(WithoutIssueRecordMutations(ctx), "workspace")
		if err := s.MutateWorkspaceScoped(ctx, key, "workspace.initials_color_migrated", "", nil, MetadataRecordScope{Fields: []string{"workspace"}}, func(data *domain.Bootstrap) error {
			if !strings.EqualFold(strings.TrimSpace(data.Workspace.Color), legacyWorkspaceColor) {
				return ErrNoMutation
			}
			data.Workspace.Color = ""
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}
