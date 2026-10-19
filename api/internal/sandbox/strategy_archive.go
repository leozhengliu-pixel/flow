package sandbox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"flow/api/internal/config"
	"flow/api/internal/objectstore"
)

// ArchiveContentType is the media type of snapshot archives.
const ArchiveContentType = "application/zstd"

// zstdMagic starts every zstd frame; uploads that are not zstd are refused.
var zstdMagic = []byte{0x28, 0xb5, 0x2f, 0xfd}

// ErrArchiveTooLarge reports an upload above FLOW_CODING_ARCHIVE_MAX_BYTES.
var ErrArchiveTooLarge = errors.New("sandbox: snapshot archive exceeds FLOW_CODING_ARCHIVE_MAX_BYTES")

// ErrArchiveInvalid reports an upload that is not a zstd stream.
var ErrArchiveInvalid = errors.New("sandbox: snapshot archive is not zstd")

// ArchivePath is the runtime API path a sandbox PUTs and GETs a snapshot
// archive at (track K serves it).
func ArchivePath(snapshotID string) string {
	return "/internal/coding-runtime/snapshots/" + snapshotID + "/archive"
}

// ArchiveKey is the object key of a snapshot archive:
// <FLOW_CODING_ARCHIVE_PREFIX>-<workspace hash>-<snapshot>.tar.zst. Object
// keys are flat (no '/'): the local storage driver and the signed-file route
// reject slashes, so every coding object key joins its parts with '-' or '_'
// (artifacts: coding-artifact_<session>_<id>.<ext>; logs: LogsKey).
func ArchiveKey(prefix, workspaceKey, snapshotID string) string {
	return FlatObjectKey(prefix, "coding-snapshots", WorkspaceLabel(workspaceKey), snapshotID) + ".tar.zst"
}

// FlatObjectKey joins key parts with '-', turning any '/' in the prefix into
// '-' and using fallback when the prefix is empty.
func FlatObjectKey(prefix, fallback string, parts ...string) string {
	prefix = strings.Trim(strings.ReplaceAll(prefix, "/", "-"), "-")
	if prefix == "" {
		prefix = fallback
	}
	key := prefix
	for _, part := range parts {
		if part != "" {
			key += "-" + part
		}
	}
	return key
}

// archiveSnapshotID recovers the snapshot id from an archive key (snapshot
// ids have no '-', see ValidID).
func archiveSnapshotID(storageRef string) string {
	base := strings.TrimSuffix(path.Base(storageRef), ".tar.zst")
	return base[strings.LastIndex(base, "-")+1:]
}

// ArchiveStrategy stores prepared workspaces as tar.zst archives in Flow's
// object store (FLOW_STORAGE_DRIVER local or s3). It works on every CSI
// driver and on Docker, and is the default.
type ArchiveStrategy struct {
	Config  config.CodingConfig
	Objects objectstore.Store
}

func (a *ArchiveStrategy) Name() string { return StrategyArchive }

// PlanBuild gives the build sandbox an empty workspace and the upload URL.
func (a *ArchiveStrategy) PlanBuild(_ context.Context, snapshotID, workspaceKey string) (BuildPlan, error) {
	if !ValidID(snapshotID) {
		return BuildPlan{}, fmt.Errorf("sandbox: invalid snapshot id %q", snapshotID)
	}
	return BuildPlan{
		UploadURL:  a.Config.RuntimeURL + ArchivePath(snapshotID),
		StorageRef: ArchiveKey(a.Config.Snapshot.ArchivePrefix, workspaceKey, snapshotID),
	}, nil
}

// Store streams an uploaded archive into the object store, enforcing the
// size cap and the zstd magic, and returns its key, size and SHA-256. The
// runtime API's PUT handler (track K) calls it; a failed upload leaves no
// object behind.
func (a *ArchiveStrategy) Store(ctx context.Context, snapshotID, workspaceKey string, body io.Reader) (storageRef string, size int64, sha string, err error) {
	if !ValidID(snapshotID) {
		return "", 0, "", fmt.Errorf("sandbox: invalid snapshot id %q", snapshotID)
	}
	head := make([]byte, len(zstdMagic))
	if _, err := io.ReadFull(body, head); err != nil || !bytes.Equal(head, zstdMagic) {
		return "", 0, "", ErrArchiveInvalid
	}
	limit := a.Config.Snapshot.ArchiveMaxBytes
	if limit <= 0 {
		limit = 10 << 30
	}
	hash := sha256.New()
	counted := &limitedReader{reader: io.MultiReader(bytes.NewReader(head), body), remaining: limit}
	key := ArchiveKey(a.Config.Snapshot.ArchivePrefix, workspaceKey, snapshotID)
	written, err := a.Objects.Put(ctx, key, io.TeeReader(counted, hash), ArchiveContentType)
	if err != nil || counted.exceeded {
		_ = a.Objects.Delete(context.WithoutCancel(ctx), key)
		if counted.exceeded {
			return "", 0, "", ErrArchiveTooLarge
		}
		return "", 0, "", fmt.Errorf("sandbox: store archive: %w", err)
	}
	return key, written, hex.EncodeToString(hash.Sum(nil)), nil
}

// limitedReader fails once more than remaining bytes are read.
type limitedReader struct {
	reader    io.Reader
	remaining int64
	exceeded  bool
}

func (l *limitedReader) Read(p []byte) (int, error) {
	n, err := l.reader.Read(p)
	l.remaining -= int64(n)
	if l.remaining < 0 {
		l.exceeded = true
		return n, ErrArchiveTooLarge
	}
	return n, err
}

// Open reads a stored archive (the restore download, track K's GET handler).
func (a *ArchiveStrategy) Open(ctx context.Context, storageRef string) (io.ReadCloser, int64, error) {
	reader, _, size, err := a.Objects.Open(ctx, storageRef)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: archive %s: %v", ErrNotFound, storageRef, err)
	}
	return reader, size, nil
}

// Finish verifies that the build uploaded its archive and returns its key
// and size. A missing upload is an error (the build failed to archive).
func (a *ArchiveStrategy) Finish(ctx context.Context, snapshotID string, plan BuildPlan) (string, int64, error) {
	key := plan.StorageRef
	if key == "" || archiveSnapshotID(key) != snapshotID {
		return "", 0, fmt.Errorf("sandbox: build plan of %s has no archive key", snapshotID)
	}
	reader, size, err := a.Open(ctx, key)
	if err != nil {
		return "", 0, err
	}
	_ = reader.Close()
	if size <= 0 {
		return "", 0, fmt.Errorf("sandbox: archive %s is empty", key)
	}
	return key, size, nil
}

// Restore makes the new sandbox's restore step download the archive.
func (a *ArchiveStrategy) Restore(_ context.Context, storageRef, _, _ string) (VolumeSource, error) {
	id := archiveSnapshotID(storageRef)
	if !ValidID(id) {
		return VolumeSource{}, fmt.Errorf("sandbox: invalid archive ref %q", storageRef)
	}
	return VolumeSource{ArchiveURL: a.Config.RuntimeURL + ArchivePath(id)}, nil
}

// Delete removes an archive; a missing archive is success.
func (a *ArchiveStrategy) Delete(ctx context.Context, storageRef string) error {
	if storageRef == "" {
		return nil
	}
	return a.Objects.Delete(ctx, storageRef)
}
