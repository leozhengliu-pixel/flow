package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func scaleP21ResourceOutputContext(t *testing.T) context.Context {
	t.Helper()
	return WithMetadataOutputOwner(t.Context(), func(close func()) bool {
		t.Cleanup(close)
		return true
	})
}

func TestScaleP21ResourceReadMetadataAdapter(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	project, initiative := idgen.New("resource-adapter-project-"), idgen.New("resource-adapter-initiative-")
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "projects", "initiatives"), workspace, "scale_p21.resource_adapter", "", nil, func(data *domain.Bootstrap) error {
		data.Projects = append(data.Projects, domain.Project{ID: project, SlugID: project + "-new", PreviousSlugIDs: []string{project + "-old"}, TeamIDs: []string{data.Teams[0].ID}})
		data.Initiatives = append(data.Initiatives, domain.Initiative{ID: initiative, ProjectIDs: []string{project}, Creator: data.Viewer})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []*domain.APIKey{nil,
		{ID: "unrestricted", Scopes: []string{"read"}},
		{ID: "empty", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{}},
		{ID: "selected", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{f.data.Teams[0].ID}},
	} {
		for _, target := range []struct{ kind, ref string }{
			{"projects", project}, {"projects", project + "-old"},
			{"projects", "missing"}, {"initiatives", initiative},
			{"initiatives", "missing"},
		} {
			oldAccess, err := f.ResolveAccess(t.Context(), workspace, f.data.Viewer.ID, key)
			if err != nil {
				t.Fatal(err)
			}
			want, wantAllowed, wantErr := f.ResourceReadMetadata(t.Context(), oldAccess, target.kind, target.ref)
			access, err := view.ResolveAccess(t.Context(), workspace, f.data.Viewer.ID, key)
			if err != nil {
				t.Fatal(err)
			}
			var closes []func()
			ctx := WithMetadataOutputOwner(t.Context(), func(close func()) bool {
				closes = append(closes, close)
				return true
			})
			actual, allowed, err := view.ResourceReadMetadata(ctx, access, target.kind, target.ref)
			before, beforeErr := json.Marshal(want)
			after, afterErr := json.Marshal(actual)
			if !errors.Is(err, wantErr) || allowed != wantAllowed || beforeErr != nil || afterErr != nil || !bytes.Equal(before, after) {
				t.Fatalf("adapter differs for %s/%s: %v/%v allowed=%v/%v\n%s\n%s", target.kind, target.ref, err, wantErr, allowed, wantAllowed, before, after)
			}
			if len(closes) != 1 || view.metadataResidency.work.used == 0 {
				t.Fatal("adapter released output before its containing operation")
			}
			for _, close := range closes {
				close()
			}
			if view.metadataResidency.work.used != 0 {
				t.Fatal("adapter leaked output admission")
			}
		}
	}
	access, err := view.ResolveAccess(t.Context(), workspace, f.data.Viewer.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithMetadataOutputOwner(t.Context(), nil)
	if _, _, err := view.ResourceReadMetadata(ctx, access, "projects", project); !errors.Is(err, ErrBoundedMetadataCoverage) ||
		!errors.Is(metadataOutputError(ctx), ErrBoundedMetadataCoverage) || view.metadataResidency.work.used != 0 {
		t.Fatal("unowned output escaped admission or its failure latch", err)
	}
	if view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("adapter hydrated the complete workspace")
	}
}
