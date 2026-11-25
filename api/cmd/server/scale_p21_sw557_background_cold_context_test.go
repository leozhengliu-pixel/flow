package main

import (
	"fmt"
	"go/ast"
	"go/types"
	"slices"
	"sort"
	"strings"
	"testing"
)

// SW557: with bounded metadata on, a COLD consumer (an unmigrated consumer
// reviewed COLD, or a reviewed consumer dispatched through the registered
// COLD facade) keeps its legacy behaviour only inside a COLD context: a
// request's COLD scope (default COLD routing, SW550) or background COLD work
// (store.WithColdWork, which store.WorkerContext returns when bounded).
// Outside one the store refuses its whole-workspace needs (SW531), which is a
// functional failure, not a slowdown.
//
// SW550 classifies goroutine roots by hand. SW557 proves the context instead,
// inductively over the SW286 reference graph of cmd/server and
// internal/store: for every call site whose callee reaches a COLD consumer on
// a flag-on path, every context.Context argument (by the callee's declared
// parameter type) is traced back through locals, derivations
// (context.With*, signal.NotifyContext, package helpers that derive a context
// from their own context parameter), struct fields (every assignment and
// keyed literal of the field) and function literal parameters (an
// immediately invoked literal takes its argument; a callback literal is
// handed a context derived from its call's context argument). The origins:
//
//   - COLD: store.WithColdWork or SQLiteStore.WorkerContext;
//   - request: Request.Context() of an http.Request parameter (its route's
//     temperature is SW550's: a HOT route reaches no COLD consumer);
//   - a parameter of the caller: the obligation moves to the caller's own
//     call sites, which this test checks too (the caller reaches the
//     consumer through the callee);
//   - anything else (context.Background/TODO, a package variable, a channel
//     value, an unresolved expression): a violation unless reviewed in
//     scaleP21SW557Reviewed with the reason the path is bounded or COLD.
//
// Background entries are thereby covered whatever starts them: go
// statements, time/context AfterFunc callbacks, schedulers and tickers,
// startup syncs in main (shutdownSignal is signal.NotifyContext of
// context.Background) and helpers that build their own context. A function
// invoked only as a value (a sink or listener the store calls back) receives
// its invoker's context, which the invoker's own call site proves; such
// value references are listed in scaleP21SW557Callbacks with that invoker.

// scaleP21SW557Reviewed names every flagged call (caller -> callee: origin)
// whose context is not COLD and whose path is reviewed bounded. Each one
// reaches only the store's own needs (scaleP21SW557StoreNeeds) through a
// migrated writer it selects explicitly, or a bounded projection; running it
// COLD would turn its bounded reads into whole-workspace legacy views.
var scaleP21SW557Reviewed = map[string]string{
	"server/main -> server/server.runRecurringIssueScheduler: background context.Background()": "HOT ticker on shutdownSignal. " +
		"Its only store needs are createRecurringOccurrence (issue.created with store.ContextWithRecurringOccurrenceWrite: mutateBoundedRecurringOccurrence) " +
		"and normalizeRecurringIssueTimingBounded (issue.updated with store.ContextWithIssueRecurrenceTimingMutation: mutateBoundedIssueRecurrenceTiming), " +
		"each with its own metadata output owner; both are selected before the COLD fallback in dispatchMutation.",
	"server/main -> server/server.runReviewImportScheduler: background context.Background()": "The ticker scans due leases in SQL. " +
		"runReviewImport wraps the per-workspace context with store.WithColdWork before executing an import.",
	"server/server.mcpResolveDeletion -> server/server.invokeMCPRoute: unknown function literal value": "The deletion's run closure is invoked by deleteMCPResource " +
		"with the MCP tool call's context: the /mcp routes run COLD and their tools COLD per operation (SW550, SW556).",
	"server/server.mcpResolveSkillDeletion -> server/server.invokeMCPRoute: unknown function literal value": "The deletion's run closure is invoked by deleteMCPResource " +
		"with the MCP tool call's context: the /mcp routes run COLD and their tools COLD per operation (SW550, SW556).",
	"server/server.realtimeSocket -> server/server.handleBudgetedCollaboration: background context.Background()": "The collaboration socket's own lifetime " +
		"context. Its only store need is PagedWorkspaceMetadata inside store.InstalledMetadataRead's bounded projection (the snapshot scopes the read to the " +
		"installed records); no COLD consumer is reachable from the socket's commands.",
	"server/server.runTriageIntelligenceWith -> store/SQLiteStore.MutateWorkspace: unknown call write": "write is the caller's triageRunWrite: bounded " +
		"(runTriageIntelligenceBounded) it derives the context from ctx with store.ContextWithIssueSuggestionWrite and WithIssueRecordMutations " +
		"(mutateBoundedIssueSuggestions) and an output owner; flag off it only adds the renumbering marker.",
	"server/server.startCommentActionsBounded -> server/server.generateCommentActions: background context.Background()": "The comment-action generator " +
		"outlives its request. Its writes select the bounded proposal writer (store.ContextWithCommentActionProposal with IsIssueDiscussionMutation: " +
		"mutateBoundedCommentActionProposal) and its read is BeginIssueBootstrapRead's bounded projection.",
	"store/OpenDatabase -> store/SQLiteStore.dataMigrationsPending: unknown function literal value": "Startup reads migration versions from SQL " +
		"under the schema lock. runDataMigrations later invokes the registered callbacks with WithColdWork.",
}

// scaleP21SW557OpenStage is the review of an OpenDatabase stage or data
// migration referenced as a value (stage.run / migration.apply).
const scaleP21SW557OpenStage = "OpenDatabase runs it before the store serves under the schema lock. Pending data migrations can use the legacy image " +
	"before admission. The resident loader requires complete coverage. TestScaleP21SW557BoundedStartupMatchesLegacy checks the persisted rows across startup modes."

// scaleP21SW557Callbacks names every function that reaches a COLD consumer or
// store need, takes a context and is referenced as a value no traced call
// invokes, with who invokes it and with which context. Callbacks the coding
// service, a store sink or another package makes through an interface are
// not edges of the graph; main hands the coding service a COLD-work context
// (runCoding), and store sinks run on the context of the write that fires
// them.
var scaleP21SW557Callbacks = map[string]string{
	"store/SQLiteStore.migrateIssueCollections":         scaleP21SW557OpenStage,
	"store/SQLiteStore.migrateWorkspaceMetadataRecords": scaleP21SW557OpenStage,
	"store/SQLiteStore.migrateIssueListProjection":      scaleP21SW557OpenStage,
	"store/SQLiteStore.migrateLoopRunRecords":           scaleP21SW557OpenStage,
	"store/SQLiteStore.backfillAllProjectProgress":      scaleP21SW557OpenStage,
	"store/SQLiteStore.migrateDocumentDefaultAccess":    scaleP21SW557OpenStage,
	"store/SQLiteStore.migrateDocumentInheritedScope":   scaleP21SW557OpenStage,
	"store/SQLiteStore.migrateDocumentOwnerGrants":      scaleP21SW557OpenStage,
	"store/SQLiteStore.migrateResourceSlugs":            scaleP21SW557OpenStage,
	"store/SQLiteStore.migrateMemberDirectory":          scaleP21SW557OpenStage,
	"store/SQLiteStore.migrateCommentActions":           scaleP21SW557OpenStage,
	"store/SQLiteStore.migrateCycleCapacityOverrides":   scaleP21SW557OpenStage,
	"store/SQLiteStore.migrateIntegrationOAuthTokens":   scaleP21SW557OpenStage,
	"store/SQLiteStore.migrateRenamedWorkspaceKeys":     scaleP21SW557OpenStage,
	"store/SQLiteStore.MigrateLegacyAuditLog":           scaleP21SW557OpenStage,
	"server/server.notifyAgentReply": "The store's agent reply listener, invoked after a reply commits with the replying write's context. " +
		"Its write selects the bounded agent-reply writer (store.ContextWithAgentReplyNotification: dispatchMutation's agent_task.replied branch) " +
		"and it owns its reads' outputs (beginIndependentMetadataOutputReads).",
	"server/server.codingMCPServers": "The coding service's MCPServers hook. It ignores its context: its only legacy read is WorkspaceMetadata, " +
		"which the bounded store answers from the legacy view of the installed workspace in any context.",
	"server/server.notifyCodingApprovalExpired": "The coding service's ApprovalExpired hook, invoked by the session reconciler with runCoding's " +
		"context (store.WithColdWork in main) or by a request transition inside its route's COLD scope.",
}

type scaleP21SW557Tracer struct {
	g        *scaleP21SW286Graph
	fields   map[types.Object][]scaleP21SW557Assign
	visiting map[string]bool
	memo     map[string][]string
}

type scaleP21SW557Assign struct {
	fn   *scaleP21SW286Func
	expr ast.Expr
}

// scaleP21SW557ContextType reports a context.Context type expression.
func scaleP21SW557ContextType(expression ast.Expr) bool {
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := selector.X.(*ast.Ident)
	return ok && pkg.Name == "context" && selector.Sel.Name == "Context"
}

// scaleP21SW557ContextParams returns the indexes of fn's context.Context
// parameters (variadic parameters excluded).
func scaleP21SW557ContextParams(fields *ast.FieldList) []int {
	var result []int
	index := 0
	if fields == nil {
		return nil
	}
	for _, field := range fields.List {
		count := max(1, len(field.Names))
		for range count {
			if scaleP21SW557ContextType(field.Type) {
				result = append(result, index)
			}
			index++
		}
	}
	return result
}

// scaleP21SW557ParamIndex reports the index of obj among the parameters.
func (tr *scaleP21SW557Tracer) paramIndex(fields *ast.FieldList, obj types.Object) (int, ast.Expr, bool) {
	if fields == nil {
		return 0, nil, false
	}
	index := 0
	for _, field := range fields.List {
		if len(field.Names) == 0 {
			index++
			continue
		}
		for _, name := range field.Names {
			if tr.g.info.Defs[name] == obj {
				return index, field.Type, true
			}
			index++
		}
	}
	return 0, nil, false
}

func (tr *scaleP21SW557Tracer) position(node ast.Node) string {
	position := tr.g.fset.Position(node.Pos())
	return fmt.Sprintf("%s:%d", position.Filename[strings.LastIndex(position.Filename, "/")+1:], position.Line)
}

// parents maps every node of fn's body to its parent.
func scaleP21SW557Parents(fn *scaleP21SW286Func) map[ast.Node]ast.Node {
	parents := map[ast.Node]ast.Node{}
	var stack []ast.Node
	ast.Inspect(fn.decl, func(node ast.Node) bool {
		if node == nil {
			stack = stack[:len(stack)-1]
			return false
		}
		if len(stack) > 0 {
			parents[node] = stack[len(stack)-1]
		}
		stack = append(stack, node)
		return true
	})
	return parents
}

// trace returns the origins of the context expression in fn: "cold",
// "request", "param:<fn>#<index>" or a violation ("background ...",
// "unknown ...").
func (tr *scaleP21SW557Tracer) trace(fn *scaleP21SW286Func, expression ast.Expr) []string {
	defer tr.g.enter(fn)()
	expression = ast.Unparen(expression)
	switch e := expression.(type) {
	case *ast.Ident:
		object := tr.g.info.Uses[e]
		if object == nil {
			object = tr.g.info.Defs[e]
		}
		if object == nil {
			return []string{"unknown identifier " + e.Name + " at " + tr.position(e)}
		}
		if object.Name() == "nil" {
			return nil
		}
		return tr.traceObject(fn, object, e)
	case *ast.CallExpr:
		return tr.traceCall(fn, e)
	case *ast.SelectorExpr:
		if selection := tr.g.info.Selections[e]; selection != nil && selection.Kind() == types.FieldVal {
			return tr.traceField(selection.Obj(), e)
		}
		return []string{"unknown selector " + scaleP21SW286Source(tr.g.fset, e) + " at " + tr.position(e)}
	}
	return []string{"unknown expression " + scaleP21SW286Source(tr.g.fset, expression) + " at " + tr.position(expression)}
}

func (tr *scaleP21SW557Tracer) traceObject(fn *scaleP21SW286Func, object types.Object, at ast.Node) []string {
	key := fmt.Sprintf("object %s %p", fn.key, object)
	if tr.visiting[key] {
		return nil // a self-derivation (ctx = context.WithValue(ctx, ...)): its other assignments decide
	}
	if cached, ok := tr.memo[key]; ok {
		return cached
	}
	tr.visiting[key] = true
	defer delete(tr.visiting, key)
	var result []string
	// A parameter of the function itself.
	if index, _, ok := tr.paramIndex(fn.decl.Type.Params, object); ok {
		result = []string{fmt.Sprintf("param:%s#%d", fn.key, index)}
		tr.memo[key] = result
		return result
	}
	if fn.decl.Recv != nil {
		if _, _, ok := tr.paramIndex(fn.decl.Recv, object); ok {
			return []string{"unknown receiver at " + tr.position(at)}
		}
	}
	parents := scaleP21SW557Parents(fn)
	found := false
	ast.Inspect(fn.decl.Body, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.FuncLit:
			if index, typ, ok := tr.paramIndex(n.Type.Params, object); ok {
				found = true
				result = append(result, tr.traceLiteralParam(fn, n, index, typ, parents)...)
			}
		case *ast.AssignStmt:
			for index, left := range n.Lhs {
				ident, ok := left.(*ast.Ident)
				if !ok || tr.g.info.Defs[ident] != object && tr.g.info.Uses[ident] != object {
					continue
				}
				found = true
				if len(n.Lhs) == len(n.Rhs) {
					result = append(result, tr.trace(fn, n.Rhs[index])...)
				} else if len(n.Rhs) == 1 {
					result = append(result, tr.trace(fn, n.Rhs[0])...)
				}
			}
		case *ast.ValueSpec:
			for index, name := range n.Names {
				if tr.g.info.Defs[name] != object {
					continue
				}
				found = true
				if len(n.Values) == len(n.Names) {
					result = append(result, tr.trace(fn, n.Values[index])...)
				} else if len(n.Values) == 1 {
					result = append(result, tr.trace(fn, n.Values[0])...)
				}
			}
		case *ast.RangeStmt:
			for _, side := range []ast.Expr{n.Key, n.Value} {
				if ident, ok := side.(*ast.Ident); ok && tr.g.info.Defs[ident] == object {
					found = true
					result = append(result, "unknown range value "+ident.Name+" at "+tr.position(ident))
				}
			}
		}
		return true
	})
	if !found {
		if object.Parent() == object.Pkg().Scope() {
			result = []string{"unknown package variable " + object.Name() + " at " + tr.position(at)}
		} else {
			result = []string{"unknown variable " + object.Name() + " at " + tr.position(at)}
		}
	}
	tr.memo[key] = result
	return result
}

// traceLiteralParam traces the index-th parameter of a function literal: the
// argument of an immediate invocation (go func(ctx) {...}(x)), or for a
// callback literal the context argument of the call it is passed to.
func (tr *scaleP21SW557Tracer) traceLiteralParam(fn *scaleP21SW286Func, literal *ast.FuncLit, index int, typ ast.Expr, parents map[ast.Node]ast.Node) []string {
	if !scaleP21SW557ContextType(typ) {
		return []string{"unknown literal parameter at " + tr.position(literal)}
	}
	call, ok := parents[literal].(*ast.CallExpr)
	if !ok {
		return []string{"unknown function literal value at " + tr.position(literal)}
	}
	if call.Fun == literal {
		if index < len(call.Args) {
			return tr.trace(fn, call.Args[index])
		}
		return []string{"unknown literal argument at " + tr.position(literal)}
	}
	origins, ok := tr.callbackOrigins(fn, call, slices.Index(call.Args, ast.Expr(literal)), index, true)
	if !ok {
		return []string{"unknown callback " + scaleP21SW286Source(tr.g.fset, call.Fun) + " at " + tr.position(literal)}
	}
	return origins
}

// callbackOrigins traces the index-th parameter of a function value passed
// as the argument-th argument of call: the contexts the callee passes when it
// invokes that parameter by name (a parameter of the callee is the call's own
// argument). With derived, a callee that never invokes it by name is taken to
// hand it a context derived from the call's context argument.
func (tr *scaleP21SW557Tracer) callbackOrigins(fn *scaleP21SW286Func, call *ast.CallExpr, argument, index int, derived bool) ([]string, bool) {
	var result []string
	traced := false
	for _, target := range tr.g.targets(call.Fun, call) {
		callee := tr.g.funcs[target]
		if callee == nil {
			continue
		}
		if invoked, ok := tr.invokedOrigins(callee, argument, index); ok {
			traced = true
			for _, origin := range invoked {
				prefix := "param:" + callee.key + "#"
				var param int
				if _, err := fmt.Sscanf(strings.TrimPrefix(origin, prefix), "%d", &param); err == nil && strings.HasPrefix(origin, prefix) && param < len(call.Args) {
					result = append(result, tr.trace(fn, call.Args[param])...)
					continue
				}
				result = append(result, origin)
			}
			continue
		}
		if !derived {
			continue
		}
		for _, param := range scaleP21SW557ContextParams(callee.decl.Type.Params) {
			if param < len(call.Args) {
				traced = true
				result = append(result, tr.trace(fn, call.Args[param])...)
			}
		}
	}
	return result, traced
}

// invokedOrigins returns, in the callee's terms, the origins of the
// index-th argument of every invocation of its argument-th parameter (a
// function value): called by name, or forwarded to another function that
// invokes it.
func (tr *scaleP21SW557Tracer) invokedOrigins(callee *scaleP21SW286Func, argument, index int) ([]string, bool) {
	if argument < 0 {
		return nil, false
	}
	key := fmt.Sprintf("invoked %s#%d#%d", callee.key, argument, index)
	if tr.visiting[key] {
		return nil, false
	}
	tr.visiting[key] = true
	defer delete(tr.visiting, key)
	defer tr.g.enter(callee)()
	var param types.Object
	position := 0
	for _, field := range callee.decl.Type.Params.List {
		for _, name := range field.Names {
			if position == argument {
				param = tr.g.info.Defs[name]
			}
			position++
		}
		if len(field.Names) == 0 {
			position++
		}
	}
	if param == nil {
		return nil, false
	}
	var result []string
	found := false
	ast.Inspect(callee.decl.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if ident, ok := call.Fun.(*ast.Ident); ok && tr.g.info.Uses[ident] == param {
			found = true
			if index < len(call.Args) {
				result = append(result, tr.trace(callee, call.Args[index])...)
			}
			return true
		}
		for forwarded, value := range call.Args {
			if ident, ok := value.(*ast.Ident); ok && tr.g.info.Uses[ident] == param {
				if origins, ok := tr.callbackOrigins(callee, call, forwarded, index, false); ok {
					found = true
					result = append(result, origins...)
				}
			}
		}
		return true
	})
	return result, found
}

func (tr *scaleP21SW557Tracer) traceCall(fn *scaleP21SW286Func, call *ast.CallExpr) []string {
	if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
		if pkg, ok := selector.X.(*ast.Ident); ok && (isPkgName(tr.g.info.Uses[pkg]) || tr.g.info.Uses[pkg] == nil && tr.g.info.Defs[pkg] == nil && fn.imports[pkg.Name]) {
			name := selector.Sel.Name
			switch {
			case pkg.Name == "context" && (name == "Background" || name == "TODO"):
				return []string{"background context." + name + "() at " + tr.position(call)}
			case pkg.Name == "store" && name == "WithColdWork":
				return []string{"cold"}
			case (pkg.Name == "context" || pkg.Name == "signal") && len(call.Args) > 0,
				strings.HasPrefix(name, "With") && len(call.Args) > 0 && pkg.Name != "store":
				return tr.trace(fn, call.Args[0])
			}
		}
		if selector.Sel.Name == "Context" && len(call.Args) == 0 && tr.requestValue(fn, selector.X) {
			return []string{"request"}
		}
	}
	if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "WithColdWork" && fn.pkg == "store" {
		return []string{"cold"}
	}
	targets := tr.g.targets(call.Fun, call)
	if len(targets) == 0 {
		return []string{"unknown call " + scaleP21SW286Source(tr.g.fset, call.Fun) + " at " + tr.position(call)}
	}
	var result []string
	for _, target := range targets {
		if target == "store/SQLiteStore.WorkerContext" || target == "store/WithColdWork" {
			result = append(result, "cold")
			continue
		}
		callee := tr.g.funcs[target]
		if callee == nil {
			result = append(result, "unknown call "+target+" at "+tr.position(call))
			continue
		}
		// A helper that derives the context it returns from its own
		// context parameter (beginMetadataOutputReads, WithMetadataOutputOwner).
		if params := scaleP21SW557ContextParams(callee.decl.Type.Params); len(params) > 0 && params[0] < len(call.Args) {
			result = append(result, tr.trace(fn, call.Args[params[0]])...)
			continue
		}
		// Otherwise the contexts it returns.
		result = append(result, tr.returned(callee)...)
	}
	return result
}

func isPkgName(object types.Object) bool {
	_, ok := object.(*types.PkgName)
	return ok
}

// returned traces the context results of fn's return statements.
func (tr *scaleP21SW557Tracer) returned(fn *scaleP21SW286Func) []string {
	key := "returned " + fn.key
	if tr.visiting[key] {
		return nil
	}
	if cached, ok := tr.memo[key]; ok {
		return cached
	}
	tr.visiting[key] = true
	defer delete(tr.visiting, key)
	results := scaleP21SW557ContextParams(fn.decl.Type.Results)
	var result []string
	ast.Inspect(fn.decl.Body, func(node ast.Node) bool {
		if _, ok := node.(*ast.FuncLit); ok {
			return false
		}
		statement, ok := node.(*ast.ReturnStmt)
		if !ok {
			return true
		}
		for _, index := range results {
			if index < len(statement.Results) {
				result = append(result, tr.trace(fn, statement.Results[index])...)
			}
		}
		return true
	})
	if len(results) == 0 {
		result = []string{"unknown result of " + fn.key}
	}
	tr.memo[key] = result
	return result
}

// requestValue reports an *http.Request parameter (or local) of fn.
func (tr *scaleP21SW557Tracer) requestValue(fn *scaleP21SW286Func, expression ast.Expr) bool {
	ident, ok := ast.Unparen(expression).(*ast.Ident)
	if !ok {
		return false
	}
	object := tr.g.info.Uses[ident]
	request := func(typ ast.Expr) bool {
		star, ok := typ.(*ast.StarExpr)
		if !ok {
			return false
		}
		selector, ok := star.X.(*ast.SelectorExpr)
		pkg, isIdent := selector.X.(*ast.Ident)
		return ok && isIdent && pkg.Name == "http" && selector.Sel.Name == "Request"
	}
	if _, typ, ok := tr.paramIndex(fn.decl.Type.Params, object); ok {
		return request(typ)
	}
	matched := false
	ast.Inspect(fn.decl.Body, func(node ast.Node) bool {
		if literal, ok := node.(*ast.FuncLit); ok {
			if _, typ, ok := tr.paramIndex(literal.Type.Params, object); ok && request(typ) {
				matched = true
			}
		}
		return !matched
	})
	return matched
}

// traceField traces every value stored into the struct field.
func (tr *scaleP21SW557Tracer) traceField(field types.Object, at ast.Node) []string {
	key := fmt.Sprintf("field %p", field)
	if tr.visiting[key] {
		return nil
	}
	if cached, ok := tr.memo[key]; ok {
		return cached
	}
	tr.visiting[key] = true
	defer delete(tr.visiting, key)
	var result []string
	for _, assign := range tr.fields[field] {
		result = append(result, tr.trace(assign.fn, assign.expr)...)
	}
	if len(tr.fields[field]) == 0 {
		result = []string{"unknown field " + field.Name() + " (never assigned) at " + tr.position(at)}
	}
	tr.memo[key] = result
	return result
}

// indexFields records every assignment and keyed literal of a struct field.
func (tr *scaleP21SW557Tracer) indexFields() {
	tr.fields = map[types.Object][]scaleP21SW557Assign{}
	for _, fn := range tr.g.funcs {
		ast.Inspect(fn.decl.Body, func(node ast.Node) bool {
			switch n := node.(type) {
			case *ast.AssignStmt:
				for index, left := range n.Lhs {
					selector, ok := left.(*ast.SelectorExpr)
					if !ok {
						continue
					}
					selection := tr.g.info.Selections[selector]
					if selection == nil || selection.Kind() != types.FieldVal {
						continue
					}
					if len(n.Lhs) == len(n.Rhs) {
						tr.fields[selection.Obj()] = append(tr.fields[selection.Obj()], scaleP21SW557Assign{fn, n.Rhs[index]})
					} else if len(n.Rhs) == 1 {
						tr.fields[selection.Obj()] = append(tr.fields[selection.Obj()], scaleP21SW557Assign{fn, n.Rhs[0]})
					}
				}
			case *ast.KeyValueExpr:
				if key, ok := n.Key.(*ast.Ident); ok {
					if field, ok := tr.g.info.Uses[key].(*types.Var); ok && field.IsField() {
						tr.fields[field] = append(tr.fields[field], scaleP21SW557Assign{fn, n.Value})
					}
				}
			}
			return true
		})
	}
}

// scaleP21SW557Reach returns, for every function, the path to the first COLD
// consumer a flag-on walk from it reaches ("" when none).
func scaleP21SW557Reach(t *testing.T, graph *scaleP21SW286Graph) func(string) string {
	t.Helper()
	inventory := scaleP21SW550Inventory(t)
	isCold := func(key string) bool {
		review, ok := inventory[graph.inventoryKey(key)]
		cold := review.Temperature == "cold" && strings.TrimSpace(review.TemperatureReason) != ""
		return ok && (review.Status == "unmigrated" && cold || review.ColdDispatch != "") || scaleP21SW557StoreNeeds[key]
	}
	out := map[string][]int{}
	for index, ref := range graph.refs {
		out[ref.from] = append(out[ref.from], index)
	}
	reach := map[string]string{}
	return func(start string) string {
		if result, ok := reach[start]; ok {
			return result
		}
		paths := map[string]string{start: start}
		queue := []string{start}
		result := ""
		if isCold(start) {
			result = start
		}
		for len(queue) > 0 && result == "" {
			current := queue[0]
			queue = queue[1:]
			for _, index := range out[current] {
				ref := graph.refs[index]
				if ref.offOnly || ref.kind == "route" || ref.byName || scaleP21SW286Cut(ref) || paths[ref.to] != "" {
					continue
				}
				paths[ref.to] = paths[current] + " -> " + ref.to
				if isCold(ref.to) {
					result = paths[ref.to]
					break
				}
				queue = append(queue, ref.to)
			}
		}
		reach[start] = result
		return result
	}
}

// scaleP21SW557StoreNeeds are the store's own COLD needs: with bounded
// metadata on each serves a COLD context (store.coldCapable) the legacy way
// and refuses, or answers bounded, anything else.
var scaleP21SW557StoreNeeds = map[string]bool{
	"store/SQLiteStore.dispatchMutation":       true,
	"store/SQLiteStore.PagedWorkspaceMetadata": true,
	"store/SQLiteStore.BeginWorkspaceRead":     true,
	"store/SQLiteStore.UpdateWorkspace":        true,
}

type scaleP21SW557Finding struct {
	key, origin, path string
}

// scaleP21SW557Check returns every call whose context argument reaches a
// COLD consumer from a non-COLD origin, and every value reference of a
// COLD-reaching function that takes a context.
func scaleP21SW557Check(t *testing.T) (findings []scaleP21SW557Finding, callbacks map[string]string, calls int) {
	t.Helper()
	graph := scaleP21SW286Load(t)
	reach := scaleP21SW557Reach(t, graph)
	tracer := &scaleP21SW557Tracer{g: graph, visiting: map[string]bool{}, memo: map[string][]string{}}
	tracer.indexFields()
	callbacks = map[string]string{}
	seen := map[string]bool{}
	keys := make([]string, 0, len(graph.funcs))
	for key := range graph.funcs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fn := graph.funcs[key]
		restore := graph.enter(fn)
		var ancestors []ast.Node
		ast.Inspect(fn.decl.Body, func(node ast.Node) bool {
			if node == nil {
				ancestors = ancestors[:len(ancestors)-1]
				return false
			}
			ancestors = append(ancestors, node)
			if len(ancestors) < 2 {
				return true
			}
			parent := ancestors[len(ancestors)-2]
			targets, byName := graph.resolve(node, parent)
			if len(targets) == 0 || byName || graph.offOnly(ancestors) {
				return true
			}
			call, isCall := parent.(*ast.CallExpr)
			for _, target := range targets {
				callee := graph.funcs[target]
				if callee == nil {
					continue
				}
				params := scaleP21SW557ContextParams(callee.decl.Type.Params)
				if len(params) == 0 {
					continue
				}
				path := reach(target)
				if path == "" {
					continue
				}
				var traced func(param int) []string
				switch {
				case isCall && call.Fun == node:
					traced = func(param int) []string {
						if param >= len(call.Args) {
							return nil
						}
						return tracer.trace(fn, call.Args[param])
					}
				case scaleP21SW286RouteCall(parent) || slices.ContainsFunc(ancestors, scaleP21SW286RouteCall):
					continue
				default:
					// A function value: passed to a call that invokes it by
					// name, its contexts are traced there; otherwise its
					// invoker must be reviewed.
					argument := -1
					if expression, ok := node.(ast.Expr); ok && isCall {
						argument = slices.Index(call.Args, expression)
					}
					resolved := isCall && argument >= 0
					for _, param := range params {
						if !resolved {
							break
						}
						if _, ok := tracer.callbackOrigins(fn, call, argument, param, false); !ok {
							resolved = false
						}
					}
					if !resolved {
						callbacks[target] = "in " + key + "; reaches " + path
						continue
					}
					traced = func(param int) []string {
						origins, _ := tracer.callbackOrigins(fn, call, argument, param, false)
						return origins
					}
				}
				calls++
				for _, param := range params {
					for _, origin := range traced(param) {
						if origin == "cold" || origin == "request" || strings.HasPrefix(origin, "param:") {
							continue
						}
						// Drop the line from the review key; keep it in the report.
						reported := origin
						if at := strings.LastIndex(origin, " at "); at >= 0 {
							reported = origin[:at]
						}
						id := key + " -> " + target + ": " + reported
						if seen[id+origin] {
							continue
						}
						seen[id+origin] = true
						findings = append(findings, scaleP21SW557Finding{key: id, origin: origin, path: path})
					}
				}
			}
			return true
		})
		restore()
	}
	return findings, callbacks, calls
}

func TestScaleP21SW557BackgroundColdContext(t *testing.T) {
	findings, callbacks, calls := scaleP21SW557Check(t)
	t.Logf("SW557: %d calls into COLD-reaching functions checked, %d findings, %d callbacks", calls, len(findings), len(callbacks))
	flagged := map[string]bool{}
	for _, finding := range findings {
		flagged[finding.key] = true
		if scaleP21SW557Reviewed[finding.key] == "" {
			t.Errorf("SW557 %s\n  origin: %s\n  reaches: %s", finding.key, finding.origin, finding.path)
		}
	}
	for key := range scaleP21SW557Reviewed {
		if !flagged[key] {
			t.Errorf("SW557 stale review %s", key)
		}
	}
	for target, from := range callbacks {
		if scaleP21SW557Callbacks[target] == "" {
			t.Errorf("SW557 COLD-reaching function %s is used as a value (%s): name its invoker and context in scaleP21SW557Callbacks", target, from)
		}
	}
	for target := range scaleP21SW557Callbacks {
		if callbacks[target] == "" {
			t.Errorf("SW557 stale callback %s", target)
		}
	}
}
