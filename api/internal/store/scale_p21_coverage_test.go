package store

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

var scaleP21SnapshotMethods = strings.Fields(`WorkspaceMetadata WorkspaceMetadataFields Bootstrap BootstrapFor BootstrapForContext BootstrapForUser PagedWorkspaceMetadata BeginWorkspaceRead MutateWorkspace MutateWorkspaceWithAggregate MutateWorkspaceAudited MutateWorkspaceMetadata workspaceData publicMetadata mcpWorkspaceData mcpWorkspaceDataForTool chatAuthorizedMutation storedWorkspace loadWorkspaceState loadWorkspaceStamped decodeWorkspaceMetadata mutateFull mutateScoped mutateTeamMetadata WorkspaceMetadataScoped WorkspaceMetadataFieldsScoped MutateWorkspaceScoped MutateWorkspaceScopedWithAggregate ProjectScopedMetadata workspaceDataScoped ScanMetadataRecords StreamMetadataRecords`)

func scaleP21RouteBinding(set *token.FileSet, call *ast.CallExpr) (string, error) {
	var handler bytes.Buffer
	if err := printer.Fprint(&handler, set, call.Args[1]); err != nil {
		return "", err
	}
	if literal, ok := call.Args[0].(*ast.BasicLit); ok && literal.Kind == token.STRING {
		pattern, err := strconv.Unquote(literal.Value)
		return pattern + "\t" + handler.String(), err
	}
	// Dynamic patterns still need an explicit review entry. Ignoring them
	// would let a new handler bypass coverage by assigning its path first.
	var pattern bytes.Buffer
	if err := printer.Fprint(&pattern, set, call.Args[0]); err != nil {
		return "", err
	}
	return "EXPR " + pattern.String() + "\t" + handler.String(), nil
}

func scaleP21SnapshotConsumer(node ast.Node) bool {
	found := false
	ast.Inspect(node, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if selector.Sel.Name == "workspaces" {
			found = true
		}
		if slices.Contains(scaleP21SnapshotMethods, selector.Sel.Name) {
			if qualifier, ok := selector.X.(*ast.Ident); !ok || qualifier.Name != "domain" {
				found = true
			}
		}
		return true
	})
	return found
}

func TestScaleP21FullWorkspaceConsumerInventory(t *testing.T) {
	actual, routes := map[string]bool{}, map[string]bool{}
	for prefix, dir := range map[string]string{"store": ".", "server": "../../cmd/server"} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range files {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			set := token.NewFileSet()
			file, err := parser.ParseFile(set, path, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if group, ok := decl.(*ast.GenDecl); ok {
					for _, spec := range group.Specs {
						value, ok := spec.(*ast.ValueSpec)
						if !ok || !scaleP21SnapshotConsumer(value) {
							continue
						}
						for _, name := range value.Names {
							actual[prefix+"/"+filepath.Base(path)+":var:"+name.Name] = true
						}
					}
				}
				if ok && scaleP21SnapshotConsumer(fn) {
					actual[prefix+"/"+filepath.Base(path)+":"+fn.Name.Name] = true
				}
				if ok && prefix == "server" && strings.HasPrefix(filepath.Base(path), "mcp") {
					ast.Inspect(fn, func(node ast.Node) bool {
						statement, ok := node.(*ast.SwitchStmt)
						if !ok {
							return true
						}
						name, ok := statement.Tag.(*ast.Ident)
						if !ok || name.Name != "name" {
							return true
						}
						for _, item := range statement.Body.List {
							for _, expression := range item.(*ast.CaseClause).List {
								literal, ok := expression.(*ast.BasicLit)
								if !ok || literal.Kind != token.STRING {
									continue
								}
								tool, err := strconv.Unquote(literal.Value)
								if err != nil {
									t.Fatal(err)
								}
								routes["MCP "+filepath.Base(path)+":"+fn.Name.Name+"\t"+tool] = true
							}
						}
						return true
					})
				}
			}
			if prefix != "server" {
				continue
			}
			ast.Inspect(file, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok || len(call.Args) < 2 {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || selector.Sel.Name != "Handle" && selector.Sel.Name != "HandleFunc" {
					return true
				}
				binding, err := scaleP21RouteBinding(set, call)
				if err != nil {
					t.Fatal(err)
				}
				routes[binding] = true
				return true
			})
		}
	}
	// Optional machine-readable evidence for reviewing an inventory update.
	// Ordinary tests never update the checked-in inventory.
	if path := os.Getenv("FLOW_P21_INVENTORY_REPORT"); path != "" {
		report, err := json.MarshalIndent(map[string]any{"consumers": actual, "routes": routes}, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, report, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	expected := map[string]bool{}
	for name, review := range metadataFullWorkspaceConsumers() {
		if review.owner != "P16" && review.owner != "P17" && review.owner != "P18" && review.owner != "P21" {
			t.Errorf("missing removal owner: %s", name)
		}
		expected[name] = true
	}
	for name, reason := range scaleP21MigrateReviewedConsumers() {
		if expected[name] || strings.TrimSpace(reason) == "" {
			t.Errorf("duplicate or unexplained consumer review: %s", name)
		}
		expected[name] = true
	}
	for name := range actual {
		if !expected[name] {
			t.Errorf("unregistered workspace consumer: %s", name)
		}
	}
	for name := range expected {
		if !actual[name] {
			t.Errorf("stale workspace consumer registration: %s", name)
		}
	}
	expectedRoutes := map[string]bool{}
	for _, route := range scaleP21ReviewedRoutes {
		expectedRoutes[route] = true
	}
	for route := range routes {
		if !expectedRoutes[route] {
			t.Errorf("unreviewed handler binding: %s", route)
		}
	}
	for route := range expectedRoutes {
		if !routes[route] {
			t.Errorf("stale handler binding: %s", route)
		}
	}
	t.Logf("registered snapshot consumers=%d; handler bindings=%d; activation blockers=%d", len(actual), len(routes), len(metadataFullWorkspaceConsumers()))
}

func TestScaleP21EveryMetadataFieldHasResidencyDecision(t *testing.T) {
	fields := map[string]bool{}
	kind := reflect.TypeFor[domain.Bootstrap]()
	for i := 0; i < kind.NumField(); i++ {
		field := kind.Field(i)
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if field.PkgPath != "" {
			continue
		}
		if name == "-" {
			if !slices.Contains(strings.Fields(scaleP21RuntimeMetadataFields), field.Name) {
				t.Errorf("unregistered runtime Bootstrap field: %s", field.Name)
			}
			continue
		}
		if name == "" {
			name = field.Name
		}
		if fields[name] {
			t.Errorf("duplicate Bootstrap JSON field: %s", name)
		}
		fields[name] = true
		mode := metadataResidencyFields()[name]
		if mode != "core" && mode != "policy" && mode != "records" && mode != "external" && mode != "derived" {
			t.Errorf("unregistered Bootstrap field: %s", name)
		}
	}
	for name := range metadataResidencyFields() {
		if !fields[name] {
			t.Errorf("stale field residency registration: %s", name)
		}
	}
	for _, name := range strings.Fields(scaleP21RuntimeMetadataFields) {
		field, ok := kind.FieldByName(name)
		if !ok || field.Tag.Get("json") != "-" {
			t.Errorf("stale runtime field residency registration: %s", name)
		}
	}
	t.Logf("explicit field decisions=%d", len(fields))
}

func TestScaleP21MCPToolInventory(t *testing.T) {
	raw, err := os.ReadFile("../../cmd/server/flow_mcp_tools.json")
	if err != nil {
		t.Fatal(err)
	}
	var tools []struct{ Name, Access string }
	if err := json.Unmarshal(raw, &tools); err != nil {
		t.Fatal(err)
	}
	expected := map[string]string{}
	for access, names := range scaleP21ReviewedMCPTools {
		for _, name := range strings.Fields(names) {
			if expected[name] != "" {
				t.Errorf("duplicate MCP registration: %s", name)
			}
			expected[name] = access
		}
	}
	seen := map[string]bool{}
	for _, tool := range tools {
		name := strings.TrimPrefix(tool.Name, "mcp__flow.")
		if name == tool.Name || expected[name] == "" || expected[name] != tool.Access {
			t.Errorf("unreviewed MCP handler: %s (%s)", tool.Name, tool.Access)
		}
		if seen[name] {
			t.Errorf("duplicate MCP handler: %s", tool.Name)
		}
		seen[name] = true
	}
	for name := range expected {
		if !seen[name] {
			t.Errorf("stale MCP registration: %s", name)
		}
	}
	t.Logf("registered MCP tools=%d", len(tools))
}

const scaleP21RuntimeMetadataFields = `NextIssueNumber ViewerTeamIDs TeamByID TeamByKey TeamChildren LabelIndex FormerMembers CollaborationUpdates`

func TestScaleP21InventoryRejectsNewHandlerOrMethodValue(t *testing.T) {
	for _, source := range []string{
		`package probe; func handler() { s.workspaceData(r) }`,
		`package probe; func handler() { callback := s.workspaceData; _ = callback }`,
		`package probe; func background() { _ = s.workspaces[key] }`,
		`package probe; var callback = s.workspaceData`,
	} {
		file, err := parser.ParseFile(token.NewFileSet(), "new.go", source, 0)
		if err != nil {
			t.Fatal(err)
		}
		if !scaleP21SnapshotConsumer(file) {
			t.Fatal("new full-workspace consumer escaped registration")
		}
	}
}

func TestScaleP21InventoryIncludesDynamicRouteBindings(t *testing.T) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, "new.go", `package probe; func routes() { mux.HandleFunc(pattern, s.newHandler) }`, 0)
	if err != nil {
		t.Fatal(err)
	}
	bindings := []string{}
	ast.Inspect(file, func(node ast.Node) bool {
		if call, ok := node.(*ast.CallExpr); ok {
			binding, err := scaleP21RouteBinding(fileSet, call)
			if err != nil {
				t.Fatal(err)
			}
			bindings = append(bindings, binding)
		}
		return true
	})
	if !slices.Equal(bindings, []string{"EXPR pattern\ts.newHandler"}) {
		t.Fatalf("dynamic handler escaped registration: %q", bindings)
	}
}
