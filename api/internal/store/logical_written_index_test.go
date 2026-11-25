package store

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"flow/api/internal/domain"
)

// The child indexes builders store for the collections they write (and copy)
// hold exactly the headers a walk reads from the spool, across COLD, HOT and
// peer-reload generations.
func TestLogicalWrittenChildIndexesMatchTheSpool(t *testing.T) {
	f, view, workspace := coldDecodeFixture(t)
	written := metadataLogicalWrittenIndexes.Load()
	verify := func(label string) {
		t.Helper()
		source := coldDecodeInstalled(t, view, workspace)
		defer source.Release()
		checked := 0
		for _, storage := range source.stores {
			count, err := storage.verifyChildIndexesForTesting(t.Context())
			if err != nil {
				t.Fatalf("%s: %v", label, err)
			}
			checked += count
		}
		// A walk with the stored indexes reads what a walk without them reads.
		index, _, _ := metadataLogicalField("projects")
		node := source.fields[index]
		var withIndex []metadataLogicalChildHeader
		if err := node.Children(t.Context(), func(_ reflect.Value, child metadataLogicalNode) error {
			withIndex = append(withIndex, metadataLogicalHeaderOf(child))
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		c := metadataLogicalCursor{node, node.offset + 25, t.Context()}
		for i, want := range withIndex {
			child, err := c.child(node.typ.Elem())
			if err != nil || metadataLogicalHeaderOf(child) != want {
				t.Fatalf("%s: project %d header differs from the spool (%v)", label, i, err)
			}
		}
		t.Logf("%s: %d stored child indexes verified, %d projects", label, checked, len(withIndex))
	}
	verify("initial")
	for step := range 6 {
		coldIncrementalRun(t, view, workspace, "project.updated", func(data *domain.Bootstrap) error {
			data.Projects[step*11].Description = fmt.Sprintf("indexed %d", step)
			return nil
		})
		verify(fmt.Sprintf("COLD write %d", step))
		var closes []func()
		output := WithMetadataOutputOwner(t.Context(), func(close func()) bool {
			closes = append(closes, close)
			return true
		})
		if err := view.MutateWorkflowRun(output, workspace, "ci-flow-1", "", "", nil, func(data *domain.Bootstrap) error {
			data.WorkflowRuns = append(data.WorkflowRuns, domain.WorkflowRun{ID: fmt.Sprintf("index-run-%d", step), WorkflowID: "ci-flow-1", Trigger: "manual", Status: "succeeded", Attempt: 1})
			return nil
		}); err != nil && !errors.Is(err, ErrNoMutation) {
			t.Fatal(err)
		}
		for _, close := range closes {
			close()
		}
		verify(fmt.Sprintf("HOT write %d", step))
		if err := f.MutateWorkspace(t.Context(), workspace, "project.updated", "", nil, func(data *domain.Bootstrap) error {
			data.Projects[step].Name = fmt.Sprintf("peer %d", step)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := view.ReloadWorkspace(t.Context(), workspace); err != nil {
			t.Fatal(err)
		}
		verify(fmt.Sprintf("peer reload %d", step))
	}
	if metadataLogicalWrittenIndexes.Load() == written {
		t.Fatal("no builder stored a child index")
	}
}
