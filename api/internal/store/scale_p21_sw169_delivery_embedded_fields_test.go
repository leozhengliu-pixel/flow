package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func scaleP21SW169Expr(t *testing.T, source string) ast.Expr {
	t.Helper()
	expression, err := parser.ParseExpr(source)
	if err != nil {
		t.Fatal(err)
	}
	return expression
}

func scaleP21SW169Function(t *testing.T, source string) *ast.FuncDecl {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "sw169-snippet.go", "package store\n"+source, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Decls) != 1 {
		t.Fatal("boundary fixture must declare exactly one function")
	}
	function, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatal("boundary fixture is not a function")
	}
	return function
}

func scaleP21SW169Catalog(t *testing.T) deliveryBoundaryCatalog {
	t.Helper()
	expr := func(source string) ast.Expr { return scaleP21SW169Expr(t, source) }
	return deliveryBoundaryCatalog{
		fields: map[string]map[string]ast.Expr{
			"Bootstrap":            {"NotificationDeliveries": expr("[]domain.NotificationDelivery")},
			"IssueBootstrapRead":   {"Data": expr("domain.Bootstrap")},
			"GeneralBootstrapRead": {},
			"ValueBootstrapRead":   {},
			"OuterRead":            {},
			"SQLiteStore":          {},
			"server":               {"store": expr("*SQLiteStore")},
		},
		embedded: map[string][]ast.Expr{
			"GeneralBootstrapRead": {expr("*IssueBootstrapRead")},
			"ValueBootstrapRead":   {expr("IssueBootstrapRead")},
			"OuterRead":            {expr("*GeneralBootstrapRead")},
		},
		result: map[string][]ast.Expr{
			"SQLiteStore.BeginGeneralBootstrapRead": {expr("*GeneralBootstrapRead"), expr("error")},
		},
	}
}

func TestScaleP21SW169DeliveryEmbeddedFieldResolution(t *testing.T) {
	type fixture struct {
		name, source, receiver string
		setup                  func(*deliveryBoundaryCatalog)
		allowed                bool
	}
	for _, test := range []fixture{
		{
			name: "pointer-embedded-reader-pointer-receiver",
			source: `func target(read *GeneralBootstrapRead) {
				_ = read.Data.NotificationDeliveries
			}`,
			receiver: "Bootstrap", allowed: true,
		},
		{
			name: "pointer-embedded-reader-value-receiver",
			source: `func target(read GeneralBootstrapRead) {
				_ = read.Data.NotificationDeliveries
			}`,
			receiver: "Bootstrap", allowed: true,
		},
		{
			name: "value-embedded-reader",
			source: `func target(read *ValueBootstrapRead) {
				_ = read.Data.NotificationDeliveries
			}`,
			receiver: "Bootstrap", allowed: true,
		},
		{
			name: "multiple-embedding-levels",
			source: `func target(read *OuterRead) {
				_ = read.Data.NotificationDeliveries
			}`,
			receiver: "Bootstrap", allowed: true,
		},
		{
			name: "local-data-alias",
			source: `func target(read *GeneralBootstrapRead) {
				data := read.Data
				_ = data.NotificationDeliveries
			}`,
			receiver: "Bootstrap", allowed: true,
		},
		{
			name: "reader-from-constructor-result",
			source: `func target(s *SQLiteStore) {
				read, _ := s.BeginGeneralBootstrapRead()
				_ = read.Data.NotificationDeliveries
			}`,
			receiver: "Bootstrap", allowed: true,
		},
		{
			name: "direct-bootstrap-field-shadows-unknown-embedded-field",
			source: `func target(read *DirectRead) {
				_ = read.Data.NotificationDeliveries
			}`,
			setup: func(c *deliveryBoundaryCatalog) {
				c.fields["DirectRead"] = map[string]ast.Expr{"Data": scaleP21SW169Expr(t, "domain.Bootstrap")}
				c.embedded["DirectRead"] = []ast.Expr{scaleP21SW169Expr(t, "*UnknownRead")}
			},
			receiver: "Bootstrap", allowed: true,
		},
		{
			name: "direct-store-field-shadows-promoted-bootstrap",
			source: `func target(read *DirectRead) {
				_ = read.Data.NotificationDeliveries
			}`,
			setup: func(c *deliveryBoundaryCatalog) {
				c.fields["DirectRead"] = map[string]ast.Expr{"Data": scaleP21SW169Expr(t, "*SQLiteStore")}
				c.embedded["DirectRead"] = []ast.Expr{scaleP21SW169Expr(t, "*IssueBootstrapRead")}
			},
			receiver: "SQLiteStore",
		},
		{
			name: "shallowest-promoted-field-wins",
			source: `func target(read *ShallowRead) {
				_ = read.Data.NotificationDeliveries
			}`,
			setup: func(c *deliveryBoundaryCatalog) {
				c.fields["ShallowRead"] = map[string]ast.Expr{}
				c.fields["DeepRead"] = map[string]ast.Expr{}
				c.fields["StoreData"] = map[string]ast.Expr{"Data": scaleP21SW169Expr(t, "*SQLiteStore")}
				// Visit the losing deep branch first to catch a DFS shortcut.
				c.embedded["ShallowRead"] = []ast.Expr{scaleP21SW169Expr(t, "*DeepRead"), scaleP21SW169Expr(t, "IssueBootstrapRead")}
				c.embedded["DeepRead"] = []ast.Expr{scaleP21SW169Expr(t, "StoreData")}
			},
			receiver: "Bootstrap", allowed: true,
		},
		{
			name: "ambiguous-peer-different-data-types",
			source: `func target(read *AmbiguousRead) {
				_ = read.Data.NotificationDeliveries
			}`,
			setup: func(c *deliveryBoundaryCatalog) {
				c.fields["AmbiguousRead"] = map[string]ast.Expr{}
				c.fields["StoreData"] = map[string]ast.Expr{"Data": scaleP21SW169Expr(t, "*SQLiteStore")}
				c.embedded["AmbiguousRead"] = []ast.Expr{scaleP21SW169Expr(t, "IssueBootstrapRead"), scaleP21SW169Expr(t, "*StoreData")}
			},
		},
		{
			name: "ambiguous-peer-identical-data-types",
			source: `func target(read *AmbiguousRead) {
				_ = read.Data.NotificationDeliveries
			}`,
			setup: func(c *deliveryBoundaryCatalog) {
				c.fields["AmbiguousRead"] = map[string]ast.Expr{}
				c.fields["OtherBootstrapRead"] = map[string]ast.Expr{"Data": scaleP21SW169Expr(t, "domain.Bootstrap")}
				c.embedded["AmbiguousRead"] = []ast.Expr{scaleP21SW169Expr(t, "IssueBootstrapRead"), scaleP21SW169Expr(t, "OtherBootstrapRead")}
			},
		},
		{
			name: "ambiguous-diamond-keeps-both-paths",
			source: `func target(read *DiamondRead) {
				_ = read.Data.NotificationDeliveries
			}`,
			setup: func(c *deliveryBoundaryCatalog) {
				for _, name := range []string{"DiamondRead", "LeftRead", "RightRead"} {
					c.fields[name] = map[string]ast.Expr{}
				}
				c.embedded["DiamondRead"] = []ast.Expr{scaleP21SW169Expr(t, "LeftRead"), scaleP21SW169Expr(t, "RightRead")}
				c.embedded["LeftRead"] = []ast.Expr{scaleP21SW169Expr(t, "*IssueBootstrapRead")}
				c.embedded["RightRead"] = []ast.Expr{scaleP21SW169Expr(t, "IssueBootstrapRead")}
			},
		},
		{
			name: "cyclic-embedding-stays-unknown",
			source: `func target(read *CycleA) {
				_ = read.Data.NotificationDeliveries
			}`,
			setup: func(c *deliveryBoundaryCatalog) {
				c.fields["CycleA"], c.fields["CycleB"] = map[string]ast.Expr{}, map[string]ast.Expr{}
				c.embedded["CycleA"] = []ast.Expr{scaleP21SW169Expr(t, "*CycleB")}
				c.embedded["CycleB"] = []ast.Expr{scaleP21SW169Expr(t, "*CycleA")}
			},
		},
		{
			name: "unresolved-embedding-stays-unknown",
			source: `func target(read *UnresolvedRead) {
				_ = read.Data.NotificationDeliveries
			}`,
			setup: func(c *deliveryBoundaryCatalog) {
				c.fields["UnresolvedRead"] = map[string]ast.Expr{}
				c.embedded["UnresolvedRead"] = []ast.Expr{scaleP21SW169Expr(t, "*NotInCatalog")}
			},
		},
		{
			name: "unresolved-peer-cannot-prove-promotion-unambiguous",
			source: `func target(read *UnresolvedRead) {
				_ = read.Data.NotificationDeliveries
			}`,
			setup: func(c *deliveryBoundaryCatalog) {
				c.fields["UnresolvedRead"] = map[string]ast.Expr{}
				c.embedded["UnresolvedRead"] = []ast.Expr{scaleP21SW169Expr(t, "IssueBootstrapRead"), scaleP21SW169Expr(t, "*NotInCatalog")}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			catalog := scaleP21SW169Catalog(t)
			if test.setup != nil {
				test.setup(&catalog)
			}
			function := scaleP21SW169Function(t, test.source)
			var selector *ast.SelectorExpr
			ast.Inspect(function, func(node ast.Node) bool {
				if candidate, ok := node.(*ast.SelectorExpr); ok && candidate.Sel.Name == "NotificationDeliveries" {
					selector = candidate
				}
				return true
			})
			if selector == nil {
				t.Fatal("delivery selector fixture missing")
			}
			if receiver := deliveryBoundaryTypeName(catalog.expressionType(selector.X, 0, 0)); receiver != test.receiver {
				t.Fatalf("promoted receiver=%q want=%q", receiver, test.receiver)
			}
			if test.allowed {
				if _, slice := catalog.expressionType(selector, 0, 0).(*ast.ArrayType); !slice {
					t.Fatal("plain Bootstrap.NotificationDeliveries did not resolve as a slice field")
				}
			}
			err := deliveryBoundaryCheck(catalog, function, deliveryBoundarySpec{})
			if (err == nil) != test.allowed {
				t.Fatalf("boundary acceptance=%t want=%t: %v", err == nil, test.allowed, err)
			}
		})
	}
}

func TestScaleP21SW169DeliveryMethodsKeepOriginalBoundaries(t *testing.T) {
	for _, test := range []struct {
		name, source string
		spec         deliveryBoundarySpec
		allowed      bool
	}{
		{
			name:   "real-store-call-unguarded-rejected",
			source: `func target(s *SQLiteStore) { _ = s.NotificationDeliveries() }`,
		},
		{
			name:   "real-store-method-value-rejected",
			source: `func target(s *SQLiteStore) { _ = s.NotificationDeliveries }`,
		},
		{
			name:   "server-store-call-rejected",
			source: `func target(s *server) { _ = s.store.NotificationDeliveries() }`,
		},
		{
			name:   "off-call-needs-original-reviewed-spec",
			source: `func target(s *SQLiteStore) { if !s.BoundedMetadataEnabled() { _ = s.NotificationDeliveries() } }`,
		},
		{
			name:    "original-reviewed-off-call-allowed",
			source:  `func target(s *SQLiteStore) { if !s.BoundedMetadataEnabled() { _ = s.NotificationDeliveries() } }`,
			spec:    deliveryBoundarySpec{legacy: []string{"NotificationDeliveries"}},
			allowed: true,
		},
		{
			name:    "original-reviewed-server-off-call-allowed",
			source:  `func target(s *server) { if !s.store.BoundedMetadataEnabled() { _ = s.store.NotificationDeliveries() } }`,
			spec:    deliveryBoundarySpec{legacy: []string{"NotificationDeliveries"}},
			allowed: true,
		},
		{
			name:   "reviewed-spec-cannot-allow-bounded-call",
			source: `func target(s *SQLiteStore) { if s.BoundedMetadataEnabled() { _ = s.NotificationDeliveries() } }`,
			spec:   deliveryBoundarySpec{legacy: []string{"NotificationDeliveries"}},
		},
		{
			name: "off-method-value-needs-original-alias-spec",
			source: `func target(s *SQLiteStore) {
				snapshot := s.NotificationDeliveries
				if !s.BoundedMetadataEnabled() { _ = snapshot() }
			}`,
			spec: deliveryBoundarySpec{legacy: []string{"NotificationDeliveries"}},
		},
		{
			name: "original-reviewed-off-alias-allowed",
			source: `func target(s *SQLiteStore) {
				snapshot := s.NotificationDeliveries
				if !s.BoundedMetadataEnabled() { _ = snapshot() }
			}`,
			spec:    deliveryBoundarySpec{legacy: []string{"NotificationDeliveries"}, alias: true},
			allowed: true,
		},
		{
			name: "reviewed-alias-cannot-escape",
			source: `func target(s *SQLiteStore) {
				snapshot := s.NotificationDeliveries
				consume(snapshot)
			}`,
			spec: deliveryBoundarySpec{legacy: []string{"NotificationDeliveries"}, alias: true},
		},
		{
			name:   "snapshot-method-still-rejected",
			source: `func target(s *SQLiteStore) { _ = s.NotificationDeliverySnapshot() }`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			catalog := scaleP21SW169Catalog(t)
			function := scaleP21SW169Function(t, test.source)
			err := deliveryBoundaryCheck(catalog, function, test.spec)
			if (err == nil) != test.allowed {
				t.Fatalf("method boundary acceptance=%t want=%t: %v", err == nil, test.allowed, err)
			}
		})
	}
}
