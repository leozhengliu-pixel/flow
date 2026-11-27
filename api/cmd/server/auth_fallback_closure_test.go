package main

import (
	"bytes"
	"context"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// literalFallbackRoutes lists registered routes that may still
// reach resourceAllowedLegacy's whole-workspace projection with bounded
// metadata on. It is empty: every non-public route is decided by a reviewed
// bounded reader or a registered COLD facade. The walk below fails on any
// difference in either direction. An entry suffixed "[escaped id]" would
// mark a pattern reached only when an escaped "/" in an ID shifts the
// decoded segments into another literal branch.
var literalFallbackRoutes = []string{}

func authFallbackClosurePatterns(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	set := map[string]bool{}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) != 2 {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "HandleFunc" && selector.Sel.Name != "Handle" {
				return true
			}
			literal, ok := call.Args[0].(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				t.Fatalf("%s: route registration without a literal pattern", name)
			}
			pattern, err := strconv.Unquote(literal.Value)
			if err != nil {
				t.Fatal(err)
			}
			set[pattern] = true
			return true
		})
	}
	patterns := make([]string, 0, len(set))
	for pattern := range set {
		patterns = append(patterns, pattern)
	}
	sort.Strings(patterns)
	return patterns
}

func TestAuthorizationFallbackClosure(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	text := func(node ast.Node) string {
		var output bytes.Buffer
		if err := format.Node(&output, token.NewFileSet(), node); err != nil {
			t.Fatal(err)
		}
		return output.String()
	}
	t.Run("hook-precedes-the-only-projection", func(t *testing.T) {
		function := authIngressBoundaryFunction(t, "auth.go", "*server", "resourceAllowedLegacy")
		hooks, projections, acquisitions := 0, 0, 0
		ast.Inspect(function.Body, func(node ast.Node) bool {
			switch node := node.(type) {
			case *ast.BlockStmt:
				for index, statement := range node.List {
					branch, ok := statement.(*ast.IfStmt)
					if !ok || branch.Init == nil || text(branch.Init) != "allowed, handled := s.boundedFallbackAuthorization(r, workspace, userID)" {
						continue
					}
					hooks++
					if index != 0 || text(branch.Cond) != "handled" || branch.Else != nil || len(branch.Body.List) != 1 || text(branch.Body.List[0]) != "return allowed" {
						t.Fatal("fallback hook is not the first, handled-only statement of its block")
					}
					next, ok := node.List[1].(*ast.IfStmt)
					if !ok || text(next.Cond) != "!acquireProjection(r)" {
						t.Fatal("fallback hook no longer immediately precedes the projection slot")
					}
				}
			case *ast.CallExpr:
				switch text(node.Fun) {
				case "repo.PagedWorkspaceMetadata", "s.store.PagedWorkspaceMetadata":
					projections++
				case "acquireProjection":
					acquisitions++
				}
			}
			return true
		})
		if hooks != 1 || projections != 1 || acquisitions != 1 {
			t.Fatal("literal projection/hook contract changed", hooks, projections, acquisitions)
		}
	})
	t.Run("decoded-shape-table", func(t *testing.T) {
		for _, row := range []struct {
			method, path string
			decision     fallbackDecision
			kind, ref    string
			output       bool
		}{
			{http.MethodGet, "/api/webhooks", fallbackMembership, "", "", false},
			{http.MethodPost, "/api/labels", fallbackMembership, "", "", false},
			{http.MethodGet, "/api", fallbackMembership, "", "", false},
			{http.MethodGet, "/uploads/projects", fallbackMembership, "", "", false},
			{http.MethodPost, "/uploads/projects", fallbackRecord, "projectsBody", "", true},
			{http.MethodGet, "/api/issues", fallbackMembership, "", "", false},
			{http.MethodPost, "/api/issues", fallbackLiteral, "", "", false},
			{http.MethodGet, "/api/issues/a/history", fallbackLiteral, "", "", false},
			{http.MethodGet, "/api/issue-records/a", fallbackLiteral, "", "", false},
			{http.MethodPost, "/api/projects", fallbackRecord, "projectsBody", "", true},
			{http.MethodPost, "/api/initiatives", fallbackRecord, "initiativesBody", "", true},
			{http.MethodGet, "/api/projects", fallbackMembership, "", "", false},
			{http.MethodPut, "/api/initiatives", fallbackNonGuest, "", "", false},
			{http.MethodDelete, "/api/projects/p", fallbackPoint, "projects", "p", false},
			{http.MethodPost, "/api/projects/p/comments", fallbackPoint, "projects", "p", false},
			{http.MethodPatch, "/api/projects/p", fallbackRecord, "projectsBody", "p", false},
			{http.MethodPatch, "/api/projects/p/comments/c", fallbackRecord, "projectsBody", "p", false},
			{http.MethodPost, "/api/projects/p/relations", fallbackRecord, "projectsBody", "p", false},
			{http.MethodPost, "/api/projects/p/relations/r", fallbackRecord, "projectsBody", "p", false},
			{http.MethodPatch, "/api/projects/p/relations/r", fallbackPoint, "projects", "p", false},
			{http.MethodPatch, "/api/projects/p/relations", fallbackRecord, "projectsBody", "p", false},
			{http.MethodPatch, "/api/projects/a%2Fb/relations/r", fallbackRecord, "projectsBody", "a", false},
			{http.MethodDelete, "/api/initiatives/i/relations/r", fallbackPoint, "initiatives", "i", false},
			{http.MethodDelete, "/api/projects/a%2Fb", fallbackPoint, "projects", "a", false},
			{http.MethodPost, "/%61pi/%70rojects/p/%63omments", fallbackPoint, "projects", "p", false},
			{http.MethodGet, "/api/views", fallbackMembership, "", "", false},
			{http.MethodPost, "/api/views", fallbackRecord, "savedViews", "", true},
			{http.MethodDelete, "/api/views/v", fallbackRecord, "savedViews", "v", false},
			{http.MethodPost, "/api/dashboards", fallbackRecord, "dashboards", "", true},
			{http.MethodGet, "/api/dashboards/d/results", fallbackRecord, "dashboards", "d", false},
			{http.MethodGet, "/api/asks", fallbackMembership, "", "", false},
			{http.MethodPost, "/api/asks", fallbackRecord, "asks", "", true},
			{http.MethodPost, "/api/asks/a/decision", fallbackRecord, "asks", "a", false},
			{http.MethodPut, "/api/customers", fallbackNonGuest, "", "", false},
			{http.MethodPatch, "/api/customers/c", fallbackRecord, "customers", "c", false},
			{http.MethodGet, "/api/customer-requests", fallbackNonGuest, "", "", false},
			{http.MethodPost, "/api/customer-requests", fallbackRecord, "customerRequests", "", true},
			{http.MethodDelete, "/api/customer-requests/r/archive", fallbackRecord, "customerRequests", "r", false},
			{http.MethodGet, "/api/favorites", fallbackMembership, "", "", false},

			{http.MethodGet, "/api/reviews", fallbackMembership, "", "", false},
			{http.MethodPost, "/api/reviews/r/comments", fallbackRecord, "reviews", "r", false},
			{http.MethodGet, "/api/documents", fallbackMembership, "", "", false},
			{http.MethodPost, "/api/documents", fallbackRecord, "documentCreate", "", true},
			{http.MethodPatch, "/api/documents/d/comments/c", fallbackLiteral, "", "", false},
			{http.MethodGet, "/api/releases", fallbackMembership, "", "", false},
			{http.MethodPost, "/api/releases/reorder", fallbackRecord, "releases", "reorder", false},
			{http.MethodPost, "/api/releases", fallbackRecord, "releases", "", true},
			{http.MethodGet, "/api/release-pipelines/p", fallbackRecord, "releasePipelines", "p", false},
			{http.MethodGet, "/api/cycles", fallbackMembership, "", "", false},
			{http.MethodGet, "/api/cycles/c", fallbackRecord, "cycles", "c", false},
			{http.MethodPatch, "/api/cycles/c/x/y", fallbackRecord, "cycles", "c", false},
			{http.MethodGet, "/api/teams", fallbackMembership, "", "", false},
			{http.MethodGet, "/api/teams/t", fallbackTeamRow, "", "t", false},
			{http.MethodPatch, "/api/teams/t/settings", fallbackTeamRow, "", "t", false},
			{http.MethodGet, "/api/teams/t/resources", fallbackTeamContent, "", "t", true},
			{http.MethodHead, "/api/teams/t/resources", fallbackTeamContent, "", "t", false},
			{http.MethodPost, "/api/teams/t/resources", fallbackTeamContent, "", "t", false},
			{http.MethodGet, "/api/teams/t/resources/x", fallbackTeamContent, "", "t", false},
			{http.MethodDelete, "/api/teams/t/resource-sections/s", fallbackTeamContent, "", "t", false},
			{http.MethodGet, "/api/notifications/n", fallbackLiteral, "", "", false},
			{http.MethodPatch, "/api/workspaces/w", fallbackMembership, "", "", false},
			{http.MethodPost, "/api/workspaces/w/teams", fallbackMembership, "", "", false},
			{http.MethodPatch, "/api/workspaces/w/teams/t", fallbackTeamRow, "", "t", false},
			{http.MethodPut, "/api/workspaces/w/teams/t/members/u", fallbackTeamRow, "", "t", false},
		} {
			request := httptest.NewRequest(row.method, row.path, nil)
			got := fallbackShape(request)
			want := fallbackRouteShape{decision: row.decision, kind: row.kind, ref: row.ref, output: row.output}
			if got != want {
				t.Fatalf("%s %s: shape %+v, want %+v", row.method, row.path, got, want)
			}
		}
		for _, row := range []struct{ method, path, target, ref string }{
			{http.MethodPut, "/api/favorites/project/p", "project", "p"},
			{http.MethodDelete, "/api/subscriptions/%20Issue%20/i", "issue", "i"},
			{http.MethodPatch, "/api/favorites/view/a%2Fb", "view", "a"},
		} {
			got := fallbackShape(httptest.NewRequest(row.method, row.path, nil))
			if got != (fallbackRouteShape{decision: fallbackRecord, kind: "references", ref: row.ref, target: row.target}) {
				t.Fatal("reference shape", row, got)
			}
		}
		for _, body := range []struct {
			text     string
			decision fallbackDecision
		}{
			{`{"favorite":true}`, fallbackReviewPreference},
			{`{"favorite":false}`, fallbackReviewPreference},
			{`{"favorite":null}`, fallbackRecord},
			{`{"favorite":true,"title":"x"}`, fallbackRecord},
			{`{}`, fallbackRecord},
		} {
			request := httptest.NewRequest(http.MethodPatch, "/api/reviews/r", strings.NewReader(body.text))
			if got := fallbackShape(request); got.decision != body.decision || got.ref != "r" || got.decision == fallbackRecord && got.kind != "reviews" {
				t.Fatal("embedded review favorite shape", body.text, got)
			}
		}
	})
	t.Run("route-table-walk", func(t *testing.T) {
		patterns := authFallbackClosurePatterns(t)
		if len(patterns) < 800 {
			t.Fatal("route table walk lost registrations", len(patterns))
		}
		for _, repo := range scaleServerTestStores(t) {
			f := setupPolicyDifferential(t, repo)
			workspace := f.data.Workspace.URLKey
			mux := http.NewServeMux()
			for _, pattern := range patterns {
				mux.HandleFunc(pattern, func(http.ResponseWriter, *http.Request) {})
			}
			literal, bounded := &server{store: repo}, &server{store: f.probe}
			plain, escapedOnly, cold := map[string]bool{}, map[string]bool{}, map[string]bool{}
			requests := 0
			for _, pattern := range patterns {
				method, path, found := strings.Cut(pattern, " ")
				if !found {
					path, method = pattern, ""
				}
				methods := []string{method}
				switch method {
				case "":
					methods = []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete}
				case http.MethodGet:
					methods = []string{http.MethodGet, http.MethodHead}
				}
				for _, method := range methods {
					for _, value := range []string{"sw490-missing", f.public, "m2-unrelated", url.PathEscape("sw490/escaped")} {
						segments := strings.Split(path, "/")
						for index, segment := range segments {
							switch {
							case !strings.HasPrefix(segment, "{"):
							case segment == "{$}":
								segments[index] = ""
							case strings.Contains(segment, "..."):
								segments[index] = value + "/tail"
							case strings.Contains(strings.ToLower(segment), "workspace"):
								segments[index] = workspace
							default:
								segments[index] = value
							}
						}
						target, err := url.Parse(strings.Join(segments, "/"))
						if err != nil {
							t.Fatal(err)
						}
						if publicAuthPath(target.Path) {
							continue // Never reaches workspace authorization.
						}
						for _, principal := range []int{0, 2, 3} {
							for _, service := range []*server{literal, bounded} {
								user := f.principals[principal].user
								request := httptest.NewRequest(method, target.String(), strings.NewReader(`{}`))
								request.Header.Set("X-Workspace-Key", workspace)
								request = request.WithContext(f.literalRequest(user, nil, nil).Context())
								// Production rewrites legacy issue writes before routing.
								boundedLegacyIssueWrites(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { request = r })).ServeHTTP(nil, request)
								hold := &projectionHold{gate: newMetadataReadGate(64, 64, time.Minute, 0)}
								ctx := context.WithValue(request.Context(), projectionHoldKey{}, hold)
								if service == bounded {
									ctx = context.WithValue(ctx, coldAuthorizationMuxKey{}, mux)
								}
								request = request.WithContext(ctx)
								request, release := beginRequestResourceReads(request)
								service.resolveIssuePathReference(request)
								if service == literal {
									if _, handled := service.boundedFallbackAuthorization(request, workspace, user.ID); handled {
										release()
										t.Fatal("fallback adapter changed flag-off authorization", method, target)
									}
									release()
									continue
								}
								requests++
								_ = service.resourceAllowed(request, workspace, user.ID)
								if hold.held {
									<-hold.gate.slots
									key := method + " " + path
									if method == http.MethodHead && found {
										key = http.MethodGet + " " + path
									}
									if _, selected := scaleColdAuthorizationRoute(request); selected {
										cold[key] = true
									} else if strings.Contains(value, "%") {
										escapedOnly[key] = true
									} else {
										plain[key] = true
									}
								}
								release()
							}
						}
					}
				}
			}
			// A pattern reached only through an escaped "/" in an ID is keyed
			// separately: its decoded segments select another literal branch.
			reached := map[string]bool{}
			for key := range plain {
				reached[key] = true
			}
			for key := range escapedOnly {
				if !plain[key] {
					reached[key+" [escaped id]"] = true
				}
			}
			var unexpected, closed []string
			for key := range reached {
				if !slices.Contains(literalFallbackRoutes, key) {
					unexpected = append(unexpected, key)
				}
			}
			for _, key := range literalFallbackRoutes {
				if !reached[key] {
					closed = append(closed, key)
				}
			}
			sort.Strings(unexpected)
			sort.Strings(closed)
			if len(unexpected) != 0 || len(closed) != 0 {
				t.Fatalf("HOT routes reaching the whole-workspace authorization projection with bounded metadata on differ from the pinned literal list:\nunexpected=%q\nno longer reached=%q", unexpected, closed)
			}
			if len(cold) == 0 {
				t.Fatal("no registered COLD route reached its cold facade; the walk lost its mux context")
			}
			t.Logf("SW490 route walk: patterns=%d bounded requests=%d literal-projection routes=%d cold-facade routes=%d", len(patterns), requests, len(reached), len(cold))
		}
	})
}
