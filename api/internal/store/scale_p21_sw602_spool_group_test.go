package store

import (
	"fmt"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

// SW602: every generation derived from a source shares its spool group, so a
// workspace holds one descriptor charge however many generations and
// storages are alive (forks, transforms, abandoned forks); settled in-memory
// spools stay within the group's allowance and the rest go to files; the
// charge returns when the last generation is released.
func TestScaleP21SW602SpoolGroupHoldsOneCharge(t *testing.T) {
	ctx := t.Context()
	disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
	labels := func(round int) []domain.IssueLabel {
		var out []domain.IssueLabel
		for i := range 8 {
			out = append(out, domain.IssueLabel{ID: fmt.Sprintf("label-%d", i), Name: fmt.Sprintf("%d %s", round, strings.Repeat("n", 1<<10)), Color: "#4cb782"})
		}
		return out
	}
	source, err := captureMetadataLogicalSource(ctx, domain.Bootstrap{Labels: labels(0), Settings: map[string]any{"large": strings.Repeat("x", 256<<10)}}, copyStamp{workspaceID: "sw602-group"})
	if err != nil {
		t.Fatal(err)
	}
	group := source.spoolGroup()
	charged := func(step string) {
		t.Helper()
		if got := metadataLogicalDescriptorsUsed.Load() - descriptors; got != metadataLogicalDescriptorBytes {
			t.Fatalf("%s: the lineage holds %d bytes of FLOW_METADATA_SOURCE_BYTES, want one charge of %d", step, got, metadataLogicalDescriptorBytes)
		}
		group.mu.Lock()
		resident := group.resident
		group.mu.Unlock()
		if resident > metadataLogicalWriteBytes {
			t.Fatalf("%s: %d bytes of settled spools in memory, over the group's allowance", step, resident)
		}
	}
	charged("captured")
	scope := MetadataRecordScope{Fields: []string{"labels"}}
	generations := []*metadataLogicalSource{source}
	defer func() {
		for _, generation := range generations {
			generation.Release()
		}
	}()
	files := 0
	for round := 1; round <= 8; round++ {
		previous := generations[len(generations)-1]
		before, err := previous.Select(ctx, scope, 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		next := cloneBootstrap(before)
		next.Labels = labels(round)
		fork, err := previous.Fork(ctx, before, next, scope, previous.Stamp())
		if err != nil {
			t.Fatal(err)
		}
		generations = append(generations, fork)
		if fork.spoolGroup() != group {
			t.Fatalf("round %d: the fork wrote a spool group of its own", round)
		}
		for _, storage := range fork.stores {
			if storage.group != group {
				t.Fatalf("round %d: the fork holds a storage of another group", round)
			}
		}
		charged(fmt.Sprintf("fork %d", round))
		// An abandoned fork (cancelled while it writes) returns what it took.
		_, err = previous.Fork(&scaleP21SW27CancelAfterChecks{Context: ctx, limit: 6}, before, next, scope, previous.Stamp())
		if err == nil {
			t.Fatalf("round %d: the cancelled fork succeeded", round)
		}
		charged(fmt.Sprintf("cancelled fork %d", round))
		unchanged, err := fork.Transform(ctx, func(string, metadataLogicalNode) (metadataAggregateNode, bool, error) { return nil, false, nil })
		if err != nil {
			t.Fatal(err)
		}
		generations = append(generations, unchanged)
		charged(fmt.Sprintf("transform %d", round))
	}
	for _, generation := range generations {
		for _, storage := range generation.stores {
			storage.pendingMu.Lock()
			if storage.file != nil && storage.bytes < metadataLogicalWriteBytes {
				files++
			}
			storage.pendingMu.Unlock()
		}
	}
	if files == 0 {
		t.Fatal("no small spool went to a file once the group's allowance was full")
	}
	// Reads of every generation still see their own labels.
	for i, generation := range generations[1:] {
		data, err := generation.Select(ctx, scope, 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		if want := fmt.Sprintf("%d ", i/2+1); len(data.Labels) != 8 || !strings.HasPrefix(data.Labels[0].Name, want) {
			t.Fatalf("generation %d reads labels %q, want round %q", i+1, data.Labels[0].Name[:8], want)
		}
	}
	for _, generation := range generations[:len(generations)-1] {
		generation.Release()
	}
	generations = generations[len(generations)-1:]
	charged("older generations released")
	generations[0].Release()
	generations = nil
	if metadataLogicalDescriptorsUsed.Load() != descriptors || metadataLogicalDiskUsed.Load() != disk {
		t.Fatalf("releasing every generation left %d descriptor and %d disk bytes", metadataLogicalDescriptorsUsed.Load()-descriptors, metadataLogicalDiskUsed.Load()-disk)
	}
}
