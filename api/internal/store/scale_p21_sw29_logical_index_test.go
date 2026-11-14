package store

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math/bits"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func sw29LogicalIndexTestKey(i int) []byte {
	var key [8]byte
	binary.BigEndian.PutUint64(key[:], uint64(i))
	return key[:]
}

func TestScaleP21SW29LogicalIndexSourcePrecedence(t *testing.T) {
	ctx := t.Context()
	longID := strings.Repeat("long\x00\xff", 10000)
	pointerID := "pointer-string-hidden"
	data := domain.Bootstrap{
		Workspace: domain.Workspace{ID: "workspace-source"},
		Projects:  []domain.Project{{ID: "project_real", Name: "prefix"}},
		Documents: []domain.Document{{ID: "doc_root", ContentData: map[string]any{
			"first":            map[string]any{"id": "nested_winner", "name": "document"},
			"prefix-collision": map[string]any{"id": "project_real", "name": "earlier nested"},
			"fallback":         map[string]any{"id": "project_missing", "name": "fallback nested"},
			"terminal":         map[string]any{"id": "team_resource_missing", "name": "must hide"},
		}}},
		CustomEmojis: []domain.CustomEmoji{{ID: "duplicate", Name: "first"}, {ID: "duplicate", Name: "second"}, {ID: longID, Name: "large-id"}},
		Settings: map[string]any{
			"id":      "root-map-id",
			"nested":  map[string]any{"id": "inner-map-id", "name": "settings"},
			"left":    map[string]any{"id": "map-tie", "name": "left"},
			"right":   map[string]any{"id": "map-tie", "name": "right"},
			"raw":     json.RawMessage(`{"id":"raw-hidden"}`),
			"pointer": map[string]any{"id": &pointerID},
		},
		ImportJobs: []domain.ImportJob{{ID: "import_job", Rows: []map[string]string{{"id": "import-row-hidden"}}}},
	}
	source, err := captureMetadataLogicalSource(ctx, data, copyStamp{"source", -1})
	if err != nil {
		t.Fatal(err)
	}
	corpus, err := sw29BuildLogicalCorpus(ctx, source)
	if err != nil {
		source.Release()
		t.Fatal(err)
	}
	defer corpus.Close()
	for _, scope := range []MetadataRecordScope{
		{Read: []RecordKey{{Kind: "customEmojis", ID: longID}, {Kind: "settings", ID: "nested"}, {Kind: "workspaceSettings", ID: "sessionDurationDays"}}},
		{Read: []RecordKey{{Kind: "customEmojis", ID: "missing"}}},
		{Read: []RecordKey{{Kind: "customEmojis", ID: "duplicate"}}},
		{Fields: []string{"settings"}},
	} {
		got, gotErr := corpus.Select(ctx, scope, 32<<20)
		want, wantErr := source.Select(ctx, scope, 32<<20)
		if (gotErr == nil) != (wantErr == nil) || gotErr == nil && !reflect.DeepEqual(got, want) {
			t.Fatalf("indexed Select differs from source: goterr=%v wanterr=%v", gotErr, wantErr)
		}
	}
	// Corpus locators pin flat storage owners independently of the source
	// descriptor. Source closure cannot invalidate current indexed readers.
	source.Release()
	for _, id := range []string{"project_real", "project_missing", "team_resource_missing", "nested_winner", "root-map-id", "inner-map-id", "map-tie", "raw-hidden", pointerID, "import-row-hidden", "import_job", longID, "absent"} {
		found, err := corpus.Resolve(ctx, id)
		if err != nil {
			t.Fatalf("resolve %q: %v", id[:min(len(id), 40)], err)
		}
		var value any
		if found != nil {
			owned, err := found.nodeValue(ctx, 32<<20)
			if err != nil {
				t.Fatal(err)
			}
			value = owned.Interface()
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		want := aggregateJSONValue(data, id)
		expected, err := json.Marshal(want)
		if err != nil {
			t.Fatal(err)
		}
		var semantic, expectedSemantic any
		if err := json.Unmarshal(encoded, &semantic); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(expected, &expectedSemantic); err != nil {
			t.Fatal(err)
		}
		if id == "map-tie" {
			var actual map[string]any
			if err := json.Unmarshal(encoded, &actual); err != nil {
				t.Fatal(err)
			}
			if actual["name"] != "left" && actual["name"] != "right" {
				t.Fatalf("invalid map-tie winner %s", encoded)
			}
		} else if !reflect.DeepEqual(semantic, expectedSemantic) {
			t.Fatalf("resolve %q got %s want %s", id[:min(len(id), 40)], encoded, expected)
		}
	}
	var duplicate []string
	err = corpus.Point(ctx, "customEmojis", "duplicate", func(node metadataLogicalNode) error {
		value, err := node.Value(ctx, 2<<20)
		if err == nil {
			duplicate = append(duplicate, value.Interface().(domain.CustomEmoji).Name)
		}
		return err
	})
	if err != nil || !reflect.DeepEqual(duplicate, []string{"first", "second"}) {
		t.Fatalf("duplicate point closure %v %v", duplicate, err)
	}
	var longNode metadataLogicalNode
	if err := corpus.Point(ctx, "customEmojis", longID, func(node metadataLogicalNode) error { longNode = node; return nil }); err != nil {
		t.Fatal(err)
	}
	canonical := RecordKey{Kind: "customEmojis", ID: "physical\x00key"}
	if err := corpus.BindCanonical(ctx, longNode, canonical, 1); err != nil {
		t.Fatal(err)
	}
	key, physical, err := corpus.Canonical(ctx, longNode)
	if err != nil || key != canonical || physical != 1 {
		t.Fatalf("physical key substituted for logical ID: %#v %d %v", key, physical, err)
	}
	if err := corpus.BindCanonical(ctx, longNode, RecordKey{Kind: "customEmojis", ID: "different"}, 0); err == nil {
		t.Fatal("immutable physical binding changed")
	}
}

func TestScaleP21SW29LogicalIndexPersistentTree(t *testing.T) {
	ctx := t.Context()
	index, err := sw29NewLogicalIndex(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()
	var root uint64
	for i := 0; i < 300; i++ {
		root, err = index.Put(ctx, root, sw29LogicalIndexTestKey(i), []byte(fmt.Sprintf("old-%d", i)))
		if err != nil {
			t.Fatal(err)
		}
	}
	original := root
	oldWrites := index.writes
	root, err = index.Put(ctx, root, sw29LogicalIndexTestKey(150), []byte("updated"))
	if err != nil {
		t.Fatal(err)
	}
	if delta := index.writes - oldWrites; delta > sw29LogicalIndexDepth*3 {
		t.Fatalf("point fork copied too many nodes: %d", delta)
	}
	root, err = index.Delete(ctx, root, sw29LogicalIndexTestKey(99))
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		root  uint64
		key   int
		value string
		found bool
	}{
		{original, 150, "old-150", true}, {root, 150, "updated", true},
		{original, 99, "old-99", true}, {root, 99, "", false},
	} {
		value, found, err := index.Get(ctx, item.root, sw29LogicalIndexTestKey(item.key))
		if err != nil || found != item.found || string(value) != item.value {
			t.Fatalf("root=%d key=%d got %q %v %v", item.root, item.key, value, found, err)
		}
	}
	var ordered []int
	err = index.Range(ctx, root, sw29LogicalIndexTestKey(95), sw29LogicalIndexTestKey(104), func(key, value []byte) error {
		ordered = append(ordered, int(binary.BigEndian.Uint64(key)))
		return nil
	})
	if err != nil || len(ordered) != 8 {
		t.Fatalf("range=%v error=%v", ordered, err)
	}
	for i, value := range ordered {
		want := 95 + i
		if want >= 99 {
			want++
		}
		if value != want {
			t.Fatalf("range order %v", ordered)
		}
	}
}

func TestScaleP21SW29LogicalIndexBoundsCancellation(t *testing.T) {
	before := recordSpoolBytes.Load()
	index, err := sw29NewLogicalIndex(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := index.Put(t.Context(), 0, make([]byte, sw29LogicalIndexKeyBytes+1), nil); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatalf("key bound: %v", err)
	}
	root, err := index.Put(t.Context(), 0, []byte("owned"), []byte("body"))
	if err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := index.Get(cancelled, root, []byte("owned")); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read: %v", err)
	}
	if _, err := index.Put(cancelled, root, []byte("next"), nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled fork: %v", err)
	}
	if err := index.Close(); err != nil {
		t.Fatal(err)
	}
	if recordSpoolBytes.Load() != before {
		t.Fatal("index leaked global spool admission")
	}
}

func TestScaleP21SW29LogicalIndexMeasuredPointWork(t *testing.T) {
	for _, size := range []int{128, 1024} {
		t.Run(fmt.Sprintf("facts-%d", size), func(t *testing.T) {
			index, err := sw29NewLogicalIndex(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer index.Close()
			var root uint64
			started := time.Now()
			for i := 0; i < size; i++ {
				// Reverse-bit insertion differs from sorted order and exercises
				// both path-copy rotations without changing logical rank.
				ordinal := int(bits.Reverse64(uint64(i)) >> uint(64-bits.Len(uint(size-1))))
				root, err = index.Put(t.Context(), root, sw29LogicalIndexTestKey(ordinal), []byte("locator"))
				if err != nil {
					t.Fatal(err)
				}
			}
			build := time.Since(started)
			before := index.reads
			lookup := time.Now()
			for _, ordinal := range []int{0, size / 2, size - 1, size + 1} {
				_, _, err := index.Get(t.Context(), root, sw29LogicalIndexTestKey(ordinal))
				if err != nil {
					t.Fatal(err)
				}
			}
			reads := index.reads - before
			if reads > 4*sw29LogicalIndexDepth {
				t.Fatalf("lookup path bound exceeded: %d", reads)
			}
			mutationReads, mutationWrites := index.reads, index.writes
			forked, err := index.Put(t.Context(), root, sw29LogicalIndexTestKey(size/2), []byte("changed"))
			if err != nil {
				t.Fatal(err)
			}
			old, _, err := index.Get(t.Context(), root, sw29LogicalIndexTestKey(size/2))
			if err != nil || !bytes.Equal(old, []byte("locator")) {
				t.Fatal("fork changed old root")
			}
			current, _, err := index.Get(t.Context(), forked, sw29LogicalIndexTestKey(size/2))
			if err != nil || !bytes.Equal(current, []byte("changed")) {
				t.Fatal("new root missing point change")
			}
			t.Logf("facts=%d build=%s four-lookups=%s local-node-reads=%d point-fork-new-nodes=%d point-fork-plus-two-proof-reads=%d canonical-SQL=0", size, build, time.Since(lookup), reads, index.writes-mutationWrites, index.reads-mutationReads)
		})
	}
}

func TestScaleP21SW29LogicalIndexSourceMeasuredLookup(t *testing.T) {
	for _, size := range []int{64, 512} {
		t.Run(fmt.Sprintf("source-records-%d", size), func(t *testing.T) {
			data := domain.Bootstrap{
				Workspace: domain.Workspace{ID: "indexed-workspace"},
				Documents: []domain.Document{{ID: "doc_large_unrelated", Content: strings.Repeat("body", 2<<20)}},
			}
			for i := 0; i < size; i++ {
				data.CustomEmojis = append(data.CustomEmojis, domain.CustomEmoji{ID: fmt.Sprintf("indexed_%06d", i), Name: "point"})
			}
			source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{})
			if err != nil {
				t.Fatal(err)
			}
			defer source.Release()
			built := time.Now()
			corpus, err := sw29BuildLogicalCorpus(t.Context(), source)
			if err != nil {
				t.Fatal(err)
			}
			defer corpus.Close()
			buildTime := time.Since(built)
			scope := MetadataRecordScope{Read: []RecordKey{{Kind: "customEmojis", ID: data.CustomEmojis[size-1].ID}}}
			started := time.Now()
			var expected domain.Bootstrap
			for i := 0; i < 5; i++ {
				expected, err = source.Select(t.Context(), scope, 2<<20)
				if err != nil {
					t.Fatal(err)
				}
			}
			linear := time.Since(started)
			before := corpus.index.reads
			started = time.Now()
			for i := 0; i < 5; i++ {
				got, err := corpus.Select(t.Context(), scope, 2<<20)
				if err != nil || !reflect.DeepEqual(got, expected) {
					t.Fatalf("indexed point %v", err)
				}
			}
			indexed := time.Since(started)
			t.Logf("records=%d unrelated-body=8MiB build=%s source-linear-five=%s indexed-five=%s local-node-reads=%d canonical-SQL=0", size, buildTime, linear, indexed, corpus.index.reads-before)
		})
	}
}
