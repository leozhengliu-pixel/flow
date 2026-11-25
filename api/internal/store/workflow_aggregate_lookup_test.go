package store

import (
	"fmt"
	"reflect"
	"testing"

	"flow/api/internal/domain"
)

func workflowLookupFixture(issues int) domain.Bootstrap {
	data := domain.Bootstrap{}
	for i := range issues {
		data.Issues = append(data.Issues, domain.Issue{ID: fmt.Sprintf("issue_wl_%05d", i), Title: fmt.Sprintf("Issue %d with some body text to give the frame weight", i)})
	}
	for i := range 6 {
		data.WorkflowDefinitions = append(data.WorkflowDefinitions, domain.WorkflowDefinition{ID: fmt.Sprintf("workflow_wl_%d", i), Name: fmt.Sprintf("Workflow %d", i)})
		data.WorkflowRuns = append(data.WorkflowRuns, domain.WorkflowRun{ID: fmt.Sprintf("workflow_run_wl_%d", i), WorkflowID: fmt.Sprintf("workflow_wl_%d", i)})
	}
	return data
}

// The prefix routing of workflow definitions and runs returns what the
// legacy lookup (and the unrouted scan) returns, for every record.
func TestWorkflowAggregateLookupEqualsLegacy(t *testing.T) {
	data := workflowLookupFixture(300)
	source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	var ids []string
	for _, definition := range data.WorkflowDefinitions {
		ids = append(ids, definition.ID)
	}
	for _, run := range data.WorkflowRuns {
		ids = append(ids, run.ID)
	}
	ids = append(ids, "workflow_missing", "workflow_run_missing", data.Issues[7].ID)
	for _, id := range ids {
		want := aggregateJSONValue(data, id)
		got, err := metadataAggregateJSONValue(t.Context(), metadataAggregateImage{Source: source}, id, 4<<20)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: bounded %#v err=%v, legacy %#v", id, got, err, want)
		}
		if id[:9] == "workflow_" && id != "workflow_missing" && id != "workflow_run_missing" && want == nil {
			t.Fatalf("%s: fixture record not found", id)
		}
	}
}

// A definition ID nobody holds in its collection still falls through to the
// whole-image scan (legacy order), as every other prefix does.
func TestWorkflowAggregateLookupMissFallsThrough(t *testing.T) {
	const id = "workflow_nested"
	data := workflowLookupFixture(5)
	data.Documents = []domain.Document{{ID: "document_wl", ContentData: map[string]any{"id": id, "marker": "nested"}}}
	source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	want := aggregateJSONValue(data, id)
	got, err := metadataAggregateJSONValue(t.Context(), metadataAggregateImage{Source: source}, id, 1<<20)
	if err != nil || want == nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("fallback %#v err=%v, legacy %#v", got, err, want)
	}
}

// The routed lookup reads the definitions collection only: the spool bytes it
// reads do not grow with the unrelated collections, and are a small part of
// the unrouted scan's.
func TestWorkflowAggregateLookupDoesNotScanOtherCollections(t *testing.T) {
	measure := func(issues int, id string) int64 {
		data := workflowLookupFixture(issues)
		source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{})
		if err != nil {
			t.Fatal(err)
		}
		defer source.Release()
		metadataLogicalSpoolCounting.Store(true)
		defer metadataLogicalSpoolCounting.Store(false)
		before := metadataLogicalSpoolReadBytes.Load()
		node, err := metadataAggregateResolve(t.Context(), metadataAggregateImage{Source: source}, id)
		if err != nil || node == nil {
			t.Fatalf("resolve %s: %v %v", id, node, err)
		}
		return metadataLogicalSpoolReadBytes.Load() - before
	}
	small, large := measure(50, "workflow_wl_5"), measure(5000, "workflow_wl_5")
	if large > small*2+4096 {
		t.Fatalf("lookup reads grew with unrelated issues: %d bytes at 50, %d at 5000", small, large)
	}
	// An ID with no routed prefix scans the issues (the former cost).
	scan := measure(5000, "issue_wl_04999")
	t.Logf("routed workflow lookup reads %d bytes at 5000 issues (%d at 50); an unrouted lookup of the last issue reads %d", large, small, scan)
}
