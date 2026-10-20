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
	"strings"
	"sync"
	"time"

	"flow/api/internal/codehost"
	githubhost "flow/api/internal/codehost/github"
	"flow/api/internal/domain"
)

// reviewRemote is one way to reach a review's repository on its code host:
// the GitHub App (installation token) or a classic GitHub/GitLab connection.
// REST paths are relative to the repository: /repos/{owner}/{name} on
// GitHub, /projects/{path} on GitLab.
type reviewRemote interface {
	Provider() string
	REST(ctx context.Context, method, path string, body, out any) error
	GraphQL(ctx context.Context, query string, variables map[string]any, out any) error
}

// reviewRemoteError is a non-2xx answer of a classic connection.
type reviewRemoteError struct {
	Provider string
	Status   int
	Message  string
}

func (e *reviewRemoteError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("%s: HTTP %d", e.Provider, e.Status)
	}
	return fmt.Sprintf("%s: HTTP %d: %s", e.Provider, e.Status, e.Message)
}

// reviewRemoteStatus is the HTTP status behind a remote error (0 when the
// request never got an answer).
func reviewRemoteStatus(err error) int {
	var remote *reviewRemoteError
	if errors.As(err, &remote) {
		return remote.Status
	}
	var api *githubhost.APIError
	if errors.As(err, &api) {
		return api.Status
	}
	switch {
	case errors.Is(err, codehost.ErrNoAccess):
		return http.StatusForbidden
	case errors.Is(err, codehost.ErrNotFound):
		return http.StatusNotFound
	}
	return 0
}

// reviewRemoteDenied: the host refused (no installation, no permission, not
// visible), so nothing was written and another credential may be tried.
func reviewRemoteDenied(err error) bool {
	switch reviewRemoteStatus(err) {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
		return true
	}
	return errors.Is(err, codehost.ErrNotConnected)
}

// reviewSyncErrorMessage is the error shown next to a change that did not
// reach the code host.
func reviewSyncErrorMessage(provider string, err error) string {
	name := "GitHub"
	if provider == "gitlab" {
		name = "GitLab"
	}
	switch reviewRemoteStatus(err) {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Sprintf("%s denied the change: Flow's access cannot write to this pull request (%v)", name, err)
	case http.StatusNotFound:
		return fmt.Sprintf("%s could not find this pull request or comment, or Flow cannot see it (%v)", name, err)
	case http.StatusUnprocessableEntity, http.StatusBadRequest:
		return fmt.Sprintf("%s rejected the change (%v)", name, err)
	}
	return fmt.Sprintf("Could not reach %s: %v", name, err)
}

// classicReviewRemote is a classic connection (OAuth / access token).
type classicReviewRemote struct {
	provider *reviewProvider
	repoPath string
}

func (c *classicReviewRemote) Provider() string { return c.provider.provider }

func (c *classicReviewRemote) REST(ctx context.Context, method, path string, body, out any) error {
	return c.provider.call(ctx, method, c.provider.base+c.repoPath+path, body, out)
}

func (c *classicReviewRemote) GraphQL(ctx context.Context, query string, variables map[string]any, out any) error {
	if c.provider.provider != "github" {
		return errors.New("GraphQL is only used for GitHub")
	}
	return c.provider.call(ctx, http.MethodPost, githubhost.GraphQLURL(c.provider.base), map[string]any{"query": query, "variables": variables}, out)
}

// call performs one request against an absolute endpoint and decodes a JSON
// answer into out. GraphQL envelopes (/graphql endpoints) are unwrapped:
// out receives data, and errors become a reviewRemoteError.
func (provider *reviewProvider) call(ctx context.Context, method, endpoint string, payload, out any) error {
	if !integrationEndpointSafe(ctx, endpoint, provider.allowLocal) {
		return fmt.Errorf("repository API endpoint is not allowed")
	}
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "flow-review-sync")
	if provider.provider == "gitlab" {
		req.Header.Set("Accept", "application/json")
		req.Header.Set("PRIVATE-TOKEN", provider.token)
	} else {
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("Authorization", "Bearer "+provider.token)
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	}
	client := http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("repository API request failed: %w", err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 16<<20))
	if err != nil {
		return fmt.Errorf("repository API response could not be read: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var message struct {
			Message any    `json:"message"`
			Error   string `json:"error"`
		}
		_ = json.Unmarshal(raw, &message)
		text := message.Error
		if message.Message != nil {
			text = fmt.Sprint(message.Message)
		}
		return &reviewRemoteError{Provider: provider.provider, Status: response.StatusCode, Message: text}
	}
	if strings.HasSuffix(endpoint, "/graphql") {
		var envelope struct {
			Data   json.RawMessage `json:"data"`
			Errors []struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"errors"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return fmt.Errorf("invalid GraphQL response")
		}
		if len(envelope.Errors) > 0 {
			status, messages := http.StatusUnprocessableEntity, []string{}
			for _, item := range envelope.Errors {
				messages = append(messages, item.Message)
				switch item.Type {
				case "FORBIDDEN":
					status = http.StatusForbidden
				case "NOT_FOUND":
					status = http.StatusNotFound
				}
			}
			return &reviewRemoteError{Provider: provider.provider, Status: status, Message: strings.Join(messages, "; ")}
		}
		raw = envelope.Data
	}
	if out == nil || len(raw) == 0 || response.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("invalid repository API response: %w", err)
	}
	return nil
}

// appReviewRemote is the GitHub App installation of the workspace.
type appReviewRemote struct {
	host      *githubhost.Host
	workspace string
	repo      codehost.RepoRef
}

func (a *appReviewRemote) Provider() string { return domain.CodeHostGitHub }

func (a *appReviewRemote) REST(ctx context.Context, method, path string, body, out any) error {
	return a.host.RepoAPI(ctx, a.workspace, a.repo, method, path, body, out)
}

func (a *appReviewRemote) GraphQL(ctx context.Context, query string, variables map[string]any, out any) error {
	return a.host.GraphQL(ctx, a.workspace, a.repo, query, variables, out)
}

// fallbackRemote tries each credential in order until one is allowed, then
// keeps using it.
type fallbackRemote struct {
	mu      sync.Mutex
	remotes []reviewRemote
	chosen  int
}

func (f *fallbackRemote) Provider() string { return f.remotes[0].Provider() }

func (f *fallbackRemote) try(call func(reviewRemote) error) error {
	f.mu.Lock()
	start := f.chosen
	f.mu.Unlock()
	var err error
	for index := start; index < len(f.remotes); index++ {
		err = call(f.remotes[index])
		if err == nil || !reviewRemoteDenied(err) || index == len(f.remotes)-1 {
			if err == nil {
				f.mu.Lock()
				f.chosen = index
				f.mu.Unlock()
			}
			return err
		}
	}
	return err
}

func (f *fallbackRemote) REST(ctx context.Context, method, path string, body, out any) error {
	return f.try(func(remote reviewRemote) error { return remote.REST(ctx, method, path, body, out) })
}

func (f *fallbackRemote) GraphQL(ctx context.Context, query string, variables map[string]any, out any) error {
	return f.try(func(remote reviewRemote) error { return remote.GraphQL(ctx, query, variables, out) })
}

// reviewRemoteFor returns how to reach a review's pull/merge request, or nil
// when the review has no code host counterpart Flow can reach (local
// reviews, no connection). An error means a connection exists but cannot be
// used (missing credentials), which callers surface.
func (s *server) reviewRemoteFor(ctx context.Context, data domain.Bootstrap, review domain.CodeReview, readOnly bool) (reviewRemote, error) {
	if (review.Provider != domain.CodeHostGitHub && review.Provider != domain.CodeHostGitLab) || review.Number <= 0 || review.RepositoryOwner == "" || review.RepositoryName == "" {
		return nil, nil
	}
	remotes := []reviewRemote{}
	if review.Provider == domain.CodeHostGitHub {
		if host := s.gitHubAppHost(); host != nil && host.Configured() && s.store != nil {
			if installations, err := s.store.CodeHostInstallations(ctx, data.Workspace.URLKey, domain.CodeHostGitHub); err == nil && len(installations) > 0 {
				remotes = append(remotes, &appReviewRemote{host: host, workspace: data.Workspace.URLKey, repo: codehost.RepoRef{CodeHost: domain.CodeHostGitHub, HostName: host.HostName(), FullName: review.RepositoryOwner + "/" + review.RepositoryName}})
			}
		}
	}
	connected := false
	for _, connection := range data.IntegrationConnections {
		if connection.Provider == review.Provider && connection.Status != "disconnected" {
			if org := connection.Config["organization"]; org == "" || strings.EqualFold(org, review.RepositoryOwner) {
				connected = true
			}
		}
	}
	if connected {
		provider, err := s.reviewProviderFor(ctx, data, review, readOnly)
		if err != nil && len(remotes) == 0 {
			return nil, err
		}
		if err == nil && provider != nil {
			repoPath := "/repos/" + url.PathEscape(review.RepositoryOwner) + "/" + url.PathEscape(review.RepositoryName)
			if provider.provider == domain.CodeHostGitLab {
				repoPath = "/projects/" + url.PathEscape(review.RepositoryOwner+"/"+review.RepositoryName)
			}
			remotes = append(remotes, &classicReviewRemote{provider: provider, repoPath: repoPath})
		}
	}
	switch len(remotes) {
	case 0:
		return nil, nil
	case 1:
		return remotes[0], nil
	}
	return &fallbackRemote{remotes: remotes}, nil
}
