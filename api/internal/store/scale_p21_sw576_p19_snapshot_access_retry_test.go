package store

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

// SW576: both P19 summary readers (the literal one and the bounded owned
// reader) take up to accessChangedReadAttempts snapshots while the installed
// metadata moves past their SQL revision, answer the post-change snapshot
// exactly as a quiet read does, and answer the bare ErrResourceAccessChanged
// only when every attempt is retired.
func TestScaleP21SW576P19SnapshotRetriesChangedAccessMetadata(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw576-")
	scaleP21SW112Seed(t, f, prefix, "Excluded P19 body.")
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	project := prefix + "project"
	rename := func(name string) {
		if err := f.MutateWorkspace(context.Background(), workspace, "scale_p21.sw576_rename", "", nil, func(data *domain.Bootstrap) error {
			index := slices.IndexFunc(data.Projects, func(item domain.Project) bool { return item.ID == project })
			if index < 0 {
				return fmt.Errorf("project %s missing", project)
			}
			data.Projects[index].Name = name
			return nil
		}); err != nil {
			t.Error(err)
		}
	}
	readers := []struct {
		name string
		// change commits a write the reader's installed source has seen
		// but its SQL snapshot has not.
		change func(string)
		read   func(context.Context) (ScaleP19Snapshot, error)
	}{
		{"literal", rename, func(ctx context.Context) (ScaleP19Snapshot, error) {
			return f.ReadScaleP19Snapshot(ctx, workspace, f.data.Viewer.ID, false)
		}},
		{"bounded", func(name string) {
			// The view installs another request's reload of the write.
			rename(name)
			if err := view.ReloadWorkspace(context.Background(), workspace); err != nil {
				t.Error(err)
			}
		}, func(ctx context.Context) (ScaleP19Snapshot, error) {
			read, err := view.BeginP19SnapshotRead(ctx, workspace, f.data.Viewer.ID, false)
			if err != nil {
				return ScaleP19Snapshot{}, err
			}
			defer read.Close()
			return read.Snapshot, nil
		}},
	}
	for _, reader := range readers {
		t.Run(reader.name, func(t *testing.T) {
			for _, changes := range []int{1, 3, accessChangedReadAttempts - 1, accessChangedReadAttempts} {
				calls, last := 0, ""
				ctx := WithAccessChangedReadHookForTesting(t.Context(), func(attempt int) {
					calls++
					if attempt <= changes {
						last = fmt.Sprintf("%s-%d-%d", reader.name, changes, attempt)
						reader.change(last)
					}
				})
				started := time.Now()
				got, err := reader.read(ctx)
				if changes >= accessChangedReadAttempts {
					if err != ErrResourceAccessChanged || calls != accessChangedReadAttempts {
						t.Fatalf("%d changes: err=%v attempts=%d", changes, err, calls)
					}
					continue
				}
				if err != nil || calls != changes+1 {
					t.Fatalf("%d changes: err=%v attempts=%d", changes, err, calls)
				}
				index := slices.IndexFunc(got.Data.Projects, func(item domain.Project) bool { return item.ID == project })
				if index < 0 || got.Data.Projects[index].Name != last {
					t.Fatalf("%d changes: snapshot lacks the post-change name %q", changes, last)
				}
				quiet, err := reader.read(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				if got.Revision != quiet.Revision || !reflect.DeepEqual(scaleP21SW112Comparable(t, got, started), scaleP21SW112Comparable(t, quiet, started)) {
					t.Fatalf("%d changes: retried snapshot differs from a quiet read (revision %s/%s)", changes, got.Revision, quiet.Revision)
				}
			}
		})
	}
	// A cancelled wait between attempts answers the same bare error.
	ctx, cancel := context.WithCancel(t.Context())
	calls := 0
	_, err = f.ReadScaleP19Snapshot(WithAccessChangedReadHookForTesting(ctx, func(attempt int) {
		calls++
		rename(fmt.Sprintf("cancelled-%d", attempt))
		if attempt == 2 {
			cancel()
		}
	}), workspace, f.data.Viewer.ID, false)
	if err != ErrResourceAccessChanged || calls != 2 {
		t.Fatalf("cancelled retry: err=%v attempts=%d", err, calls)
	}
}
