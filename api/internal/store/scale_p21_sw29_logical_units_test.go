package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP21SW29LogicalUnitsColdNativeAndNoOriginalFilePin(t *testing.T) {
	ctx := t.Context()
	now := time.Now()
	data := domain.Bootstrap{
		Workspace:    domain.Workspace{ID: "unit-workspace"},
		CustomEmojis: []domain.CustomEmoji{{ID: "one", Name: "first"}, {ID: "two", Name: "second"}},
		Documents:    []domain.Document{{ID: "large", Content: strings.Repeat("native\x00\xff", 1<<18)}},
		Settings:     map[string]any{"native-time": now, "odd\x00\xff": []byte{0, 255}},
	}
	beforeDisk, beforeDescriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
	original, err := captureMetadataLogicalSource(ctx, data, copyStamp{"unit-origin", -1})
	if err != nil {
		t.Fatal(err)
	}
	originalPath := original.stores[0].file.Name()
	scopes := []MetadataRecordScope{
		{Read: []RecordKey{{Kind: "customEmojis", ID: "two"}, {Kind: "settings", ID: "native-time"}}},
		{Fields: []string{"settings", "documents", "workspaceSettings"}},
		{Read: []RecordKey{{Kind: "customEmojis", ID: "missing"}}},
	}
	wanted := make([]domain.Bootstrap, len(scopes))
	for i, scope := range scopes {
		wanted[i], err = original.Select(ctx, scope, 32<<20)
		if err != nil {
			original.Release()
			t.Fatal(err)
		}
	}
	packed, err := metadataLogicalUnitsImport(ctx, original)
	original.Release()
	if err != nil {
		t.Fatal(err)
	}
	defer packed.Release()
	if packed.generation == ([16]byte{}) {
		t.Fatal("packed source has no independent generation identity")
	}
	if _, err := os.Stat(originalPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("packed units retained original contiguous source file", err)
	}
	arenaPath := packed.units.arena.index.stage.path
	for i, scope := range scopes {
		actual, err := packed.Select(ctx, scope, 32<<20)
		if err != nil || !reflect.DeepEqual(actual, wanted[i]) {
			t.Fatalf("scope %d changed native/shape/order semantics: %v", i, err)
		}
	}
	packed.Release()
	if _, err := os.Stat(arenaPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("last root did not remove packed arena", err)
	}
	if metadataLogicalDiskUsed.Load() != beforeDisk || metadataLogicalDescriptorsUsed.Load() != beforeDescriptors {
		t.Fatal("cold packed lifetime leaked disk or registry descriptors")
	}
}

type sw29LogicalUnitsCancelFile struct {
	metadataLogicalFile
	cancel context.CancelFunc
}

func (f sw29LogicalUnitsCancelFile) ReadAtContext(_ context.Context, p []byte, off int64) (int, error) {
	n, err := f.metadataLogicalFile.ReadAt(p, off)
	if len(p) > 4096 {
		f.cancel()
	}
	return n, err
}

func TestScaleP21SW29LogicalUnitsCancelledColdReclaims(t *testing.T) {
	ctx := t.Context()
	data := domain.Bootstrap{
		Workspace: domain.Workspace{ID: "cancel-origin"},
		Documents: []domain.Document{{ID: "large", Content: strings.Repeat("x", 4<<20)}},
	}
	source, err := captureMetadataLogicalSource(ctx, data, copyStamp{"cancel-origin", 1})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	beforeDisk, beforeDescriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
	cancelled, cancel := context.WithCancel(ctx)
	defer cancel()
	source.stores[0].file = sw29LogicalUnitsCancelFile{source.stores[0].file, cancel}
	if result, err := metadataLogicalUnitsImport(cancelled, source); result != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("cold import did not cancel before publishing a root", err)
	}
	if metadataLogicalDiskUsed.Load() != beforeDisk || metadataLogicalDescriptorsUsed.Load() != beforeDescriptors {
		t.Fatal("cancelled import leaked arena/registry reservation")
	}
}

func TestScaleP21SW29LogicalUnitsPersistentDeltaReclaimsOldUnit(t *testing.T) {
	for _, count := range []int{32, 256} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			ctx := t.Context()
			data := domain.Bootstrap{Workspace: domain.Workspace{ID: "delta-origin"}}
			for i := 0; i < count; i++ {
				data.CustomEmojis = append(data.CustomEmojis, domain.CustomEmoji{ID: fmt.Sprint(i), Name: "old"})
			}
			source, err := captureMetadataLogicalSource(ctx, data, copyStamp{"delta-origin", -1})
			if err != nil {
				t.Fatal(err)
			}
			packed, err := metadataLogicalUnitsImport(ctx, source)
			source.Release()
			if err != nil {
				t.Fatal(err)
			}
			defer packed.Release()
			arena := packed.units.arena
			field, _, _ := metadataLogicalField("customEmojis")
			order := metadataLogicalUnitsOrderKey(field, 0)
			builder, err := newMetadataLogicalBuilder(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer builder.Close()
			replacement, err := builder.writeValue(reflect.ValueOf(domain.CustomEmoji{ID: "0", Name: "new"}), 0, false)
			if err != nil {
				t.Fatal(err)
			}
			oldRaw, found, err := arena.index.Get(ctx, packed.units.trees[0], order)
			if err != nil || !found {
				t.Fatal("old unit missing", err)
			}
			oldLeaf, err := metadataLogicalUnitsDecodeLeaf(oldRaw)
			if err != nil {
				t.Fatal(err)
			}
			oldID := oldLeaf.unit
			scope := MetadataRecordScope{Write: []RecordKey{{Kind: "customEmojis", ID: "0"}}}
			preimage, err := packed.Select(ctx, scope, 2<<20)
			if err != nil {
				t.Fatal(err)
			}
			changed := cloneBootstrap(preimage)
			changed.CustomEmojis[0].Name = "new"
			writtenBefore, bytesBefore := arena.index.writes, arena.unitBytes
			next, err := packed.Fork(ctx, preimage, changed, scope, packed.Stamp())
			if err != nil {
				t.Fatal(err)
			}
			defer next.Release()
			if next.generation == packed.generation || next.Stamp() != packed.Stamp() {
				t.Fatal("same unknown stamp aliased distinct native generations")
			}
			old, err := packed.Select(ctx, scope, 2<<20)
			if err != nil || old.CustomEmojis[0].Name != "old" {
				t.Fatal("point delta mutated retained earlier generation", err)
			}
			current, err := next.Select(ctx, scope, 2<<20)
			if err != nil || current.CustomEmojis[0].Name != "new" {
				t.Fatal("point delta did not publish new unit", err)
			}
			var live int
			if err := arena.index.stage.db.QueryRow(`SELECT COUNT(*) FROM logical_units WHERE id=?`, oldID).Scan(&live); err != nil || live != 1 {
				t.Fatal("old pinned generation lost its replaced unit", live, err)
			}
			writes, copied := arena.index.writes-writtenBefore, arena.unitBytes-bytesBefore
			packed.Release()
			if err := arena.index.stage.db.QueryRow(`SELECT COUNT(*) FROM logical_units WHERE id=?`, oldID).Scan(&live); err != nil || live != 0 {
				t.Fatal("unreachable replaced unit was not reclaimed", live, err)
			}
			if arena.cleanupErr != nil {
				t.Fatal(arena.cleanupErr)
			}
			t.Logf("records=%d point_delta_tree_nodes=%d copied_unit_bytes=%d", count, writes, copied)
			if copied != uint64(replacement.size) || writes > 128 {
				t.Fatal("point delta copied untouched unit bodies or rebuilt whole order tree")
			}
		})
	}
}
