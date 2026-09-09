package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"flow/api/internal/domain"
)

type reviewProvider struct {
	base, token, resource, provider string
	allowLocal                      bool
}

func (s *server) reviewProvider(data domain.Bootstrap, review domain.CodeReview) (*reviewProvider, error) {
	if review.Provider != "github" && review.Provider != "gitlab" {
		return nil, nil
	}
	for _, connection := range data.IntegrationConnections {
		if connection.Provider != review.Provider || connection.Status == "disconnected" {
			continue
		}
		if org := connection.Config["organization"]; org != "" && !strings.EqualFold(org, review.RepositoryOwner) {
			continue
		}
		token := connection.OAuthAccessToken
		if token == "" {
			token = os.Getenv("FLOW_INTEGRATION_" + strings.ToUpper(review.Provider) + "_ACCESS_TOKEN")
		}
		if token == "" {
			return nil, fmt.Errorf("repository credentials are required for %s actions", review.Provider)
		}
		base, resource := "https://api.github.com", fmt.Sprintf("/repos/%s/%s/pulls/%d", url.PathEscape(review.RepositoryOwner), url.PathEscape(review.RepositoryName), review.Number)
		configuredBase := strings.TrimRight(os.Getenv("FLOW_INTEGRATION_"+strings.ToUpper(review.Provider)+"_API_URL"), "/")
		if review.Provider == "gitlab" {
			host := strings.TrimRight(os.Getenv("FLOW_INTEGRATION_GITLAB_HOST"), "/")
			if host == "" {
				host = "https://gitlab.com"
			}
			if requested := strings.TrimRight(connection.Config["host"], "/"); requested != "" && requested != host && !s.authDisabled && configuredBase == "" {
				return nil, fmt.Errorf("configure the GitLab host in the deployment before using repository actions")
			}
			base = host + "/api/v4"
			resource = fmt.Sprintf("/projects/%s/merge_requests/%d", url.PathEscape(review.RepositoryOwner+"/"+review.RepositoryName), review.Number)
		}
		if configuredBase != "" {
			base = configuredBase
		}
		if custom := connection.Config["apiUrl"]; custom != "" {
			if !s.authDisabled && strings.TrimRight(custom, "/") != base {
				return nil, fmt.Errorf("repository API URL must match the deployment configuration")
			}
			base = strings.TrimRight(custom, "/")
		}
		return &reviewProvider{base: base, token: token, resource: resource, provider: review.Provider, allowLocal: s.authDisabled}, nil
	}
	return nil, fmt.Errorf("connect %s before changing this review", review.Provider)
}

func (provider *reviewProvider) request(ctx context.Context, method, path string, payload any) (map[string]any, error) {
	endpoint := provider.base + path
	if !integrationEndpointSafe(ctx, endpoint, provider.allowLocal) {
		return nil, fmt.Errorf("repository API endpoint is not allowed")
	}
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if provider.provider == "gitlab" {
		req.Header.Set("PRIVATE-TOKEN", provider.token)
	} else {
		req.Header.Set("Authorization", "Bearer "+provider.token)
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	}
	client := http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("repository API request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("%s rejected the action (%d)", provider.provider, response.StatusCode)
	}
	result := map[string]any{}
	if response.StatusCode != 204 {
		if err = json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&result); err != nil {
			return nil, fmt.Errorf("invalid repository API response")
		}
	}
	return result, nil
}

func (s *server) syncReviewStatus(ctx context.Context, data domain.Bootstrap, review domain.CodeReview, status, method string) error {
	provider, err := s.reviewProvider(data, review)
	if err != nil || provider == nil {
		return err
	}
	if status == "merged" {
		if provider.provider == "github" {
			result, err := provider.request(ctx, http.MethodPut, provider.resource+"/merge", map[string]string{"merge_method": method})
			if err != nil {
				return err
			}
			if result["merged"] != true {
				return fmt.Errorf("pull request could not be merged")
			}
			return nil
		}
		if method == "rebase" {
			state, err := provider.request(ctx, http.MethodGet, provider.resource+"?include_rebase_in_progress=true&include_diverged_commits_count=true", nil)
			if err != nil {
				return err
			}
			if state["rebase_in_progress"] == true {
				return fmt.Errorf("repository rebase is still in progress")
			}
			if count, _ := state["diverged_commits_count"].(float64); count > 0 {
				if _, err := provider.request(ctx, http.MethodPut, provider.resource+"/rebase", map[string]bool{"skip_ci": false}); err != nil {
					return err
				}
				return fmt.Errorf("rebase started; retry merging after the repository finishes rebasing")
			}
		}
		_, err := provider.request(ctx, http.MethodPut, provider.resource+"/merge", map[string]bool{"squash": method == "squash"})
		return err
	}
	if status == "closed" || status == "open" {
		if provider.provider == "github" {
			_, err = provider.request(ctx, http.MethodPatch, provider.resource, map[string]string{"state": status})
		} else {
			event := "reopen"
			if status == "closed" {
				event = "close"
			}
			_, err = provider.request(ctx, http.MethodPut, provider.resource, map[string]string{"state_event": event})
		}
	}
	return err
}

func mergeMethod(preference string) string {
	switch preference {
	case "Merge commit", "merge":
		return "merge"
	case "Rebase and merge", "rebase":
		return "rebase"
	default:
		return "squash"
	}
}

func (s *server) markReviewReady(ctx context.Context, data domain.Bootstrap, review domain.CodeReview) error {
	if !review.Draft {
		return nil
	}
	provider, err := s.reviewProvider(data, review)
	if err != nil || provider == nil {
		return err
	}
	if provider.provider == "gitlab" {
		title := strings.TrimSpace(review.Title)
		for _, prefix := range []string{"Draft:", "WIP:", "draft:", "wip:"} {
			title = strings.TrimSpace(strings.TrimPrefix(title, prefix))
		}
		_, err := provider.request(ctx, http.MethodPut, provider.resource, map[string]string{"title": title})
		return err
	}
	pr, err := provider.request(ctx, http.MethodGet, provider.resource, nil)
	if err != nil {
		return err
	}
	id, _ := pr["node_id"].(string)
	if id == "" {
		return fmt.Errorf("GitHub pull request node ID is unavailable")
	}
	result, err := provider.request(ctx, http.MethodPost, "/graphql", map[string]any{"query": "mutation($id: ID!) { markPullRequestReadyForReview(input: {pullRequestId: $id}) { pullRequest { id isDraft } } }", "variables": map[string]string{"id": id}})
	if err != nil {
		return err
	}
	if errors, ok := result["errors"].([]any); ok && len(errors) > 0 {
		return fmt.Errorf("GitHub rejected the draft conversion")
	}
	return nil
}
