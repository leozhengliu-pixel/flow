package store

import (
	"context"
	"reflect"
	"testing"

	"flow/api/internal/domain"
)

func TestScaleP21SW325ColdPreferencesPreserveNextIssueNumber(t *testing.T) {
	for _, mode := range []string{"warm", "sql-reloaded"} {
		t.Run(mode, func(t *testing.T) {
			disk, descriptors, spools := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load(), recordSpoolBytes.Load()
			t.Cleanup(func() {
				if metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors || recordSpoolBytes.Load() != spools {
					t.Error("cold preferences retained source storage after fixture cleanup")
				}
			})
			f, view, workspace := scaleP21ColdPreferenceFixture(t)
			if mode == "sql-reloaded" {
				decodes := f.WorkspaceMetadataDecodes()
				loaded, stamp, err := f.loadWorkspaceStamped(t.Context(), workspace)
				if err != nil {
					t.Fatal("actual SQL reload", err)
				}
				if f.WorkspaceMetadataDecodes() != decodes+1 {
					t.Fatal("reload did not execute the literal SQL metadata decoder")
				}
				f.mu.Lock()
				f.workspaces[workspace] = loaded
				f.setCopyRevisionLocked(workspace, stamp)
				f.mu.Unlock()
			}
			// The runtime counter is omitted from SQL JSON. Seed the same
			// installed value after hydration, then exercise both dispatchers.
			f.writeMu.Lock()
			f.mu.Lock()
			original := f.workspaces[workspace]
			original.NextIssueNumber = 777
			f.setWorkspaceLocked(workspace, original)
			f.mu.Unlock()
			f.writeMu.Unlock()
			if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
				t.Fatal(err)
			}
			saved := scaleP21ColdPreferenceSnapshot(t, f.SQLiteStore, workspace)
			test := scaleP21ColdPreferenceCases(f.data.Viewer.ID)[0]
			ctx := withMutationAggregateID(ContextWithActor(t.Context(), f.data.Viewer), test.aggregate)
			var inputs []int
			callback := func(data *domain.Bootstrap) (string, error) {
				inputs = append(inputs, data.NextIssueNumber)
				test.change(data)
				return test.aggregate, nil
			}
			if err := f.MutateWorkspaceWithAggregate(ctx, workspace, test.event, test.payload, callback); err != nil {
				t.Fatal("literal preferences dispatch", err)
			}
			want, found := f.storedWorkspace(workspace)
			if !found || want.NextIssueNumber != 777 {
				t.Fatal("literal preferences kernel did not preserve installed counter", found, want.NextIssueNumber)
			}
			wantSQL := scaleP21ColdPreferenceSnapshot(t, f.SQLiteStore, workspace)
			scaleP21ColdPreferenceRestore(t, f, workspace, saved, original)
			if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
				t.Fatal(err)
			}
			if err := view.withColdMutation(ctx, workspace, test.event, test.payload, callback); err != nil {
				t.Fatal("cold preferences dispatch", err)
			}
			if !reflect.DeepEqual(inputs, []int{777, 777}) {
				t.Fatal("preferences callback inputs changed", inputs)
			}
			if got := scaleP21ColdPreferenceSnapshot(t, f.SQLiteStore, workspace); !reflect.DeepEqual(got, wantSQL) {
				t.Fatal("cold preferences changed literal SQL postimage")
			}
			if err := view.withColdMetadataRead(ctx, func(_ context.Context, facade *SQLiteStore) error {
				got, found := facade.storedWorkspace(workspace)
				if !found || got.NextIssueNumber != want.NextIssueNumber || got.NextIssueNumber != 777 {
					t.Error("cold publication lost installed counter", found, got.NextIssueNumber, want.NextIssueNumber)
				}
				if !reflect.DeepEqual(got.UserSettings, want.UserSettings) {
					t.Error("cold publication changed literal preferences postimage")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if view.MetadataWorkUsedForTesting() != 0 || len(view.metadataResidency.coldSlot) != 0 {
				t.Fatal("cold preferences retained work or slot")
			}
			if !view.writeMu.TryLock() {
				t.Fatal("cold preferences retained shared write lock")
			}
			view.writeMu.Unlock()
		})
	}
}
