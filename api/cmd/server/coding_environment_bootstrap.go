package main

import (
	"context"

	"flow/api/internal/coding"
	"flow/api/internal/domain"
	"flow/api/internal/sandbox"
)

// codingEnvironmentBootstrap answers GET /bootstrap for environment sandboxes
// (coding.Service.SnapshotBootstrap): snapshot builds (track K's
// coding.SnapshotBootstrap with the strategy's upload plan and whether the
// environment has secrets) and debug terminals (coding.DebugBootstrap,
// restored when the sandbox starts from a snapshot).
func (s *server) codingEnvironmentBootstrap(ctx context.Context, grant coding.SandboxGrant) (coding.BootstrapResponse, error) {
	store := s.codingStore()
	environmentID := grant.EnvironmentID
	var snapshot domain.CodingEnvironmentSnapshot
	if grant.SnapshotID != "" {
		var err error
		if snapshot, err = store.CodingSnapshot(ctx, grant.WorkspaceKey, grant.SnapshotID); err != nil {
			return coding.BootstrapResponse{}, err
		}
		if environmentID == "" {
			environmentID = snapshot.EnvironmentID
		}
	}
	if environmentID == "" {
		return coding.BootstrapResponse{}, coding.ErrForbidden
	}
	environment, err := store.CodingEnvironment(ctx, grant.WorkspaceKey, environmentID)
	if err != nil {
		return coding.BootstrapResponse{}, err
	}
	cloneURL := coding.CloneURL(environment.HostName, environment.Repository)
	switch grant.Kind {
	case sandbox.KindSnapshot:
		if snapshot.ID == "" {
			return coding.BootstrapResponse{}, coding.ErrForbidden
		}
		plan := sandbox.BuildPlan{}
		if strategy := s.codingSnapshotStrategy(); strategy != nil {
			// PlanBuild is deterministic (track A), so the plan the snapshot
			// reconciler persisted is recomputed here.
			if plan, err = strategy.PlanBuild(ctx, snapshot.ID, grant.WorkspaceKey); err != nil {
				return coding.BootstrapResponse{}, err
			}
		}
		names, err := store.CodingEnvironmentSecretNames(ctx, grant.WorkspaceKey, environment.ID)
		if err != nil {
			return coding.BootstrapResponse{}, err
		}
		return coding.SnapshotBootstrap(s.coding.config, environment, snapshot, grant.SandboxID, cloneURL, plan, len(names) > 0), nil
	case sandbox.KindDebug:
		restored := snapshot.ID != ""
		if record, err := store.CodingSandbox(ctx, grant.SandboxID); err == nil {
			restored = restored || record.Workspace.ArchiveURL != "" || record.Workspace.VolumeID != ""
		}
		return coding.DebugBootstrap(s.coding.config, environment, grant.SandboxID, cloneURL, restored), nil
	}
	return coding.BootstrapResponse{}, coding.ErrForbidden
}
