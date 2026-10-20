package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"flow/api/internal/codehost"
)

// RepoAPI calls a REST endpoint of one repository with the App's
// installation token (contents read, pull_requests write): path is relative
// to /repos/{owner}/{name}, e.g. "/pulls/7/files?per_page=100". The CodeReview
// sync reads pull request files, comments and reviews through it and writes
// review comments back.
func (h *Host) RepoAPI(ctx context.Context, workspaceKey string, repo codehost.RepoRef, method, path string, body, out any) error {
	token, owner, name, err := h.repoToken(ctx, workspaceKey, repo, permissionsFlow)
	if err != nil {
		return err
	}
	return h.tokenRequest(ctx, "review_sync", method, repoPath(owner, name)+path, token, body, out)
}

// GraphQL runs a GraphQL query or mutation with the repository's
// installation token (review threads and their resolution are GraphQL only).
// GraphQL errors in a 200 response are returned as an *APIError.
func (h *Host) GraphQL(ctx context.Context, workspaceKey string, repo codehost.RepoRef, query string, variables map[string]any, out any) error {
	token, _, _, err := h.repoToken(ctx, workspaceKey, repo, permissionsFlow)
	if err != nil {
		return err
	}
	var response struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := h.do(ctx, "review_graphql", http.MethodPost, GraphQLURL(h.apiURL), "token "+token, map[string]any{"query": query, "variables": variables}, &response); err != nil {
		return err
	}
	if len(response.Errors) > 0 {
		messages := make([]string, 0, len(response.Errors))
		class := error(nil)
		for _, item := range response.Errors {
			messages = append(messages, item.Message)
			switch item.Type {
			case "FORBIDDEN":
				class = codehost.ErrNoAccess
			case "NOT_FOUND":
				class = codehost.ErrNotFound
			}
		}
		status := http.StatusUnprocessableEntity
		if errors.Is(class, codehost.ErrNoAccess) {
			status = http.StatusForbidden
		} else if errors.Is(class, codehost.ErrNotFound) {
			status = http.StatusNotFound
		}
		return &APIError{Status: status, Message: strings.Join(messages, "; "), Class: class}
	}
	if out == nil || len(response.Data) == 0 {
		return nil
	}
	if err := json.Unmarshal(response.Data, out); err != nil {
		return fmt.Errorf("github: decode graphql: %w", err)
	}
	return nil
}

// GraphQLURL is the GraphQL endpoint for a REST base: api.github.com/graphql,
// or https://<ghe>/api/graphql for GitHub Enterprise Server (/api/v3).
func GraphQLURL(apiURL string) string {
	apiURL = strings.TrimRight(apiURL, "/")
	if base, ok := strings.CutSuffix(apiURL, "/api/v3"); ok {
		return base + "/api/graphql"
	}
	return apiURL + "/graphql"
}
