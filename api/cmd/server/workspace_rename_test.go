package main

import (
	"encoding/json"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// SW555: a workspace update with bounded metadata on, a rename included, runs
// the literal legacy update on a COLD facade (PATCH /api/workspaces/{key} is a
// COLD route). Renamed flag off and bounded from the same state, the answers
// are identical, the complete metadata under the new key is identical (SQL
// and the bounded image), and the bounded store no longer holds the old key.
// Before, the bounded update wrote the resident skeleton as the workspace's
// metadata (its non-resident records replaced by nothing).
func TestScaleP21SW555WorkspaceRenameColdDifferential(t *testing.T) {
	f, from := scaleP21SW550Fixture(t)
	to := from + "-renamed"
	ctx, scope := store.WithColdScope(t.Context(), "PATCH /api/workspaces/{workspaceKey}")
	defer scope.Close()
	encode := func(value any) string {
		t.Helper()
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	original, ok := f.repository.WorkspaceMetadata(from)
	if !ok || len(original.Users) == 0 {
		t.Fatal("SW555 fixture workspace missing")
	}
	rename := func(repo *store.SQLiteStore, key, next, name string) domain.Bootstrap {
		t.Helper()
		workspace := original.Workspace
		workspace.URLKey, workspace.Name = next, name
		updated, err := repo.UpdateWorkspace(ctx, key, workspace)
		if err != nil {
			t.Fatal("SW555 rename", key, next, err)
		}
		return updated
	}
	records := func(key string) int {
		t.Helper()
		data, ok := f.repository.WorkspaceMetadata(key)
		if !ok {
			return -1
		}
		return len(data.Users) + len(data.Labels) + len(data.Projects) + len(data.Settings) + len(data.Webhooks) + len(data.ProjectStatuses)
	}
	// Flag off.
	legacy := rename(f.repository, from, to, "SW555 renamed")
	// Compare what a running server holds: the image reloaded from SQL.
	if err := f.repository.ReloadWorkspace(t.Context(), to); err != nil {
		t.Fatal(err)
	}
	legacyImage, ok := f.repository.WorkspaceMetadata(to)
	if !ok {
		t.Fatal("SW555 flag-off rename lost the workspace")
	}
	legacyRecords := records(to)
	rename(f.repository, to, from, original.Workspace.Name)
	if err := f.repository.ReloadWorkspace(t.Context(), from); err != nil {
		t.Fatal(err)
	}
	if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), f.repository, from); err != nil {
		t.Fatal(err)
	}
	// Bounded, COLD.
	bounded := rename(f.probe, from, to, "SW555 renamed")
	if got, want := encode(bounded), encode(legacy); got != want {
		t.Fatalf("SW555 the rename answers differ:\n legacy=%s\nbounded=%s", want, got)
	}
	if _, found := f.probe.WorkspaceSettingsMetadata(from); found {
		t.Fatal("SW555 the bounded store still holds the old key")
	}
	boundedImage, ok := f.probe.WorkspaceMetadata(to)
	if !ok || encode(boundedImage) != encode(legacyImage) {
		t.Fatalf("SW555 the bounded image under the new key differs from flag off:\n legacy=%s\nbounded=%s", scaleP21SW555Clip(encode(legacyImage), encode(boundedImage)), scaleP21SW555Clip(encode(boundedImage), encode(legacyImage)))
	}
	if err := f.repository.ReloadWorkspace(t.Context(), to); err != nil {
		t.Fatal(err)
	}
	if got := records(to); got != legacyRecords || legacyRecords == 0 {
		t.Fatalf("SW555 the renamed workspace's stored metadata differs: %d records, flag off %d", got, legacyRecords)
	}
	stored, _ := f.repository.WorkspaceMetadata(to)
	if encode(stored) != encode(legacyImage) {
		t.Fatal("SW555 the stored metadata after the bounded rename differs from flag off")
	}
}

// scaleP21SW555Clip shows text around its first difference from other.
func scaleP21SW555Clip(text, other string) string {
	index := 0
	for index < len(text) && index < len(other) && text[index] == other[index] {
		index++
	}
	return text[max(0, index-200):min(len(text), index+300)]
}
