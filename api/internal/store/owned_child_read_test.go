package store

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"flow/api/internal/domain"
)

func TestScaleP21OwnedChildReadGeneration(t *testing.T) {
	f, warm, _ := scaleP21SW27WarmLogicalFixture(t)
	workspace := warm.Workspace.URLKey
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	parent, found, err := view.BeginInstalledMetadataRead(t.Context(), workspace)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	defer parent.Close()
	ctx, err := parent.Context(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "settings"), workspace, "scale_p21.child_source", "", nil, func(data *domain.Bootstrap) error {
		data.Settings["sw27Typed"] = "new installed generation"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := view.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	view.mu.Lock()
	view.lastWorkspaceKey = "changed-default"
	view.mu.Unlock()
	child, found, err := view.BeginInstalledMetadataRead(ctx, "")
	if err != nil || !found {
		t.Fatal("child did not inherit physical workspace and generation", found, err)
	}
	defer child.Close()
	parent.Close()
	scope := MetadataRecordScope{Read: []RecordKey{{Kind: "settings", ID: "sw27Typed"}}}
	selected, err := child.Select(t.Context(), scope)
	if err != nil {
		t.Fatal("parent release invalidated independently owned child", err)
	}
	if !reflect.DeepEqual(selected.Data.Settings["sw27Typed"], warm.Settings["sw27Typed"]) {
		t.Fatal("child substituted the current installed generation")
	}
	selected.Close()
	fresh, found, err := view.BeginInstalledMetadataRead(t.Context(), workspace)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	current, err := fresh.Select(t.Context(), scope)
	if err != nil || current.Data.Settings["sw27Typed"] != "new installed generation" {
		t.Fatal("unpinned read inherited an old generation", err)
	}
	current.Close()
	fresh.Close()
	child.Close()
	if view.metadataResidency.work.used != 0 || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("child ownership leaked admission or hydrated complete metadata")
	}
}

func TestScaleP21OwnedChildReadCloseAndBindings(t *testing.T) {
	f, _, _ := scaleP21SW27WarmLogicalFixture(t)
	workspace := f.data.Workspace.URLKey
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	parent, found, err := view.BeginInstalledMetadataRead(t.Context(), workspace)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	defer parent.Close()
	ctx, err := parent.Context(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := view.BeginInstalledMetadataRead(ctx, "foreign-workspace"); !errors.Is(err, ErrMetadataRecordQuery) {
		t.Fatal("child widened its physical binding", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := view.BeginInstalledMetadataRead(canceled, workspace); !errors.Is(err, context.Canceled) {
		t.Fatal("child ignored cancellation", err)
	}
	held := view.metadataResidency.work.used
	_, release, err := view.tryAdmitAdditionalMetadataWork(t.Context(), view.metadataResidency.work.limit-held)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := view.BeginInstalledMetadataRead(ctx, workspace); !errors.Is(err, ErrMetadataWorkBudget) {
		release()
		t.Fatal("child borrowed a parent's descriptor allowance", err)
	}
	release()
	if view.metadataResidency.work.used != held {
		t.Fatal("denied child retained admission")
	}
	var workers sync.WaitGroup
	for range 100 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			child, found, err := view.BeginInstalledMetadataRead(ctx, workspace)
			if errors.Is(err, errMetadataLogicalClosed) {
				return
			}
			if err != nil || !found {
				t.Errorf("concurrent child registration failed: found=%v err=%v", found, err)
				return
			}
			child.Close()
		}()
	}
	parent.Close()
	workers.Wait()
	if view.metadataResidency.work.used != 0 {
		t.Fatal("child registration racing parent close leaked admission")
	}
}
