package sandbox

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"flow/api/internal/config"
	"flow/api/internal/objectstore"
)

// VolumeSnapshotStrategy stores prepared workspaces as CSI VolumeSnapshots
// (design §10): the build sandbox writes to a PVC keyed by the snapshot id,
// Flow snapshots it after the build succeeds, and restores are PVCs with the
// snapshot as dataSource.
type VolumeSnapshotStrategy struct {
	Config   config.CodingConfig
	Provider Provider
}

func (v *VolumeSnapshotStrategy) Name() string { return StrategyVolumeSnapshot }

// snapshotRef is the storage ref of a snapshot: the VolumeSnapshot name.
func snapshotRef(snapshotID string) string { return ObjectName("snap", snapshotID) }

func snapshotIDFromRef(ref string) string { return strings.TrimPrefix(ref, "flow-snap-") }

// PlanBuild creates (idempotently) the build volume and mounts it.
func (v *VolumeSnapshotStrategy) PlanBuild(ctx context.Context, snapshotID, workspaceKey string) (BuildPlan, error) {
	if !ValidID(snapshotID) {
		return BuildPlan{}, fmt.Errorf("sandbox: invalid snapshot id %q", snapshotID)
	}
	volume := VolumeSpec{
		ID: snapshotID, WorkspaceKey: workspaceKey, Size: v.Config.WorkspaceSize, StorageClass: v.Config.Kubernetes.StorageClass,
		Labels: map[string]string{LabelSnapshotID: snapshotID},
	}
	if _, err := v.Provider.EnsureVolume(ctx, volume); err != nil {
		return BuildPlan{}, err
	}
	return BuildPlan{Volume: &volume, Workspace: VolumeSource{VolumeID: snapshotID}, StorageRef: snapshotRef(snapshotID)}, nil
}

// Finish snapshots the build volume. It is called repeatedly until the
// snapshot is readyToUse (ErrPending meanwhile); then it deletes the build
// volume and returns the VolumeSnapshot name and restore size.
func (v *VolumeSnapshotStrategy) Finish(ctx context.Context, snapshotID string, plan BuildPlan) (string, int64, error) {
	volumeID := snapshotID
	if plan.Volume != nil && plan.Volume.ID != "" {
		volumeID = plan.Volume.ID
	}
	status, err := v.Provider.SnapshotStatus(ctx, snapshotID)
	if errors.Is(err, ErrNotFound) {
		status, err = v.Provider.CreateSnapshot(ctx, SnapshotSpec{
			ID: snapshotID, VolumeID: volumeID, Class: v.Config.Snapshot.VolumeSnapshotClass,
			Labels: map[string]string{LabelSnapshotID: snapshotID},
		})
	}
	if err != nil {
		return "", 0, err
	}
	if status.Error != "" {
		return "", 0, fmt.Errorf("sandbox: volume snapshot %s failed: %s", snapshotID, status.Error)
	}
	if !status.Ready {
		return "", 0, fmt.Errorf("sandbox: volume snapshot %s is not ready: %w", snapshotID, ErrPending)
	}
	if err := v.Provider.DeleteVolume(ctx, volumeID); err != nil {
		return "", 0, err
	}
	return snapshotRef(snapshotID), status.SizeBytes, nil
}

// Restore creates the sandbox's workspace PVC (keyed by the sandbox id, so
// StopSandbox deletes it) from the snapshot.
func (v *VolumeSnapshotStrategy) Restore(ctx context.Context, storageRef, sandboxID, workspaceKey string) (VolumeSource, error) {
	snapshotID := snapshotIDFromRef(storageRef)
	if !ValidID(snapshotID) || !ValidID(sandboxID) {
		return VolumeSource{}, fmt.Errorf("sandbox: invalid restore of %q into %q", storageRef, sandboxID)
	}
	_, err := v.Provider.RestoreWorkspace(ctx, RestoreSpec{
		VolumeID: sandboxID, SnapshotID: snapshotID, WorkspaceKey: workspaceKey,
		Size: v.Config.WorkspaceSize, StorageClass: v.Config.Kubernetes.StorageClass,
		Labels: map[string]string{LabelSandboxID: sandboxID},
	})
	if err != nil {
		return VolumeSource{}, err
	}
	return VolumeSource{VolumeID: sandboxID}, nil
}

// Delete removes the VolumeSnapshot; missing is success.
func (v *VolumeSnapshotStrategy) Delete(ctx context.Context, storageRef string) error {
	if storageRef == "" {
		return nil
	}
	return v.Provider.DeleteSnapshot(ctx, snapshotIDFromRef(storageRef))
}

// NewStrategy returns the strategy FLOW_CODING_SNAPSHOT_STRATEGY selects.
func NewStrategy(c config.CodingConfig, provider Provider, objects objectstore.Store) (SnapshotStrategy, error) {
	switch c.Snapshot.Strategy {
	case "", StrategyArchive:
		if objects == nil {
			return nil, fmt.Errorf("sandbox: the archive strategy needs an object store")
		}
		return &ArchiveStrategy{Config: c, Objects: objects}, nil
	case StrategyVolumeSnapshot:
		if !provider.Capabilities().VolumeSnapshots {
			return nil, fmt.Errorf("sandbox: provider %s has no VolumeSnapshot support (use FLOW_CODING_SNAPSHOT_STRATEGY=archive)", provider.Name())
		}
		return &VolumeSnapshotStrategy{Config: c, Provider: provider}, nil
	}
	return nil, fmt.Errorf("sandbox: unknown snapshot strategy %q", c.Snapshot.Strategy)
}
