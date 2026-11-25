package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// This inventory deliberately overapproximates callback reachability: every
// production API codec must be reviewed, including helpers returning any, local
// aliases, embedded types and producers reached through SDK/MCP/import paths.
// A Bootstrap-only field walk cannot see concrete values assigned to interfaces.
// Existing transport-only decoders are separately identified below; they are
// never allowances for codecs encountered in the metadata type walk.
type scaleP21ProducerCodec struct {
	receiver, signature string
}

func scaleP21ProducerCodecKey(pkg, receiver, method string) string {
	return pkg + "." + receiver + "." + method
}

func scaleP21ProducerSourceCodecs(path string, file *ast.File) []scaleP21ProducerCodec {
	var result []scaleP21ProducerCodec
	for _, declaration := range file.Decls {
		method, ok := declaration.(*ast.FuncDecl)
		if !ok || method.Recv == nil || len(method.Recv.List) != 1 ||
			(method.Name.Name != "MarshalJSON" && method.Name.Name != "UnmarshalJSON") {
			continue
		}
		var receiver bytes.Buffer
		_ = format.Node(&receiver, token.NewFileSet(), method.Recv.List[0].Type)
		// Parameter names do not affect a method's signature. Preserve pointer
		// receivers and exact argument/result types rather than matching names.
		signature := *method.Type
		normalize := func(fields *ast.FieldList) *ast.FieldList {
			if fields == nil {
				return nil
			}
			copy := &ast.FieldList{}
			for _, field := range fields.List {
				for range max(1, len(field.Names)) {
					copy.List = append(copy.List, &ast.Field{Type: field.Type})
				}
			}
			return copy
		}
		signature.Params, signature.Results = normalize(signature.Params), normalize(signature.Results)
		var rendered bytes.Buffer
		_ = format.Node(&rendered, token.NewFileSet(), &signature)
		result = append(result, scaleP21ProducerCodec{
			receiver:  scaleP21ProducerCodecKey(filepath.ToSlash(filepath.Dir(path)), receiver.String(), method.Name.Name),
			signature: rendered.String(),
		})
	}
	return result
}

func scaleP21ProducerSourceProblems(codecs []scaleP21ProducerCodec, allowed map[string]string) []string {
	var problems []string
	for _, codec := range codecs {
		want, ok := allowed[codec.receiver]
		if !ok || codec.signature != want {
			problems = append(problems, codec.receiver+" "+codec.signature)
		}
	}
	sort.Strings(problems)
	return problems
}

// Walk both method sets, including promoted methods. An alias retains its
// original identity; a new defined type or embedding with promoted codecs must
// earn its own review. Follow function results as well as ordinary fields so
// callback-valued metadata fields cannot hide statically named payload types.
func scaleP21ProducerTypeProblems(roots []reflect.Type, allowed map[reflect.Type]bool) ([]string, map[reflect.Type]bool) {
	seen := map[reflect.Type]bool{}
	var problems []string
	var visit func(reflect.Type)
	visit = func(typ reflect.Type) {
		if seen[typ] {
			return
		}
		seen[typ] = true
		for _, candidate := range []reflect.Type{typ, reflect.PointerTo(typ)} {
			for _, contract := range []reflect.Type{reflect.TypeFor[json.Marshaler](), reflect.TypeFor[json.Unmarshaler]()} {
				if candidate.Implements(contract) && !allowed[candidate] {
					problems = append(problems, candidate.String()+" implements "+contract.String())
				}
			}
		}
		// These are opaque leaves with explicitly reviewed implementations.
		// In particular, walking time.Time's private Location graph is neither
		// the reader's representation nor part of a producer's JSON payload.
		if typ == reflect.TypeFor[time.Time]() || typ == reflect.TypeFor[json.RawMessage]() {
			return
		}
		switch typ.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Chan:
			visit(typ.Elem())
		case reflect.Map:
			visit(typ.Key())
			visit(typ.Elem())
		case reflect.Struct:
			for i := 0; i < typ.NumField(); i++ {
				field := typ.Field(i)
				if field.PkgPath == "" {
					visit(field.Type)
				}
			}
		case reflect.Func:
			for i := 0; i < typ.NumIn(); i++ {
				visit(typ.In(i))
			}
			for i := 0; i < typ.NumOut(); i++ {
				visit(typ.Out(i))
			}
		case reflect.Interface:
			for i := 0; i < typ.NumMethod(); i++ {
				visit(typ.Method(i).Type)
			}
		}
	}
	for _, root := range roots {
		visit(root)
	}
	sort.Strings(problems)
	return problems, seen
}

type scaleP21ProducerUnsupported struct{ Hidden string }

func (scaleP21ProducerUnsupported) MarshalJSON() ([]byte, error) { return []byte(`"opaque"`), nil }

type scaleP21ProducerUnsupportedDecode struct{ Hidden string }

func (*scaleP21ProducerUnsupportedDecode) UnmarshalJSON([]byte) error { return nil }

func TestScaleP21SupportedProducerCodecs(t *testing.T) {
	const marshal = "func() ([]byte, error)"
	const unmarshal = "func([]byte) error"
	allowedSource := map[string]string{
		// Exact native reconstruction retains named types and their contents.
		// AgentToolCall still sanitizes partial Arguments/Result at encoding;
		// ActivityMetadata still converts only the two label-delta strings;
		// OptionalInt still distinguishes the request's Set and Value state.
		"internal/domain.AgentToolCall.MarshalJSON":       marshal,
		"internal/domain.ActivityMetadata.MarshalJSON":    marshal,
		"internal/domain.*ActivityMetadata.UnmarshalJSON": unmarshal,
		"internal/domain.OptionalInt.MarshalJSON":         marshal,
		"internal/domain.*OptionalInt.UnmarshalJSON":      unmarshal,
		// Event payloads are encoded after callbacks, outside logical metadata
		// reconstruction. This wrapper exposes its complete Input/Child graph
		// to the bounded payload work estimator before its ordinary encoder.
		"cmd/server.childEventPayload.MarshalJSON": marshal,
		// Transport/configuration-only decoders, not logical metadata codecs:
		// SCIM manager claims live in claims_json; ModelInfo reads provider
		// configuration; the remaining two decode transient chat responses.
		"internal/store.*SCIMManager.UnmarshalJSON":  unmarshal,
		"internal/config.*ModelInfo.UnmarshalJSON":   unmarshal,
		"cmd/server.*chatStreamDelta.UnmarshalJSON":  unmarshal,
		"cmd/server.*agentLenientText.UnmarshalJSON": unmarshal,
	}
	allowedTypes := map[reflect.Type]bool{}
	for _, typ := range []reflect.Type{
		reflect.TypeFor[time.Time](), reflect.TypeFor[json.RawMessage](),
		reflect.TypeFor[domain.AgentToolCall](), reflect.TypeFor[domain.ActivityMetadata](),
		reflect.TypeFor[domain.OptionalInt](),
	} {
		allowedTypes[typ], allowedTypes[reflect.PointerTo(typ)] = true, true
	}

	t.Run("production source inventory", func(t *testing.T) {
		// Relative to the package directory, independently of the checkout name.
		root := filepath.Join("..", "..")
		found := map[string]bool{}
		var codecs []scaleP21ProducerCodec
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if entry.Name() == "vendor" || strings.HasPrefix(entry.Name(), ".") && path != root {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				return err
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			for _, codec := range scaleP21ProducerSourceCodecs(relative, file) {
				found[codec.receiver] = true
				codecs = append(codecs, codec)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if problems := scaleP21ProducerSourceProblems(codecs, allowedSource); len(problems) != 0 {
			t.Fatalf("review new producer codecs against the bounded logical reader (do not blanket-allow):\n%s", strings.Join(problems, "\n"))
		}
		for key := range allowedSource {
			if !found[key] {
				t.Errorf("stale codec review: %s", key)
			}
		}
	})

	t.Run("metadata type closure", func(t *testing.T) {
		problems, seen := scaleP21ProducerTypeProblems([]reflect.Type{reflect.TypeFor[domain.Bootstrap]()}, allowedTypes)
		if len(problems) != 0 {
			t.Fatalf("unsupported reachable metadata codecs:\n%s", strings.Join(problems, "\n"))
		}
		for _, typ := range []reflect.Type{
			reflect.TypeFor[time.Time](), reflect.TypeFor[json.RawMessage](),
			reflect.TypeFor[domain.AgentToolCall](), reflect.TypeFor[domain.ActivityMetadata](),
		} {
			if !seen[typ] {
				t.Errorf("metadata closure no longer exercises reviewed codec %v", typ)
			}
		}
	})

	t.Run("synthetic source additions fail closed", func(t *testing.T) {
		for _, declaration := range []string{
			"type Added struct{}; func (Added) MarshalJSON() ([]byte,error) { return nil,nil }",
			"type Added struct{}; func (*Added) UnmarshalJSON([]byte) error { return nil }",
			// An allowed receiver does not authorize a newly added method or a
			// different receiver/signature on its existing method.
			"func (*AgentToolCall) UnmarshalJSON([]byte) error { return nil }",
			"func (*AgentToolCall) MarshalJSON() ([]byte,error) { return nil,nil }",
			"func (AgentToolCall) MarshalJSON(string) ([]byte,error) { return nil,nil }",
		} {
			file, err := parser.ParseFile(token.NewFileSet(), "synthetic.go", "package domain; "+declaration, 0)
			if err != nil {
				t.Fatal(err)
			}
			codecs := scaleP21ProducerSourceCodecs("internal/domain/synthetic.go", file)
			if len(codecs) != 1 || len(scaleP21ProducerSourceProblems(codecs, allowedSource)) != 1 {
				t.Errorf("guard accepted synthetic codec: %s", declaration)
			}
		}
	})

	t.Run("synthetic aliases nesting embedding and callbacks", func(t *testing.T) {
		type alias = scaleP21ProducerUnsupported
		type nested struct{ Values map[string][]*alias }
		type embedded struct{ scaleP21ProducerUnsupported }
		for _, typ := range []reflect.Type{
			reflect.TypeFor[nested](), reflect.TypeFor[embedded](),
			reflect.TypeFor[func() *scaleP21ProducerUnsupportedDecode](),
		} {
			if problems, _ := scaleP21ProducerTypeProblems([]reflect.Type{typ}, allowedTypes); len(problems) == 0 {
				t.Errorf("guard accepted unsupported codec through %v", typ)
			}
		}
	})

	t.Run("reviewed codecs preserve native values and wire behavior", func(t *testing.T) {
		number := 17
		for name, input := range map[string]any{
			"time": time.Date(2026, 11, 14, 1, 2, 3, 123456789, time.UTC),
			"raw":  json.RawMessage(`{ "number":9007199254740993,"items":[null,true] }`),
			"partial tool": domain.AgentToolCall{
				Arguments: json.RawMessage(`{"unfinished":`), Result: json.RawMessage(`not json`),
			},
			"activity labels":  domain.ActivityMetadata{"addedLabelIds": `["one","two"]`, "title": "before"},
			"activity invalid": domain.ActivityMetadata{"removedLabelIds": `not json`},
			"optional value":   domain.OptionalInt{Set: true, Value: &number},
			"optional null":    domain.OptionalInt{Set: true},
		} {
			t.Run(name, func(t *testing.T) {
				source, err := captureMetadataLogicalSource(t.Context(), domain.Bootstrap{
					Settings: map[string]any{"producer": input},
				}, copyStamp{})
				if err != nil {
					t.Fatal(err)
				}
				defer source.Release()
				index, _, _ := metadataLogicalField("settings")
				node := source.fields[index]
				detached, err := node.Value(t.Context(), int(node.cost))
				if err != nil {
					t.Fatal(err)
				}
				output := detached.Interface().(map[string]any)["producer"]
				if !reflect.DeepEqual(input, output) {
					t.Fatalf("native producer changed: %#v != %#v", input, output)
				}
				want, wantErr := json.Marshal(input)
				got, gotErr := json.Marshal(output)
				if !bytes.Equal(want, got) || fmt.Sprint(wantErr) != fmt.Sprint(gotErr) {
					t.Fatalf("codec wire behavior changed: %s (%v) != %s (%v)", want, wantErr, got, gotErr)
				}
				// Unmarshal only after encoding, as SQL/HTTP producers do. The
				// reconstructed named type must select the same decoder too.
				if wantErr == nil {
					old, next := reflect.New(reflect.TypeOf(input)), reflect.New(reflect.TypeOf(output))
					oldErr, nextErr := json.Unmarshal(want, old.Interface()), json.Unmarshal(got, next.Interface())
					if !reflect.DeepEqual(old.Interface(), next.Interface()) || fmt.Sprint(oldErr) != fmt.Sprint(nextErr) {
						t.Fatal("codec decoder behavior changed")
					}
				}
			})
		}
	})
}
