package gitlab

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

// GitLab access levels.
const (
	accessDeveloper  = 30
	accessMaintainer = 40
)

type apiProject struct {
	ID                int64  `json:"id"`
	PathWithNamespace string `json:"path_with_namespace"`
	DefaultBranch     string `json:"default_branch"`
	HTTPURLToRepo     string `json:"http_url_to_repo"`
	WebURL            string `json:"web_url"`
	Visibility        string `json:"visibility"`
	Archived          bool   `json:"archived"`
	Permissions       *struct {
		ProjectAccess *struct {
			AccessLevel int `json:"access_level"`
		} `json:"project_access"`
		GroupAccess *struct {
			AccessLevel int `json:"access_level"`
		} `json:"group_access"`
	} `json:"permissions"`
}

func (p apiProject) accessLevel() int {
	level := 0
	if p.Permissions != nil {
		if p.Permissions.ProjectAccess != nil {
			level = max(level, p.Permissions.ProjectAccess.AccessLevel)
		}
		if p.Permissions.GroupAccess != nil {
			level = max(level, p.Permissions.GroupAccess.AccessLevel)
		}
	}
	return level
}

func (p apiProject) repository(connection Connection) codehost.Repository {
	return codehost.Repository{
		RepoRef: codehost.RepoRef{
			CodeHost: domain.CodeHostGitLab,
			HostName: connection.HostName,
			FullName: p.PathWithNamespace,
			ID:       strconv.FormatInt(p.ID, 10),
		},
		DefaultBranch:  p.DefaultBranch,
		CloneURL:       p.HTTPURLToRepo,
		WebURL:         p.WebURL,
		Private:        p.Visibility != "public",
		Archived:       p.Archived,
		InstallationID: connection.ID,
	}
}

// ListRepositories lists projects the connection can push to (Developer or
// higher), most recently active first. Cursor is GitLab's page number.
func (h *Host) ListRepositories(ctx context.Context, workspaceKey string, options codehost.ListOptions) (codehost.RepositoryPage, error) {
	connection, err := h.connection(ctx, workspaceKey, options.HostName)
	if err != nil {
		return codehost.RepositoryPage{}, err
	}
	client, err := h.client(ctx, connection)
	if err != nil {
		return codehost.RepositoryPage{}, err
	}
	limit := options.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	query := url.Values{}
	query.Set("membership", "true")
	query.Set("min_access_level", strconv.Itoa(accessDeveloper))
	query.Set("archived", "false")
	query.Set("order_by", "last_activity_at")
	query.Set("sort", "desc")
	query.Set("per_page", strconv.Itoa(limit))
	query.Set("page", strconv.Itoa(pageNumber(options.Cursor)))
	if search := strings.TrimSpace(options.Query); search != "" {
		query.Set("search", search)
		query.Set("search_namespaces", "true")
	}
	var projects []apiProject
	resp, err := client.do(ctx, http.MethodGet, "/projects?"+query.Encode(), nil, &projects)
	if err != nil {
		return codehost.RepositoryPage{}, err
	}
	page := codehost.RepositoryPage{Repositories: make([]codehost.Repository, 0, len(projects))}
	for _, project := range projects {
		page.Repositories = append(page.Repositories, project.repository(connection))
	}
	if resp.NextPage != "" {
		page.NextCursor = resp.NextPage
	}
	return page, nil
}

func (h *Host) getProject(ctx context.Context, client *apiClient, repo codehost.RepoRef) (apiProject, error) {
	var project apiProject
	if _, err := client.do(ctx, http.MethodGet, "/projects/"+projectPath(repo), nil, &project); err != nil {
		if errors.Is(err, codehost.ErrNotFound) {
			// GitLab answers 404 for private projects the token cannot see.
			return project, &Error{Kind: codehost.ErrNoAccess, Message: fmt.Sprintf("The GitLab connection cannot access %s.", repo.FullName), Status: statusOf(err)}
		}
		return project, err
	}
	return project, nil
}

// ResolveRepository returns the project (default branch, clone URL, id).
func (h *Host) ResolveRepository(ctx context.Context, workspaceKey string, repo codehost.RepoRef) (codehost.Repository, error) {
	connection, client, err := h.repoClient(ctx, workspaceKey, repo)
	if err != nil {
		return codehost.Repository{}, err
	}
	project, err := h.getProject(ctx, client, repo)
	if err != nil {
		return codehost.Repository{}, err
	}
	return project.repository(connection), nil
}

// CheckCredentialMode verifies the connection can serve the configured
// credential mode for repo (Maintainer + api scope for project-token, an
// OAuth connection for oauth). The environment editor and provisioning use
// it to fail early with a precise message.
func (h *Host) CheckCredentialMode(ctx context.Context, workspaceKey string, repo codehost.RepoRef) error {
	connection, client, err := h.repoClient(ctx, workspaceKey, repo)
	if err != nil {
		return err
	}
	switch h.CredentialMode() {
	case ModeOAuth:
		if connection.Kind != ConnectionOAuth {
			return &Error{Kind: ErrOAuthRequired, Message: MessageOAuthRequired}
		}
	case ModeProjectToken:
		if connection.Readonly {
			return &Error{Kind: ErrReadOnlyConnection, Message: MessageReadOnly}
		}
		project, err := h.getProject(ctx, client, repo)
		if err != nil {
			return err
		}
		if level := project.accessLevel(); level > 0 && level < accessMaintainer {
			return &Error{Kind: ErrProjectTokensUnavailable, Message: MessageProjectTokens}
		}
	}
	return nil
}

// CheckUserAccess verifies the requesting user is at least Developer on the
// project (Linear's personal access check).
func (h *Host) CheckUserAccess(ctx context.Context, workspaceKey string, repo codehost.RepoRef, user codehost.UserIdentity) error {
	login := strings.TrimPrefix(strings.TrimSpace(user.Login), "@")
	if login == "" {
		return &Error{Kind: ErrUserNotLinked, Message: MessageUserNotLinked}
	}
	_, client, err := h.repoClient(ctx, workspaceKey, repo)
	if err != nil {
		return err
	}
	var users []struct {
		ID       int64  `json:"id"`
		Username string `json:"username"`
	}
	if _, err := client.do(ctx, http.MethodGet, "/users?username="+url.QueryEscape(login), nil, &users); err != nil {
		return err
	}
	if len(users) == 0 {
		return &Error{Kind: codehost.ErrUserNoAccess, Message: fmt.Sprintf("GitLab user %s does not exist.", login)}
	}
	var member struct {
		AccessLevel int `json:"access_level"`
	}
	_, err = client.do(ctx, http.MethodGet, fmt.Sprintf("/projects/%s/members/all/%d", projectPath(repo), users[0].ID), nil, &member)
	if errors.Is(err, codehost.ErrNotFound) {
		return &Error{Kind: codehost.ErrUserNoAccess, Message: fmt.Sprintf("%s is not a member of %s on GitLab.", login, repo.FullName)}
	}
	if err != nil {
		return err
	}
	if member.AccessLevel < accessDeveloper {
		return &Error{Kind: codehost.ErrUserNoAccess, Message: fmt.Sprintf("%s needs the Developer role (or higher) on %s to start coding sessions.", login, repo.FullName)}
	}
	return nil
}

// BranchHead returns the branch's head SHA ("" and ErrNotFound when absent).
func (h *Host) BranchHead(ctx context.Context, workspaceKey string, repo codehost.RepoRef, branch string) (string, error) {
	_, client, err := h.repoClient(ctx, workspaceKey, repo)
	if err != nil {
		return "", err
	}
	var result struct {
		Commit struct {
			ID string `json:"id"`
		} `json:"commit"`
	}
	_, err = client.do(ctx, http.MethodGet, "/projects/"+projectPath(repo)+"/repository/branches/"+url.PathEscape(branch), nil, &result)
	if err != nil {
		return "", err
	}
	if result.Commit.ID == "" {
		return "", fmt.Errorf("%w: branch %s has no commit", codehost.ErrNotFound, branch)
	}
	return result.Commit.ID, nil
}
