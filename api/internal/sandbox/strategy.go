package sandbox

import "context"

// Snapshot strategies (FLOW_CODING_SNAPSHOT_STRATEGY).
const (
	StrategyArchive        = "archive"
	StrategyVolumeSnapshot = "volumesnapshot"
)

// BuildPlan is how a snapshot-building sandbox is set up.
type BuildPlan struct {
	// Volume is created before the build sandbox (volumesnapshot strategy).
	Volume *VolumeSpec
	// Workspace mounts that volume, or is empty (archive: emptyDir).
	Workspace VolumeSource
	// UploadURL is where the runner PUTs the archive (archive strategy).
	UploadURL string
}

// SnapshotStrategy stores and restores prepared workspaces (track A).
//   - archive: the runner in a snapshot sandbox tars /workspace and the home
//     directory (zstd) and PUTs it to Flow's runtime API, which writes it to the
//     object store (FLOW_STORAGE_DRIVER local or s3); restore is an init
//     container that GETs and unpacks it into a fresh emptyDir or PVC.
//   - volumesnapshot: the build sandbox writes to a PVC; after it succeeds Flow
//     creates a VolumeSnapshot of it; restore is a PVC with that dataSource.
type SnapshotStrategy interface {
	Name() string
	// PlanBuild prepares a build of snapshotID.
	PlanBuild(ctx context.Context, snapshotID, workspaceKey string) (BuildPlan, error)
	// Finish runs after the build sandbox succeeded and returns the storage ref.
	Finish(ctx context.Context, snapshotID string, plan BuildPlan) (storageRef string, sizeBytes int64, err error)
	// Restore returns the workspace source of a new sandbox from storageRef.
	Restore(ctx context.Context, storageRef, sandboxID, workspaceKey string) (VolumeSource, error)
	// Delete removes stored data (idempotent).
	Delete(ctx context.Context, storageRef string) error
}
