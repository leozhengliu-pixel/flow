package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

type deliveryBoundarySpec struct {
	legacy, owned []string
	alias         bool
}

var deliveryBoundarySites = map[string]deliveryBoundarySpec{
	"store/scale_p21_delivery_context.go:BeginNotificationDeliveryContextRead":   {legacy: []string{"NotificationDeliverySnapshot"}, owned: []string{"BeginDueNotificationDeliveryRead"}},
	"store/scale_p21_sw15_notification_read.go:BeginDueNotificationDeliveryRead": {legacy: []string{"NotificationDeliveries"}},
	"server/chat_personal_notifications.go:dispatchChatNotifications":            {legacy: []string{"NotificationDeliveries"}, owned: []string{"BeginDueNotificationDeliveryRead"}},
	"server/delivery_scheduler.go:prepareDueNotificationDeliveries":              {legacy: []string{"NotificationDeliveries"}, owned: []string{"BeginDueNotificationDeliveryRead"}},
	"server/notification_settings.go:dispatchNotificationDigests":                {legacy: []string{"NotificationDeliverySnapshot"}, owned: []string{"BeginNotificationDeliveryContextRead"}},
	"server/notifications_workflow.go:dispatchNotificationEmails":                {legacy: []string{"NotificationDeliverySnapshot", "NotificationDeliveries"}, owned: []string{"BeginDueNotificationDeliveryRead", "BeginNotificationDeliveryContextRead"}, alias: true},
}

type deliveryBoundaryCatalog struct {
	fields map[string]map[string]ast.Expr
	result map[string][]ast.Expr
}

func deliveryBoundaryTypeName(value ast.Expr) string {
	switch value := value.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.SelectorExpr:
		return value.Sel.Name
	case *ast.StarExpr:
		return deliveryBoundaryTypeName(value.X)
	case *ast.ParenExpr:
		return deliveryBoundaryTypeName(value.X)
	}
	return ""
}

// This intentionally resolves only declaration/assignment types needed to
// distinguish Bootstrap slice fields from identically named store methods.
// Unknown receivers are audited as possible method values, never ignored.
func (c deliveryBoundaryCatalog) expressionType(value ast.Expr, result, depth int) ast.Expr {
	if value == nil || depth > 30 {
		return nil
	}
	recur := func(value ast.Expr, index int) ast.Expr { return c.expressionType(value, index, depth+1) }
	switch value := value.(type) {
	case *ast.CompositeLit:
		return value.Type
	case *ast.TypeAssertExpr:
		return value.Type
	case *ast.ParenExpr:
		return recur(value.X, result)
	case *ast.UnaryExpr:
		if value.Op == token.RANGE {
			switch ranged := recur(value.X, 0).(type) {
			case *ast.MapType:
				if result == 0 {
					return ranged.Key
				}
				return ranged.Value
			case *ast.ArrayType:
				if result > 0 {
					return ranged.Elt
				}
			}
			return nil
		}
		return recur(value.X, result)
	case *ast.StarExpr:
		return recur(value.X, result)
	case *ast.SelectorExpr:
		return c.fields[deliveryBoundaryTypeName(recur(value.X, 0))][value.Sel.Name]
	case *ast.IndexExpr:
		switch indexed := recur(value.X, 0).(type) {
		case *ast.MapType:
			return indexed.Value
		case *ast.ArrayType:
			return indexed.Elt
		}
	case *ast.CallExpr:
		name := ""
		switch function := value.Fun.(type) {
		case *ast.SelectorExpr:
			name = deliveryBoundaryTypeName(recur(function.X, 0)) + "." + function.Sel.Name
		case *ast.Ident:
			name = function.Name
			if (name == "new" || name == "make") && len(value.Args) > 0 {
				return value.Args[0]
			}
		}
		if values := c.result[name]; result < len(values) {
			return values[result]
		}
	case *ast.Ident:
		if value.Obj == nil {
			return nil
		}
		switch declaration := value.Obj.Decl.(type) {
		case *ast.Field:
			return declaration.Type
		case *ast.ValueSpec:
			if declaration.Type != nil {
				return declaration.Type
			}
			for index, name := range declaration.Names {
				if name.Name == value.Name {
					if len(declaration.Values) == 1 {
						return recur(declaration.Values[0], index)
					}
					if index < len(declaration.Values) {
						return recur(declaration.Values[index], 0)
					}
				}
			}
		case *ast.AssignStmt:
			for index, target := range declaration.Lhs {
				if target, ok := target.(*ast.Ident); ok && target.Name == value.Name {
					if len(declaration.Rhs) == 1 {
						return recur(declaration.Rhs[0], index)
					}
					if index < len(declaration.Rhs) {
						return recur(declaration.Rhs[index], 0)
					}
				}
			}
		case *ast.RangeStmt:
			switch ranged := recur(declaration.X, 0).(type) {
			case *ast.MapType:
				return ranged.Value
			case *ast.ArrayType:
				return ranged.Elt
			}
		}
	}
	return nil
}

func deliveryBoundaryCondition(value ast.Expr) (int, bool) {
	const off, on = 1, 2
	positive := true
	for {
		switch expression := value.(type) {
		case *ast.ParenExpr:
			value = expression.X
		case *ast.UnaryExpr:
			if expression.Op != token.NOT {
				return 0, false
			}
			positive, value = !positive, expression.X
		default:
			call, ok := value.(*ast.CallExpr)
			if !ok || len(call.Args) != 0 {
				return 0, false
			}
			method, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || method.Sel.Name != "BoundedMetadataEnabled" || !deliveryBoundaryStoreReceiver(method.X) {
				return 0, false
			}
			if positive {
				return on, true
			}
			return off, true
		}
	}
}

func deliveryBoundaryStoreReceiver(value ast.Expr) bool {
	if value, ok := value.(*ast.Ident); ok {
		return value.Name == "s"
	}
	if value, ok := value.(*ast.SelectorExpr); ok && value.Sel.Name == "store" {
		name, ok := value.X.(*ast.Ident)
		return ok && name.Name == "s"
	}
	return false
}

func deliveryBoundaryReturns(statement ast.Stmt) bool {
	block, ok := statement.(*ast.BlockStmt)
	if !ok || len(block.List) == 0 {
		return false
	}
	_, ok = block.List[len(block.List)-1].(*ast.ReturnStmt)
	return ok
}

func deliveryBoundaryMode(function *ast.FuncDecl, node ast.Node, parents map[ast.Node]ast.Node) int {
	mode := 3
	for current := parents[node]; current != nil && current != function; current = parents[current] {
		if _, closure := current.(*ast.FuncLit); closure {
			return 0 // Lexical guards cannot prove when an escaping closure runs.
		}
		if branch, ok := current.(*ast.IfStmt); ok {
			truth, known := deliveryBoundaryCondition(branch.Cond)
			if !known {
				continue
			}
			if node.Pos() >= branch.Body.Pos() && node.End() <= branch.Body.End() {
				mode &= truth
			} else if branch.Else != nil && node.Pos() >= branch.Else.Pos() && node.End() <= branch.Else.End() {
				mode &= 3 ^ truth
			}
		}
	}
	// The two store adapters return their legacy result in an initial off
	// branch; their native owned-reader calls follow that terminating guard.
	for _, statement := range function.Body.List {
		branch, ok := statement.(*ast.IfStmt)
		if !ok || branch.End() >= node.Pos() {
			continue
		}
		truth, known := deliveryBoundaryCondition(branch.Cond)
		if !known {
			continue
		}
		if deliveryBoundaryReturns(branch.Body) {
			mode &= 3 ^ truth
		}
		if deliveryBoundaryReturns(branch.Else) {
			mode &= truth
		}
	}
	return mode
}

func deliveryBoundaryCheck(c deliveryBoundaryCatalog, function *ast.FuncDecl, spec deliveryBoundarySpec) error {
	parents := map[ast.Node]ast.Node{}
	var stack []ast.Node
	ast.Inspect(function, func(node ast.Node) bool {
		if node == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		if len(stack) > 0 {
			parents[node] = stack[len(stack)-1]
		}
		stack = append(stack, node)
		return true
	})
	legacy, owned := []string{}, []string{}
	aliases := map[*ast.Object]bool{}
	var failure error
	ast.Inspect(function.Body, func(node ast.Node) bool {
		method, ok := node.(*ast.SelectorExpr)
		if !ok || failure != nil {
			return true
		}
		name := method.Sel.Name
		isLegacy := name == "NotificationDeliveries" || name == "NotificationDeliverySnapshot"
		if !isLegacy && name != "BeginDueNotificationDeliveryRead" && name != "BeginNotificationDeliveryContextRead" {
			return true
		}
		receiver := deliveryBoundaryTypeName(c.expressionType(method.X, 0, 0))
		if isLegacy && (receiver == "Bootstrap" || receiver == "TrashLinks") {
			return true
		}
		if !isLegacy && !slices.Contains(spec.owned, name) {
			return true
		}
		call, direct := parents[method].(*ast.CallExpr)
		direct = direct && call.Fun == method
		if !isLegacy {
			if !direct || deliveryBoundaryMode(function, call, parents) != 2 {
				failure = fmt.Errorf("owned reader %s is not exclusively bounded", name)
			}
			owned = append(owned, name)
			return true
		}
		legacy = append(legacy, name)
		if !slices.Contains(spec.legacy, name) || receiver != "SQLiteStore" || !deliveryBoundaryStoreReceiver(method.X) {
			failure = fmt.Errorf("unreviewed legacy selector %s (receiver %q)", name, receiver)
		} else if direct {
			if deliveryBoundaryMode(function, call, parents) != 1 {
				failure = fmt.Errorf("legacy call %s is not exclusively off", name)
			}
		} else {
			assignment, ok := parents[method].(*ast.AssignStmt)
			if !spec.alias || !ok || len(assignment.Lhs) != 1 || len(assignment.Rhs) != 1 || assignment.Rhs[0] != method {
				failure = fmt.Errorf("unreviewed method-value escape %s", name)
				return true
			}
			target, ok := assignment.Lhs[0].(*ast.Ident)
			if !ok || target.Name != "snapshot" || target.Obj == nil {
				failure = fmt.Errorf("unreviewed method-value alias %s", name)
			} else {
				aliases[target.Obj] = true
			}
		}
		return true
	})
	aliasCalls := 0
	ast.Inspect(function.Body, func(node ast.Node) bool {
		name, ok := node.(*ast.Ident)
		if !ok || !aliases[name.Obj] || failure != nil {
			return true
		}
		switch parent := parents[name].(type) {
		case *ast.AssignStmt:
			if len(parent.Lhs) == 1 && parent.Lhs[0] == name && len(parent.Rhs) == 1 {
				if method, ok := parent.Rhs[0].(*ast.SelectorExpr); ok && slices.Contains(spec.legacy, method.Sel.Name) {
					return true
				}
			}
		case *ast.CallExpr:
			if parent.Fun == name && deliveryBoundaryMode(function, parent, parents) == 1 {
				aliasCalls++
				return true
			}
		}
		failure = fmt.Errorf("method-value alias %s escapes or is invoked in bounded mode", name.Name)
		return true
	})
	if failure != nil {
		return failure
	}
	slices.Sort(legacy)
	slices.Sort(owned)
	wantLegacy, wantOwned := slices.Clone(spec.legacy), slices.Clone(spec.owned)
	slices.Sort(wantLegacy)
	slices.Sort(wantOwned)
	if !slices.Equal(legacy, wantLegacy) || !slices.Equal(owned, wantOwned) || spec.alias && (len(aliases) != 1 || aliasCalls != 1) {
		return fmt.Errorf("boundary references changed: legacy=%v owned=%v aliases=%d calls=%d", legacy, owned, len(aliases), aliasCalls)
	}
	return nil
}

func TestScaleP21HotDeliveryLegacyBoundaryAST(t *testing.T) {
	catalog := deliveryBoundaryCatalog{fields: map[string]map[string]ast.Expr{}, result: map[string][]ast.Expr{}}
	functions := map[string]*ast.FuncDecl{}
	for prefix, directory := range map[string]string{"store": ".", "server": "../../cmd/server", "domain": "../domain"} {
		paths, err := filepath.Glob(filepath.Join(directory, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range paths {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, declaration := range file.Decls {
				switch declaration := declaration.(type) {
				case *ast.GenDecl:
					for _, item := range declaration.Specs {
						item, ok := item.(*ast.TypeSpec)
						if !ok {
							continue
						}
						if body, ok := item.Type.(*ast.StructType); ok {
							fields := map[string]ast.Expr{}
							for _, field := range body.Fields.List {
								for _, name := range field.Names {
									fields[name.Name] = field.Type
								}
							}
							catalog.fields[item.Name.Name] = fields
						}
					}
				case *ast.FuncDecl:
					if prefix != "domain" {
						functions[prefix+"/"+filepath.Base(path)+":"+declaration.Name.Name] = declaration
					}
					var results []ast.Expr
					if declaration.Type.Results != nil {
						for _, field := range declaration.Type.Results.List {
							for range max(1, len(field.Names)) {
								results = append(results, field.Type)
							}
						}
					}
					name := declaration.Name.Name
					if declaration.Recv != nil {
						name = deliveryBoundaryTypeName(declaration.Recv.List[0].Type) + "." + name
					}
					catalog.result[name] = results
				}
			}
		}
	}
	for name, function := range functions {
		if err := deliveryBoundaryCheck(catalog, function, deliveryBoundarySites[name]); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name := range deliveryBoundarySites {
		if functions[name] == nil {
			t.Errorf("reviewed delivery boundary disappeared: %s", name)
		}
	}
	implementation := functions["store/notification_delivery_records.go:notificationDeliverySnapshot"]
	guard, ok := implementation.Body.List[0].(*ast.IfStmt)
	if !ok {
		t.Fatal("legacy implementation no longer rejects bounded mode first")
	}
	truth, known := deliveryBoundaryCondition(guard.Cond)
	rejected := false
	if truth == 2 && known && guard.Init == nil && len(guard.Body.List) == 1 {
		if result, ok := guard.Body.List[0].(*ast.ReturnStmt); ok && len(result.Results) == 2 {
			errorName, _ := result.Results[1].(*ast.Ident)
			empty, _ := result.Results[0].(*ast.CompositeLit)
			rejected = errorName != nil && errorName.Name == "ErrBoundedMetadataCoverage" &&
				empty != nil && deliveryBoundaryTypeName(empty.Type) == "Bootstrap" && len(empty.Elts) == 0
		}
	}
	if !rejected {
		t.Fatal("legacy by-value implementation must reject bounded mode before metadata or SQL")
	}
	for index, name := range []string{"NotificationDeliveries", "NotificationDeliverySnapshot"} {
		function := functions["store/notification_delivery_records.go:"+name]
		if len(function.Body.List) != 1 {
			t.Fatalf("%s no longer immediately forwards to the rejection boundary", name)
		}
		result, ok := function.Body.List[0].(*ast.ReturnStmt)
		if !ok || len(result.Results) != 1 {
			t.Fatal("legacy wrapper gained another execution path", name)
		}
		call, ok := result.Results[0].(*ast.CallExpr)
		if !ok || len(call.Args) != 5 {
			t.Fatal("legacy wrapper forwarding changed", name)
		}
		method, _ := call.Fun.(*ast.SelectorExpr)
		full, _ := call.Args[4].(*ast.Ident)
		if method == nil || method.Sel.Name != "notificationDeliverySnapshot" || !deliveryBoundaryStoreReceiver(method.X) || full == nil || full.Name != []string{"false", "true"}[index] {
			t.Fatal("legacy wrapper no longer preserves full/non-rendering selection", name)
		}
		for index, parameter := range []string{"ctx", "workspace", "statuses", "now"} {
			argument, ok := call.Args[index].(*ast.Ident)
			if !ok || argument.Name != parameter {
				t.Fatal("legacy wrapper evaluates or changes an argument before rejection", name)
			}
		}
	}
	t.Log("audited 7 legacy selectors across 6 production boundaries; bounded paths use owned readers")
}

func TestScaleP21HotDeliveryLegacyBoundaryRejectsNewEdges(t *testing.T) {
	catalog := deliveryBoundaryCatalog{
		fields: map[string]map[string]ast.Expr{"server": {"store": &ast.StarExpr{X: ast.NewIdent("SQLiteStore")}}},
		result: map[string][]ast.Expr{},
	}
	spec := deliveryBoundarySpec{legacy: []string{"NotificationDeliveries"}, owned: []string{"BeginDueNotificationDeliveryRead"}}
	aliasSpec := deliveryBoundarySites["server/notifications_workflow.go:dispatchNotificationEmails"]
	alias := `snapshot := s.store.NotificationDeliverySnapshot
		if s.mailer == nil { snapshot = s.store.NotificationDeliveries }
		if s.store.BoundedMetadataEnabled() {
			s.store.BeginDueNotificationDeliveryRead()
			s.store.BeginNotificationDeliveryContextRead()
		} else { snapshot() }`
	for _, test := range []struct {
		name, body string
		spec       deliveryBoundarySpec
		valid      bool
	}{
		{"off-guard", `if s.store.BoundedMetadataEnabled() { s.store.BeginDueNotificationDeliveryRead() } else { s.store.NotificationDeliveries() }`, spec, true},
		{"new-caller", `s.store.NotificationDeliveries()`, deliveryBoundarySpec{}, false},
		{"new-method-value", `consume(s.store.NotificationDeliverySnapshot)`, deliveryBoundarySpec{}, false},
		{"reversed-guard", `if !s.store.BoundedMetadataEnabled() { s.store.BeginDueNotificationDeliveryRead() } else { s.store.NotificationDeliveries() }`, spec, false},
		{"different-store-guard", `if other.BoundedMetadataEnabled() { s.store.BeginDueNotificationDeliveryRead() } else { s.store.NotificationDeliveries() }`, spec, false},
		{"extra-call", `if s.store.BoundedMetadataEnabled() { s.store.BeginDueNotificationDeliveryRead() } else { s.store.NotificationDeliveries(); s.store.NotificationDeliveries() }`, spec, false},
		{"off-method-value-call", alias, aliasSpec, true},
		{"method-value-escape", alias + `; consume(snapshot)`, aliasSpec, false},
		{"method-value-bounded-call", strings.Replace(alias, "} else { snapshot() }", "snapshot() }", 1), aliasSpec, false},
		{"escaping-closure", `if !s.store.BoundedMetadataEnabled() { return func() { s.store.NotificationDeliveries() } }; s.store.BeginDueNotificationDeliveryRead()`, spec, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "probe.go", "package probe; func(s *server) probe() {"+test.body+"}", 0)
			if err != nil {
				t.Fatal(err)
			}
			err = deliveryBoundaryCheck(catalog, file.Decls[0].(*ast.FuncDecl), test.spec)
			if (err == nil) != test.valid {
				t.Fatalf("guard result=%v valid=%v", err, test.valid)
			}
		})
	}
}

func TestScaleP21HotDeliveryLegacyRejectsAndColdFacade(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace, now := f.data.Workspace.URLKey, time.Now().UTC()
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.delivery_boundary", "", nil, func(data *domain.Bootstrap) error {
		data.Projects = append(data.Projects, domain.Project{ID: "delivery-boundary-large", Description: strings.Repeat("cold", 100000)})
		data.Notifications = []domain.Notification{{ID: "delivery-boundary-notification", RecipientID: data.Viewer.ID, Title: "Owned source", CreatedAt: now, UpdatedAt: now}}
		data.NotificationDeliveries = []domain.NotificationDelivery{{ID: "delivery-boundary", NotificationID: data.Notifications[0].ID, RecipientID: data.Viewer.ID, Status: "pending", Channel: "email", CreatedAt: now, UpdatedAt: now}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	for _, full := range []bool{false, true} {
		var reads int
		coldSlot := view.metadataResidency.coldSlot
		ctx := WithSQLStatementObserver(t.Context(), func(string) { reads++ })
		call := view.NotificationDeliveries
		if full {
			call = view.NotificationDeliverySnapshot
		}
		data, err := call(ctx, workspace, []string{"pending"}, now)
		if !errors.Is(err, ErrBoundedMetadataCoverage) || !reflect.DeepEqual(data, domain.Bootstrap{}) || reads != 0 || view.metadataResidency.coldSlot != coldSlot {
			t.Fatalf("direct bounded full=%v returned data, performed SQL, or entered cold hydration: %v", full, err)
		}
		call = f.NotificationDeliveries
		if full {
			call = f.NotificationDeliverySnapshot
		}
		want, err := call(t.Context(), workspace, []string{"pending"}, now)
		if err != nil {
			t.Fatal(err)
		}
		var retired *SQLiteStore
		err = view.withColdMetadataRead(t.Context(), func(ctx context.Context, facade *SQLiteStore) error {
			retired = facade
			if facade.BoundedMetadataEnabled() || len(view.metadataResidency.coldSlot) != 1 {
				t.Fatal("cold facade did not retain its replica slot around the legacy call")
			}
			call := facade.NotificationDeliveries
			if full {
				call = facade.NotificationDeliverySnapshot
			}
			got, err := call(ctx, workspace, []string{"pending"}, now)
			if err != nil {
				return err
			}
			left, _ := json.Marshal(want)
			right, _ := json.Marshal(got)
			if string(left) != string(right) {
				t.Fatal("cold legacy delivery API changed its complete old response")
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if retired.workspaces != nil || retired.coldParent != nil || len(view.metadataResidency.coldSlot) != 0 {
			t.Fatal("cold delivery compatibility retained its facade graph or slot")
		}
	}
	if view.WorkspaceMetadataDecodes() != 0 || view.metadataResidency.work.used != 0 {
		t.Fatal("legacy boundary decoded HOT metadata or leaked admission")
	}
}
