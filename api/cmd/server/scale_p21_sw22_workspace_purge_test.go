package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW22WorkspacePurgeIdentityDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		other, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), f.data.Viewer), "SW22 purge sibling", idgen.New("sw22-purge-sibling-"), "us")
		if err != nil {
			t.Fatal(err)
		}
		f.resources.workspaces = append(f.resources.workspaces, other.Workspace.URLKey)
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "projects"), other.Workspace.URLKey, "scale_p21_sw22.purge_seed", "", nil, func(data *domain.Bootstrap) error {
			data.Projects = append(data.Projects, domain.Project{ID: idgen.New("sw22-purge-project-"), Description: strings.Repeat("unrelated purge body ", 50000)})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		view, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := repo.ScheduleWorkspaceDeletion(t.Context(), f.data.Workspace.URLKey); err != nil {
			t.Fatal(err)
		}
		receipt, started, err := repo.BeginWorkspacePurge(t.Context(), f.data.Workspace.URLKey, time.Now().UTC().Add(time.Minute))
		if err != nil || !started {
			t.Fatal("create owned purge receipt", started, err)
		}
		receipt.WorkspaceKey = other.Workspace.URLKey
		for _, source := range []*store.SQLiteStore{repo, view} {
			// Both identities are ours. A receipt naming a different live
			// workspace must fail before object/credential deletion starts.
			if (&server{store: source}).purgeWorkspace(t.Context(), receipt) {
				t.Fatal("purge accepted a key reused by a different workspace")
			}
			saved, found, err := repo.WorkspaceDeletionReceipt(t.Context(), receipt.WorkspaceID)
			if err != nil || !found || saved.LastError != "catalog: "+store.ErrWorkspaceKeyReused.Error() {
				t.Fatal("catalog mismatch receipt differs", saved.LastError, found, err)
			}
			if !repo.HasWorkspace(f.data.Workspace.URLKey) || !repo.HasWorkspace(other.Workspace.URLKey) {
				t.Fatal("identity check deleted an owned workspace")
			}
			cancelled, cancel := context.WithCancel(t.Context())
			cancel()
			if (&server{store: source}).purgeWorkspace(cancelled, receipt) {
				t.Fatal("cancelled purge succeeded")
			}
		}
		if view.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("purge identity lookup decoded the metadata catalog")
		}
	}
}
