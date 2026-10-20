package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// reviewProvider is one token-based credential for a review's code host:
// the acting member's linked account, or the workspace's classic
// connection (OAuth / access token). It is used through classicReviewRemote.
type reviewProvider struct {
	base, token, provider string
	allowLocal            bool
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

// reviewHostName is the host of the review's PR/MR URL ("" when unknown).
func reviewHostName(review domain.CodeReview) string {
	parsed, err := url.Parse(strings.TrimSpace(review.URL))
	if err != nil || parsed.Host == "" {
		return ""
	}
	return strings.ToLower(parsed.Host)
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
	provider := &reviewProvider{token: tokens.AccessToken, provider: review.Provider, allowLocal: s.loopbackCodeHosts(), bearer: !tokens.PrivateToken, account: &account,
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

// withReviewRemote runs a synchronous action (approve, request changes,
// merge, close, reopen, ready for review) on the code host as the actor,
// through the same credential path as comment write-back (reviewRemoteAs).
// It returns who the action was posted as, or nil when the review has no
// code host.
func (s *server) withReviewRemote(ctx context.Context, data domain.Bootstrap, review domain.CodeReview, actor domain.User, action func(reviewRemote) error) (*domain.ReviewPostedAs, error) {
	if review.Provider != "github" && review.Provider != "gitlab" {
		return nil, nil
	}
	remote, err := s.reviewRemoteAs(ctx, data, review, actor, false)
	if err != nil {
		return nil, err
	}
	if remote == nil {
		return nil, &reviewCredentialError{message: fmt.Sprintf("connect %s before changing this review", review.Provider)}
	}
	if err := action(remote); err != nil {
		return nil, err
	}
	return reviewRemotePostedAs(remote), nil
}

// reviewProviderFor resolves the classic connection's credentials for a
// review; readOnly accepts a read-only GitLab token (the review sync reads).
// A connection that cannot be used is a *reviewCredentialError.
func (s *server) reviewProviderFor(ctx context.Context, data domain.Bootstrap, review domain.CodeReview, readOnly bool) (*reviewProvider, error) {
	provider, err := s.workspaceReviewProvider(ctx, data, review, readOnly)
	if err != nil {
		return nil, &reviewCredentialError{message: err.Error()}
	}
	if provider != nil {
		provider.postedAs.Provider, provider.postedAs.Via = review.Provider, domain.CodeHostAccountActorBot
	}
	return provider, nil
}

func (s *server) workspaceReviewProvider(ctx context.Context, data domain.Bootstrap, review domain.CodeReview, readOnly bool) (*reviewProvider, error) {
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
				if connection.Config["readonly"] == "true" && !readOnly {
					return nil, fmt.Errorf("the GitLab access token is read-only (read_api scope)")
				}
				return &reviewProvider{base: credential.URL + "/api/v4", token: credential.Token, provider: "gitlab", allowLocal: s.loopbackCodeHosts(), postedAs: domain.ReviewPostedAs{Login: workspaceIntegrationLogin(connection)}}, nil
			}
		}
		if token == "" {
			token = os.Getenv("FLOW_INTEGRATION_" + strings.ToUpper(review.Provider) + "_ACCESS_TOKEN")
		}
		if token == "" {
			return nil, fmt.Errorf("repository credentials are required for %s actions", review.Provider)
		}
		base := "https://api.github.com"
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
		return &reviewProvider{base: base, token: token, provider: review.Provider, allowLocal: s.loopbackCodeHosts(), postedAs: domain.ReviewPostedAs{Login: workspaceIntegrationLogin(connection)}}, nil
	}
	return nil, fmt.Errorf("connect %s before changing this review", review.Provider)
}

func (s *server) syncReviewStatus(ctx context.Context, data domain.Bootstrap, review domain.CodeReview, status, method string) error {
	_, err := s.syncReviewStatusAs(ctx, data, review, data.Viewer, status, method)
	return err
}

// syncReviewStatusAs merges, closes or reopens the PR/MR as the actor (their
// linked account, else the workspace integration) and reports who it was
// posted as.
func (s *server) syncReviewStatusAs(ctx context.Context, data domain.Bootstrap, review domain.CodeReview, actor domain.User, status, method string) (*domain.ReviewPostedAs, error) {
	return s.withReviewRemote(ctx, data, review, actor, func(remote reviewRemote) error {
		return reviewStatusAction(ctx, remote, review, status, method)
	})
}

// reviewRequestPath is the pull/merge request below the repository.
func reviewRequestPath(provider string, review domain.CodeReview) string {
	if provider == domain.CodeHostGitLab {
		return "/merge_requests/" + strconv.Itoa(review.Number)
	}
	return "/pulls/" + strconv.Itoa(review.Number)
}

func reviewStatusAction(ctx context.Context, remote reviewRemote, review domain.CodeReview, status, method string) error {
	provider := remote.Provider()
	resource := reviewRequestPath(provider, review)
	if status == "merged" {
		if provider == domain.CodeHostGitHub {
			var result struct {
				Merged bool `json:"merged"`
			}
			if err := remote.REST(ctx, http.MethodPut, resource+"/merge", map[string]string{"merge_method": method}, &result); err != nil {
				return err
			}
			if !result.Merged {
				return fmt.Errorf("pull request could not be merged")
			}
			return nil
		}
		if method == "rebase" {
			var state struct {
				RebaseInProgress     bool `json:"rebase_in_progress"`
				DivergedCommitsCount int  `json:"diverged_commits_count"`
			}
			if err := remote.REST(ctx, http.MethodGet, resource+"?include_rebase_in_progress=true&include_diverged_commits_count=true", nil, &state); err != nil {
				return err
			}
			if state.RebaseInProgress {
				return fmt.Errorf("repository rebase is still in progress")
			}
			if state.DivergedCommitsCount > 0 {
				if err := remote.REST(ctx, http.MethodPut, resource+"/rebase", map[string]bool{"skip_ci": false}, nil); err != nil {
					return err
				}
				return fmt.Errorf("rebase started; retry merging after the repository finishes rebasing")
			}
		}
		return remote.REST(ctx, http.MethodPut, resource+"/merge", map[string]bool{"squash": method == "squash"}, nil)
	}
	if status == "closed" || status == "open" {
		if provider == domain.CodeHostGitHub {
			return remote.REST(ctx, http.MethodPatch, resource, map[string]string{"state": status}, nil)
		}
		event := "reopen"
		if status == "closed" {
			event = "close"
		}
		return remote.REST(ctx, http.MethodPut, resource, map[string]string{"state_event": event}, nil)
	}
	return nil
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
	_, err := s.withReviewRemote(ctx, data, review, data.Viewer, func(remote reviewRemote) error {
		return markReviewReadyAction(ctx, remote, review)
	})
	return err
}

func markReviewReadyAction(ctx context.Context, remote reviewRemote, review domain.CodeReview) error {
	resource := reviewRequestPath(remote.Provider(), review)
	if remote.Provider() == domain.CodeHostGitLab {
		title := strings.TrimSpace(review.Title)
		for _, prefix := range []string{"Draft:", "WIP:", "draft:", "wip:"} {
			title = strings.TrimSpace(strings.TrimPrefix(title, prefix))
		}
		return remote.REST(ctx, http.MethodPut, resource, map[string]string{"title": title}, nil)
	}
	var pull struct {
		NodeID string `json:"node_id"`
	}
	if err := remote.REST(ctx, http.MethodGet, resource, nil, &pull); err != nil {
		return err
	}
	if pull.NodeID == "" {
		return fmt.Errorf("GitHub pull request node ID is unavailable")
	}
	if err := remote.GraphQL(ctx, "mutation($id: ID!) { markPullRequestReadyForReview(input: {pullRequestId: $id}) { pullRequest { id isDraft } } }", map[string]any{"id": pull.NodeID}, nil); err != nil {
		return fmt.Errorf("GitHub rejected the draft conversion: %w", err)
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

// postReviewActivity posts a Flow review decision to the PR/MR as the actor
// (their linked account, else the workspace integration) and reports who it
// was posted as. A review without a code host, or a workspace without a
// usable credential, keeps the activity in Flow only (nil, nil).
func (s *server) postReviewActivity(ctx context.Context, data domain.Bootstrap, review domain.CodeReview, actor domain.User, kind, body string) (*domain.ReviewPostedAs, error) {
	postedAs, _, err := s.postReviewDecision(ctx, data, review, actor, kind, body)
	return postedAs, err
}

// postReviewDecision is postReviewActivity that also returns the code host
// object it created (the review or note), so syncs update the Flow event
// instead of duplicating it.
func (s *server) postReviewDecision(ctx context.Context, data domain.Bootstrap, review domain.CodeReview, actor domain.User, kind, body string) (*domain.ReviewPostedAs, *domain.ReviewEventRemote, error) {
	if (review.Provider != "github" && review.Provider != "gitlab") || review.Number <= 0 || review.RepositoryOwner == "" || review.RepositoryName == "" {
		return nil, nil, nil
	}
	body = strings.TrimSpace(body)
	if kind == "comment" && body == "" {
		return nil, nil, nil
	}
	var ref *domain.ReviewEventRemote
	postedAs, err := s.withReviewRemote(ctx, data, review, actor, func(remote reviewRemote) error {
		var err error
		ref, err = reviewActivityAction(ctx, remote, review, kind, body)
		return err
	})
	var credentialErr *reviewCredentialError
	if errors.As(err, &credentialErr) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	return postedAs, ref, nil
}

// reviewActivityAction posts a review decision: kind is approve,
// requestChanges or comment. It returns the created review or comment.
func reviewActivityAction(ctx context.Context, remote reviewRemote, review domain.CodeReview, kind, body string) (*domain.ReviewEventRemote, error) {
	resource := reviewRequestPath(remote.Provider(), review)
	if remote.Provider() == domain.CodeHostGitLab {
		switch kind {
		case "approve":
			if err := remote.REST(ctx, http.MethodPost, resource+"/approve", map[string]string{}, nil); err != nil {
				return nil, err
			}
			if body == "" {
				return nil, nil
			}
		case "requestChanges":
			if body == "" {
				body = "Changes requested."
			}
		}
		var note gitlabAPINote
		if err := remote.REST(ctx, http.MethodPost, resource+"/notes", map[string]string{"body": body}, &note); err != nil {
			return nil, err
		}
		return reviewCreatedRef(domain.CodeHostGitLab, reviewRemoteNote, note.ID, ""), nil
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
		var created githubAPIReview
		if err := remote.REST(ctx, http.MethodPost, resource+"/reviews", payload, &created); err != nil {
			return nil, err
		}
		return reviewCreatedRef(domain.CodeHostGitHub, reviewRemoteReview, created.ID, created.HTMLURL), nil
	}
	var comment githubAPIIssueComment
	if err := remote.REST(ctx, http.MethodPost, "/issues/"+strconv.Itoa(review.Number)+"/comments", map[string]string{"body": body}, &comment); err != nil {
		return nil, err
	}
	return reviewCreatedRef(domain.CodeHostGitHub, reviewRemoteIssueComment, comment.ID, comment.HTMLURL), nil
}

func reviewCreatedRef(provider, kind string, id int64, link string) *domain.ReviewEventRemote {
	if id == 0 {
		return nil
	}
	return &domain.ReviewEventRemote{Provider: provider, Kind: kind, ID: strconv.FormatInt(id, 10), URL: link}
}
