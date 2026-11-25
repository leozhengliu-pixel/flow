package store

import (
	"context"
	"fmt"
	"log/slog"
	"testing"

	"flow/api/internal/domain"
)

// A bounded store serves a workspace only through its installed resident
// image: the policy copy, its stamp and journal head, and the installed
// logical source every bounded reader and writer retains. A flag-on start
// installs one for every workspace it loads (loadResidentWorkspaces), after
// validateResidentBackfill certified the workspace's canonical records,
// record journal coverage and complete metadata catalog. A workspace created
// at runtime got none: the legacy creation installed a complete copy only,
// so every later read or write of the new workspace (account settings, an
// issue, the built-in agent's installation) and every background pass that
// visited it failed with ErrBoundedMetadataCoverage.
//
// The creation write already leaves the workspace covered: its records are
// written in one journaled transaction, whose flush opens each new
// collection's catalog checkpoint complete (bounded_catalog_collections.go)
// and catalogs its keys. createResidentWorkspace runs the legacy creation
// under the write lock and, before the lock is released (so before the new
// workspace can be served), installs its resident image from SQL through the
// same validated loader a flag-on start uses. When that validation refuses
// the workspace the creation fails: a workspace a restart would refuse is
// never served.
func (s *SQLiteStore) createResidentWorkspace(ctx context.Context, name, urlKey, region string, seed WorkspaceSeed) (domain.Bootstrap, error) {
	s.lockWorkspaceWrites()
	persisted := false
	data, err := s.createWorkspaceLocked(ctx, name, urlKey, region, seed, &persisted)
	s.mu.Unlock()
	defer s.writeMu.Unlock()
	// The legacy creation keeps a workspace whose metadata committed even
	// when a later membership write fails; so does the bounded one. A
	// creation refused before its write (a taken key) installs nothing.
	if !persisted {
		return data, err
	}
	if publishErr := s.publishCreatedWorkspaceLocked(ctx, urlKey); publishErr != nil && err == nil {
		err = publishErr
	}
	return data, err
}

// publishCreatedWorkspaceLocked installs the resident image of a workspace
// the caller just created. The caller holds the write lock.
func (s *SQLiteStore) publishCreatedWorkspaceLocked(ctx context.Context, workspace string) error {
	// The workspace is committed: a cancelled request must not leave it
	// unserved.
	ctx = context.WithoutCancel(ctx)
	if err := s.reloadResidentWorkspaceLogical(ctx, workspace, true); err != nil {
		slog.Error("bounded metadata: a created workspace could not be installed", "workspace", workspace, "error", err)
		return fmt.Errorf("install created workspace %q: %w", workspace, err)
	}
	s.setLastWorkspaceKey(workspace)
	return nil
}

// WithdrawInstalledWorkspaceForTesting removes workspace's installed logical
// source and stamps while keeping it listed: the state a workspace created
// before createResidentWorkspace was left in, which every bounded read
// refuses with ErrBoundedMetadataCoverage.
func (s *SQLiteStore) WithdrawInstalledWorkspaceForTesting(workspace string) error {
	if !testing.Testing() || s.metadataResidency == nil {
		return ErrBoundedMetadataCoverage
	}
	r := s.metadataResidency
	r.mu.Lock()
	source := r.logical[workspace]
	delete(r.logical, workspace)
	delete(r.stamps, workspace)
	delete(r.applied, workspace)
	r.mu.Unlock()
	if source != nil {
		source.Release()
	}
	return nil
}

// InstalledWorkspaceForTesting reports whether workspace has an installed
// logical source.
func (s *SQLiteStore) InstalledWorkspaceForTesting(workspace string) bool {
	if s.metadataResidency == nil {
		return false
	}
	s.metadataResidency.mu.Lock()
	defer s.metadataResidency.mu.Unlock()
	return s.metadataResidency.logical[workspace] != nil
}

// SourceChargeForTesting is the installed logical sources' share of
// FLOW_METADATA_SOURCE_BYTES.
type SourceChargeForTesting struct {
	Sources  int   // installed sources
	Groups   int   // distinct spool groups their storages belong to
	Storages int   // distinct storages they share
	Files    int   // storages whose spool is a file
	Resident int64 // in-memory bytes of their settled file-less spools
	Charged  int64 // descriptor bytes those groups and storages hold
	Process  int64 // descriptor bytes held in the process (every store, installed or not)
}

// InstalledSourceChargeForTesting reports what the store's installed sources
// hold of FLOW_METADATA_SOURCE_BYTES.
func (s *SQLiteStore) InstalledSourceChargeForTesting() SourceChargeForTesting {
	var out SourceChargeForTesting
	if !testing.Testing() || s.metadataResidency == nil {
		out.Process = metadataLogicalDescriptorsUsed.Load()
		return out
	}
	r := s.metadataResidency
	r.mu.Lock()
	defer r.mu.Unlock()
	storages := map[*metadataLogicalStorage]bool{}
	groups := map[*metadataLogicalSpoolGroup]bool{}
	for _, source := range r.logical {
		if source == nil {
			continue
		}
		out.Sources++
		source.mu.Lock()
		for _, storage := range source.stores {
			if storages[storage] {
				continue
			}
			storages[storage] = true
			storage.mu.Lock()
			out.Charged += storage.locationBytes
			storage.mu.Unlock()
			storage.pendingMu.Lock()
			out.Charged += storage.writeCharge
			out.Resident += storage.resident
			if storage.file != nil {
				out.Files++
			}
			storage.pendingMu.Unlock()
			if storage.group != nil && !groups[storage.group] {
				groups[storage.group] = true
				out.Charged += metadataLogicalDescriptorBytes
			}
		}
		source.mu.Unlock()
	}
	out.Storages, out.Groups = len(storages), len(groups)
	out.Process = metadataLogicalDescriptorsUsed.Load()
	return out
}
