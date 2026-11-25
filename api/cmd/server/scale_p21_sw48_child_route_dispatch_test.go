package main

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type sw48UnreadBody struct{}

func (*sw48UnreadBody) Read([]byte) (int, error) { panic("off helper read the request body") }
func (*sw48UnreadBody) Close() error             { panic("off helper closed the request body") }

func TestScaleP21SW48ChildRouteDispatch(t *testing.T) {
	expected := map[string]bool{
		"POST /api/issue-records/{id}/reactions":                           true,
		"POST /api/issue-records/{id}/comments":                            true,
		"PUT /api/issue-records/{id}/comments/{commentId}/subscription":    true,
		"DELETE /api/issue-records/{id}/comments/{commentId}/subscription": true,
		"PATCH /api/issue-records/{id}/comments/{commentId}":               true,
		"DELETE /api/issue-records/{id}/comments/{commentId}":              true,
		"POST /api/issue-records/{id}/comments/{commentId}/reactions":      true,
		"POST /api/issue-records/{id}/relations":                           true,
		"DELETE /api/issue-records/{id}/relations/{relationId}":            true,
		"POST /api/issue-records/{id}/attachments":                         true,
		"POST /api/issue-records/{id}/attachments/from-url":                true,
		"POST /api/issue-records/{id}/links":                               true,
		"DELETE /api/issue-records/{id}/attachments/{attachmentId}":        true,
	}
	expand := strings.NewReplacer(
		"{id}", "issue_native", "{commentId}", "comment_native",
		"{relationId}", "relation_native", "{attachmentId}", "attachment_native",
		"{permissionId}", "permission_native", "{suggestionId}", "suggestion_native",
	)
	mutations := http.NewServeMux()
	for pattern := range expected {
		mutations.HandleFunc(pattern, func(http.ResponseWriter, *http.Request) {})
	}
	check := func(pattern string, want bool) {
		t.Helper()
		method, path, ok := strings.Cut(pattern, " ")
		if !ok {
			t.Fatal("invalid test route", pattern)
		}
		request := httptest.NewRequest(method, expand.Replace(path), nil)
		if got := boundedIssueChildWriteRequest(request); got != want {
			t.Errorf("route %q classified %v, want %v", pattern, got, want)
		}
	}
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	routes := 0
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		function, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || function.Sel.Name != "HandleFunc" {
			return true
		}
		literal, ok := call.Args[0].(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		pattern, err := strconv.Unquote(literal.Value)
		if err != nil {
			t.Fatal(err)
		}
		_, path, ok := strings.Cut(pattern, " ")
		if !ok || path != "/api/issue-records" && !strings.HasPrefix(path, "/api/issue-records/") {
			return true
		}
		routes++
		check(pattern, expected[pattern])
		if expected[pattern] {
			found[pattern] = true
		}
		return true
	})
	for pattern := range expected {
		if !found[pattern] {
			t.Errorf("reviewed child route missing from main.go: %s", pattern)
		}
		_, path, _ := strings.Cut(pattern, " ")
		for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions} {
			other := method + " " + path
			if !expected[other] {
				_, matched := mutations.Handler(httptest.NewRequest(method, expand.Replace(path), nil))
				check(other, matched != "")
			}
		}
	}
	for _, pattern := range []string{
		"POST /api/issue-records",
		"PATCH /api/issue-records/{id}",
		"POST /api/issue-records/batch",
		"GET /api/issue-records/bootstrap",
		"POST /api/issue-records/bootstrap",
		"POST /api/issue-records/{id}/unknown",
		"POST /api/issue-records/{id}/attachments/unknown",
		"POST /api/issue-records/{id}/comments/{commentId}/unknown",
		"POST /api/issue-records/{id}/comments/{commentId}/subscription",
		"DELETE /api/issue-records/{id}/relations/{relationId}/unknown",
		"POST /api/issue-records/{id}/links/unknown",
		"POST /api/issues/{id}/comments",
		"POST /api/issue-records//comments",
		"POST /api/issue-records/{id}/comments/",
	} {
		check(pattern, false)
	}
	service := &server{store: &store.SQLiteStore{}}
	request := httptest.NewRequest(http.MethodPost, "/api/issue-records/issue_native/comments", nil)
	body := &sw48UnreadBody{}
	request.Body = body
	request.Header.Set("Content-Type", "application/json")
	canceled, cancel := context.WithCancel(request.Context())
	cancel()
	request = request.WithContext(canceled)
	data, handled, err := service.boundedIssueWriteAuthorizationMetadata(request)
	if handled || err != nil || !reflect.DeepEqual(data, domain.Bootstrap{}) ||
		request.Context() != canceled || request.Body != body {
		t.Fatal("off helper performed a read or altered the canceled request", handled, err)
	}
	t.Logf("checked %d registered issue-record routes; exact bounded child routes=%d", routes, len(found))
}
