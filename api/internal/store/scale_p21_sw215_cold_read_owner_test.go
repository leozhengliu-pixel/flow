package store

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"flow/api/internal/domain"
)

const scaleP21SW215ColdConsumer = "server/loop_settings.go:getLoopConfig"

func TestScaleP21SW215ColdReadOwnerOffIdentity(t *testing.T) {
	repo := &SQLiteStore{}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	read, err := repo.BeginColdMetadataRead(ctx, "unregistered")
	if err != nil || read.Context() != ctx || read.Store() != repo {
		t.Fatal("off owner changed literal context/receiver or validated cold inventory", err)
	}
	read.Close()
	read.Close()
	if err := read.Refresh(ctx, "missing"); err != nil {
		t.Fatal("off refresh added cancellation/work to its literal accessor", err)
	}
	if read.Store() != repo || read.Context() != ctx {
		t.Fatal("off close changed the original store")
	}
}

func TestScaleP21SW215ColdReadOwnerRefreshesAtFreshAccessor(t *testing.T) {
	f, parent, workspace := scaleP21ColdReadFixture(t)
	explicit := workspaceRead{
		database: f.db, workspace: workspace,
		metadata: domain.Bootstrap{Workspace: domain.Workspace{ID: f.data.Workspace.ID, Name: "explicit SQL read snapshot"}},
	}
	ctx := context.WithValue(t.Context(), workspaceReadKey{}, explicit)
	read, err := parent.BeginColdMetadataRead(ctx, scaleP21SW215ColdConsumer)
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	facade := read.Store()
	before, found := facade.WorkspaceMetadata(workspace)
	if !found {
		t.Fatal("first accessor missing")
	}
	beforeCopy := cloneBootstrap(before)
	read.state.mu.Lock()
	old := read.state.sources[workspace]
	read.state.mu.Unlock()
	latest := cloneBootstrap(before)
	latest.Workspace.Name = "same-version installed update"
	latest.Settings["sw215-fresh-read"] = "new generation"
	next, err := captureMetadataLogicalSource(ctx, latest, copyStamp{workspaceID: latest.Workspace.ID, revision: -1})
	if err != nil {
		t.Fatal(err)
	}
	// Use the same atomic source/registry publication boundary as an
	// installed same-version HOT write between two legacy accessors.
	parent.mu.Lock()
	parent.metadataResidency.mu.Lock()
	previous := parent.metadataResidency.logical[workspace]
	parent.metadataResidency.logical[workspace] = next
	parent.workspaces[workspace] = domain.Bootstrap{Workspace: latest.Workspace}
	parent.metadataResidency.mu.Unlock()
	parent.mu.Unlock()
	previous.Release()
	stillPinned, _ := facade.WorkspaceMetadata(workspace)
	if !reflect.DeepEqual(stillPinned, before) {
		t.Fatal("old auth handoff changed before a fresh accessor")
	}
	if err := read.Refresh(read.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	fresh, found := facade.WorkspaceMetadata(workspace)
	if !found || !reflect.DeepEqual(fresh, latest) || !reflect.DeepEqual(before, beforeCopy) {
		t.Fatal("fresh accessor stayed pinned or changed previous output")
	}
	_, inherited, found := facade.workspaceReadSource(read.Context(), workspace)
	if !found || inherited.Workspace.Name != explicit.metadata.Workspace.Name {
		t.Fatal("explicit refresh discarded inherited SQL/read snapshot context")
	}
	if err := old.Retain(); err != nil {
		t.Fatal("refresh released a source still owned by previous output", err)
	}
	old.Release()
	read.Close()
	if err := old.Retain(); !errors.Is(err, errMetadataLogicalClosed) {
		if err == nil {
			old.Release()
		}
		t.Fatal("outer output close retained the retired generation", err)
	}
	if err := read.Refresh(ctx, workspace); !errors.Is(err, errMetadataLogicalClosed) {
		t.Fatal("closed owner refreshed a new lifetime", err)
	}
	if err := parent.RefreshColdMetadataRead(ctx, workspace); !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatal("HOT parent refreshed without a cold owner", err)
	}
}

func TestScaleP21SW215ColdReadOwnerBorrowsUntilOuterOutput(t *testing.T) {
	f, view, workspace := scaleP21ColdReadFixture(t)
	want, found := f.WorkspaceMetadata(workspace)
	if !found {
		t.Fatal("fixture source missing")
	}
	sql := 0
	ctx := WithSQLStatementObserver(t.Context(), func(string) { sql++ })
	read, err := view.BeginColdMetadataRead(ctx, scaleP21SW215ColdConsumer)
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	facade := read.Store()
	if facade == nil || facade == view || facade.BoundedMetadataEnabled() || len(view.metadataResidency.coldSlot) != 1 {
		t.Fatal("owned read did not hold one facade/replica slot")
	}
	got, found := facade.WorkspaceMetadata(workspace)
	if !found || !reflect.DeepEqual(got, want) || sql != 0 {
		t.Fatal("owned capture changed native source or decoded SQL", sql)
	}
	nested, err := facade.BeginColdMetadataRead(read.Context(), scaleP21SW215ColdConsumer)
	if err != nil || nested.Store() != facade || nested.Context() != read.Context() {
		t.Fatal("owned nesting selected another facade, context or slot", err)
	}
	nested.Close()
	if read.Store() != facade || len(view.metadataResidency.coldSlot) != 1 {
		t.Fatal("nested close released outer ownership")
	}
	if err := view.WithColdMetadataRead(read.Context(), scaleP21SW215ColdConsumer, func(ctx context.Context, source *SQLiteStore) error {
		if source != facade || ctx != read.Context() {
			t.Fatal("existing handler bridge did not borrow the owned auth facade")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	read.Close()
	if read.Store() != nil || len(view.metadataResidency.coldSlot) != 0 || len(facade.workspaces) != 0 || facade.coldParent != nil ||
		view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("outer output release retained graph, source, replica slot or HOT admission")
	}
	next, err := view.BeginColdMetadataRead(t.Context(), scaleP21SW215ColdConsumer)
	if err != nil || next.Store() == nil || next.Store() == facade {
		t.Fatal("released owner prevented an independent next request", err)
	}
	next.Close()
}

func TestScaleP21SW215ColdReadOwnerWaitCancellationAndConcurrentClose(t *testing.T) {
	_, view, _ := scaleP21ColdReadFixture(t)
	read, err := view.BeginColdMetadataRead(t.Context(), scaleP21SW215ColdConsumer)
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	waiting, cancel := context.WithCancel(t.Context())
	entered, done := make(chan struct{}), make(chan error, 1)
	go func() {
		close(entered)
		next, err := view.BeginColdMetadataRead(waiting, scaleP21SW215ColdConsumer)
		if next != nil {
			next.Close()
		}
		done <- err
	}()
	<-entered
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) || len(view.metadataResidency.coldSlot) != 1 {
		t.Fatal("queued cancel released or bypassed another request's slot", err)
	}
	var group sync.WaitGroup
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func() { defer group.Done(); read.Close() }()
	}
	group.Wait()
	if read.Store() != nil || len(view.metadataResidency.coldSlot) != 0 {
		t.Fatal("concurrent idempotent close leaked the replica slot")
	}
}

func TestScaleP21SW215ColdReadOwnerCaptureFailureReleasesSlot(t *testing.T) {
	_, view, workspace := scaleP21ColdReadFixture(t)
	view.metadataResidency.mu.Lock()
	source := view.metadataResidency.logical[workspace]
	delete(view.metadataResidency.logical, workspace)
	view.metadataResidency.mu.Unlock()
	defer func() {
		view.metadataResidency.mu.Lock()
		view.metadataResidency.logical[workspace] = source
		view.metadataResidency.mu.Unlock()
	}()
	read, err := view.BeginColdMetadataRead(t.Context(), scaleP21SW215ColdConsumer)
	if read != nil || !errors.Is(err, ErrBoundedMetadataCoverage) || len(view.metadataResidency.coldSlot) != 0 ||
		view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("partial installed capture leaked its graph/replica slot", err)
	}
}
