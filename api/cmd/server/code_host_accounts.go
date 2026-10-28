package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/codehost"
	githubhost "flow/api/internal/codehost/github"
	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

// Members' personal GitHub and GitLab accounts (Settings › Account ›
// Connected accounts; docs/integrations.md "Personal accounts").
//
// Code hosting works at two levels. An admin connects the workspace once
// (GitHub App installation or classic connection, GitLab connection with an
// optional self-hosted URL); each member may then link their own account so
// Flow attributes host activity to them and acts as them: Reviews comments,
// approvals, change requests and merges, and coding session pushes and
// PRs/MRs.
//
//   - GitHub: the GitHub App's user-to-server OAuth when the App has a client
//     id and secret (8-hour tokens with refresh), otherwise a classic OAuth
//     app from FLOW_INTEGRATION_GITHUB_CLIENT_ID / _CLIENT_SECRET.
//   - GitLab: only instances an admin connected to the workspace are offered
//     (no member-supplied hosts, so no SSRF and no token sent elsewhere).
//     OAuth through an application the admin registered on that instance
//     (2-hour tokens with refresh), or a personal access token with the
//     read_user and api scopes when no application is configured.
//
// Tokens live only in the encrypted connector secret store, are used only
// for the member they belong to, and never reach the browser. Every flow
// uses a one-time server-side state with a PKCE verifier; link and unlink
// write an audit entry.

const (
	codeHostLinkCallbackPath = "/api/account/code-host-accounts/oauth/callback"
	codeHostLinkStatePrefix  = "chl."
	codeHostLinkStateTTL     = 15 * time.Minute
	// codeHostTokenRefreshMargin refreshes access tokens this long before
	// they expire.
	codeHostTokenRefreshMargin = 5 * time.Minute
)

// gitlabMemberScopes are the scopes members' GitLab links need: read_user
// for the identity, api for comments, approvals and merge requests.
var gitlabMemberScopes = []string{"api", "read_user"}

func codeHostLinkCallbackURL() string { return appBaseURL() + codeHostLinkCallbackPath }

func (s *server) registerCodeHostAccountRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/account/code-host-accounts", s.listCodeHostAccounts)
	mux.HandleFunc("POST /api/account/code-host-accounts/github/authorize", s.startGitHubAccountLink)
	mux.HandleFunc("POST /api/account/code-host-accounts/gitlab/authorize", s.startGitLabAccountLink)
	mux.HandleFunc("POST /api/account/code-host-accounts/gitlab/token", s.linkGitLabAccountToken)
	mux.HandleFunc("GET /api/account/code-host-accounts/oauth/callback", s.finishCodeHostAccountLink)
	mux.HandleFunc("DELETE /api/account/code-host-accounts/{id}", s.unlinkCodeHostAccount)
	mux.HandleFunc("GET /api/integrations/gitlab/{id}/oauth-application", s.getGitLabMemberOAuthApplication)
	mux.HandleFunc("PUT /api/integrations/gitlab/{id}/oauth-application", s.putGitLabMemberOAuthApplication)
	mux.HandleFunc("DELETE /api/integrations/gitlab/{id}/oauth-application", s.deleteGitLabMemberOAuthApplication)
}

// codeHostAccountTokens is the sealed token blob of an account.
type codeHostAccountTokens struct {
	AccessToken      string    `json:"accessToken"`
	RefreshToken     string    `json:"refreshToken,omitempty"`
	ExpiresAt        time.Time `json:"expiresAt,omitempty"`
	RefreshExpiresAt time.Time `json:"refreshExpiresAt,omitempty"`
	// PrivateToken: a GitLab personal access token (PRIVATE-TOKEN header).
	PrivateToken bool `json:"privateToken,omitempty"`
}

type codeHostAccountView struct {
	domain.CodeHostAccount
	// CanAct: a usable token is stored, so Flow can act as the member.
	CanAct bool `json:"canAct"`
}

type gitlabInstanceOption struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	BaseURL        string `json:"baseUrl"`
	HostName       string `json:"hostName"`
	OAuthAvailable bool   `json:"oauthAvailable"`
}

type githubLinkOption struct {
	Available bool   `json:"available"`
	Method    string `json:"method"`
	HostName  string `json:"hostName"`
}

type codeHostAccountsResponse struct {
	Accounts        []codeHostAccountView  `json:"accounts"`
	GitHub          githubLinkOption       `json:"github"`
	GitLabInstances []gitlabInstanceOption `json:"gitlabInstances"`
}

// ---------------------------------------------------------------- GitHub

// githubPersonalOAuth is how members link GitHub on this deployment.
type githubPersonalOAuth struct {
	Method       string // githubApp | oauth
	HostName     string
	APIURL       string
	AuthorizeURL string
	TokenURL     string
	ClientID     string
	ClientSecret string
	RedirectURI  string
	App          *githubhost.Host
}

func (s *server) githubPersonalOAuth() (githubPersonalOAuth, bool) {
	if host := s.gitHubAppHost(); host != nil && host.CanAuthorizeUsers() {
		return githubPersonalOAuth{Method: domain.CodeHostAuthGitHubApp, HostName: host.HostName(), APIURL: host.APIURL(), RedirectURI: gitHubAppSetupURL(), App: host}, true
	}
	cfg := integrationOAuthConfigFor("github", nil)
	if cfg.ClientID == "" || cfg.ClientSecret == "" {
		return githubPersonalOAuth{}, false
	}
	result := githubPersonalOAuth{Method: domain.CodeHostAuthOAuth, AuthorizeURL: cfg.AuthorizationURL, TokenURL: cfg.TokenURL, ClientID: cfg.ClientID, ClientSecret: cfg.ClientSecret, RedirectURI: codeHostLinkCallbackURL(),
		APIURL: strings.TrimRight(os.Getenv("FLOW_INTEGRATION_GITHUB_API_URL"), "/")}
	if result.AuthorizeURL == "" {
		result.AuthorizeURL = "https://github.com/login/oauth/authorize"
	}
	if result.TokenURL == "" {
		result.TokenURL = "https://github.com/login/oauth/access_token"
	}
	if result.APIURL == "" {
		result.APIURL = "https://api.github.com"
	}
	parsed, err := url.Parse(result.AuthorizeURL)
	if err != nil || parsed.Host == "" {
		return githubPersonalOAuth{}, false
	}
	result.HostName = strings.ToLower(parsed.Host)
	return result, true
}

// githubPersonalHostName is the GitHub host members' links are keyed by.
func (s *server) githubPersonalHostName() string {
	if config, ok := s.githubPersonalOAuth(); ok {
		return config.HostName
	}
	if host := s.gitHubAppHost(); host != nil && host.HostName() != "" {
		return host.HostName()
	}
	return "github.com"
}

// ---------------------------------------------------------------- GitLab

func gitlabMemberOAuthSecretID(workspace, connectionID string) string {
	return connectorSecretID(workspace, "gitlab-member-oauth", connectionID)
}

type gitlabMemberOAuthApp struct {
	ApplicationID string `json:"applicationId"`
	Secret        string `json:"secret"`
}

// gitlabInstances are the GitLab instances an admin connected to the
// workspace — the only hosts members may link.
func (s *server) gitlabInstances(ctx context.Context, workspaceKey string) []gitlabInstanceOption {
	if strings.TrimSpace(workspaceKey) == "" {
		return []gitlabInstanceOption{}
	}
	source := gitlabCodingSource{s: s}
	_, connections := source.workspaceConnections(workspaceKey)
	instances := []gitlabInstanceOption{}
	for _, connection := range connections {
		base := source.gitlabConnectionBaseURL(connection)
		parsed, err := url.Parse(base)
		if err != nil || parsed.Host == "" {
			continue
		}
		name := strings.TrimSpace(connection.Name)
		if name == "" {
			name = "GitLab"
		}
		instances = append(instances, gitlabInstanceOption{ID: connection.ID, Name: name, BaseURL: base, HostName: strings.ToLower(parsed.Host),
			OAuthAvailable: strings.TrimSpace(connection.Config["memberOAuthApplicationId"]) != "" && connection.Config["memberOAuthConfigured"] == "true"})
	}
	return instances
}

func (s *server) gitlabInstance(ctx context.Context, workspaceKey, id string) (gitlabInstanceOption, bool) {
	for _, instance := range s.gitlabInstances(ctx, workspaceKey) {
		if instance.ID == id {
			return instance, true
		}
	}
	return gitlabInstanceOption{}, false
}

func (s *server) gitlabMemberOAuthApp(ctx context.Context, workspace, connectionID string) (gitlabMemberOAuthApp, error) {
	raw, err := s.store.ReadConnectorSecret(ctx, gitlabMemberOAuthSecretID(workspace, connectionID), false)
	if err != nil {
		return gitlabMemberOAuthApp{}, errors.New("the GitLab OAuth application is not configured")
	}
	var app gitlabMemberOAuthApp
	if json.Unmarshal(raw, &app) != nil || app.ApplicationID == "" || app.Secret == "" {
		return gitlabMemberOAuthApp{}, errors.New("the GitLab OAuth application is not configured")
	}
	return app, nil
}

// gitlabMemberRequest calls the instance's API with a member token. base is
// always an admin-configured instance URL; the client is the integration's
// SSRF-hardened one.
func (s *server) gitlabMemberRequest(ctx context.Context, base string, tokens codeHostAccountTokens, method, path string, payload, out any) (int, error) {
	client, err := s.gitlabClient(ctx, base)
	if err != nil {
		return 0, err
	}
	return doCodeHostJSON(ctx, client, method, base+"/api/v4"+path, gitlabAuthHeader(tokens), payload, out)
}

func gitlabAuthHeader(tokens codeHostAccountTokens) http.Header {
	header := http.Header{}
	if tokens.PrivateToken {
		header.Set("PRIVATE-TOKEN", tokens.AccessToken)
	} else {
		header.Set("Authorization", "Bearer "+tokens.AccessToken)
	}
	return header
}

// codeHostHTTPError is a non-2xx host response (the body is never kept: it
// could echo a token).
type codeHostHTTPError struct{ Status int }

func (e *codeHostHTTPError) Error() string {
	return fmt.Sprintf("the code host returned HTTP %d", e.Status)
}

func codeHostStatus(err error) int {
	var httpErr *codeHostHTTPError
	if errors.As(err, &httpErr) {
		return httpErr.Status
	}
	return 0
}

// doCodeHostJSON performs one JSON (or form, for url.Values) request.
func doCodeHostJSON(ctx context.Context, client *http.Client, method, endpoint string, header http.Header, payload, out any) (int, error) {
	var body io.Reader
	contentType := ""
	switch value := payload.(type) {
	case nil:
	case url.Values:
		body, contentType = strings.NewReader(value.Encode()), "application/x-www-form-urlencoded"
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return 0, err
		}
		body, contentType = strings.NewReader(string(encoded)), "application/json"
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return 0, err
	}
	for name, values := range header {
		request.Header[name] = values
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "Flow")
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, fmt.Errorf("could not reach the code host: %w", err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return response.StatusCode, &codeHostHTTPError{Status: response.StatusCode}
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return response.StatusCode, errors.New("the code host returned an invalid response")
		}
	}
	return response.StatusCode, nil
}

// outboundClient reaches a deployment-configured endpoint (GitHub OAuth app
// endpoints and API): public HTTPS through the hardened dialer, loopback
// only in local development. The GitHub host the operator configured (the
// GitHub App's, or personal OAuth's) is reached like the GitHub App client
// reaches it, without the public-address check: resolvers that answer with
// synthetic addresses (fake-IP proxies, 198.18.0.0/15) would otherwise make
// members' requests fail and fall back to the workspace's credentials.
func (s *server) outboundClient(ctx context.Context, endpoint string) (*http.Client, error) {
	noRedirect := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if s.loopbackCodeHosts() && safeLocalDevelopmentURL(endpoint) {
		return &http.Client{Timeout: 15 * time.Second, CheckRedirect: noRedirect}, nil
	}
	if s.configuredGitHubEndpoint(endpoint) {
		return &http.Client{Timeout: 15 * time.Second, CheckRedirect: noRedirect, Transport: operatorCATransport()}, nil
	}
	if err := outboundEndpointError(ctx, endpoint, outboundTrustedPrivate); err != nil {
		return nil, errors.New("the code host endpoint is not allowed: " + err.Error())
	}
	client := secureOutboundClient(15*time.Second, outboundTrustedPrivate)
	client.CheckRedirect = noRedirect
	return client, nil
}

// configuredGitHubEndpoint reports whether endpoint is an HTTPS URL on a
// GitHub host from the deployment's configuration: the GitHub App's web or
// API host, or the personal OAuth app's authorize, token or API host.
func (s *server) configuredGitHubEndpoint(endpoint string) bool {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Hostname() == "" {
		return false
	}
	configured := []string{}
	if host := s.gitHubAppHost(); host != nil && host.Configured() {
		configured = append(configured, "https://"+host.HostName(), host.APIURL())
	}
	if config, ok := s.githubPersonalOAuth(); ok {
		configured = append(configured, "https://"+config.HostName, config.APIURL, config.AuthorizeURL, config.TokenURL)
	}
	for _, raw := range configured {
		if candidate, err := url.Parse(raw); err == nil && candidate.Scheme == "https" && strings.EqualFold(candidate.Host, parsed.Host) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- state

type codeHostLinkState struct {
	UserID       string `json:"u"`
	Workspace    string `json:"w"`
	CodeHost     string `json:"h"`
	Method       string `json:"m"`
	ConnectionID string `json:"c,omitempty"`
	BaseURL      string `json:"b,omitempty"`
	Verifier     string `json:"v"`
	ReturnTo     string `json:"r,omitempty"`
}

func codeHostLinkStateSecretID(state string) string {
	sum := sha256.Sum256([]byte(state))
	return "code-host-link-state:" + hex.EncodeToString(sum[:])
}

func codeHostRandomToken(size int) (string, error) {
	buffer := make([]byte, size)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// newCodeHostLinkState stores a one-time state (with the PKCE verifier)
// server side and returns the opaque state value and the challenge.
func (s *server) newCodeHostLinkState(ctx context.Context, state codeHostLinkState) (string, string, error) {
	value, err := codeHostRandomToken(32)
	if err != nil {
		return "", "", err
	}
	value = codeHostLinkStatePrefix + value
	if state.Verifier, err = codeHostRandomToken(48); err != nil {
		return "", "", err
	}
	raw, _ := json.Marshal(state)
	if err := s.store.PutConnectorSecret(ctx, codeHostLinkStateSecretID(value), raw, time.Now().Add(codeHostLinkStateTTL)); err != nil {
		return "", "", err
	}
	return value, pkceChallenge(state.Verifier), nil
}

// consumeCodeHostLinkState reads and deletes a state (one use only).
func (s *server) consumeCodeHostLinkState(ctx context.Context, value string) (codeHostLinkState, error) {
	if !strings.HasPrefix(value, codeHostLinkStatePrefix) || len(value) > 200 {
		return codeHostLinkState{}, errors.New("The link request is invalid. Start again from Flow.")
	}
	raw, err := s.store.ReadConnectorSecret(ctx, codeHostLinkStateSecretID(value), true)
	if err != nil {
		return codeHostLinkState{}, errors.New("The link request expired or was already used. Start again from Flow.")
	}
	var state codeHostLinkState
	if json.Unmarshal(raw, &state) != nil || state.UserID == "" || state.Verifier == "" {
		return codeHostLinkState{}, errors.New("The link request is invalid. Start again from Flow.")
	}
	return state, nil
}

func codeHostReturnTo(value string) string {
	if value == "integration" {
		return "integration"
	}
	return "account"
}

// codeHostLinkResult redirects browsers back to Flow (Connected accounts, or
// the integration page) and answers API clients with JSON.
func (s *server) codeHostLinkResult(w http.ResponseWriter, r *http.Request, state codeHostLinkState, status, message string) {
	if !wantsHTMLRedirect(r) {
		code := http.StatusOK
		if status == "error" {
			code = http.StatusBadRequest
		}
		writeJSON(w, code, map[string]string{"status": status, "error": message})
		return
	}
	target := appBaseURL() + "/"
	if state.Workspace != "" {
		target += url.PathEscape(state.Workspace) + "/settings/account/connections"
		if state.ReturnTo == "integration" && state.CodeHost != "" {
			target = appBaseURL() + "/" + url.PathEscape(state.Workspace) + "/settings/integrations/" + state.CodeHost
		}
	}
	values := url.Values{"codeHostLink": {status}}
	if message != "" {
		values.Set("error", message)
	}
	http.Redirect(w, r, target+"?"+values.Encode(), http.StatusFound)
}

// ---------------------------------------------------------------- handlers

func (s *server) codeHostAccountUser(w http.ResponseWriter, r *http.Request) (string, bool) {
	user := authUser(r)
	if user.ID == "" {
		writeError(w, http.StatusUnauthorized, "sign in to manage connected accounts")
		return "", false
	}
	if _, isAPIKey := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); isAPIKey {
		writeError(w, http.StatusForbidden, "connected accounts can only be managed by the signed-in member")
		return "", false
	}
	return user.ID, true
}

// listCodeHostAccounts — GET /api/account/code-host-accounts: the viewer's
// linked accounts and what they can link in the current workspace.
func (s *server) listCodeHostAccounts(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.codeHostAccountUser(w, r)
	if !ok {
		return
	}
	s.importLegacyGitHubLink(r.Context(), userID)
	accounts, err := s.store.ListCodeHostAccounts(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read connected accounts")
		return
	}
	response := codeHostAccountsResponse{Accounts: []codeHostAccountView{}, GitLabInstances: s.gitlabInstances(r.Context(), workspaceKey(r))}
	for _, account := range accounts {
		response.Accounts = append(response.Accounts, s.codeHostAccountView(r.Context(), account))
	}
	if config, ok := s.githubPersonalOAuth(); ok {
		response.GitHub = githubLinkOption{Available: true, Method: config.Method, HostName: config.HostName}
	} else {
		response.GitHub = githubLinkOption{HostName: s.githubPersonalHostName()}
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *server) codeHostAccountView(ctx context.Context, account domain.CodeHostAccount) codeHostAccountView {
	view := codeHostAccountView{CodeHostAccount: account}
	if view.Scopes == nil {
		view.Scopes = []string{}
	}
	if account.Status == domain.CodeHostAccountActive {
		if tokens, err := s.readCodeHostTokens(ctx, account.ID); err == nil && tokens.AccessToken != "" {
			view.CanAct = true
		}
	}
	return view
}

// importLegacyGitHubLink turns a GitHub App link made before tokens were
// kept (identity only) into an account row, so it shows as connected.
func (s *server) importLegacyGitHubLink(ctx context.Context, userID string) {
	host := s.gitHubAppHost()
	if host == nil || !host.Configured() {
		return
	}
	if _, err := s.store.CodeHostAccountFor(ctx, userID, domain.CodeHostGitHub, host.HostName()); err == nil {
		return
	}
	link, err := host.UserLink(ctx, userID)
	if err != nil {
		return
	}
	_, _ = s.store.UpsertCodeHostAccount(ctx, domain.CodeHostAccount{ID: newCodeHostAccountID(), UserID: userID, CodeHost: domain.CodeHostGitHub, HostName: host.HostName(), BaseURL: "https://" + host.HostName(),
		ExternalUserID: strconv.FormatInt(link.ID, 10), Login: link.Login, AuthType: domain.CodeHostAuthGitHubApp, Scopes: []string{}, Status: domain.CodeHostAccountActive, LinkedAt: link.LinkedAt})
}

func newCodeHostAccountID() string {
	token, err := codeHostRandomToken(12)
	if err != nil {
		return idgen.New("cha_")
	}
	return "cha_" + token
}

// startGitHubAccountLink — POST /api/account/code-host-accounts/github/authorize.
func (s *server) startGitHubAccountLink(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.codeHostAccountUser(w, r)
	if !ok {
		return
	}
	var input struct {
		ReturnTo string `json:"returnTo"`
	}
	if r.ContentLength != 0 && !decodeJSON(w, r, &input) {
		return
	}
	config, ok := s.githubPersonalOAuth()
	if !ok {
		writeError(w, http.StatusConflict, "GitHub account linking is not configured. An admin must set up the GitHub App's client ID and secret or a GitHub OAuth app.")
		return
	}
	if !store.ConnectorSecretsConfigured() {
		writeError(w, http.StatusServiceUnavailable, "Configure FLOW_CONNECTOR_SECRET_KEY before linking accounts")
		return
	}
	state, challenge, err := s.newCodeHostLinkState(r.Context(), codeHostLinkState{UserID: userID, Workspace: workspaceKey(r), CodeHost: domain.CodeHostGitHub, Method: config.Method, ReturnTo: codeHostReturnTo(input.ReturnTo)})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start GitHub authorization")
		return
	}
	if config.App != nil {
		writeJSON(w, http.StatusOK, map[string]string{"url": config.App.AuthorizeURLWithChallenge(state, config.RedirectURI, challenge)})
		return
	}
	values := url.Values{"client_id": {config.ClientID}, "redirect_uri": {config.RedirectURI}, "scope": {"repo read:user user:email"}, "state": {state}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}, "allow_signup": {"false"}}
	writeJSON(w, http.StatusOK, map[string]string{"url": config.AuthorizeURL + "?" + values.Encode()})
}

// startGitLabAccountLink — POST /api/account/code-host-accounts/gitlab/authorize.
func (s *server) startGitLabAccountLink(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.codeHostAccountUser(w, r)
	if !ok {
		return
	}
	var input struct {
		InstanceID string `json:"instanceId"`
		ReturnTo   string `json:"returnTo"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	workspace := workspaceKey(r)
	instance, found := s.gitlabInstance(r.Context(), workspace, strings.TrimSpace(input.InstanceID))
	if !found {
		writeError(w, http.StatusNotFound, "Choose a GitLab instance connected to this workspace")
		return
	}
	if !instance.OAuthAvailable {
		writeError(w, http.StatusConflict, "This GitLab instance has no OAuth application. Link it with a personal access token, or ask an admin to add one.")
		return
	}
	app, err := s.gitlabMemberOAuthApp(r.Context(), s.codeHostWorkspaceURLKey(workspace), instance.ID)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	state, challenge, err := s.newCodeHostLinkState(r.Context(), codeHostLinkState{UserID: userID, Workspace: workspace, CodeHost: domain.CodeHostGitLab, Method: domain.CodeHostAuthOAuth, ConnectionID: instance.ID, BaseURL: instance.BaseURL, ReturnTo: codeHostReturnTo(input.ReturnTo)})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start GitLab authorization")
		return
	}
	values := url.Values{"client_id": {app.ApplicationID}, "redirect_uri": {codeHostLinkCallbackURL()}, "response_type": {"code"}, "scope": {strings.Join(gitlabMemberScopes, " ")}, "state": {state}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}}
	writeJSON(w, http.StatusOK, map[string]string{"url": instance.BaseURL + "/oauth/authorize?" + values.Encode()})
}

// codeHostWorkspaceURLKey is the workspace's URL key (connector secrets are keyed by it).
func (s *server) codeHostWorkspaceURLKey(key string) string {
	if data, ok := s.store.WorkspaceMetadataFields(key, "integrationConnections"); ok && data.Workspace.URLKey != "" {
		return data.Workspace.URLKey
	}
	return key
}

// gitlabUser is GET /api/v4/user.
type gitlabUser struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	CommitEmail string `json:"commit_email"`
	State       string `json:"state"`
}

// linkGitLabAccountToken — POST /api/account/code-host-accounts/gitlab/token:
// links a GitLab account with a personal access token on an instance the
// workspace connected. The token must carry read_user and api.
func (s *server) linkGitLabAccountToken(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.codeHostAccountUser(w, r)
	if !ok {
		return
	}
	var input struct {
		InstanceID string `json:"instanceId"`
		Token      string `json:"token"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	token := strings.TrimSpace(input.Token)
	if token == "" || len(token) > 512 {
		writeError(w, http.StatusBadRequest, gitlabMissingTokenMessage)
		return
	}
	workspace := workspaceKey(r)
	instance, found := s.gitlabInstance(r.Context(), workspace, strings.TrimSpace(input.InstanceID))
	if !found {
		writeError(w, http.StatusNotFound, "Choose a GitLab instance connected to this workspace")
		return
	}
	if !store.ConnectorSecretsConfigured() {
		writeError(w, http.StatusServiceUnavailable, "Configure FLOW_CONNECTOR_SECRET_KEY before linking accounts")
		return
	}
	tokens := codeHostAccountTokens{AccessToken: token, PrivateToken: true}
	var info struct {
		Scopes    []string `json:"scopes"`
		ExpiresAt string   `json:"expires_at"`
		Active    *bool    `json:"active"`
		Revoked   bool     `json:"revoked"`
	}
	scopes := []string{}
	status, err := s.gitlabMemberRequest(r.Context(), instance.BaseURL, tokens, http.MethodGet, "/personal_access_tokens/self", nil, &info)
	switch {
	case err == nil:
		if info.Revoked || (info.Active != nil && !*info.Active) {
			writeError(w, http.StatusUnprocessableEntity, "The access token is inactive or revoked.")
			return
		}
		scopes = uniqueLower(info.Scopes)
		missing := []string{}
		for _, scope := range gitlabMemberScopes {
			if !slices.Contains(scopes, scope) {
				missing = append(missing, scope)
			}
		}
		if len(missing) > 0 {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "The access token is missing required scopes: " + strings.Join(missing, ", ") + ".", "missingScopes": missing})
			return
		}
		if expires := parseGitLabDate(info.ExpiresAt); expires != nil {
			if !expires.After(time.Now()) {
				writeError(w, http.StatusUnprocessableEntity, "The access token has expired.")
				return
			}
			tokens.ExpiresAt = *expires
		}
	case status == http.StatusUnauthorized:
		writeError(w, http.StatusUnprocessableEntity, "The access token is invalid, expired or revoked.")
		return
	case status == http.StatusNotFound:
		// GitLab before 15.5 has no self endpoint: scopes cannot be checked
		// up front; actions report missing permissions when they happen.
	default:
		writeError(w, http.StatusBadGateway, "Could not reach GitLab at "+instance.BaseURL+".")
		return
	}
	var user gitlabUser
	if status, err := s.gitlabMemberRequest(r.Context(), instance.BaseURL, tokens, http.MethodGet, "/user", nil, &user); err != nil || user.Username == "" {
		if status == http.StatusUnauthorized || status == http.StatusForbidden {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "The access token cannot read your GitLab user. It needs the read_user scope.", "missingScopes": []string{"read_user"}})
			return
		}
		writeError(w, http.StatusBadGateway, "Could not read your GitLab user from "+instance.BaseURL+".")
		return
	}
	account, err := s.saveCodeHostAccount(r.Context(), r, domain.CodeHostAccount{UserID: userID, CodeHost: domain.CodeHostGitLab, HostName: instance.HostName, BaseURL: instance.BaseURL, ExternalUserID: strconv.FormatInt(user.ID, 10),
		Login: user.Username, Email: user.CommitEmail, AuthType: domain.CodeHostAuthPAT, Scopes: scopes, WorkspaceKey: s.codeHostWorkspaceURLKey(workspace), ConnectionID: instance.ID}, tokens, workspace)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not save the linked GitLab account")
		return
	}
	writeJSON(w, http.StatusOK, s.codeHostAccountView(r.Context(), account))
}

// saveCodeHostAccount stores the identity and the sealed tokens and writes
// the audit entry.
func (s *server) saveCodeHostAccount(ctx context.Context, r *http.Request, account domain.CodeHostAccount, tokens codeHostAccountTokens, workspace string) (domain.CodeHostAccount, error) {
	account.ID = newCodeHostAccountID()
	account.Status, account.StatusReason = domain.CodeHostAccountActive, ""
	if !tokens.ExpiresAt.IsZero() {
		expires := tokens.ExpiresAt
		account.TokenExpiresAt = &expires
	}
	if account.Scopes == nil {
		account.Scopes = []string{}
	}
	saved, err := s.store.UpsertCodeHostAccount(ctx, account)
	if err != nil {
		return saved, err
	}
	if err := s.writeCodeHostTokens(ctx, saved.ID, tokens); err != nil {
		_, _ = s.store.DeleteCodeHostAccount(context.WithoutCancel(ctx), saved.UserID, saved.ID)
		return saved, err
	}
	if saved.CodeHost == domain.CodeHostGitHub {
		if host := s.gitHubAppHost(); host != nil && strings.EqualFold(host.HostName(), saved.HostName) {
			// The identity-only link CheckUserAccess still reads.
			id, _ := strconv.ParseInt(saved.ExternalUserID, 10, 64)
			raw, _ := json.Marshal(githubhost.UserLink{Login: saved.Login, ID: id, HostName: saved.HostName, LinkedAt: saved.LinkedAt})
			_ = s.store.PutConnectorSecret(ctx, githubhost.UserLinkSecretID(host.HostName(), saved.UserID), raw, time.Now().AddDate(10, 0, 0))
		}
	}
	s.auditCodeHostAccount(ctx, r, workspace, "code_host_account.linked", saved)
	return saved, nil
}

func (s *server) auditCodeHostAccount(ctx context.Context, r *http.Request, workspace, action string, account domain.CodeHostAccount) {
	if strings.TrimSpace(workspace) == "" {
		return
	}
	auditCtx := store.WithMetadataFields(context.WithoutCancel(ctx), "auditLog")
	if r != nil {
		auditCtx = store.WithMetadataFields(context.WithoutCancel(r.Context()), "auditLog")
	}
	metadata := map[string]any{"codeHost": account.CodeHost, "host": account.HostName, "login": account.Login, "authType": account.AuthType, "userId": account.UserID}
	if err := s.store.MutateWorkspace(auditCtx, workspace, "audit.code_host_account", account.ID, map[string]any{"action": action}, func(data *domain.Bootstrap) error {
		appendAudit(data, action, "code_host_account", account.ID, metadata)
		return nil
	}); err != nil {
		log.Printf("code host account audit workspace=%s action=%s: %v", workspace, action, err)
	}
}

func (s *server) writeCodeHostTokens(ctx context.Context, accountID string, tokens codeHostAccountTokens) error {
	raw, err := json.Marshal(tokens)
	if err != nil {
		return err
	}
	expires := time.Now().AddDate(5, 0, 0)
	if !tokens.RefreshExpiresAt.IsZero() {
		expires = tokens.RefreshExpiresAt.Add(24 * time.Hour)
	} else if tokens.RefreshToken == "" && !tokens.ExpiresAt.IsZero() {
		expires = tokens.ExpiresAt.Add(30 * 24 * time.Hour)
	}
	return s.store.PutConnectorSecret(ctx, store.CodeHostAccountSecretID(accountID), raw, expires)
}

func (s *server) readCodeHostTokens(ctx context.Context, accountID string) (codeHostAccountTokens, error) {
	raw, err := s.store.ReadConnectorSecret(ctx, store.CodeHostAccountSecretID(accountID), false)
	if err != nil {
		return codeHostAccountTokens{}, codehost.ErrUserNotLinked
	}
	var tokens codeHostAccountTokens
	if json.Unmarshal(raw, &tokens) != nil || tokens.AccessToken == "" {
		return codeHostAccountTokens{}, codehost.ErrUserNotLinked
	}
	return tokens, nil
}

// finishCodeHostAccountLink — GET /api/account/code-host-accounts/oauth/callback
// (and the GitHub App setup URL for App user authorization): consumes the
// one-time state, checks it belongs to the signed-in member, exchanges the
// code with the PKCE verifier, and stores the account.
func (s *server) finishCodeHostAccountLink(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	state, err := s.consumeCodeHostLinkState(r.Context(), query.Get("state"))
	if err != nil {
		s.codeHostLinkResult(w, r, codeHostLinkState{}, "error", err.Error())
		return
	}
	user := authUser(r)
	if user.ID == "" || user.ID != state.UserID {
		s.codeHostLinkResult(w, r, state, "error", "This account link was started by another user")
		return
	}
	if state.Workspace != "" {
		if err := s.checkGitHubAppMember(r.Context(), state.Workspace, state.UserID, false); err != nil {
			s.codeHostLinkResult(w, r, state, "error", err.Error())
			return
		}
	}
	if denied := query.Get("error"); denied != "" {
		s.codeHostLinkResult(w, r, state, "error", "Authorization was not granted")
		return
	}
	code := strings.TrimSpace(query.Get("code"))
	if code == "" || len(code) > 1024 {
		s.codeHostLinkResult(w, r, state, "error", "The code host did not return an authorization code")
		return
	}
	var account domain.CodeHostAccount
	var tokens codeHostAccountTokens
	switch state.CodeHost {
	case domain.CodeHostGitHub:
		account, tokens, err = s.exchangeGitHubAccountCode(r.Context(), state, code)
	case domain.CodeHostGitLab:
		account, tokens, err = s.exchangeGitLabAccountCode(r.Context(), state, code)
	default:
		err = errors.New("unknown code host")
	}
	if err != nil {
		log.Printf("code host account link user=%s host=%s: %v", state.UserID, state.CodeHost, err)
		s.codeHostLinkResult(w, r, state, "error", "Linking the account failed. Try again.")
		return
	}
	account.UserID = state.UserID
	if _, err := s.saveCodeHostAccount(r.Context(), r, account, tokens, state.Workspace); err != nil {
		s.codeHostLinkResult(w, r, state, "error", "Could not save the linked account")
		return
	}
	s.codeHostLinkResult(w, r, state, "linked", "")
}

func (s *server) exchangeGitHubAccountCode(ctx context.Context, state codeHostLinkState, code string) (domain.CodeHostAccount, codeHostAccountTokens, error) {
	config, ok := s.githubPersonalOAuth()
	if !ok || config.Method != state.Method {
		return domain.CodeHostAccount{}, codeHostAccountTokens{}, errors.New("GitHub account linking is no longer configured")
	}
	account := domain.CodeHostAccount{CodeHost: domain.CodeHostGitHub, HostName: config.HostName, BaseURL: "https://" + config.HostName, AuthType: config.Method, Scopes: []string{}}
	if config.App != nil {
		link, token, err := config.App.ExchangeUserToken(ctx, code, config.RedirectURI, state.Verifier)
		if err != nil {
			return account, codeHostAccountTokens{}, err
		}
		account.Login, account.ExternalUserID = link.Login, strconv.FormatInt(link.ID, 10)
		return account, codeHostAccountTokens{AccessToken: token.AccessToken, RefreshToken: token.RefreshToken, ExpiresAt: token.ExpiresAt, RefreshExpiresAt: token.RefreshExpiresAt}, nil
	}
	client, err := s.outboundClient(ctx, config.TokenURL)
	if err != nil {
		return account, codeHostAccountTokens{}, err
	}
	var token struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
		Scope        string `json:"scope"`
		Error        string `json:"error"`
	}
	values := url.Values{"client_id": {config.ClientID}, "client_secret": {config.ClientSecret}, "code": {code}, "redirect_uri": {config.RedirectURI}, "code_verifier": {state.Verifier}}
	if _, err := doCodeHostJSON(ctx, client, http.MethodPost, config.TokenURL, nil, values, &token); err != nil {
		return account, codeHostAccountTokens{}, err
	}
	if token.AccessToken == "" {
		return account, codeHostAccountTokens{}, fmt.Errorf("github: authorization failed: %s", token.Error)
	}
	tokens := codeHostAccountTokens{AccessToken: token.AccessToken, RefreshToken: token.RefreshToken}
	if token.ExpiresIn > 0 {
		tokens.ExpiresAt = time.Now().UTC().Add(time.Duration(token.ExpiresIn) * time.Second)
	}
	account.Scopes = strings.FieldsFunc(token.Scope, func(r rune) bool { return r == ',' || r == ' ' })
	apiClient, err := s.outboundClient(ctx, config.APIURL)
	if err != nil {
		return account, codeHostAccountTokens{}, err
	}
	var user struct {
		Login string `json:"login"`
		ID    int64  `json:"id"`
	}
	header := http.Header{"Authorization": {"Bearer " + tokens.AccessToken}}
	if _, err := doCodeHostJSON(ctx, apiClient, http.MethodGet, config.APIURL+"/user", header, nil, &user); err != nil || user.Login == "" {
		return account, codeHostAccountTokens{}, errors.New("github: could not read the authorized user")
	}
	account.Login, account.ExternalUserID = user.Login, strconv.FormatInt(user.ID, 10)
	return account, tokens, nil
}

type gitlabOAuthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
}

func (s *server) exchangeGitLabAccountCode(ctx context.Context, state codeHostLinkState, code string) (domain.CodeHostAccount, codeHostAccountTokens, error) {
	instance, found := s.gitlabInstance(ctx, state.Workspace, state.ConnectionID)
	if !found || instance.BaseURL != state.BaseURL {
		return domain.CodeHostAccount{}, codeHostAccountTokens{}, errors.New("the GitLab instance is no longer connected")
	}
	workspace := s.codeHostWorkspaceURLKey(state.Workspace)
	app, err := s.gitlabMemberOAuthApp(ctx, workspace, instance.ID)
	if err != nil {
		return domain.CodeHostAccount{}, codeHostAccountTokens{}, err
	}
	client, err := s.gitlabClient(ctx, instance.BaseURL)
	if err != nil {
		return domain.CodeHostAccount{}, codeHostAccountTokens{}, err
	}
	var token gitlabOAuthTokenResponse
	values := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {codeHostLinkCallbackURL()}, "client_id": {app.ApplicationID}, "client_secret": {app.Secret}, "code_verifier": {state.Verifier}}
	if _, err := doCodeHostJSON(ctx, client, http.MethodPost, instance.BaseURL+"/oauth/token", nil, values, &token); err != nil {
		return domain.CodeHostAccount{}, codeHostAccountTokens{}, err
	}
	if token.AccessToken == "" {
		return domain.CodeHostAccount{}, codeHostAccountTokens{}, errors.New("gitlab: no access token")
	}
	tokens := codeHostAccountTokens{AccessToken: token.AccessToken, RefreshToken: token.RefreshToken}
	if token.ExpiresIn > 0 {
		tokens.ExpiresAt = time.Now().UTC().Add(time.Duration(token.ExpiresIn) * time.Second)
	}
	var user gitlabUser
	if _, err := s.gitlabMemberRequest(ctx, instance.BaseURL, tokens, http.MethodGet, "/user", nil, &user); err != nil || user.Username == "" {
		return domain.CodeHostAccount{}, codeHostAccountTokens{}, errors.New("gitlab: could not read the authorized user")
	}
	scopes := strings.Fields(token.Scope)
	if len(scopes) == 0 {
		scopes = slices.Clone(gitlabMemberScopes)
	}
	return domain.CodeHostAccount{CodeHost: domain.CodeHostGitLab, HostName: instance.HostName, BaseURL: instance.BaseURL, ExternalUserID: strconv.FormatInt(user.ID, 10), Login: user.Username, Email: user.CommitEmail,
		AuthType: domain.CodeHostAuthOAuth, Scopes: scopes, WorkspaceKey: workspace, ConnectionID: instance.ID}, tokens, nil
}

// unlinkCodeHostAccount — DELETE /api/account/code-host-accounts/{id}.
func (s *server) unlinkCodeHostAccount(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.codeHostAccountUser(w, r)
	if !ok {
		return
	}
	account, err := s.store.CodeHostAccount(r.Context(), userID, strings.TrimSpace(r.PathValue("id")))
	if errors.Is(err, store.ErrCodeHostAccountNotFound) {
		writeError(w, http.StatusNotFound, "resource not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read the connected account")
		return
	}
	s.revokeGitLabMemberToken(r.Context(), account)
	if _, err := s.store.DeleteCodeHostAccount(r.Context(), userID, account.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "could not unlink the account")
		return
	}
	if account.CodeHost == domain.CodeHostGitHub {
		if host := s.gitHubAppHost(); host != nil && strings.EqualFold(host.HostName(), account.HostName) {
			_ = s.store.DeleteConnectorSecret(r.Context(), githubhost.UserLinkSecretID(host.HostName(), userID))
		}
	}
	s.auditCodeHostAccount(r.Context(), r, workspaceKey(r), "code_host_account.unlinked", account)
	w.WriteHeader(http.StatusNoContent)
}

// revokeGitLabMemberToken revokes an OAuth token Flow obtained (best
// effort). A personal access token belongs to the member and is left alone.
func (s *server) revokeGitLabMemberToken(ctx context.Context, account domain.CodeHostAccount) {
	if account.CodeHost != domain.CodeHostGitLab || account.AuthType != domain.CodeHostAuthOAuth {
		return
	}
	tokens, err := s.readCodeHostTokens(ctx, account.ID)
	if err != nil {
		return
	}
	app, err := s.gitlabMemberOAuthApp(ctx, account.WorkspaceKey, account.ConnectionID)
	if err != nil {
		return
	}
	client, err := s.gitlabClient(ctx, account.BaseURL)
	if err != nil {
		return
	}
	_, _ = doCodeHostJSON(ctx, client, http.MethodPost, account.BaseURL+"/oauth/revoke", nil, url.Values{"client_id": {app.ApplicationID}, "client_secret": {app.Secret}, "token": {tokens.AccessToken}}, nil)
}

// ---------------------------------------------------------------- admin

type gitlabMemberOAuthApplicationView struct {
	Configured    bool     `json:"configured"`
	ApplicationID string   `json:"applicationId"`
	RedirectURL   string   `json:"redirectUrl"`
	Scopes        []string `json:"scopes"`
}

func (s *server) gitlabMemberOAuthView(connection *domain.IntegrationConnection) gitlabMemberOAuthApplicationView {
	view := gitlabMemberOAuthApplicationView{RedirectURL: codeHostLinkCallbackURL(), Scopes: slices.Clone(gitlabMemberScopes)}
	if connection != nil {
		view.ApplicationID = connection.Config["memberOAuthApplicationId"]
		view.Configured = view.ApplicationID != "" && connection.Config["memberOAuthConfigured"] == "true"
	}
	return view
}

// getGitLabMemberOAuthApplication — GET /api/integrations/gitlab/{id}/oauth-application.
// The secret is never returned.
func (s *server) getGitLabMemberOAuthApplication(w http.ResponseWriter, r *http.Request) {
	_, connection := s.gitlabConnectionSnapshot(r, strings.TrimSpace(r.PathValue("id")))
	if connection == nil {
		writeError(w, http.StatusNotFound, "resource not found")
		return
	}
	writeJSON(w, http.StatusOK, s.gitlabMemberOAuthView(connection))
}

// putGitLabMemberOAuthApplication — PUT /api/integrations/gitlab/{id}/oauth-application
// (admins): the Application ID and Secret of an OAuth application registered
// on that GitLab instance with the callback URL, scopes api and read_user.
func (s *server) putGitLabMemberOAuthApplication(w http.ResponseWriter, r *http.Request) {
	if !s.requireWorkspaceAdmin(w, r) {
		return
	}
	var input struct {
		ApplicationID string `json:"applicationId"`
		Secret        string `json:"secret"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.ApplicationID, input.Secret = strings.TrimSpace(input.ApplicationID), strings.TrimSpace(input.Secret)
	if input.ApplicationID == "" || input.Secret == "" || len(input.ApplicationID) > 255 || len(input.Secret) > 512 {
		writeError(w, http.StatusBadRequest, "Enter the application ID and secret")
		return
	}
	if !store.ConnectorSecretsConfigured() {
		writeError(w, http.StatusServiceUnavailable, "Configure FLOW_CONNECTOR_SECRET_KEY before saving the OAuth application")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	data, connection := s.gitlabConnectionSnapshot(r, id)
	if connection == nil {
		writeError(w, http.StatusNotFound, "resource not found")
		return
	}
	raw, _ := json.Marshal(gitlabMemberOAuthApp(input))
	if err := s.store.PutConnectorSecret(r.Context(), gitlabMemberOAuthSecretID(data.Workspace.URLKey, id), raw, time.Now().AddDate(20, 0, 0)); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not store the OAuth application")
		return
	}
	updated, err := s.saveGitLabConnection(r.Context(), workspaceKey(r), id, "integration.member_oauth_configured", func(connection *domain.IntegrationConnection, now time.Time) {
		connection.Config["memberOAuthApplicationId"] = input.ApplicationID
		connection.Config["memberOAuthConfigured"] = "true"
	})
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	writeJSON(w, http.StatusOK, s.gitlabMemberOAuthView(&updated))
}

// deleteGitLabMemberOAuthApplication — DELETE /api/integrations/gitlab/{id}/oauth-application (admins).
func (s *server) deleteGitLabMemberOAuthApplication(w http.ResponseWriter, r *http.Request) {
	if !s.requireWorkspaceAdmin(w, r) {
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	data, connection := s.gitlabConnectionSnapshot(r, id)
	if connection == nil {
		writeError(w, http.StatusNotFound, "resource not found")
		return
	}
	_ = s.store.DeleteConnectorSecret(r.Context(), gitlabMemberOAuthSecretID(data.Workspace.URLKey, id))
	_, err := s.saveGitLabConnection(r.Context(), workspaceKey(r), id, "integration.member_oauth_removed", func(connection *domain.IntegrationConnection, now time.Time) {
		delete(connection.Config, "memberOAuthApplicationId")
		delete(connection.Config, "memberOAuthConfigured")
	})
	respondMutation(w, err, http.StatusNoContent, nil)
}

// ---------------------------------------------------------------- using links

// Refresh coordination across API replicas. GitHub and GitLab rotate the
// refresh token on use: two replicas refreshing the same account at once
// would each spend the same refresh token, and the loser's rejected refresh
// would force the member to reconnect. A refresh therefore first takes the
// account row's refresh lease (store.ClaimCodeHostAccountRefresh, a
// compare-and-swap the database serializes). The replica that wins refreshes
// and stores the new tokens before releasing; the others poll the stored
// tokens and return the fresh ones instead of refreshing again. A lease left
// by a crashed replica expires after codeHostRefreshLease.
var (
	codeHostRefreshLease     = 45 * time.Second
	codeHostRefreshWait      = 30 * time.Second
	codeHostRefreshPollEvery = 150 * time.Millisecond
)

func codeHostTokensFresh(tokens codeHostAccountTokens) bool {
	return tokens.ExpiresAt.IsZero() || time.Until(tokens.ExpiresAt) > codeHostTokenRefreshMargin
}

// memberHostTokens returns the account's tokens, refreshed when they expire
// soon. A revoked or expired grant marks the account "reconnect" and returns
// codehost.ErrUserNotLinked. The account must be the acting member's own.
func (s *server) memberHostTokens(ctx context.Context, account domain.CodeHostAccount) (codeHostAccountTokens, error) {
	if account.Status != domain.CodeHostAccountActive {
		return codeHostAccountTokens{}, codehost.ErrUserNotLinked
	}
	tokens, err := s.readCodeHostTokens(ctx, account.ID)
	if err != nil {
		return tokens, err
	}
	if codeHostTokensFresh(tokens) {
		return tokens, nil
	}
	if tokens.RefreshToken == "" {
		s.markCodeHostAccountReconnect(ctx, account, "The token expired. Reconnect the account.")
		return codeHostAccountTokens{}, codehost.ErrUserNotLinked
	}
	owner, err := codeHostRandomToken(18)
	if err != nil {
		return codeHostAccountTokens{}, err
	}
	deadline := time.Now().Add(codeHostRefreshWait)
	for {
		claimed, err := s.store.ClaimCodeHostAccountRefresh(ctx, account.ID, owner, codeHostRefreshLease)
		if err != nil {
			return codeHostAccountTokens{}, fmt.Errorf("%w: refresh lease: %v", codehost.ErrTransient, err)
		}
		if claimed {
			break
		}
		// Another replica (or request) is refreshing: wait for its tokens.
		if !time.Now().Before(deadline) {
			return codeHostAccountTokens{}, fmt.Errorf("%w: the account's token refresh is still in progress", codehost.ErrTransient)
		}
		select {
		case <-ctx.Done():
			return codeHostAccountTokens{}, ctx.Err()
		case <-time.After(codeHostRefreshPollEvery):
		}
		if current, err := s.readCodeHostTokens(ctx, account.ID); err != nil {
			return codeHostAccountTokens{}, err
		} else if codeHostTokensFresh(current) {
			return current, nil
		}
	}
	defer func() {
		if err := s.store.ReleaseCodeHostAccountRefresh(context.WithoutCancel(ctx), account.ID, owner); err != nil {
			log.Printf("code host account %s: release refresh lease: %v", account.ID, err)
		}
	}()
	// Re-read under the lease: another replica may have refreshed (and so
	// spent the refresh token read above) between that read and the claim.
	if current, err := s.store.CodeHostAccount(ctx, account.UserID, account.ID); err != nil || current.Status != domain.CodeHostAccountActive {
		return codeHostAccountTokens{}, codehost.ErrUserNotLinked // unlinked or marked reconnect meanwhile
	}
	if tokens, err = s.readCodeHostTokens(ctx, account.ID); err != nil {
		return tokens, err
	}
	if codeHostTokensFresh(tokens) {
		return tokens, nil
	}
	refreshed, err := s.refreshMemberHostTokens(ctx, account, tokens)
	if err != nil {
		if errors.Is(err, codehost.ErrUserNotLinked) || codeHostStatus(err) == http.StatusBadRequest || codeHostStatus(err) == http.StatusUnauthorized {
			s.markCodeHostAccountReconnect(ctx, account, "The authorization was revoked or expired. Reconnect the account.")
			return codeHostAccountTokens{}, codehost.ErrUserNotLinked
		}
		return codeHostAccountTokens{}, fmt.Errorf("%w: refresh: %v", codehost.ErrTransient, err)
	}
	if err := s.writeCodeHostTokens(ctx, account.ID, refreshed); err != nil {
		return codeHostAccountTokens{}, err
	}
	expires := refreshed.ExpiresAt
	_ = s.store.SetCodeHostAccountStatus(ctx, account.ID, domain.CodeHostAccountActive, "", &expires)
	return refreshed, nil
}

func (s *server) refreshMemberHostTokens(ctx context.Context, account domain.CodeHostAccount, tokens codeHostAccountTokens) (codeHostAccountTokens, error) {
	switch {
	case account.CodeHost == domain.CodeHostGitHub && account.AuthType == domain.CodeHostAuthGitHubApp:
		host := s.gitHubAppHost()
		if host == nil {
			return tokens, codehost.ErrUserNotLinked
		}
		token, err := host.RefreshUserToken(ctx, tokens.RefreshToken)
		if err != nil {
			return tokens, err
		}
		return codeHostAccountTokens{AccessToken: token.AccessToken, RefreshToken: firstNonEmpty(token.RefreshToken, tokens.RefreshToken), ExpiresAt: token.ExpiresAt, RefreshExpiresAt: token.RefreshExpiresAt}, nil
	case account.CodeHost == domain.CodeHostGitLab && account.AuthType == domain.CodeHostAuthOAuth:
		app, err := s.gitlabMemberOAuthApp(ctx, account.WorkspaceKey, account.ConnectionID)
		if err != nil {
			return tokens, codehost.ErrUserNotLinked
		}
		client, err := s.gitlabClient(ctx, account.BaseURL)
		if err != nil {
			return tokens, err
		}
		var out gitlabOAuthTokenResponse
		values := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tokens.RefreshToken}, "client_id": {app.ApplicationID}, "client_secret": {app.Secret}, "redirect_uri": {codeHostLinkCallbackURL()}}
		if _, err := doCodeHostJSON(ctx, client, http.MethodPost, account.BaseURL+"/oauth/token", nil, values, &out); err != nil {
			return tokens, err
		}
		if out.AccessToken == "" {
			return tokens, codehost.ErrUserNotLinked
		}
		refreshed := codeHostAccountTokens{AccessToken: out.AccessToken, RefreshToken: firstNonEmpty(out.RefreshToken, tokens.RefreshToken)}
		if out.ExpiresIn > 0 {
			refreshed.ExpiresAt = time.Now().UTC().Add(time.Duration(out.ExpiresIn) * time.Second)
		}
		return refreshed, nil
	}
	return tokens, codehost.ErrUserNotLinked
}

func (s *server) markCodeHostAccountReconnect(ctx context.Context, account domain.CodeHostAccount, reason string) {
	if err := s.store.SetCodeHostAccountStatus(context.WithoutCancel(ctx), account.ID, domain.CodeHostAccountReconnect, reason, account.TokenExpiresAt); err != nil {
		log.Printf("code host account %s: mark reconnect: %v", account.ID, err)
	}
}

// workspaceMemberAccount is a member's active account usable in a workspace:
// GitHub on the deployment's GitHub host, GitLab only on an instance the
// workspace connected (matched by host and base URL).
func (s *server) workspaceMemberAccount(ctx context.Context, workspace, userID, codeHost, hostName string) (domain.CodeHostAccount, gitlabInstanceOption, bool) {
	if userID == "" {
		return domain.CodeHostAccount{}, gitlabInstanceOption{}, false
	}
	hostName = strings.ToLower(strings.TrimSpace(hostName))
	switch codeHost {
	case domain.CodeHostGitHub:
		if hostName == "" {
			hostName = s.githubPersonalHostName()
		}
		account, err := s.store.CodeHostAccountFor(ctx, userID, domain.CodeHostGitHub, hostName)
		if err != nil || account.Status != domain.CodeHostAccountActive {
			return domain.CodeHostAccount{}, gitlabInstanceOption{}, false
		}
		return account, gitlabInstanceOption{}, true
	case domain.CodeHostGitLab:
		for _, instance := range s.gitlabInstances(ctx, workspace) {
			if hostName != "" && instance.HostName != hostName {
				continue
			}
			account, err := s.store.CodeHostAccountFor(ctx, userID, domain.CodeHostGitLab, instance.HostName)
			if err == nil && account.Status == domain.CodeHostAccountActive && account.BaseURL == instance.BaseURL {
				return account, instance, true
			}
		}
	}
	return domain.CodeHostAccount{}, gitlabInstanceOption{}, false
}

// codeHostMemberLogins maps lowercased host logins of the workspace's
// members who linked an account on the host to their Flow user, for
// attribution of webhook and API activity.
func (s *server) codeHostMemberLogins(ctx context.Context, data *domain.Bootstrap, codeHost, hostName string, logins ...string) map[string]domain.User {
	result := map[string]domain.User{}
	hostName = strings.ToLower(strings.TrimSpace(hostName))
	if hostName == "" {
		switch codeHost {
		case domain.CodeHostGitHub:
			hostName = s.githubPersonalHostName()
		case domain.CodeHostGitLab:
			if instances := s.gitlabInstances(ctx, data.Workspace.URLKey); len(instances) > 0 {
				hostName = instances[0].HostName
			}
		}
	}
	if hostName == "" || len(logins) == 0 {
		return result
	}
	accounts, err := s.store.CodeHostAccountsByLogin(ctx, codeHost, hostName, logins)
	if err != nil {
		return result
	}
	members := map[string]domain.User{}
	for _, user := range data.Users {
		members[user.ID] = user
	}
	for _, member := range data.Members {
		if member.Status == "" || member.Status == "active" {
			members[member.User.ID] = member.User
		}
	}
	for _, account := range accounts {
		user, ok := members[account.UserID]
		if !ok || user.App {
			continue // only members of this workspace
		}
		key := strings.ToLower(account.Login)
		if _, taken := result[key]; !taken {
			result[key] = user
		}
	}
	return result
}
