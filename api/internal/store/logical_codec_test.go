package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

type scaleP21SW29Integer int64
type scaleP21SW29Pointer *scaleP21SW29Integer
type scaleP21SW29Bytes []byte
type scaleP21SW29Key struct {
	Text string
	N    int64
}
type scaleP21SW29Graph struct {
	ID     string
	Hidden map[string]any `json:"-"`
	Values [3]any
	Map    map[scaleP21SW29Key]any
}

func TestScaleP21SW29LogicalCodecTypedRoundTrip(t *testing.T) {
	value := scaleP21SW29Integer(1<<60 + 19)
	named := scaleP21SW29Pointer(&value)
	instant := time.Now()
	var nilPointer *scaleP21SW29Integer
	var nilSlice scaleP21SW29Bytes
	values := []any{
		int(-19), int8(-8), int16(-190), int32(-1999), int64(-1 << 61),
		uint(19), uint8(250), uint16(65000), uint32(1 << 31), uint64(1<<64 - 1),
		uintptr(123), float32(1.25), float64(-19.5), complex64(1 + 2i), complex128(-19 + 18i),
		scaleP21SW29Integer(1<<60 + 29), named, nilPointer, nilSlice, scaleP21SW29Bytes{},
		scaleP21SW29Bytes{0, 255, 128}, map[string]any(nil), map[string]any{},
		[]any{nil, (*int)(nil), []string{}, map[string]any{"x": int64(1<<60 + 9)}},
		scaleP21SW29Graph{
			ID:     "native\x00\xff",
			Hidden: map[string]any{"must": "drop"},
			Values: [3]any{instant, instant.Add(17 * time.Nanosecond), scaleP21SW29Bytes{255, 0}},
			Map: map[scaleP21SW29Key]any{
				{Text: "\x00\xff", N: 7}: [2]any{named, "value\xff\x00"},
			},
		},
	}
	for i, value := range values {
		data := domain.Bootstrap{Settings: map[string]any{"graph": value}}
		expected := cloneBootstrap(data)
		source, err := captureMetadataLogicalSource(context.Background(), data, copyStamp{"codec", int64(i)})
		if err != nil {
			t.Fatalf("case %d capture: %v", i, err)
		}
		got, err := source.Select(context.Background(), MetadataRecordScope{Fields: []string{"settings"}}, 2<<20)
		source.Release()
		if err != nil {
			t.Fatalf("case %d read: %v", i, err)
		}
		if !reflect.DeepEqual(expected.Settings, got.Settings) {
			t.Fatalf("case %d typed round trip differs: got %#v want %#v", i, got.Settings, expected.Settings)
		}
	}
}

func TestScaleP21SW29LogicalCodecDeferredOpaque(t *testing.T) {
	ctx := context.Background()
	data := domain.Bootstrap{Settings: map[string]any{
		"safe":   int64(1<<60 + 7),
		"opaque": time.Date(2026, 11, 13, 1, 2, 3, 4, time.FixedZone("audited-only", 7331)),
	}}
	disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
	source, err := captureMetadataLogicalSource(ctx, data, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	safe, err := source.Select(ctx, MetadataRecordScope{Read: []RecordKey{{Kind: "settings", ID: "safe"}}}, 2<<20)
	if err != nil || safe.Settings["safe"] != data.Settings["safe"] {
		t.Fatalf("safe sibling: %#v %v", safe.Settings, err)
	}
	if _, err := source.Select(ctx, MetadataRecordScope{Fields: []string{"settings"}}, 2<<20); !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatalf("complete opaque field: %v", err)
	}
	if _, err := source.Select(ctx, MetadataRecordScope{Read: []RecordKey{{Kind: "settings", ID: "opaque"}}}, 2<<20); !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatalf("selected opaque record: %v", err)
	}
	next := domain.Bootstrap{Settings: map[string]any{"safe": "changed"}}
	fork, err := source.Fork(ctx, safe, next, MetadataRecordScope{Write: []RecordKey{{Kind: "settings", ID: "safe"}}}, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	source.Release()
	safe, err = fork.Select(ctx, MetadataRecordScope{Read: []RecordKey{{Kind: "settings", ID: "safe"}}}, 2<<20)
	if err != nil || safe.Settings["safe"] != "changed" {
		t.Fatalf("fork safe sibling: %#v %v", safe.Settings, err)
	}
	if _, err := fork.Select(ctx, MetadataRecordScope{Read: []RecordKey{{Kind: "settings", ID: "opaque"}}}, 2<<20); !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatalf("fork lost opaque hold: %v", err)
	}
	fork.Release()
	if metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors {
		t.Fatal("deferred opaque source leaked accounting")
	}
}

func TestScaleP21SW29LogicalCodecLargeUnrelatedKeys(t *testing.T) {
	ctx := context.Background()
	key := strings.Repeat("x", 2<<20)
	data := domain.Bootstrap{Settings: map[string]any{
		key: "unrelated", "safe": int64(7),
	}}
	source, err := captureMetadataLogicalSource(ctx, data, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	selected, err := source.Select(ctx, MetadataRecordScope{Read: []RecordKey{{Kind: "settings", ID: "safe"}}}, 2<<20)
	if err != nil || selected.Settings["safe"] != int64(7) {
		t.Fatalf("large unrelated key point: %#v %v", selected.Settings, err)
	}
	next := domain.Bootstrap{Settings: map[string]any{"safe": int64(9)}}
	fork, err := source.Fork(ctx, selected, next, MetadataRecordScope{Write: []RecordKey{{Kind: "settings", ID: "safe"}}}, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	defer fork.Release()
	selected, err = fork.Select(ctx, MetadataRecordScope{Read: []RecordKey{{Kind: "settings", ID: "safe"}}}, 2<<20)
	if err != nil || selected.Settings["safe"] != int64(9) {
		t.Fatalf("large unrelated key fork: %#v %v", selected.Settings, err)
	}
}

func TestScaleP21SW29LogicalCodecIncrementalBuilder(t *testing.T) {
	ctx := context.Background()
	data := domain.Bootstrap{
		Settings: map[string]any{"typed": scaleP21SW29Integer(1<<60 + 1)},
		Teams:    []domain.Team{{ID: "team_old", Name: "before\xff\x00"}},
	}
	b, err := newMetadataLogicalBuilder(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	value := reflect.ValueOf(data)
	for i := 0; i < value.NumField(); i++ {
		field := value.Type().Field(i)
		if field.Tag.Get("json") == "-" {
			continue
		}
		name := jsonFieldName(field)
		if name == "teams" {
			if err := b.BeginCollection(name, false); err != nil {
				t.Fatal(err)
			}
			if err := b.Append(reflect.Value{}, value.Field(i).Index(0)); err != nil {
				t.Fatal(err)
			}
			if err := b.EndCollection(); err != nil {
				t.Fatal(err)
			}
		} else if name == "settings" {
			if err := b.BeginCollection(name, false); err != nil {
				t.Fatal(err)
			}
			key := reflect.ValueOf("typed")
			if err := b.Append(key, value.Field(i).MapIndex(key)); err != nil {
				t.Fatal(err)
			}
			if err := b.EndCollection(); err != nil {
				t.Fatal(err)
			}
		} else if err := b.Field(name, value.Field(i)); err != nil {
			t.Fatal(err)
		}
	}
	source, err := b.Finish(copyStamp{"builder", 7})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	b.Close()
	if err := b.Field("teams", reflect.ValueOf([]domain.Team{})); err == nil {
		t.Fatal("finished builder changed a published source")
	}
	selected, err := source.Select(ctx, MetadataRecordScope{Fields: []string{"teams", "settings"}}, 2<<20)
	if err != nil || !reflect.DeepEqual(selected.Teams, data.Teams) || !reflect.DeepEqual(selected.Settings, data.Settings) {
		t.Fatalf("incremental typed source: %#v %#v %v", selected.Teams, selected.Settings, err)
	}
	scope := MetadataRecordScope{Write: []RecordKey{
		{Kind: "teams", ID: "team_old"}, {Kind: "teams", ID: "team_later"}, {Kind: "teams", ID: "team_first"},
	}}
	next := selected
	next.Teams = []domain.Team{
		{ID: "team_old", Name: "changed"}, {ID: "team_later", Name: "later"}, {ID: "team_first", Name: "first"},
	}
	fork, err := source.Fork(ctx, selected, next, scope, copyStamp{}, map[metadataRecordKey]int{
		{field: "teams", key: "team_later"}: 11, {field: "teams", key: "team_first"}: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer fork.Release()
	got, err := fork.Select(ctx, MetadataRecordScope{Fields: []string{"teams"}}, 2<<20)
	if err != nil || len(got.Teams) != 3 || got.Teams[0].ID != "team_old" || got.Teams[1].ID != "team_first" || got.Teams[2].ID != "team_later" {
		t.Fatalf("declared append order: %#v %v", got.Teams, err)
	}
}

func TestScaleP21SW29LogicalCodecDisplayTransform(t *testing.T) {
	ctx := context.Background()
	user := domain.User{ID: "usr_current", Name: "updated\x00\xff"}
	stale := user
	stale.Name = "stale"
	data := domain.Bootstrap{
		Users:    []domain.User{user},
		Projects: []domain.Project{{ID: "project_a", Creator: &stale}},
		Settings: map[string]any{"unrelated": strings.Repeat("body", 1<<20)},
	}
	expected := cloneBootstrap(data)
	refreshDisplayReferences(&expected)
	source, err := captureMetadataLogicalSource(ctx, data, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	transformed, err := source.Transform(ctx, func(name string, node metadataLogicalNode) (metadataAggregateNode, bool, error) {
		base := metadataAggregateStoredNode{node}
		changed := name == "projects"
		if changed {
			return metadataAggregateRefreshField(source, name, base), true, nil
		}
		return base, false, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer transformed.Release()
	got, err := transformed.Select(ctx, MetadataRecordScope{Fields: []string{"projects"}}, 2<<20)
	if err != nil || !reflect.DeepEqual(got.Projects, expected.Projects) {
		t.Fatalf("typed display source transform: %#v want %#v err %v", got.Projects, expected.Projects, err)
	}
	var originalSettings, transformedSettings metadataLogicalNode
	if err := source.VisitFields(ctx, func(name string, node metadataLogicalNode) error {
		if name == "settings" {
			originalSettings = node
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := transformed.VisitFields(ctx, func(name string, node metadataLogicalNode) error {
		if name == "settings" {
			transformedSettings = node
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if originalSettings.source != transformedSettings.source || originalSettings.offset != transformedSettings.offset {
		t.Fatal("display transform copied the unrelated settings source")
	}
}

func TestScaleP21SW29LogicalCodecForeignLargeUserTransform(t *testing.T) {
	ctx := context.Background()
	stale := domain.User{ID: "usr_display", Name: "old"}
	data := domain.Bootstrap{
		Users:    []domain.User{stale},
		Projects: []domain.Project{{ID: "project_display", Creator: &stale}},
	}
	source, err := captureMetadataLogicalSource(ctx, data, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	now := time.Now()
	current := domain.User{ID: stale.ID, Name: strings.Repeat("u", 4<<20), OutOfOfficeUntil: &now}
	next := data
	next.Users = []domain.User{current}
	updated, err := source.Fork(ctx, data, next, MetadataRecordScope{Fields: []string{"users"}}, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	defer updated.Release()
	transformed, err := updated.Transform(ctx, func(name string, node metadataLogicalNode) (metadataAggregateNode, bool, error) {
		base := metadataAggregateStoredNode{node}
		if name == "projects" {
			return metadataAggregateRefreshField(updated, name, base), true, nil
		}
		return base, false, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer transformed.Release()
	got, err := transformed.Select(ctx, MetadataRecordScope{Fields: []string{"projects"}}, 32<<20)
	if err != nil || len(got.Projects) != 1 || !reflect.DeepEqual(got.Projects[0].Creator, &current) {
		t.Fatalf("foreign large User transform failed: %v", err)
	}
}
