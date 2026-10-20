package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"flow/api/internal/codehost"
	"flow/api/internal/domain"
)

type apiPullRequest struct {
	ID       int64   `json:"id"`
	NodeID   string  `json:"node_id"`
	Number   int     `json:"number"`
	HTMLURL  string  `json:"html_url"`
	Title    string  `json:"title"`
	Body     *string `json:"body"`
	State    string  `json:"state"`
	Draft    bool    `json:"draft"`
	Merged   bool    `json:"merged"`
	MergedAt *string `json:"merged_at"`
	User     struct {
		Login string `json:"login"`
	} `json:"user"`
	Head struct {
		Ref  string `json:"ref"`
		SHA  string `json:"sha"`
		Repo *struct {
			FullName string `json:"full_name"`
		} `json:"repo"`
	} `json:"head"`
	Base struct {
		Ref  string `json:"ref"`
		Repo struct {
			FullName string `json:"full_name"`
		} `json:"repo"`
	} `json:"base"`
}

func (h *Host) changeRequest(repo codehost.RepoRef, item apiPullRequest) codehost.ChangeRequest {
	state := item.State
	if item.Merged || (item.MergedAt != nil && *item.MergedAt != "") {
		state = "merged"
	}
	if item.Base.Repo.FullName != "" {
		repo.FullName = item.Base.Repo.FullName
	}
	repo.CodeHost, repo.HostName = domain.CodeHostGitHub, h.cfg.Host
	body := ""
	if item.Body != nil {
		body = *item.Body
	}
	return codehost.ChangeRequest{
		Repo: repo, Number: item.Number, ExternalID: strconv.FormatInt(item.ID, 10), URL: item.HTMLURL, Title: item.Title,
		State: state, Draft: item.Draft, HeadBranch: item.Head.Ref, BaseBranch: item.Base.Ref, HeadSHA: item.Head.SHA,
		AuthorLogin: item.User.Login, Body: body,
	}
}

func repoPath(owner, name string) string {
	return "/repos/" + pathEscape(owner) + "/" + pathEscape(name)
}

// BranchHead returns the SHA of refs/heads/<branch>.
func (h *Host) BranchHead(ctx context.Context, workspaceKey string, repo codehost.RepoRef, branch string) (string, error) {
	branch = strings.TrimPrefix(strings.TrimSpace(branch), "refs/heads/")
	if branch == "" {
		return "", errors.New("github: branch is required")
	}
	token, owner, name, err := h.repoToken(ctx, workspaceKey, repo, permissionsFlow)
	if err != nil {
		return "", err
	}
	var out struct {
		Ref    string `json:"ref"`
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	err = h.tokenRequest(ctx, "branch_head", http.MethodGet, repoPath(owner, name)+"/git/ref/heads/"+refPath(branch), token, nil, &out)
	if errors.Is(err, codehost.ErrNotFound) {
		return "", fmt.Errorf("%w: branch %s", codehost.ErrNotFound, branch)
	}
	if err != nil {
		return "", err
	}
	if out.Ref != "" && out.Ref != "refs/heads/"+branch {
		// The matching-refs fallback of the API returned a different ref.
		return "", fmt.Errorf("%w: branch %s", codehost.ErrNotFound, branch)
	}
	return out.Object.SHA, nil
}

// splitHead turns "branch" or "owner:branch" into the API's owner:branch.
func splitHead(owner, head string) (string, string) {
	head = strings.TrimPrefix(strings.TrimSpace(head), "refs/heads/")
	if headOwner, branch, ok := strings.Cut(head, ":"); ok && headOwner != "" {
		return headOwner, branch
	}
	return owner, head
}

// FindChangeRequest returns the open pull request whose head is head
// ("branch" or "owner:branch"), or nil. Closed and merged pull requests are
// not returned, so a new turn after a merge opens a new PR.
func (h *Host) FindChangeRequest(ctx context.Context, workspaceKey string, repo codehost.RepoRef, head string) (*codehost.ChangeRequest, error) {
	token, owner, name, err := h.repoToken(ctx, workspaceKey, repo, permissionsFlow)
	if err != nil {
		return nil, err
	}
	return h.findOpen(ctx, token, repo, owner, name, head)
}

func (h *Host) findOpen(ctx context.Context, token string, repo codehost.RepoRef, owner, name, head string) (*codehost.ChangeRequest, error) {
	headOwner, branch := splitHead(owner, head)
	if branch == "" {
		return nil, errors.New("github: head branch is required")
	}
	var out []apiPullRequest
	query := url.Values{"head": {headOwner + ":" + branch}, "state": {"open"}, "per_page": {"10"}}
	if err := h.tokenRequest(ctx, "find_pull_request", http.MethodGet, repoPath(owner, name)+"/pulls?"+query.Encode(), token, nil, &out); err != nil {
		return nil, err
	}
	for _, item := range out {
		if item.Head.Ref == branch {
			found := h.changeRequest(repo, item)
			return &found, nil
		}
	}
	return nil, nil
}

// GetChangeRequest reads a pull request by number.
func (h *Host) GetChangeRequest(ctx context.Context, workspaceKey string, repo codehost.RepoRef, number int) (codehost.ChangeRequest, error) {
	token, owner, name, err := h.repoToken(ctx, workspaceKey, repo, permissionsFlow)
	if err != nil {
		return codehost.ChangeRequest{}, err
	}
	var out apiPullRequest
	if err := h.tokenRequest(ctx, "get_pull_request", http.MethodGet, repoPath(owner, name)+"/pulls/"+strconv.Itoa(number), token, nil, &out); err != nil {
		return codehost.ChangeRequest{}, err
	}
	return h.changeRequest(repo, out), nil
}

// CreateChangeRequest opens a (draft) pull request. It is idempotent: an open
// pull request for the head is returned as is, and a concurrent create that
// GitHub rejects as a duplicate resolves to the existing one.
func (h *Host) CreateChangeRequest(ctx context.Context, workspaceKey string, input codehost.CreateChangeRequestInput) (codehost.ChangeRequest, error) {
	token, owner, name, err := h.repoToken(ctx, workspaceKey, input.Repo, permissionsFlow)
	if err != nil {
		return codehost.ChangeRequest{}, err
	}
	if existing, err := h.findOpen(ctx, token, input.Repo, owner, name, input.Head); err != nil {
		return codehost.ChangeRequest{}, err
	} else if existing != nil {
		return *existing, nil
	}
	headOwner, branch := splitHead(owner, input.Head)
	head := branch
	if headOwner != owner {
		head = headOwner + ":" + branch
	}
	if strings.TrimSpace(input.Base) == "" || strings.TrimSpace(input.Title) == "" {
		return codehost.ChangeRequest{}, errors.New("github: base and title are required")
	}
	body := map[string]any{"title": input.Title, "head": head, "base": input.Base, "body": input.Body, "draft": input.Draft, "maintainer_can_modify": true}
	var out apiPullRequest
	create := func(authorization string) error {
		err := h.do(ctx, "create_pull_request", http.MethodPost, h.apiURL+repoPath(owner, name)+"/pulls", authorization, body, &out)
		if err != nil && isStatus(err, http.StatusUnprocessableEntity) && body["draft"] == true && strings.Contains(strings.ToLower(err.Error()), "draft") {
			// Draft pull requests are unavailable on some plans; open a regular one.
			body["draft"] = false
			err = h.do(ctx, "create_pull_request", http.MethodPost, h.apiURL+repoPath(owner, name)+"/pulls", authorization, body, &out)
		}
		return err
	}
	alreadyExists := func(err error) bool {
		return isStatus(err, http.StatusUnprocessableEntity) && strings.Contains(strings.ToLower(err.Error()), "already exists")
	}
	byMember := false
	if input.ActAs != nil && strings.TrimSpace(input.ActAs.Token) != "" {
		// The member's user-to-server token: the pull request is theirs. A
		// rejected token (revoked, expired, no access) falls back to the App.
		err = create("Bearer " + input.ActAs.Token)
		switch {
		case err == nil:
			byMember = true
		case alreadyExists(err):
		case isStatus(err, http.StatusUnauthorized), isStatus(err, http.StatusForbidden), isStatus(err, http.StatusNotFound), isStatus(err, http.StatusUnprocessableEntity):
			err = create("token " + token)
		default:
			return codehost.ChangeRequest{}, err
		}
	} else {
		err = create("token " + token)
	}
	if err != nil && alreadyExists(err) {
		if existing, findErr := h.findOpen(ctx, token, input.Repo, owner, name, input.Head); findErr == nil && existing != nil {
			return *existing, nil
		}
	}
	if err != nil {
		return codehost.ChangeRequest{}, err
	}
	if len(input.Labels) > 0 {
		// Labels are best effort: the PR exists either way.
		_ = h.tokenRequest(ctx, "label_pull_request", http.MethodPost, repoPath(owner, name)+"/issues/"+strconv.Itoa(out.Number)+"/labels", token, map[string]any{"labels": input.Labels}, nil)
	}
	change := h.changeRequest(input.Repo, out)
	change.OpenedByMember = byMember
	return change, nil
}

// UpdateChangeRequestBody replaces the pull request description.
func (h *Host) UpdateChangeRequestBody(ctx context.Context, workspaceKey string, repo codehost.RepoRef, number int, body string) error {
	token, owner, name, err := h.repoToken(ctx, workspaceKey, repo, permissionsFlow)
	if err != nil {
		return err
	}
	return h.tokenRequest(ctx, "update_pull_request", http.MethodPatch, repoPath(owner, name)+"/pulls/"+strconv.Itoa(number), token, map[string]any{"body": body}, nil)
}

// Comment adds a conversation comment to the pull request.
func (h *Host) Comment(ctx context.Context, workspaceKey string, repo codehost.RepoRef, number int, body string) error {
	token, owner, name, err := h.repoToken(ctx, workspaceKey, repo, permissionsFlow)
	if err != nil {
		return err
	}
	return h.tokenRequest(ctx, "comment", http.MethodPost, repoPath(owner, name)+"/issues/"+strconv.Itoa(number)+"/comments", token, map[string]any{"body": body}, nil)
}

// CIStatus combines check runs and commit statuses of a ref.
func (h *Host) CIStatus(ctx context.Context, workspaceKey string, repo codehost.RepoRef, ref string) (codehost.CIStatus, error) {
	if strings.TrimSpace(ref) == "" {
		return codehost.CIStatus{}, errors.New("github: ref is required")
	}
	token, owner, name, err := h.repoToken(ctx, workspaceKey, repo, permissionsFlow)
	if err != nil {
		return codehost.CIStatus{}, err
	}
	var checks struct {
		TotalCount int `json:"total_count"`
		CheckRuns  []struct {
			Name       string `json:"name"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
			HTMLURL    string `json:"html_url"`
		} `json:"check_runs"`
	}
	if err := h.tokenRequest(ctx, "check_runs", http.MethodGet, repoPath(owner, name)+"/commits/"+refPath(ref)+"/check-runs?per_page=100", token, nil, &checks); err != nil {
		return codehost.CIStatus{}, err
	}
	var combined struct {
		State    string `json:"state"`
		Statuses []struct {
			State     string `json:"state"`
			TargetURL string `json:"target_url"`
			Context   string `json:"context"`
		} `json:"statuses"`
	}
	if err := h.tokenRequest(ctx, "commit_status", http.MethodGet, repoPath(owner, name)+"/commits/"+refPath(ref)+"/status", token, nil, &combined); err != nil && !errors.Is(err, codehost.ErrNotFound) {
		return codehost.CIStatus{}, err
	}
	var passed, failed, pending int
	failingURL := ""
	for _, run := range checks.CheckRuns {
		switch {
		case run.Status != "completed":
			pending++
		case run.Conclusion == "success" || run.Conclusion == "neutral" || run.Conclusion == "skipped":
			passed++
		default: // failure, timed_out, cancelled, action_required, startup_failure, stale
			failed++
			if failingURL == "" {
				failingURL = run.HTMLURL
			}
		}
	}
	for _, status := range combined.Statuses {
		switch status.State {
		case "success":
			passed++
		case "pending":
			pending++
		default: // failure, error
			failed++
			if failingURL == "" {
				failingURL = status.TargetURL
			}
		}
	}
	result := codehost.CIStatus{Summary: fmt.Sprintf("%d passed, %d failed, %d pending", passed, failed, pending)}
	switch {
	case failed > 0:
		result.State, result.URL = "failure", failingURL
	case pending > 0:
		result.State = "pending"
	case passed > 0:
		result.State = "success"
	default:
		result.State, result.Summary = "none", ""
	}
	return result, nil
}
