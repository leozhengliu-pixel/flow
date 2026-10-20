package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	// bearer sends a GitLab token as "Authorization: Bearer" (OAuth); GitLab
	// access tokens use PRIVATE-TOKEN. GitHub tokens are always Bearer.
	bearer bool
	// client, when set, is the transport that already enforces which hosts
	// may be reached (member tokens on admin-configured instances).
	client *http.Client
	// postedAs is the identity actions are posted with; account is the
	// acting member's own account when the member's token is used.
	postedAs domain.ReviewPostedAs
	account  *domain.CodeHostAccount
}

// reviewProviderError is a non-2xx answer of the code host to an action.
type reviewProviderError struct {
	provider string
	status   int
}

func (e *reviewProviderError) Error() string {
	return fmt.Sprintf("%s rejected the action (%d)", e.provider, e.status)
}

func reviewProviderStatus(err error) int {
	var providerErr *reviewProviderError
	if errors.As(err, &providerErr) {
		return providerErr.status
	}
	return 0
}

// reviewHostName is the host of the review's PR/MR URL ("" when unknown).
func reviewHostName(review domain.CodeReview) string {
	parsed, err := url.Parse(strings.TrimSpace(review.URL))
	if err != nil || parsed.Host == "" {
		return ""
	}
	return strings.ToLower(parsed.Host)
}

func reviewResource(review domain.CodeReview) string {
	if review.Provider == "gitlab" {
		return fmt.Sprintf("/projects/%s/merge_requests/%d", url.PathEscape(review.RepositoryOwner+"/"+review.RepositoryName), review.Number)
	}
	return fmt.Sprintf("/repos/%s/%s/pulls/%d", url.PathEscape(review.RepositoryOwner), url.PathEscape(review.RepositoryName), review.Number)
}

// memberReviewProvider acts as the member with their own linked account on
// the review's host, or returns nil when they have none. GitLab accounts are
// only used on an instance the workspace connected.
func (s *server) memberReviewProvider(ctx context.Context, data domain.Bootstrap, review domain.CodeReview, actor domain.User) *reviewProvider {
	if s.store == nil || actor.ID == "" || actor.App || (review.Provider != "github" && review.Provider != "gitlab") {
		return nil
	}
	account, instance, ok := s.workspaceMemberAccount(ctx, data.Workspace.URLKey, actor.ID, review.Provider, reviewHostName(review))
	if !ok || account.UserID != actor.ID {
		return nil
	}
	tokens, err := s.memberHostTokens(ctx, account)
	if err != nil {
		return nil
	}
	provider := &reviewProvider{token: tokens.AccessToken, resource: reviewResource(review), provider: review.Provider, allowLocal: s.authDisabled, bearer: !tokens.PrivateToken, account: &account,
		postedAs: domain.ReviewPostedAs{Provider: review.Provider, Login: account.Login, Via: domain.CodeHostAccountActorMember}}
	if review.Provider == "gitlab" {
		client, err := s.gitlabClient(ctx, instance.BaseURL)
		if err != nil {
			return nil
		}
		client.Timeout = 15 * time.Second
		provider.base, provider.client = instance.BaseURL+"/api/v4", client
		return provider
	}
	provider.base = "https://api.github.com"
	if config, ok := s.githubPersonalOAuth(); ok && strings.EqualFold(config.HostName, account.HostName) {
		provider.base = config.APIURL
	} else if account.HostName != "github.com" {
		provider.base = "https://" + account.HostName + "/api/v3"
	}
	client, err := s.outboundClient(ctx, provider.base)
	if err != nil {
		return nil
	}
	provider.client = client
	return provider
}

// withReviewProvider runs an action on the code host as the actor: with
// their linked account when they have one, otherwise with the workspace
// integration. A member token the host rejects as invalid (401) marks the
// account "reconnect" and the action falls back to the workspace; a member
// who lacks permission (403) is refused rather than escalated. It returns
// who the action was posted as, or nil when the review has no code host.
func (s *server) withReviewProvider(ctx context.Context, data domain.Bootstrap, review domain.CodeReview, actor domain.User, action func(*reviewProvider) error) (*domain.ReviewPostedAs, error) {
	if member := s.memberReviewProvider(ctx, data, review, actor); member != nil {
		err := action(member)
		if err == nil {
			postedAs := member.postedAs
			return &postedAs, nil
		}
		if reviewProviderStatus(err) != http.StatusUnauthorized {
			return nil, err
		}
		s.markCodeHostAccountReconnect(ctx, *member.account, "The code host rejected the token. Reconnect the account.")
	}
	provider, err := s.reviewProvider(ctx, data, review)
	if err != nil || provider == nil {
		return nil, err
	}
	if err := action(provider); err != nil {
		return nil, err
	}
	postedAs := provider.postedAs
	return &postedAs, nil
}

func (s *server) reviewProvider(ctx context.Context, data domain.Bootstrap, review domain.CodeReview) (*reviewProvider, error) {
	provider, err := s.workspaceReviewProvider(ctx, data, review)
	if err != nil {
		return nil, &reviewCredentialError{message: err.Error()}
	}
	if provider != nil {
		provider.postedAs.Provider, provider.postedAs.Via = review.Provider, domain.CodeHostAccountActorBot
	}
	return provider, nil
}

func (s *server) workspaceReviewProvider(ctx context.Context, data domain.Bootstrap, review domain.CodeReview) (*reviewProvider, error) {
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
		if token == "" && review.Provider == "gitlab" {
			// Token connections: the validated access token and its GitLab URL
			// live in encrypted connector storage.
			if credential, err := s.gitlabCredential(ctx, data.Workspace.URLKey, connection.ID); err == nil {
				if connection.Config["readonly"] == "true" {
					return nil, fmt.Errorf("the GitLab access token is read-only (read_api scope)")
				}
				return &reviewProvider{base: credential.URL + "/api/v4", token: credential.Token, resource: fmt.Sprintf("/projects/%s/merge_requests/%d", url.PathEscape(review.RepositoryOwner+"/"+review.RepositoryName), review.Number), provider: "gitlab", allowLocal: s.authDisabled, postedAs: domain.ReviewPostedAs{Login: workspaceIntegrationLogin(connection)}}, nil
			}
		}
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
		return &reviewProvider{base: base, token: token, resource: resource, provider: review.Provider, allowLocal: s.authDisabled, postedAs: domain.ReviewPostedAs{Login: workspaceIntegrationLogin(connection)}}, nil
	}
	return nil, fmt.Errorf("connect %s before changing this review", review.Provider)
}

func (provider *reviewProvider) request(ctx context.Context, method, path string, payload any) (map[string]any, error) {
	endpoint := provider.base + path
	if provider.client == nil && !integrationEndpointSafe(ctx, endpoint, provider.allowLocal) {
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
	if provider.provider == "gitlab" && !provider.bearer {
		req.Header.Set("PRIVATE-TOKEN", provider.token)
	} else {
		req.Header.Set("Authorization", "Bearer "+provider.token)
	}
	if provider.provider != "gitlab" {
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	}
	client := provider.client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("repository API request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, &reviewProviderError{provider: provider.provider, status: response.StatusCode}
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
	_, err := s.syncReviewStatusAs(ctx, data, review, data.Viewer, status, method)
	return err
}

// syncReviewStatusAs merges, closes or reopens the PR/MR as the actor (their
// linked account, else the workspace integration) and reports who it was
// posted as.
func (s *server) syncReviewStatusAs(ctx context.Context, data domain.Bootstrap, review domain.CodeReview, actor domain.User, status, method string) (*domain.ReviewPostedAs, error) {
	return s.withReviewProvider(ctx, data, review, actor, func(provider *reviewProvider) error {
		return reviewStatusAction(ctx, provider, status, method)
	})
}

func reviewStatusAction(ctx context.Context, provider *reviewProvider, status, method string) error {
	var err error
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
	_, err := s.withReviewProvider(ctx, data, review, data.Viewer, func(provider *reviewProvider) error {
		return markReviewReadyAction(ctx, provider, review)
	})
	return err
}

func markReviewReadyAction(ctx context.Context, provider *reviewProvider, review domain.CodeReview) error {
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

// workspaceIntegrationLogin is the host login a workspace connection acts
// as, for "posted as" labels.
func workspaceIntegrationLogin(connection domain.IntegrationConnection) string {
	return firstNonEmpty(connection.Config["username"], connection.Config["login"], connection.Config["organization"], connection.Name)
}

// reviewCredentialError: the workspace has no usable code host credential
// for the review (no connection, no token, read-only token).
type reviewCredentialError struct{ message string }

func (e *reviewCredentialError) Error() string { return e.message }

// postReviewActivity posts a Flow comment or review decision to the PR/MR
// as the actor (their linked account, else the workspace integration) and
// reports who it was posted as. A review without a code host, or a workspace
// without a usable credential, keeps the activity in Flow only (nil, nil).
func (s *server) postReviewActivity(ctx context.Context, data domain.Bootstrap, review domain.CodeReview, actor domain.User, kind, body, path string, line int) (*domain.ReviewPostedAs, error) {
	if (review.Provider != "github" && review.Provider != "gitlab") || review.Number <= 0 || review.RepositoryOwner == "" || review.RepositoryName == "" {
		return nil, nil
	}
	body = strings.TrimSpace(body)
	if kind == "comment" && body == "" {
		return nil, nil
	}
	postedAs, err := s.withReviewProvider(ctx, data, review, actor, func(provider *reviewProvider) error {
		return reviewActivityAction(ctx, provider, kind, body, path, line)
	})
	var credentialErr *reviewCredentialError
	if errors.As(err, &credentialErr) {
		return nil, nil
	}
	return postedAs, err
}

// reviewActivityAction: kind is comment, inlineComment, approve or
// requestChanges.
func reviewActivityAction(ctx context.Context, provider *reviewProvider, kind, body, path string, line int) error {
	if provider.provider == "gitlab" {
		switch kind {
		case "approve":
			if _, err := provider.request(ctx, http.MethodPost, provider.resource+"/approve", map[string]string{}); err != nil {
				return err
			}
			if body == "" {
				return nil
			}
		case "requestChanges":
			if body == "" {
				body = "Changes requested."
			}
		case "inlineComment":
			mr, err := provider.request(ctx, http.MethodGet, provider.resource, nil)
			if err != nil {
				return err
			}
			refs, _ := mr["diff_refs"].(map[string]any)
			if refs != nil {
				position := map[string]any{"position_type": "text", "base_sha": refs["base_sha"], "head_sha": refs["head_sha"], "start_sha": refs["start_sha"], "new_path": path, "old_path": path, "new_line": line}
				_, err = provider.request(ctx, http.MethodPost, provider.resource+"/discussions", map[string]any{"body": body, "position": position})
				if err == nil || (reviewProviderStatus(err) != http.StatusBadRequest && reviewProviderStatus(err) != http.StatusUnprocessableEntity) {
					return err
				}
			}
			body = fmt.Sprintf("`%s:%d`\n\n%s", path, line, body)
		}
		_, err := provider.request(ctx, http.MethodPost, provider.resource+"/notes", map[string]string{"body": body})
		return err
	}
	switch kind {
	case "approve", "requestChanges":
		event := "APPROVE"
		if kind == "requestChanges" {
			event = "REQUEST_CHANGES"
			if body == "" {
				body = "Changes requested."
			}
		}
		payload := map[string]string{"event": event}
		if body != "" {
			payload["body"] = body
		}
		_, err := provider.request(ctx, http.MethodPost, provider.resource+"/reviews", payload)
		return err
	case "inlineComment":
		pr, err := provider.request(ctx, http.MethodGet, provider.resource, nil)
		if err != nil {
			return err
		}
		head, _ := pr["head"].(map[string]any)
		if sha, _ := head["sha"].(string); sha != "" {
			_, err = provider.request(ctx, http.MethodPost, provider.resource+"/comments", map[string]any{"body": body, "commit_id": sha, "path": path, "line": line, "side": "RIGHT"})
			if err == nil || reviewProviderStatus(err) != http.StatusUnprocessableEntity {
				return err
			}
		}
		body = fmt.Sprintf("`%s:%d`\n\n%s", path, line, body)
	}
	_, err := provider.request(ctx, http.MethodPost, strings.Replace(provider.resource, "/pulls/", "/issues/", 1)+"/comments", map[string]string{"body": body})
	return err
}
