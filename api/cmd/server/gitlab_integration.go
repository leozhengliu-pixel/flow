package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// GitLab token connections. An administrator pastes a personal, group or
// project access token (and optionally a self-hosted URL). The token is
// validated against the GitLab REST API, its scopes decide whether Flow may
// write linkbacks (api) or only read (read_api), and the token itself lives
// only in the encrypted connector secret store — never in workspace state,
// events, logs or API responses. Tokens carrying the api or self_rotate scope
// can be renewed through GitLab's self-rotation endpoint, manually or on a
// schedule before they expire.

const (
	gitlabDefaultURL          = "https://gitlab.com"
	gitlabRotationLifetime    = 30 * 24 * time.Hour
	gitlabRotationLeadTime    = 7 * 24 * time.Hour
	gitlabResponseBodyLimit   = 64 << 10
	gitlabErrorBodyLimit      = 2 << 10
	gitlabInvalidURLMessage   = "Please enter a valid HTTPS URL."
	gitlabMissingTokenMessage = "Please enter a valid access token."
)

// gitlabSettingKeys are user-facing settings that survive a reconnect or a
// token replacement.
var gitlabSettingKeys = []string{"branchFormat", "privateLinkbacks", "publicLinkbacks", "includeDescriptions", "rotationEnabled"}

type gitlabCredential struct {
	Token   string `json:"token"`
	URL     string `json:"url"`
	TokenID int64  `json:"tokenId,omitempty"`
}

type gitlabTokenProbe struct {
	URL           string
	Username      string
	Bot           bool
	Scopes        []string
	Readonly      bool
	CanSelfRotate bool
	ExpiresAt     *time.Time
	TokenID       int64
	TokenName     string
}

// gitlabAPIError carries a user-presentable message plus the request line and
// a truncated response body for the "Response details" disclosure. Neither
// ever contains the access token.
type gitlabAPIError struct {
	Message string
	Status  int
	Request string
	Body    string
	Headers string
}

func (e *gitlabAPIError) Error() string { return e.Message }

func (e *gitlabAPIError) details() map[string]string {
	details := map[string]string{}
	if e.Request != "" {
		details["errorRequest"] = e.Request
	}
	if e.Body != "" {
		details["errorResponseBody"] = e.Body
	}
	if e.Headers != "" {
		details["errorResponseHeaders"] = e.Headers
	}
	return details
}

func writeGitLabError(w http.ResponseWriter, err error) {
	var apiErr *gitlabAPIError
	if errors.As(err, &apiErr) {
		status := http.StatusBadGateway
		if apiErr.Status == http.StatusUnauthorized || apiErr.Status == http.StatusForbidden || apiErr.Status == http.StatusUnprocessableEntity {
			status = http.StatusUnprocessableEntity
		}
		payload := map[string]any{"error": apiErr.Message}
		if details := apiErr.details(); len(details) > 0 {
			payload["current"] = details
		}
		writeJSON(w, status, payload)
		return
	}
	writeError(w, http.StatusBadGateway, err.Error())
}

// normalizeGitLabURL validates the optional self-hosted URL. Empty means
// gitlab.com. Only HTTPS is accepted, except loopback URLs in local
// development so a fake GitLab can stand in.
func normalizeGitLabURL(raw string, allowLocal bool) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return gitlabDefaultURL, nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New(gitlabInvalidURLMessage)
	}
	if u.Scheme != "https" && !(allowLocal && u.Scheme == "http" && safeLocalDevelopmentURL(raw)) {
		return "", errors.New(gitlabInvalidURLMessage)
	}
	path := strings.TrimRight(u.EscapedPath(), "/")
	path = strings.TrimSuffix(path, "/api/v4")
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host) + path, nil
}

// gitlabClient returns an HTTP client that may reach base. Public HTTPS hosts
// go through the SSRF-hardened dialer; a host the deployment configured in
// FLOW_INTEGRATION_GITLAB_HOST is trusted even on a private network, and
// loopback hosts are allowed only in local development.
func (s *server) gitlabClient(ctx context.Context, base string) (*http.Client, error) {
	noRedirect := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if s.authDisabled && safeLocalDevelopmentURL(base) {
		return &http.Client{Timeout: 10 * time.Second, CheckRedirect: noRedirect}, nil
	}
	if trusted, err := normalizeGitLabURL(os.Getenv("FLOW_INTEGRATION_GITLAB_HOST"), false); err == nil && os.Getenv("FLOW_INTEGRATION_GITLAB_HOST") != "" && trusted == base {
		return &http.Client{Timeout: 10 * time.Second, CheckRedirect: noRedirect}, nil
	}
	if !safeOutboundHTTPS(ctx, base) {
		return nil, &gitlabAPIError{Message: "The GitLab URL must be a public HTTPS address."}
	}
	client := secureOutboundClient(10 * time.Second)
	client.CheckRedirect = noRedirect
	return client, nil
}

func (s *server) gitlabRequest(ctx context.Context, base, token, method, path string, payload any, out any) error {
	client, err := s.gitlabClient(ctx, base)
	if err != nil {
		return err
	}
	endpoint := base + "/api/v4" + path
	requestLine := method + " " + endpoint
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return &gitlabAPIError{Message: gitlabInvalidURLMessage}
	}
	request.Header.Set("PRIVATE-TOKEN", token)
	request.Header.Set("Accept", "application/json")
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		return &gitlabAPIError{Message: "Could not reach GitLab at " + base + ".", Request: requestLine}
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(response.Body, gitlabResponseBodyLimit))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message := fmt.Sprintf("GitLab returned HTTP %d.", response.StatusCode)
		switch {
		case response.StatusCode == http.StatusUnauthorized:
			message = "The access token is invalid, expired or revoked."
		case response.StatusCode == http.StatusForbidden:
			message = "The access token does not have permission for this request."
		case response.StatusCode == http.StatusNotFound:
			message = "Could not find the GitLab API at this URL. Check the custom GitLab URL."
		case response.StatusCode >= 300 && response.StatusCode < 400:
			message = "GitLab redirected the request. Check the custom GitLab URL."
		}
		snippet := string(raw)
		if len(snippet) > gitlabErrorBodyLimit {
			snippet = snippet[:gitlabErrorBodyLimit]
		}
		if token != "" {
			snippet = strings.ReplaceAll(snippet, token, "[redacted]")
		}
		headers := []string{}
		for _, name := range []string{"Content-Type", "WWW-Authenticate", "X-Request-Id"} {
			if value := response.Header.Get(name); value != "" {
				headers = append(headers, name+": "+value)
			}
		}
		return &gitlabAPIError{Message: message, Status: response.StatusCode, Request: requestLine, Body: snippet, Headers: strings.Join(headers, "\n")}
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return &gitlabAPIError{Message: "GitLab returned an invalid response. Check the custom GitLab URL.", Status: response.StatusCode, Request: requestLine}
		}
	}
	return nil
}

func parseGitLabDate(value string) *time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	for _, layout := range []string{"2006-01-02", time.RFC3339} {
		if parsed, err := time.Parse(layout, value); err == nil {
			parsed = parsed.UTC()
			return &parsed
		}
	}
	return nil
}

// probeGitLabToken checks the token against /personal_access_tokens/self
// (scopes, expiry, active state) and /user (identity). Older GitLab servers
// without the self endpoint fall back to /user with unknown scopes.
func (s *server) probeGitLabToken(ctx context.Context, base, token string) (gitlabTokenProbe, error) {
	probe := gitlabTokenProbe{URL: base}
	var info struct {
		ID        int64    `json:"id"`
		Name      string   `json:"name"`
		Scopes    []string `json:"scopes"`
		ExpiresAt string   `json:"expires_at"`
		Active    *bool    `json:"active"`
		Revoked   bool     `json:"revoked"`
	}
	selfErr := s.gitlabRequest(ctx, base, token, http.MethodGet, "/personal_access_tokens/self", nil, &info)
	var apiErr *gitlabAPIError
	if selfErr != nil && !(errors.As(selfErr, &apiErr) && apiErr.Status == http.StatusNotFound) {
		return probe, selfErr
	}
	if selfErr == nil {
		if info.Revoked || (info.Active != nil && !*info.Active) {
			return probe, &gitlabAPIError{Message: "The access token is inactive or revoked.", Status: http.StatusUnauthorized}
		}
		probe.Scopes = uniqueLower(info.Scopes)
		probe.TokenID, probe.TokenName, probe.ExpiresAt = info.ID, info.Name, parseGitLabDate(info.ExpiresAt)
		hasAPI, hasRead := slices.Contains(probe.Scopes, "api"), slices.Contains(probe.Scopes, "read_api")
		if !hasAPI && !hasRead {
			return probe, &gitlabAPIError{Message: "The access token requires the api or read_api scope.", Status: http.StatusUnprocessableEntity}
		}
		probe.Readonly = !hasAPI
		probe.CanSelfRotate = hasAPI || slices.Contains(probe.Scopes, "self_rotate")
		if probe.ExpiresAt != nil && !probe.ExpiresAt.After(time.Now().UTC()) {
			return probe, &gitlabAPIError{Message: "The access token has expired.", Status: http.StatusUnauthorized}
		}
	}
	var user struct {
		Username string `json:"username"`
		Bot      bool   `json:"bot"`
	}
	if err := s.gitlabRequest(ctx, base, token, http.MethodGet, "/user", nil, &user); err != nil {
		return probe, err
	}
	probe.Username, probe.Bot = user.Username, user.Bot
	return probe, nil
}

func gitlabCredentialID(workspace, connectionID string) string {
	return connectorSecretID(workspace, "provider", connectionID)
}

func gitlabSecretExpiry(expiresAt *time.Time) time.Time {
	// Keep the ciphertext a little past the token's own expiry so the UI can
	// still report "Token expired" and rotation can be retried.
	if expiresAt != nil {
		return expiresAt.Add(30 * 24 * time.Hour)
	}
	return time.Now().UTC().AddDate(5, 0, 0)
}

func (s *server) putGitLabCredential(ctx context.Context, workspace, connectionID string, credential gitlabCredential, expiresAt *time.Time) error {
	raw, err := json.Marshal(credential)
	if err != nil {
		return err
	}
	return s.store.PutConnectorSecret(ctx, gitlabCredentialID(workspace, connectionID), raw, gitlabSecretExpiry(expiresAt))
}

func (s *server) gitlabCredential(ctx context.Context, workspace, connectionID string) (gitlabCredential, error) {
	var credential gitlabCredential
	if !store.ConnectorSecretsConfigured() {
		return credential, errors.New("encrypted credential storage is not configured")
	}
	raw, err := s.store.ReadConnectorSecret(ctx, gitlabCredentialID(workspace, connectionID), false)
	if err != nil {
		return credential, errors.New("the GitLab access token is not available; update the token")
	}
	if err := json.Unmarshal(raw, &credential); err != nil || credential.Token == "" {
		return credential, errors.New("the GitLab access token is not available; update the token")
	}
	return credential, nil
}

// applyGitLabProbe writes the non-secret token facts into connection config.
func applyGitLabProbe(config map[string]string, probe gitlabTokenProbe, token string, now time.Time) {
	config["host"] = probe.URL
	config["username"] = probe.Username
	config["tokenHint"] = token[max(0, len(token)-4):]
	config["tokenName"] = probe.TokenName
	config["tokenKind"] = "personal"
	if probe.Bot {
		config["tokenKind"] = "project"
	}
	config["readonly"] = fmt.Sprint(probe.Readonly)
	config["canSelfRotate"] = fmt.Sprint(probe.CanSelfRotate)
	config["expiresAt"] = ""
	if probe.ExpiresAt != nil {
		config["expiresAt"] = probe.ExpiresAt.Format(time.RFC3339)
	}
	config["validatedAt"] = now.Format(time.RFC3339)
	delete(config, "rotationFailureReason")
	if !probe.CanSelfRotate {
		config["rotationEnabled"] = "false"
	}
	config["nextRotationAt"] = gitlabNextRotation(config, now)
}

// gitlabNextRotation schedules a renewal a week before expiry (or now when the
// token is already inside that window).
func gitlabNextRotation(config map[string]string, now time.Time) string {
	if config["rotationEnabled"] != "true" {
		return ""
	}
	expiresAt := parseGitLabDate(config["expiresAt"])
	if expiresAt == nil {
		return ""
	}
	next := expiresAt.Add(-gitlabRotationLeadTime)
	if next.Before(now) {
		next = now
	}
	return next.Format(time.RFC3339)
}

type gitlabConnectResponse struct {
	domain.IntegrationConnection
	WebhookSecret string `json:"webhookSecret,omitempty"`
	WebhookPath   string `json:"webhookPath,omitempty"`
}

func gitlabWebhookPath(workspace string) string {
	path := "/api/integrations/gitlab/webhook"
	if workspace != "" {
		path += "?workspace=" + url.QueryEscape(workspace)
	}
	return path
}

// connectGitLabToken handles PUT /api/integrations/gitlab.
func (s *server) connectGitLabToken(w http.ResponseWriter, r *http.Request, name string, input map[string]string) {
	token := strings.TrimSpace(input["apiToken"])
	if token == "" {
		writeError(w, http.StatusBadRequest, gitlabMissingTokenMessage)
		return
	}
	rawURL := input["host"]
	if strings.TrimSpace(rawURL) == "" {
		rawURL = input["gitlabUrl"]
	}
	base, err := normalizeGitLabURL(rawURL, s.authDisabled)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if !store.ConnectorSecretsConfigured() {
		writeError(w, http.StatusServiceUnavailable, "Configure FLOW_CONNECTOR_SECRET_KEY before connecting GitLab")
		return
	}
	probe, err := s.probeGitLabToken(r.Context(), base, token)
	if err != nil {
		writeGitLabError(w, err)
		return
	}
	metadata := s.workspaceData(r)
	workspace := metadata.Workspace.URLKey
	now := time.Now().UTC()
	connectionID := fmt.Sprintf("integration_gitlab_%d", now.UnixNano())
	webhookSecret := ""
	var previous *domain.IntegrationConnection
	if index := slices.IndexFunc(metadata.IntegrationConnections, func(item domain.IntegrationConnection) bool { return item.Provider == "gitlab" }); index >= 0 {
		previous = &metadata.IntegrationConnections[index]
		connectionID = previous.ID
		if previous.Config["host"] == base {
			webhookSecret = previous.Config["webhookSecret"]
		}
	}
	if webhookSecret == "" {
		if webhookSecret, err = randomSecret("flow_glwh_"); err != nil {
			respondMutation(w, err, http.StatusOK, nil)
			return
		}
	}
	if err := s.putGitLabCredential(r.Context(), workspace, connectionID, gitlabCredential{Token: token, URL: base, TokenID: probe.TokenID}, probe.ExpiresAt); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not store the GitLab access token")
		return
	}
	actor := requestActor(s, r)
	var updated domain.IntegrationConnection
	event := map[string]string{"provider": "gitlab", "host": base, "username": probe.Username}
	err = s.store.MutateWorkspace(r.Context(), workspaceKey(r), "integration.connected", "gitlab", event, func(data *domain.Bootstrap) error {
		config := map[string]string{}
		index := slices.IndexFunc(data.IntegrationConnections, func(item domain.IntegrationConnection) bool {
			return item.ID == connectionID && item.Provider == "gitlab"
		})
		if index >= 0 {
			for _, key := range gitlabSettingKeys {
				if value, ok := data.IntegrationConnections[index].Config[key]; ok {
					config[key] = value
				}
			}
		}
		applyGitLabProbe(config, probe, token, now)
		config["webhookSecret"] = webhookSecret
		updated = domain.IntegrationConnection{ID: connectionID, Provider: "gitlab", Name: strings.TrimSpace(name), Status: "connected", Config: config, SecretHash: secretHash(token), ConnectedBy: actor.ID, CreatedAt: now, UpdatedAt: now, Scopes: probe.Scopes, Channels: []string{}, LinkbackEnabled: !probe.Readonly, LastTestAt: &now, LastTestStatus: "ready"}
		if updated.Name == "" {
			updated.Name = "GitLab"
		}
		if updated.Scopes == nil {
			updated.Scopes = []string{}
		}
		if index >= 0 {
			updated.CreatedAt = data.IntegrationConnections[index].CreatedAt
			updated.LastWebhookAt = data.IntegrationConnections[index].LastWebhookAt
			updated.DeliveryAttempts = data.IntegrationConnections[index].DeliveryAttempts
			updated.LastDeliveryAt = data.IntegrationConnections[index].LastDeliveryAt
			data.IntegrationConnections[index] = updated
		} else {
			data.IntegrationConnections = append(data.IntegrationConnections, updated)
		}
		return nil
	})
	if err != nil {
		if previous == nil {
			_ = s.store.DeleteConnectorSecret(r.Context(), gitlabCredentialID(workspace, connectionID))
		}
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	writeJSON(w, http.StatusOK, gitlabConnectResponse{IntegrationConnection: redactIntegrationConnection(updated), WebhookSecret: webhookSecret, WebhookPath: gitlabWebhookPath(workspace)})
}

func (s *server) gitlabConnectionSnapshot(r *http.Request, id string) (domain.Bootstrap, *domain.IntegrationConnection) {
	data := s.workspaceData(r)
	index := slices.IndexFunc(data.IntegrationConnections, func(item domain.IntegrationConnection) bool { return item.ID == id && item.Provider == "gitlab" })
	if index < 0 {
		return data, nil
	}
	return data, &data.IntegrationConnections[index]
}

func (s *server) saveGitLabConnection(ctx context.Context, workspaceKey, id, eventType string, mutate func(connection *domain.IntegrationConnection, now time.Time)) (domain.IntegrationConnection, error) {
	var updated domain.IntegrationConnection
	err := s.store.MutateWorkspace(ctx, workspaceKey, eventType, id, map[string]string{"provider": "gitlab"}, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.IntegrationConnections, func(item domain.IntegrationConnection) bool { return item.ID == id && item.Provider == "gitlab" })
		if index < 0 {
			return errNotFound
		}
		connection := &data.IntegrationConnections[index]
		if connection.Config == nil {
			connection.Config = map[string]string{}
		}
		now := time.Now().UTC()
		mutate(connection, now)
		connection.UpdatedAt = now
		updated = *connection
		return nil
	})
	return updated, err
}

// updateGitLabToken handles POST /api/integrations/gitlab/{id}/token.
func (s *server) updateGitLabToken(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	var input struct {
		Token string `json:"token"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	token := strings.TrimSpace(input.Token)
	if token == "" {
		writeError(w, http.StatusBadRequest, gitlabMissingTokenMessage)
		return
	}
	if !store.ConnectorSecretsConfigured() {
		writeError(w, http.StatusServiceUnavailable, "Configure FLOW_CONNECTOR_SECRET_KEY before connecting GitLab")
		return
	}
	data, connection := s.gitlabConnectionSnapshot(r, id)
	if connection == nil {
		writeError(w, http.StatusNotFound, "resource not found")
		return
	}
	base, err := normalizeGitLabURL(connection.Config["host"], s.authDisabled)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	probe, err := s.probeGitLabToken(r.Context(), base, token)
	if err != nil {
		writeGitLabError(w, err)
		return
	}
	if err := s.putGitLabCredential(r.Context(), data.Workspace.URLKey, id, gitlabCredential{Token: token, URL: base, TokenID: probe.TokenID}, probe.ExpiresAt); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not store the GitLab access token")
		return
	}
	updated, err := s.saveGitLabConnection(r.Context(), workspaceKey(r), id, "integration.token_updated", func(connection *domain.IntegrationConnection, now time.Time) {
		applyGitLabProbe(connection.Config, probe, token, now)
		connection.SecretHash, connection.Scopes, connection.LinkbackEnabled = secretHash(token), probe.Scopes, !probe.Readonly
		if connection.Scopes == nil {
			connection.Scopes = []string{}
		}
		connection.Status, connection.LastError, connection.LastTestStatus, connection.LastTestAt = "connected", "", "ready", &now
	})
	respondMutation(w, err, http.StatusOK, redactIntegrationConnection(updated))
}

// rotateGitLabTokenNow calls GitLab's self-rotation endpoint, which revokes
// the current token and returns its replacement, and stores the replacement.
func (s *server) rotateGitLabTokenNow(ctx context.Context, workspaceKey, workspace, id string) (domain.IntegrationConnection, error) {
	credential, err := s.gitlabCredential(ctx, workspace, id)
	if err != nil {
		return domain.IntegrationConnection{}, &gitlabAPIError{Message: err.Error()}
	}
	expires := time.Now().UTC().Add(gitlabRotationLifetime).Format("2006-01-02")
	var rotated struct {
		ID        int64    `json:"id"`
		Name      string   `json:"name"`
		Scopes    []string `json:"scopes"`
		Token     string   `json:"token"`
		ExpiresAt string   `json:"expires_at"`
	}
	rotateErr := s.gitlabRequest(ctx, credential.URL, credential.Token, http.MethodPost, "/personal_access_tokens/self/rotate", map[string]string{"expires_at": expires}, &rotated)
	if rotateErr == nil && strings.TrimSpace(rotated.Token) == "" {
		rotateErr = &gitlabAPIError{Message: "GitLab did not return a replacement token."}
	}
	if rotateErr != nil {
		message := rotateErr.Error()
		_, _ = s.saveGitLabConnection(ctx, workspaceKey, id, "integration.token_rotation_failed", func(connection *domain.IntegrationConnection, now time.Time) {
			connection.Config["rotationFailureReason"] = "Last rotation failed: " + message
		})
		return domain.IntegrationConnection{}, rotateErr
	}
	expiresAt := parseGitLabDate(rotated.ExpiresAt)
	if err := s.putGitLabCredential(ctx, workspace, id, gitlabCredential{Token: rotated.Token, URL: credential.URL, TokenID: rotated.ID}, expiresAt); err != nil {
		// The old token is already revoked by GitLab; surface the failure.
		_, _ = s.saveGitLabConnection(ctx, workspaceKey, id, "integration.token_rotation_failed", func(connection *domain.IntegrationConnection, now time.Time) {
			connection.Config["rotationFailureReason"] = "The rotated token could not be stored. Update the token."
		})
		return domain.IntegrationConnection{}, errors.New("the rotated GitLab token could not be stored")
	}
	return s.saveGitLabConnection(ctx, workspaceKey, id, "integration.token_rotated", func(connection *domain.IntegrationConnection, now time.Time) {
		config := connection.Config
		config["tokenHint"] = rotated.Token[max(0, len(rotated.Token)-4):]
		if rotated.Name != "" {
			config["tokenName"] = rotated.Name
		}
		config["expiresAt"] = ""
		if expiresAt != nil {
			config["expiresAt"] = expiresAt.Format(time.RFC3339)
		}
		config["lastRotatedAt"] = now.Format(time.RFC3339)
		delete(config, "rotationFailureReason")
		config["nextRotationAt"] = gitlabNextRotation(config, now)
		if scopes := uniqueLower(rotated.Scopes); len(scopes) > 0 {
			connection.Scopes = scopes
		}
		connection.SecretHash = secretHash(rotated.Token)
		connection.Status, connection.LastError = "connected", ""
	})
}

// rotateGitLabToken handles POST /api/integrations/gitlab/{id}/token/rotate.
func (s *server) rotateGitLabToken(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	data, connection := s.gitlabConnectionSnapshot(r, id)
	if connection == nil {
		writeError(w, http.StatusNotFound, "resource not found")
		return
	}
	if connection.Config["canSelfRotate"] != "true" {
		writeError(w, http.StatusUnprocessableEntity, "This token cannot rotate itself. Update it with api or both read_api and self_rotate scopes.")
		return
	}
	if expiresAt := parseGitLabDate(connection.Config["expiresAt"]); expiresAt != nil && !expiresAt.After(time.Now().UTC()) {
		writeError(w, http.StatusUnprocessableEntity, "Update the expired token to enable automatic rotation.")
		return
	}
	updated, err := s.rotateGitLabTokenNow(r.Context(), workspaceKey(r), data.Workspace.URLKey, id)
	if err != nil {
		writeGitLabError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, redactIntegrationConnection(updated))
}

// setGitLabRotation handles PUT /api/integrations/gitlab/{id}/token/rotation.
func (s *server) setGitLabRotation(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	var input struct {
		Enabled bool `json:"enabled"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	_, connection := s.gitlabConnectionSnapshot(r, id)
	if connection == nil {
		writeError(w, http.StatusNotFound, "resource not found")
		return
	}
	if input.Enabled && connection.Config["canSelfRotate"] != "true" {
		writeError(w, http.StatusUnprocessableEntity, "This token cannot rotate itself. Update it with api or both read_api and self_rotate scopes.")
		return
	}
	updated, err := s.saveGitLabConnection(r.Context(), workspaceKey(r), id, "integration.token_rotation_updated", func(connection *domain.IntegrationConnection, now time.Time) {
		connection.Config["rotationEnabled"] = fmt.Sprint(input.Enabled)
		if !input.Enabled {
			delete(connection.Config, "rotationFailureReason")
		}
		connection.Config["nextRotationAt"] = gitlabNextRotation(connection.Config, now)
	})
	respondMutation(w, err, http.StatusOK, redactIntegrationConnection(updated))
}

// rotateDueGitLabTokens renews tokens whose scheduled rotation time passed.
// It runs from the minute sweep; failures are recorded on the connection and
// not retried until an administrator acts.
func (s *server) rotateDueGitLabTokens(ctx context.Context, key string, now time.Time) {
	if !store.ConnectorSecretsConfigured() {
		return
	}
	data, ok := s.store.WorkspaceMetadataFields(key, "integrationConnections")
	if !ok {
		return
	}
	for _, connection := range data.IntegrationConnections {
		if connection.Provider != "gitlab" || connection.Config["rotationEnabled"] != "true" || connection.Config["canSelfRotate"] != "true" || connection.Config["rotationFailureReason"] != "" {
			continue
		}
		next := parseGitLabDate(connection.Config["nextRotationAt"])
		if next == nil || next.After(now) {
			continue
		}
		workspace := data.Workspace.URLKey
		if workspace == "" {
			workspace = key
		}
		if _, err := s.rotateGitLabTokenNow(ctx, key, workspace, connection.ID); err != nil {
			log.Printf("GitLab token rotation workspace=%s connection=%s failed: %s", key, connection.ID, err.Error())
		}
	}
}
