package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"flow/api/internal/domain"
)

func sw29UnitsSameJSON(t *testing.T, got, want metadataAggregateNode) {
	t.Helper()
	semantic := func(node metadataAggregateNode) any {
		t.Helper()
		if node == nil {
			return nil
		}
		owned, err := node.nodeValue(t.Context(), 32<<20)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(owned.Interface())
		if err != nil {
			t.Fatal(err)
		}
		var decoded any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatal(err)
		}
		return decoded
	}
	if !reflect.DeepEqual(semantic(got), semantic(want)) {
		t.Fatal("indexed aggregate semantics differ from literal typed source")
	}
}

func TestScaleP21SW29LogicalUnitsForkMapsArraysAndGlobalFacts(t *testing.T) {
	ctx := t.Context()
	data := domain.Bootstrap{
		Workspace:    domain.Workspace{ID: "facts-origin"},
		CustomEmojis: []domain.CustomEmoji{{ID: "one", Name: "first"}, {ID: "two", Name: "second"}},
		Settings: map[string]any{
			"id":     "whole-map-id",
			"nested": map[string]any{"id": "old-nested-id", "name": "old"},
		},
	}
	source, err := captureMetadataLogicalSource(ctx, data, copyStamp{"facts-origin", -1})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	packed, err := metadataLogicalUnitsImport(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	defer packed.Release()
	scope := MetadataRecordScope{Write: []RecordKey{
		{Kind: "settings", ID: "nested"}, {Kind: "settings", ID: "added"},
		{Kind: "customEmojis", ID: "one"}, {Kind: "customEmojis", ID: "three"},
		{Kind: "workspaceSettings", ID: "sessionDurationDays"},
	}}
	before, err := source.Select(ctx, scope, 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	next := cloneBootstrap(before)
	next.Settings["nested"] = map[string]any{"id": "new-nested-id", "name": "new"}
	next.Settings["added"] = map[string]any{"id": "added-map-id", "name": "added"}
	next.CustomEmojis = []domain.CustomEmoji{{ID: "three", Name: "third"}}
	next.WorkspaceSettings.SessionDurationDays = 7
	orders := map[metadataRecordKey]int{{field: "customEmojis", key: "three"}: 3}
	want, err := source.Fork(ctx, before, next, scope, source.Stamp(), orders)
	if err != nil {
		t.Fatal(err)
	}
	defer want.Release()
	got, err := packed.Fork(ctx, before, next, scope, packed.Stamp(), orders)
	if err != nil {
		t.Fatal(err)
	}
	defer got.Release()
	full := MetadataRecordScope{Fields: []string{"settings", "customEmojis", "workspaceSettings"}}
	expected, err := want.Select(ctx, full, 32<<20)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := got.Select(ctx, full, 32<<20)
	if err != nil || !reflect.DeepEqual(actual, expected) {
		t.Fatal("array deletion/append, map replacement/create or struct property changed", err)
	}
	for _, id := range []string{"whole-map-id", "old-nested-id", "new-nested-id", "added-map-id", "one", "two", "three", "absent"} {
		indexed, err := got.unitCorpus(ctx).Resolve(ctx, id)
		if err != nil {
			t.Fatal(id, err)
		}
		literal, err := metadataAggregateResolve(ctx, metadataAggregateImage{Source: want}, id)
		if err != nil {
			t.Fatal(id, err)
		}
		sw29UnitsSameJSON(t, indexed, literal)
	}
	// Complete-field replacement is a separate bounded operation.
	replace := MetadataRecordScope{Fields: []string{"customEmojis"}}
	preimage, err := got.Select(ctx, replace, 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	changed := cloneBootstrap(preimage)
	changed.CustomEmojis = nil
	empty, err := got.Fork(ctx, preimage, changed, replace, got.Stamp())
	if err != nil {
		t.Fatal(err)
	}
	defer empty.Release()
	result, err := empty.Select(ctx, replace, 2<<20)
	if err != nil || result.CustomEmojis != nil {
		t.Fatal("complete-field replacement lost literal nil shape", err)
	}
}

func TestScaleP21SW29LogicalUnitsCompactionUsesPendingUnits(t *testing.T) {
	ctx := t.Context()
	data := domain.Bootstrap{
		Workspace: domain.Workspace{ID: "compact-origin"},
		ImportJobs: []domain.ImportJob{
			{ID: "large-input", Rows: []map[string]string{{"data": strings.Repeat("x", 4<<20)}}},
			{ID: "already-compacted", Rows: nil},
		},
	}
	source, err := captureMetadataLogicalSource(ctx, data, copyStamp{"compact-origin", -1})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	packed, err := metadataLogicalUnitsImport(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	defer packed.Release()
	beforeBytes := packed.units.arena.unitBytes
	compacted, err := metadataLogicalInstalledCandidate(ctx, packed)
	if err != nil {
		t.Fatal(err)
	}
	defer compacted.Release()
	want, err := metadataLogicalInstalledCandidate(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	defer want.Release()
	scope := MetadataRecordScope{Fields: []string{"importJobs"}}
	actual, err := compacted.Select(ctx, scope, 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := want.Select(ctx, scope, 2<<20)
	if err != nil || !reflect.DeepEqual(actual, expected) {
		t.Fatal("pending-unit compaction changed import semantics", err)
	}
	rewritten := compacted.units.arena.unitBytes - beforeBytes
	if rewritten > 16<<10 {
		t.Fatal("compaction copied discarded large input or unrelated record", rewritten)
	}
	beforeBytes = compacted.units.arena.unitBytes
	again, err := metadataLogicalInstalledCandidate(ctx, compacted)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Release()
	if again.units.arena.unitBytes != beforeBytes {
		t.Fatal("ordinary already-compacted candidate recopied historical unit bytes")
	}
}

func TestScaleP21SW29LogicalUnitsCancelledDeltaKeepsPinnedRoot(t *testing.T) {
	ctx := t.Context()
	data := domain.Bootstrap{
		Workspace:    domain.Workspace{ID: "cancel-delta-origin"},
		CustomEmojis: []domain.CustomEmoji{{ID: "one", Name: "untouched"}},
	}
	source, err := captureMetadataLogicalSource(ctx, data, copyStamp{"cancel-delta-origin", 1})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	packed, err := metadataLogicalUnitsImport(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	defer packed.Release()
	r, a := packed.units, packed.units.arena
	field, _, _ := metadataLogicalField("customEmojis")
	order := metadataLogicalUnitsOrderKey(field, 0)
	beforeDescriptors := metadataLogicalDescriptorsUsed.Load()
	cancelled, cancel := context.WithCancel(ctx)
	defer cancel()
	result, err := a.change(cancelled, r, r.stamp, func(tx *sql.Tx, trees *metadataLogicalUnitTrees, fields []metadataLogicalUnitsField) error {
		raw, _, err := a.index.Get(cancelled, trees[0], order)
		if err != nil {
			return err
		}
		leaf, err := metadataLogicalUnitsDecodeLeaf(raw)
		if err != nil {
			return err
		}
		if err := a.dropLeaf(cancelled, tx, trees, field, 0, &fields[field], leaf); err != nil {
			return err
		}
		cancel()
		return cancelled.Err()
	})
	if result != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled candidate was published", err)
	}
	if metadataLogicalDescriptorsUsed.Load() != beforeDescriptors {
		t.Fatal("cancelled candidate leaked root/registry descriptors")
	}
	actual, err := packed.Select(ctx, MetadataRecordScope{Fields: []string{"customEmojis"}}, 2<<20)
	if err != nil || !reflect.DeepEqual(actual.CustomEmojis, data.CustomEmojis) {
		t.Fatal("cancelled path copy changed a pinned earlier root", err)
	}
}

func TestScaleP21SW29LogicalUnitsConcurrentRetainedRootAndForkGC(t *testing.T) {
	ctx := t.Context()
	source, err := captureMetadataLogicalSource(ctx, domain.Bootstrap{
		Workspace:    domain.Workspace{ID: "concurrent-origin"},
		CustomEmojis: []domain.CustomEmoji{{ID: "one", Name: "initial"}},
	}, copyStamp{"concurrent-origin", -1})
	if err != nil {
		t.Fatal(err)
	}
	packed, err := metadataLogicalUnitsImport(ctx, source)
	source.Release()
	if err != nil {
		t.Fatal(err)
	}
	if err := packed.Retain(); err != nil {
		t.Fatal(err)
	}
	defer packed.Release()
	scope := MetadataRecordScope{Write: []RecordKey{{Kind: "customEmojis", ID: "one"}}}
	errs := make(chan error, 4)
	var readers sync.WaitGroup
	for i := 0; i < 4; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for iteration := 0; iteration < 20; iteration++ {
				selected, err := packed.Select(ctx, scope, 2<<20)
				if err == nil && (len(selected.CustomEmojis) != 1 || selected.CustomEmojis[0].Name != "initial") {
					err = errors.New("retained immutable generation changed during concurrent forks")
				}
				if err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	current := packed
	var writerErr error
	for i := 0; i < 6; i++ {
		before, err := current.Select(ctx, scope, 2<<20)
		if err != nil {
			writerErr = err
			break
		}
		next := cloneBootstrap(before)
		next.CustomEmojis[0].Name = strings.Repeat("updated", i+1)
		candidate, err := current.Fork(ctx, before, next, scope, current.Stamp())
		if err != nil {
			writerErr = err
			break
		}
		current.Release()
		current = candidate
	}
	current.Release()
	readers.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if writerErr != nil {
		t.Fatal(writerErr)
	}
}
