package github

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"flow/api/internal/codehost"
)

func TestRepoAPIAndGraphQL(t *testing.T) {
	fake := newFakeGitHub(t)
	host, _ := newTestHost(t, fake, nil)
	fake.setOverride("GET /repos/acme/app/pulls/7/files", func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Query().Get("per_page") != "100" || r.Header.Get("Authorization") == "" {
			t.Errorf("unexpected files request %s", r.URL)
		}
		writeJSONResponse(w, http.StatusOK, []map[string]any{{"filename": "lib/a.js", "previous_filename": "src/a.js", "status": "renamed"}})
		return true
	})
	var files []struct {
		Filename string `json:"filename"`
		Previous string `json:"previous_filename"`
	}
	if err := host.RepoAPI(t.Context(), "ws", appRepo, http.MethodGet, "/pulls/7/files?per_page=100", nil, &files); err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Previous != "src/a.js" {
		t.Fatalf("files = %#v", files)
	}

	fake.setOverride("POST /graphql", func(w http.ResponseWriter, r *http.Request) bool {
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Variables["id"] == "denied" {
			writeJSONResponse(w, http.StatusOK, map[string]any{"data": nil, "errors": []map[string]string{{"type": "FORBIDDEN", "message": "Resource not accessible by integration"}}})
			return true
		}
		writeJSONResponse(w, http.StatusOK, map[string]any{"data": map[string]any{"resolveReviewThread": map[string]any{"thread": map[string]any{"isResolved": true}}}})
		return true
	})
	var out struct {
		ResolveReviewThread struct {
			Thread struct {
				IsResolved bool `json:"isResolved"`
			} `json:"thread"`
		} `json:"resolveReviewThread"`
	}
	if err := host.GraphQL(t.Context(), "ws", appRepo, "mutation", map[string]any{"id": "PRRT_1"}, &out); err != nil || !out.ResolveReviewThread.Thread.IsResolved {
		t.Fatalf("graphql = %#v, %v", out, err)
	}
	err := host.GraphQL(t.Context(), "ws", appRepo, "mutation", map[string]any{"id": "denied"}, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusForbidden || !errors.Is(err, codehost.ErrNoAccess) {
		t.Fatalf("forbidden graphql error = %v", err)
	}
}

func TestGraphQLURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://api.github.com":         "https://api.github.com/graphql",
		"https://ghe.example.com/api/v3": "https://ghe.example.com/api/graphql",
		"http://127.0.0.1:1234/":         "http://127.0.0.1:1234/graphql",
	} {
		if got := GraphQLURL(in); got != want {
			t.Errorf("GraphQLURL(%q) = %q, want %q", in, got, want)
		}
	}
}
