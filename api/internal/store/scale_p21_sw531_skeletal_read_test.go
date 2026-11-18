package store

import (
	"context"
	"slices"
	"testing"
)

// SW531: a legacy whole-workspace read of the live bounded image outside a
// registered COLD facade fails closed on a production store (coverage
// required): WorkspaceMetadata, BootstrapFor and a field-only read of a
// non-resident field report the workspace as missing instead of answering
// from the resident skeleton. A resident field-only read, the registered
// COLD facade and the flag-off store answer as before; a test view keeps
// the skeleton read and reports it to its observer.
func TestScaleP21SW531SkeletalReadFailsClosed(t *testing.T) {
	f, view, workspace := scaleP21ColdReadFixture(t)
	full, found := f.WorkspaceMetadata(workspace)
	if !found || len(full.IntegrationConnections) != 1 {
		t.Fatal("flag-off store lost its complete image", found, len(full.IntegrationConnections))
	}
	var observed []string
	view.SetSkeletalReadObserverForTesting(func(accessor string) { observed = append(observed, accessor) })
	t.Cleanup(func() { view.SetSkeletalReadObserverForTesting(nil) })
	reads := func() (metadata, bootstrap, nonResident, resident bool) {
		_, metadata = view.WorkspaceMetadata(workspace)
		_, bootstrap = view.BootstrapFor(workspace)
		_, nonResident = view.WorkspaceMetadataFields(workspace, "teams", "integrationConnections")
		_, resident = view.WorkspaceMetadataFields(workspace, "teams", "teamSettings", "workspaceSettings")
		return
	}
	// Test view: observed, answered from the skeleton.
	if metadata, bootstrap, nonResident, resident := reads(); !metadata || !bootstrap || !nonResident || !resident {
		t.Fatal("test view changed the skeleton reads", metadata, bootstrap, nonResident, resident)
	}
	want := []string{"WorkspaceMetadata", "BootstrapFor", "WorkspaceMetadataFields(integrationConnections)"}
	if !slices.Equal(observed, want) {
		t.Fatalf("observed %q, want %q", observed, want)
	}
	// Production store: refused.
	view.metadataResidency.requireCoverage = true
	defer func() { view.metadataResidency.requireCoverage = false }()
	observed = nil
	if metadata, bootstrap, nonResident, resident := reads(); metadata || bootstrap || nonResident || !resident {
		t.Fatal("production bounded store answered a skeleton read", metadata, bootstrap, nonResident, resident)
	}
	if !slices.Equal(observed, want) {
		t.Fatalf("observed %q, want %q", observed, want)
	}
	// The registered COLD facade reads the complete image.
	view.metadataResidency.work.limit = metadataWorkBytes
	view.metadataResidency.requireCoverage = false
	if err := view.WithColdMetadataRead(t.Context(), scaleP21SW191Read, func(_ context.Context, repo *SQLiteStore) error {
		data, found := repo.WorkspaceMetadata(workspace)
		if !found || len(data.IntegrationConnections) != 1 {
			t.Fatal("COLD facade lost the complete image", found, len(data.IntegrationConnections))
		}
		if _, found := repo.WorkspaceMetadataFields(workspace, "integrationConnections"); !found {
			t.Fatal("COLD facade refused a field read")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, found := f.WorkspaceMetadataFields(workspace, "integrationConnections"); !found {
		t.Fatal("flag-off store refused a field read")
	}
}
