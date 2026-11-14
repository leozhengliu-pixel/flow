package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP21SW20InstalledViewerOwnershipAndAdmission(t *testing.T) {
	stamp := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	viewer := domain.User{ID: "owner\x00", Name: "name\x00", DisplayName: "display\x00", AppScopes: []string{"read\x00"}, AppTeamIDs: []string{"team\x00"}, OutOfOfficeUntil: &stamp, Via: &domain.ActorBot{ID: "discarded-runtime"}}
	repo := &SQLiteStore{workspaces: map[string]domain.Bootstrap{"workspace": {Viewer: viewer, Projects: []domain.Project{{Description: strings.Repeat("unused", 100000)}}}}, lastWorkspaceKey: "workspace", metadataResidency: newMetadataResidency()}
	want := cloneBootstrap(domain.Bootstrap{Viewer: viewer}).Viewer
	parent, releaseParent, err := repo.admitMetadataWork(t.Context(), 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	read, found, err := repo.BeginInstalledViewerRead(parent, "")
	if err != nil || !found || !reflect.DeepEqual(read.Viewer(), want) {
		t.Fatal("installed viewer source changed", found, err)
	}
	owned := repo.metadataResidency.work.used - 8<<20
	if owned <= 0 || owned >= 1<<20 {
		t.Fatal("result reused parent or cloned unrelated data", owned)
	}
	releaseParent()
	if repo.metadataResidency.work.used != owned {
		t.Fatal("parent release uncharged owned viewer")
	}
	got := read.Viewer()
	got.AppScopes[0], got.AppTeamIDs[0] = "changed", "changed"
	*got.OutOfOfficeUntil = stamp.Add(time.Hour)
	if viewer.AppScopes[0] != "read\x00" || viewer.AppTeamIDs[0] != "team\x00" || !stamp.Equal(time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("caller mutated installed viewer")
	}
	read.Close()
	read.Close()
	if repo.metadataResidency.work.used != 0 {
		t.Fatal("viewer handle allowance leaked")
	}
	for _, key := range []string{"missing", "workspace\x00", "WORKSPACE"} {
		if read, found, err := repo.BeginInstalledViewerRead(t.Context(), key); read != nil || found || err != nil {
			t.Fatal("exact missing installed workspace changed", key, found, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if read, _, err := repo.BeginInstalledViewerRead(ctx, "workspace"); read != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled viewer admission", err)
	}
	if read, _, err := repo.BeginInstalledViewerRead(parent, "workspace"); read != nil || !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("released parent revived", err)
	}
	saturated, releaseSaturated, err := repo.admitMetadataWork(t.Context(), repo.metadataResidency.work.limit)
	if err != nil {
		t.Fatal(err)
	}
	if read, _, err := repo.BeginInstalledViewerRead(saturated, "workspace"); read != nil || !errors.Is(err, ErrMetadataWorkBudget) || repo.metadataResidency.work.waiting != 0 {
		t.Fatal("nested viewer acquisition waited behind parent", err)
	}
	if read, _, err := repo.BeginInstalledViewerRead(context.WithoutCancel(ctx), "workspace"); read != nil || !errors.Is(err, ErrMetadataWorkBudget) || repo.metadataResidency.work.waiting != 0 {
		t.Fatal("context-insensitive viewer read queued without capacity", err)
	}
	releaseSaturated()
	if repo.metadataResidency.work.used != 0 || repo.metadataResidency.work.waiting != 0 {
		t.Fatal("failed viewer acquisitions leaked admission")
	}
}

func TestScaleP21SW20InstalledViewerSourceLifecycle(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	expected := f.data.Viewer
	expected.ID += "\x00"
	expected.Name, expected.DisplayName = "name\x00", "display\x00"
	expected.AppScopes, expected.AppTeamIDs = []string{"read\x00"}, []string{"team\x00"}
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "viewer"), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
		data.Viewer = expected
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	compare := func(phase string) {
		t.Helper()
		legacy, ok := f.WorkspaceMetadata(workspace)
		read, found, err := f.BeginInstalledViewerRead(t.Context(), workspace)
		if err != nil || !ok || !found || !reflect.DeepEqual(legacy.Viewer, read.Viewer()) {
			t.Fatal(phase, "exact installed source differs", found, err)
		}
		defer read.Close()
		// Exercise the same selector with a policy-only representation at
		// this exact installed logical source, not a canonical reload.
		bounded := &SQLiteStore{workspaces: map[string]domain.Bootstrap{workspace: {Viewer: legacy.Viewer}}, metadataResidency: newMetadataResidency()}
		projected, found, err := bounded.BeginInstalledViewerRead(t.Context(), workspace)
		if err != nil || !found || !reflect.DeepEqual(legacy.Viewer, projected.Viewer()) {
			t.Fatal(phase, "resident source differs", found, err)
		}
		projected.Close()
	}
	compare("ordinary-viewer-delta")
	input := f.data.Workspace
	input.Name += " actual full write"
	if _, err := f.UpdateWorkspace(t.Context(), workspace, input); err != nil {
		t.Fatal(err)
	}
	compare("warm-after-full-write")
	scoped, ok, err := f.WorkspaceMetadataScoped(t.Context(), workspace, MetadataRecordScope{})
	if err != nil || !ok || !reflect.DeepEqual(scoped.Viewer, expected) {
		t.Fatal("off-mode oracle changed", err)
	}
	// A true cold load is separately compared against its own installed
	// source. It must not be used to normalize away the warm comparison.
	cold, _, err := f.loadWorkspaceStamped(t.Context(), workspace)
	if err != nil || reflect.DeepEqual(cold.Viewer, expected) {
		t.Fatal("full-write warm/cold fixture lost its difference", err)
	}
	f.mu.Lock()
	f.workspaces[workspace] = cold
	f.mu.Unlock()
	compare("actual-cold-loaded-source")
}

func TestScaleP21SW20InstalledViewerLargeAndBudget(t *testing.T) {
	viewer := domain.User{ID: "owner", Name: strings.Repeat("plain name ", 60000)}
	repo := &SQLiteStore{workspaces: map[string]domain.Bootstrap{"workspace": {Viewer: viewer}}, metadataResidency: newMetadataResidency()}
	read, found, err := repo.BeginInstalledViewerRead(t.Context(), "workspace")
	if err != nil || !found || read.Viewer().Name != viewer.Name {
		t.Fatal("legal large installed viewer clone failed", found, err)
	}
	read.Close()
	repo.workspaces["workspace"] = domain.Bootstrap{Viewer: domain.User{Name: strings.Repeat("x", scaleP21SW20ViewerMaxWork/33+1)}}
	if read, found, err := repo.BeginInstalledViewerRead(t.Context(), "workspace"); read != nil || !found || !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("selected viewer bypassed clone bound", found, err)
	}
	if repo.metadataResidency.work.used != 0 {
		t.Fatal("large viewer rejection leaked admission")
	}
}
