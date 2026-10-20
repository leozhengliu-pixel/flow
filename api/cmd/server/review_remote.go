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
	"slices"
	"strconv"
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
	// RateLimited: the host refused the call for its rate limit; RetryAt is
	// when it says to try again (zero when it did not say).
	RateLimited bool
	RetryAt     time.Time
}

// reviewRateLimit is the last rate limit budget a token credential's
// answers reported (GitHub X-RateLimit-*, GitLab RateLimit-*).
type reviewRateLimit struct {
	mu        sync.Mutex
	known     bool
	remaining int
	reset     time.Time
}

func (r *reviewRateLimit) note(header http.Header) {
	remaining := firstNonEmpty(header.Get("X-RateLimit-Remaining"), header.Get("RateLimit-Remaining"))
	value, err := strconv.Atoi(strings.TrimSpace(remaining))
	if err != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.known, r.remaining, r.reset = true, value, reviewRateLimitReset(header)
}

func (r *reviewRateLimit) budget() (remaining int, reset time.Time, known bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.remaining, r.reset, r.known
}

// reviewRateLimitReset is when a rate limited answer says to retry:
// Retry-After seconds, else the reset epoch of the window.
func reviewRateLimitReset(header http.Header) time.Time {
	if seconds, err := strconv.Atoi(strings.TrimSpace(header.Get("Retry-After"))); err == nil && seconds >= 0 {
		return time.Now().Add(time.Duration(seconds) * time.Second).UTC()
	}
	if epoch, err := strconv.ParseInt(strings.TrimSpace(firstNonEmpty(header.Get("X-RateLimit-Reset"), header.Get("RateLimit-Reset"))), 10, 64); err == nil && epoch > 0 {
		return time.Unix(epoch, 0).UTC()
	}
	return time.Time{}
}

// reviewRemoteRateLimited reports a call refused for the host's rate limit
// and when to retry (zero when unknown).
func reviewRemoteRateLimited(err error) (bool, time.Time) {
	var remote *reviewRemoteError
	if errors.As(err, &remote) && remote.RateLimited {
		return true, remote.RetryAt
	}
	var api *githubhost.APIError
	if errors.As(err, &api) && errors.Is(err, codehost.ErrTransient) && (api.Status == http.StatusTooManyRequests || api.Status == http.StatusForbidden) {
		return true, time.Time{}
	}
	return false, time.Time{}
}

// reviewRemoteBudget is the rate limit budget a remote last saw (known is
// false for remotes that do not report it, such as the GitHub App).
func reviewRemoteBudget(remote reviewRemote) (remaining int, reset time.Time, known bool) {
	if budgeted, ok := remote.(interface {
		budget() (int, time.Time, bool)
	}); ok {
		return budgeted.budget()
	}
	return 0, time.Time{}, false
}

func (e *reviewRemoteError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("%s: HTTP %d", e.Provider, e.Status)
	}
	return fmt.Sprintf("%s: HTTP %d: %s", e.Provider, e.Status, e.Message)
}

// reviewRemoteErrorText is a code host's error message, with the details
// GitHub puts in "errors" (strings or objects with a message), such as
// "Can not approve your own pull request" behind a bare 422.
func reviewRemoteErrorText(raw []byte) string {
	var body struct {
		Message any               `json:"message"`
		Error   string            `json:"error"`
		Errors  []json.RawMessage `json:"errors"`
	}
	_ = json.Unmarshal(raw, &body)
	text := body.Error
	if body.Message != nil {
		text = fmt.Sprint(body.Message)
	}
	details := []string{}
	for _, item := range body.Errors {
		var detail string
		if json.Unmarshal(item, &detail) != nil {
			var object struct {
				Message string `json:"message"`
			}
			_ = json.Unmarshal(item, &object)
			detail = object.Message
		}
		if detail = strings.TrimSpace(detail); detail != "" && !slices.Contains(details, detail) {
			details = append(details, detail)
		}
	}
	if len(details) == 0 {
		return text
	}
	if text == "" {
		return strings.Join(details, "; ")
	}
	return text + ": " + strings.Join(details, "; ")
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
	name := reviewHostLabel(provider)
	var memberErr *reviewMemberAccessError
	if errors.As(err, &memberErr) {
		return memberErr.Error() + ". Your change is kept in Flow; retry once your account has access."
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

func reviewHostLabel(provider string) string {
	if provider == domain.CodeHostGitLab {
		return "GitLab"
	}
	return "GitHub"
}

// reviewMemberAccessError: the acting member's own account was refused
// (403) or cannot see the pull request or comment (404). Flow does not
// retry with the workspace credential, which would let the member do what
// they cannot do on the code host.
type reviewMemberAccessError struct {
	provider, login string
	err             error
}

func (e *reviewMemberAccessError) Error() string {
	account := "Your " + reviewHostLabel(e.provider) + " account"
	if e.login != "" {
		account += " (@" + e.login + ")"
	}
	if reviewRemoteStatus(e.err) == http.StatusNotFound {
		return fmt.Sprintf("%s can't see this pull request or comment (%v)", account, e.err)
	}
	return fmt.Sprintf("%s can't make this change on the pull request (%v)", account, e.err)
}

func (e *reviewMemberAccessError) Unwrap() error { return e.err }

// reviewRemotePostedAs is the identity a remote's last successful call used
// (nil when it cannot tell).
func reviewRemotePostedAs(remote reviewRemote) *domain.ReviewPostedAs {
	if identified, ok := remote.(interface{ PostedAs() *domain.ReviewPostedAs }); ok {
		return identified.PostedAs()
	}
	return nil
}

// classicReviewRemote is a token credential: the workspace's classic
// connection (OAuth / access token) or the acting member's linked account.
type classicReviewRemote struct {
	provider *reviewProvider
	repoPath string
}

func newClassicReviewRemote(provider *reviewProvider, review domain.CodeReview) *classicReviewRemote {
	repoPath := "/repos/" + url.PathEscape(review.RepositoryOwner) + "/" + url.PathEscape(review.RepositoryName)
	if provider.provider == domain.CodeHostGitLab {
		repoPath = "/projects/" + url.PathEscape(review.RepositoryOwner+"/"+review.RepositoryName)
	}
	return &classicReviewRemote{provider: provider, repoPath: repoPath}
}

func (c *classicReviewRemote) Provider() string { return c.provider.provider }

func (c *classicReviewRemote) budget() (int, time.Time, bool) { return c.provider.rate.budget() }

func (c *classicReviewRemote) PostedAs() *domain.ReviewPostedAs {
	postedAs := c.provider.postedAs
	return &postedAs
}

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
	if provider.client == nil && !integrationEndpointSafe(ctx, endpoint, provider.allowLocal, outboundTrustedPrivate) {
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
		if provider.bearer {
			req.Header.Set("Authorization", "Bearer "+provider.token)
		} else {
			req.Header.Set("PRIVATE-TOKEN", provider.token)
		}
	} else {
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("Authorization", "Bearer "+provider.token)
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	}
	client := provider.client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("repository API request failed: %w", err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 16<<20))
	if err != nil {
		return fmt.Errorf("repository API response could not be read: %w", err)
	}
	provider.rate.note(response.Header)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		remoteErr := &reviewRemoteError{Provider: provider.provider, Status: response.StatusCode, Message: reviewRemoteErrorText(raw)}
		if response.StatusCode == http.StatusTooManyRequests || (response.StatusCode == http.StatusForbidden &&
			(firstNonEmpty(response.Header.Get("X-RateLimit-Remaining"), response.Header.Get("RateLimit-Remaining")) == "0" || response.Header.Get("Retry-After") != "")) {
			remoteErr.RateLimited, remoteErr.RetryAt = true, reviewRateLimitReset(response.Header)
		}
		return remoteErr
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

func (a *appReviewRemote) PostedAs() *domain.ReviewPostedAs {
	login := "GitHub App"
	if slug := a.host.Slug(); slug != "" {
		login = slug + "[bot]"
	}
	return &domain.ReviewPostedAs{Provider: domain.CodeHostGitHub, Login: login, Via: domain.CodeHostAccountActorBot}
}

func (a *appReviewRemote) REST(ctx context.Context, method, path string, body, out any) error {
	return a.host.RepoAPI(ctx, a.workspace, a.repo, method, path, body, out)
}

func (a *appReviewRemote) GraphQL(ctx context.Context, query string, variables map[string]any, out any) error {
	return a.host.GraphQL(ctx, a.workspace, a.repo, query, variables, out)
}

// fallbackRemote tries each workspace credential in order until one is
// allowed, then keeps using it.
type fallbackRemote struct {
	mu      sync.Mutex
	remotes []reviewRemote
	chosen  int
}

func (f *fallbackRemote) Provider() string { return f.remotes[0].Provider() }

func (f *fallbackRemote) PostedAs() *domain.ReviewPostedAs {
	f.mu.Lock()
	defer f.mu.Unlock()
	return reviewRemotePostedAs(f.remotes[f.chosen])
}

func (f *fallbackRemote) budget() (int, time.Time, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return reviewRemoteBudget(f.remotes[f.chosen])
}

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

// memberReviewRemote acts as a member: their linked account first, then the
// workspace credentials only when the host rejects the member's token as
// invalid (401, which also marks the account "Reconnect"). A member refused
// (403) or not shown the object (404) gets a *reviewMemberAccessError and
// nothing is retried with the workspace credential.
type memberReviewRemote struct {
	s       *server
	member  reviewRemote
	account domain.CodeHostAccount
	// workspace is nil when the workspace has no usable credential
	// (workspaceErr says why, if a connection exists).
	workspace    reviewRemote
	workspaceErr error
	mu           sync.Mutex
	revoked      bool
	used         reviewRemote
}

func (m *memberReviewRemote) Provider() string { return m.member.Provider() }

func (m *memberReviewRemote) PostedAs() *domain.ReviewPostedAs {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.used == nil {
		return nil
	}
	return reviewRemotePostedAs(m.used)
}

func (m *memberReviewRemote) try(ctx context.Context, call func(reviewRemote) error) error {
	m.mu.Lock()
	revoked := m.revoked
	m.mu.Unlock()
	if !revoked {
		err := call(m.member)
		switch status := reviewRemoteStatus(err); {
		case err == nil:
			m.mu.Lock()
			m.used = m.member
			m.mu.Unlock()
			return nil
		case status == http.StatusUnauthorized:
			m.mu.Lock()
			first := !m.revoked
			m.revoked = true
			m.mu.Unlock()
			if first {
				m.s.markCodeHostAccountReconnect(ctx, m.account, "The code host rejected the token. Reconnect the account.")
			}
			if m.workspace == nil {
				if m.workspaceErr != nil {
					return m.workspaceErr
				}
				return fmt.Errorf("%s rejected your account's token; reconnect it in Settings: %w", reviewHostLabel(m.Provider()), err)
			}
		case status == http.StatusForbidden || status == http.StatusNotFound:
			return &reviewMemberAccessError{provider: m.Provider(), login: m.account.Login, err: err}
		default:
			return err
		}
	}
	if m.workspace == nil {
		if m.workspaceErr != nil {
			return m.workspaceErr
		}
		return fmt.Errorf("connect %s before changing this review", m.Provider())
	}
	if err := call(m.workspace); err != nil {
		return err
	}
	m.mu.Lock()
	m.used = m.workspace
	m.mu.Unlock()
	return nil
}

func (m *memberReviewRemote) REST(ctx context.Context, method, path string, body, out any) error {
	return m.try(ctx, func(remote reviewRemote) error { return remote.REST(ctx, method, path, body, out) })
}

func (m *memberReviewRemote) GraphQL(ctx context.Context, query string, variables map[string]any, out any) error {
	return m.try(ctx, func(remote reviewRemote) error { return remote.GraphQL(ctx, query, variables, out) })
}

// reviewRemoteAs is the one credential path for everything Flow writes to a
// review's code host on a member's behalf (comments, replies, inline
// comments, resolution, approvals, merge, close, reopen): the actor's
// linked account first (memberReviewRemote), else the workspace order of
// reviewRemoteFor. Without a linked account (webhooks, system actions,
// apps) it is reviewRemoteFor.
func (s *server) reviewRemoteAs(ctx context.Context, data domain.Bootstrap, review domain.CodeReview, actor domain.User, readOnly bool) (reviewRemote, error) {
	workspace, workspaceErr := s.reviewRemoteFor(ctx, data, review, readOnly)
	if (review.Provider != domain.CodeHostGitHub && review.Provider != domain.CodeHostGitLab) || review.Number <= 0 || review.RepositoryOwner == "" || review.RepositoryName == "" {
		return workspace, workspaceErr
	}
	member := s.memberReviewProvider(ctx, data, review, actor)
	if member == nil {
		return workspace, workspaceErr
	}
	return &memberReviewRemote{s: s, member: newClassicReviewRemote(member, review), account: *member.account, workspace: workspace, workspaceErr: workspaceErr}, nil
}

// reviewRemoteFor returns how to reach a review's pull/merge request with
// the workspace's credentials (the GitHub App installation, then the
// classic connection), or nil when the review has no code host counterpart
// Flow can reach (local reviews, no connection). An error means a
// connection exists but cannot be used (missing credentials), which callers
// surface.
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
			remotes = append(remotes, newClassicReviewRemote(provider, review))
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
