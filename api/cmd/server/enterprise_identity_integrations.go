package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

func (s *server) listIdentityProviders(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.workspaceData(r).IdentityProviders)
}

func validateIdentityProvider(input domain.IdentityProvider) error {
	input.Type = strings.ToLower(strings.TrimSpace(input.Type))
	if !slices.Contains([]string{"oidc", "saml"}, input.Type) || strings.TrimSpace(input.Name) == "" {
		return errInvalid
	}
	u, err := url.Parse(strings.TrimSpace(input.Issuer))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return errInvalid
	}
	if input.Type == "oidc" && strings.TrimSpace(input.ClientID) == "" {
		return errInvalid
	}
	if input.ClientSecretEnv != "" && !strings.HasPrefix(input.ClientSecretEnv, "FLOW_IDP_") {
		return errInvalid
	}
	for _, domainName := range input.Domains {
		if strings.Contains(domainName, "@") || strings.ContainsAny(domainName, "/: ") || !strings.Contains(domainName, ".") {
			return errInvalid
		}
	}
	if input.DefaultRole != "" && !identityRoleValid(input.DefaultRole) {
		return errInvalid
	}
	for _, mapped := range input.RoleMapping {
		if !identityRoleValid(mapped) {
			return errInvalid
		}
	}
	return nil
}

func (s *server) createIdentityProvider(w http.ResponseWriter, r *http.Request) {
	var input domain.IdentityProvider
	if !decodeJSON(w, r, &input) {
		return
	}
	if validateIdentityProvider(input) != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid identity provider")
		return
	}
	now := time.Now().UTC()
	input.ID = fmt.Sprintf("idp_%d", now.UnixNano())
	input.WorkspaceID = s.workspaceData(r).Workspace.ID
	input.CreatedAt = now
	input.UpdatedAt = now
	input.DiscoveryStatus = "unverified"
	if input.Scopes == nil {
		input.Scopes = []string{"openid", "profile", "email"}
	}
	if input.DefaultRole == "" {
		input.DefaultRole = "member"
	}
	if input.RoleMapping == nil {
		input.RoleMapping = map[string]string{}
	}
	input.Domains = uniqueLower(input.Domains)
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "identity_provider.created", input.ID, nil, func(data *domain.Bootstrap) error {
		data.IdentityProviders = append(data.IdentityProviders, input)
		return nil
	})
	respondMutation(w, err, http.StatusCreated, input)
}

func (s *server) updateIdentityProvider(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Type            *string            `json:"type"`
		Name            *string            `json:"name"`
		Issuer          *string            `json:"issuer"`
		ClientID        *string            `json:"clientId"`
		ClientSecretEnv *string            `json:"clientSecretEnv"`
		Scopes          *[]string          `json:"scopes"`
		Domains         *[]string          `json:"domains"`
		Enabled         *bool              `json:"enabled"`
		Enforced        *bool              `json:"enforced"`
		RoleClaim       *string            `json:"roleClaim"`
		RoleMapping     *map[string]string `json:"roleMapping"`
		DefaultRole     *string            `json:"defaultRole"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	var result domain.IdentityProvider
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "identity_provider.updated", r.PathValue("id"), nil, func(data *domain.Bootstrap) error {
		i := slices.IndexFunc(data.IdentityProviders, func(v domain.IdentityProvider) bool { return v.ID == r.PathValue("id") })
		if i < 0 {
			return errNotFound
		}
		current := data.IdentityProviders[i]
		if input.Name != nil {
			current.Name = strings.TrimSpace(*input.Name)
		}
		if input.Issuer != nil {
			current.Issuer = strings.TrimSpace(*input.Issuer)
		}
		if input.ClientID != nil {
			current.ClientID = strings.TrimSpace(*input.ClientID)
		}
		if input.ClientSecretEnv != nil {
			current.ClientSecretEnv = strings.TrimSpace(*input.ClientSecretEnv)
		}
		if input.Type != nil {
			current.Type = strings.ToLower(strings.TrimSpace(*input.Type))
		}
		if input.Scopes != nil {
			current.Scopes = uniqueLower(*input.Scopes)
		}
		if input.Domains != nil {
			current.Domains = uniqueLower(*input.Domains)
		}
		if input.Enabled != nil {
			current.Enabled = *input.Enabled
		}
		if input.Enforced != nil {
			current.Enforced = *input.Enforced
		}
		if input.RoleClaim != nil {
			current.RoleClaim = strings.TrimSpace(*input.RoleClaim)
		}
		if input.RoleMapping != nil {
			current.RoleMapping = map[string]string{}
			for key, value := range *input.RoleMapping {
				mapped := strings.ToLower(strings.TrimSpace(value))
				if identityRoleValid(mapped) {
					current.RoleMapping[strings.ToLower(strings.TrimSpace(key))] = mapped
				}
			}
		}
		if input.DefaultRole != nil {
			role := strings.ToLower(strings.TrimSpace(*input.DefaultRole))
			if !identityRoleValid(role) {
				return errInvalid
			}
			current.DefaultRole = role
		}
		current.UpdatedAt = time.Now().UTC()
		if validateIdentityProvider(current) != nil {
			return errInvalid
		}
		data.IdentityProviders[i] = current
		result = current
		return nil
	})
	respondMutation(w, err, http.StatusOK, result)
}

func (s *server) deleteIdentityProvider(w http.ResponseWriter, r *http.Request) {
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "identity_provider.deleted", r.PathValue("id"), nil, func(data *domain.Bootstrap) error {
		before := len(data.IdentityProviders)
		data.IdentityProviders = slices.DeleteFunc(data.IdentityProviders, func(v domain.IdentityProvider) bool { return v.ID == r.PathValue("id") })
		if before == len(data.IdentityProviders) {
			return errNotFound
		}
		return nil
	})
	respondMutation(w, err, http.StatusNoContent, nil)
}

func (s *server) verifyIdentityProvider(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	index := slices.IndexFunc(data.IdentityProviders, func(v domain.IdentityProvider) bool { return v.ID == r.PathValue("id") })
	if index < 0 {
		writeError(w, http.StatusNotFound, "identity provider not found")
		return
	}
	snapshot := data.IdentityProviders[index]
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	client := secureOutboundClient(5 * time.Second)
	if !safeOutboundHTTPS(ctx, snapshot.Issuer) {
		writeError(w, http.StatusUnprocessableEntity, "identity provider URL must be a public HTTPS endpoint")
		return
	}
	if snapshot.Type == "oidc" {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(snapshot.Issuer, "/")+"/.well-known/openid-configuration", nil)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid identity provider URL")
			return
		}
		resp, err := client.Do(req)
		if err != nil {
			writeError(w, http.StatusBadGateway, "identity provider verification failed")
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			writeError(w, http.StatusBadGateway, "identity provider metadata was unavailable")
			return
		}
		var metadata struct {
			Issuer                string `json:"issuer"`
			AuthorizationEndpoint string `json:"authorization_endpoint"`
			TokenEndpoint         string `json:"token_endpoint"`
		}
		if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&metadata) != nil || strings.TrimRight(metadata.Issuer, "/") != strings.TrimRight(snapshot.Issuer, "/") || !safeOutboundHTTPS(ctx, metadata.AuthorizationEndpoint) || !safeOutboundHTTPS(ctx, metadata.TokenEndpoint) {
			writeError(w, http.StatusUnprocessableEntity, "identity provider metadata is invalid")
			return
		}
	} else {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, snapshot.Issuer, nil)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid metadata URL")
			return
		}
		resp, err := client.Do(req)
		if err != nil {
			writeError(w, http.StatusBadGateway, "identity provider verification failed")
			return
		}
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			writeError(w, http.StatusBadGateway, "identity provider metadata was unavailable")
			return
		}
	}
	var result domain.IdentityProvider
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "identity_provider.verified", r.PathValue("id"), nil, func(data *domain.Bootstrap) error {
		i := slices.IndexFunc(data.IdentityProviders, func(v domain.IdentityProvider) bool { return v.ID == snapshot.ID })
		if i < 0 {
			return errNotFound
		}
		p := &data.IdentityProviders[i]
		if p.UpdatedAt != snapshot.UpdatedAt || p.Issuer != snapshot.Issuer {
			return errors.New("identity provider changed while verification was in progress")
		}
		now := time.Now().UTC()
		p.LastVerifiedAt = &now
		p.DiscoveryStatus = "verified"
		p.UpdatedAt = now
		result = *p
		return nil
	})
	respondMutation(w, err, http.StatusOK, result)
}

func (s *server) discoverWorkspaceSSO(w http.ResponseWriter, r *http.Request) {
	email := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("email")))
	at := strings.LastIndex(email, "@")
	if at < 1 || at == len(email)-1 {
		writeError(w, http.StatusBadRequest, "valid email is required")
		return
	}
	domainName := email[at+1:]
	items := []map[string]string{}
	// Workspaces are deliberately scanned without returning issuer or client identifiers.
	for _, key := range s.store.WorkspaceKeys() {
		data, ok := s.store.BootstrapFor(key)
		if !ok {
			continue
		}
		for _, p := range data.IdentityProviders {
			if p.Enabled && p.DiscoveryStatus == "verified" && slices.Contains(p.Domains, domainName) {
				items = append(items, map[string]string{"id": p.ID, "name": p.Name, "type": p.Type, "workspace": data.Workspace.URLKey, "startUrl": "/api/auth/enterprise/" + url.PathEscape(p.ID) + "/start?workspace=" + url.QueryEscape(data.Workspace.URLKey)})
				if len(items) >= 10 {
					writeJSON(w, http.StatusOK, items)
					return
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, items)
}

func enterpriseProviderKey(workspaceKey, providerID string) string {
	return "enterprise~" + workspaceKey + "~" + providerID
}
func parseEnterpriseProviderKey(value string) (string, string, bool) {
	parts := strings.Split(value, "~")
	return func() (string, string, bool) {
		if len(parts) != 3 || parts[0] != "enterprise" {
			return "", "", false
		}
		return parts[1], parts[2], true
	}()
}

func (s *server) enterpriseOIDCClient(ctx context.Context, workspaceKey, providerID string) (*oidcClient, domain.IdentityProvider, error) {
	data, ok := s.store.BootstrapFor(workspaceKey)
	if !ok {
		return nil, domain.IdentityProvider{}, errNotFound
	}
	i := slices.IndexFunc(data.IdentityProviders, func(v domain.IdentityProvider) bool {
		return v.ID == providerID && v.Type == "oidc" && v.Enabled && v.DiscoveryStatus == "verified"
	})
	if i < 0 {
		return nil, domain.IdentityProvider{}, errNotFound
	}
	p := data.IdentityProviders[i]
	if !safeOutboundHTTPS(ctx, p.Issuer) {
		return nil, p, errors.New("unsafe identity provider issuer")
	}
	secret := ""
	if strings.HasPrefix(p.ClientSecretEnv, "FLOW_IDP_") {
		secret = os.Getenv(p.ClientSecretEnv)
	}
	if p.ClientSecretEnv != "" && secret == "" {
		return nil, p, errors.New("identity provider client secret is not configured")
	}
	redirect := strings.TrimRight(s.allowedOrigin, "/") + "/api/auth/enterprise/" + url.PathEscape(p.ID) + "/callback?workspace=" + url.QueryEscape(workspaceKey)
	ctx = oidc.ClientContext(ctx, secureOutboundClient(10*time.Second))
	client, err := discoverOIDC(ctx, p.Name, p.Issuer, p.ClientID, secret, redirect, p.Scopes)
	return client, p, err
}

func (s *server) startEnterpriseOIDC(w http.ResponseWriter, r *http.Request) {
	workspaceKey := strings.TrimSpace(r.URL.Query().Get("workspace"))
	providerID := r.PathValue("id")
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	client, _, err := s.enterpriseOIDCClient(ctx, workspaceKey, providerID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "identity provider is unavailable")
		return
	}
	state, nonce, verifier := randomURLToken(32), randomURLToken(32), randomURLToken(48)
	encoded := encodeExternalState(externalAuthState{State: state, Nonce: nonce, Verifier: verifier, Provider: enterpriseProviderKey(workspaceKey, providerID)})
	http.SetCookie(w, &http.Cookie{Name: externalAuthCookie, Value: encoded, Path: "/api/auth/", HttpOnly: true, Secure: secureCookie(r), SameSite: http.SameSiteLaxMode, MaxAge: 600})
	challenge := sha256.Sum256([]byte(verifier))
	redirect := client.oauth.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.SetAuthURLParam("code_challenge", base64.RawURLEncoding.EncodeToString(challenge[:])), oauth2.SetAuthURLParam("code_challenge_method", "S256"))
	http.Redirect(w, r, redirect, http.StatusFound)
}

func (s *server) finishEnterpriseOIDC(w http.ResponseWriter, r *http.Request) {
	state, err := readExternalState(r)
	workspaceKey, providerID, ok := parseEnterpriseProviderKey(state.Provider)
	if err != nil || !ok || providerID != r.PathValue("id") || workspaceKey != r.URL.Query().Get("workspace") || state.State != r.URL.Query().Get("state") {
		writeError(w, http.StatusBadRequest, "invalid authentication state")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	client, provider, err := s.enterpriseOIDCClient(ctx, workspaceKey, providerID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "identity provider is unavailable")
		return
	}
	token, err := client.oauth.Exchange(ctx, r.URL.Query().Get("code"), oauth2.SetAuthURLParam("code_verifier", state.Verifier))
	if err != nil {
		writeError(w, http.StatusBadGateway, "identity provider token exchange failed")
		return
	}
	raw, _ := token.Extra("id_token").(string)
	idToken, err := client.verifier.Verify(ctx, raw)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid identity token")
		return
	}
	claims := map[string]any{}
	if idToken.Claims(&claims) != nil || stringClaim(claims, "nonce") != state.Nonce {
		writeError(w, http.StatusUnauthorized, "invalid identity token claims")
		return
	}
	subject, email, name, picture := stringClaim(claims, "sub"), stringClaim(claims, "email"), stringClaim(claims, "name"), stringClaim(claims, "picture")
	if subject == "" {
		writeError(w, http.StatusForbidden, "identity subject is required")
		return
	}
	if email != "" && len(provider.Domains) > 0 {
		at := strings.LastIndex(email, "@")
		if at < 0 || !slices.Contains(provider.Domains, strings.ToLower(email[at+1:])) {
			writeError(w, http.StatusForbidden, "identity domain is not allowed")
			return
		}
	}
	if name == "" {
		name = stringClaim(claims, "preferred_username")
	}
	claimsJSON, _ := json.Marshal(claims)
	session, sessionToken, err := s.store.LoginExternalIdentity(ctx, "enterprise:"+provider.ID, provider.Issuer, subject, stringClaim(claims, "preferred_username"), email, name, picture, string(claimsJSON), true)
	if err != nil {
		writeError(w, http.StatusForbidden, "could not create Flow session")
		return
	}
	data, _ := s.store.BootstrapFor(workspaceKey)
	if err = s.store.EnsureWorkspaceMembership(ctx, data.Workspace.ID, session.User.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "could not grant workspace access")
		return
	}
	if role := identityRole(provider, claims); role != "" {
		if err := s.store.UpdateMemberRole(ctx, data.Workspace.ID, session.User.ID, role); err != nil {
			writeError(w, http.StatusInternalServerError, "could not apply identity role")
			return
		}
	}
	clearExternalState(w, r)
	setSessionCookie(w, r, sessionToken, session.ExpiresAt)
	http.Redirect(w, r, strings.TrimRight(s.allowedOrigin, "/")+"/"+workspaceKey, http.StatusFound)
}

func uniqueLower(values []string) []string {
	result := []string{}
	for _, v := range values {
		v = strings.ToLower(strings.TrimSpace(v))
		if v != "" && !slices.Contains(result, v) {
			result = append(result, v)
		}
	}
	return result
}

func identityRole(provider domain.IdentityProvider, claims map[string]any) string {
	role := ""
	if provider.RoleClaim != "" {
		role = roleClaimValue(claims, provider.RoleClaim)
	}
	if role == "" {
		role = provider.DefaultRole
	}
	if mapped := provider.RoleMapping[strings.ToLower(strings.TrimSpace(role))]; mapped != "" {
		role = mapped
	}
	role = strings.ToLower(strings.TrimSpace(role))
	if !identityRoleValid(role) {
		return ""
	}
	return role
}

func roleClaimValue(claims map[string]any, claim string) string {
	value, ok := claims[strings.TrimSpace(claim)]
	if !ok {
		return ""
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	if values, ok := value.([]any); ok {
		for _, item := range values {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				return strings.TrimSpace(text)
			}
		}
	}
	return ""
}

func identityRoleValid(role string) bool {
	role = strings.ToLower(strings.TrimSpace(role))
	return role == "owner" || role == "admin" || role == "member" || role == "guest"
}
func uniqueTrimmed(values []string) []string {
	result := []string{}
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" && !slices.Contains(result, v) {
			result = append(result, v)
		}
	}
	return result
}

func redactIntegrationConnection(item domain.IntegrationConnection) domain.IntegrationConnection {
	item.OAuthState, item.OAuthAccessToken, item.OAuthRefreshToken = "", "", ""
	config := map[string]string{}
	for key, value := range item.Config {
		lower := strings.ToLower(key)
		if strings.Contains(lower, "secret") || strings.Contains(lower, "token") || strings.Contains(lower, "password") || strings.Contains(lower, "privatekey") {
			if strings.HasSuffix(lower, "env") || lower == "tokenhint" {
				config[key] = value
			}
			continue
		}
		config[key] = value
	}
	item.Config = config
	return item
}

func (s *server) listAccountIdentities(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListAuthIdentities(r.Context(), authUser(r).ID)
	respondMutation(w, err, http.StatusOK, items)
}
func (s *server) unlinkAccountIdentity(w http.ResponseWriter, r *http.Request) {
	err := s.store.UnlinkAuthIdentity(r.Context(), authUser(r).ID, r.PathValue("id"))
	respondMutation(w, err, http.StatusNoContent, nil)
}
func (s *server) revokeAccountSession(w http.ResponseWriter, r *http.Request) {
	cookie, _ := r.Cookie(sessionCookieName)
	err := s.store.RevokeSession(r.Context(), authUser(r).ID, r.PathValue("id"), cookie.Value)
	respondMutation(w, err, http.StatusNoContent, nil)
}

func (s *server) upsertGitAutomation(w http.ResponseWriter, r *http.Request) {
	var input domain.GitAutomationState
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.TeamID == "" || input.Repository == "" || input.Event == "" || input.WorkflowStateID == "" {
		writeError(w, http.StatusUnprocessableEntity, "team, repository, event, and workflow state are required")
		return
	}
	var result domain.GitAutomationState
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "git_automation.upserted", input.ID, input, func(data *domain.Bootstrap) error {
		if !slices.ContainsFunc(data.Teams, func(v domain.Team) bool { return v.ID == input.TeamID }) || !slices.ContainsFunc(data.States, func(v domain.WorkflowState) bool {
			return v.ID == input.WorkflowStateID && (v.TeamID == input.TeamID || v.TeamID == "")
		}) {
			return errInvalid
		}
		now := time.Now().UTC()
		i := slices.IndexFunc(data.GitAutomationStates, func(v domain.GitAutomationState) bool { return v.ID == input.ID && input.ID != "" })
		if i < 0 {
			input.ID = fmt.Sprintf("git_automation_%d", now.UnixNano())
			input.CreatedAt = now
			data.GitAutomationStates = append(data.GitAutomationStates, input)
			i = len(data.GitAutomationStates) - 1
		}
		input.CreatedAt = data.GitAutomationStates[i].CreatedAt
		input.UpdatedAt = now
		data.GitAutomationStates[i] = input
		result = input
		return nil
	})
	respondMutation(w, err, http.StatusOK, result)
}
func (s *server) deleteGitAutomation(w http.ResponseWriter, r *http.Request) {
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "git_automation.deleted", r.PathValue("id"), nil, func(data *domain.Bootstrap) error {
		before := len(data.GitAutomationStates)
		data.GitAutomationStates = slices.DeleteFunc(data.GitAutomationStates, func(v domain.GitAutomationState) bool { return v.ID == r.PathValue("id") })
		if before == len(data.GitAutomationStates) {
			return errNotFound
		}
		return nil
	})
	respondMutation(w, err, http.StatusNoContent, nil)
}
func (s *server) upsertTargetBranch(w http.ResponseWriter, r *http.Request) {
	var input domain.TargetBranch
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.TeamID == "" || input.Repository == "" || input.Branch == "" {
		writeError(w, http.StatusUnprocessableEntity, "team, repository, and branch are required")
		return
	}
	var result domain.TargetBranch
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "target_branch.upserted", input.ID, input, func(data *domain.Bootstrap) error {
		if !slices.ContainsFunc(data.Teams, func(v domain.Team) bool { return v.ID == input.TeamID }) {
			return errInvalid
		}
		now := time.Now().UTC()
		if input.Default {
			for i := range data.TargetBranches {
				if data.TargetBranches[i].TeamID == input.TeamID && data.TargetBranches[i].Repository == input.Repository {
					data.TargetBranches[i].Default = false
				}
			}
		}
		i := slices.IndexFunc(data.TargetBranches, func(v domain.TargetBranch) bool { return v.ID == input.ID && input.ID != "" })
		if i < 0 {
			input.ID = fmt.Sprintf("target_branch_%d", now.UnixNano())
			input.CreatedAt = now
			data.TargetBranches = append(data.TargetBranches, input)
			i = len(data.TargetBranches) - 1
		}
		input.CreatedAt = data.TargetBranches[i].CreatedAt
		input.UpdatedAt = now
		data.TargetBranches[i] = input
		result = input
		return nil
	})
	respondMutation(w, err, http.StatusOK, result)
}
func (s *server) deleteTargetBranch(w http.ResponseWriter, r *http.Request) {
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "target_branch.deleted", r.PathValue("id"), nil, func(data *domain.Bootstrap) error {
		before := len(data.TargetBranches)
		data.TargetBranches = slices.DeleteFunc(data.TargetBranches, func(v domain.TargetBranch) bool { return v.ID == r.PathValue("id") })
		if before == len(data.TargetBranches) {
			return errNotFound
		}
		return nil
	})
	respondMutation(w, err, http.StatusNoContent, nil)
}

func (s *server) findAttachmentsByURL(w http.ResponseWriter, r *http.Request) {
	needle := strings.TrimSpace(r.URL.Query().Get("url"))
	if needle == "" || len(needle) > 2048 {
		writeError(w, http.StatusBadRequest, "url is required")
		return
	}
	items := []domain.Attachment{}
	for _, issue := range s.workspaceData(r).Issues {
		for _, a := range issue.Attachments {
			if a.URL == needle || a.ProviderURL == needle || a.LinkbackURL == needle {
				items = append(items, a)
				if len(items) >= 100 {
					writeJSON(w, http.StatusOK, items)
					return
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *server) createIntegrationDelivery(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ConnectionID string          `json:"connectionId"`
		EventType    string          `json:"eventType"`
		ResourceID   string          `json:"resourceId"`
		Channel      string          `json:"channel"`
		Payload      json.RawMessage `json:"payload"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.ConnectionID == "" || input.EventType == "" || len(input.Payload) == 0 || !json.Valid(input.Payload) {
		writeError(w, http.StatusUnprocessableEntity, "connection, event type, and JSON payload are required")
		return
	}
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(idempotencyKey) > 128 {
		writeError(w, http.StatusBadRequest, "idempotency key is too long")
		return
	}
	if idempotencyKey == "" {
		sum := sha256.Sum256([]byte(input.ConnectionID + "\x00" + input.EventType + "\x00" + input.ResourceID + "\x00" + input.Channel + "\x00" + string(input.Payload)))
		idempotencyKey = fmt.Sprintf("%x", sum[:])
	}
	var result domain.IntegrationDelivery
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "integration_delivery.queued", input.ResourceID, nil, func(data *domain.Bootstrap) error {
		if existing := slices.IndexFunc(data.IntegrationDeliveries, func(v domain.IntegrationDelivery) bool { return v.IdempotencyKey == idempotencyKey }); existing >= 0 {
			result = data.IntegrationDeliveries[existing]
			return nil
		}
		i := slices.IndexFunc(data.IntegrationConnections, func(v domain.IntegrationConnection) bool {
			return v.ID == input.ConnectionID && v.Status == "configured"
		})
		if i < 0 {
			return errNotFound
		}
		connection := data.IntegrationConnections[i]
		if input.Channel != "" && len(connection.Channels) > 0 && !slices.Contains(connection.Channels, input.Channel) {
			return errInvalid
		}
		if len(connection.Scopes) > 0 && !slices.Contains(connection.Scopes, input.EventType) && !slices.Contains(connection.Scopes, "events:write") {
			return errInvalid
		}
		if len(data.IntegrationDeliveries) >= 10000 {
			data.IntegrationDeliveries = slices.DeleteFunc(data.IntegrationDeliveries, func(v domain.IntegrationDelivery) bool {
				return v.Status == "delivered" && time.Since(v.UpdatedAt) > 7*24*time.Hour
			})
			if len(data.IntegrationDeliveries) >= 10000 {
				return errors.New("integration delivery queue is full")
			}
		}
		now := time.Now().UTC()
		result = domain.IntegrationDelivery{ID: fmt.Sprintf("integration_delivery_%d", now.UnixNano()), IdempotencyKey: idempotencyKey, ConnectionID: input.ConnectionID, EventType: input.EventType, ResourceID: input.ResourceID, Channel: input.Channel, Payload: append(json.RawMessage(nil), input.Payload...), Status: "pending", CreatedAt: now, UpdatedAt: now}
		data.IntegrationDeliveries = append(data.IntegrationDeliveries, result)
		return nil
	})
	respondMutation(w, err, http.StatusAccepted, result)
}

func (s *server) retryIntegrationDelivery(w http.ResponseWriter, r *http.Request) {
	result, err := s.processIntegrationDelivery(r.Context(), workspaceKey(r), r.PathValue("id"))
	if errors.Is(err, errConflict) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if errors.Is(err, errInvalid) {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	respondMutation(w, err, http.StatusOK, result)
}

func (s *server) processIntegrationDelivery(ctx context.Context, workspace, id string) (domain.IntegrationDelivery, error) {
	data, ok := s.store.BootstrapFor(workspace)
	if !ok {
		return domain.IntegrationDelivery{}, errNotFound
	}
	deliveryIndex := slices.IndexFunc(data.IntegrationDeliveries, func(v domain.IntegrationDelivery) bool { return v.ID == id })
	if deliveryIndex < 0 {
		return domain.IntegrationDelivery{}, errNotFound
	}
	snapshot := data.IntegrationDeliveries[deliveryIndex]
	if snapshot.Status != "pending" && snapshot.Status != "failed" {
		return domain.IntegrationDelivery{}, fmt.Errorf("%w: delivery is not retryable", errConflict)
	}
	if snapshot.Attempts >= 8 {
		return domain.IntegrationDelivery{}, fmt.Errorf("%w: delivery retry limit reached", errConflict)
	}
	connectionIndex := slices.IndexFunc(data.IntegrationConnections, func(v domain.IntegrationConnection) bool { return v.ID == snapshot.ConnectionID })
	if connectionIndex < 0 {
		return domain.IntegrationDelivery{}, errNotFound
	}
	connectionSnapshot := data.IntegrationConnections[connectionIndex]
	endpoint := strings.TrimSpace(connectionSnapshot.Config["deliveryURL"])
	if !integrationEndpointSafe(ctx, endpoint, s.authDisabled) {
		return domain.IntegrationDelivery{}, fmt.Errorf("%w: delivery URL must be a public HTTPS endpoint", errInvalid)
	}
	claimErr := s.store.MutateWorkspace(ctx, workspace, "integration_delivery.claimed", id, nil, func(current *domain.Bootstrap) error {
		i := slices.IndexFunc(current.IntegrationDeliveries, func(v domain.IntegrationDelivery) bool { return v.ID == id })
		if i < 0 {
			return errNotFound
		}
		if current.IntegrationDeliveries[i].Attempts >= 8 {
			return errConflict
		}
		if current.IntegrationDeliveries[i].Status == "delivering" {
			return errConflict
		}
		snapshot = current.IntegrationDeliveries[i]
		connectionIndex := slices.IndexFunc(current.IntegrationConnections, func(v domain.IntegrationConnection) bool { return v.ID == snapshot.ConnectionID })
		if connectionIndex < 0 {
			return errNotFound
		}
		current.IntegrationDeliveries[i].Status = "delivering"
		current.IntegrationDeliveries[i].UpdatedAt = time.Now().UTC()
		return nil
	})
	if claimErr != nil {
		return domain.IntegrationDelivery{}, claimErr
	}
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, requestErr := http.NewRequestWithContext(callCtx, http.MethodPost, endpoint, strings.NewReader(string(snapshot.Payload)))
	if requestErr != nil {
		return domain.IntegrationDelivery{}, fmt.Errorf("%w: invalid delivery request", errInvalid)
	}
	req.Header.Set("Content-Type", "application/json")
	// Sign outbound deliveries when a deployment secret is configured. The
	// timestamp and delivery id are part of the signed envelope so receivers
	// can reject replays while retaining idempotent retries.
	deliverySecret := strings.TrimSpace(connectionSnapshot.Config["deliverySecret"])
	if envName := strings.TrimSpace(connectionSnapshot.Config["deliverySecretEnv"]); strings.HasPrefix(envName, "FLOW_INTEGRATION_") {
		deliverySecret = strings.TrimSpace(os.Getenv(envName))
	}
	if deliverySecret == "" {
		for _, suffix := range []string{"DELIVERY_SECRET", "WEBHOOK_SECRET"} {
			if value := strings.TrimSpace(os.Getenv("FLOW_INTEGRATION_" + strings.ToUpper(connectionSnapshot.Provider) + "_" + suffix)); value != "" {
				deliverySecret = value
				break
			}
		}
	}
	if deliverySecret != "" {
		timestamp := fmt.Sprint(time.Now().UTC().Unix())
		envelope := timestamp + "." + string(snapshot.Payload)
		mac := hmac.New(sha256.New, []byte(deliverySecret))
		_, _ = mac.Write([]byte(envelope))
		req.Header.Set("X-Flow-Timestamp", timestamp)
		req.Header.Set("X-Flow-Delivery", snapshot.ID)
		req.Header.Set("X-Flow-Event", snapshot.EventType)
		req.Header.Set("X-Flow-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	if envName := connectionSnapshot.Config["deliveryTokenEnv"]; strings.HasPrefix(envName, "FLOW_INTEGRATION_") {
		if token := os.Getenv(envName); token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	client := secureOutboundClient(10 * time.Second)
	if s.authDisabled && safeLocalDevelopmentURL(endpoint) {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, callErr := client.Do(req)
	status, lastError := "delivered", ""
	if callErr != nil {
		status, lastError = "failed", truncateSecurityError(callErr.Error())
	} else {
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			status, lastError = "failed", fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
	}
	var result domain.IntegrationDelivery
	persistCtx, persistCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer persistCancel()
	err := s.store.MutateWorkspace(persistCtx, workspace, "integration_delivery.retry", id, nil, func(data *domain.Bootstrap) error {
		i := slices.IndexFunc(data.IntegrationDeliveries, func(v domain.IntegrationDelivery) bool { return v.ID == id })
		if i < 0 {
			return errNotFound
		}
		d := &data.IntegrationDeliveries[i]
		if d.Attempts != snapshot.Attempts || d.Status != "delivering" {
			return errors.New("delivery changed while retrying")
		}
		connectionIndex := slices.IndexFunc(data.IntegrationConnections, func(v domain.IntegrationConnection) bool { return v.ID == d.ConnectionID })
		if connectionIndex < 0 {
			return errNotFound
		}
		connection := &data.IntegrationConnections[connectionIndex]
		d.Attempts++
		now := time.Now().UTC()
		d.UpdatedAt = now
		connection.DeliveryAttempts++
		connection.LastDeliveryAt = &now
		d.Status, d.LastError = status, lastError
		if d.Status == "failed" {
			next := now.Add(time.Duration(1<<min(d.Attempts, 6)) * time.Minute)
			d.NextAttemptAt = &next
			connection.LastError = d.LastError
		} else {
			d.NextAttemptAt = nil
			connection.LastError = ""
		}
		result = *d
		return nil
	})
	return result, err
}

func truncateSecurityError(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 512 {
		return value[:512]
	}
	return value
}

func safeOutboundHTTPS(ctx context.Context, raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return false
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIPAddr(lookupCtx, u.Hostname())
	if err != nil || len(ips) == 0 {
		return false
	}
	for _, item := range ips {
		ip := item.IP
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return false
		}
	}
	return true
}

func safeLocalDevelopmentURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	if strings.EqualFold(u.Hostname(), "localhost") {
		return true
	}
	ip := net.ParseIP(u.Hostname())
	return ip != nil && ip.IsLoopback()
}

func secureOutboundClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, entry := range ips {
			ip := entry.IP
			if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
				continue
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		}
		return nil, errors.New("outbound destination does not resolve to a public address")
	}
	return &http.Client{Timeout: timeout, Transport: transport, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 || !safeOutboundHTTPS(req.Context(), req.URL.String()) {
			return errors.New("unsafe redirect")
		}
		return nil
	}}
}
