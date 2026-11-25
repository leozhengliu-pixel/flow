package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

type scaleP21SW125Writer struct {
	*httptest.ResponseRecorder
	t                         *testing.T
	source                    *store.SQLiteStore
	scope                     *requestResourceReads
	held                      int
	prepares, headers, writes int
}

func (w *scaleP21SW125Writer) sample() {
	work := w.source.MetadataWorkUsedForTesting()
	w.held = max(w.held, work)
	if w.source.BoundedMetadataEnabled() && (work == 0 || w.scope == nil || w.scope.closed) {
		w.t.Fatal("account response lost its read or registered request scope during output")
	}
}

func (w *scaleP21SW125Writer) prepareJSONOutput(any) error {
	w.sample()
	w.prepares++
	return nil
}

func (w *scaleP21SW125Writer) WriteHeader(code int) {
	w.sample()
	w.headers++
	w.ResponseRecorder.WriteHeader(code)
}

func (w *scaleP21SW125Writer) Write(body []byte) (int, error) {
	w.sample()
	w.writes++
	return w.ResponseRecorder.Write(body)
}

func scaleP21SW125Fixture(t *testing.T) (*store.SQLiteStore, store.DatabaseConfig, []string) {
	t.Helper()
	config := scaleP03DatabaseConfig(t)
	repo, err := store.OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	resources := scaleP03TrackResources(t, repo)
	owner, token, err := repo.Register(t.Context(), "SW125 isolated owner", idgen.New("sw125-person-")+"@example.test", "sw125-owned-password")
	if err != nil {
		t.Fatal(err)
	}
	resources.users = append(resources.users, owner)
	if err := repo.VerifyEmail(t.Context(), token); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, time.November, 15, 1, 2, 3, 456789123, time.FixedZone("sw125", 20700))
	namePrefix := idgen.New("sw125-catalog-") + "-"
	var keys []string
	for index, name := range []string{"Zulu", "alpha", "Beta"} {
		key := idgen.New("sw125-workspace-")
		data, err := repo.CreateWorkspaceWith(store.ContextWithActor(t.Context(), owner), namePrefix+name, key, "us", func(data *domain.Bootstrap) error {
			data.Workspace.CreatedAt = at
			// Internal producers retain native NUL even when canonical SQL
			// strips it. The paired view must use the same installed header.
			data.Workspace.Icon, data.Workspace.LogoURL, data.Workspace.Color = "native\x00icon", "data:image/png;base64,sw125", "green"
			if index == 1 {
				deletion := at.Add(7 * 24 * time.Hour)
				data.Workspace.Region, data.Workspace.DeletionRequestedAt = "eu", &deletion
			}
			for issue := 0; issue <= index; issue++ {
				data.Issues = append(data.Issues, domain.Issue{
					ID: idgen.New("sw125-issue-"), Identifier: fmt.Sprintf("SW125-%d", issue+1), Number: issue + 1,
					Title: "SW125_EXCLUDED_ISSUE_BODY", Description: strings.Repeat("SW125_EXCLUDED_DESCRIPTION ", 100),
					Team: data.Teams[0], State: data.States[0], Creator: owner,
				})
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		resources.workspaces = append(resources.workspaces, key)
		if data.Viewer.ID != owner.ID {
			t.Fatal("isolated account fixture did not use its registered owner")
		}
		keys = append(keys, key)
	}
	// Keep a catalog entry outside the focused fixture keys, even on an empty
	// local database, so a selected-workspace view cannot pass this test.
	other, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), owner), namePrefix+"outside-focus", idgen.New("sw125-catalog-other-"), "us")
	if err != nil {
		t.Fatal(err)
	}
	resources.workspaces = append(resources.workspaces, other.Workspace.URLKey)
	return repo, config, keys
}

type scaleP21SW125Result struct {
	Code   int
	Header http.Header
	Body   []byte
}

func scaleP21SW125Serve(t *testing.T, source *store.SQLiteStore, path string, configured *server, outer bool) scaleP21SW125Result {
	t.Helper()
	service := &server{
		store: source, authDisabled: true,
		workspaceDefaultRegion:         configured.workspaceDefaultRegion,
		workspaceRegionSelectorEnabled: configured.workspaceRegionSelectorEnabled,
	}
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Authorization", "Bearer sw125-unused-development-credential")
	release := func() {}
	if outer {
		request, release = beginRequestResourceReads(request)
	}
	defer release()
	beforeWork, beforeDecodes := source.MetadataWorkUsedForTesting(), source.WorkspaceMetadataDecodes()
	writer := &scaleP21SW125Writer{ResponseRecorder: httptest.NewRecorder(), t: t, source: source}
	scopeReleased, registered := false, false
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/auth/session", service.authSession)
	mux.HandleFunc("GET /api/account/bootstrap", service.accountBootstrap)
	before := time.Now().UTC()
	service.authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writer.scope, _ = r.Context().Value(requestResourceReadsKey{}).(*requestResourceReads)
		if source.BoundedMetadataEnabled() || outer {
			if writer.scope == nil || writer.scope.closed {
				t.Fatal("registered account dispatch has no open request scope")
			}
			registered = registerRequestReadClose(r.Context(), func() {
				if writer.writes == 0 {
					t.Error("request scope released before account body encoding")
				}
				scopeReleased = true
			})
			if !registered {
				t.Fatal("account dispatch rejected its scope closer")
			}
		}
		mux.ServeHTTP(w, r)
		// The account handlers own their local read through writeJSON.
		if source.MetadataWorkUsedForTesting() != beforeWork {
			t.Fatal("account handler left its local owned read open")
		}
	})).ServeHTTP(writer, request)
	after := time.Now().UTC()
	if outer && (scopeReleased || writer.scope.closed) {
		t.Fatal("account dispatch closed its caller's outer scope")
	}
	release()
	release()
	if registered && (!scopeReleased || !writer.scope.closed) {
		t.Fatal("account request scope or registered closer survived final release")
	}
	if source.MetadataWorkUsedForTesting() != beforeWork || source.WorkspaceMetadataDecodes() != beforeDecodes {
		t.Fatal("account route leaked work or hydrated complete metadata")
	}
	if writer.Code != http.StatusOK || writer.prepares != 1 || writer.headers != 1 || writer.writes == 0 {
		t.Fatalf("account route did not encode its successful output: status=%d prepares=%d headers=%d writes=%d body=%s",
			writer.Code, writer.prepares, writer.headers, writer.writes, writer.Body)
	}
	body := bytes.Clone(writer.Body.Bytes())
	if path == "/api/auth/session" {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(body, &fields); err != nil {
			t.Fatal(err)
		}
		var expires time.Time
		if err := json.Unmarshal(fields["expiresAt"], &expires); err != nil {
			t.Fatal(err)
		}
		if expires.Before(before.Add(24*time.Hour)) || expires.After(after.Add(24*time.Hour)) || expires.Location() != time.UTC {
			t.Fatal("auth session expiry escaped its literal 24-hour UTC bracket", expires, before, after)
		}
		field := append([]byte(`"expiresAt":`), fields["expiresAt"]...)
		if bytes.Count(body, field) != 1 {
			t.Fatal("auth session has no unique generated expiry field")
		}
		body = bytes.Replace(body, field, []byte(`"expiresAt":"sw125-generated"`), 1)
	}
	return scaleP21SW125Result{Code: writer.Code, Header: writer.Header().Clone(), Body: body}
}

func TestScaleP21SW125AccountRoutesDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	repo, config, keys := scaleP21SW125Fixture(t)
	pairs := 0
	for _, phase := range []struct {
		name, hydration string
		reopen          bool
	}{
		{"warm", "on", false},
		{"reopened-hydration-off", "off", true},
		{"reopened-hydration-on", "on", true},
	} {
		t.Setenv("FLOW_RECORD_HYDRATION", phase.hydration)
		literal := repo
		if phase.reopen {
			var err error
			literal, err = store.OpenDatabase(config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = literal.Close() })
		}
		view, err := literal.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		catalogSize := len(literal.Account().Workspaces)
		ownedCounts := map[string]int{keys[0]: 1, keys[1]: 2, keys[2]: 3}
		for _, settings := range []struct {
			name, region, platform string
			selector               bool
		}{
			{"defaults", "", "slack", false},
			{"fixed-region", "eu", "feishu", false},
			{"selectable-region", "us", "lark", true},
		} {
			t.Setenv("FLOW_CHAT_PLATFORM", settings.platform)
			service := &server{workspaceDefaultRegion: settings.region, workspaceRegionSelectorEnabled: settings.selector}
			for _, outer := range []bool{false, true} {
				for _, path := range []string{"/api/auth/session", "/api/account/bootstrap"} {
					t.Run(fmt.Sprintf("%s/%s/outer=%t%s", phase.name, settings.name, outer, path), func(t *testing.T) {
						old := scaleP21SW125Serve(t, literal, path, service, outer)
						bounded := scaleP21SW125Serve(t, view, path, service, outer)
						if old.Code != bounded.Code || !reflect.DeepEqual(old.Header, bounded.Header) || !bytes.Equal(old.Body, bounded.Body) {
							t.Fatalf("development account output differs: status=%d/%d headers=%v/%v\nold=%s\nbounded=%s",
								old.Code, bounded.Code, old.Header, bounded.Header, old.Body, bounded.Body)
						}
						if bytes.Contains(bounded.Body, []byte("SW125_EXCLUDED_")) {
							t.Fatal("account response included issue bodies")
						}
						if path == "/api/account/bootstrap" {
							var account domain.AccountBootstrap
							if err := json.Unmarshal(bounded.Body, &account); err != nil {
								t.Fatal(err)
							}
							if len(account.Workspaces) != catalogSize || account.ChatPlatform != settings.platform ||
								account.WorkspaceDefaultRegion != service.defaultWorkspaceRegion() ||
								account.WorkspaceRegionSelectorEnabled != service.regionSelectorEnabled() {
								t.Fatal("account route lost deployment configuration or owned workspaces", account)
							}
							var ownedOrder []string
							for _, membership := range account.Workspaces {
								key := membership.Workspace.URLKey
								if count, owned := ownedCounts[key]; owned {
									if membership.IssueCount != count {
										t.Fatal("account route lost the owned workspace's SQL count", key, membership.IssueCount, count)
									}
									ownedOrder = append(ownedOrder, key)
								}
							}
							if !reflect.DeepEqual(ownedOrder, []string{keys[1], keys[2], keys[0]}) {
								t.Fatal("account route lost sorted owned memberships within the complete installed catalog", ownedOrder)
							}
						}
						pairs++
					})
				}
			}
		}
	}
	t.Logf("driver=%s exact actual-handler account pairs=%d; complete installed catalog; normalized only generated AuthSession.ExpiresAt", config.Driver, pairs)
}

func TestScaleP21SW125AccountNativeIconProducerWitness(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	repo, _, keys := scaleP21SW125Fixture(t)
	service := &server{store: repo, authDisabled: true}
	request := httptest.NewRequest(http.MethodPatch, "/api/workspaces/"+keys[0], strings.NewReader(`{"icon":"native\u0000icon"}`))
	request.SetPathValue("workspaceKey", keys[0])
	response := httptest.NewRecorder()
	service.updateWorkspace(response, request)
	if response.Code != http.StatusOK {
		t.Fatal("HTTP icon producer failed", response.Code, response.Body)
	}
	find := func(account domain.AccountBootstrap) domain.Workspace {
		t.Helper()
		for _, membership := range account.Workspaces {
			if membership.Workspace.URLKey == keys[0] {
				return membership.Workspace
			}
		}
		t.Fatal("owned icon witness workspace missing")
		return domain.Workspace{}
	}
	workspace := find(repo.Account())
	if workspace.Icon != "nativeicon" {
		t.Fatalf("HTTP decoder did not sanitize the icon before installation: %q", workspace.Icon)
	}
	// The internal API persists sanitized canonical JSON but installs the
	// original native Workspace. Account must keep that installed value.
	const nativeIcon = "native\x00icon"
	workspace.Icon = nativeIcon
	if _, err := repo.UpdateWorkspace(t.Context(), keys[0], workspace); err != nil {
		t.Fatal(err)
	}
	if got := find(repo.Account()).Icon; got != nativeIcon {
		t.Fatalf("internal UpdateWorkspace did not install the native witness: %q", got)
	}
	view, err := repo.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got := find(view.Account()).Icon; got != nativeIcon {
		t.Fatalf("warm all-workspace test view changed the internal producer's installed icon: literal=%q bounded-policy=%q", nativeIcon, got)
	}
	old := scaleP21SW125Serve(t, repo, "/api/account/bootstrap", service, false)
	bounded := scaleP21SW125Serve(t, view, "/api/account/bootstrap", service, false)
	if !reflect.DeepEqual(old, bounded) {
		t.Fatalf("native icon account route differs:\nold=%s\nbounded=%s", old.Body, bounded.Body)
	}
}

type scaleP21SW125AccountCall struct {
	function string
	safe     bool
	direct   bool
}

func scaleP21SW125Store(expr ast.Expr) string {
	if receiver, ok := expr.(*ast.Ident); ok && receiver.Name == "s" {
		return "s"
	}
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "store" {
		return ""
	}
	receiver, ok := selector.X.(*ast.Ident)
	if ok && receiver.Name == "s" {
		return "s.store"
	}
	return ""
}

func scaleP21SW125Flag(expr ast.Expr, receiver string) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	return ok && selector.Sel.Name == "BoundedMetadataEnabled" && receiver != "" && scaleP21SW125Store(selector.X) == receiver
}

func scaleP21SW125FlagOff(stack []ast.Node, receiver string) bool {
	for at, node := range stack {
		branch, conditional := node.(*ast.IfStmt)
		offArm := conditional && branch.Init == nil && at+1 < len(stack) &&
			scaleP21SW125Flag(branch.Cond, receiver) && branch.Else == stack[at+1]
		if conditional && branch.Init == nil && at+1 < len(stack) && branch.Body == stack[at+1] {
			if negated, ok := branch.Cond.(*ast.UnaryExpr); ok && negated.Op == token.NOT && scaleP21SW125Flag(negated.X, receiver) {
				offArm = true
			}
		}
		if offArm {
			escaped := false
			for _, child := range stack[at+1:] {
				if _, ok := child.(*ast.FuncLit); ok {
					escaped = true
				}
			}
			if !escaped {
				return true
			}
		}
		block, ok := node.(*ast.BlockStmt)
		if !ok || at+1 >= len(stack) {
			continue
		}
		// OAuth uses the equivalent `if on { return ... }; return old`.
		for index, statement := range block.List {
			if statement != stack[at+1] || index == 0 {
				continue
			}
			previous, ok := block.List[index-1].(*ast.IfStmt)
			if !ok || previous.Init != nil || previous.Else != nil || !scaleP21SW125Flag(previous.Cond, receiver) || len(previous.Body.List) != 1 {
				continue
			}
			if _, ok := previous.Body.List[0].(*ast.ReturnStmt); !ok {
				continue
			}
			escaped := false
			for _, child := range stack[at+1:] {
				if _, ok := child.(*ast.FuncLit); ok {
					escaped = true
				}
			}
			if !escaped {
				return true
			}
		}
	}
	return false
}

func scaleP21SW125AccountCalls(file *ast.File) []scaleP21SW125AccountCall {
	var stack []ast.Node
	var calls []scaleP21SW125AccountCall
	ast.Inspect(file, func(node ast.Node) bool {
		if node == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		stack = append(stack, node)
		selector, ok := node.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Account" {
			return true
		}
		parent, direct := stack[len(stack)-2].(*ast.CallExpr)
		direct = direct && parent.Fun == selector
		receiver := scaleP21SW125Store(selector.X)
		if !direct && receiver == "" {
			return true
		}
		function := "<global>"
		for _, ancestor := range stack {
			if declaration, ok := ancestor.(*ast.FuncDecl); ok {
				function = declaration.Name.Name
			}
		}
		calls = append(calls, scaleP21SW125AccountCall{
			function: function, direct: direct,
			safe: direct && receiver != "" && scaleP21SW125FlagOff(stack, receiver),
		})
		return true
	})
	return calls
}

func TestScaleP21SW125AccountProductionCallset(t *testing.T) {
	expected := map[string]int{
		"cmd/server/auth.go:authenticate":                           1,
		"cmd/server/auth.go:authSession":                            1,
		"cmd/server/main.go:accountBootstrap":                       1,
		"cmd/server/oauth_mcp_auth.go:oauthRequestUser":             1,
		"internal/store/scale_p21_account_read.go:BeginAccountRead": 1,
	}
	actual := map[string]int{}
	err := filepath.WalkDir("../..", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !bytes.Contains(source, []byte("Account")) {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel("../..", path)
		if err != nil {
			return err
		}
		for _, call := range scaleP21SW125AccountCalls(file) {
			key := filepath.ToSlash(relative) + ":" + call.function
			actual[key]++
			if !call.safe {
				t.Errorf("Account call or method escape is reachable without its exact store's flag-off dominance: %s direct=%t", key, call.direct)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("production Account callset changed: actual=%v expected=%v", actual, expected)
	}
	t.Log("all four server Account calls and the literal adapter fallback are flag-off only; this certifies their bypass, not Account's unrestricted count helper")
}

func TestScaleP21SW125AccountCallsetRejectsFalseProofs(t *testing.T) {
	for _, fixture := range []struct {
		name, body string
		safe       bool
	}{
		{"else", `if s.store.BoundedMetadataEnabled() { return nil } else { return s.store.Account() }`, true},
		{"negative-body", `if !s.store.BoundedMetadataEnabled() { return s.store.Account() }; return nil`, true},
		{"terminating-on", `if s.store.BoundedMetadataEnabled() { return nil }; return s.store.Account()`, true},
		{"unguarded", `return s.store.Account()`, false},
		{"on-arm", `if s.store.BoundedMetadataEnabled() { return s.store.Account() }; return nil`, false},
		{"reversed-flag", `if !s.store.BoundedMetadataEnabled() { return nil } else { return s.store.Account() }`, false},
		{"other-store-flag", `if other.store.BoundedMetadataEnabled() { return nil }; return s.store.Account()`, false},
		{"other-store-read", `if s.store.BoundedMetadataEnabled() { return nil }; return other.store.Account()`, false},
		{"conditional-return", `if s.store.BoundedMetadataEnabled() { if enabled { return nil } }; return s.store.Account()`, false},
		{"intervening-statement", `if s.store.BoundedMetadataEnabled() { return nil }; changed(); return s.store.Account()`, false},
		{"escaping-closure", `if s.store.BoundedMetadataEnabled() { return nil } else { return func() any { return s.store.Account() } }`, false},
		{"method-value", `if s.store.BoundedMetadataEnabled() { return nil } else { return s.store.Account }`, false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "sw125.go", "package main; func (s *server) check() any { "+fixture.body+" }", 0)
			if err != nil {
				t.Fatal(err)
			}
			calls := scaleP21SW125AccountCalls(file)
			if len(calls) != 1 || calls[0].safe != fixture.safe {
				t.Fatalf("Account dominance proof=%+v want safe=%t", calls, fixture.safe)
			}
		})
	}
}
