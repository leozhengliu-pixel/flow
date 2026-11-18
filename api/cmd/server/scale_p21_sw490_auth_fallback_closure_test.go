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

// scaleP21SW490LiteralFallbackRoutes are the registered routes whose literal
// resource predicate still reads projected records (asks,
// customers, customer requests, releases, pipelines, reviews,
// favorite/subscription references) or the request body (create roots,
// relation creation and PATCH under projects/initiatives). With bounded
// metadata on they keep the literal whole-workspace projection. Every other
// non-public route must be decided by a reviewed bounded reader or by a
// registered COLD facade. The walk below fails on any difference, in either
// direction, so this list can only shrink with a matching closure.
// "[escaped id]" marks a pattern that keeps the literal projection only when an
// escaped "/" in an ID shifts the decoded segments into a body predicate.
var scaleP21SW490LiteralFallbackRoutes = []string{
	"PATCH /api/initiatives/{id}/relations/{relationId} [escaped id]",
	"PATCH /api/projects/{id}/relations/{relationId} [escaped id]",
	"DELETE /api/asks/{id}",
	"DELETE /api/customer-requests/{id}",
	"DELETE /api/customer-requests/{id}/archive",
	"DELETE /api/customer-requests/{id}/attachments/{attachmentId}",
	"DELETE /api/customers/{id}",
	"DELETE /api/favorites/{type}/{id}",
	"DELETE /api/release-pipelines/{id}",
	"DELETE /api/release-pipelines/{id}/access-key",
	"DELETE /api/releases/{id}",
	"DELETE /api/releases/{id}/notes/{noteId}",
	"DELETE /api/subscriptions/{type}/{id}",
	"GET /api/customers/{id}",
	"GET /api/release-pipelines/{id}",
	"GET /api/releases/{id}",
	"GET /api/releases/{id}/history",
	"GET /api/releases/{id}/notes",
	"GET /api/reviews/{id}",
	"PATCH /api/asks/{id}",
	"PATCH /api/customer-requests/{id}",
	"PATCH /api/customers/{id}",
	"PATCH /api/favorites/{type}/{id}",
	"PATCH /api/initiatives/{id}",
	"PATCH /api/initiatives/{id}/comments/{commentId}",
	"PATCH /api/initiatives/{id}/resources/{resourceId}",
	"PATCH /api/initiatives/{id}/updates/{updateId}",
	"PATCH /api/projects/{id}",
	"PATCH /api/projects/{id}/comments/{commentId}",
	"PATCH /api/projects/{id}/milestones/{milestoneId}",
	"PATCH /api/projects/{id}/resources/{resourceId}",
	"PATCH /api/projects/{id}/updates/{updateId}",
	"PATCH /api/release-pipelines/{id}",
	"PATCH /api/releases/{id}",
	"PATCH /api/releases/{id}/notes/{noteId}",
	"PATCH /api/reviews/{id}",
	"PATCH /api/reviews/{id}/comments/{commentId}",
	"POST /api/asks",
	"POST /api/asks/{id}/decision",
	"POST /api/customer-requests",
	"POST /api/customer-requests/{id}/archive",
	"POST /api/customer-requests/{id}/attachments",
	"POST /api/customers/{id}/merge",
	"POST /api/documents",
	"POST /api/initiatives",
	"POST /api/initiatives/{id}/relations",
	"POST /api/projects",
	"POST /api/projects/{id}/relations",
	"POST /api/release-pipelines",
	"POST /api/release-pipelines/reorder",
	"POST /api/release-pipelines/{id}/access-key",
	"POST /api/releases",
	"POST /api/releases/reorder",
	"POST /api/releases/{id}/notes",
	"POST /api/reviews/{id}/coding-session",
	"POST /api/reviews/{id}/comment-attachments",
	"POST /api/reviews/{id}/comments",
	"POST /api/reviews/{id}/reactions",
	"POST /api/reviews/{id}/submit",
	"POST /api/reviews/{id}/sync",
	"PUT /api/favorites/{type}/{id}",
	"PUT /api/reviews/{id}/previews",
	"PUT /api/subscriptions/{type}/{id}",
}

func scaleP21SW490Patterns(t *testing.T) []string {
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

func TestScaleP21SW490AuthorizationFallbackClosure(t *testing.T) {
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
		function := scaleP21SW305Function(t, "auth.go", "*server", "resourceAllowedLegacy")
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
			decision     scaleP21SW490Decision
			kind, ref    string
			output       bool
		}{
			{http.MethodGet, "/api/webhooks", scaleP21SW490Membership, "", "", false},
			{http.MethodPost, "/api/labels", scaleP21SW490Membership, "", "", false},
			{http.MethodGet, "/api", scaleP21SW490Membership, "", "", false},
			{http.MethodGet, "/uploads/projects", scaleP21SW490Membership, "", "", false},
			{http.MethodPost, "/uploads/projects", scaleP21SW490Literal, "", "", false},
			{http.MethodGet, "/api/issues", scaleP21SW490Membership, "", "", false},
			{http.MethodPost, "/api/issues", scaleP21SW490Literal, "", "", false},
			{http.MethodGet, "/api/issues/a/history", scaleP21SW490Literal, "", "", false},
			{http.MethodGet, "/api/issue-records/a", scaleP21SW490Literal, "", "", false},
			{http.MethodPost, "/api/projects", scaleP21SW490Literal, "", "", false},
			{http.MethodGet, "/api/projects", scaleP21SW490Membership, "", "", false},
			{http.MethodPut, "/api/initiatives", scaleP21SW490NonGuest, "", "", false},
			{http.MethodDelete, "/api/projects/p", scaleP21SW490Point, "projects", "p", false},
			{http.MethodPost, "/api/projects/p/comments", scaleP21SW490Point, "projects", "p", false},
			{http.MethodPatch, "/api/projects/p", scaleP21SW490Literal, "", "", false},
			{http.MethodPatch, "/api/projects/p/comments/c", scaleP21SW490Literal, "", "", false},
			{http.MethodPost, "/api/projects/p/relations", scaleP21SW490Literal, "", "", false},
			{http.MethodPost, "/api/projects/p/relations/r", scaleP21SW490Literal, "", "", false},
			{http.MethodPatch, "/api/projects/p/relations/r", scaleP21SW490Point, "projects", "p", false},
			{http.MethodPatch, "/api/projects/p/relations", scaleP21SW490Literal, "", "", false},
			{http.MethodDelete, "/api/initiatives/i/relations/r", scaleP21SW490Point, "initiatives", "i", false},
			{http.MethodDelete, "/api/projects/a%2Fb", scaleP21SW490Point, "projects", "a", false},
			{http.MethodPost, "/%61pi/%70rojects/p/%63omments", scaleP21SW490Point, "projects", "p", false},
			{http.MethodGet, "/api/views", scaleP21SW490Membership, "", "", false},
			{http.MethodPost, "/api/views", scaleP21SW490Record, "savedViews", "", true},
			{http.MethodDelete, "/api/views/v", scaleP21SW490Record, "savedViews", "v", false},
			{http.MethodPost, "/api/dashboards", scaleP21SW490Record, "dashboards", "", true},
			{http.MethodGet, "/api/dashboards/d/results", scaleP21SW490Record, "dashboards", "d", false},
			{http.MethodGet, "/api/asks", scaleP21SW490Membership, "", "", false},
			{http.MethodPost, "/api/asks/a/decision", scaleP21SW490Literal, "", "", false},
			{http.MethodPut, "/api/customers", scaleP21SW490NonGuest, "", "", false},
			{http.MethodPatch, "/api/customers/c", scaleP21SW490Literal, "", "", false},
			{http.MethodGet, "/api/customer-requests", scaleP21SW490NonGuest, "", "", false},
			{http.MethodPost, "/api/customer-requests", scaleP21SW490Literal, "", "", false},
			{http.MethodGet, "/api/favorites", scaleP21SW490Membership, "", "", false},
			{http.MethodPut, "/api/favorites/project/p", scaleP21SW490Literal, "", "", false},
			{http.MethodDelete, "/api/subscriptions/issue/i", scaleP21SW490Literal, "", "", false},
			{http.MethodGet, "/api/reviews", scaleP21SW490Membership, "", "", false},
			{http.MethodPost, "/api/reviews/r/comments", scaleP21SW490Literal, "", "", false},
			{http.MethodGet, "/api/documents", scaleP21SW490Membership, "", "", false},
			{http.MethodPost, "/api/documents", scaleP21SW490Literal, "", "", false},
			{http.MethodPatch, "/api/documents/d/comments/c", scaleP21SW490Literal, "", "", false},
			{http.MethodGet, "/api/releases", scaleP21SW490Membership, "", "", false},
			{http.MethodPost, "/api/releases/reorder", scaleP21SW490Literal, "", "", false},
			{http.MethodGet, "/api/release-pipelines/p", scaleP21SW490Literal, "", "", false},
			{http.MethodGet, "/api/cycles", scaleP21SW490Membership, "", "", false},
			{http.MethodGet, "/api/cycles/c", scaleP21SW490Record, "cycles", "c", false},
			{http.MethodPatch, "/api/cycles/c/x/y", scaleP21SW490Record, "cycles", "c", false},
			{http.MethodGet, "/api/teams", scaleP21SW490Membership, "", "", false},
			{http.MethodGet, "/api/teams/t", scaleP21SW490TeamRow, "", "t", false},
			{http.MethodPatch, "/api/teams/t/settings", scaleP21SW490TeamRow, "", "t", false},
			{http.MethodGet, "/api/teams/t/resources", scaleP21SW490TeamContent, "", "t", true},
			{http.MethodHead, "/api/teams/t/resources", scaleP21SW490TeamContent, "", "t", false},
			{http.MethodPost, "/api/teams/t/resources", scaleP21SW490TeamContent, "", "t", false},
			{http.MethodGet, "/api/teams/t/resources/x", scaleP21SW490TeamContent, "", "t", false},
			{http.MethodDelete, "/api/teams/t/resource-sections/s", scaleP21SW490TeamContent, "", "t", false},
			{http.MethodGet, "/api/notifications/n", scaleP21SW490Literal, "", "", false},
			{http.MethodPatch, "/api/workspaces/w", scaleP21SW490Membership, "", "", false},
			{http.MethodPost, "/api/workspaces/w/teams", scaleP21SW490Membership, "", "", false},
			{http.MethodPatch, "/api/workspaces/w/teams/t", scaleP21SW490TeamRow, "", "t", false},
			{http.MethodPut, "/api/workspaces/w/teams/t/members/u", scaleP21SW490TeamRow, "", "t", false},
		} {
			request := httptest.NewRequest(row.method, row.path, nil)
			got := scaleP21SW490FallbackShape(request)
			want := scaleP21SW490Shape{decision: row.decision, kind: row.kind, ref: row.ref, output: row.output}
			if got != want {
				t.Fatalf("%s %s: shape %+v, want %+v", row.method, row.path, got, want)
			}
		}
		for _, body := range []struct {
			text     string
			decision scaleP21SW490Decision
		}{
			{`{"favorite":true}`, scaleP21SW490ReviewPreference},
			{`{"favorite":false}`, scaleP21SW490ReviewPreference},
			{`{"favorite":null}`, scaleP21SW490Literal},
			{`{"favorite":true,"title":"x"}`, scaleP21SW490Literal},
			{`{}`, scaleP21SW490Literal},
		} {
			request := httptest.NewRequest(http.MethodPatch, "/api/reviews/r", strings.NewReader(body.text))
			if got := scaleP21SW490FallbackShape(request); got.decision != body.decision || got.decision == scaleP21SW490ReviewPreference && got.ref != "r" {
				t.Fatal("embedded review favorite shape", body.text, got)
			}
		}
	})
	t.Run("route-table-walk", func(t *testing.T) {
		patterns := scaleP21SW490Patterns(t)
		if len(patterns) < 800 {
			t.Fatal("route table walk lost registrations", len(patterns))
		}
		for _, repo := range scaleP03ServerTestStores(t) {
			f := scaleP21M2Setup(t, repo)
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
									if _, selected := scaleP21ColdAuthorizationRoute(request); selected {
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
				if !slices.Contains(scaleP21SW490LiteralFallbackRoutes, key) {
					unexpected = append(unexpected, key)
				}
			}
			for _, key := range scaleP21SW490LiteralFallbackRoutes {
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
