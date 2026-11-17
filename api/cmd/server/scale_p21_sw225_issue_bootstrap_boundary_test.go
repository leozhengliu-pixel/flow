package main

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

func scaleP21SW225Node(node ast.Node) string {
	if node == nil {
		return ""
	}
	var out bytes.Buffer
	if err := format.Node(&out, token.NewFileSet(), node); err != nil {
		return "<invalid>"
	}
	return out.String()
}

func scaleP21SW225Function(fn *ast.FuncDecl) string {
	if fn.Recv == nil {
		return fn.Name.Name
	}
	receiver := fn.Recv.List[0].Type
	if pointer, ok := receiver.(*ast.StarExpr); ok {
		receiver = pointer.X
	}
	return scaleP21SW225Node(receiver) + "." + fn.Name.Name
}

func scaleP21SW225Statements(block *ast.BlockStmt, want ...string) bool {
	if block == nil || len(block.List) < len(want) {
		return false
	}
	for i, text := range want {
		if scaleP21SW225Node(block.List[i]) != text {
			return false
		}
	}
	return true
}

func scaleP21SW225OwnedBranch(stmt ast.Stmt, init, condition, call string) bool {
	branch, ok := stmt.(*ast.IfStmt)
	if !ok || branch.Else != nil || scaleP21SW225Node(branch.Init) != init ||
		scaleP21SW225Node(branch.Cond) != condition || len(branch.Body.List) != 2 {
		return false
	}
	exit, ok := branch.Body.List[1].(*ast.ReturnStmt)
	return ok && len(exit.Results) == 0 && scaleP21SW225Node(branch.Body.List[0]) == call
}

func scaleP21SW225BootstrapTypeReference(file *ast.File, selector *ast.SelectorExpr) bool {
	if selector.Sel.Name != "Bootstrap" {
		return false
	}
	qualifier, ok := selector.X.(*ast.Ident)
	if !ok || qualifier.Obj != nil {
		return false
	}
	for _, imported := range file.Imports {
		path, err := strconv.Unquote(imported.Path.Value)
		if err != nil || path != "flow/api/internal/domain" {
			continue
		}
		name := "domain"
		if imported.Name != nil {
			name = imported.Name.Name
		}
		return qualifier.Name == name
	}
	return false
}

// Dynamic generic GETs can spell the reserved bootstrap path. Their result
// must remain a statically declared Issue, rejected by the gateway's checked
// Bootstrap assertion before the in-process handler can be constructed.
func scaleP21SW225IssueResult(fn *ast.FuncDecl, result ast.Expr) bool {
	address, ok := result.(*ast.UnaryExpr)
	if !ok || address.Op != token.AND {
		return false
	}
	name, ok := address.X.(*ast.Ident)
	if !ok {
		return false
	}
	declarations := 0
	valid := true
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.ValueSpec:
			for _, candidate := range node.Names {
				if candidate.Name == name.Name {
					declarations++
					valid = valid && scaleP21SW225Node(node.Type) == "domain.Issue"
				}
			}
		case *ast.AssignStmt:
			if node.Tok == token.DEFINE {
				for _, lhs := range node.Lhs {
					if candidate, ok := lhs.(*ast.Ident); ok && candidate.Name == name.Name {
						valid = false
					}
				}
			}
		}
		return true
	})
	return valid && declarations == 1
}

func TestScaleP21SW225IssueBootstrapLegacyBoundaries(t *testing.T) {
	// The two complete typed-bootstrap callers remain committed COLD. The
	// allowlist preserves their independent cold-routing obligations.
	coldBootstrap := map[string]bool{
		"server.executeChatGroupRequest": true,
		"server.chatLinkPreviewFor":      true,
	}
	for file, name := range map[string]string{
		"chat_group_requests.json": "executeChatGroupRequest",
		"chat_unfurl.json":         "chatLinkPreviewFor",
	} {
		raw, err := os.ReadFile("../../internal/store/scale_p21_inventory/server/" + file)
		if err != nil {
			t.Fatal(err)
		}
		var shard struct {
			Consumers map[string]struct {
				Temperature string `json:"temperature"`
			} `json:"consumers"`
		}
		if err := json.Unmarshal(raw, &shard); err != nil {
			t.Fatal(err)
		}
		if shard.Consumers[name].Temperature != "cold" {
			t.Fatalf("%s is no longer committed COLD; bootstrap reachability requires review", name)
		}
	}
	expected := map[string]int{
		"issueRecordsBootstrapData/server.issueRecordsBootstrap":             1,
		"issueRecordsBootstrapData/server.chatAuthorizedMutation":            1,
		"issueRecordsBootstrapBounded/server.issueRecordsBootstrap":          1,
		"beginIssueRecordsBootstrapRead/server.issueRecordsBootstrapBounded": 1,
		"beginIssueRecordsBootstrapRead/server.chatBootstrapHeaders":         1,
		"BeginIssueBootstrapRead/server.beginIssueRecordsBootstrapRead":      1,
		"chatAuthorizedMutation/server.chatRunQuickAction":                   1,
		"chatAuthorizedMutation/server.chatDMComment":                        1,
		"chatAuthorizedMutation/server.chatDMCreateIssue":                    1,
		"chatAuthorizedMutation/server.executeChatGroupRequest":              2,
		"chatAuthorizedMutation/server.processChatThreadReply":               1,
		"chatAuthorizedMutation/server.chatCreateFromMessage":                1,
		"chatAuthorizedMutation/server.chatFileRequest":                      1,
		"chatAuthorizedMutation/server.chatLinkPreviewFor":                   2,
		"chatAuthorizedMutation/server.chatExecuteAction":                    4,
		"chatAuthorizedMutation/server.chatAuthorizedBootstrapHeaders":       1,
		"chatAuthorizedMutation/server.chatAuthorizedWorkflowHeaders":        1,
		"chatAuthorizedMutation/server.chatAuthorizedPointRead":              2,
		"chatAuthorizedPointRead/server.chatPersonalNotificationCard":        1,
		"chatAuthorizedPointRead/server.chatRunQuickAction":                  1,
		"chatAuthorizedPointRead/server.chatSendUpdateForm":                  1,
		"chatAuthorizedPointRead/server.chatDMComment":                       1,
		"chatAuthorizedPointRead/server.chatDMLinkPreview":                   1,
		"chatAuthorizedPointRead/server.processChatThreadReply":              1,
		"chatAuthorizedPointRead/server.processChatMessageResult":            1,
		"chatAuthorizedPointRead/server.chatMessageIntakeExists":             1,
		"chatAuthorizedPointRead/server.processChatNotification":             1,
	}
	targets := map[string]bool{
		"issueRecordsBootstrapData": true, "issueRecordsBootstrapBounded": true,
		"beginIssueRecordsBootstrapRead": true, "chatAuthorizedMutation": true,
		"chatAuthorizedPointRead": true, "BeginIssueBootstrapRead": true,
	}
	owned := map[string]bool{
		"server.issueRecordsBootstrapBounded":   true,
		"server.beginIssueRecordsBootstrapRead": true,
		"server.chatBootstrapHeaders":           true,
	}
	legacyReads := map[string]bool{
		"issueRecordsBootstrapData": true, "WorkspaceMetadataForRead": true,
		"PagedWorkspaceMetadata": true, "WorkspaceMetadata": true,
		"WorkspaceMetadataFields": true, "BootstrapForContext": true,
		"Bootstrap": true, "BootstrapFor": true, "BootstrapForUser": true,
	}
	counts := map[string]int{}
	functions := map[string]*ast.FuncDecl{}
	files, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".go") || strings.HasSuffix(file.Name(), "_test.go") {
			continue
		}
		raw, err := os.ReadFile(file.Name())
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), file.Name(), raw, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range parsed.Decls {
			fn, isFunction := decl.(*ast.FuncDecl)
			name := "package-level"
			if isFunction {
				name = scaleP21SW225Function(fn)
				if strings.HasPrefix(name, "server.") {
					if functions[name] != nil {
						t.Fatalf("duplicate function %s", name)
					}
					functions[name] = fn
				}
			}
			var ancestors []ast.Node
			ast.Inspect(decl, func(node ast.Node) bool {
				if node == nil {
					ancestors = ancestors[:len(ancestors)-1]
					return false
				}
				ancestors = append(ancestors, node)
				selector, ok := node.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if owned[name] && legacyReads[selector.Sel.Name] && !scaleP21SW225BootstrapTypeReference(parsed, selector) {
					t.Errorf("%s regained a legacy read %s", name, selector.Sel.Name)
				}
				if !targets[selector.Sel.Name] {
					return true
				}
				key := selector.Sel.Name + "/" + name
				counts[key]++
				if !isFunction || len(ancestors) < 2 {
					t.Errorf("%s reference requires explicit reachability review", key)
					return true
				}
				call, direct := ancestors[len(ancestors)-2].(*ast.CallExpr)
				receiver := "s"
				if selector.Sel.Name == "BeginIssueBootstrapRead" {
					receiver = "s.store"
				}
				if !direct || call.Fun != selector || scaleP21SW225Node(selector.X) != receiver {
					t.Errorf("%s captures a method or changes its receiver; explicit review required", key)
					return true
				}
				if expected[key] == 0 {
					t.Errorf("new bootstrap boundary reference %s", key)
				}
				if selector.Sel.Name != "chatAuthorizedMutation" {
					return true
				}
				if len(call.Args) != 7 {
					t.Errorf("%s changed gateway arguments", key)
					return true
				}
				method, path := scaleP21SW225Node(call.Args[3]), scaleP21SW225Node(call.Args[4])
				if method == "http.MethodPost" || method == "http.MethodPatch" {
					return true // The complete bootstrap branch requires GET.
				}
				if method != "http.MethodGet" {
					t.Errorf("%s has an unreviewed dynamic method %s", key, method)
					return true
				}
				if path == `"/api/issue-records/bootstrap"` {
					if coldBootstrap[name] || name == "server.chatAuthorizedBootstrapHeaders" || name == "server.chatAuthorizedWorkflowHeaders" {
						return true
					}
					t.Errorf("%s reaches complete typed bootstrap without an owned kind", key)
					return true
				}
				if name == "server.chatAuthorizedPointRead" && path == "path" {
					return true // Its exact marker dispatch and gateway refusal are checked below.
				}
				if coldBootstrap[name] {
					return true
				}
				if !scaleP21SW225IssueResult(fn, call.Args[6]) {
					t.Errorf("%s dynamic GET can reach typed bootstrap without a statically checked Issue result", key)
				}
				return true
			})
		}
	}
	for key, want := range expected {
		if counts[key] != want {
			t.Errorf("bootstrap callset changed: %s count=%d want=%d", key, counts[key], want)
		}
	}
	require := func(name string) *ast.FuncDecl {
		t.Helper()
		fn := functions["server."+name]
		if fn == nil || fn.Body == nil {
			t.Fatalf("missing server.%s", name)
		}
		return fn
	}
	route := require("issueRecordsBootstrap")
	if len(route.Body.List) == 0 || !scaleP21SW225OwnedBranch(route.Body.List[0], "", "s.store.BoundedMetadataEnabled()", "s.issueRecordsBootstrapBounded(w, r)") {
		t.Error("HTTP bootstrap no longer returns through its first owned bounded dispatch")
	}
	for name, kind := range map[string]string{
		"chatAuthorizedBootstrapHeaders": "chatDMHeaders",
		"chatAuthorizedWorkflowHeaders":  "chatWorkflowHeaders",
	} {
		fn := require(name)
		want := "if s.store.BoundedMetadataEnabled() {\n\tctx = context.WithValue(ctx, chatBootstrapHeadersKey{}, " + kind + ")\n}"
		if len(fn.Body.List) != 2 || !scaleP21SW225Statements(fn.Body, want, `return s.chatAuthorizedMutation(ctx, workspace, userID, http.MethodGet, "/api/issue-records/bootstrap", nil, result)`) {
			t.Errorf("%s lost its bounded kind before entering the gateway", name)
		}
	}
	point := require("chatAuthorizedPointRead")
	if len(point.Body.List) != 2 || !scaleP21SW225Statements(point.Body,
		"if !s.store.BoundedMetadataEnabled() {\n\treturn s.chatAuthorizedMutation(ctx, workspace, userID, http.MethodGet, path, nil, result)\n}",
		"return s.chatAuthorizedMutation(context.WithValue(ctx, chatPointReadKey{}, true), workspace, userID, http.MethodGet, path, nil, result)") {
		t.Error("point adapter lost its bounded point marker")
	}
	gateway := require("chatAuthorizedMutation")
	bootstrapCases, pointGuards := 0, 0
	ast.Inspect(gateway.Body, func(node ast.Node) bool {
		if clause, ok := node.(*ast.CaseClause); ok && len(clause.List) == 1 &&
			scaleP21SW225Node(clause.List[0]) == `path == "/api/issue-records/bootstrap" && method == http.MethodGet` {
			bootstrapCases++
			block := &ast.BlockStmt{List: clause.Body}
			if len(block.List) != 5 || !scaleP21SW225Statements(block,
				"metadata, ok := result.(*domain.Bootstrap)",
				"if result != nil && !ok {\n\treturn errors.New(\"unsupported chat operation\")\n}") {
				t.Error("bootstrap gateway lost its early checked result type")
				return true
			}
			assign, ok := block.List[4].(*ast.AssignStmt)
			if !ok || len(assign.Rhs) != 1 || len(assign.Lhs) != 1 || scaleP21SW225Node(assign.Lhs[0]) != "handler" {
				t.Error("bootstrap gateway changed handler construction")
				return true
			}
			handler, ok := assign.Rhs[0].(*ast.FuncLit)
			if !ok || len(handler.Body.List) != 3 ||
				!scaleP21SW225OwnedBranch(handler.Body.List[1],
					"kind, selected := ctx.Value(chatBootstrapHeadersKey{}).(chatBootstrapHeadersKind)",
					"selected && s.store.BoundedMetadataEnabled()",
					"bootstrapErr = s.chatBootstrapHeaders(w, r, metadata, kind)") {
				t.Error("typed HOT bootstrap no longer returns through its owned kind")
				return true
			}
			if !scaleP21SW225OwnedBranch(handler.Body.List[0], "",
				`result == nil && internalRecordsEnabled("FLOW_INTERNAL_CHAT_RECORDS")`,
				"w.WriteHeader(http.StatusOK)") {
				t.Error("consent-only branch changed; bootstrap reachability needs review")
			}
			legacy, ok := handler.Body.List[2].(*ast.IfStmt)
			if !ok || scaleP21SW225Node(legacy.Init) != "data, ok := s.issueRecordsBootstrapData(w, r)" {
				t.Error("legacy bootstrap call no longer follows both returning dispatches")
			}
		}
		if branch, ok := node.(*ast.IfStmt); ok &&
			scaleP21SW225Node(branch.Init) == "point, _ := ctx.Value(chatPointReadKey{}).(bool)" &&
			scaleP21SW225Node(branch.Cond) == "point && s.store.BoundedMetadataEnabled()" {
			pointGuards++
			if !scaleP21SW225Statements(branch.Body,
				"if method != http.MethodGet || inProcess {\n\treturn store.ErrBoundedMetadataCoverage\n}") {
				t.Error("point gateway no longer refuses in-process bootstrap before dispatch")
			}
		}
		return true
	})
	if bootstrapCases != 1 || pointGuards != 1 {
		t.Errorf("gateway boundaries changed: bootstrap cases=%d point guards=%d", bootstrapCases, pointGuards)
	}
	stream := require("issueRecordsBootstrapBounded")
	if !scaleP21SW225Statements(stream.Body,
		"started := time.Now()",
		"read, ok := s.beginIssueRecordsBootstrapRead(w, r)",
		"if !ok {\n\treturn\n}",
		"defer read.Close()") {
		t.Error("HTTP owned entry lost its read and containing response lifetime")
	}
	chat := require("chatBootstrapHeaders")
	if !scaleP21SW225Statements(chat.Body,
		"if kind != chatDMHeaders && kind != chatWorkflowHeaders {\n\treturn store.ErrBoundedMetadataCoverage\n}",
		"read, ok := s.beginIssueRecordsBootstrapRead(w, r)",
		"if !ok {\n\treturn nil\n}") {
		t.Error("chat owned entry lost its closed kind or shared owned reader")
	}
	transfer, destination := -1, -1
	for i, stmt := range chat.Body.List {
		if scaleP21SW225Node(stmt) == "if err := read.TransferOutput(r.Context()); err != nil {\n\treturn err\n}" {
			transfer = i
		}
		if scaleP21SW225Node(stmt) == "*result = data" {
			destination = i
		}
	}
	if transfer < 0 || destination != transfer+1 {
		t.Error("chat bootstrap destination is no longer dominated by successful output transfer")
	}
}

func TestScaleP21SW225OwnedBranchRejectsFallthrough(t *testing.T) {
	for _, cell := range []struct {
		name, body string
		want       bool
	}{
		{"owned-return", "if enabled { owned(); return }", true},
		{"fallthrough", "if enabled { owned() }", false},
		{"nested-return", "if enabled { owned(); if other { return } }", false},
		{"wrong-condition", "if other { owned(); return }", false},
		{"wrong-call", "if enabled { legacy(); return }", false},
		{"extra-before-return", "if enabled { owned(); legacy(); return }", false},
		{"else-path", "if enabled { owned(); return } else { legacy() }", false},
	} {
		t.Run(cell.name, func(t *testing.T) {
			parsed, err := parser.ParseFile(token.NewFileSet(), "probe.go", "package probe; func f() { "+cell.body+" }", 0)
			if err != nil {
				t.Fatal(err)
			}
			fn := parsed.Decls[0].(*ast.FuncDecl)
			if got := scaleP21SW225OwnedBranch(fn.Body.List[0], "", "enabled", "owned()"); got != cell.want {
				t.Fatalf("owned branch=%v want=%v", got, cell.want)
			}
		})
	}
}

func TestScaleP21SW225DynamicGETResultRejectsBootstrap(t *testing.T) {
	for _, cell := range []struct {
		declaration string
		want        bool
	}{
		{"var issue domain.Issue", true},
		{"var issue domain.Bootstrap", false},
		{"issue := domain.Issue{}", false},
		{"var issue domain.Issue; if true { issue := domain.Bootstrap{}; _ = issue }", false},
	} {
		t.Run(strconv.FormatBool(cell.want)+"/"+cell.declaration, func(t *testing.T) {
			parsed, err := parser.ParseFile(token.NewFileSet(), "probe.go", "package probe; func f() { "+cell.declaration+"; _ = issue }", 0)
			if err != nil {
				t.Fatal(err)
			}
			fn := parsed.Decls[0].(*ast.FuncDecl)
			result := &ast.UnaryExpr{Op: token.AND, X: ast.NewIdent("issue")}
			if got := scaleP21SW225IssueResult(fn, result); got != cell.want {
				t.Fatalf("checked Issue result=%v want=%v", got, cell.want)
			}
		})
	}
}

func TestScaleP21SW225BootstrapTypeDoesNotHideLegacyReferences(t *testing.T) {
	for _, cell := range []struct {
		name, source string
		legacy       int
	}{
		{"qualified-types", `import "flow/api/internal/domain"; func owned(result *domain.Bootstrap) { var data domain.Bootstrap; _ = data; _ = result }`, 0},
		{"aliased-type", `import d "flow/api/internal/domain"; func owned() { _ = d.Bootstrap{} }`, 0},
		{"actual-method-call", `func owned(s *server) { _ = s.store.Bootstrap() }`, 1},
		{"actual-method-value", `func owned(s *server) { callback := s.store.Bootstrap; _ = callback }`, 1},
		{"actual-method-expression", `import "flow/api/internal/store"; func owned() { callback := (*store.SQLiteStore).Bootstrap; _ = callback }`, 1},
		{"unknown-receiver", `func owned() { callback := unknown.Bootstrap; _ = callback }`, 1},
		{"different-package", `import domain "example.test/unknown"; func owned() { _ = domain.Bootstrap }`, 1},
		{"shadowed-package-parameter", `import "flow/api/internal/domain"; func owned(domain *SQLiteStore) { _ = domain.Bootstrap }`, 1},
		{"shadowed-package-local", `import "flow/api/internal/domain"; func owned(s *server) { domain := s.store; _ = domain.Bootstrap }`, 1},
		{"type-plus-legacy-call", `import "flow/api/internal/domain"; func owned(s *server) { _ = domain.Bootstrap(s.store.Bootstrap()) }`, 1},
		{"method-on-qualified-type", `import "flow/api/internal/domain"; func owned() { _ = domain.Bootstrap.Bootstrap }`, 1},
	} {
		t.Run(cell.name, func(t *testing.T) {
			parsed, err := parser.ParseFile(token.NewFileSet(), "sw225-reference.go", "package main\n"+cell.source, 0)
			if err != nil {
				t.Fatal(err)
			}
			legacy := 0
			ast.Inspect(parsed, func(node ast.Node) bool {
				selector, ok := node.(*ast.SelectorExpr)
				if ok && selector.Sel.Name == "Bootstrap" && !scaleP21SW225BootstrapTypeReference(parsed, selector) {
					legacy++
				}
				return true
			})
			if legacy != cell.legacy {
				t.Fatalf("legacy references=%d want=%d", legacy, cell.legacy)
			}
		})
	}
}
