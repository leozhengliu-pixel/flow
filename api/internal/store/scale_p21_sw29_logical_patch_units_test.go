package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP21SW29LogicalPatchUnitsFirstOccurrenceAndMapShape(t *testing.T) {
	ctx := t.Context()
	now := time.Now()
	data := domain.Bootstrap{
		Workspace:    domain.Workspace{ID: "patch-units"},
		CustomEmojis: []domain.CustomEmoji{{ID: "duplicate", Name: "first"}, {ID: "duplicate", Name: "second"}, {ID: "last", Name: "last"}},
		Settings:     map[string]any{"native\x00key": now, "untouched": []byte{0, 255}},
	}
	typed, err := captureMetadataLogicalSource(ctx, data, copyStamp{"patch-units", -1})
	if err != nil {
		t.Fatal(err)
	}
	defer typed.Release()
	packed, err := metadataLogicalUnitsImport(ctx, typed)
	if err != nil {
		t.Fatal(err)
	}
	defer packed.Release()
	builder, err := newMetadataLogicalBuilder(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer builder.Close()
	emoji, err := builder.writeValue(reflect.ValueOf(domain.CustomEmoji{ID: "duplicate", Name: "replacement"}), 0, false)
	if err != nil {
		t.Fatal(err)
	}
	transform := func(source *metadataLogicalSource) (*metadataLogicalSource, error) {
		return source.Transform(ctx, func(name string, node metadataLogicalNode) (metadataAggregateNode, bool, error) {
			var value metadataAggregateNode = metadataAggregateStoredNode{node}
			switch name {
			case "customEmojis":
				value = metadataLogicalPatchedCollection{value, node, "duplicate", nil}
				value = metadataLogicalPatchedCollection{value, node, "duplicate", metadataAggregateStoredNode{emoji}}
				return value, true, nil
			case "settings":
				value = metadataLogicalPatchedCollection{value, node, "native\x00key", nil}
				var native any = "value"
				value = metadataLogicalPatchedCollection{value, node, "new", metadataAggregateNativeNode{reflect.ValueOf(&native).Elem()}}
				return value, true, nil
			}
			return value, false, nil
		})
	}
	expected, err := transform(typed)
	if err != nil {
		t.Fatal(err)
	}
	defer expected.Release()
	actual, err := transform(packed)
	if err != nil {
		t.Fatal(err)
	}
	defer actual.Release()
	scope := MetadataRecordScope{Fields: []string{"customEmojis", "settings"}}
	want, err := expected.Select(ctx, scope, 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	got, err := actual.Select(ctx, scope, 2<<20)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal("framed point chain changed first-occurrence/order/native-map semantics", err, got, want)
	}
	before, err := packed.Select(ctx, scope, 2<<20)
	if err != nil || len(before.CustomEmojis) != 3 || before.Settings["native\x00key"] != now {
		t.Fatal("framed patch changed a retained old root", err)
	}
}

func TestScaleP21SW29LogicalPatchUnitsLargeFrameDeltaAndCancel(t *testing.T) {
	for _, count := range []int{32, 256} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			ctx := t.Context()
			data := domain.Bootstrap{Workspace: domain.Workspace{ID: "patch-large"}}
			for i := 0; i < count; i++ {
				data.CustomEmojis = append(data.CustomEmojis, domain.CustomEmoji{ID: fmt.Sprint(i), Name: "old"})
			}
			typed, err := captureMetadataLogicalSource(ctx, data, copyStamp{"patch-large", -1})
			if err != nil {
				t.Fatal(err)
			}
			packed, err := metadataLogicalUnitsImport(ctx, typed)
			typed.Release()
			if err != nil {
				t.Fatal(err)
			}
			defer packed.Release()
			builder, err := newMetadataLogicalBuilder(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer builder.Close()
			replacement, err := builder.writeValue(reflect.ValueOf(domain.CustomEmoji{ID: "0", Name: strings.Repeat("n", 3<<20)}), 0, false)
			if err != nil {
				t.Fatal(err)
			}
			transform := func(ctx context.Context) (*metadataLogicalSource, error) {
				return packed.Transform(ctx, func(name string, node metadataLogicalNode) (metadataAggregateNode, bool, error) {
					base := metadataAggregateStoredNode{node}
					if name == "customEmojis" {
						return metadataLogicalPatchedCollection{base, node, "0", metadataAggregateStoredNode{replacement}}, true, nil
					}
					return base, false, nil
				})
			}
			copied := packed.units.arena.unitBytes
			next, err := transform(ctx)
			if err != nil {
				t.Fatal("legal large replacement frame was decoded under small-page budget", err)
			}
			defer next.Release()
			delta := packed.units.arena.unitBytes - copied
			if delta != uint64(replacement.size) {
				t.Fatal("point patch copied untouched collection units", count, delta, replacement.size)
			}
			got, err := next.Select(ctx, MetadataRecordScope{Read: []RecordKey{{Kind: "customEmojis", ID: "0"}}}, 16<<20)
			if err != nil || len(got.CustomEmojis[0].Name) != 3<<20 {
				t.Fatal("large framed postimage changed its contents", err)
			}
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			beforeDisk, beforeDescriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
			if result, err := transform(cancelled); result != nil || !errors.Is(err, context.Canceled) {
				t.Fatal("cancelled framed patch produced a candidate", err)
			}
			if metadataLogicalDiskUsed.Load() != beforeDisk || metadataLogicalDescriptorsUsed.Load() != beforeDescriptors {
				t.Fatal("cancelled framed patch leaked ownership")
			}
			t.Logf("records=%d copied-point-frame-bytes=%d", count, delta)
		})
	}
}

func TestScaleP21SW29LogicalPatchUnitsRetainedPrivateAndJSONTime(t *testing.T) {
	ctx := t.Context()
	native := time.Now()
	data := domain.Bootstrap{
		Workspace:     domain.Workspace{ID: "patch-private"},
		ExportJobs:    []domain.ExportJob{{ID: "export", Data: "native\x00private", CreatedAt: native}},
		ImportJobs:    []domain.ImportJob{{ID: "import", Rows: []map[string]string{{"native": "row\x00input"}}, CreatedAt: native}},
		MigrationJobs: []domain.MigrationJob{{ID: "migration", Bundle: json.RawMessage("{ \"native\" : true }"), CreatedAt: native}},
	}
	source, err := captureMetadataLogicalSource(ctx, data, copyStamp{"patch-private", -1})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	packed, err := metadataLogicalUnitsImport(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	defer packed.Release()
	stamp := `"2026-11-13T12:34:56+05:45"`
	patches := []metadataLogicalRecordPatch{
		{metadataRecordKey{"exportJobs", "export"}, []byte(`{"id":"export","status":"done","createdAt":` + stamp + `}`)},
		{metadataRecordKey{"importJobs", "import"}, []byte(`{"id":"import","status":"done","createdAt":` + stamp + `}`)},
		{metadataRecordKey{"migrationJobs", "migration"}, []byte(`{"id":"migration","status":"done","createdAt":` + stamp + `}`)},
		{metadataRecordKey{"customEmojis", "new"}, []byte(`{"id":"new","name":"appended","createdAt":` + stamp + `}`)},
	}
	expected, err := metadataLogicalPatchRecords(ctx, source, patches)
	if err != nil {
		t.Fatal(err)
	}
	defer expected.Release()
	actual, err := metadataLogicalPatchRecords(ctx, packed, patches)
	if err != nil {
		t.Fatal(err)
	}
	defer actual.Release()
	scope := MetadataRecordScope{Fields: []string{"customEmojis", "exportJobs", "importJobs", "migrationJobs"}}
	want, err := expected.Select(ctx, scope, 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	got, err := actual.Select(ctx, scope, 2<<20)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal("framed postimage lost private/input subtrees or parser timestamp registry", err)
	}
}

func TestScaleP21SW29LogicalPatchUnitsNilMapDeleteShape(t *testing.T) {
	ctx := t.Context()
	source, err := captureMetadataLogicalSource(ctx, domain.Bootstrap{
		Workspace: domain.Workspace{ID: "patch-nil-map"},
	}, copyStamp{"patch-nil-map", -1})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	packed, err := metadataLogicalUnitsImport(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	defer packed.Release()
	patches := []metadataLogicalRecordPatch{{key: metadataRecordKey{"userSettings", "missing"}}}
	expected, err := metadataLogicalPatchRecords(ctx, source, patches)
	if err != nil {
		t.Fatal(err)
	}
	defer expected.Release()
	actual, err := metadataLogicalPatchRecords(ctx, packed, patches)
	if err != nil {
		t.Fatal(err)
	}
	defer actual.Release()
	scope := MetadataRecordScope{Fields: []string{"userSettings"}}
	want, err := expected.Select(ctx, scope, 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	got, err := actual.Select(ctx, scope, 2<<20)
	if err != nil || !reflect.DeepEqual(got, want) || got.UserSettings == nil {
		t.Fatal("point tombstone lost the producer's explicit nonnil map shape", err)
	}
}

func TestScaleP21SW29LogicalPatchUnitsCarriesObservedPhysicalBinding(t *testing.T) {
	ctx := t.Context()
	source, err := captureMetadataLogicalSource(ctx, domain.Bootstrap{
		Workspace:    domain.Workspace{ID: "patch-binding"},
		CustomEmojis: []domain.CustomEmoji{{ID: "logical", Name: "old"}},
	}, copyStamp{"patch-binding", -1})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	packed, err := metadataLogicalUnitsImport(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	defer packed.Release()
	logical, canonical := RecordKey{"customEmojis", "logical"}, RecordKey{"customEmojis", "physical\x00alias"}
	bound, err := packed.WithPhysicalBindings(ctx, []metadataLogicalPhysicalBinding{{
		Generation: packed.generation, Logical: logical, Canonical: canonical, Storage: 1,
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer bound.Release()
	next, err := metadataLogicalPatchRecords(ctx, bound, []metadataLogicalRecordPatch{{
		key: metadataRecordKey{"customEmojis", "logical"}, raw: []byte(`{"id":"logical","name":"fresh"}`),
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer next.Release()
	for _, candidate := range []*metadataLogicalSource{bound, next} {
		key, storage, err := candidate.PhysicalBinding(ctx, logical)
		if err != nil || key != canonical || storage != 1 {
			t.Fatal("framed update lost or re-inferred its observed physical identity", key, storage, err)
		}
	}
	deleted, err := metadataLogicalPatchRecords(ctx, next, []metadataLogicalRecordPatch{{
		key: metadataRecordKey{"customEmojis", "logical"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer deleted.Release()
	if _, _, err := deleted.PhysicalBinding(ctx, logical); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("deleted unit retained a physical binding", err)
	}
}
