package store

import (
	"context"
	"errors"
	"testing"

	"flow/api/internal/domain"
)

func TestScaleP21SW33OwnedAuthorityRecreatedKey(t *testing.T) {
	f, warm, original := scaleP21SW27WarmLogicalFixture(t)
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
	next, err := f.CreateWorkspace(ContextWithActor(t.Context(), f.data.Viewer), "SW33 replacement tenant", workspace, "us")
	if err != nil {
		t.Fatal(err)
	}
	if next.Workspace.ID == original.workspaceID {
		t.Fatal("workspace key recreation did not replace physical identity")
	}
	scope := MetadataRecordScope{Fields: []string{"customEmojis"}}
	if _, err := view.ProjectScopedMetadata(bound, workspace, f.data.Viewer.ID, nil, scope); !errors.Is(err, ErrResourceAccessChanged) {
		t.Fatal("earlier tenant source accepted replacement authority", err)
	}
	read.Close()
	if view.metadataResidency.work.used != 0 {
		t.Fatal("rejected authorization retained response work after owner closed")
	}

	// Deliberately forge only the editable payload identity. The old source
	// receipt must still prevent a retained old tenant body becoming a new one.
	warm.Workspace.ID = next.Workspace.ID
	forged, err := captureMetadataLogicalSource(t.Context(), warm, original)
	if err != nil {
		t.Fatal(err)
	}
	view.installMetadataLogicalSource(workspace, forged)
	read, found, err = view.BeginInstalledMetadataRead(t.Context(), workspace)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	defer read.Close()
	bound, err = read.Context(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := view.ProjectScopedMetadata(bound, workspace, f.data.Viewer.ID, nil, scope); !errors.Is(err, ErrResourceAccessChanged) {
		t.Fatal("payload ID forged to replacement tenant bypassed source-origin binding", err)
	}
	if _, err := view.ProjectScopedMetadata(t.Context(), workspace, f.data.Viewer.ID, nil, scope); !errors.Is(err, ErrResourceAccessChanged) {
		t.Fatal("unbound projection trusted forged payload identity over source origin", err)
	}
	read.Close()
	if view.metadataResidency.work.used != 0 {
		t.Fatal("forged payload rejection leaked owned selection admission")
	}
}

func TestScaleP21SW33OwnedAuthorityPostgresNativeIdentity(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	if f.dialect != "postgres" {
		t.Skip("Postgres removes NUL in SQL text arguments; SQLite retains it")
	}
	workspace, canonicalID := f.data.Workspace.URLKey, f.data.Workspace.ID
	nativeID := canonicalID + "\x00"
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "workspace", "customEmojis"), workspace, "integration.adapter_configured", "", nil, func(data *domain.Bootstrap) error {
		data.Workspace.ID = nativeID
		data.CustomEmojis = []domain.CustomEmoji{{ID: "sw33-native-identity", Name: "native\x00emoji"}}
		return nil
	}); err != nil {
		t.Fatal("actual accepted native identity producer", err)
	}
	old, ok, err := f.BootstrapForUser(t.Context(), workspace, f.data.Viewer.ID)
	if err != nil || !ok || old.Workspace.ID != nativeID {
		t.Fatal("literal old authorized source lost accepted native identity", ok, err)
	}
	f.mu.Lock()
	f.forgetCopyRevisionLocked(workspace)
	f.mu.Unlock()
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
	if read.source.Stamp().workspaceID != nativeID || read.source.Stamp().revision != -1 {
		t.Fatal("unknown native source provenance did not exercise dialect identity comparison")
	}
	bound, err := read.Context(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	actual, err := view.ProjectScopedMetadata(bound, workspace, f.data.Viewer.ID, nil, MetadataRecordScope{Fields: []string{"customEmojis"}})
	if err != nil || actual.Workspace.ID != old.Workspace.ID || actual.ViewerRole != old.ViewerRole || len(actual.CustomEmojis) != 1 || actual.CustomEmojis[0].Name != old.CustomEmojis[0].Name {
		t.Fatal("physical-origin binding rejected or canonicalized literal native identity", err)
	}
	if err := read.validateWorkspaceIdentity(context.Background(), canonicalID); err != nil {
		t.Fatal("native identity did not retain canonical SQL tenant authority", err)
	}
	if err := view.validateMetadataLogicalOrigin(t.Context(), metadataLogicalOrigin{
		workspace: workspace, stamp: copyStamp{workspaceID: canonicalID},
	}, nativeID); err != nil {
		t.Fatal("native access spelling lost canonical source tenant authority", err)
	}
	unbound, err := view.ProjectScopedMetadata(t.Context(), workspace, f.data.Viewer.ID, nil, MetadataRecordScope{Fields: []string{"customEmojis"}})
	if err != nil || unbound.Workspace.ID != old.Workspace.ID || unbound.ViewerRole != old.ViewerRole {
		t.Fatal("unbound source identity comparison changed accepted native tenant", err)
	}
}
