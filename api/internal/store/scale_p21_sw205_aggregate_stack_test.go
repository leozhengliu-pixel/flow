package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestScaleP21SW205AggregateTraversalReusesSiblingHighWater(t *testing.T) {
	const depth, siblings = 160, 200
	left, right := scaleP21SW205NestedJSON(depth), scaleP21SW205NestedJSON(depth)
	leftRows, rightRows := make([]any, siblings), make([]any, siblings)
	for i := range leftRows {
		leftRows[i], rightRows[i] = left, right
	}
	var used, peak uint64
	calls := 0
	admit := func(size uint64) (func(), error) {
		if size > (2<<20)-used {
			return nil, ErrMetadataWorkBudget
		}
		used += size
		peak = max(peak, used)
		calls++
		return func() { used -= size }, nil
	}
	previous, release, err := metadataAggregateOwnedDiff(t.Context(), admit,
		map[string]any{"rows": leftRows}, map[string]any{"rows": rightRows})
	if err != nil || previous != nil {
		t.Fatal("equal deep siblings changed their diff or exhausted reusable stack", err)
	}
	release()
	if used != 0 || calls > depth+8 || peak > 2<<20 {
		t.Fatalf("walk work grew with siblings or survived diff: used=%d calls=%d peak=%d", used, calls, peak)
	}
}

func TestScaleP21SW205AggregateTraversalDeepEncodingOwned(t *testing.T) {
	for _, depth := range []int{180, 2000} {
		value := scaleP21SW205NestedJSON(depth)
		want, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		probe := &SQLiteStore{metadataResidency: newMetadataResidency()}
		raw, release, err := probe.metadataAggregateEncodeNormalizedOwned(t.Context(), value)
		if err != nil || !bytes.Equal(raw, want) {
			t.Fatal("required deep RawMessage fallback changed/refused", depth, err)
		}
		if probe.MetadataWorkUsedForTesting() != scaleP21SW205ByteCharge(len(raw)) {
			t.Fatal("graph-walk/encoder stack survived output construction", probe.MetadataWorkUsedForTesting())
		}
		release()
		if probe.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("deep output release leaked")
		}
	}
}

func TestScaleP21SW205AggregateTraversalQuotaAndCancellation(t *testing.T) {
	value := scaleP21SW205NestedJSON(200)
	for _, canceled := range []bool{false, true} {
		probe := &SQLiteStore{metadataResidency: newMetadataResidency()}
		ctx, cancel := context.WithCancel(t.Context())
		var work context.Context = ctx
		want := ErrMetadataWorkBudget
		if canceled {
			work = &scaleP21SW205CancelContext{Context: ctx, cancel: cancel, at: 10}
			want = context.Canceled
		} else {
			probe.metadataResidency.work.limit = 32 << 10
		}
		raw, release, err := probe.metadataAggregateEncodeNormalizedOwned(work, value)
		cancel()
		if release != nil {
			release()
		}
		if !errors.Is(err, want) || raw != nil || probe.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("stack admission/cancel failure leaked output or work", canceled, err, probe.MetadataWorkUsedForTesting())
		}
	}
}

func TestScaleP21SW205AggregateDiffTraversalRefusalReleases(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		probe := &SQLiteStore{metadataResidency: newMetadataResidency()}
		ctx, cancel := context.WithCancel(t.Context())
		var work context.Context = ctx
		want := ErrMetadataWorkBudget
		if canceled {
			work = &scaleP21SW205CancelContext{Context: ctx, cancel: cancel, at: 10}
			want = context.Canceled
		} else {
			probe.metadataResidency.work.limit = 32 << 10
		}
		admit := func(size uint64) (func(), error) {
			_, release, err := probe.tryAdmitAdditionalMetadataWork(work, int(size))
			return release, err
		}
		diff, release, err := metadataAggregateOwnedDiff(work, admit,
			map[string]any{"deep": scaleP21SW205NestedJSON(200)},
			map[string]any{"deep": scaleP21SW205NestedJSON(200)})
		cancel()
		if release != nil {
			release()
		}
		if !errors.Is(err, want) || diff != nil || probe.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("diff child-stack refusal/cancel leaked work", canceled, err, probe.MetadataWorkUsedForTesting())
		}
	}
}

func scaleP21SW205NestedJSON(depth int) any {
	var value any = float64(7)
	for i := 0; i < depth; i++ {
		value = []any{value}
	}
	return value
}
