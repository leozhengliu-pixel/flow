package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP12GatesOwnedResourceCompatibilityWireErrors(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	for _, f := range scaleP12ResourceSnapshotFixtures(t) {
		view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), f.legacy.Workspace.URLKey)
		if err != nil {
			t.Fatal(err)
		}
		defer view.releaseMetadataLogicalSources()
		ctx, release := scaleP12GatesBoundedResourceSnapshot(t, f, "low-level", "projects", f.project)
		defer release()
		access, err := view.ResolveAccess(ctx, f.legacy.Workspace.URLKey, f.member.ID, nil)
		if err != nil {
			t.Fatal(err)
		}
		var project domain.Project
		for _, candidate := range f.legacy.Projects {
			if candidate.ID == f.project {
				project = candidate
			}
		}
		if project.ID == "" {
			t.Fatal("wire error fixture target missing")
		}
		invalid := project
		invalid.CreatedAt = time.Date(10000, time.January, 1, 0, 0, 0, 0, time.UTC)
		captured := resourceReadCompatibilitySnapshot{
			database: f.db, workspace: f.legacy.Workspace.URLKey,
			data: domain.Bootstrap{Workspace: f.legacy.Workspace, Viewer: f.legacy.Viewer, Projects: []domain.Project{invalid}},
		}
		ctx = context.WithValue(ctx, resourceReadCompatibilityKey{}, captured)
		read := &ResourceRead{store: view}
		defer read.Close()
		if err := read.reserve(ctx, 2<<20); err != nil {
			t.Fatal(err)
		}
		data := domain.Bootstrap{Projects: []domain.Project{project}}
		err = read.compatibility(ctx, access.policy, &data)
		var wireErr *json.MarshalerError
		if !errors.As(err, &wireErr) {
			t.Fatalf("typed wire failure became an ordinary mismatch/normalization fallback: %v", err)
		}
		read.Close()
		var one int
		if err := f.db.QueryRowContext(ctx, `SELECT 1`).Scan(&one); err != nil || one != 1 {
			t.Fatalf("wire failure closed caller SQL transaction: %v", err)
		}
		if view.metadataResidency.work.used != 0 {
			t.Fatal("wire failure retained temporary or output admission")
		}
	}
}
