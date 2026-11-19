package store

import (
	"context"
	"fmt"
	"sort"

	"flow/api/internal/domain"
)

// The startup syncs SyncAllApplicationIdentities and BackfillUploadOwners
// enumerate every workspace's OAuth applications. With bounded metadata on
// the installed image does not hold that field (it is a record field), so
// reading the image directly found no application: the identity sync was a
// no-op and no application icon owner was recorded. Both now read the field
// from the legacy view of each installed workspace (coldAccessorView: the
// complete field decoded from the installed logical source, the decode the
// legacy accessor performs), so they see exactly the applications the
// legacy store holds. Flag off, and on a store without installed sources
// (a test view), the image is read as before.

// installedOAuthApplications calls visit with each installed workspace's OAuth
// applications, as the legacy store holds them, in workspace key order. The
// slice visit receives is its own (it may keep it). A workspace whose field
// cannot be read fails the enumeration.
func (s *SQLiteStore) installedOAuthApplications(ctx context.Context, accessor string, visit func(workspace string, data domain.Bootstrap, apps []domain.OAuthApplication) error) error {
	s.mu.RLock()
	keys := make([]string, 0, len(s.workspaces))
	for key := range s.workspaces {
		keys = append(keys, key)
	}
	s.mu.RUnlock()
	sort.Strings(keys)
	for _, key := range keys {
		s.mu.RLock()
		data, exists := s.workspaces[key]
		s.mu.RUnlock()
		if !exists {
			continue // removed meanwhile
		}
		apps := data.OAuthApplications
		if view, release, handled := s.coldAccessorView(ctx, accessor, key, []string{"oauthApplications"}); handled {
			if view == nil {
				release()
				if err := ctx.Err(); err != nil {
					return err
				}
				if !s.HasWorkspace(key) {
					continue
				}
				return fmt.Errorf("%w: %s could not read the OAuth applications of workspace %q", ErrBoundedMetadataCoverage, accessor, key)
			}
			view.mu.RLock()
			apps = view.workspaces[view.lastWorkspaceKey].OAuthApplications
			view.mu.RUnlock()
			release()
		}
		if err := visit(key, data, apps); err != nil {
			return err
		}
	}
	return nil
}
