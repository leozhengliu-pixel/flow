package sandbox

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"flow/api/internal/config"
)

// memObjects is an in-memory objectstore.Store.
type memObjects struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func newMemObjects() *memObjects { return &memObjects{objects: map[string][]byte{}} }

func (m *memObjects) Put(_ context.Context, key string, reader io.Reader, _ string) (int64, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objects[key] = data
	return int64(len(data)), nil
}

func (m *memObjects) Open(_ context.Context, key string) (io.ReadCloser, string, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.objects[key]
	if !ok {
		return nil, "", 0, errors.New("no such key")
	}
	return io.NopCloser(bytes.NewReader(data)), "", int64(len(data)), nil
}

func (m *memObjects) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objects, key)
	return nil
}

func TestArchiveStrategy(t *testing.T) {
	ctx := context.Background()
	objects := newMemObjects()
	c := config.CodingConfig{RuntimeURL: "http://flow:8090", Snapshot: config.CodingSnapshotConfig{Strategy: StrategyArchive, ArchivePrefix: "coding-snapshots", ArchiveMaxBytes: 64}}
	strategy, err := NewStrategy(c, NewFake(nil), objects)
	if err != nil {
		t.Fatal(err)
	}
	archive := strategy.(*ArchiveStrategy)
	plan, err := archive.PlanBuild(ctx, "snap1", "acme")
	if err != nil || plan.UploadURL != "http://flow:8090/internal/coding-runtime/snapshots/snap1/archive" || plan.Volume != nil || plan.Workspace != (VolumeSource{}) {
		t.Fatalf("plan = %+v %v", plan, err)
	}
	if plan.StorageRef != "coding-snapshots-"+WorkspaceLabel("acme")+"-snap1.tar.zst" {
		t.Fatalf("storage ref = %s", plan.StorageRef)
	}
	if _, _, err := archive.Finish(ctx, "snap1", plan); !errors.Is(err, ErrNotFound) {
		t.Fatalf("finish before upload = %v", err)
	}
	if _, _, _, err := archive.Store(ctx, "snap1", "acme", strings.NewReader("not zstd at all")); !errors.Is(err, ErrArchiveInvalid) {
		t.Fatalf("invalid archive = %v", err)
	}
	big := append(append([]byte{}, zstdMagic...), bytes.Repeat([]byte("x"), 100)...)
	if _, _, _, err := archive.Store(ctx, "snap1", "acme", bytes.NewReader(big)); !errors.Is(err, ErrArchiveTooLarge) {
		t.Fatalf("too large = %v", err)
	}
	if len(objects.objects) != 0 {
		t.Fatal("a rejected upload must leave no object")
	}
	body := append(append([]byte{}, zstdMagic...), []byte("payload")...)
	ref, size, sha, err := archive.Store(ctx, "snap1", "acme", bytes.NewReader(body))
	if err != nil || ref != plan.StorageRef || size != int64(len(body)) || len(sha) != 64 {
		t.Fatalf("store = %s %d %s %v", ref, size, sha, err)
	}
	finished, finishedSize, err := archive.Finish(ctx, "snap1", plan)
	if err != nil || finished != ref || finishedSize != size {
		t.Fatalf("finish = %s %d %v", finished, finishedSize, err)
	}
	source, err := archive.Restore(ctx, ref, "sb1", "acme")
	if err != nil || source.ArchiveURL != plan.UploadURL || source.VolumeID != "" {
		t.Fatalf("restore = %+v %v", source, err)
	}
	if err := archive.Delete(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if err := archive.Delete(ctx, ref); err != nil {
		t.Fatalf("delete is idempotent: %v", err)
	}
	if _, err := NewStrategy(c, NewFake(nil), nil); err == nil {
		t.Fatal("archive needs an object store")
	}
}

func TestVolumeSnapshotStrategy(t *testing.T) {
	ctx := context.Background()
	fake := NewFake(nil)
	c := config.CodingConfig{WorkspaceSize: "20Gi", Snapshot: config.CodingSnapshotConfig{Strategy: StrategyVolumeSnapshot, VolumeSnapshotClass: "csi"}}
	strategy, err := NewStrategy(c, fake, nil)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := strategy.PlanBuild(ctx, "snap1", "acme")
	if err != nil || plan.Volume == nil || plan.Workspace.VolumeID != "snap1" || plan.StorageRef != "flow-snap-snap1" {
		t.Fatalf("plan = %+v %v", plan, err)
	}
	if _, err := fake.VolumeStatus(ctx, "snap1"); err != nil {
		t.Fatalf("the build volume must exist before the build sandbox: %v", err)
	}
	ref, _, err := strategy.Finish(ctx, "snap1", plan)
	if err != nil || ref != "flow-snap-snap1" {
		t.Fatalf("finish = %s %v", ref, err)
	}
	if _, err := fake.VolumeStatus(ctx, "snap1"); !errors.Is(err, ErrNotFound) {
		t.Fatal("the build volume is deleted once the snapshot is ready")
	}
	source, err := strategy.Restore(ctx, ref, "sb1", "acme")
	if err != nil || source.VolumeID != "sb1" {
		t.Fatalf("restore = %+v %v", source, err)
	}
	if err := strategy.Delete(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStrategy(c, Disabled{}, nil); err == nil {
		t.Fatal("volumesnapshot needs provider support")
	}
}

// TestObjectKeysAreFlat: the local storage driver and the signed-file route
// reject '/' in object keys, so archive and log keys must be flat even when
// the configured prefix has slashes.
func TestObjectKeysAreFlat(t *testing.T) {
	for _, key := range []string{
		ArchiveKey("coding/snapshots/", "acme", "snap1"),
		ArchiveKey("", "acme", "snap1"),
		LogsKey("acme", "s1"),
		LogsKey("", "s1"),
	} {
		if strings.Contains(key, "/") || strings.Contains(key, "--") {
			t.Fatalf("object key %q is not flat", key)
		}
	}
	if id := archiveSnapshotID(ArchiveKey("coding/snapshots", "acme", "snap1")); id != "snap1" {
		t.Fatalf("snapshot id = %q", id)
	}
}
