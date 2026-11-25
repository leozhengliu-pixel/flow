package store

import (
	"context"
	"slices"
	"testing"
)

// SW531: a legacy whole-workspace read of a bounded store never answers from
// the resident skeleton. WorkspaceMetadata, BootstrapFor and a field-only
// read of a non-resident field answer the complete installed image from a
// legacy view of the installed logical source (default COLD routing,
// scale_p21_default_cold.go), on a test view and on a production store
// (coverage required) alike, and report the read to the observer (SW530 and
// SW550 fail a proven-bounded route that makes one). A resident field-only
// read, the registered COLD facade and the flag-off store answer as before.
func TestScaleP21SW531LegacyReadsAnswerTheInstalledImage(t *testing.T) {
	f, view, workspace := scaleP21ColdReadFixture(t)
	full, found := f.WorkspaceMetadata(workspace)
	if !found || len(full.IntegrationConnections) != 1 {
		t.Fatal("flag-off store lost its complete image", found, len(full.IntegrationConnections))
	}
	var observed []string
	view.SetSkeletalReadObserverForTesting(func(accessor string) { observed = append(observed, accessor) })
	t.Cleanup(func() { view.SetSkeletalReadObserverForTesting(nil) })
	reads := func() {
		t.Helper()
		metadata, found := view.WorkspaceMetadata(workspace)
		if !found || len(metadata.IntegrationConnections) != 1 || metadata.IntegrationConnections[0].ID != full.IntegrationConnections[0].ID {
			t.Fatal("WorkspaceMetadata did not answer the installed image", found, len(metadata.IntegrationConnections))
		}
		bootstrap, found := view.BootstrapFor(workspace)
		if !found || len(bootstrap.IntegrationConnections) != 1 || bootstrap.Settings["cold-typed"] == nil {
			t.Fatal("BootstrapFor did not answer the installed image", found)
		}
		fields, found := view.WorkspaceMetadataFields(workspace, "teams", "integrationConnections")
		if !found || len(fields.IntegrationConnections) != 1 || len(fields.Teams) != len(full.Teams) || fields.Settings != nil {
			t.Fatal("a non-resident field read did not answer exactly its fields", found, len(fields.IntegrationConnections), len(fields.Teams))
		}
		resident, found := view.WorkspaceMetadataFields(workspace, "teams", "teamSettings", "workspaceSettings")
		if !found || len(resident.Teams) != len(full.Teams) {
			t.Fatal("a resident field read changed", found)
		}
		if _, found := view.WorkspaceMetadata("sw531-missing"); found {
			t.Fatal("a missing workspace was found")
		}
	}
	want := []string{"WorkspaceMetadata", "BootstrapFor", "WorkspaceMetadataFields(integrationConnections)"}
	// Test view.
	reads()
	if !slices.Equal(observed, want) {
		t.Fatalf("observed %q, want %q", observed, want)
	}
	// Production store: the same answers.
	view.metadataResidency.requireCoverage = true
	defer func() { view.metadataResidency.requireCoverage = false }()
	observed = nil
	reads()
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
