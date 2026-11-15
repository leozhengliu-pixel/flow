package store

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP21SW109LegacyReadReservationBoundary(t *testing.T) {
	const physical, nativeKey = "physical\x00key", "native-large"
	stamp := time.Now()
	body := strings.Repeat("immutable native body\x00", 50000)
	native := domain.Bootstrap{
		Workspace: domain.Workspace{ID: "native-organization", URLKey: "logical-native"},
		Viewer: domain.User{
			ID: "native-viewer", OutOfOfficeUntil: &stamp,
			Via: &domain.ActorBot{ID: "native-via", Name: "keep attribution"},
		},
		Documents: []domain.Document{{ID: "large-document", Content: body, TeamIDs: []string{}}},
		Settings: map[string]any{
			"exact": int64(9007199254740993), "stamp": stamp,
			"bytes": make([]byte, 1<<20), "body": body,
		},
	}
	nested := make([]map[string]any, 2048)
	for index := range nested {
		nested[index] = map[string]any{"body": body, "native": stamp, "empty": []string{}}
	}
	native.Settings["nested"] = nested
	small := domain.Bootstrap{Workspace: domain.Workspace{ID: "organization", URLKey: "logical-public"}}
	values := map[string]domain.Bootstrap{physical: small, nativeKey: native}
	off := &SQLiteStore{workspaces: values, lastWorkspaceKey: physical}
	// The installed-reader harness supplies a bounded store. Its deliberately
	// smaller logical image must be irrelevant to this contextless scalar.
	on := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{
		physical: small, nativeKey: {Workspace: native.Workspace},
	})
	on.mu.Lock()
	on.workspaces, on.lastWorkspaceKey = values, physical
	on.mu.Unlock()
	on.metadataResidency.work.limit = 1
	if HydratedSnapshotReservation(small) >= HydratedSnapshotReservationMaxBytes ||
		HydratedSnapshotReservation(native) <= HydratedSnapshotReservation(small) {
		t.Fatal("fixture does not distinguish small, native-large and maximum reservations")
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	for _, request := range []struct {
		name, workspace, defaultKey string
	}{
		{"resident", physical, physical},
		{"default", "", physical},
		{"native-large", nativeKey, physical},
		{"native-default", "", nativeKey},
		{"missing", "missing", physical},
		{"logical-alias-is-missing", "logical-public", physical},
		{"missing-default", "", "missing"},
	} {
		for _, operation := range []struct {
			name string
			ctx  context.Context
		}{{"live", t.Context()}, {"canceled", canceled}} {
			t.Run(request.name+"/"+operation.name, func(t *testing.T) {
				// There is intentionally no context parameter on the legacy
				// accessor. A canceled containing operation cannot change it.
				if operation.name == "canceled" && operation.ctx.Err() != context.Canceled {
					t.Fatal("containing operation is not canceled")
				}
				off.lastWorkspaceKey = request.defaultKey
				on.mu.Lock()
				on.lastWorkspaceKey = request.defaultKey
				on.mu.Unlock()
				selected := request.workspace
				if selected == "" {
					selected = request.defaultKey
				}
				want := HydratedSnapshotReservationMaxBytes
				if data, found := values[selected]; found {
					want = HydratedSnapshotReservation(data)
				}
				if got := off.WorkspaceReadSnapshotReservation(request.workspace); got != want {
					t.Fatal("off scalar differs from literal resident reservation", got, want)
				}
				disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
				if got := on.WorkspaceReadSnapshotReservation(request.workspace); got != HydratedSnapshotReservationMaxBytes {
					t.Fatal("bounded scalar did not return the fixed maximum", got)
				}
				if on.MetadataWorkUsedForTesting() != 0 || on.WorkspaceMetadataDecodes() != 0 ||
					metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors {
					t.Fatal("bounded scalar opened or retained metadata work")
				}
			})
		}
	}
	if values[nativeKey].Viewer.Via.ID != "native-via" ||
		values[nativeKey].Settings["exact"] != int64(9007199254740993) ||
		values[nativeKey].Settings["stamp"].(time.Time) != stamp ||
		values[nativeKey].Documents[0].Content != body {
		t.Fatal("reservation mutated or normalized native resident values")
	}
	t.Run("fixed-maximum-guard-precedes-traversal", scaleP21SW109ReservationGuard)
}

func scaleP21SW109ReservationGuard(t *testing.T) {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "scale_p15_snapshot_reservation.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var function *ast.FuncDecl
	for _, declaration := range file.Decls {
		candidate, ok := declaration.(*ast.FuncDecl)
		if ok && candidate.Name.Name == "WorkspaceReadSnapshotReservation" {
			function = candidate
			break
		}
	}
	if function == nil || function.Body == nil || function.Recv == nil ||
		len(function.Recv.List) != 1 || len(function.Recv.List[0].Names) != 1 {
		t.Fatal("missing reservation receiver/body")
	}
	receiver := function.Recv.List[0].Names[0].Name
	guardIndex := -1
	for index, statement := range function.Body.List {
		guard, ok := statement.(*ast.IfStmt)
		if !ok || !scaleP21SW109BoundedGuard(guard.Cond, receiver) {
			continue
		}
		if guard.Init != nil || guard.Else != nil || len(guard.Body.List) != 1 {
			t.Fatal("bounded guard can perform extra work or fall through")
		}
		result, ok := guard.Body.List[0].(*ast.ReturnStmt)
		if !ok || len(result.Results) != 1 || !scaleP21SW109Ident(result.Results[0], "HydratedSnapshotReservationMaxBytes") {
			t.Fatal("bounded guard does not directly return the fixed maximum")
		}
		guardIndex = index
		break
	}
	if guardIndex < 0 {
		t.Fatal("bounded reservation guard is absent")
	}
	// No helper, closure or iteration before the guard may inspect a resident
	// graph. Exact registry lookup/default resolution and locking are scalar.
	for _, statement := range function.Body.List[:guardIndex] {
		ast.Inspect(statement, func(node ast.Node) bool {
			switch node := node.(type) {
			case *ast.RangeStmt, *ast.ForStmt:
				t.Error("reservation traverses metadata before the bounded guard")
			case *ast.CallExpr:
				method, ok := node.Fun.(*ast.SelectorExpr)
				if !ok || len(node.Args) != 0 || method.Sel.Name != "RLock" && method.Sel.Name != "RUnlock" {
					t.Error("reservation calls a helper before the bounded guard")
					break
				}
				mutex, ok := method.X.(*ast.SelectorExpr)
				if !ok || mutex.Sel.Name != "mu" || !scaleP21SW109Ident(mutex.X, receiver) {
					t.Error("reservation calls an unproved method before the bounded guard")
				}
			}
			return true
		})
	}
	if guardIndex != len(function.Body.List)-2 {
		t.Fatal("bounded guard is not immediately before the literal off return")
	}
	result, ok := function.Body.List[len(function.Body.List)-1].(*ast.ReturnStmt)
	if !ok || len(result.Results) != 1 {
		t.Fatal("missing literal off reservation return")
	}
	call, ok := result.Results[0].(*ast.CallExpr)
	if !ok || !scaleP21SW109Ident(call.Fun, "HydratedSnapshotReservation") || len(call.Args) != 1 {
		t.Fatal("off path no longer invokes the literal reservation helper")
	}
}

func scaleP21SW109Ident(expression ast.Expr, name string) bool {
	value, ok := expression.(*ast.Ident)
	return ok && value.Name == name
}

func scaleP21SW109BoundedGuard(expression ast.Expr, receiver string) bool {
	if grouped, ok := expression.(*ast.ParenExpr); ok {
		return scaleP21SW109BoundedGuard(grouped.X, receiver)
	}
	binary, ok := expression.(*ast.BinaryExpr)
	if !ok {
		return false
	}
	if binary.Op == token.LOR {
		return scaleP21SW109BoundedGuard(binary.X, receiver) || scaleP21SW109BoundedGuard(binary.Y, receiver)
	}
	if binary.Op != token.NEQ {
		return false
	}
	residency := func(expression ast.Expr) bool {
		field, ok := expression.(*ast.SelectorExpr)
		return ok && field.Sel.Name == "metadataResidency" && scaleP21SW109Ident(field.X, receiver)
	}
	return residency(binary.X) && scaleP21SW109Ident(binary.Y, "nil") ||
		residency(binary.Y) && scaleP21SW109Ident(binary.X, "nil")
}
