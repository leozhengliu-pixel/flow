package gitlab

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"flow/api/internal/codehost"
	"flow/api/internal/domain"
)

type apiMergeRequest struct {
	ID             int64  `json:"id"`
	IID            int    `json:"iid"`
	ProjectID      int64  `json:"project_id"`
	SourceProject  int64  `json:"source_project_id"`
	TargetProject  int64  `json:"target_project_id"`
	Title          string `json:"title"`
	State          string `json:"state"`
	Draft          bool   `json:"draft"`
	WorkInProgress bool   `json:"work_in_progress"`
	SourceBranch   string `json:"source_branch"`
	TargetBranch   string `json:"target_branch"`
	SHA            string `json:"sha"`
	WebURL         string `json:"web_url"`
	Description    string `json:"description"`
	Author         struct {
		Username string `json:"username"`
	} `json:"author"`
}

// normalizeMRState maps GitLab states to open | closed | merged.
func normalizeMRState(state string) string {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "opened", "open", "reopened":
		return "open"
	case "merged":
		return "merged"
	default: // closed, locked
		return "closed"
	}
}

var draftTitle = regexp.MustCompile(`(?i)^\s*(\[draft\]|\(draft\)|draft:|draft\s|wip:|\[wip\])`)

func (m apiMergeRequest) changeRequest(connection Connection, repo codehost.RepoRef) codehost.ChangeRequest {
	ref := repo
	ref.CodeHost, ref.HostName = domain.CodeHostGitLab, connection.HostName
	if m.ProjectID != 0 && ref.ID == "" {
		ref.ID = strconv.FormatInt(m.ProjectID, 10)
	}
	return codehost.ChangeRequest{
		Repo:        ref,
		Number:      m.IID,
		ExternalID:  strconv.FormatInt(m.ID, 10),
		URL:         m.WebURL,
		Title:       m.Title,
		State:       normalizeMRState(m.State),
		Draft:       m.Draft || m.WorkInProgress || draftTitle.MatchString(m.Title),
		HeadBranch:  m.SourceBranch,
		BaseBranch:  m.TargetBranch,
		HeadSHA:     m.SHA,
		AuthorLogin: m.Author.Username,
		Body:        m.Description,
	}
}

func mergeRequestPath(repo codehost.RepoRef, number int) string {
	return fmt.Sprintf("/projects/%s/merge_requests/%d", projectPath(repo), number)
}

// FindChangeRequest returns the open MR whose source branch is head, or nil.
func (h *Host) FindChangeRequest(ctx context.Context, workspaceKey string, repo codehost.RepoRef, head string) (*codehost.ChangeRequest, error) {
	connection, client, err := h.repoClient(ctx, workspaceKey, repo)
	if err != nil {
		return nil, err
	}
	return h.findOpen(ctx, connection, client, repo, head)
}

func (h *Host) findOpen(ctx context.Context, connection Connection, client *apiClient, repo codehost.RepoRef, head string) (*codehost.ChangeRequest, error) {
	query := url.Values{}
	query.Set("source_branch", head)
	query.Set("state", "opened")
	query.Set("order_by", "created_at")
	query.Set("sort", "desc")
	query.Set("per_page", "20")
	var mergeRequests []apiMergeRequest
	if _, err := client.do(ctx, http.MethodGet, "/projects/"+projectPath(repo)+"/merge_requests?"+query.Encode(), nil, &mergeRequests); err != nil {
		if errors.Is(err, codehost.ErrNotFound) {
			return nil, &Error{Kind: codehost.ErrNoAccess, Message: fmt.Sprintf("The GitLab connection cannot access %s.", repo.FullName), Status: statusOf(err)}
		}
		return nil, err
	}
	for _, mergeRequest := range mergeRequests {
		// Only same-project MRs: a fork MR with the same branch name is not ours.
		if mergeRequest.SourceBranch == head && (mergeRequest.SourceProject == 0 || mergeRequest.SourceProject == mergeRequest.TargetProject) {
			found := mergeRequest.changeRequest(connection, repo)
			return &found, nil
		}
	}
	return nil, nil
}

// GetChangeRequest reads one MR by iid.
func (h *Host) GetChangeRequest(ctx context.Context, workspaceKey string, repo codehost.RepoRef, number int) (codehost.ChangeRequest, error) {
	connection, client, err := h.repoClient(ctx, workspaceKey, repo)
	if err != nil {
		return codehost.ChangeRequest{}, err
	}
	var mergeRequest apiMergeRequest
	if _, err := client.do(ctx, http.MethodGet, mergeRequestPath(repo, number), nil, &mergeRequest); err != nil {
		return codehost.ChangeRequest{}, err
	}
	return mergeRequest.changeRequest(connection, repo), nil
}

// DraftTitle prefixes title with GitLab's "Draft: " marker once.
func DraftTitle(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Flow coding session"
	}
	if draftTitle.MatchString(title) {
		return title
	}
	return "Draft: " + title
}

// CreateChangeRequest opens a draft MR, idempotently: an open MR for Head is
// returned as is, and a concurrent creation (409) resolves to the winner.
func (h *Host) CreateChangeRequest(ctx context.Context, workspaceKey string, input codehost.CreateChangeRequestInput) (codehost.ChangeRequest, error) {
	connection, client, err := h.repoClient(ctx, workspaceKey, input.Repo)
	if err != nil {
		return codehost.ChangeRequest{}, err
	}
	if strings.TrimSpace(input.Head) == "" {
		return codehost.ChangeRequest{}, &Error{Kind: errAPI, Message: "A source branch is required to open a merge request."}
	}
	if existing, err := h.findOpen(ctx, connection, client, input.Repo, input.Head); err != nil || existing != nil {
		if existing != nil {
			return *existing, nil
		}
		return codehost.ChangeRequest{}, err
	}
	base := strings.TrimSpace(input.Base)
	if base == "" {
		project, err := h.getProject(ctx, client, input.Repo)
		if err != nil {
			return codehost.ChangeRequest{}, err
		}
		base = project.DefaultBranch
	}
	title := strings.TrimSpace(input.Title)
	if input.Draft {
		title = DraftTitle(title)
	} else if title == "" {
		title = "Flow coding session"
	}
	payload := map[string]any{
		"source_branch":        input.Head,
		"target_branch":        base,
		"title":                title,
		"description":          input.Body,
		"remove_source_branch": true,
	}
	if len(input.Labels) > 0 {
		payload["labels"] = strings.Join(input.Labels, ",")
	}
	var created apiMergeRequest
	if _, err := client.do(ctx, http.MethodPost, "/projects/"+projectPath(input.Repo)+"/merge_requests", payload, &created); err != nil {
		if statusOf(err) == http.StatusConflict {
			if existing, findErr := h.findOpen(ctx, connection, client, input.Repo, input.Head); findErr == nil && existing != nil {
				return *existing, nil
			}
		}
		return codehost.ChangeRequest{}, err
	}
	return created.changeRequest(connection, input.Repo), nil
}

// UpdateChangeRequestBody replaces the MR description.
func (h *Host) UpdateChangeRequestBody(ctx context.Context, workspaceKey string, repo codehost.RepoRef, number int, body string) error {
	_, client, err := h.repoClient(ctx, workspaceKey, repo)
	if err != nil {
		return err
	}
	_, err = client.do(ctx, http.MethodPut, mergeRequestPath(repo, number), map[string]any{"description": body}, nil)
	return err
}

// Comment adds an MR note.
func (h *Host) Comment(ctx context.Context, workspaceKey string, repo codehost.RepoRef, number int, body string) error {
	_, client, err := h.repoClient(ctx, workspaceKey, repo)
	if err != nil {
		return err
	}
	_, err = client.do(ctx, http.MethodPost, mergeRequestPath(repo, number)+"/notes", map[string]any{"body": body}, nil)
	return err
}

var shaPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// normalizePipelineStatus maps GitLab pipeline statuses to the CIStatus states.
func normalizePipelineStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "success":
		return "success"
	case "failed", "canceled", "cancelled":
		return "failure"
	case "skipped", "":
		return "none"
	default: // created, waiting_for_resource, preparing, pending, running, scheduled, manual
		return "pending"
	}
}

// CIStatus returns the latest pipeline of ref (a branch, tag or commit SHA).
func (h *Host) CIStatus(ctx context.Context, workspaceKey string, repo codehost.RepoRef, ref string) (codehost.CIStatus, error) {
	_, client, err := h.repoClient(ctx, workspaceKey, repo)
	if err != nil {
		return codehost.CIStatus{}, err
	}
	query := url.Values{}
	if shaPattern.MatchString(strings.ToLower(ref)) {
		query.Set("sha", strings.ToLower(ref))
	} else {
		query.Set("ref", ref)
	}
	query.Set("order_by", "id")
	query.Set("sort", "desc")
	query.Set("per_page", "1")
	var pipelines []struct {
		ID     int64  `json:"id"`
		Status string `json:"status"`
		WebURL string `json:"web_url"`
		Ref    string `json:"ref"`
	}
	if _, err := client.do(ctx, http.MethodGet, "/projects/"+projectPath(repo)+"/pipelines?"+query.Encode(), nil, &pipelines); err != nil {
		return codehost.CIStatus{}, err
	}
	if len(pipelines) == 0 {
		return codehost.CIStatus{State: "none"}, nil
	}
	pipeline := pipelines[0]
	status := codehost.CIStatus{State: normalizePipelineStatus(pipeline.Status), URL: pipeline.WebURL}
	status.Summary = fmt.Sprintf("Pipeline #%d %s", pipeline.ID, strings.ReplaceAll(pipeline.Status, "_", " "))
	if status.State == "failure" {
		var jobs []struct {
			Name  string `json:"name"`
			Stage string `json:"stage"`
		}
		if _, err := client.do(ctx, http.MethodGet, fmt.Sprintf("/projects/%s/pipelines/%d/jobs?scope[]=failed&per_page=20", projectPath(repo), pipeline.ID), nil, &jobs); err == nil && len(jobs) > 0 {
			names := make([]string, 0, len(jobs))
			for _, job := range jobs {
				names = append(names, job.Stage+"/"+job.Name)
			}
			status.Summary += ": failed jobs " + summarizeList(names, 5)
		}
	}
	return status, nil
}

func summarizeList(items []string, limit int) string {
	if len(items) <= limit {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:limit], ", ") + fmt.Sprintf(" and %d more", len(items)-limit)
}
