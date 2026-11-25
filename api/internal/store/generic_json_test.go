package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func scaleP21SW248Repo(t *testing.T) *SQLiteStore {
	t.Helper()
	t.Setenv("FLOW_BOUNDED_METADATA", "1")
	disk, descriptors, spools := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load(), recordSpoolBytes.Load()
	repo := &SQLiteStore{
		workspaces: map[string]domain.Bootstrap{"sw248": {}}, lastWorkspaceKey: "sw248",
		metadataResidency: newMetadataResidency(),
	}
	t.Cleanup(func() {
		repo.releaseMetadataLogicalSources()
		if repo.MetadataWorkUsedForTesting() != 0 || repo.WorkspaceMetadataDecodes() != 0 ||
			metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors ||
			recordSpoolBytes.Load() != spools {
			t.Error("generic JSON frame retained work/disk/descriptors or decoded a full workspace")
		}
	})
	return repo
}

func scaleP21SW248Builder(t *testing.T) *metadataLogicalBuilder {
	t.Helper()
	builder, err := newMetadataLogicalBuilder(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(builder.Close)
	return builder
}

func scaleP21SW248Oracle(t *testing.T, typ reflect.Type, raw string) (reflect.Value, []byte) {
	t.Helper()
	value := reflect.New(typ)
	if err := json.Unmarshal([]byte(raw), value.Interface()); err != nil {
		t.Fatal("invalid standard JSON oracle fixture", err)
	}
	wire, err := json.Marshal(value.Elem().Interface())
	if err != nil {
		t.Fatal(err)
	}
	return value.Elem(), wire
}

func scaleP21SW248Frame(t *testing.T, repo *SQLiteStore, builder *metadataLogicalBuilder, typ reflect.Type, raw string) metadataLogicalNode {
	t.Helper()
	before := repo.MetadataWorkUsedForTesting()
	node, err := builder.writeGenericJSON(t.Context(), repo, typ, legacySessionJSONPart{bytes.NewReader([]byte(raw)), 0, int64(len(raw))})
	if err != nil {
		t.Fatal("supported normalized generic JSON must frame successfully", err)
	}
	if node.Type() != typ || repo.MetadataWorkUsedForTesting() != before {
		t.Fatal("constructor changed the declared type or retained its temporary native graph")
	}
	return node
}

func scaleP21SW248AssertValue(t *testing.T, repo *SQLiteStore, node metadataLogicalNode, want reflect.Value, wire []byte) {
	t.Helper()
	owner := &ResourceRead{store: repo}
	defer owner.Close()
	if err := owner.reserve(t.Context(), node.cost); err != nil {
		t.Fatal(err)
	}
	for _, read := range []func(context.Context, int) (reflect.Value, error){node.Value, node.WireValue} {
		value, err := read(t.Context(), metadataWorkBytes)
		if err != nil || value.Type() != want.Type() || !reflect.DeepEqual(value.Interface(), want.Interface()) {
			t.Fatal("generic JSON changed standard native type/null/container/value semantics", err, node.Type(), want.Type())
		}
		if value.Kind() == reflect.Float64 && math.Signbit(value.Float()) != math.Signbit(want.Float()) {
			t.Fatal("generic JSON normalization lost the sign of zero")
		}
		if repo.MetadataWorkUsedForTesting() == 0 {
			t.Fatal("returned native graph outlived its caller's admission owner")
		}
	}
	if node.cost == 0 {
		t.Fatal("generic JSON leaf has no materialization cost")
	}
	if _, err := node.Value(t.Context(), int(node.cost)-1); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("generic materialization bypassed the caller's size cap", err)
	}
	spool, err := legacySessionNodeSpool(t.Context(), owner, node)
	if err != nil {
		t.Fatal("stream generic JSON", err)
	}
	defer spool.Close()
	var actual bytes.Buffer
	if _, err := io.Copy(&actual, io.NewSectionReader(spool, 0, spool.bytes)); err != nil || !bytes.Equal(actual.Bytes(), wire) {
		t.Fatal("generic wire output differs from standard normalized JSON", err)
	}
}

func scaleP21SW248Walk(t *testing.T, node metadataLogicalNode, expected reflect.Value) {
	t.Helper()
	type pair struct {
		node  metadataLogicalNode
		value reflect.Value
	}
	stack := []pair{{node, expected}}
	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if current.node.Type() != current.value.Type() {
			t.Fatal("generic child lost its exact declared interface/container type", current.node.Type(), current.value.Type())
		}
		count := 0
		err := current.node.Children(t.Context(), func(key reflect.Value, child metadataLogicalNode) error {
			count++
			var want reflect.Value
			switch current.value.Kind() {
			case reflect.Interface:
				if current.value.IsNil() || key.IsValid() {
					t.Fatal("nil interface produced a child or dynamic child acquired an index")
				}
				want = current.value.Elem()
			case reflect.Map:
				if !key.IsValid() || key.Kind() != reflect.String {
					t.Fatal("generic object exposed a non-string key")
				}
				want = current.value.MapIndex(key)
			case reflect.Slice:
				want = current.value.Index(int(key.Int()))
			default:
				t.Fatal("generic scalar exposed a child")
			}
			if !want.IsValid() {
				t.Fatal("generic traversal invented an object key")
			}
			stack = append(stack, pair{child, want})
			return nil
		})
		if err != nil {
			t.Fatal("generic child traversal failed at supported JSON depth", err)
		}
		wantCount := 0
		switch current.value.Kind() {
		case reflect.Interface:
			if !current.value.IsNil() {
				wantCount = 1
			}
		case reflect.Map, reflect.Slice:
			wantCount = current.value.Len()
			nilValue, err := current.node.isNil(t.Context())
			if err != nil || nilValue != current.value.IsNil() {
				t.Fatal("generic traversal lost nil versus empty container", err)
			}
		case reflect.String:
			same, err := current.node.StringEqual(t.Context(), current.value.String())
			if err != nil || !same {
				t.Fatal("generic string lookup changed normalized decoded bytes", err)
			}
		case reflect.Float64:
			value, err := current.node.Value(t.Context(), metadataWorkBytes)
			if err != nil || value.Float() != current.value.Float() || math.Signbit(value.Float()) != math.Signbit(current.value.Float()) {
				t.Fatal("generic scalar lost standard float64 conversion/sign", err)
			}
		}
		if count != wantCount {
			t.Fatal("generic child count differs from standard decoded value", count, wantCount)
		}
		if current.value.Kind() == reflect.Map {
			seen := map[string]bool{}
			if err := current.node.MapEntries(t.Context(), func(key, child metadataLogicalNode) error {
				value, err := key.Value(t.Context(), metadataWorkBytes)
				if err != nil {
					return err
				}
				name := value.String()
				if seen[name] || !current.value.MapIndex(value).IsValid() || child.Type() != reflect.TypeFor[any]() {
					t.Fatal("generic map entries changed key uniqueness or interface wrapping")
				}
				seen[name] = true
				return nil
			}); err != nil || len(seen) != current.value.Len() {
				t.Fatal("generic map-entry view differs", err)
			}
		}
	}
}

func TestScaleP21SW248GenericJSONStdlibTypesAndChildren(t *testing.T) {
	if metadataLogicalDepth != 128 {
		t.Fatal("generic JSON support changed the ordinary typed codec depth bound")
	}
	deep := strings.Repeat(`{"next":[`, 256) + `{"id":"deep","n":-0,"empty":{},"null":null}` + strings.Repeat(`]}`, 256)
	for _, test := range []struct {
		name, raw string
		typ       reflect.Type
	}{
		{"nil-map", `null`, reflect.TypeFor[map[string]any]()},
		{"empty-map", `{}`, reflect.TypeFor[map[string]any]()},
		{"nil-array", `null`, reflect.TypeFor[[]any]()},
		{"empty-array", `[]`, reflect.TypeFor[[]any]()},
		{"nil-interface", `null`, reflect.TypeFor[any]()},
		{"interface-object", `{"x":{}}`, reflect.TypeFor[any]()},
		{"interface-array", `[null,{},[],true,"s",1]`, reflect.TypeFor[any]()},
		{"ordinary-number", `9007199254740993`, reflect.TypeFor[float64]()},
		{"negative-zero", `-0`, reflect.TypeFor[float64]()},
		{"bool", `true`, reflect.TypeFor[bool]()},
		{"string", `"a\u0000<>&\u2028\ud800"`, reflect.TypeFor[string]()},
		{"repeated-decoded-keys", `{"same":{"old":1},"same":{"new":2},"a":1,"\u0061":3,"null":null,"map":{},"array":[],"n":1e-7}`, reflect.TypeFor[map[string]any]()},
		{"deep-map", deep, reflect.TypeFor[map[string]any]()},
		{"deep-array", `[` + deep + `]`, reflect.TypeFor[[]any]()},
		{"deep-interface", deep, reflect.TypeFor[any]()},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := scaleP21SW248Repo(t)
			builder := scaleP21SW248Builder(t)
			node := scaleP21SW248Frame(t, repo, builder, test.typ, test.raw)
			oracle, wire := scaleP21SW248Oracle(t, test.typ, test.raw)
			scaleP21SW248AssertValue(t, repo, node, oracle, wire)
			owner := &ResourceRead{store: repo}
			defer owner.Close()
			if err := owner.reserve(t.Context(), node.cost); err != nil {
				t.Fatal(err)
			}
			scaleP21SW248Walk(t, node, oracle)
		})
	}
}

func TestScaleP21SW248GenericJSONSupportedLargeDeepSuccess(t *testing.T) {
	const blocks, textBytes = 256, 3 << 20
	document := `{"type":"doc","content":[` +
		strings.Repeat(`{"type":"blockquote","content":[`, blocks) +
		`{"type":"paragraph","content":[{"type":"text","text":"` +
		strings.Repeat("x", textBytes) + `"}]}` +
		strings.Repeat(`]}`, blocks) + `]}`
	request := []byte(`{"descriptionData":` + document + `}`)
	if len(document) != 3154517 || len(request) != 3154537 || len(request) >= 16<<20 {
		t.Fatal("large/deep witness changed its supported HTTP shape or byte limit", len(document), len(request))
	}
	var input domain.IssueUpdateInput
	decoder := json.NewDecoder(bytes.NewReader(request))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		t.Fatal("supported HTTP descriptionData must decode successfully", err)
	}
	expected, err := json.Marshal(input.DescriptionData)
	if err != nil {
		t.Fatal("supported HTTP ContentData must marshal completely", err)
	}
	if metadataLogicalDepth != 128 || metadataWorkBytes != 256<<20 {
		t.Fatal("large/deep support changed an ordinary depth or production work quota")
	}
	repo := scaleP21SW248Repo(t)
	if repo.metadataResidency.work.limit != metadataWorkBytes {
		t.Fatal("large/deep default-success fixture changed the process work limit")
	}
	origin := scaleP21SW248Builder(t)
	if _, err := origin.writeValue(reflect.ValueOf("large-deep-input-offset"), 0, false); err != nil {
		t.Fatal(err)
	}
	node := scaleP21SW248Frame(t, repo, origin, reflect.TypeFor[map[string]any](), document)
	oracle := reflect.ValueOf(input.DescriptionData)
	// A work-budget refusal is a failure, even if every representation
	// returns the same error. Native and complete wire output must succeed.
	scaleP21SW248AssertValue(t, repo, node, oracle, expected)
	var copies []metadataLogicalNode
	for _, mode := range []string{"copy", "import"} {
		target := scaleP21SW248Builder(t)
		if _, err := target.writeValue(reflect.ValueOf(strings.Repeat(mode+"-different-offset", 5)), 0, false); err != nil {
			t.Fatal(err)
		}
		var copied metadataLogicalNode
		var err error
		if mode == "copy" {
			copied, err = target.copyNode(node)
		} else {
			target.storage.types = []reflect.Type{reflect.TypeFor[[]string]()}
			copied, err = target.importNode(node, 0)
		}
		if err != nil {
			t.Fatal("legal large/deep generic copy/import must succeed", mode, err)
		}
		copies = append(copies, copied)
	}
	origin.Close()
	for _, copied := range copies {
		scaleP21SW248AssertValue(t, repo, copied, oracle, expected)
	}
	if repo.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("successful large/deep value, wire or copy retained temporary native ownership")
	}
}

func TestScaleP21SW248GenericJSONEquality(t *testing.T) {
	deep := func(end string) string {
		return strings.Repeat(`{"next":[`, 256) + end + strings.Repeat(`]}`, 256)
	}
	for _, test := range []struct {
		name, left, right string
		typ               reflect.Type
		shallow           bool
	}{
		{"signed-zero", `-0`, `0`, reflect.TypeFor[float64](), true},
		{"nested-signed-zero", `{"n":-0,"a":[-0]}`, `{"a":[0],"n":0}`, reflect.TypeFor[map[string]any](), true},
		{"normalized-number", `1e0`, `1.0`, reflect.TypeFor[any](), true},
		{"last-key-wins", `{"x":0,"x":2}`, `{"x":2}`, reflect.TypeFor[map[string]any](), true},
		{"map-null-empty", `null`, `{}`, reflect.TypeFor[map[string]any](), true},
		{"array-null-empty", `null`, `[]`, reflect.TypeFor[[]any](), true},
		{"null-type-differs", `null`, `{}`, reflect.TypeFor[any](), true},
		{"array-order", `[1,2]`, `[2,1]`, reflect.TypeFor[[]any](), true},
		{"map-array", `{"x":[]}`, `{"x":{}}`, reflect.TypeFor[map[string]any](), true},
		{"deep-zero", deep(`{"n":-0}`), deep(`{"n":0}`), reflect.TypeFor[map[string]any](), false},
		{"deep-difference", deep(`{"n":1}`), deep(`{"n":2}`), reflect.TypeFor[map[string]any](), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := scaleP21SW248Repo(t)
			builder := scaleP21SW248Builder(t)
			a := scaleP21SW248Frame(t, repo, builder, test.typ, test.left)
			b := scaleP21SW248Frame(t, repo, builder, test.typ, test.right)
			left, leftWire := scaleP21SW248Oracle(t, test.typ, test.left)
			right, rightWire := scaleP21SW248Oracle(t, test.typ, test.right)
			want := reflect.DeepEqual(left.Interface(), right.Interface())
			if test.name == "signed-zero" && bytes.Equal(leftWire, rightWire) {
				t.Fatal("signed-zero fixture no longer distinguishes canonical bytes from value equality")
			}
			pairs := [][2]metadataLogicalNode{{a, b}}
			if test.shallow {
				native, err := builder.writeValue(right, 0, false)
				if err != nil {
					t.Fatal(err)
				}
				pairs = append(pairs, [2]metadataLogicalNode{a, native}, [2]metadataLogicalNode{native, a})
			}
			for _, pair := range pairs {
				for _, strict := range []bool{false, true} {
					same, err := legacyMetadataWriteEqualMode(t.Context(), pair[0], pair[1], 0, strict)
					if err != nil || same != want {
						t.Fatal("generic/typed equality differs from normalized native equality", strict, same, want, err)
					}
				}
			}
		})
	}
}

func scaleP21SW248Source(t *testing.T, builder *metadataLogicalBuilder, settings metadataLogicalNode, revision int64) *metadataLogicalSource {
	t.Helper()
	typ := reflect.TypeFor[domain.Bootstrap]()
	for index := 0; index < typ.NumField(); index++ {
		field := typ.Field(index)
		if field.Tag.Get("json") == "-" {
			continue
		}
		name := jsonFieldName(field)
		if name == "settings" {
			builder.source.fields[index] = settings
		} else if err := builder.Field(name, reflect.Zero(field.Type)); err != nil {
			t.Fatal(err)
		}
	}
	source, err := builder.Finish(copyStamp{workspaceID: "sw248", revision: revision})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(source.Release)
	return source
}

func scaleP21SW248Install(t *testing.T, repo *SQLiteStore, source *metadataLogicalSource) {
	t.Helper()
	if err := source.Retain(); err != nil {
		t.Fatal(err)
	}
	repo.installMetadataLogicalSource("sw248", source)
}

func TestScaleP21SW248GenericJSONCopyImportInstall(t *testing.T) {
	repo := scaleP21SW248Repo(t)
	raw := `{"deep":` + strings.Repeat(`{"n":[`, 256) + `{"v":-0,"null":null}` + strings.Repeat(`]}`, 256) + `}`
	typ := reflect.TypeFor[map[string]any]()
	oracle, wire := scaleP21SW248Oracle(t, typ, raw)
	input := scaleP21SW248Builder(t)
	if _, err := input.writeValue(reflect.ValueOf("input-padding"), 0, false); err != nil {
		t.Fatal(err)
	}
	node := scaleP21SW248Frame(t, repo, input, typ, raw)
	parent := t
	var copies []metadataLogicalNode
	for _, mode := range []string{"copy", "import", "aggregate"} {
		t.Run(mode, func(t *testing.T) {
			target := scaleP21SW248Builder(parent)
			// A foreign type registry must not reinterpret a generic range.
			target.storage.types = []reflect.Type{reflect.TypeFor[[]string](), reflect.TypeFor[map[string]int64]()}
			if _, err := target.writeValue(reflect.ValueOf(strings.Repeat("target-padding", 7)), 0, false); err != nil {
				t.Fatal(err)
			}
			var copied metadataLogicalNode
			var err error
			switch mode {
			case "copy":
				copied, err = target.copyNode(node)
			case "import":
				copied, err = target.importNode(node, 0)
			default:
				copied, err = target.writeAggregateNode(metadataAggregateStoredNode{node}, 0)
			}
			if err != nil {
				t.Fatal("deep generic leaf was recursively reframed or its registry changed", err)
			}
			scaleP21SW248AssertValue(t, repo, copied, oracle, wire)
			copies = append(copies, copied)
		})
	}
	target := scaleP21SW248Builder(t)
	if _, err := target.writeValue(reflect.ValueOf("different-install-offset"), 0, false); err != nil {
		t.Fatal(err)
	}
	copied, err := target.importNode(node, 0)
	if err != nil {
		t.Fatal(err)
	}
	source := scaleP21SW248Source(t, target, copied, 1)
	input.Close()
	for _, copied := range copies {
		scaleP21SW248AssertValue(t, repo, copied, oracle, wire)
	}
	scaleP21SW248Install(t, repo, source)
	pinned, err := repo.retainMetadataLogicalSource(t.Context(), "sw248")
	if err != nil {
		t.Fatal(err)
	}
	defer pinned.Release()
	nextBuilder := scaleP21SW248Builder(t)
	nextNode := scaleP21SW248Frame(t, repo, nextBuilder, typ, `{"generation":"next"}`)
	next := scaleP21SW248Source(t, nextBuilder, nextNode, 2)
	scaleP21SW248Install(t, repo, next)
	index, _, _ := metadataLogicalField("settings")
	scaleP21SW248AssertValue(t, repo, pinned.fields[index], oracle, wire)
	firstOwner := &ResourceRead{store: repo}
	if err := firstOwner.reserve(t.Context(), pinned.fields[index].cost); err != nil {
		t.Fatal(err)
	}
	first, err := pinned.fields[index].Value(t.Context(), metadataWorkBytes)
	if err != nil {
		firstOwner.Close()
		t.Fatal(err)
	}
	first.Interface().(map[string]any)["mutated"] = true
	first = reflect.Value{}
	firstOwner.Close()
	scaleP21SW248AssertValue(t, repo, pinned.fields[index], oracle, wire)
	generation := pinned
	if err := generation.Retain(); err != nil {
		t.Fatal(err)
	}
	defer func() { generation.Release() }()
	for range 12 {
		fork, err := generation.Transform(t.Context(), func(name string, node metadataLogicalNode) (metadataAggregateNode, bool, error) {
			return metadataAggregateStoredNode{node}, name == "settings", nil
		})
		if err != nil {
			t.Fatal("deep generic source transform failed", err)
		}
		generation.Release()
		generation = fork
		if len(generation.stores) > 3 {
			t.Fatal("generic copy retained a chain of prior source storage owners")
		}
	}
	scaleP21SW248AssertValue(t, repo, generation.fields[index], oracle, wire)
}

func TestScaleP21SW248GenericJSONNestedLookupAndOverlay(t *testing.T) {
	deep := func(body string) string {
		return strings.Repeat(`{"next":[`, 256) + body + strings.Repeat(`]}`, 256)
	}
	for _, test := range []struct{ name, raw, target string }{
		{"own-id-before-descendants", `{"id":"sw248-target","teamId":"allowed","child":` + deep(`{"id":"sw248-target","teamId":"hidden"}`) + `}`, "sw248-target"},
		{"first-array-descendant", `{"list":[` + deep(`{"id":"sw248-target","teamId":"hidden"}`) + `,{"id":"sw248-target","teamId":"allowed"}]}`, "sw248-target"},
		{"direct-team-precedence", `{"root":` + deep(`{"id":"sw248-target","teamId":"hidden","team":{"id":"allowed"},"teamIds":["allowed"]}`) + `}`, "sw248-target"},
		{"typed-terminal-prefix", `{"root":` + deep(`{"id":"team_resource_sw248","teamId":"allowed"}`) + `}`, "team_resource_sw248"},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := scaleP21SW248Repo(t)
			builder := scaleP21SW248Builder(t)
			node := scaleP21SW248Frame(t, repo, builder, reflect.TypeFor[map[string]any](), test.raw)
			source := scaleP21SW248Source(t, builder, node, 1)
			settings, _ := scaleP21SW248Oracle(t, reflect.TypeFor[map[string]any](), test.raw)
			data := domain.Bootstrap{Settings: settings.Interface().(map[string]any)}
			owner := &ResourceRead{store: repo}
			defer owner.Close()
			if err := owner.reserve(t.Context(), node.cost*8+64<<10); err != nil {
				t.Fatal(err)
			}
			expected := aggregateJSONValue(data, test.target)
			expectedRaw, err := json.Marshal(expected)
			if err != nil {
				t.Fatal(err)
			}
			var expectedJSON any
			if err := json.Unmarshal(expectedRaw, &expectedJSON); err != nil {
				t.Fatal(err)
			}
			got, err := metadataAggregateJSONValue(t.Context(), metadataAggregateImage{Source: source}, test.target, metadataWorkBytes)
			if err != nil || !reflect.DeepEqual(got, expectedJSON) {
				t.Fatal("typed nested aggregate/own-ID/prefix precedence changed", err)
			}
			scaleP21SW248Install(t, repo, source)
			read, found, err := repo.BeginWebhookAggregateRead(t.Context(), "sw248")
			if err != nil || !found {
				t.Fatal(found, err)
			}
			defer read.Close()
			for _, teams := range [][]string{nil, {"allowed"}, {"hidden"}, {"allowed", "hidden"}} {
				want := scaleP21WebhookJSONLiteral(t, data, test.target, teams)
				got, err := read.TeamsAllowed(t.Context(), test.target, teams)
				if err != nil || got != want {
					t.Fatal("JSON-view nested own-ID/team precedence changed", teams, got, want, err)
				}
			}
		})
	}
	t.Run("selected-point-before-retained-subtree", func(t *testing.T) {
		repo := scaleP21SW248Repo(t)
		builder := scaleP21SW248Builder(t)
		raw := `{"slot":{"id":"sw248-overlay","teamId":"hidden","nested":{"id":"sw248-overlay","teamId":"hidden"}},"unrelated":` + deep(`{"id":"different","teamId":"hidden"}`) + `}`
		node := scaleP21SW248Frame(t, repo, builder, reflect.TypeFor[map[string]any](), raw)
		source := scaleP21SW248Source(t, builder, node, 1)
		selected := domain.Bootstrap{Settings: map[string]any{
			"slot": map[string]any{"id": "sw248-overlay", "teamId": "allowed", "nested": map[string]any{"id": "sw248-overlay", "teamId": "hidden"}},
		}}
		scope := MetadataRecordScope{Write: []RecordKey{{Kind: "settings", ID: "slot"}}}
		owner := &ResourceRead{store: repo}
		defer owner.Close()
		if err := owner.reserve(t.Context(), node.cost*8+64<<10); err != nil {
			t.Fatal(err)
		}
		image := metadataAggregateImage{Source: source, Selected: &selected, Scope: scope}
		got, err := metadataAggregateJSONValue(t.Context(), image, "sw248-overlay", metadataWorkBytes)
		if err != nil || !reflect.DeepEqual(got, selected.Settings["slot"]) {
			t.Fatal("selected overlay lost own-ID precedence or searched the retained replaced record", err)
		}
		old, _ := scaleP21SW248Oracle(t, reflect.TypeFor[map[string]any](), raw)
		before := domain.Bootstrap{Settings: map[string]any{"slot": old.Interface().(map[string]any)["slot"]}}
		fork, err := source.Fork(t.Context(), before, selected, scope, copyStamp{workspaceID: "sw248", revision: 2})
		if err != nil {
			t.Fatal("point overlay could not copy unrelated deep generic leaves", err)
		}
		defer fork.Release()
		scaleP21SW248Install(t, repo, fork)
		read, found, err := repo.BeginWebhookAggregateRead(t.Context(), "sw248")
		if err != nil || !found {
			t.Fatal(found, err)
		}
		defer read.Close()
		for _, teams := range [][]string{{"allowed"}, {"hidden"}} {
			allowed, err := read.TeamsAllowed(t.Context(), "sw248-overlay", teams)
			if err != nil || allowed != (teams[0] == "allowed") {
				t.Fatal("installed overlay changed JSON-view own-ID precedence", teams, allowed, err)
			}
		}
	})
}

type scaleP21SW248ReaderAt func([]byte, int64) (int, error)

func (read scaleP21SW248ReaderAt) ReadAt(dst []byte, offset int64) (int, error) {
	return read(dst, offset)
}

type scaleP21SW248Writer func([]byte) (int, error)

func (write scaleP21SW248Writer) Write(data []byte) (int, error) { return write(data) }

func TestScaleP21SW248GenericJSONCancellationAndDenial(t *testing.T) {
	for _, moment := range []string{"before", "during-input"} {
		t.Run(moment, func(t *testing.T) {
			repo := scaleP21SW248Repo(t)
			builder := scaleP21SW248Builder(t)
			prior, err := builder.writeValue(reflect.ValueOf("prior"), 0, false)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			raw := []byte(`{"deep":` + strings.Repeat(`{"next":[`, 256) + `"tail"` + strings.Repeat(`]}`, 256) + `}`)
			reader := bytes.NewReader(raw)
			calls := 0
			input := scaleP21SW248ReaderAt(func(dst []byte, offset int64) (int, error) {
				calls++
				n, err := reader.ReadAt(dst, offset)
				cancel()
				return n, err
			})
			if moment == "before" {
				cancel()
			}
			before := builder.offset
			node, err := builder.writeGenericJSON(ctx, repo, reflect.TypeFor[map[string]any](), legacySessionJSONPart{input, 0, int64(len(raw))})
			if !errors.Is(err, context.Canceled) || node.Type() != nil || repo.MetadataWorkUsedForTesting() != 0 ||
				moment == "before" && (calls != 0 || builder.offset != before) ||
				moment == "during-input" && calls == 0 {
				t.Fatal("generic constructor cancellation exposed a node, read past cancellation or leaked ownership", calls, err)
			}
			value, err := prior.Value(t.Context(), 4096)
			if err != nil || value.String() != "prior" {
				t.Fatal("cancelled construction changed an earlier owned node", err)
			}
		})
	}
	t.Run("traversal-and-size-cap", func(t *testing.T) {
		repo := scaleP21SW248Repo(t)
		builder := scaleP21SW248Builder(t)
		node := scaleP21SW248Frame(t, repo, builder, reflect.TypeFor[map[string]any](), `{"a":{"x":1},"b":{"x":2}}`)
		ctx, cancel := context.WithCancel(t.Context())
		visits := 0
		err := node.MapEntries(ctx, func(_, _ metadataLogicalNode) error {
			visits++
			cancel()
			return nil
		})
		if !errors.Is(err, context.Canceled) || visits != 1 {
			t.Fatal("generic traversal ignored cancellation between children", visits, err)
		}
		if _, err := node.Value(ctx, metadataWorkBytes); !errors.Is(err, context.Canceled) || repo.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("cancelled generic value leaked decoded ownership", err)
		}
		if _, err := node.Value(t.Context(), int(node.cost)-1); !errors.Is(err, ErrMetadataWorkBudget) || repo.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("generic materialization denial acquired ownership", err)
		}
	})
	t.Run("constructor-budget-denial", func(t *testing.T) {
		repo := scaleP21SW248Repo(t)
		builder := scaleP21SW248Builder(t)
		ctx, release, err := repo.tryAdmitAdditionalMetadataWork(t.Context(), 4096)
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		before := repo.MetadataWorkUsedForTesting()
		limit := repo.metadataResidency.work.limit
		repo.metadataResidency.work.limit = before
		raw := `{"x":[]}`
		node, err := builder.writeGenericJSON(ctx, repo, reflect.TypeFor[map[string]any](), legacySessionJSONPart{strings.NewReader(raw), 0, int64(len(raw))})
		repo.metadataResidency.work.limit = limit
		if !errors.Is(err, ErrMetadataWorkBudget) || node.Type() != nil || repo.MetadataWorkUsedForTesting() != before {
			t.Fatal("denied generic construction changed its independent owner", err)
		}
	})
	t.Run("stream-output", func(t *testing.T) {
		repo := scaleP21SW248Repo(t)
		builder := scaleP21SW248Builder(t)
		raw := `{"body":"` + strings.Repeat("body", 64<<10) + `"}`
		node := scaleP21SW248Frame(t, repo, builder, reflect.TypeFor[map[string]any](), raw)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		writes := 0
		owner := &ResourceRead{store: repo}
		err := legacyProjectStreamNode(ctx, owner, node, scaleP21SW248Writer(func(data []byte) (int, error) {
			writes++
			cancel()
			return len(data), nil
		}))
		owner.Close()
		if !errors.Is(err, context.Canceled) || writes == 0 || repo.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("generic range streaming ignored cancellation or retained its owner", writes, err)
		}
	})
	t.Run("invalid-root-and-json", func(t *testing.T) {
		repo := scaleP21SW248Repo(t)
		for _, raw := range []string{`[]`, `true`, `{"x":`, `{"n":1e400}`} {
			builder := scaleP21SW248Builder(t)
			var oracle map[string]any
			if json.Unmarshal([]byte(raw), &oracle) == nil {
				t.Fatal("invalid generic map fixture was accepted by the standard decoder")
			}
			node, err := builder.writeGenericJSON(t.Context(), repo, reflect.TypeFor[map[string]any](), legacySessionJSONPart{strings.NewReader(raw), 0, int64(len(raw))})
			if err == nil || node.Type() != nil || repo.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("invalid generic map exposed a node or retained temporary ownership", err)
			}
		}
	})
}
