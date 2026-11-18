package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP21SW352PublicationStoredFrame(t *testing.T) {
	issues := []domain.Issue{{ID: "sw352", CreatedAt: time.Date(2026, 11, 17, 4, 5, 6, 123456789, time.FixedZone("sw352", 9*3600+30*60))}}
	values := map[string]any{
		"large-raw":               json.RawMessage(` {"body":"` + strings.Repeat("x", 300<<10) + `"} `),
		"invalid-utf8-raw":        json.RawMessage(`{"body":"` + "\xff" + `"}`),
		"duplicate-escape-number": json.RawMessage(` {"n":9007199254740993,"n":-0,"e":1e-100,"s":"<>&\u2028\u2029\u0000","u":"\ud800"} `),
		"nested-mixed": map[string]any{
			"items": []any{nil, true, json.Number("9007199254740993"), float64(1e-7), map[string]any{"raw": json.RawMessage(`[1,2,3]`)}},
			"actor": domain.User{ID: "sw352", Name: "<actor>\xff"},
			"bytes": []byte{0, 255}, "empty": []string{}, "nil": []string(nil),
		},
		"malformed-raw": json.RawMessage(`{"truncated":`),
	}
	source, err := captureMetadataLogicalSource(t.Context(), domain.Bootstrap{Settings: values, Issues: issues}, copyStamp{workspaceID: "sw352", revision: 19})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	index, _, ok := metadataLogicalField("settings")
	if !ok {
		t.Fatal("settings field missing")
	}
	parent := metadataAggregateStoredNode{source.fields[index]}
	fields, stamp, refs := slices.Clone(source.fields), source.Stamp(), source.refs
	storage := source.stores[0]
	types, times, locations := slices.Clone(storage.types), slices.Clone(storage.times), slices.Clone(storage.locations)
	storageBytes, storageRefs := storage.bytes, storage.refs
	disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
	probe := &SQLiteStore{metadataResidency: newMetadataResidency()}
	for _, name := range []string{"large-raw", "invalid-utf8-raw", "duplicate-escape-number", "fixed-offset-time", "nested-mixed", "malformed-raw"} {
		t.Run(name, func(t *testing.T) {
			node, err := metadataAggregateChild(t.Context(), parent, name)
			native := values[name]
			if name == "fixed-offset-time" {
				issueIndex, _, found := metadataLogicalField("issues")
				if !found {
					t.Fatal("issues field missing")
				}
				node, native = metadataAggregateStoredNode{source.fields[issueIndex]}, issues
			}
			if err != nil {
				t.Fatal(err)
			}
			stored, ok := node.(metadataAggregateStoredNode)
			if !ok {
				t.Fatal("captured fixture did not select the stored-frame path")
			}
			before, err := stored.value.Value(t.Context(), metadataWorkBytes)
			if name == "fixed-offset-time" {
				if !errors.Is(err, ErrBoundedMetadataCoverage) {
					t.Fatal("fresh named zone changed its native-read refusal", err)
				}
			} else if err != nil || !reflect.DeepEqual(before.Interface(), native) {
				t.Fatal("captured fixture changed its original native value", err)
			}
			owner := &ResourceRead{store: probe}
			defer owner.Close()
			if err := owner.reserve(t.Context(), 4096); err != nil {
				t.Fatal(err)
			}
			base := probe.MetadataWorkUsedForTesting()
			want, wantErr := json.Marshal(native)
			raw, release, gotErr := probe.metadataAggregateEncodeOwned(t.Context(), stored)
			if wantErr != nil {
				if release != nil {
					release()
				}
				var original *json.MarshalerError
				var codec *metadataAggregateCodecError
				if !errors.As(wantErr, &original) || !errors.As(gotErr, &codec) || raw != nil ||
					probe.MetadataWorkUsedForTesting() != base {
					t.Fatal("malformed raw changed codec classification or leaked its result", wantErr, gotErr)
				}
			} else {
				if gotErr != nil || release == nil || !bytes.Equal(raw, want) {
					if release != nil {
						release()
					}
					t.Fatal("stored publication differs from the original encoder", gotErr)
				}
				charge := scaleP21SW205ByteCharge(len(raw))
				if probe.MetadataWorkUsedForTesting() != base+charge {
					release()
					t.Fatal("publication retained frame/scratch work beyond its result lease")
				}
				again, closeAgain, err := probe.metadataAggregateEncodeOwned(t.Context(), stored)
				if err != nil || closeAgain == nil || !bytes.Equal(again, want) {
					release()
					if closeAgain != nil {
						closeAgain()
					}
					t.Fatal("encoding consumed its borrowed source", err)
				}
				closeAgain()
				release()
				release()
				if probe.MetadataWorkUsedForTesting() != base {
					t.Fatal("result release changed the caller's existing ownership")
				}
			}
			after, err := stored.value.Value(t.Context(), metadataWorkBytes)
			if name == "fixed-offset-time" {
				nativeAfter, marshalErr := json.Marshal(native)
				if !errors.Is(err, ErrBoundedMetadataCoverage) || marshalErr != nil || !bytes.Equal(nativeAfter, want) {
					t.Fatal("publication changed the named-zone value or native-read refusal", err, marshalErr)
				}
			} else if err != nil || !reflect.DeepEqual(after.Interface(), before.Interface()) ||
				!reflect.DeepEqual(native, before.Interface()) {
				t.Fatal("publication mutated its source or original value", err)
			}
			owner.Close()
			if probe.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("caller ownership was not released")
			}
		})
	}
	t.Run("cancelled", func(t *testing.T) {
		node, err := metadataAggregateChild(t.Context(), parent, "large-raw")
		if err != nil {
			t.Fatal(err)
		}
		for _, during := range []bool{false, true} {
			ctx, cancel := context.WithCancel(t.Context())
			var work context.Context = ctx
			if during {
				work = &scaleP21SW205CancelContext{Context: ctx, cancel: cancel, at: 3}
			} else {
				cancel()
			}
			raw, release, err := probe.metadataAggregateEncodeOwned(work, node)
			cancel()
			if release != nil {
				release()
			}
			if !errors.Is(err, context.Canceled) || raw != nil || probe.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("cancelled publication escaped or retained its result", during, err)
			}
		}
		raw, release, err := probe.metadataAggregateEncodeOwned(t.Context(), node)
		if err != nil || release == nil || len(raw) <= 300<<10 {
			if release != nil {
				release()
			}
			t.Fatal("cancellation consumed the caller's source", err)
		}
		release()
	})
	if err := source.Retain(); err != nil {
		t.Fatal("publication closed the caller's source", err)
	}
	source.Release()
	if !reflect.DeepEqual(source.fields, fields) || source.Stamp() != stamp || source.refs != refs ||
		!reflect.DeepEqual(storage.types, types) || !reflect.DeepEqual(storage.times, times) ||
		!reflect.DeepEqual(storage.locations, locations) || storage.bytes != storageBytes || storage.refs != storageRefs ||
		metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors ||
		probe.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("publication changed source, type/time registry or resource lifetime")
	}
}
