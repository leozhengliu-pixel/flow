package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func TestScaleP21SW32OwnedReadRetainsGenerationAndWork(t *testing.T) {
	f, warm, _ := scaleP21SW27WarmLogicalFixture(t)
	workspace := f.data.Workspace.URLKey
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(view.releaseMetadataLogicalSources)
	parent, releaseParent, err := view.admitMetadataWork(t.Context(), 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	read, found, err := view.BeginInstalledMetadataRead(parent, "")
	if err != nil || !found {
		t.Fatal(found, err)
	}
	defer read.Close()
	bound, err := read.Context(parent)
	if err != nil {
		t.Fatal(err)
	}
	releaseParent()
	if view.metadataResidency.work.used == 0 {
		t.Fatal("parent release uncharged the independent source handle")
	}
	if _, err := view.ResolveAccess(bound, workspace, f.data.Viewer.ID, nil); err != nil {
		t.Fatal("small source-handle allowance blocked independently admitted live authority", err)
	}
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "settings"), workspace, "integration.adapter_configured", "", nil, func(data *domain.Bootstrap) error {
		data.Settings["sw27Typed"] = "newer"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := view.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	view.mu.Lock()
	view.lastWorkspaceKey = "changed-default-missing"
	view.mu.Unlock()
	scope := MetadataRecordScope{Read: []RecordKey{{Kind: "settings", ID: "sw27Typed"}}}
	actual, found, err := view.WorkspaceMetadataScoped(bound, "", scope)
	if err != nil || !found || !reflect.DeepEqual(actual.Settings["sw27Typed"], warm.Settings["sw27Typed"]) {
		t.Fatal("request handoff followed current generation or a changed default", found, err)
	}
	if view.metadataResidency.work.used < metadataOwnedSelectionWork {
		t.Fatal("by-value adapter released selected response work before its owner")
	}
	read.Close()
	if view.metadataResidency.work.used != 0 {
		t.Fatal("request close leaked response work")
	}
	if _, _, err := view.WorkspaceMetadataScoped(bound, "", scope); !errors.Is(err, errMetadataLogicalClosed) {
		t.Fatal("expired request token fell back to a still-live current source", err)
	}
}

func TestScaleP21SW32OwnedSelectionCloseAndBindings(t *testing.T) {
	f, _, _ := scaleP21SW27WarmLogicalFixture(t)
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), f.data.Workspace.URLKey)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(view.releaseMetadataLogicalSources)
	read, found, err := view.BeginInstalledMetadataRead(t.Context(), f.data.Workspace.URLKey)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	defer read.Close()
	before := view.metadataResidency.work.used
	selection, err := read.Select(t.Context(), MetadataRecordScope{Fields: []string{"integrationConnections"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(selection.Data.IntegrationConnections) != 1 || view.metadataResidency.work.used != before+metadataOwnedSelectionWork {
		t.Fatal("owned selection was not admitted through processing")
	}
	selection.Close()
	selection.Close()
	if selection.Data.IntegrationConnections != nil || view.metadataResidency.work.used != before || len(read.selections) != 0 {
		t.Fatal("closing one selection leaked work or left unbounded closed handles")
	}
	bound, err := read.Context(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := view.WorkspaceMetadataScoped(bound, "other-workspace", MetadataRecordScope{}); !errors.Is(err, ErrMetadataRecordQuery) {
		t.Fatal("bound source token crossed workspace identity", err)
	}
	other := &SQLiteStore{db: &sqlDatabase{}, metadataResidency: newMetadataResidency()}
	if _, _, err := other.WorkspaceMetadataScoped(bound, f.data.Workspace.URLKey, MetadataRecordScope{}); !errors.Is(err, ErrMetadataRecordQuery) {
		t.Fatal("bound source token crossed store identity", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if selection, err := read.Select(cancelled, MetadataRecordScope{}); selection != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled read returned a retained selection", err)
	}
	read.Close()
	if view.metadataResidency.work.used != 0 {
		t.Fatal("binding tests leaked owned work")
	}
}

func TestScaleP21SW32OwnedProjectionRejectsRecreatedWorkspaceAuthority(t *testing.T) {
	f, _, _ := scaleP21SW27WarmLogicalFixture(t)
	workspace := f.data.Workspace.URLKey
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(view.releaseMetadataLogicalSources)
	read, found, err := view.BeginInstalledMetadataRead(t.Context(), workspace)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	defer read.Close()
	bound, err := read.Context(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.DeleteWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	next, err := f.CreateWorkspace(ContextWithActor(t.Context(), f.data.Viewer), "Replacement owned tenant", workspace, "us")
	if err != nil {
		t.Fatal(err)
	}
	if next.Workspace.ID == f.data.Workspace.ID {
		t.Fatal("fixture did not replace workspace identity")
	}
	if _, err := view.ProjectScopedMetadata(bound, workspace, f.data.Viewer.ID, nil, MetadataRecordScope{Fields: []string{"customEmojis"}}); !errors.Is(err, ErrResourceAccessChanged) {
		t.Fatal("retained old tenant was projected under replacement tenant authority", err)
	}
}

func TestScaleP21SW32WritesAndRecoveryIgnoreReadPins(t *testing.T) {
	f, warm, _ := scaleP21SW27WarmLogicalFixture(t)
	workspace := f.data.Workspace.URLKey
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(view.releaseMetadataLogicalSources)
	read, found, err := view.BeginInstalledMetadataRead(t.Context(), workspace)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	defer read.Close()
	bound, err := read.Context(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	set := func(ctx context.Context, value string) {
		t.Helper()
		if err := f.MutateWorkspace(WithMetadataFields(ctx, "settings"), workspace, "integration.adapter_configured", "", nil, func(data *domain.Bootstrap) error {
			data.Settings["sw27Typed"] = value
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	set(t.Context(), "current installed")
	if err := view.RefreshInstalledMetadataViewForTesting(bound, f.SQLiteStore, workspace); err != nil {
		t.Fatal("refresh borrowed an old read generation", err)
	}
	scope := MetadataRecordScope{Write: []RecordKey{{Kind: "settings", ID: "sw27Typed"}}}
	if err := view.MutateWorkspaceScoped(bound, workspace, "integration.adapter_configured", "", nil, scope, func(data *domain.Bootstrap) error {
		if data.Settings["sw27Typed"] != "current installed" {
			return errors.New("mutation read its caller's pinned earlier source")
		}
		data.Settings["sw27Typed"] = "writer published"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	pinned, err := read.Select(t.Context(), MetadataRecordScope{Read: scope.Write})
	if err != nil || !reflect.DeepEqual(pinned.Data.Settings["sw27Typed"], warm.Settings["sw27Typed"]) {
		t.Fatal("mutation changed the immutable earlier read", err)
	}
	pinned.Close()
	read.Close()
	// An expired token must fail reads, but may not inhibit a write or
	// recovery operation, whose original contract never consumed that pin.
	if err := view.MutateWorkspaceScoped(bound, workspace, "integration.adapter_configured", "", nil, scope, func(data *domain.Bootstrap) error {
		if data.Settings["sw27Typed"] != "writer published" {
			return errors.New("expired read context did not use current writer source")
		}
		data.Settings["sw27Typed"] = "after owner close"
		return nil
	}); err != nil {
		t.Fatal("expired token blocked fresh write", err)
	}
	if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	set(t.Context(), "SQL recovery")
	if err := view.ReloadWorkspace(bound, workspace); err != nil {
		t.Fatal("reload consumed expired read token", err)
	}
	if err := view.loadResidentWorkspaces(bound); err != nil {
		t.Fatal("whole recovery consumed expired read token", err)
	}
	actual, ok, err := view.WorkspaceMetadataScoped(t.Context(), workspace, MetadataRecordScope{Read: scope.Write})
	if err != nil || !ok || actual.Settings["sw27Typed"] != "SQL recovery" {
		t.Fatal("recovery did not install latest SQL generation", ok, err)
	}
	scratch := view.metadataLogicalScratchContext(bound, workspace)
	config, _ := scratch.Value(metadataLogicalScratchKey{}).(metadataLogicalScratch)
	if config.directory == "" {
		t.Fatal("scratch selection followed expired read instead of current installed source")
	}
	if _, handled, err := installedMetadataReadFromContext(scratch, view, workspace); handled || err != nil {
		t.Fatal("scratch context leaked its caller's old read pin", handled, err)
	}
	if view.metadataResidency.work.used != 0 {
		t.Fatal("write/recovery bypass leaked independent read work")
	}
}

func TestScaleP21SW32OwnedSelectionBudgetErrors(t *testing.T) {
	f, warm, stamp := scaleP21SW27WarmLogicalFixture(t)
	workspace := f.data.Workspace.URLKey
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(view.releaseMetadataLogicalSources)
	warm.Settings["sw32Oversized"] = strings.Repeat("x", 2<<20)
	source, err := captureMetadataLogicalSource(t.Context(), warm, stamp)
	if err != nil {
		t.Fatal(err)
	}
	view.installMetadataLogicalSource(workspace, source)
	read, found, err := view.BeginInstalledMetadataRead(t.Context(), workspace)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	defer read.Close()
	before := view.metadataResidency.work.used
	scope := MetadataRecordScope{Read: []RecordKey{{Kind: "settings", ID: "sw32Oversized"}}}
	if selection, err := read.Select(t.Context(), scope); selection != nil || !errors.Is(err, ErrMetadataPageBudget) {
		t.Fatal("intrinsic selected-data cap changed adapter error contract", err)
	}
	if view.metadataResidency.work.used != before || len(read.selections) != 0 {
		t.Fatal("rejected intrinsic selection retained work or response")
	}
	_, release, err := view.tryAdmitAdditionalMetadataWork(t.Context(), view.metadataResidency.work.limit-before)
	if err != nil {
		t.Fatal(err)
	}
	if selection, err := read.Select(t.Context(), MetadataRecordScope{}); selection != nil || !errors.Is(err, ErrMetadataWorkBudget) {
		release()
		t.Fatal("process admission exhaustion became intrinsic page-budget error", err)
	}
	release()
	read.Close()
	if view.metadataResidency.work.used != 0 {
		t.Fatal("budget error paths leaked independent work")
	}
}
