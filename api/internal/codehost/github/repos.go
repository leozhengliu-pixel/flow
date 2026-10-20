package github

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/codehost"
	"flow/api/internal/domain"
)

type apiRepository struct {
	ID            int64  `json:"id"`
	FullName      string `json:"full_name"`
	DefaultBranch string `json:"default_branch"`
	CloneURL      string `json:"clone_url"`
	HTMLURL       string `json:"html_url"`
	Private       bool   `json:"private"`
	Archived      bool   `json:"archived"`
}

func (h *Host) repository(item apiRepository, installationID string) codehost.Repository {
	return codehost.Repository{
		RepoRef:       codehost.RepoRef{CodeHost: domain.CodeHostGitHub, HostName: h.cfg.Host, FullName: item.FullName, ID: strconv.FormatInt(item.ID, 10)},
		DefaultBranch: item.DefaultBranch, CloneURL: item.CloneURL, WebURL: item.HTMLURL,
		Private: item.Private, Archived: item.Archived, InstallationID: installationID,
	}
}

// ListRepositories pages through every repository of the workspace's active
// installations. The cursor is "<installation index>:<page>".
func (h *Host) ListRepositories(ctx context.Context, workspaceKey string, options codehost.ListOptions) (codehost.RepositoryPage, error) {
	if err := h.ready(); err != nil {
		return codehost.RepositoryPage{}, err
	}
	if options.HostName != "" && !strings.EqualFold(options.HostName, h.cfg.Host) {
		return codehost.RepositoryPage{}, nil
	}
	all, err := h.workspaceInstallations(ctx, workspaceKey)
	if err != nil {
		return codehost.RepositoryPage{}, err
	}
	active := all[:0:0]
	for _, item := range all {
		if item.SuspendedAt == nil {
			active = append(active, item)
		}
	}
	if len(active) == 0 {
		return codehost.RepositoryPage{}, fmt.Errorf("%w: no active GitHub App installation in this workspace", codehost.ErrNotConnected)
	}
	index, page := 0, 1
	if options.Cursor != "" {
		left, right, ok := strings.Cut(options.Cursor, ":")
		index, err = strconv.Atoi(left)
		if err == nil && ok {
			page, err = strconv.Atoi(right)
		}
		if err != nil || !ok || index < 0 || page < 1 {
			return codehost.RepositoryPage{}, fmt.Errorf("github: invalid cursor %q", options.Cursor)
		}
	}
	if index >= len(active) {
		return codehost.RepositoryPage{Repositories: []codehost.Repository{}}, nil
	}
	perPage := 100
	if options.Limit > 0 && options.Limit < perPage {
		perPage = options.Limit
	}
	installation := active[index]
	token, _, err := h.installationToken(ctx, tokenRequest{installationID: installation.InstallationID, permissions: permissionsMetadata, scope: "list"})
	if err != nil {
		return codehost.RepositoryPage{}, err
	}
	var out struct {
		TotalCount   int             `json:"total_count"`
		Repositories []apiRepository `json:"repositories"`
	}
	if err := h.tokenRequest(ctx, "list_repositories", http.MethodGet, fmt.Sprintf("/installation/repositories?per_page=%d&page=%d", perPage, page), token.token, nil, &out); err != nil {
		return codehost.RepositoryPage{}, err
	}
	query := strings.ToLower(strings.TrimSpace(options.Query))
	result := codehost.RepositoryPage{Repositories: []codehost.Repository{}}
	for _, item := range out.Repositories {
		if query == "" || strings.Contains(strings.ToLower(item.FullName), query) {
			result.Repositories = append(result.Repositories, h.repository(item, installation.InstallationID))
		}
	}
	switch {
	case len(out.Repositories) == perPage && page*perPage < out.TotalCount:
		result.NextCursor = fmt.Sprintf("%d:%d", index, page+1)
	case index+1 < len(active):
		result.NextCursor = fmt.Sprintf("%d:1", index+1)
	}
	return result, nil
}

// ResolveRepository checks that the workspace's installation covers the
// repository and returns its metadata.
func (h *Host) ResolveRepository(ctx context.Context, workspaceKey string, repo codehost.RepoRef) (codehost.Repository, error) {
	installationID, err := h.installationFor(ctx, workspaceKey, repo)
	if err != nil {
		return codehost.Repository{}, err
	}
	owner, name, _ := h.checkRepo(repo)
	token, _, err := h.installationToken(ctx, tokenRequest{installationID: installationID, repository: name, permissions: permissionsMetadata, scope: "flow"})
	if err != nil {
		return codehost.Repository{}, err
	}
	var out apiRepository
	err = h.tokenRequest(ctx, "get_repository", http.MethodGet, "/repos/"+pathEscape(owner)+"/"+pathEscape(name), token.token, nil, &out)
	if errors.Is(err, codehost.ErrNotFound) {
		return codehost.Repository{}, fmt.Errorf("%w: %s/%s", codehost.ErrNoAccess, owner, name)
	}
	if err != nil {
		return codehost.Repository{}, err
	}
	return h.repository(out, installationID), nil
}

// UserLink is a Flow user's linked GitHub account (App user-to-server OAuth).
// Only the identity is kept; the OAuth token is discarded after linking.
type UserLink struct {
	Login    string    `json:"login"`
	ID       int64     `json:"id"`
	HostName string    `json:"hostName"`
	LinkedAt time.Time `json:"linkedAt"`
}

// UserLinkSecretID is the connector secret id holding a user's link.
func UserLinkSecretID(hostName, flowUserID string) string {
	return "github-app-user:" + strings.ToLower(hostName) + ":" + flowUserID
}

// UserLink reads the Flow user's linked GitHub account (ErrUserNotLinked when none).
func (h *Host) UserLink(ctx context.Context, flowUserID string) (UserLink, error) {
	if flowUserID == "" || h.deps.Secrets == nil {
		return UserLink{}, codehost.ErrUserNotLinked
	}
	raw, err := h.deps.Secrets.ReadConnectorSecret(ctx, UserLinkSecretID(h.cfg.Host, flowUserID), false)
	if errors.Is(err, sql.ErrNoRows) {
		return UserLink{}, codehost.ErrUserNotLinked
	}
	if err != nil {
		return UserLink{}, fmt.Errorf("github: read linked account: %w", err)
	}
	var link UserLink
	if err := json.Unmarshal(raw, &link); err != nil || link.Login == "" {
		return UserLink{}, codehost.ErrUserNotLinked
	}
	return link, nil
}

// CheckUserAccess verifies that the requesting user's GitHub account may
// push to the repository: GET /repos/{o}/{r}/collaborators/{login}/permission
// must return write, maintain or admin. Without a login (and no link for the
// Flow user) it is ErrUserNotLinked.
func (h *Host) CheckUserAccess(ctx context.Context, workspaceKey string, repo codehost.RepoRef, user codehost.UserIdentity) error {
	login := strings.TrimSpace(user.Login)
	if login == "" {
		link, err := h.UserLink(ctx, user.FlowUserID)
		if err != nil {
			return err
		}
		login = link.Login
	}
	token, owner, name, err := h.repoToken(ctx, workspaceKey, repo, permissionsMetadata)
	if err != nil {
		return err
	}
	var out struct {
		Permission string `json:"permission"`
		RoleName   string `json:"role_name"`
	}
	err = h.tokenRequest(ctx, "user_permission", http.MethodGet, "/repos/"+pathEscape(owner)+"/"+pathEscape(name)+"/collaborators/"+pathEscape(login)+"/permission", token, nil, &out)
	if errors.Is(err, codehost.ErrNotFound) {
		return fmt.Errorf("%w: %s is not a collaborator on %s/%s", codehost.ErrUserNoAccess, login, owner, name)
	}
	if err != nil {
		return err
	}
	switch strings.ToLower(out.Permission) {
	case "admin", "write", "maintain":
		return nil
	}
	switch strings.ToLower(out.RoleName) {
	case "admin", "maintain", "write":
		return nil
	}
	return fmt.Errorf("%w: %s has %q access to %s/%s", codehost.ErrUserNoAccess, login, out.Permission, owner, name)
}

// MintCredential issues an installation token for one repository with the
// minimal permissions for the access level. Tokens are cached per sandbox
// (so revoking one never affects another sandbox) until 5 minutes before
// expiry; git asks the credential helper again, so long sessions refresh.
func (h *Host) MintCredential(ctx context.Context, request codehost.CredentialRequest) (codehost.Credential, error) {
	_, name, err := h.checkRepo(request.Repo)
	if err != nil {
		return codehost.Credential{}, err
	}
	installationID, err := h.installationFor(ctx, request.WorkspaceKey, request.Repo)
	if err != nil {
		return codehost.Credential{}, err
	}
	permissions := permissionsSandboxRead
	if request.Access == codehost.AccessWrite {
		permissions = permissionsSandboxWrite
	}
	scope := "sandbox:" + request.SandboxID
	if request.SandboxID == "" {
		scope = "session:" + request.SessionID
	}
	token, key, err := h.installationToken(ctx, tokenRequest{installationID: installationID, repository: name, permissions: permissions, scope: scope})
	if err != nil {
		return codehost.Credential{}, err
	}
	env := map[string]string{"GH_TOKEN": token.token, "GH_HOST": h.cfg.Host}
	if h.cfg.Host != "github.com" {
		env["GH_ENTERPRISE_TOKEN"] = token.token
	}
	return codehost.Credential{Username: "x-access-token", Password: token.token, ExpiresAt: token.expiresAt, RevokeID: key, CLIEnv: env}, nil
}

// RevokeCredential revokes an installation token early (DELETE
// /installation/token) and drops it from the cache. An already expired or
// revoked token counts as success.
func (h *Host) RevokeCredential(ctx context.Context, _ string, credential codehost.Credential) error {
	token := credential.Password
	h.mu.Lock()
	if credential.RevokeID != "" {
		if cached, ok := h.tokens[credential.RevokeID]; ok {
			if token == "" {
				token = cached.token
			}
			if cached.token == token {
				delete(h.tokens, credential.RevokeID)
			}
		}
	}
	for key, cached := range h.tokens {
		if token != "" && cached.token == token {
			delete(h.tokens, key)
		}
	}
	h.mu.Unlock()
	if token == "" {
		return nil
	}
	err := h.tokenRequest(ctx, "revoke_token", http.MethodDelete, "/installation/token", token, nil, nil)
	if err == nil || errors.Is(err, codehost.ErrNoAccess) || errors.Is(err, codehost.ErrNotFound) {
		return nil
	}
	return err
}

// InstallURL is where an admin installs the App; state comes back on /setup.
func (h *Host) InstallURL(state string) string {
	return h.webURL + "/apps/" + url.PathEscape(h.cfg.Slug) + "/installations/new?state=" + url.QueryEscape(state)
}

// AuthorizeURL starts user-to-server OAuth to link a user's GitHub account.
func (h *Host) AuthorizeURL(state, redirectURI string) string {
	return h.AuthorizeURLWithChallenge(state, redirectURI, "")
}

// AuthorizeURLWithChallenge is AuthorizeURL with a PKCE S256 code challenge.
func (h *Host) AuthorizeURLWithChallenge(state, redirectURI, challenge string) string {
	values := url.Values{"client_id": {h.cfg.ClientID}, "state": {state}}
	if redirectURI != "" {
		values.Set("redirect_uri", redirectURI)
	}
	if challenge != "" {
		values.Set("code_challenge", challenge)
		values.Set("code_challenge_method", "S256")
	}
	return h.webURL + "/login/oauth/authorize?" + values.Encode()
}

// APIURL is the REST base the App talks to (api.github.com or <ghe>/api/v3).
func (h *Host) APIURL() string { return h.apiURL }

// SetWebURL overrides the web base used for OAuth (tests point it at a fake
// GitHub; production derives it from the host).
func (h *Host) SetWebURL(webURL string) { h.webURL = strings.TrimRight(webURL, "/") }

// UserToken is a member's user-to-server token pair. GitHub Apps with
// expiring user tokens issue 8-hour access tokens and 6-month refresh
// tokens; without expiry both expiry times are zero.
type UserToken struct {
	AccessToken      string    `json:"accessToken"`
	RefreshToken     string    `json:"refreshToken,omitempty"`
	ExpiresAt        time.Time `json:"expiresAt,omitempty"`
	RefreshExpiresAt time.Time `json:"refreshExpiresAt,omitempty"`
}

type oauthTokenResponse struct {
	AccessToken           string `json:"access_token"`
	RefreshToken          string `json:"refresh_token"`
	ExpiresIn             int64  `json:"expires_in"`
	RefreshTokenExpiresIn int64  `json:"refresh_token_expires_in"`
	Error                 string `json:"error"`
	Description           string `json:"error_description"`
}

func (h *Host) userToken(out oauthTokenResponse) (UserToken, error) {
	if out.AccessToken == "" {
		if out.Error == "bad_refresh_token" || out.Error == "bad_verification_code" {
			return UserToken{}, fmt.Errorf("%w: github: %s", codehost.ErrUserNotLinked, out.Error)
		}
		return UserToken{}, fmt.Errorf("github: authorization failed: %s %s", out.Error, out.Description)
	}
	token := UserToken{AccessToken: out.AccessToken, RefreshToken: out.RefreshToken}
	now := h.now().UTC()
	if out.ExpiresIn > 0 {
		token.ExpiresAt = now.Add(time.Duration(out.ExpiresIn) * time.Second)
	}
	if out.RefreshTokenExpiresIn > 0 {
		token.RefreshExpiresAt = now.Add(time.Duration(out.RefreshTokenExpiresIn) * time.Second)
	}
	return token, nil
}

// ExchangeUserToken finishes user-to-server OAuth (with the PKCE verifier
// when one was sent) and returns the member's identity and token pair.
func (h *Host) ExchangeUserToken(ctx context.Context, code, redirectURI, verifier string) (UserLink, UserToken, error) {
	if !h.CanAuthorizeUsers() {
		return UserLink{}, UserToken{}, fmt.Errorf("%w: GitHub App user authorization is not configured", codehost.ErrNotConnected)
	}
	body := map[string]string{"client_id": h.cfg.ClientID, "client_secret": h.cfg.ClientSecret, "code": code}
	if redirectURI != "" {
		body["redirect_uri"] = redirectURI
	}
	if verifier != "" {
		body["code_verifier"] = verifier
	}
	var out oauthTokenResponse
	if err := h.do(ctx, "oauth_exchange", http.MethodPost, h.webURL+"/login/oauth/access_token", "", body, &out); err != nil {
		return UserLink{}, UserToken{}, err
	}
	token, err := h.userToken(out)
	if err != nil {
		return UserLink{}, UserToken{}, err
	}
	link, err := h.UserForToken(ctx, token.AccessToken)
	return link, token, err
}

// UserForToken reads GET /user with a member's token.
func (h *Host) UserForToken(ctx context.Context, accessToken string) (UserLink, error) {
	var user struct {
		Login string `json:"login"`
		ID    int64  `json:"id"`
	}
	if err := h.do(ctx, "get_user", http.MethodGet, h.apiURL+"/user", "Bearer "+accessToken, nil, &user); err != nil {
		return UserLink{}, err
	}
	if user.Login == "" {
		return UserLink{}, errors.New("github: authorization returned no user")
	}
	return UserLink{Login: user.Login, ID: user.ID, HostName: h.cfg.Host, LinkedAt: h.now().UTC()}, nil
}

// RefreshUserToken exchanges a refresh token for a new pair (GitHub rotates
// the refresh token). A rejected refresh token is ErrUserNotLinked: the
// member must reconnect.
func (h *Host) RefreshUserToken(ctx context.Context, refreshToken string) (UserToken, error) {
	if !h.CanAuthorizeUsers() {
		return UserToken{}, fmt.Errorf("%w: GitHub App user authorization is not configured", codehost.ErrNotConnected)
	}
	if strings.TrimSpace(refreshToken) == "" {
		return UserToken{}, fmt.Errorf("%w: no refresh token", codehost.ErrUserNotLinked)
	}
	body := map[string]string{"client_id": h.cfg.ClientID, "client_secret": h.cfg.ClientSecret, "grant_type": "refresh_token", "refresh_token": refreshToken}
	var out oauthTokenResponse
	if err := h.do(ctx, "oauth_refresh", http.MethodPost, h.webURL+"/login/oauth/access_token", "", body, &out); err != nil {
		return UserToken{}, err
	}
	return h.userToken(out)
}

// ExchangeUserCode finishes user-to-server OAuth and returns the user's
// GitHub identity. The access token is used once for GET /user and dropped.
func (h *Host) ExchangeUserCode(ctx context.Context, code, redirectURI string) (UserLink, error) {
	if !h.CanAuthorizeUsers() {
		return UserLink{}, fmt.Errorf("%w: GitHub App user authorization is not configured", codehost.ErrNotConnected)
	}
	body := map[string]string{"client_id": h.cfg.ClientID, "client_secret": h.cfg.ClientSecret, "code": code}
	if redirectURI != "" {
		body["redirect_uri"] = redirectURI
	}
	var token struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if err := h.do(ctx, "oauth_exchange", http.MethodPost, h.webURL+"/login/oauth/access_token", "", body, &token); err != nil {
		return UserLink{}, err
	}
	if token.AccessToken == "" {
		return UserLink{}, fmt.Errorf("github: authorization failed: %s %s", token.Error, token.Description)
	}
	var user struct {
		Login string `json:"login"`
		ID    int64  `json:"id"`
	}
	if err := h.do(ctx, "get_user", http.MethodGet, h.apiURL+"/user", "Bearer "+token.AccessToken, nil, &user); err != nil {
		return UserLink{}, err
	}
	if user.Login == "" {
		return UserLink{}, errors.New("github: authorization returned no user")
	}
	return UserLink{Login: user.Login, ID: user.ID, HostName: h.cfg.Host, LinkedAt: h.now().UTC()}, nil
}

type apiInstallation struct {
	ID      int64 `json:"id"`
	Account struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"account"`
	RepositorySelection string     `json:"repository_selection"`
	SuspendedAt         *time.Time `json:"suspended_at"`
}

func (h *Host) installation(item apiInstallation) domain.CodeHostInstallation {
	return domain.CodeHostInstallation{
		CodeHost: domain.CodeHostGitHub, HostName: h.cfg.Host, InstallationID: strconv.FormatInt(item.ID, 10),
		AccountLogin: item.Account.Login, AccountType: item.Account.Type, RepositorySelection: item.RepositorySelection,
		SuspendedAt: item.SuspendedAt,
	}
}

// Installation fetches an installation with the App JWT (setup callback).
func (h *Host) Installation(ctx context.Context, installationID string) (domain.CodeHostInstallation, error) {
	if _, err := strconv.ParseInt(installationID, 10, 64); err != nil {
		return domain.CodeHostInstallation{}, fmt.Errorf("github: invalid installation id %q", installationID)
	}
	var out apiInstallation
	err := h.appRequest(ctx, "get_installation", http.MethodGet, "/app/installations/"+installationID, nil, &out)
	if errors.Is(err, codehost.ErrNotFound) {
		return domain.CodeHostInstallation{}, fmt.Errorf("%w: GitHub App installation %s not found", codehost.ErrNoAccess, installationID)
	}
	if err != nil {
		return domain.CodeHostInstallation{}, err
	}
	return h.installation(out), nil
}
