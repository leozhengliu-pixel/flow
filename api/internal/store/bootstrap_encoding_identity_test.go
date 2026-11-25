package store

import (
	"testing"

	"flow/api/internal/domain"
)

func TestBootstrapEncodingGenerationsTrackFields(t *testing.T) {
	_, view, workspace := coldIncrementalFixture(t)
	generation := func() [len(bootstrapEncodingFields)]IssueBootstrapGeneration {
		t.Helper()
		source, err := view.retainMetadataLogicalSource(t.Context(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		defer source.Release()
		image, ok, err := view.metadataResidency.residentImage(t.Context(), workspace, source)
		if err != nil || !ok {
			t.Fatalf("failed to build resident image: %v", err)
		}
		read := &IssueBootstrapRead{resident: true, owner: &ResourceRead{source: source}}
		read.setEncodingGenerations(image)
		for _, name := range bootstrapEncodingFields {
			if _, ok := read.EncodingGeneration(name); !ok {
				t.Fatalf("%s has no encoding generation", name)
			}
		}
		return read.encodingGenerations
	}
	before := generation()
	const id = "ci-project-05"
	scope := MetadataRecordScope{Write: []RecordKey{{Kind: "projects", ID: id}}}
	if err := view.MutateWorkspaceScoped(WithScopedSelectionWrite(t.Context()), workspace, "project.updated", id, nil, scope, func(data *domain.Bootstrap) error {
		data.Projects[0].Description = "Changed encoding"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	after := generation()
	for i, name := range bootstrapEncodingFields {
		if (before[i] != after[i]) != (name == "projects") {
			t.Errorf("unexpected encoding generation change for %s: %d to %d", name, before[i], after[i])
		}
	}
}
