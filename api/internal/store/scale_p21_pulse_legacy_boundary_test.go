package store

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

func TestScaleP21PulseLegacyFeedBoundary(t *testing.T) {
	// The exceptional paths use state established earlier in the function or
	// a legacy-only helper. Pin their reviewed ASTs so a changed invariant
	// requires review instead of being accepted by a shallow guard matcher.
	// The bounded constructor installs source before returning; Close clears
	// Snapshot before source, and LoadFeed rejects a nil Snapshot first.
	// Chat's bounded branches return on failed reads, so pulse != nil is
	// exact mode dispatch. Both legacy initiative-card callers and the sole
	// pulseWorkspaceKey caller dispatch through their owned alternatives.
	invariants := map[string]string{
		"internal/store/scale_p21_hot_pulse_read.go:beginPulseRead":              "270a37c18133c04d1b1ac8d8cbda5449e633ea4a5c3788bdee4702199da274ac",
		"internal/store/scale_p21_hot_pulse_read.go:Close":                       "36570cd11e1f9b0e932d050c0918140f1350180bc862677e65ab1ea4d9b518d6",
		"internal/store/scale_p21_hot_pulse_read.go:LoadFeed":                    "666a5ed3c46d87228920afd1e211fe1bcd3c927a025d85c0f1dc33ada8da6b51",
		"cmd/server/chat_update_cards.go:chatSendProjectUpdate":                  "d28aa38e558a4d26ea1d132d1ba9d9f8384880aa6d46fe119d18ea6f3b344c88",
		"cmd/server/chat_update_cards.go:chatInitiativeUpdateCard":               "71b3b2396957a4d7ab45ade2cf7ce5480b0dae047ae75f561594e11f97405914",
		"cmd/server/chat_notifications.go:chatSendInitiativeUpdate":              "3f8b870208c7296b70f403efe9851661276c0e876de6dcd976454a1e7949b654",
		"cmd/server/chat_initiative_bindings.go:chatSendInitiativeBindingUpdate": "3441afdfa80aeb91eab535ce1241460120411fd6deb24b45c62c4673479e2c70",
		"cmd/server/pulse_summary.go:pulseWorkspaceKey":                          "08d3fb92e13de7bef03fe491e8e04307773062d5a290389d4e0162900823959c",
		"cmd/server/pulse_summary.go:pulseSummaryWorkspaceKey":                   "95f75daa4d71fd722cee98e5f46999d431fdbde65f32c90849030907b7581bf1",
	}
	sites := map[string]bool{
		"internal/store/scale_p21_hot_pulse_read.go:beginPulseRead": false,
		"internal/store/scale_p21_hot_pulse_read.go:LoadFeed":       true,
		"cmd/server/chat_update_cards.go:chatSendProjectUpdate":     true,
		"cmd/server/chat_update_cards.go:chatInitiativeUpdateCard":  true,
		"cmd/server/pulse_summary.go:pulseWorkspaceKey":             true,
		"cmd/server/pulse_summary.go:pulseSummaryUpdates":           false,
		"cmd/server/pulse_summary.go:pulseSummaryNotification":      false,
		"cmd/server/pulse_summary.go:loadPulseSummary":              false,
		"cmd/server/pulse_feed.go:pulseRequestContext":              false,
		"cmd/server/main.go:createProjectUpdate":                    false,
		"cmd/server/chat_unfurl.go:chatLinkPreviewFor":              false,
		"cmd/server/webhook_children.go:webhookChildEntity":         false,
	}
	helperCallers := map[string]map[string]bool{
		"chatInitiativeUpdateCard": {
			"cmd/server/chat_notifications.go:chatSendInitiativeUpdate":              true,
			"cmd/server/chat_initiative_bindings.go:chatSendInitiativeBindingUpdate": true,
		},
		"pulseWorkspaceKey": {"cmd/server/pulse_summary.go:pulseSummaryWorkspaceKey": true},
	}
	seen, pinned, callers := map[string]int{}, map[string]bool{}, map[string]int{}
	literals := 0
	root := filepath.Clean("../..")
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root && strings.HasPrefix(entry.Name(), ".") || entry.Name() == "vendor" ||
				path == filepath.Join(root, "internal", "store", "data") {
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
		for _, declaration := range file.Decls {
			function, isFunction := declaration.(*ast.FuncDecl)
			name := filepath.ToSlash(relative) + ":<declaration>"
			if isFunction {
				name = filepath.ToSlash(relative) + ":" + function.Name.Name
				if expected, ok := invariants[name]; ok {
					var rendered bytes.Buffer
					if err := format.Node(&rendered, token.NewFileSet(), function); err != nil {
						return err
					}
					actual := fmt.Sprintf("%x", sha256.Sum256(rendered.Bytes()))
					if actual != expected {
						t.Errorf("%s: reviewed invariant changed; review constructor/state/helper path before updating digest: %s", name, actual)
					}
					pinned[name] = true
				}
			}
			parents := map[ast.Node]ast.Node{}
			var stack []ast.Node
			ast.Inspect(declaration, func(node ast.Node) bool {
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
			ast.Inspect(declaration, func(node ast.Node) bool {
				if literal, ok := node.(*ast.CompositeLit); ok && deliveryBoundaryTypeName(literal.Type) == "PulseRead" {
					literals++
					if name != "internal/store/scale_p21_hot_pulse_read.go:beginPulseRead" {
						t.Errorf("%s: unreviewed PulseRead constructor", name)
					}
				}
				if variable, ok := node.(*ast.ValueSpec); ok && deliveryBoundaryTypeName(variable.Type) == "PulseRead" {
					if _, pointer := variable.Type.(*ast.StarExpr); !pointer {
						t.Errorf("%s: unreviewed zero-value PulseRead declaration", name)
					}
				}
				if alias, ok := node.(*ast.TypeSpec); ok && deliveryBoundaryTypeName(alias.Type) == "PulseRead" {
					t.Errorf("%s: unreviewed PulseRead alias", name)
				}
				if call, ok := node.(*ast.CallExpr); ok && len(call.Args) == 1 && deliveryBoundaryTypeName(call.Fun) == "new" && deliveryBoundaryTypeName(call.Args[0]) == "PulseRead" {
					t.Errorf("%s: unreviewed new(PulseRead)", name)
				}
				method, ok := node.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if allowed, helper := helperCallers[method.Sel.Name]; helper {
					call, direct := parents[method].(*ast.CallExpr)
					if !direct || call.Fun != method || !allowed[name] {
						t.Errorf("%s: unreviewed legacy helper call/escape %s", name, method.Sel.Name)
					}
					callers[method.Sel.Name+":"+name]++
				}
				if method.Sel.Name != "PulseFeed" {
					return true
				}
				seen[name]++
				exceptional, reviewed := sites[name]
				call, direct := parents[method].(*ast.CallExpr)
				receiver := deliveryBoundaryStoreReceiver(method.X)
				if name == "internal/store/scale_p21_hot_pulse_read.go:LoadFeed" {
					owner, ok := method.X.(*ast.SelectorExpr)
					receiver = ok && owner.Sel.Name == "store" && deliveryBoundaryTypeName(owner.X) == "r"
				}
				if !reviewed || !isFunction || !direct || call.Fun != method || !receiver {
					t.Errorf("%s: unreviewed PulseFeed call or method-value escape", name)
				} else if !exceptional && deliveryBoundaryMode(function, call, parents) != 1 {
					t.Errorf("%s: PulseFeed is not in a proved off branch", name)
				}
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for site := range sites {
		if seen[site] != 1 {
			t.Errorf("%s: legacy selector count changed: %d", site, seen[site])
		}
	}
	for invariant := range invariants {
		if !pinned[invariant] {
			t.Error("reviewed invariant disappeared", invariant)
		}
	}
	for helper, allowed := range helperCallers {
		for caller := range allowed {
			if callers[helper+":"+caller] != 1 {
				t.Errorf("%s: reviewed caller count changed for %s", caller, helper)
			}
		}
	}
	if literals != 2 {
		t.Error("reviewed off/bounded constructor literal count changed", literals)
	}
	t.Log("PulseFeed has twelve reviewed off-only selectors, including pinned reader-state and transitive helper boundaries")
}

func pulseLegacySourcesBoundary(function *ast.FuncDecl, ownedMethod string) (legacy, owned int, failure error) {
	parents := map[ast.Node]ast.Node{}
	var stack []ast.Node
	ast.Inspect(function, func(node ast.Node) bool {
		if node == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		if len(stack) != 0 {
			parents[node] = stack[len(stack)-1]
		}
		stack = append(stack, node)
		return true
	})
	ast.Inspect(function.Body, func(node ast.Node) bool {
		method, ok := node.(*ast.SelectorExpr)
		if !ok || failure != nil || method.Sel.Name != "PulseSources" && (ownedMethod == "" || method.Sel.Name != ownedMethod) {
			return true
		}
		mode := 2
		if method.Sel.Name == "PulseSources" {
			legacy++
			mode = 1
		} else {
			owned++
		}
		call, direct := parents[method].(*ast.CallExpr)
		if !direct || call.Fun != method || !deliveryBoundaryStoreReceiver(method.X) {
			failure = fmt.Errorf("%s must be a direct call on the reviewed store receiver", method.Sel.Name)
		} else if deliveryBoundaryMode(function, call, parents) != mode {
			failure = fmt.Errorf("%s is not exclusively in its reviewed metadata mode", method.Sel.Name)
		}
		return true
	})
	return
}

func TestScaleP21PulseLegacySourcesBoundary(t *testing.T) {
	sites := map[string]string{
		"internal/store/scale_p21_hot_pulse_read.go:beginPulseRead":     "BeginInstalledMetadataRead",
		"cmd/server/realtime_entity_payloads.go:pagedPulseSourceEvent":  "BeginPulseRead",
		"cmd/server/scale_p12_metadata_source.go:buildPulseEventSource": "BeginPulseRead",
	}
	seen := map[string]bool{}
	root := filepath.Clean("../..")
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root && strings.HasPrefix(entry.Name(), ".") || entry.Name() == "vendor" ||
				path == filepath.Join(root, "internal", "store", "data") {
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
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok {
				ast.Inspect(declaration, func(node ast.Node) bool {
					if method, ok := node.(*ast.SelectorExpr); ok && method.Sel.Name == "PulseSources" {
						t.Errorf("%s: legacy method value outside a reviewed function", relative)
					}
					return true
				})
				continue
			}
			name := filepath.ToSlash(relative) + ":" + function.Name.Name
			ownedMethod, reviewed := sites[name]
			legacy, owned, err := pulseLegacySourcesBoundary(function, ownedMethod)
			if err != nil {
				t.Errorf("%s: %v", name, err)
			}
			if legacy != 0 && !reviewed {
				t.Errorf("%s: unreviewed PulseSources caller", name)
			}
			if reviewed {
				seen[name] = true
				if legacy != 1 || owned != 1 {
					t.Errorf("%s: reviewed dispatch changed: legacy=%d owned=%d", name, legacy, owned)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for site := range sites {
		if !seen[site] {
			t.Error("reviewed PulseSources dispatch disappeared", site)
		}
	}
	for _, fixture := range []struct {
		name, body string
		allowed    bool
	}{
		{"off", `if !s.BoundedMetadataEnabled() { s.PulseSources("w") }`, true},
		{"else", `if s.BoundedMetadataEnabled() { return }; s.PulseSources("w")`, true},
		{"unguarded", `s.PulseSources("w")`, false},
		{"bounded", `if s.BoundedMetadataEnabled() { s.PulseSources("w") }`, false},
		{"method-value", `if !s.BoundedMetadataEnabled() { read := s.PulseSources; read("w") }`, false},
		{"closure", `if !s.BoundedMetadataEnabled() { go func() { s.PulseSources("w") }() }`, false},
		{"other-receiver", `if !s.BoundedMetadataEnabled() { other.PulseSources("w") }`, false},
		{"environment-guard", `if enabled { return }; s.PulseSources("w")`, false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "boundary.go", "package boundary; func (s *SQLiteStore) read() { "+fixture.body+" }", 0)
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = pulseLegacySourcesBoundary(file.Decls[0].(*ast.FuncDecl), "")
			if (err == nil) != fixture.allowed {
				t.Fatal("legacy boundary accepted an unproved mode or rejected an off branch", err)
			}
		})
	}
	t.Log("PulseSources has exactly three reviewed off-only dispatches; PulseFeed has its independent boundary guard")
}
