package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

type ScaleP21SW38Embedded struct {
	Promoted string `json:"promoted,omitempty"`
}

type scaleP21SW38Shape struct {
	ScaleP21SW38Embedded
	Body    string         `json:"body"`
	Absent  string         `json:"absent,omitempty"`
	Ignore  string         `json:"-"`
	Count   int64          `json:"count"`
	Float   float32        `json:"float"`
	NegZero float64        `json:"negativeZero"`
	Nil     []string       `json:"nil"`
	Empty   []string       `json:"empty"`
	Bytes   []byte         `json:"bytes"`
	Array   [2]string      `json:"array"`
	Values  map[string]any `json:"values"`
	When    *time.Time     `json:"when"`
}

func scaleP21SW38Legacy(value any) any {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var result any
	if json.Unmarshal(raw, &result) != nil {
		return nil
	}
	return result
}

func TestScaleP21SW38ProxyDifferential(t *testing.T) {
	when := time.Date(2026, 11, 13, 1, 2, 3, 123456789, time.FixedZone("offset", 3600))
	marker := strings.Repeat(".", metadataAggregateProxyString+1) + ":0"
	long := strings.Repeat("abc\x00<&\u2028\U0001f44b\xff\xfe", 1500)
	shape := scaleP21SW38Shape{
		ScaleP21SW38Embedded: ScaleP21SW38Embedded{Promoted: long},
		Body:                 long, Ignore: strings.Repeat("hidden", 10000), Count: 9007199254740993,
		Float: 1.23456789, NegZero: math.Copysign(0, -1), Empty: []string{},
		Bytes: bytes.Repeat([]byte{0xff, 0, '.'}, 5000), Array: [2]string{marker, long},
		Values: map[string]any{"\xff": "invalid", "\uFFFD": "valid", "marker": marker, "raw": json.RawMessage(nil)},
		When:   &when,
	}
	for _, value := range []any{
		shape, &shape, map[int]any{-1: long, 2: shape}, []any{shape, nil, []string{}, long},
		map[string]any{"invalid": strings.Repeat("\xff\xfe", 3000), "ascii": strings.Repeat("x", 5000)},
		struct{ Empty json.RawMessage }{Empty: json.RawMessage{}},
		struct{ Time time.Time }{Time: when}, math.NaN(),
	} {
		node := metadataAggregateNativeNode{reflect.ValueOf(value)}
		profile, plain, err := metadataAggregateJSONProfile(t.Context(), node, 0)
		if err != nil || !plain {
			t.Fatalf("%T profile=%+v plain=%t err=%v", value, profile, plain, err)
		}
		plan := metadataAggregatePlan{chunked: true, WinnerBytes: metadataWorkBytes}
		got, err := plan.normalized(t.Context(), node)
		if err != nil {
			t.Fatal(err)
		}
		want := scaleP21SW38Legacy(value)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%T normalized value differs from standard JSON round trip", value)
		}
		raw, err := metadataAggregateMarshalNormalized(t.Context(), got)
		if err != nil {
			t.Fatal(err)
		}
		expected, err := json.Marshal(want)
		if err != nil || !bytes.Equal(raw, expected) {
			t.Fatalf("%T fixed encoder differs: err=%v", value, err)
		}
		if uint64(len(raw)) > profile.encoded && want != nil {
			t.Fatalf("%T encoded bound=%d actual=%d", value, profile.encoded, len(raw))
		}
		proxy := metadataAggregateJSONProxy{}
		masked, err := proxy.value(t.Context(), reflect.ValueOf(value), 0)
		if err != nil {
			t.Fatal(err)
		}
		shell, err := json.Marshal(masked.Interface())
		if err == nil && uint64(len(shell)) > profile.shell {
			t.Fatalf("%T shell bound=%d actual=%d", value, profile.shell, len(shell))
		}
	}
}

type scaleP21SW38Custom string

func (scaleP21SW38Custom) MarshalJSON() ([]byte, error) { return []byte(`{"custom":true}`), nil }

type scaleP21SW38UnexportedEmbedded struct{ Value string }

type scaleP21SW38UnexportedShape struct {
	scaleP21SW38UnexportedEmbedded
}

func TestScaleP21SW38ProfileFallback(t *testing.T) {
	for _, value := range []any{
		scaleP21SW38Custom("x"), json.RawMessage(`{"raw":true}`), json.Number("42"),
		struct {
			Value int `json:",string"`
		}{Value: 42},
		map[string]any{strings.Repeat("key", 5000): "large-key"},
		scaleP21SW38UnexportedShape{scaleP21SW38UnexportedEmbedded{Value: strings.Repeat("x", 8000)}},
	} {
		_, plain, err := metadataAggregateJSONProfile(t.Context(), metadataAggregateNativeNode{reflect.ValueOf(value)}, 0)
		if err != nil || plain {
			t.Fatalf("%T must preserve conservative legacy normalization: plain=%t err=%v", value, plain, err)
		}
	}
}

func TestScaleP21SW38FixedEncoderBoundaries(t *testing.T) {
	for _, length := range []int{0, 1, 4095, 4096, 16382, 16383, 16384, 16385, 32768} {
		for _, suffix := range []string{"\U0001f44b\u2028<&\x00\n\t", "\xff\xfe\x80\x80", "\xe2\x82", "\xc0\x80"} {
			value := map[string]any{"value": strings.Repeat("a", length) + suffix, "neg": math.Copysign(0, -1), "array": []any{true, nil, float64(1e100)}}
			want, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			got, err := metadataAggregateMarshalNormalized(t.Context(), value)
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("length=%d suffix=%q: fixed JSON differs, err=%v", length, suffix, err)
			}
		}
	}
	deep := json.RawMessage(strings.Repeat("[", 200) + "null" + strings.Repeat("]", 200))
	plan := &metadataAggregatePlan{}
	for _, payload := range []json.RawMessage{nil, []byte("{"), []byte("null"), []byte("7"), []byte(`{"entity":"old","deleted":false}`), deep} {
		for _, event := range []string{"entry.updated", "entry.deleted"} {
			entity := map[string]any{"body": strings.Repeat("<", 5000)}
			got, err := plan.Realtime(t.Context(), payload, entity, event)
			want := enrichRealtimePayload(payload, entity, event)
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("realtime payload length=%d event=%s differs: err=%v", len(payload), event, err)
			}
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := metadataAggregateMarshalNormalized(ctx, map[string]any{"value": "x"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled encoder: %v", err)
	}
}

func TestScaleP21SW38PublicationLargeNative(t *testing.T) {
	source, err := captureMetadataLogicalSource(t.Context(), domain.Bootstrap{}, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	for _, size := range []int{8 << 20, 14 << 20} {
		for _, pattern := range []string{"x", "\x00"} {
			t.Run(strings.ReplaceAll(pattern, "\x00", "nul")+"-"+strconv.Itoa(size/(1<<20)), func(t *testing.T) {
				old := map[string]any{"id": "sw38-large", "body": strings.Repeat(pattern, size)}
				next := map[string]any{"id": "sw38-large", "body": strings.Repeat(pattern, size-1) + "y"}
				before := domain.Bootstrap{Documents: []domain.Document{{ID: "outer", ContentData: old}}}
				after := domain.Bootstrap{Documents: []domain.Document{{ID: "outer", ContentData: next}}}
				scope := MetadataRecordScope{Fields: []string{"documents"}}
				plan, err := planMetadataAggregateWork(t.Context(), metadataAggregateImage{Source: source, Selected: &before, Scope: scope}, metadataAggregateImage{Source: source, Selected: &after, Scope: scope}, "sw38-large", true, 0)
				if err != nil {
					t.Fatalf("legal %d-byte winner rejected: %v", size, err)
				}
				defer plan.Close()
				if !plan.chunked || plan.RequiredBytes > metadataWorkBytes {
					t.Fatalf("plan lacks bounded normalization: %+v", plan)
				}
				runtime.GC()
				var start, end runtime.MemStats
				runtime.ReadMemStats(&start)
				previous, entity, err := plan.Values(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				realtime, err := plan.Realtime(t.Context(), json.RawMessage(`{}`), entity, "entry.updated")
				if err != nil {
					t.Fatal(err)
				}
				runtime.ReadMemStats(&end)
				runtime.KeepAlive(previous)
				runtime.KeepAlive(realtime)
				t.Logf("body=%d pattern=%q required=%d alloc=%d retained-delta=%d previous=%d realtime=%d", size, pattern, plan.RequiredBytes, end.TotalAlloc-start.TotalAlloc, int64(end.HeapAlloc)-int64(start.HeapAlloc), len(previous), len(realtime))
				if end.TotalAlloc-start.TotalAlloc > uint64(plan.RequiredBytes) {
					t.Fatalf("publication allocation exceeded admission without relying on collection: allocation=%d allowance=%d", end.TotalAlloc-start.TotalAlloc, plan.RequiredBytes)
				}
				if len(previous) == 0 || len(realtime) < size || !bytes.Contains(realtime, []byte(`"entity"`)) {
					t.Fatal("publication lost previous or next winner")
				}
				var got any
				if err := json.Unmarshal(previous, &got); err != nil || !reflect.DeepEqual(got, map[string]any{"body": old["body"]}) {
					t.Fatalf("large previous values differ: %v", err)
				}
			})
		}
	}
}

func TestScaleP21SW38PublicationDenseContainers(t *testing.T) {
	items := make([]any, 10000)
	for i := range items {
		items[i] = map[string]any{"tiny": true, "number": i}
	}
	value := map[string]any{"id": "dense-sw38", "items": items}
	node := metadataAggregateNativeNode{reflect.ValueOf(value)}
	profile, plain, err := metadataAggregateJSONProfile(t.Context(), node, 0)
	if err != nil || !plain || profile.generic < uint64(len(items))*1024 {
		t.Fatalf("dense container bound: profile=%+v plain=%t err=%v", profile, plain, err)
	}
	plan := metadataAggregatePlan{chunked: true, WinnerBytes: metadataWorkBytes}
	got, err := plan.normalized(t.Context(), node)
	if err != nil || !reflect.DeepEqual(got, scaleP21SW38Legacy(value)) {
		t.Fatalf("dense normalization differs: %v", err)
	}
	raw, err := metadataAggregateMarshalNormalized(t.Context(), got)
	want, marshalErr := json.Marshal(got)
	if err != nil || marshalErr != nil || !bytes.Equal(raw, want) {
		t.Fatalf("dense encoding differs: %v %v", err, marshalErr)
	}
}

func TestScaleP21SW38PublicationLargeStored(t *testing.T) {
	for _, packed := range []bool{false, true} {
		t.Run(strconv.FormatBool(packed), func(t *testing.T) {
			body := strings.Repeat("\x00", 14<<20)
			data := domain.Bootstrap{Documents: []domain.Document{{ID: "outer", ContentData: map[string]any{
				"id": "sw38-stored-large", "body": body,
			}}}}
			source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{})
			if err != nil {
				t.Fatal(err)
			}
			if packed {
				indexed, err := metadataLogicalUnitsImport(t.Context(), source)
				source.Release()
				if err != nil {
					t.Fatal(err)
				}
				source = indexed
			}
			defer func() {
				if source != nil {
					source.Release()
				}
			}()
			plan, err := planMetadataAggregatePayload(t.Context(), metadataAggregateImage{Source: source}, metadataAggregateImage{Source: source}, "sw38-stored-large", true, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer plan.Close()
			if plan.previous {
				t.Fatal("identical deterministic stored winner retained redundant previous normalization")
			}
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			previous, entity, err := plan.Values(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			realtime, err := plan.Realtime(t.Context(), nil, entity, "entry.updated")
			if err != nil {
				t.Fatal(err)
			}
			runtime.ReadMemStats(&after)
			runtime.KeepAlive(realtime)
			t.Logf("packed=%t required=%d allocated=%d realtime=%d", packed, plan.RequiredBytes, after.TotalAlloc-before.TotalAlloc, len(realtime))
			if !packed && after.TotalAlloc-before.TotalAlloc > uint64(plan.RequiredBytes) {
				t.Fatalf("stored publication exceeded admission without relying on collection: allocation=%d allowance=%d", after.TotalAlloc-before.TotalAlloc, plan.RequiredBytes)
			}
			// SQL Scan copies one 32 KiB driver chunk at a time; cumulative
			// allocation grows with bytes read but only the fixed two-slot
			// cache and current chunk remain reachable from the reader.
			runtime.GC()
			var retained runtime.MemStats
			runtime.ReadMemStats(&retained)
			runtime.KeepAlive(entity)
			runtime.KeepAlive(realtime)
			t.Logf("packed=%t retained-delta=%d", packed, int64(retained.HeapAlloc)-int64(before.HeapAlloc))
			if int64(retained.HeapAlloc)-int64(before.HeapAlloc) > int64(plan.RequiredBytes) {
				t.Fatal("simultaneously retained stored publication exceeded admission")
			}
			if len(previous) != 0 || len(realtime) <= len(body) || entity.(map[string]any)["body"] != body {
				t.Fatal("stored publication lost original winner")
			}
			// Decoder-owned strings remain valid after their source is closed.
			source.Release()
			source = nil
			if entity.(map[string]any)["body"] != body {
				t.Fatal("owned decoded winner aliases released source storage")
			}
		})
	}
}

func TestScaleP21SW38StoredProxyDifferential(t *testing.T) {
	body := strings.Repeat("a", 32767) + "\U0001f44b\xff\xfe" + strings.Repeat("\x80", 32769)
	value := map[string]any{"id": "sw38-stored", "body": body, "integer": int64(9007199254740993), "data": []byte{0, 0xff}}
	data := domain.Bootstrap{Documents: []domain.Document{{ID: "outer", ContentData: value}}}
	source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	node, err := metadataAggregateResolve(t.Context(), metadataAggregateImage{Source: source}, "sw38-stored")
	if err != nil {
		t.Fatal(err)
	}
	profile, plain, err := metadataAggregateJSONProfile(t.Context(), node, 0)
	if err != nil || !plain {
		t.Fatalf("stored profile=%+v plain=%t err=%v", profile, plain, err)
	}
	native, nativePlain, err := metadataAggregateJSONProfile(t.Context(), metadataAggregateNativeNode{reflect.ValueOf(value)}, 0)
	if err != nil || !nativePlain || native != profile {
		t.Fatalf("stored/native profile differs: %+v / %+v err=%v", profile, native, err)
	}
	plan := metadataAggregatePlan{chunked: true, WinnerBytes: metadataWorkBytes}
	got, err := plan.normalized(t.Context(), node)
	if err != nil || !reflect.DeepEqual(got, scaleP21SW38Legacy(value)) {
		t.Fatalf("stored normalization differs: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := metadataAggregateJSONProfile(ctx, node, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled stored profile: %v", err)
	}
	if _, err := plan.normalized(ctx, node); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled stored normalization: %v", err)
	}
}

func TestScaleP21SW38PublicationPackedChangedLifetime(t *testing.T) {
	images := make([]metadataAggregateImage, 0, 2)
	body := strings.Repeat("\x00", 14<<20)
	for _, value := range []string{body, body[:len(body)-1] + "y"} {
		data := domain.Bootstrap{Documents: []domain.Document{{ID: "outer", ContentData: map[string]any{
			"id": "sw38-packed-change", "body": value,
		}}}}
		source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{})
		if err != nil {
			t.Fatal(err)
		}
		packed, err := metadataLogicalUnitsImport(t.Context(), source)
		source.Release()
		if err != nil {
			t.Fatal(err)
		}
		defer packed.Release()
		images = append(images, metadataAggregateImage{Source: packed})
	}
	plan, err := planMetadataAggregatePayload(t.Context(), images[0], images[1], "sw38-packed-change", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Close()
	runtime.GC()
	var baseline runtime.MemStats
	runtime.ReadMemStats(&baseline)
	measure := func(phase string, retained ...any) {
		t.Helper()
		runtime.GC()
		var current runtime.MemStats
		runtime.ReadMemStats(&current)
		for _, value := range retained {
			runtime.KeepAlive(value)
		}
		delta := int64(current.HeapAlloc) - int64(baseline.HeapAlloc)
		t.Logf("phase=%s required=%d retained-delta=%d allocated=%d", phase, plan.RequiredBytes, delta, current.TotalAlloc-baseline.TotalAlloc)
		if delta > int64(plan.RequiredBytes) {
			t.Fatalf("%s simultaneously retained graph exceeds reservation", phase)
		}
	}
	old, err := plan.normalized(t.Context(), plan.before)
	if err != nil {
		t.Fatal(err)
	}
	measure("old-normalized", old)
	next, err := plan.normalized(t.Context(), plan.after)
	if err != nil {
		t.Fatal(err)
	}
	measure("both-normalized", old, next)
	previous, err := metadataAggregateChunkedDiff(t.Context(), old, next)
	if err != nil {
		t.Fatal(err)
	}
	measure("diff-retains-old", old, next, previous)
	old = nil
	realtime, err := plan.Realtime(t.Context(), nil, next, "entry.updated")
	if err != nil {
		t.Fatal(err)
	}
	measure("both-publication-envelopes", next, previous, realtime)
	if len(previous) <= len(body) || len(realtime) <= len(body) || next.(map[string]any)["body"] != body[:len(body)-1]+"y" {
		t.Fatal("changed packed publication lost before/after values")
	}
}

type scaleP21SW38CancelProgress struct {
	context.Context
	calls int
}

func (c *scaleP21SW38CancelProgress) Err() error {
	c.calls++
	if c.calls >= 4 {
		return context.Canceled
	}
	return nil
}

func TestScaleP21SW38PublicationCancellationProgress(t *testing.T) {
	body := strings.Repeat("\x00", 1<<20)
	ctx := &scaleP21SW38CancelProgress{Context: t.Context()}
	if _, _, err := metadataAggregateStringSizesContext(ctx, body); !errors.Is(err, context.Canceled) || ctx.calls != 4 {
		t.Fatalf("long string profiling ignored cancellation: calls=%d err=%v", ctx.calls, err)
	}
	ctx = &scaleP21SW38CancelProgress{Context: t.Context()}
	writer := metadataAggregateFixedJSON{ctx: ctx, data: make([]byte, 6*len(body)+2)}
	if err := writer.quoted(body); !errors.Is(err, context.Canceled) || ctx.calls != 4 || writer.offset >= len(writer.data) {
		t.Fatalf("long string encoding ignored cancellation: calls=%d encoded=%d err=%v", ctx.calls, writer.offset, err)
	}
}

func TestScaleP21SW38PublicationProducerDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, test := range []struct {
		name, pattern string
		size          int
	}{
		{"ascii8", "x", 8 << 20},
		{"nul14", "\x00", 14 << 20},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := scaleP09Database(t)
			workspace := f.data.Workspace.URLKey
			target, document := idgen.New("sw38-target-"), idgen.New("document-sw38-")
			body := strings.Repeat(test.pattern, test.size)
			reset := func() {
				t.Helper()
				f.SetWebhookSink(nil)
				f.SetRealtimeSink(nil)
				if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
				if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "settings", "documents", "webhooks"), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
					data.Settings["sw38-target"] = "before"
					data.Documents = []domain.Document{{
						ID: document,
						ContentData: map[string]any{
							"id": target, "body": body, "integer": int64(9007199254740993),
							"hidden": map[string]any{"actor": f.data.Viewer.ID, "count": int64(17), "team": "private-team"},
						},
					}}
					data.Webhooks = []domain.Webhook{{ID: "hook-sw38", Enabled: true, URL: "https://example.test/sw38"}}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			type outcome struct {
				previous, snapshot, realtime json.RawMessage
				target, foreign              string
			}
			read := func(field, key string) string {
				t.Helper()
				var raw string
				if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?`, workspace, field, key).Scan(&raw); err != nil {
					t.Fatal(err)
				}
				return raw
			}
			var outcomes []outcome
			for _, bounded := range []bool{false, true} {
				reset()
				repo := f.SQLiteStore
				if bounded {
					var err error
					repo, err = f.BoundedMetadataViewForTesting(t.Context())
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(repo.releaseMetadataLogicalSources)
				}
				got := outcome{}
				held := func() int {
					if repo.metadataResidency == nil {
						return 0
					}
					repo.metadataResidency.work.mu.Lock()
					defer repo.metadataResidency.work.mu.Unlock()
					return repo.metadataResidency.work.used
				}
				ctx, cancel := context.WithCancel(t.Context())
				repo.SetWebhookSink(func(_ string, event domain.DomainEvent) {
					got.previous, got.snapshot = event.PreviousValues, event.Snapshot
					if bounded && held() < len(event.Snapshot)+test.size {
						t.Errorf("webhook work not held: held=%d encoded=%d body=%d", held(), len(event.Snapshot), test.size)
					}
					cancel()
				})
				repo.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
					got.realtime = event.Payload
					if ctx.Err() != context.Canceled {
						t.Error("late webhook cancellation missing")
					}
					if bounded && held() < len(event.Payload)+test.size {
						t.Errorf("realtime work not held: held=%d encoded=%d body=%d", held(), len(event.Payload), test.size)
					}
				})
				foreignBefore := read("documents", document)
				runtime.GC()
				var memoryBefore, memoryAfter runtime.MemStats
				runtime.ReadMemStats(&memoryBefore)
				err := repo.MutateWorkspaceScoped(ctx, workspace, "application_policy.updated", target, map[string]any{"value": "after"},
					MetadataRecordScope{Write: []RecordKey{{Kind: "settings", ID: "sw38-target"}}}, func(data *domain.Bootstrap) error {
						if bounded && len(data.Documents) != 0 {
							t.Error("scoped settings callback received unrelated documents")
						}
						data.Settings["sw38-target"] = "after"
						return nil
					})
				runtime.ReadMemStats(&memoryAfter)
				cancel()
				if err != nil {
					t.Fatalf("bounded=%t body=%d: %v", bounded, test.size, err)
				}
				got.target, got.foreign = read("settings", "sw38-target"), read("documents", document)
				if got.foreign != foreignBefore {
					t.Fatal("small scoped mutation changed unrelated canonical document")
				}
				if bounded && (held() != 0 || repo.WorkspaceMetadataDecodes() != 0) {
					t.Fatal("bounded producer leaked work or hydrated full metadata")
				}
				if len(got.snapshot) < test.size || len(got.realtime) < test.size {
					t.Fatal("producer dropped legal large warm winner")
				}
				t.Logf("bounded=%t body=%d snapshot=%d previous=%d realtime=%d alloc=%d heap-delta=%d", bounded, test.size, len(got.snapshot), len(got.previous), len(got.realtime), memoryAfter.TotalAlloc-memoryBefore.TotalAlloc, int64(memoryAfter.HeapAlloc)-int64(memoryBefore.HeapAlloc))
				outcomes = append(outcomes, got)
			}
			if !bytes.Equal(outcomes[0].previous, outcomes[1].previous) || !bytes.Equal(outcomes[0].snapshot, outcomes[1].snapshot) ||
				!bytes.Equal(outcomes[0].realtime, outcomes[1].realtime) || outcomes[0].target != outcomes[1].target || outcomes[0].foreign != outcomes[1].foreign {
				t.Fatal("large publication differs from literal flag-off producer")
			}
		})
	}
}
