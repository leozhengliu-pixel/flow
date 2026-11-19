package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"flow/api/internal/store"
)

// SW550: with bounded metadata on, a route runs COLD (the store's COLD scope:
// full legacy behaviour behind the per-replica COLD slot) unless it is proven
// bounded, and a proven-bounded route never runs COLD.
//
// A route is proven bounded when no flag-on path from its handler reaches a
// consumer still on the legacy whole-workspace path: an "unmigrated"
// inventory consumer, or a reviewed consumer whose dispatch is the
// registered COLD facade (coldDispatch). Paths are the SW286 reference graph
// of cmd/server and internal/store (calls, function values, goroutines,
// interface and by-name dispatch; flag-off-only references are not edges)
// with its pinned boundaries; every registered handler reference of the
// pattern is a root.
//
// The decision is materialised in scale_p21_route_temperature.go, which the
// server reads at runtime (a pattern missing from it runs COLD). This test
// recomputes the table from the sources and the inventory and fails on any
// difference, so the table cannot drift: a handler that starts reaching an
// unmigrated consumer, a consumer marked reviewed, a new or removed route
// each fail until the table is regenerated (FLOW_SW550_WRITE=1) and the diff
// is reviewed. A route that leaves the HOT set must also be named in
// scaleP21SW550HotDemotions with its reason, so no HOT route is pushed onto
// the COLD path silently.

// scaleP21SW550HotDemotions names routes that leave the HOT set relative to
// the table's previous version (empty while nothing was demoted).
var scaleP21SW550HotDemotions = map[string]string{}

type scaleP21SW550Review struct {
	Status            string `json:"status"`
	Temperature       string `json:"temperature"`
	TemperatureReason string `json:"temperatureReason"`
	ColdDispatch      string `json:"coldDispatch"`
}

func scaleP21SW550Inventory(t *testing.T) map[string]scaleP21SW550Review {
	t.Helper()
	result := map[string]scaleP21SW550Review{}
	for _, pkg := range []string{"server", "store"} {
		paths, err := filepath.Glob(filepath.Join(scaleP21SW286Root, "../../internal/store/scale_p21_inventory", pkg, "*.json"))
		if err != nil || len(paths) == 0 {
			t.Fatalf("%s inventory missing: %v", pkg, err)
		}
		for _, path := range paths {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var file struct {
				Consumers map[string]scaleP21SW550Review `json:"consumers"`
			}
			if err := json.Unmarshal(raw, &file); err != nil {
				t.Fatal(err)
			}
			source := strings.TrimSuffix(filepath.Base(path), ".json") + ".go"
			for name, review := range file.Consumers {
				result[pkg+"/"+source+":"+name] = review
			}
		}
	}
	return result
}

// scaleP21SW550Classify returns, for every registered pattern, "hot" or the
// reason it runs COLD: the first legacy consumer a flag-on path reaches and
// that path.
func scaleP21SW550Classify(t *testing.T) map[string]string {
	t.Helper()
	graph := scaleP21SW286Load(t)
	inventory := scaleP21SW550Inventory(t)
	isCold := func(key string) bool {
		review, ok := inventory[graph.inventoryKey(key)]
		// An unmigrated COLD consumer keeps the legacy path; a reviewed
		// consumer dispatched through the registered COLD facade is COLD by
		// construction. Unmigrated HOT consumers are HOT routes still being
		// migrated (the startup guard refuses bounded mode while any is
		// left); they are not pushed onto the COLD path.
		cold := review.Temperature == "cold" && strings.TrimSpace(review.TemperatureReason) != ""
		return ok && (review.Status == "unmigrated" && cold || review.ColdDispatch != "")
	}
	out := map[string][]int{}
	roots := map[string][]string{}
	for index, ref := range graph.refs {
		out[ref.from] = append(out[ref.from], index)
		if ref.kind == "route" && ref.pattern != "" && !slices.Contains(roots[ref.pattern], ref.to) {
			roots[ref.pattern] = append(roots[ref.pattern], ref.to)
		}
	}
	// The temperature of every function: "" (bounded) or the path to the
	// first legacy consumer it reaches. Memoised over the whole graph.
	reach := map[string]string{}
	var walk func(start string) string
	walk = func(start string) string {
		if result, ok := reach[start]; ok {
			return result
		}
		// A breadth-first walk from start; its own result is final.
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
				path := fmt.Sprintf("%s -(%s %s:%d)-> %s", paths[current], ref.kind, ref.file, ref.line, ref.to)
				paths[ref.to] = path
				if isCold(ref.to) {
					result = path
					break
				}
				queue = append(queue, ref.to)
			}
		}
		reach[start] = result
		return result
	}
	locals := scaleP21SW550LocalHandlers(t)
	table := map[string]string{}
	for _, pattern := range scaleP21SW490Patterns(t) {
		targets := roots[pattern]
		if len(targets) == 0 {
			// A handler held in a local (the MCP endpoints) is the server
			// method its assignment calls.
			targets = locals[pattern]
		}
		if len(targets) == 0 {
			// An imported handler value: nothing proves it bounded.
			table[pattern] = "cold: unresolved-handler"
			continue
		}
		sort.Strings(targets)
		table[pattern] = "hot"
		for _, target := range targets {
			if path := walk(target); path != "" {
				table[pattern] = "cold: " + path
				break
			}
		}
	}
	return table
}

// scaleP21SW550Source renders the generated table.
func scaleP21SW550Source(t *testing.T, table map[string]string) []byte {
	t.Helper()
	var text bytes.Buffer
	text.WriteString(`// Code generated by TestScaleP21SW550DefaultColdRouteTable (FLOW_SW550_WRITE=1); DO NOT EDIT.

package main

// scaleP21RouteTemperature is the HOT/COLD decision of every registered
// route with bounded metadata on (scale_p21_sw550_default_cold_test.go): "hot"
// for a route proven bounded, else "cold" with the first legacy consumer a
// flag-on path from its handler reaches. A pattern missing here runs COLD.
var scaleP21RouteTemperature = map[string]string{
`)
	patterns := make([]string, 0, len(table))
	for pattern := range table {
		patterns = append(patterns, pattern)
	}
	sort.Strings(patterns)
	for _, pattern := range patterns {
		value := table[pattern]
		if value != "hot" {
			// Keep the consumer the path ends at; the full path is in the test log.
			value = "cold: " + value[strings.LastIndex(value, " ")+1:]
		}
		fmt.Fprintf(&text, "\t%q: %q,\n", pattern, value)
	}
	text.WriteString("}\n")
	formatted, err := format.Source(text.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	return formatted
}

const scaleP21SW550TableFile = "scale_p21_route_temperature.go"

func TestScaleP21SW550DefaultColdRouteTable(t *testing.T) {
	table := scaleP21SW550Classify(t)
	hot, cold := 0, 0
	for pattern, value := range table {
		if value == "hot" {
			hot++
			continue
		}
		cold++
		if testing.Verbose() {
			t.Logf("SW550 COLD %s: %s", pattern, value)
		}
	}
	t.Logf("SW550 route table: %d routes, %d HOT, %d COLD", len(table), hot, cold)
	want := scaleP21SW550Source(t, table)
	if os.Getenv("FLOW_SW550_WRITE") == "1" {
		if err := os.WriteFile(scaleP21SW550TableFile, want, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(scaleP21SW550TableFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		for pattern, value := range table {
			if current, ok := scaleP21RouteTemperature[pattern]; !ok {
				t.Errorf("route %s is missing from %s (computed %s)", pattern, scaleP21SW550TableFile, value)
			} else if (current == "hot") != (value == "hot") {
				t.Errorf("route %s is %q in %s but computes %s", pattern, current, scaleP21SW550TableFile, value)
			}
		}
		for pattern := range scaleP21RouteTemperature {
			if _, ok := table[pattern]; !ok {
				t.Errorf("route %s in %s is no longer registered", pattern, scaleP21SW550TableFile)
			}
		}
		t.Fatalf("%s is stale: regenerate it with FLOW_SW550_WRITE=1 and review the diff", scaleP21SW550TableFile)
	}
	for pattern, reason := range scaleP21SW550HotDemotions {
		if table[pattern] == "hot" || reason == "" {
			t.Errorf("stale HOT demotion %s: %q", pattern, reason)
		}
	}
}

// TestScaleP21SW550ColdRoutingFollowsTheTable sends every registered pattern
// through the production route classifier (withColdMetadataRouteMux) of a
// bounded server: a HOT route runs without a COLD scope, every other route
// with one; flag off no route does.
func TestScaleP21SW550ColdRoutingFollowsTheTable(t *testing.T) {
	f, workspace := scaleP21SW550Fixture(t)
	for _, bounded := range []bool{false, true} {
		repo := f.repository
		if bounded {
			repo = f.probe
		}
		s := &server{store: repo}
		mux := http.NewServeMux()
		seen := map[string]bool{}
		for _, pattern := range scaleP21SW490Patterns(t) {
			mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
				_, scoped := store.ColdScopeFromContext(r.Context())
				seen[pattern] = scoped
			})
		}
		for _, pattern := range scaleP21SW490Patterns(t) {
			method, path, found := strings.Cut(pattern, " ")
			if !found {
				method, path = http.MethodGet, pattern
			}
			path = strings.NewReplacer("{$}", "", "...}", "}").Replace(path)
			for _, segment := range strings.Split(path, "/") {
				if strings.HasPrefix(segment, "{") {
					path = strings.Replace(path, segment, "sw550", 1)
				}
			}
			request := httptest.NewRequest(method, path, nil)
			request.Header.Set("X-Workspace-Key", workspace)
			if _, matched := mux.Handler(request); matched != pattern {
				t.Fatalf("SW550 request %s %s selects %q, not %q", method, path, matched, pattern)
			}
			delete(seen, pattern)
			s.withColdMetadataRouteMux(mux, mux).ServeHTTP(httptest.NewRecorder(), request)
			scoped, ran := seen[pattern]
			if !ran {
				t.Fatalf("SW550 %s did not reach its handler", pattern)
			}
			wantCold := bounded && scaleP21RouteTemperature[pattern] != "hot"
			if scoped != wantCold {
				t.Errorf("SW550 bounded=%v %s: COLD scope %v, want %v (table %q)", bounded, pattern, scoped, wantCold, scaleP21RouteTemperature[pattern])
			}
		}
	}
	if held := store.ColdSlotsHeldForTesting(); held != 0 {
		t.Fatalf("SW550 %d COLD slots still held", held)
	}
}

type scaleP21SW550Stores struct {
	repository, probe *store.SQLiteStore
}

// scaleP21SW550Fixture opens a flag-off store with one workspace and a
// bounded view of it.
func scaleP21SW550Fixture(t *testing.T) (scaleP21SW550Stores, string) {
	t.Helper()
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	repo, err := store.OpenDatabase(scaleP03DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	f := scaleP03Fixture(t, repo)
	view, err := repo.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), repo, f.data.Workspace.URLKey); err != nil {
		t.Fatal(err)
	}
	return scaleP21SW550Stores{repository: repo, probe: view}, f.data.Workspace.URLKey
}

// scaleP21SW550BackgroundRoots lists the goroutines started outside a
// request: every go statement in a function that server/main reaches without
// passing a route handler, with the first legacy consumer its flag-on walk
// reaches ("" when bounded).
func scaleP21SW550BackgroundRoots(t *testing.T) map[string]string {
	t.Helper()
	graph := scaleP21SW286Load(t)
	inventory := scaleP21SW550Inventory(t)
	isCold := func(key string) bool {
		review, ok := inventory[graph.inventoryKey(key)]
		cold := review.Temperature == "cold" && strings.TrimSpace(review.TemperatureReason) != ""
		return ok && (review.Status == "unmigrated" && cold || review.ColdDispatch != "")
	}
	out := map[string][]int{}
	for index, ref := range graph.refs {
		out[ref.from] = append(out[ref.from], index)
	}
	walk := func(start string, follow func(scaleP21SW286Ref) bool) map[string]string {
		paths := map[string]string{start: start}
		queue := []string{start}
		for len(queue) > 0 {
			current := queue[0]
			queue = queue[1:]
			for _, index := range out[current] {
				ref := graph.refs[index]
				if ref.offOnly || ref.kind == "route" || ref.byName || scaleP21SW286Cut(ref) || paths[ref.to] != "" || !follow(ref) {
					continue
				}
				paths[ref.to] = fmt.Sprintf("%s -(%s %s:%d)-> %s", paths[current], ref.kind, ref.file, ref.line, ref.to)
				queue = append(queue, ref.to)
			}
		}
		return paths
	}
	result := map[string]string{}
	for _, ref := range graph.refs {
		{
			if !ref.async || ref.offOnly || ref.byName || ref.kind == "route" {
				continue
			}
			root := ref.from + " go " + ref.to
			if _, seen := result[root]; seen {
				continue
			}
			result[root] = ""
			for target, path := range walk(ref.to, func(scaleP21SW286Ref) bool { return true }) {
				if isCold(target) && (result[root] == "" || len(path) < len(result[root])) {
					result[root] = path
				}
			}
		}
	}
	return result
}

// scaleP21SW550BackgroundWork names every goroutine root (a go statement)
// whose flag-on walk reaches a legacy consumer, with how its COLD needs run
// with bounded metadata on. Legacy whole-workspace reads are served from the
// legacy view whatever the context (scale_p21_default_cold.go); a write no
// migrated writer serves, a legacy projection and a legacy snapshot need a
// COLD context: a request's COLD scope (inherited by work the request leaves
// running, then per operation) or store.WithColdWork (per operation).
var scaleP21SW550BackgroundWork = map[string]string{
	"server/main go server/server.runApplicationAgentWorker":                                  "COLD work: main passes store.WithColdWork",
	"server/main go server/server.runReviewImportScheduler":                                   "COLD work: main passes store.WithColdWork",
	"server/main go server/server.runStorageMaintenance":                                      "COLD work: main passes store.WithColdWork",
	"server/server.runApplicationAgentWorker go server/server.deliverApplicationTask":         "COLD work: the worker's context",
	"server/server.startDeliveryScheduler go server/server.processChatIntegrationTick":        "COLD work: the scheduler context is store.WithColdWork",
	"server/server.startDeliveryScheduler go server/server.runDeliverySchedulerTick":          "COLD work: the scheduler context is store.WithColdWork",
	"server/server.startChatDMReplyJobs go server/server.processChatDMReplyJob":               "COLD work: the delivery scheduler's context",
	"server/server.createExport go server/server.completeExport":                              "COLD work: store.WorkerContext",
	"server/server.retryExport go server/server.completeExport":                               "COLD work: store.WorkerContext",
	"server/server.createPersonalDataExport go server/server.buildPersonalDataExport":         "COLD work: store.WorkerContext",
	"server/server.extractLoopContractAsync go server/server.loopContractFor":                 "COLD work: store.WorkerContext",
	"server/server.startLoopContractMigration go server/server.loopContractFor":               "COLD work: store.WorkerContext",
	"server/server.loopContractFor go server/server.readLoopContract":                         "inherits its caller's context (store.WorkerContext)",
	"server/server.commitImport go server/server.commitImportSync":                            "inherits POST /api/imports/{id}/commit's COLD scope",
	"server/server.codingEnvironmentTerminal go server/server.runCodingTerminalPrepareScript": "inherits GET /api/coding/environments/{id}/terminal's COLD scope",
	"server/server.startAgentLiveReply go server/agentLiveReply.run":                          "inherits the agent-session route's COLD scope",
	"server/server.startBoundedLoopRun go server/server.executeLoopRun":                       "COLD work: registerLoopWork's run context is store.WithColdWork (SW552)",
	"server/server.replyLoopRun go server/server.executeLoopRun":                              "COLD work: registerLoopWork's run context is store.WithColdWork (SW552)",
}

// TestScaleP21SW550BackgroundColdWork fails on a goroutine root that reaches a
// legacy consumer and is not named in scaleP21SW550BackgroundWork, and on a
// named root that no longer does.
func TestScaleP21SW550BackgroundColdWork(t *testing.T) {
	roots := scaleP21SW550BackgroundRoots(t)
	for root, path := range roots {
		if path == "" {
			continue
		}
		if scaleP21SW550BackgroundWork[root] == "" {
			t.Errorf("SW550 goroutine %s reaches a legacy consumer and is not classified:\n  %s", root, path)
		}
	}
	for root := range scaleP21SW550BackgroundWork {
		if roots[root] == "" {
			t.Errorf("SW550 stale background root %s: it no longer reaches a legacy consumer", root)
		}
	}
}

// scaleP21SW550LocalHandlers resolves a registration whose handler is a local
// variable (readWriteMCP := s.mcpHTTP(false)) to the server methods its
// assignment in the same function names.
func scaleP21SW550LocalHandlers(t *testing.T) map[string][]string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	result := map[string][]string{}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range parsed.Decls {
			function, ok := decl.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			assigned := map[string][]string{}
			ast.Inspect(function.Body, func(node ast.Node) bool {
				assignment, ok := node.(*ast.AssignStmt)
				if !ok || len(assignment.Lhs) != len(assignment.Rhs) {
					return true
				}
				for index, left := range assignment.Lhs {
					ident, ok := left.(*ast.Ident)
					if !ok {
						continue
					}
					ast.Inspect(assignment.Rhs[index], func(node ast.Node) bool {
						if selector, ok := node.(*ast.SelectorExpr); ok {
							if receiver, ok := selector.X.(*ast.Ident); ok && receiver.Name == "s" {
								assigned[ident.Name] = append(assigned[ident.Name], "server/server."+selector.Sel.Name)
							}
						}
						return true
					})
				}
				return true
			})
			ast.Inspect(function.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok || len(call.Args) != 2 {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || selector.Sel.Name != "HandleFunc" && selector.Sel.Name != "Handle" {
					return true
				}
				literal, ok := call.Args[0].(*ast.BasicLit)
				handler, local := call.Args[1].(*ast.Ident)
				if !ok || !local || len(assigned[handler.Name]) == 0 {
					return true
				}
				pattern, err := strconv.Unquote(literal.Value)
				if err == nil {
					result[pattern] = assigned[handler.Name]
				}
				return true
			})
		}
	}
	return result
}
