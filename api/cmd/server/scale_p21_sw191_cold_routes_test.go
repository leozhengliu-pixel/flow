package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW191ColdRouteBindings(t *testing.T) {
	_, path, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate production route registrations")
	}
	file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(filepath.Dir(path), "main.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	roots, expected := map[string]bool{}, map[string]string{}
	for _, route := range scaleP21ColdRoutes {
		_, method, found := strings.Cut(route.consumer, ":")
		if !found {
			t.Fatal("cold manifest lacks its inventoried consumer", route)
		}
		roots[method] = true
		pattern := route.method + " " + route.path
		if expected[pattern] != "" {
			t.Fatal("duplicate cold route manifest entry", pattern)
		}
		expected[pattern] = method
	}
	actual := map[string]string{}
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) != 2 {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "HandleFunc" {
			return true
		}
		target, ok := call.Args[1].(*ast.SelectorExpr)
		if !ok || !roots[target.Sel.Name] {
			return true
		}
		literal, ok := call.Args[0].(*ast.BasicLit)
		if !ok {
			t.Fatal("cold route registration needs an explicit pattern")
		}
		pattern, err := strconv.Unquote(literal.Value)
		if err != nil {
			t.Fatal(err)
		}
		actual[pattern] = target.Sel.Name
		return true
	})
	if len(expected) == 0 || !reflect.DeepEqual(expected, actual) {
		t.Fatalf("cold route manifest no longer covers its production bindings: manifest=%v actual=%v", expected, actual)
	}
}

func scaleP21SW191Serve(t *testing.T, service *server, request *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/loop-config", service.getLoopConfig)
	mux.HandleFunc("GET /api/workspace/loop-settings", service.getLoopConfig)
	mux.HandleFunc("PATCH /api/workspace/loop-settings", service.updateLoopSettings)
	result := httptest.NewRecorder()
	mux.ServeHTTP(result, request)
	return result
}

func TestScaleP21SW191ColdLoopRoutesDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace, app := f.data.Workspace.URLKey, idgen.New("sw191-app-")
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "members", "integrationConnections"), workspace, "scale_p21.sw191_seed", "", nil, func(data *domain.Bootstrap) error {
			data.Members = append(data.Members, domain.WorkspaceMember{User: domain.User{ID: app, Name: "warm\x00app"}, Role: "app", Status: "active"})
			data.IntegrationConnections = append(data.IntegrationConnections,
				domain.IntegrationConnection{ID: idgen.New("sw191-connection-"), Provider: "custom", Name: "warm\x00integration", Status: "connected"},
				domain.IntegrationConnection{ID: idgen.New("sw191-connection-"), Provider: "github", Status: "con\x00nected"},
			)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		baseline, found := repo.BootstrapFor(workspace)
		if !found {
			t.Fatal("owned cold route workspace missing")
		}
		saved, err := json.Marshal(baseline)
		if err != nil {
			t.Fatal(err)
		}
		reset := func(reload bool) {
			t.Helper()
			// The bounded view shares SQL but owns its installed generation.
			// Refresh the literal owner before declaring the reset's edits.
			if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
			var data domain.Bootstrap
			if err := json.Unmarshal(saved, &data); err != nil {
				t.Fatal(err)
			}
			if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw191_reset", "", nil, func(target *domain.Bootstrap) error {
				*target = data
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if reload {
				if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
			t.Fatal(err)
		}
		// The route bodies are literal and keep their original server receiver.
		// Exercise every principal/key shape without duplicating auth policy.
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			for _, path := range []string{"/api/loop-config", "/api/workspace/loop-settings"} {
				var expected *httptest.ResponseRecorder
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					request := f.literalRequest(user, key, nil)
					request.Method, request.URL.Path = http.MethodGet, path
					actual := scaleP21SW191Serve(t, &server{store: source}, request)
					if source == repo {
						expected = actual
					} else if actual.Code != expected.Code || !bytes.Equal(actual.Body.Bytes(), expected.Body.Bytes()) {
						t.Fatalf("cold read changed response: %d %s / %d %s", expected.Code, expected.Body.String(), actual.Code, actual.Body.String())
					}
				}
			}
		})
		for _, reload := range []bool{false, true} {
			for _, test := range []struct {
				name, body string
				code       int
			}{
				{"settings", `{"externalLoopTriggers":true,"trustedSourcesMode":"allowlist","trustedSourcesAllowlist":["integration:custom","appUser:missing"],"agentWebSearch":false}`, http.StatusOK},
				{"clear", `{"trustedSourcesMode":"","trustedSourcesAllowlist":[]}`, http.StatusOK},
				{"bad-mode", `{"trustedSourcesMode":"bad"}`, http.StatusBadRequest},
				{"bad-source", `{"trustedSourcesAllowlist":["wrong:source"]}`, http.StatusBadRequest},
				{"bad-json", `{`, http.StatusBadRequest},
			} {
				t.Run(fmt.Sprintf("reload=%t/%s", reload, test.name), func(t *testing.T) {
					var expectedBody []byte
					var expectedSettings domain.WorkspaceSettings
					for _, bounded := range []bool{false, true} {
						reset(reload)
						source := repo
						if bounded {
							if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
								t.Fatal(err)
							}
							source = f.probe
						}
						request := f.literalRequest(f.data.Viewer, nil, nil)
						request.Method, request.URL.Path = http.MethodPatch, "/api/workspace/loop-settings"
						request.Body = io.NopCloser(strings.NewReader(test.body))
						actual := scaleP21SW191Serve(t, &server{store: source}, request)
						if actual.Code != test.code {
							t.Fatalf("bounded=%t status=%d body=%s", bounded, actual.Code, actual.Body.String())
						}
						current, found := source.WorkspaceSettingsMetadata(workspace)
						if !found {
							t.Fatal("cold write lost parent installed settings")
						}
						if !bounded {
							expectedBody = bytes.Clone(actual.Body.Bytes())
							expectedSettings = current.WorkspaceSettings
						} else {
							if !bytes.Equal(expectedBody, actual.Body.Bytes()) {
								t.Fatalf("cold write response changed: %s / %s", expectedBody, actual.Body.String())
							}
							expectedSettings.UpdatedAt = current.WorkspaceSettings.UpdatedAt
							if !reflect.DeepEqual(expectedSettings, current.WorkspaceSettings) {
								t.Fatal("cold write changed parent installed settings")
							}
							read := f.literalRequest(f.data.Viewer, nil, nil)
							read.Method, read.URL.Path = http.MethodGet, "/api/workspace/loop-settings"
							after := scaleP21SW191Serve(t, &server{store: source}, read)
							if test.code == http.StatusOK && !bytes.Equal(actual.Body.Bytes(), after.Body.Bytes()) {
								t.Fatal("next cold reader missed the committed settings")
							}
						}
					}
				})
			}
		}
		if f.probe.MetadataWorkUsedForTesting() != 0 || f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("cold routes escaped their lifetime or used a HOT full accessor")
		}
		// Waiting is cancellable before a literal route body is invoked.
		entered, release := make(chan struct{}), make(chan struct{})
		done := make(chan error, 1)
		go func() {
			done <- f.probe.WithColdMetadataRead(t.Context(), "server/loop_settings.go:getLoopConfig", func(context.Context, *store.SQLiteStore) error {
				close(entered)
				<-release
				return nil
			})
		}()
		<-entered
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		request := f.literalRequest(f.data.Viewer, nil, nil).WithContext(ctx)
		request.Method, request.URL.Path = http.MethodGet, "/api/workspace/loop-settings"
		response := scaleP21SW191Serve(t, &server{store: f.probe}, request)
		close(release)
		if err := <-done; err != nil || response.Code == http.StatusOK {
			t.Fatal("cancelled cold route succeeded", response.Code, err)
		}
	}
}
