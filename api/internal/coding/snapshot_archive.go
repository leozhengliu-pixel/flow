package coding

// Archive storage of snapshots (docs/coding-sessions-design.md §10). Owner: track K.
//
// The runner in a snapshot sandbox PUTs `tar --zstd` of /workspace and the
// home directory to /internal/coding-runtime/snapshots/{id}/archive; Flow
// streams it into the object store. Restores GET the same route with the
// restoring sandbox's token. The one archive implementation is track A's
// sandbox.ArchiveStrategy (flat object keys, see sandbox.ArchiveKey); the
// runtime archive routes reach it through ArchiveStore.

import (
	"context"
	"io"

	"flow/api/internal/sandbox"
)

// Archive upload errors (aliases of track A's).
var (
	ErrArchiveTooLarge = sandbox.ErrArchiveTooLarge
	ErrArchiveInvalid  = sandbox.ErrArchiveInvalid
)

// ArchiveStore is the upload/download half of an archive strategy;
// *sandbox.ArchiveStrategy implements it and the runtime archive routes use it.
type ArchiveStore interface {
	Store(ctx context.Context, snapshotID, workspaceKey string, body io.Reader) (storageRef string, size int64, sha string, err error)
	Open(ctx context.Context, storageRef string) (io.ReadCloser, int64, error)
}

var _ ArchiveStore = (*sandbox.ArchiveStrategy)(nil)
