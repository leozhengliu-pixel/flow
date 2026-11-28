package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/format"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

const (
	jsonPayloadStore  = "flow/api/internal/store"
	jsonPayloadDomain = "flow/api/internal/domain"
	jsonPayloadServer = "flow/api/cmd/server"
)

// Audit the static shapes at metered JSON boundaries and the concrete values
// boxed into interfaces by the API. Decoded JSON is already limited to JSON
// values. Go values returned as any from dependencies still need source review.
type jsonPayloadPackage struct {
	ImportPath, Dir, Export string
	GoFiles                 []string
}

type jsonPayloadImporter func(string) (*types.Package, error)

func (f jsonPayloadImporter) Import(path string) (*types.Package, error) { return f(path) }

type jsonPayloadAudit struct {
	files      *token.FileSet
	info       types.Info
	marshal    []*types.Interface
	sources    []*ast.File
	shapes     map[types.Type]jsonPayloadShape
	local      map[string]*types.Package
	interfaces map[string]bool
	typeParams map[*types.TypeParam]bool
}

func newJSONPayloadAudit() *jsonPayloadAudit {
	return &jsonPayloadAudit{
		files: token.NewFileSet(),
		info: types.Info{
			Types:      map[ast.Expr]types.TypeAndValue{},
			Defs:       map[*ast.Ident]types.Object{},
			Uses:       map[*ast.Ident]types.Object{},
			Selections: map[*ast.SelectorExpr]*types.Selection{},
			Instances:  map[*ast.Ident]types.Instance{},
		},
		shapes:     map[types.Type]jsonPayloadShape{},
		local:      map[string]*types.Package{},
		interfaces: map[string]bool{},
		typeParams: map[*types.TypeParam]bool{},
	}
}

func (a *jsonPayloadAudit) loadMarshalInterfaces(imports types.Importer) error {
	for _, contract := range []struct{ path, name string }{
		{"encoding/json", "Marshaler"},
		{"encoding", "TextMarshaler"},
	} {
		pkg, err := imports.Import(contract.path)
		if err != nil {
			return err
		}
		a.marshal = append(a.marshal, pkg.Scope().Lookup(contract.name).Type().Underlying().(*types.Interface))
	}
	return nil
}

func loadJSONPayloadAudit(t *testing.T) *jsonPayloadAudit {
	t.Helper()
	audit := newJSONPayloadAudit()
	// go list supplies build-selected files in dependency order. Check the
	// repository packages from source and use export data for external types.
	cmd := exec.CommandContext(t.Context(), "go", "list", "-p=1", "-deps", "-export", "-json=ImportPath,Dir,Export,GoFiles", ".")
	raw, err := cmd.Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			t.Fatalf("load JSON producer packages: %v\n%s", err, exit.Stderr)
		}
		t.Fatal(err)
	}
	exports := map[string]string{}
	var local []jsonPayloadPackage
	decode := json.NewDecoder(bytes.NewReader(raw))
	for {
		var pkg jsonPayloadPackage
		if err := decode.Decode(&pkg); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatal(err)
		}
		exports[pkg.ImportPath] = pkg.Export
		if strings.HasPrefix(pkg.ImportPath, "flow/api/") {
			local = append(local, pkg)
		}
	}
	exportImports := importer.ForCompiler(audit.files, "gc", func(path string) (io.ReadCloser, error) {
		if path := exports[path]; path != "" {
			return os.Open(path)
		}
		return nil, fmt.Errorf("missing export data for %s", path)
	})
	if err := audit.loadMarshalInterfaces(exportImports); err != nil {
		t.Fatal(err)
	}
	config := types.Config{Importer: jsonPayloadImporter(func(path string) (*types.Package, error) {
		if strings.HasPrefix(path, "flow/api/") {
			if pkg := audit.local[path]; pkg != nil {
				return pkg, nil
			}
			return nil, fmt.Errorf("source package %s was not checked", path)
		}
		return exportImports.Import(path)
	})}
	for _, entry := range local {
		var sources []*ast.File
		for _, name := range entry.GoFiles {
			file, err := parser.ParseFile(audit.files, filepath.Join(entry.Dir, name), nil, parser.SkipObjectResolution)
			if err != nil {
				t.Fatal(err)
			}
			sources = append(sources, file)
		}
		pkg, err := config.Check(entry.ImportPath, audit.files, sources, &audit.info)
		if err != nil {
			t.Fatal(err)
		}
		audit.local[entry.ImportPath] = pkg
		audit.sources = append(audit.sources, sources...)
	}
	return audit
}

func jsonPayloadTypeName(typ types.Type) string {
	return types.TypeString(typ, func(pkg *types.Package) string { return pkg.Name() })
}

func jsonPayloadObjectName(obj types.Object) string {
	if obj.Pkg() == nil {
		return obj.Name()
	}
	if fn, ok := obj.(*types.Func); ok {
		if recv := fn.Signature().Recv(); recv != nil {
			return strings.TrimPrefix(jsonPayloadTypeName(recv.Type()), "*") + "." + fn.Name()
		}
	}
	return obj.Pkg().Name() + "." + obj.Name()
}

type jsonPayloadShape struct {
	problems   []string
	codecs     []string
	dynamic    bool
	interfaces []types.Type
	typeParams []*types.TypeParam
}

func (shape jsonPayloadShape) summary() string {
	var parts []string
	if len(shape.codecs) != 0 {
		parts = append(parts, "sized "+strings.Join(shape.codecs, ", "))
	}
	if shape.dynamic {
		parts = append(parts, "dynamic")
	}
	if len(parts) == 0 {
		return "plain"
	}
	return strings.Join(parts, "; ")
}

func (a *jsonPayloadAudit) shape(root types.Type) jsonPayloadShape {
	if result, ok := a.shapes[root]; ok {
		return result
	}
	seen := map[types.Type]bool{}
	var result jsonPayloadShape
	var visit func(types.Type, string)
	visit = func(typ types.Type, path string) {
		typ = types.Unalias(typ)
		if seen[typ] {
			return
		}
		seen[typ] = true
		if pointer, ok := typ.(*types.Pointer); ok {
			visit(pointer.Elem(), path)
			return
		}
		if named, ok := typ.(*types.Named); ok && named.Obj().Pkg() != nil {
			obj := named.Obj()
			// Keep this list in step with metadataPayloadEncodingWork.
			switch obj.Pkg().Path() + "." + obj.Name() {
			case "time.Time", "encoding/json.RawMessage", "flow/api/internal/domain.OptionalInt", "flow/api/internal/domain.ActivityMetadata":
				result.codecs = append(result.codecs, jsonPayloadTypeName(typ))
				return
			case "flow/api/internal/domain.AgentToolCall":
				result.codecs = append(result.codecs, jsonPayloadTypeName(typ))
				visit(named.Underlying(), path)
				return
			case "reflect.Value":
				result.dynamic = true
				return
			}
		}
		if param, ok := typ.(*types.TypeParam); ok {
			result.dynamic = true
			result.typeParams = append(result.typeParams, param)
			return
		}
		for _, contract := range a.marshal {
			_, dynamic := typ.Underlying().(*types.Interface)
			if types.Implements(typ, contract) || !dynamic && types.Implements(types.NewPointer(typ), contract) {
				result.problems = append(result.problems, fmt.Sprintf("%s%s has an unreviewed %s method", path, jsonPayloadTypeName(typ), contract.Method(0).Name()))
				return
			}
		}
		switch underlying := typ.Underlying().(type) {
		case *types.Pointer:
			visit(underlying.Elem(), path)
		case *types.Struct:
			for i := 0; i < underlying.NumFields(); i++ {
				field := underlying.Field(i)
				if jsonPayloadField(field, underlying.Tag(i)) {
					visit(field.Type(), path+field.Name()+".")
				}
			}
		case *types.Array:
			visit(underlying.Elem(), path+"[].")
		case *types.Slice:
			visit(underlying.Elem(), path+"[].")
		case *types.Map:
			keyType := underlying.Key()
			key, ok := keyType.Underlying().(*types.Basic)
			if (!ok || key.Kind() != types.String) && types.Implements(keyType, a.marshal[1]) {
				result.problems = append(result.problems, fmt.Sprintf("%smap key %s requires MarshalText", path, jsonPayloadTypeName(keyType)))
			} else if !ok || key.Kind() != types.String && key.Info()&types.IsInteger == 0 {
				result.problems = append(result.problems, fmt.Sprintf("%smap key %s has no standard bounded encoding", path, jsonPayloadTypeName(keyType)))
			} else {
				visit(keyType, path+"[key].")
			}
			visit(underlying.Elem(), path+"[value].")
		case *types.Basic:
			if underlying.Info()&types.IsComplex != 0 || underlying.Kind() == types.UnsafePointer {
				result.problems = append(result.problems, path+jsonPayloadTypeName(typ)+" has no JSON encoding")
			}
		case *types.Signature, *types.Chan:
			result.problems = append(result.problems, path+jsonPayloadTypeName(typ)+" has no JSON encoding")
		case *types.Interface:
			result.dynamic = true
			result.interfaces = append(result.interfaces, typ)
		}
	}
	visit(root, "")
	slices.Sort(result.codecs)
	result.codecs = slices.Compact(result.codecs)
	a.shapes[root] = result
	return result
}

func jsonPayloadField(field *types.Var, tag string) bool {
	_, embeddedStruct := field.Type().Underlying().(*types.Struct)
	return reflect.StructTag(tag).Get("json") != "-" && (field.Exported() || field.Embedded() && embeddedStruct)
}

func (a *jsonPayloadAudit) calledObject(expr ast.Expr) types.Object {
	switch expr := ast.Unparen(expr).(type) {
	case *ast.Ident:
		return a.info.Uses[expr]
	case *ast.SelectorExpr:
		return a.info.Uses[expr.Sel]
	case *ast.IndexExpr:
		return a.calledObject(expr.X)
	case *ast.IndexListExpr:
		return a.calledObject(expr.X)
	}
	return nil
}

var jsonPayloadArgs = map[string]int{
	"main.server.createTeamRecords":                             5,
	"main.server.mutateSavedView":                               3,
	"main.server.mutateSavedViewWithAggregate":                  3,
	"main.server.marshalSyncJSON":                               1,
	"main.server.writeSyncJSON":                                 2,
	"main.realtimeJSONEncoding.marshal":                         0,
	"main.collaborationSyncBytes":                               0,
	"main.realtimeEntityPayload":                                1,
	"main.realtimeEntityPayloadValue":                           1,
	"main.bareRealtimeEvent":                                    1,
	"main.pagedIssueIDsPayload":                                 2,
	"main.writeRealtimeSSE":                                     1,
	"main.sendSocketJSON":                                       1,
	"main.server.queueWebhookDelivery":                          3,
	"main.projectRecordScope":                                   4,
	"store.MetadataJSONEncodingWork":                            1,
	"store.MetadataJSONRead.ReserveEncoding":                    1,
	"store.MetadataJSONRead.ReserveIntegrationDeliveryEncoding": 1,
	"store.MetadataJSONRead.ReserveDecode":                      2,
	"store.MetadataJSONRead.EncodeGeneric":                      1,
	"store.GeneralBootstrapRead.EncodeRecordJSON":               1,
	"store.SharedIssueRead.ReserveOutput":                       1,
	"store.IssueFacetRead.ReserveValue":                         1,
	"store.SQLiteStore.AdmitMetadataOutputValue":                1,
	"store.ContextWithMutationResults":                          1,
	"store.WithMetadataRecordVerification":                      1,
	"store.committedChangeJSONBytes":                            0,
	"store.recordDeltaJSONBytes":                                0,
	"store.recordKeyJSONBytes":                                  0,
	"store.recordJSONBytes":                                     0,
	"store.SQLiteStore.EnqueueWebhookDelivery":                  1,
	"store.SQLiteStore.publishMutation":                         2,
	"store.encodeOwnedMetadataValue":                            2,
	"store.encodeOwnedMetadataRecord":                           2,
	"store.encodeRetainedMetadataRecord":                        2,
	"store.metadataRecordEncodingWork":                          1,
	"store.metadataPayloadEncodingWork":                         1,
	"store.metadataAggregatePayloadEncodingWork":                1,
	"store.metadataAggregatePayloadJSONWork":                    1,
	"store.issueRecordDecodeWork":                               1,
	"store.realtimePayloadFields":                               1,
	"store.metadataAggregateRealtimeFields":                     3,
	"store.SQLiteStore.encodeMetadataJSONRetained":              1,
	"store.SQLiteStore.metadataAggregateEncodeNormalizedOwned":  1,
	"store.marshalMetadataJSON":                                 0,
	"store.SQLiteStore.encodeMetadataRealtimePayload":           3,
}

func (a *jsonPayloadAudit) payloadArg(obj types.Object) (int, bool) {
	if obj == nil || obj.Pkg() == nil {
		return 0, false
	}
	switch obj.Pkg().Path() {
	case jsonPayloadServer, jsonPayloadStore:
		if index, found := jsonPayloadArgs[jsonPayloadObjectName(obj)]; found {
			return index, true
		}
	}
	fn, ok := obj.(*types.Func)
	if !ok || fn.Pkg().Path() != jsonPayloadStore {
		return 0, false
	}
	params := fn.Signature().Params()
	for i := 0; i < params.Len(); i++ {
		param := params.At(i)
		if param.Name() == "payload" {
			return i, true
		}
	}
	return 0, false
}

// Check where a concrete value enters an interface, even when its eventual
// consumer only sees any. This deliberately includes boxes outside the writer
// call tree so forwarding helpers and writes in mutation callbacks are covered.
type jsonPayloadBox struct {
	expr     ast.Expr
	src, dst types.Type
	use      string
	owner    *ast.FuncDecl
}

func jsonPayloadInterface(typ types.Type) (*types.Interface, bool) {
	if _, param := types.Unalias(typ).(*types.TypeParam); param {
		return nil, false
	}
	iface, ok := typ.Underlying().(*types.Interface)
	return iface, ok
}

func jsonPayloadInterfaceKey(typ types.Type) string {
	return types.TypeString(types.Unalias(typ), (*types.Package).Path)
}

func (a *jsonPayloadAudit) boxes(file *ast.File, report func(jsonPayloadBox)) {
	var owner *ast.FuncDecl
	check := func(expr ast.Expr, src, dst types.Type, use string) {
		if src != nil && dst != nil {
			if _, ok := jsonPayloadInterface(dst); ok && !types.Identical(src, dst) {
				report(jsonPayloadBox{expr, src, dst, use, owner})
			}
		}
	}
	checkList := func(exprs []ast.Expr, destinations []types.Type, use string) {
		if len(exprs) == 1 {
			if tuple, ok := a.info.TypeOf(exprs[0]).(*types.Tuple); ok && tuple.Len() == len(destinations) {
				for i, dst := range destinations {
					check(exprs[0], tuple.At(i).Type(), dst, use)
				}
				return
			}
		}
		for i, expr := range exprs {
			if i < len(destinations) {
				check(expr, a.info.TypeOf(expr), destinations[i], use)
			}
		}
	}
	ast.PreorderStack(file, nil, func(node ast.Node, stack []ast.Node) bool {
		owner = nil
		if len(stack) > 1 {
			owner, _ = stack[1].(*ast.FuncDecl)
		}
		switch node := node.(type) {
		case *ast.CompositeLit:
			switch typ := a.info.TypeOf(node).Underlying().(type) {
			case *types.Struct:
				for i, elem := range node.Elts {
					field, expr := typ.Field(i), elem
					if pair, ok := elem.(*ast.KeyValueExpr); ok {
						field = a.info.Uses[pair.Key.(*ast.Ident)].(*types.Var)
						expr = pair.Value
					}
					check(expr, a.info.TypeOf(expr), field.Type(), "field "+field.Name())
				}
			case *types.Map:
				for _, elem := range node.Elts {
					pair := elem.(*ast.KeyValueExpr)
					check(pair.Key, a.info.TypeOf(pair.Key), typ.Key(), "map key")
					check(pair.Value, a.info.TypeOf(pair.Value), typ.Elem(), "map value")
				}
			case *types.Array:
				a.checkBoxElements(node, typ.Elem(), check)
			case *types.Slice:
				a.checkBoxElements(node, typ.Elem(), check)
			}
		case *ast.AssignStmt:
			dst := make([]types.Type, len(node.Lhs))
			for i, expr := range node.Lhs {
				dst[i] = a.info.TypeOf(expr)
			}
			checkList(node.Rhs, dst, "assignment")
		case *ast.ValueSpec:
			dst := make([]types.Type, len(node.Names))
			for i, name := range node.Names {
				dst[i] = a.info.TypeOf(name)
			}
			checkList(node.Values, dst, "initializer")
		case *ast.ReturnStmt:
			var typ types.Type
			for _, parent := range slices.Backward(stack) {
				switch parent := parent.(type) {
				case *ast.FuncDecl:
					typ = a.info.TypeOf(parent.Name)
				case *ast.FuncLit:
					typ = a.info.TypeOf(parent)
				}
				if typ != nil {
					break
				}
			}
			if sig, ok := typ.(*types.Signature); ok {
				dst := make([]types.Type, sig.Results().Len())
				for i := range dst {
					dst[i] = sig.Results().At(i).Type()
				}
				checkList(node.Results, dst, "return")
			}
		case *ast.SendStmt:
			typ := a.info.TypeOf(node.Chan).Underlying().(*types.Chan)
			check(node.Value, a.info.TypeOf(node.Value), typ.Elem(), "send")
		case *ast.CallExpr:
			callee := a.calledObject(node.Fun)
			typ := a.info.TypeOf(node.Fun)
			if a.info.Types[node.Fun].IsType() && len(node.Args) == 1 {
				check(node.Args[0], a.info.TypeOf(node.Args[0]), typ, "conversion")
				break
			}
			if callee == types.Universe.Lookup("append") && !node.Ellipsis.IsValid() {
				if dst, ok := a.info.TypeOf(node.Args[0]).Underlying().(*types.Slice); ok {
					for _, arg := range node.Args[1:] {
						check(arg, a.info.TypeOf(arg), dst.Elem(), "append")
					}
				}
				break
			}
			sig, ok := typ.Underlying().(*types.Signature)
			if !ok || sig.Params().Len() == 0 {
				break
			}
			if callee != nil && callee.Pkg() != nil && a.local[callee.Pkg().Path()] == nil &&
				!(callee.Pkg().Path() == "reflect" && callee.Name() == "ValueOf") {
				// Foreign calls such as fmt.Printf consume arguments. A
				// reflect.Value can carry the original value to a writer.
				break
			}
			if callee != nil && callee.Pkg() != nil && callee.Pkg().Path() == "reflect" && len(stack) != 0 {
				if selector, ok := stack[len(stack)-1].(*ast.SelectorExpr); ok && selector.X == node && selector.Sel.Name == "Pointer" {
					break
				}
			}
			dst := make([]types.Type, max(sig.Params().Len(), len(node.Args)))
			for i := range dst {
				param := min(i, sig.Params().Len()-1)
				typ := sig.Params().At(param).Type()
				if sig.Variadic() && param == sig.Params().Len()-1 && !node.Ellipsis.IsValid() {
					typ = typ.(*types.Slice).Elem()
				}
				dst[i] = typ
			}
			use := "function argument"
			if callee != nil {
				use = jsonPayloadObjectName(callee) + " argument"
			}
			checkList(node.Args, dst, use)
		}
		return true
	})
}

func (a *jsonPayloadAudit) checkBoxElements(literal *ast.CompositeLit, dst types.Type, check func(ast.Expr, types.Type, types.Type, string)) {
	for _, elem := range literal.Elts {
		if pair, ok := elem.(*ast.KeyValueExpr); ok {
			elem = pair.Value
		}
		check(elem, a.info.TypeOf(elem), dst, "element")
	}
}

// These functions have interface values for other encoders or in-process
// state. Re-review their bodies if the value's lifetime changes. The type walk
// still checks every writer argument in these functions.
var jsonPayloadNonWriterScopes = map[string]string{
	// Authentication options are for the WebAuthn HTTP response.
	jsonPayloadServer + "/main.server.beginMFA":                 "feeab3629826c04dca3ea9fd097e05cb6ade02286c8df339eb147af2e0fcdd42",
	jsonPayloadServer + "/main.server.beginPasskeyRegistration": "1b2368e78a4d17d2fb32bb27e248088e7fca9c5a6da2ac0b27394a8586b9eab8",
	// SAML passes a crypto.Signer to the middleware's options.
	jsonPayloadServer + "/main.configureSAML": "68714b90dc9d26ef4a7ca3557f411bba99e53a2d4f44da0e1070257e80183073",
	// The connector returns only size-checked RawMessage.
	jsonPayloadServer + "/main.remoteMCPClient.rpc": "a63c00ecae9a7537bfb9e7537d8423b64c7db926eb11a98e04e5eee1267aa4af",
	// These values stay inside the network-policy API and CRDT respectively.
	"flow/api/internal/sandbox/kubernetes/kubernetes.Provider.EnsureNetworkPolicies": "09c8b383d0b6059010f38f47dafb1b27088920354fc8de77c81c836c5ae53adb",
	"flow/api/internal/boundedcrdt/internal/encoding/encoding.Update.Apply":          "0fb94651493528a73ce30045c939a92d6e2db1843cd7e0d49d2f04170346c365",
}

func (a *jsonPayloadAudit) payloadBoxes() []jsonPayloadBox {
	var boxes []jsonPayloadBox
	for _, file := range a.sources {
		a.boxes(file, func(box jsonPayloadBox) { boxes = append(boxes, box) })
	}
	needed := func(typ types.Type) bool {
		iface, _ := jsonPayloadInterface(typ)
		return iface.Empty() || a.interfaces[jsonPayloadInterfaceKey(typ)]
	}
	// A named interface can itself be copied into an any. Follow those
	// conversions back to concrete origins without auditing unused interfaces.
	for changed := true; changed; {
		changed = false
		for _, box := range boxes {
			if _, ok := jsonPayloadInterface(box.src); ok && !needed(box.src) && needed(box.dst) {
				a.interfaces[jsonPayloadInterfaceKey(box.src)] = true
				changed = true
			}
		}
	}
	return slices.DeleteFunc(boxes, func(box jsonPayloadBox) bool {
		_, forwarding := jsonPayloadInterface(box.src)
		return forwarding || !needed(box.dst)
	})
}

func (a *jsonPayloadAudit) checkBoxes(t *testing.T) {
	t.Helper()
	seen := map[string]bool{}
	for _, box := range a.payloadBoxes() {
		shape := a.shape(box.src)
		for _, param := range shape.typeParams {
			a.typeParams[param] = true
		}
		if len(shape.problems) == 0 {
			continue
		}
		if box.owner != nil {
			obj := a.info.Defs[box.owner.Name]
			key := obj.Pkg().Path() + "/" + jsonPayloadObjectName(obj)
			if want, reviewed := jsonPayloadNonWriterScopes[key]; reviewed {
				if !seen[key] {
					var body bytes.Buffer
					if err := format.Node(&body, a.files, box.owner); err != nil {
						t.Fatal(err)
					}
					if got := fmt.Sprintf("%x", sha256.Sum256(body.Bytes())); got != want {
						t.Errorf("review interface flow outside bounded JSON: %s has digest %s", key, got)
					}
					seen[key] = true
				}
				continue
			}
		}
		for _, problem := range shape.problems {
			t.Errorf("%s %s: %s", a.files.Position(box.expr.Pos()), box.use, problem)
		}
	}
	for key := range jsonPayloadNonWriterScopes {
		if !seen[key] {
			t.Errorf("stale interface-flow review for %s", key)
		}
	}
}

type jsonPayloadTypeArgument struct {
	ident *ast.Ident
	param *types.TypeParam
	arg   types.Type
}

func (a *jsonPayloadAudit) payloadTypeArguments() []jsonPayloadTypeArgument {
	var uses []jsonPayloadTypeArgument
	for ident, instance := range a.info.Instances {
		var params *types.TypeParamList
		switch typ := types.Unalias(a.info.Uses[ident].Type()).(type) {
		case *types.Signature:
			params = typ.TypeParams()
		case *types.Named:
			params = typ.TypeParams()
		}
		for i := 0; i < params.Len(); i++ {
			uses = append(uses, jsonPayloadTypeArgument{ident, params.At(i), instance.TypeArgs.At(i)})
		}
	}
	// Instantiate only parameters used as payload data. A generic control
	// helper can legitimately use channels or functions without encoding them.
	for changed := true; changed; {
		changed = false
		for _, use := range uses {
			if !a.typeParams[use.param] {
				continue
			}
			for _, param := range a.shape(use.arg).typeParams {
				if !a.typeParams[param] {
					a.typeParams[param], changed = true, true
				}
			}
		}
	}
	return slices.DeleteFunc(uses, func(use jsonPayloadTypeArgument) bool { return !a.typeParams[use.param] })
}

func (a *jsonPayloadAudit) checkRoot(t *testing.T, label string, typ types.Type) string {
	t.Helper()
	shape := a.shape(typ)
	for _, iface := range shape.interfaces {
		a.interfaces[jsonPayloadInterfaceKey(iface)] = true
	}
	for _, param := range shape.typeParams {
		a.typeParams[param] = true
	}
	for _, problem := range shape.problems {
		t.Errorf("%s: %s", label, problem)
	}
	return fmt.Sprintf("%s | %s | %s", label, jsonPayloadTypeName(typ), shape.summary())
}

// Associate the direct mux registrations with their producer. A shared
// mutation helper or background job is identified by its function and event.
func (a *jsonPayloadAudit) routes() map[string][]string {
	routes := map[string][]string{}
	for _, file := range a.sources {
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) != 2 {
				return true
			}
			switch callee := a.calledObject(call.Fun); {
			case callee == nil || callee.Pkg() == nil || callee.Pkg().Path() != "net/http":
				return true
			case callee.Name() != "Handle" && callee.Name() != "HandleFunc":
				return true
			}
			pattern := a.info.Types[call.Args[0]].Value
			if pattern == nil {
				return true
			}
			if handler, ok := a.calledObject(call.Args[1]).(*types.Func); ok && handler.Pkg().Path() == jsonPayloadServer {
				name := jsonPayloadObjectName(handler)
				routes[name] = append(routes[name], pattern.ExactString())
			}
			return true
		})
	}
	for name, patterns := range routes {
		slices.Sort(patterns)
		routes[name] = slices.Compact(patterns)
	}
	return routes
}

func TestBoundedJSONPayloadGuard(t *testing.T) {
	audit := loadJSONPayloadAudit(t)
	var inventory []string
	bootstrap := audit.local[jsonPayloadDomain].Scope().Lookup("Bootstrap").Type().Underlying().(*types.Struct)
	for i := 0; i < bootstrap.NumFields(); i++ {
		field := bootstrap.Field(i)
		if jsonPayloadField(field, bootstrap.Tag(i)) {
			inventory = append(inventory, audit.checkRoot(t, "resident domain.Bootstrap."+field.Name(), field.Type()))
		}
	}
	for path, names := range map[string][]string{
		jsonPayloadDomain: {"DomainEvent", "RealtimeEvent", "WebhookDelivery"},
		jsonPayloadStore:  {"RecordKey", "MetadataRecord", "RecordPage", "RecordBatch", "RecordDelta", "CommittedChange", "CommittedPage"},
		jsonPayloadServer: {"childEventPayload", "legacyWebhookEnvelope", "flowWebhookEnvelope", "collaborationEventPayload", "collaborationSyncMessage", "syncV2Transaction", "syncV2ChangesResponse", "syncManifestResponse"},
	} {
		for _, name := range names {
			obj := audit.local[path].Scope().Lookup(name)
			inventory = append(inventory, audit.checkRoot(t, "frame "+jsonPayloadObjectName(obj), obj.Type()))
		}
	}
	routes := audit.routes()
	sites := 0
	for _, file := range audit.sources {
		for _, decl := range file.Decls {
			owner := "package initializer"
			if fn, ok := decl.(*ast.FuncDecl); ok {
				owner = jsonPayloadObjectName(audit.info.Defs[fn.Name])
			}
			ast.Inspect(decl, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				callee := audit.calledObject(call.Fun)
				index, found := audit.payloadArg(callee)
				pos := audit.files.Position(call.Pos())
				name := filepath.Base(pos.Filename)
				// Some publishers use the ordinary encoder before handing
				// raw JSON to a budget or queue. Check those inputs too.
				if callee != nil && callee.Pkg() != nil && callee.Pkg().Path() == "encoding/json" && callee.Name() == "Marshal" {
					if strings.Contains(name, "webhook") || name == "record_journal.go" || name == "realtime_collaboration.go" || name == "realtime.go" {
						index, found = 0, true
					}
				}
				if callee != nil && callee.Name() == "marshal" && name == "event_source.go" {
					index, found = 0, true // projectEvent and pulseProjection's encoding callback
				}
				if callee != nil && callee.Name() == "encode" && name == "sync_v2.go" {
					index, found = 0, true // syncRecordSummary's encoding callback
				}
				if !found {
					return true
				}
				if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
					if selection := audit.info.Selections[selector]; selection != nil && selection.Kind() == types.MethodExpr {
						index++
					}
				}
				last := min(len(call.Args), index+1)
				if sig, ok := audit.info.TypeOf(call.Fun).(*types.Signature); ok &&
					sig.Variadic() && (index == sig.Params().Len()-1 || callee.Name() == "projectRecordScope") {
					last = len(call.Args)
				}
				for _, expr := range call.Args[min(index, last):last] {
					if wrapped, ok := ast.Unparen(expr).(*ast.CallExpr); ok && len(wrapped.Args) == 1 {
						if fn := audit.calledObject(wrapped.Fun); fn != nil && fn.Pkg() != nil && fn.Pkg().Path() == "reflect" && fn.Name() == "ValueOf" {
							expr = wrapped.Args[0]
						}
					}
					typ := audit.info.TypeOf(expr)
					if typ == nil {
						t.Errorf("unresolved payload at %s", audit.files.Position(expr.Pos()))
						continue
					}
					site := fmt.Sprintf("%s:%d %s -> %s", filepath.Base(pos.Filename), pos.Line, owner, jsonPayloadObjectName(callee))
					for _, arg := range call.Args[:min(index, len(call.Args))] {
						if literal, ok := arg.(*ast.BasicLit); ok && literal.Kind == token.STRING && strings.Contains(literal.Value, ".") {
							site += " " + literal.Value
							break
						}
					}
					if patterns := routes[owner]; len(patterns) != 0 {
						site += " " + strings.Join(patterns, ", ")
					}
					inventory = append(inventory, audit.checkRoot(t, site, typ))
					sites++
				}
				return true
			})
		}
	}
	audit.checkBoxes(t)
	for _, use := range audit.payloadTypeArguments() {
		pos := audit.files.Position(use.ident.Pos())
		label := fmt.Sprintf("%s:%d generic %s", filepath.Base(pos.Filename), pos.Line, use.ident.Name)
		inventory = append(inventory, audit.checkRoot(t, label, use.arg))
		sites++
	}
	if sites == 0 {
		t.Fatal("no production JSON payload sites found")
	}
	t.Logf("%d producer arguments and %d resident fields or frame types", sites, len(inventory)-sites)
	if os.Getenv("FLOW_TEST_BOUNDED_JSON_INVENTORY") == "1" && !t.Failed() {
		slices.Sort(inventory)
		t.Logf("Producer inventory\n%s", strings.Join(inventory, "\n"))
	}
}

func TestBoundedJSONPayloadGuardShapes(t *testing.T) {
	const source = `
package producer
type jsonMarshaler interface { MarshalJSON() ([]byte, error) }
type textMarshaler interface { MarshalText() ([]byte, error) }
type JSON int
func (JSON) MarshalJSON() ([]byte, error) { return nil, nil }
type Text int
func (*Text) MarshalText() ([]byte, error) { return nil, nil }
type Promoted struct { JSON }
type Alias = Promoted
type DefinedPointer *Text
type Nested struct { Value map[string][]*Text }
type Key struct{}
func (Key) MarshalText() ([]byte, error) { return nil, nil }
type EncodedKey map[Key]int
type InvalidKey map[bool]string
type InvalidFields struct { Fn func(); Ch chan int; Number complex64 }
type CodecField struct { Value jsonMarshaler }
type Plain struct { Values map[int]string; Hidden func() ` + "`json:\"-\"`" + ` }
type Named interface { Mark() }
func (JSON) Mark() {}
func box[T any](value T) map[string]any { return map[string]any{"generic": value} }
func forward[T any](value T) map[string]any { return box(value) }
func source() any {
	var named Named = JSON(0)
	value := map[string]any{"name": named}
	value["child"] = Alias{}
	_ = append([]any{}, new(Text))
	return forward(Key{})
}
`
	audit := newJSONPayloadAudit()
	file, err := parser.ParseFile(audit.files, "producer.go", source, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := new(types.Config).Check("producer", audit.files, []*ast.File{file}, &audit.info)
	if err != nil {
		t.Fatal(err)
	}
	audit.local[pkg.Path()] = pkg
	audit.sources = []*ast.File{file}
	for _, name := range []string{"jsonMarshaler", "textMarshaler"} {
		audit.marshal = append(audit.marshal, pkg.Scope().Lookup(name).Type().Underlying().(*types.Interface))
	}
	for name, want := range map[string]string{
		"JSON": "MarshalJSON", "Text": "MarshalText", "Promoted": "MarshalJSON", "Alias": "MarshalJSON",
		"DefinedPointer": "MarshalText", "Nested": "Value.[value].[].", "EncodedKey": "requires MarshalText", "InvalidKey": "map key bool",
		"InvalidFields": "Fn.func()", "CodecField": "MarshalJSON", "Plain": "",
	} {
		problems := strings.Join(audit.shape(pkg.Scope().Lookup(name).Type()).problems, "\n")
		if want == "" && problems != "" || !strings.Contains(problems, want) {
			t.Errorf("%s problems = %q, want %q", name, problems, want)
		}
	}
	var rejected []string
	for _, box := range audit.payloadBoxes() {
		shape := audit.shape(box.src)
		for _, param := range shape.typeParams {
			audit.typeParams[param] = true
		}
		if len(shape.problems) != 0 {
			rejected = append(rejected, box.use)
		}
	}
	for _, use := range audit.payloadTypeArguments() {
		if len(audit.shape(use.arg).problems) != 0 {
			rejected = append(rejected, "generic "+use.ident.Name)
		}
	}
	slices.Sort(rejected)
	if want := []string{"append", "assignment", "generic forward", "initializer"}; !slices.Equal(rejected, want) {
		t.Errorf("boxed producer problems = %v, want %v", rejected, want)
	}
}
