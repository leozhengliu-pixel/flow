package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// This fixture supplies callback-native dropped fields that ordinary persisted
// capture correctly zeros already. Its bodies still use stored typed frames.
type scaleP21SW96NativeView struct {
	base  metadataLogicalNode
	value reflect.Value
}

func (n scaleP21SW96NativeView) nodeType() reflect.Type           { return n.base.typ }
func (n scaleP21SW96NativeView) logicalBase() metadataLogicalNode { return n.base }
func (n scaleP21SW96NativeView) nodeValue(context.Context, int) (reflect.Value, error) {
	return reflect.Value{}, ErrBoundedMetadataCoverage
}
func (n scaleP21SW96NativeView) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	return n.base.Children(ctx, func(key reflect.Value, child metadataLogicalNode) error {
		var native reflect.Value
		switch n.value.Kind() {
		case reflect.Struct:
			field, found := n.value.Type().FieldByName(key.String())
			if !found {
				return ErrMetadataRecordQuery
			}
			native = n.value.FieldByIndex(field.Index)
			if field.Tag.Get("json") == "-" && field.PkgPath == "" {
				return visit(key, metadataAggregateNativeNode{value: native})
			}
		case reflect.Pointer, reflect.Interface:
			native = n.value.Elem()
		case reflect.Slice, reflect.Array:
			native = n.value.Index(int(key.Int()))
		default:
			return ErrMetadataRecordQuery
		}
		return visit(key, scaleP21SW96NativeNode(child, native))
	})
}
func (n scaleP21SW96NativeView) logicalMapEntries(ctx context.Context, visit func(metadataAggregateNode, metadataAggregateNode) error) error {
	return n.base.MapEntries(ctx, func(key, child metadataLogicalNode) error {
		nativeKey, err := key.Value(ctx, 2<<20)
		if err != nil {
			return err
		}
		native := n.value.MapIndex(nativeKey)
		if !native.IsValid() {
			return ErrMetadataRecordQuery
		}
		return visit(metadataAggregateStoredNode{value: key}, scaleP21SW96NativeNode(child, native))
	})
}

func scaleP21SW96NativeNode(base metadataLogicalNode, value reflect.Value) metadataAggregateNode {
	if value.Type() == reflect.TypeFor[time.Time]() {
		return metadataAggregateStoredNode{value: base}
	}
	switch value.Kind() {
	case reflect.Struct, reflect.Map, reflect.Slice, reflect.Array, reflect.Interface, reflect.Pointer:
		if value.Kind() == reflect.Slice && value.Type().Elem().Kind() == reflect.Uint8 {
			return metadataAggregateStoredNode{value: base}
		}
		return scaleP21SW96NativeView{base: base, value: value}
	default:
		return metadataAggregateStoredNode{value: base}
	}
}

func scaleP21SW96Source(t *testing.T, data domain.Bootstrap) *metadataLogicalSource {
	t.Helper()
	base, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	defer base.Release()
	value := reflect.ValueOf(data)
	source, err := base.Transform(t.Context(), func(name string, node metadataLogicalNode) (metadataAggregateNode, bool, error) {
		index, _, found := metadataLogicalField(name)
		if !found {
			return nil, false, ErrMetadataRecordQuery
		}
		return scaleP21SW96NativeNode(node, value.Field(index)), true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(source.Release)
	return source
}

func scaleP21SW96Value(t *testing.T, source *metadataLogicalSource) domain.Bootstrap {
	t.Helper()
	var data domain.Bootstrap
	target := reflect.ValueOf(&data).Elem()
	err := source.VisitFields(t.Context(), func(name string, node metadataLogicalNode) error {
		if name == "-" {
			return nil
		}
		index, _, found := metadataLogicalField(name)
		if !found {
			return ErrMetadataRecordQuery
		}
		value, err := node.Value(t.Context(), 32<<20)
		if err != nil {
			return err
		}
		target.Field(index).Set(value)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestScaleP21SW96LogicalCloneLiteral(t *testing.T) {
	type key struct {
		ID      string
		Ignored string `json:"-"`
	}
	type value struct {
		User    domain.User
		Dropped string `json:"-"`
		Kept    string `json:"-,omitempty"`
	}
	at := time.Now()
	via := &domain.ActorBot{ID: "native-via", Type: domain.ActorBotTypeOAuthApplication, Name: "Native actor", OnBehalfOfID: "person"}
	user := domain.User{ID: "native-user", Name: "Native\x00name", Via: via}
	native := value{User: user, Dropped: "drop me", Kept: "keep exact tag semantics"}
	keyValue := key{ID: "map-key", Ignored: "key must survive"}
	data := domain.Bootstrap{
		Viewer:    user,
		Users:     []domain.User{user, {ID: "", Via: via}, {ID: "nil"}},
		Documents: []domain.Document{{ID: "document", Creator: user, ContentData: map[string]any{"user": user}}},
		Settings: map[string]any{
			"direct":              user,
			"pointer":             &user,
			"slice":               []domain.User{user, {ID: "other", Via: via}},
			"array":               [2]domain.User{user, {ID: "other", Via: via}},
			"array-interface":     [2]any{native, map[string]any{"nested": []any{&user}}},
			"map":                 map[string]domain.User{"native": user},
			"native-keys":         map[key]value{keyValue: native},
			"struct":              native,
			"native-time":         at,
			"native-time-pointer": &at,
			"native-times-array":  [2]time.Time{at, at.Add(time.Second)},
			"nil":                 nil,
			"nil-slice":           []domain.User(nil),
			"empty-slice":         []domain.User{},
			"nil-map":             map[string]domain.User(nil),
			"empty-map":           map[string]domain.User{},
		},
	}
	disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
	source := scaleP21SW96Source(t, data)
	before := scaleP21SW96Value(t, source)
	if !reflect.DeepEqual(before, data) {
		t.Fatal("native fixture lost a dropped field before the clone boundary")
	}
	cloned, err := cloneMetadataLogicalSnapshot(t.Context(), source)
	if err != nil {
		source.Release()
		t.Fatal(err)
	}
	source.Release()
	got := scaleP21SW96Value(t, cloned)
	want := cloneBootstrap(data)
	if !reflect.DeepEqual(got, want) || got.Viewer.Via != nil || got.Users[0].Via != nil {
		cloned.Release()
		t.Fatal("logical clone differs from literal cloneBootstrap")
	}
	if !reflect.DeepEqual(got.Settings["native-keys"], want.Settings["native-keys"]) {
		cloned.Release()
		t.Fatal("struct map key was cloned as a value")
	}
	if got.Settings["native-time"].(time.Time) != at || *got.Settings["native-time-pointer"].(*time.Time) != at {
		cloned.Release()
		t.Fatal("native timestamp identity or monotonic data changed")
	}
	got.Documents[0].ContentData["user"] = "caller mutation"
	got.Users[0].Name = "caller mutation"
	again := scaleP21SW96Value(t, cloned)
	if !reflect.DeepEqual(again, want) {
		cloned.Release()
		t.Fatal("materialized clone aliased another selection")
	}
	cloned.Release()
	if metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors {
		t.Fatal("clone/source release retained a frame owner")
	}
}

func TestScaleP21SW96LogicalCloneNativeTimeCoverage(t *testing.T) {
	type droppedTime struct {
		At   time.Time `json:"-"`
		Kept string
	}
	unknown := time.Date(2026, 11, 14, 1, 2, 3, 4, time.FixedZone("unknown native producer", 7*3600+123))
	known := time.Now()
	via := &domain.ActorBot{ID: "via"}
	data := domain.Bootstrap{Settings: map[string]any{
		"unknown":      unknown,
		"known":        known,
		"user":         domain.User{ID: "native", Via: via},
		"dropped-time": droppedTime{At: unknown, Kept: "retained"},
	}}
	source := scaleP21SW96Source(t, data)
	defer source.Release()
	index, _, _ := metadataLogicalField("settings")
	before := source.fields[index]
	cloned, err := cloneMetadataLogicalSnapshot(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	defer cloned.Release()
	after := cloned.fields[index]
	if !slices.Equal(before.source.types, after.source.types) ||
		!slices.Equal(before.source.times, after.source.times) ||
		!slices.Equal(before.source.locations, after.source.locations) {
		t.Fatal("rewritten field replaced native registries")
	}
	lookup := func(root metadataLogicalNode, name string) metadataLogicalNode {
		t.Helper()
		var selected metadataLogicalNode
		if err := root.MapEntries(t.Context(), func(key, value metadataLogicalNode) error {
			match, err := key.StringEqual(t.Context(), name)
			if err == nil && match {
				selected = value
			}
			return err
		}); err != nil || selected.typ == nil {
			t.Fatal("missing time", name, err)
		}
		return selected
	}
	oldTime, newTime := lookup(before, "unknown"), lookup(after, "unknown")
	_, oldErr := oldTime.Value(t.Context(), 4096)
	_, newErr := newTime.Value(t.Context(), 4096)
	if !errors.Is(oldErr, ErrBoundedMetadataCoverage) || !errors.Is(newErr, ErrBoundedMetadataCoverage) {
		t.Fatal("unknown native time gained or lost availability", oldErr, newErr)
	}
	oldWire, oldErr := oldTime.WireValue(t.Context(), 4096)
	newWire, newErr := newTime.WireValue(t.Context(), 4096)
	if oldErr != nil || newErr != nil || !reflect.DeepEqual(oldWire.Interface(), newWire.Interface()) {
		t.Fatal("unknown time's existing wire receipt changed", oldErr, newErr)
	}
	dropped, err := lookup(after, "dropped-time").Value(t.Context(), 4096)
	if err != nil || !reflect.DeepEqual(dropped.Interface(), droppedTime{Kept: "retained"}) {
		t.Fatal("dropping an unknown native time retained its unavailable flag", err)
	}
}

type scaleP21SW96CancelContext struct {
	context.Context
	cancel context.CancelFunc
	calls  atomic.Int64
	after  int64
}

func (ctx *scaleP21SW96CancelContext) Err() error {
	if ctx.calls.Add(1) >= ctx.after {
		ctx.cancel()
	}
	return ctx.Context.Err()
}

func TestScaleP21SW96LogicalCloneCancellationAndLargeBodies(t *testing.T) {
	var retainedDescriptors int64
	for _, scale := range []int{1, 10} {
		body := strings.Repeat("large native body\x00", scale*100000)
		via := &domain.ActorBot{ID: "via"}
		data := domain.Bootstrap{
			Viewer:    domain.User{ID: "native", Name: body, Via: via},
			Documents: []domain.Document{{ID: "untouched", Content: body}},
			Settings: map[string]any{
				"array":     [1]domain.User{{ID: "native", Name: body, Via: via}},
				"interface": map[string]any{"native": domain.User{ID: "native", Name: body, Via: via}},
			},
		}
		disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
		source := scaleP21SW96Source(t, data)
		beforeDisk, beforeDescriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if clone, err := cloneMetadataLogicalSnapshot(ctx, source); clone != nil || !errors.Is(err, context.Canceled) ||
			metadataLogicalDiskUsed.Load() != beforeDisk || metadataLogicalDescriptorsUsed.Load() != beforeDescriptors {
			source.Release()
			t.Fatal("pre-canceled clone allocated or retained a source", err)
		}
		parent, cancel := context.WithCancel(t.Context())
		mid := &scaleP21SW96CancelContext{Context: parent, cancel: cancel, after: 100}
		if clone, err := cloneMetadataLogicalSnapshot(mid, source); clone != nil || !errors.Is(err, context.Canceled) ||
			metadataLogicalDiskUsed.Load() != beforeDisk || metadataLogicalDescriptorsUsed.Load() != beforeDescriptors {
			cancel()
			source.Release()
			t.Fatal("mid-transform cancellation retained partial frames", err)
		}
		cancel()
		cloned, err := cloneMetadataLogicalSnapshot(t.Context(), source)
		if err != nil {
			source.Release()
			t.Fatal(err)
		}
		held := metadataLogicalDescriptorsUsed.Load() - beforeDescriptors
		if retainedDescriptors == 0 {
			retainedDescriptors = held
		}
		if held != retainedDescriptors {
			t.Fatal("10x body growth changed clone descriptor work", held, retainedDescriptors)
		}
		documentIndex, _, _ := metadataLogicalField("documents")
		if cloned.fields[documentIndex] != source.fields[documentIndex] {
			t.Fatal("body with no dropped descendants lost its shared owner")
		}
		viewerIndex, _, _ := metadataLogicalField("viewer")
		name, err := issueHotField(t.Context(), cloned.fields[viewerIndex], "Name")
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.New()
		if err := name.StreamBytes(t.Context(), func(input io.Reader) error {
			_, err := io.Copy(hash, input)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		expected := sha256.New()
		if _, err := io.Copy(expected, strings.NewReader(body)); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(hash.Sum(nil), expected.Sum(nil)) {
			t.Fatal("rewritten native body bytes changed")
		}
		dropped, err := issueHotField(t.Context(), cloned.fields[viewerIndex], "Via")
		if err != nil {
			t.Fatal(err)
		}
		value, err := dropped.Value(t.Context(), 4096)
		if err != nil || !value.IsNil() {
			t.Fatal("huge native record retained Via", err)
		}
		source.Release()
		if err := name.StreamBytes(t.Context(), func(input io.Reader) error {
			_, err := io.Copy(io.Discard, input)
			return err
		}); err != nil {
			t.Fatal("clone lost retained native storage", err)
		}
		cloned.Release()
		if metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors {
			t.Fatal("large clone retained storage after release")
		}
		t.Logf("body_scale=%d retained_clone_descriptors=%d", scale, held)
	}
	if clone, err := cloneMetadataLogicalSnapshot(t.Context(), nil); clone != nil || !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatal("nil clone source accepted", err)
	}
	closed, err := captureMetadataLogicalSource(t.Context(), domain.Bootstrap{}, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	closed.Release()
	if clone, err := cloneMetadataLogicalSnapshot(t.Context(), closed); clone != nil || !errors.Is(err, errMetadataLogicalClosed) {
		t.Fatal("closed clone source accepted", err)
	}
}

func TestScaleP21SW96LogicalCloneWarmReload(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	for _, reload := range []bool{false, true} {
		if reload {
			if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
		}
		if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
			t.Fatal(err)
		}
		parent, _, err := view.BeginInstalledMetadataRead(t.Context(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		actor := f.data.Viewer
		actor.Via = &domain.ActorBot{ID: "application", Type: domain.ActorBotTypeOAuthApplication, Name: "Native attribution"}
		image, err := view.prepareLegacyActorImage(t.Context(), parent.source, &actor)
		parent.Close()
		if err != nil {
			t.Fatal(err)
		}
		want := cloneBootstrap(scaleP21SW96Value(t, image.Source()))
		cloned, err := cloneMetadataLogicalSnapshot(t.Context(), image.Source())
		image.Close()
		if err != nil {
			t.Fatal(err)
		}
		got := scaleP21SW96Value(t, cloned)
		cloned.Release()
		if !reflect.DeepEqual(got, want) || got.Viewer.Via != nil || view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("warm/reloaded logical clone changed literal values or leaked")
		}
	}
}
