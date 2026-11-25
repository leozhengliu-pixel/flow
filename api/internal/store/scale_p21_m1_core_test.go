package store

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP21M1CoreMetadataDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.seed", "", nil, func(data *domain.Bootstrap) error {
		data.Projects = append(data.Projects, domain.Project{ID: "m1-unrelated", Description: strings.Repeat("unrelated ", 40000)})
		data.Settings["m1-small"] = map[string]any{"name": "Small"}
		data.Trash = append(data.Trash, domain.TrashEntry{ID: "m1-trash", Links: &domain.TrashLinks{}})
		requested := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		data.Workspace.DeletionRequestedAt = &requested
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	probe, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	data, _ := f.WorkspaceMetadata(workspace)
	legacy := &SQLiteStore{db: f.db, dialect: f.dialect, maxStateBytes: f.maxStateBytes, viewer: f.viewer, lastWorkspaceKey: workspace, workspaces: map[string]domain.Bootstrap{workspace: data}}
	f.mu.RLock()
	if revision, ok := f.copyRevisionLocked(workspace); ok {
		legacy.copyRevisions = map[string]copyRevision{workspace: revision}
	}
	f.mu.RUnlock()
	// CI shares the database with other packages. Compare only owned catalogs,
	// keeping each representation's default identity at the same workspace.
	probe.workspaces = map[string]domain.Bootstrap{workspace: probe.workspaces[workspace]}
	probe.lastWorkspaceKey, probe.viewer = workspace, f.viewer
	var before []byte
	if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='projects' AND record_key='m1-unrelated'`, workspace).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []MetadataRecordScope{
		{},
		{Read: []RecordKey{{Kind: "settings", ID: "m1-small"}}},
		{Read: []RecordKey{{Kind: "settings", ID: "m1-missing"}}},
		{Read: []RecordKey{{Kind: "trash", ID: "m1-trash"}}},
	} {
		left, leftErr := legacy.HydrateWorkspaceMetadata(t.Context(), workspace, scope)
		right, rightErr := probe.HydrateWorkspaceMetadata(t.Context(), workspace, scope)
		if leftErr != nil || rightErr != nil || !reflect.DeepEqual(left, right) {
			t.Fatalf("explicit hydration differs: %#v/%#v errors=%v/%v", left, right, leftErr, rightErr)
		}
		if right.RecordLoaded(RecordKey{Kind: "settings", ID: "m1-small"}) && len(right.Data.Settings) > 0 {
			right.Data.Settings["m1-small"].(map[string]any)["name"] = "Caller mutation"
			again, err := probe.HydrateWorkspaceMetadata(t.Context(), workspace, scope)
			if err != nil || again.Data.Settings["m1-small"].(map[string]any)["name"] != "Small" {
				t.Fatal("hydration returned shared mutable storage")
			}
		}
	}
	for _, source := range []*SQLiteStore{legacy, probe} {
		if _, err := source.HydrateWorkspaceMetadata(t.Context(), workspace, MetadataRecordScope{Read: []RecordKey{{Kind: "projects", ID: "m1-unrelated"}}}); !errors.Is(err, ErrMetadataPageBudget) {
			t.Fatalf("oversized hydration did not report its budget: %v", err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := source.HydrateWorkspaceMetadata(ctx, workspace, MetadataRecordScope{}); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled hydration did not stop: %v", err)
		}
	}
	if left, right := legacy.Account(), probe.Account(); !reflect.DeepEqual(left, right) {
		t.Fatalf("account workspace identities/counts differ: %#v/%#v", left, right)
	}
	for _, key := range []string{workspace, "", "m1-missing"} {
		left, leftOK := legacy.WorkspaceSearchViewer(key)
		right, rightOK := probe.WorkspaceSearchViewer(key)
		if leftOK != rightOK || !reflect.DeepEqual(left, right) {
			t.Fatalf("search viewer identity differs for %q", key)
		}
	}
	for _, cutoff := range []time.Time{time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)} {
		if left, right := legacy.WorkspacesDueForDeletion(cutoff), probe.WorkspacesDueForDeletion(cutoff); !reflect.DeepEqual(left, right) {
			t.Fatalf("due workspace identities differ: %#v/%#v", left, right)
		}
	}
	for _, input := range []domain.Bootstrap{
		{},
		{Workspace: domain.Workspace{URLKey: workspace}, Releases: []domain.Release{{ID: "m1-release"}}},
		{Releases: []domain.Release{{ID: "m1-release"}}},
	} {
		left, right := cloneBootstrap(input), cloneBootstrap(input)
		before := legacy.PopulateReleaseProgress(t.Context(), &left)
		after := probe.PopulateReleaseProgress(t.Context(), &right)
		if !reflect.DeepEqual(left, right) || (before == nil) != (after == nil) {
			t.Fatalf("release progress differs: %#v/%#v err=%v/%v", left, right, before, after)
		}
	}
	if probe.WorkspaceMetadataDecodes() != 0 || probe.metadataResidency.work.used != 0 {
		t.Fatal("core fallback decoded complete metadata or retained work")
	}
	var after []byte
	if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='projects' AND record_key='m1-unrelated'`, workspace).Scan(&after); err != nil || !bytes.Equal(before, after) {
		t.Fatal("core/hydration reads changed unrelated canonical bytes")
	}
}

func TestScaleP21M1CoreSeedOwnerDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	probe, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	legacy := &SQLiteStore{db: f.db, dialect: f.dialect, workspaces: map[string]domain.Bootstrap{workspace: f.data}}
	probe.workspaces = map[string]domain.Bootstrap{workspace: probe.workspaces[workspace]}
	for _, source := range []*SQLiteStore{legacy, probe} {
		if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_memberships SET role='admin' WHERE workspace_id=? AND user_id=?`, f.data.Workspace.ID, f.data.Viewer.ID); err != nil {
			t.Fatal(err)
		}
		if err := source.ensureSeedWorkspaceOwners(t.Context()); err != nil {
			t.Fatal(err)
		}
		role, status, err := f.WorkspaceRole(t.Context(), f.data.Workspace.ID, f.data.Viewer.ID)
		if err != nil || role != "owner" || status != "active" {
			t.Fatalf("seed owner SQL effect differs: role=%q status=%q err=%v", role, status, err)
		}
		if err := source.ensureSeedWorkspaceOwners(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if probe.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("owner backfill decoded complete metadata")
	}
}
