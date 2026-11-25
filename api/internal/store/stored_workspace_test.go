package store

import (
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21SW18StoredWorkspaceResidentBoundary(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "projects"), workspace, "project.updated", "", nil, func(data *domain.Bootstrap) error {
		for range 75 {
			data.Projects = append(data.Projects, domain.Project{ID: idgen.New("sw18-stored-"), Name: "unrelated", Description: strings.Repeat("body ", 4000)})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []*SQLiteStore{f.SQLiteStore, view} {
		source.writeMu.Lock()
		got, found := source.storedWorkspace(workspace)
		source.mu.RLock()
		installed := source.workspaces[workspace]
		source.mu.RUnlock()
		if !found || !reflect.DeepEqual(got, installed) {
			source.writeMu.Unlock()
			t.Fatal("borrow does not return exact installed source")
		}
		if _, found := source.storedWorkspace(workspace + "-missing"); found {
			source.writeMu.Unlock()
			t.Fatal("missing registry key became present")
		}
		source.mu.RLock()
		_, installedEmpty := source.workspaces[""]
		source.mu.RUnlock()
		if _, found := source.storedWorkspace(""); found != installedEmpty {
			source.writeMu.Unlock()
			t.Fatal("empty registry key was silently resolved to a default")
		}
		allocs := testing.AllocsPerRun(50, func() {
			if _, found := source.storedWorkspace(workspace); !found {
				t.Fatal("installed source vanished under writer hold")
			}
		})
		source.writeMu.Unlock()
		if allocs != 0 {
			t.Fatalf("installed-map borrow allocated %.1f objects per call", allocs)
		}
		if source == view {
			value := reflect.ValueOf(got)
			for _, field := range persistedMetadataFields() {
				if metadataResidencyFields()[field.name] == "records" && !value.Field(field.index).IsZero() {
					t.Fatalf("bounded helper exposed nonresident collection %s", field.name)
				}
			}
			if view.WorkspaceMetadataDecodes() != 0 || view.metadataResidency.work.used != 0 ||
				view.metadataResidency.policyBytes > view.metadataResidency.policyLimit {
				t.Fatal("borrow decoded metadata, retained work, or exceeded resident policy cap")
			}
		}
		t.Logf("bounded=%t exact installed borrow allocations/op=%.0f", source.BoundedMetadataEnabled(), allocs)
	}
}
