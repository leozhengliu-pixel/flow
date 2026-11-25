package store

import (
	"context"
	"strings"
	"sync"
)

const installedWorkspaceIdentityMaxBytes = 1 << 20

// InstalledWorkspaceIdentity separates the physical registry key from the
// logical URLKey the installed legacy metadata accessor returns.
type InstalledWorkspaceIdentity struct {
	RegistryKey, URLKey, ID string
}

// InstalledWorkspaceIdentityRead owns three detached strings and their work
// allowance until Close. Identity must not be retained beyond that lifetime.
type InstalledWorkspaceIdentityRead struct {
	Identity InstalledWorkspaceIdentity
	release  func()
	once     sync.Once
}

func (r *InstalledWorkspaceIdentityRead) Close() error {
	if r != nil {
		r.once.Do(func() {
			r.Identity = InstalledWorkspaceIdentity{}
			if r.release != nil {
				r.release()
				r.release = nil
			}
		})
	}
	return nil
}

// BeginInstalledWorkspaceIdentityRead is a trusted installed-map read. Empty
// workspace selects the default under the same lock as the source lookup.
// Callers preserve their original accessor off mode and use Identity.URLKey
// when reproducing that accessor's logical field, including after reload.
// RegistryKey is separate: substituting it can change legacy native lookups.
func (s *SQLiteStore) BeginInstalledWorkspaceIdentityRead(ctx context.Context, workspace string) (*InstalledWorkspaceIdentityRead, bool, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		cost, found, err := s.installedWorkspaceIdentityCost(workspace)
		if err != nil || !found {
			return nil, found, err
		}
		// A retained result can outlive a parent lease. Always reserve a
		// separate allowance, without waiting while holding the store lock.
		_, release, err := s.tryAdmitAdditionalMetadataWork(ctx, cost)
		if err != nil {
			return nil, true, err
		}
		s.mu.RLock()
		selected := workspace
		if selected == "" {
			selected = s.lastWorkspaceKey
		}
		source, found := s.workspaces[selected]
		current, err := installedWorkspaceIdentityCost(selected, source.Workspace.URLKey, source.Workspace.ID)
		if !found || err != nil || current > cost || ctx.Err() != nil {
			s.mu.RUnlock()
			release()
			if ctx.Err() != nil {
				return nil, found, ctx.Err()
			}
			if !found || err != nil {
				return nil, found, err
			}
			continue
		}
		identity := InstalledWorkspaceIdentity{
			RegistryKey: strings.Clone(selected),
			URLKey:      strings.Clone(source.Workspace.URLKey),
			ID:          strings.Clone(source.Workspace.ID),
		}
		s.mu.RUnlock()
		if err := ctx.Err(); err != nil {
			release()
			return nil, true, err
		}
		return &InstalledWorkspaceIdentityRead{Identity: identity, release: release}, true, nil
	}
}

func (s *SQLiteStore) installedWorkspaceIdentityCost(workspace string) (int, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if workspace == "" {
		workspace = s.lastWorkspaceKey
	}
	source, found := s.workspaces[workspace]
	if !found {
		return 0, false, nil
	}
	cost, err := installedWorkspaceIdentityCost(workspace, source.Workspace.URLKey, source.Workspace.ID)
	return cost, true, err
}

func installedWorkspaceIdentityCost(values ...string) (int, error) {
	bytes := 0
	for _, value := range values {
		if len(value) > installedWorkspaceIdentityMaxBytes-bytes {
			return 0, ErrMetadataWorkBudget
		}
		bytes += len(value)
	}
	// Include the handle/lease and allocator rounding for three detached
	// strings plus their bounded downstream scalar arguments.
	return 4096 + 8*bytes, nil
}
