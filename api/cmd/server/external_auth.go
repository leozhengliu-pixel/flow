package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"flow/api/internal/config"
	"flow/api/internal/domain"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/crewjam/saml/samlsp"
	"golang.org/x/oauth2"
)

const externalAuthCookie = "flow_external_auth"

type externalAuth struct {
	config    config.AuthConfig
	appURL    string
	providers map[string]*oidcClient
	saml      *samlsp.Middleware
}

type oidcClient struct {
	name     string
	issuer   string
	oauth    oauth2.Config
	verifier *oidc.IDTokenVerifier
}

type externalAuthState struct {
	State, Nonce, Verifier, Provider string
}

func newExternalAuth(ctx context.Context, authConfig config.AuthConfig, appURL string) (*externalAuth, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	result := &externalAuth{config: authConfig, appURL: strings.TrimRight(appURL, "/"), providers: map[string]*oidcClient{}}
	if authConfig.Google.Enabled {
		client, err := discoverOIDC(ctx, "Google", "https://accounts.google.com", authConfig.Google.ClientID, authConfig.Google.ClientSecret, authConfig.Google.RedirectURL, []string{oidc.ScopeOpenID, oidc.ScopeProfile, oidc.ScopeEmail})
		if err != nil {
			return nil, fmt.Errorf("configure Google OAuth: %w", err)
		}
		result.providers["google"] = client
	}
	if authConfig.OIDC.Enabled {
		client, err := discoverOIDC(ctx, authConfig.OIDC.DisplayName, authConfig.OIDC.IssuerURL, authConfig.OIDC.ClientID, authConfig.OIDC.ClientSecret, authConfig.OIDC.RedirectURL, authConfig.OIDC.Scopes)
		if err != nil {
			return nil, fmt.Errorf("configure OIDC: %w", err)
		}
		result.providers["oidc"] = client
	}
	if authConfig.SAML.Enabled {
		middleware, err := configureSAML(ctx, authConfig.SAML, result.appURL)
		if err != nil {
			return nil, fmt.Errorf("configure SAML: %w", err)
		}
		result.saml = middleware
	}
	return result, nil
}

func discoverOIDC(ctx context.Context, name, issuer, clientID, clientSecret, redirectURL string, scopes []string) (*oidcClient, error) {
	provider, err := oidc.NewProvider(ctx, strings.TrimRight(issuer, "/"))
	if err != nil {
		return nil, err
	}
	if !slices.Contains(scopes, oidc.ScopeOpenID) {
		scopes = append([]string{oidc.ScopeOpenID}, scopes...)
	}
	return &oidcClient{name: name, issuer: strings.TrimRight(issuer, "/"), oauth: oauth2.Config{ClientID: clientID, ClientSecret: clientSecret, RedirectURL: redirectURL, Endpoint: provider.Endpoint(), Scopes: scopes}, verifier: provider.Verifier(&oidc.Config{ClientID: clientID})}, nil
}

func configureSAML(ctx context.Context, provider config.SAMLProvider, appURL string) (*samlsp.Middleware, error) {
	key, err := parseRSAPrivateKey(provider.SPPrivateKey)
	if err != nil {
		return nil, err
	}
	certificate, err := parseCertificate(provider.SPCertificate)
	if err != nil {
		return nil, err
	}
	var metadataURL *url.URL
	var metadata []byte
	if provider.MetadataURL != "" {
		parsed, err := url.Parse(provider.MetadataURL)
		if err != nil || parsed.Scheme != "https" && parsed.Scheme != "http" {
			return nil, fmt.Errorf("invalid SAML metadata URL")
		}
		metadataURL = parsed
	} else {
		metadata = []byte(provider.MetadataXML)
	}
	var idpMetadataResult = (*samlsp.Middleware)(nil)
	baseURL, _ := url.Parse(strings.TrimRight(appURL, "/") + "/api/auth/")
	options := samlsp.Options{URL: *baseURL, EntityID: provider.EntityID, Key: key, Certificate: certificate, CookieSameSite: http.SameSiteLaxMode, DefaultRedirectURI: strings.TrimRight(appURL, "/") + "/api/auth/saml/complete"}
	if metadataURL != nil {
		options.IDPMetadata, err = samlsp.FetchMetadata(ctx, http.DefaultClient, *metadataURL)
		if err != nil {
			return nil, err
		}
	}
	if len(metadata) > 0 {
		options.IDPMetadata, err = samlsp.ParseMetadata(metadata)
		if err != nil {
			return nil, err
		}
	}
	idpMetadataResult, err = samlsp.New(options)
	if err != nil {
		return nil, err
	}
	if parsedACS, parseErr := url.Parse(provider.ACSURL); parseErr == nil && parsedACS.Host != "" {
		idpMetadataResult.ServiceProvider.AcsURL = *parsedACS
	}
	_ = ctx
	return idpMetadataResult, nil
}

func (s *server) authProviders(w http.ResponseWriter, _ *http.Request) {
	providers := []map[string]any{}
	email := true
	if s.externalAuth != nil {
		email = s.externalAuth.config.EmailEnabled
		if client := s.externalAuth.providers["google"]; client != nil {
			providers = append(providers, map[string]any{"id": "google", "name": client.name, "startUrl": "/api/auth/google/start"})
		}
		if client := s.externalAuth.providers["oidc"]; client != nil {
			providers = append(providers, map[string]any{"id": "oidc", "name": client.name, "startUrl": "/api/auth/oidc/start"})
		}
		if s.externalAuth.saml != nil {
			providers = append(providers, map[string]any{"id": "saml", "name": s.externalAuth.config.SAML.DisplayName, "startUrl": "/api/auth/saml/start"})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"email": email, "providers": providers})
}

func (s *server) requireEmailAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.externalAuth != nil && !s.externalAuth.config.EmailEnabled {
			writeError(w, http.StatusNotFound, "email authentication is disabled")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *server) startOIDC(w http.ResponseWriter, r *http.Request) {
	providerID := r.PathValue("provider")
	if s.externalAuth == nil || s.externalAuth.providers[providerID] == nil {
		http.NotFound(w, r)
		return
	}
	state := randomURLToken(32)
	nonce := randomURLToken(32)
	verifier := randomURLToken(48)
	encoded := encodeExternalState(externalAuthState{State: state, Nonce: nonce, Verifier: verifier, Provider: providerID})
	http.SetCookie(w, &http.Cookie{Name: externalAuthCookie, Value: encoded, Path: "/api/auth/", HttpOnly: true, Secure: secureCookie(r), SameSite: http.SameSiteLaxMode, MaxAge: 600})
	challenge := sha256.Sum256([]byte(verifier))
	redirect := s.externalAuth.providers[providerID].oauth.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.SetAuthURLParam("code_challenge", base64.RawURLEncoding.EncodeToString(challenge[:])), oauth2.SetAuthURLParam("code_challenge_method", "S256"))
	http.Redirect(w, r, redirect, http.StatusFound)
}

func (s *server) finishOIDC(w http.ResponseWriter, r *http.Request) {
	providerID := r.PathValue("provider")
	client := (*oidcClient)(nil)
	if s.externalAuth != nil {
		client = s.externalAuth.providers[providerID]
	}
	state, err := readExternalState(r)
	if client == nil || err != nil || state.Provider != providerID || state.State != r.URL.Query().Get("state") {
		writeError(w, http.StatusBadRequest, "invalid authentication state")
		return
	}
	token, err := client.oauth.Exchange(r.Context(), r.URL.Query().Get("code"), oauth2.SetAuthURLParam("code_verifier", state.Verifier))
	if err != nil {
		writeError(w, http.StatusBadGateway, "identity provider token exchange failed")
		return
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		writeError(w, http.StatusBadGateway, "identity provider did not return an ID token")
		return
	}
	idToken, err := client.verifier.Verify(r.Context(), rawIDToken)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid identity token")
		return
	}
	var claims map[string]any
	if idToken.Claims(&claims) != nil {
		writeError(w, http.StatusForbidden, "identity is not allowed")
		return
	}
	nonce := stringClaim(claims, "nonce")
	identityClaim := strings.TrimSpace(s.externalAuth.config.OIDC.IdentityClaim)
	if identityClaim == "" {
		identityClaim = "sub"
	}
	subject := stringClaim(claims, identityClaim)
	if subject == "" && identityClaim != "sub" {
		subject = stringClaim(claims, "sub")
	}
	email := stringClaim(claims, "email")
	if email != "" {
		if verified, present := claims["email_verified"]; present {
			if verifiedValue, ok := verified.(bool); ok && !verifiedValue {
				email = ""
			}
		}
	}
	name := stringClaim(claims, "name")
	if name == "" {
		name = stringClaim(claims, "preferred_username")
	}
	picture := stringClaim(claims, "picture")
	if subject == "" || nonce != state.Nonce || (email != "" && !allowedExternalEmail(email, s.externalAuth.config.AllowedDomains)) {
		writeError(w, http.StatusForbidden, "identity is not allowed")
		return
	}
	username := stringClaim(claims, "preferred_username")
	if identityClaim != "sub" {
		username = subject
	}
	claimsJSON, _ := json.Marshal(claims)
	issuer := strings.TrimRight(idToken.Issuer, "/")
	if issuer == "" {
		issuer = client.issuer
	}
	session, sessionToken, err := s.store.LoginExternalIdentity(r.Context(), providerID, issuer, subject, username, email, name, picture, string(claimsJSON), s.externalAuth.config.AutoProvision)
	if err != nil {
		writeError(w, http.StatusForbidden, "could not create Flow session")
		return
	}
	if role := externalOIDCRole(s.externalAuth.config.OIDC, claims); role != "" {
		for _, membership := range session.Memberships {
			if err := s.store.UpdateMemberRole(r.Context(), membership.Workspace.ID, session.User.ID, role); err != nil {
				writeError(w, http.StatusForbidden, "could not apply identity role")
				return
			}
		}
	}
	clearExternalState(w, r)
	setSessionCookie(w, r, sessionToken, session.ExpiresAt)
	http.Redirect(w, r, s.externalAuth.appURL, http.StatusFound)
}

func externalOIDCRole(provider config.OIDCProvider, claims map[string]any) string {
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
		role = strings.ToLower(strings.TrimSpace(provider.DefaultRole))
	}
	if !identityRoleValid(role) {
		return ""
	}
	return role
}

func stringClaim(claims map[string]any, name string) string {
	value, ok := claims[name]
	if !ok {
		return ""
	}
	stringValue, _ := value.(string)
	return strings.TrimSpace(stringValue)
}

func (s *server) startSAML(w http.ResponseWriter, r *http.Request) {
	if s.externalAuth == nil || s.externalAuth.saml == nil {
		http.NotFound(w, r)
		return
	}
	redirect := r.Clone(r.Context())
	redirect.URL.Path = "/api/auth/saml/complete"
	s.externalAuth.saml.HandleStartAuthFlow(w, redirect)
}

func (s *server) finishSAML(w http.ResponseWriter, r *http.Request) {
	session := samlsp.SessionFromContext(r.Context())
	attributes, ok := session.(samlsp.SessionWithAttributes)
	if !ok {
		writeError(w, http.StatusUnauthorized, "SAML session has no attributes")
		return
	}
	values := attributes.GetAttributes()
	email := firstAttribute(values, "email", "mail", "http://schemas.xmlsoap.org/ws/2005/05/identity/claims/emailaddress")
	name := firstAttribute(values, "name", "displayName", "http://schemas.xmlsoap.org/ws/2005/05/identity/claims/name")
	subject := firstAttribute(values, "uid", "employeeNumber", "employeeId", "userName", "username", "nameID", "nameid", "subject")
	if subject == "" {
		subject = email
	}
	if subject == "" || email != "" && !allowedExternalEmail(email, s.externalAuth.config.AllowedDomains) {
		writeError(w, http.StatusForbidden, "SAML identity is not allowed")
		return
	}
	var flowSession domain.AuthSession
	var token string
	var err error
	if email == "" {
		claims, _ := json.Marshal(map[string]any{"subject": subject, "name": name})
		issuer := strings.TrimSpace(s.externalAuth.config.SAML.EntityID)
		if issuer == "" {
			issuer = "saml"
		}
		flowSession, token, err = s.store.LoginExternalIdentity(r.Context(), "saml", issuer, subject, subject, "", name, "", string(claims), s.externalAuth.config.AutoProvision)
	} else {
		flowSession, token, err = s.store.LoginExternal(r.Context(), email, name, "", s.externalAuth.config.AutoProvision)
	}
	if err != nil {
		writeError(w, http.StatusForbidden, "could not create Flow session")
		return
	}
	setSessionCookie(w, r, token, flowSession.ExpiresAt)
	http.Redirect(w, r, s.externalAuth.appURL, http.StatusFound)
}

func firstAttribute(attributes samlsp.Attributes, names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(attributes.Get(name)); value != "" {
			return value
		}
	}
	return ""
}

func allowedExternalEmail(email string, domains []string) bool {
	if len(domains) == 0 {
		return true
	}
	parts := strings.Split(strings.ToLower(strings.TrimSpace(email)), "@")
	return len(parts) == 2 && slices.Contains(domains, parts[1])
}

func randomURLToken(size int) string {
	buffer := make([]byte, size)
	_, _ = rand.Read(buffer)
	return base64.RawURLEncoding.EncodeToString(buffer)
}

func encodeExternalState(state externalAuthState) string {
	return strings.Join([]string{state.Provider, state.State, state.Nonce, state.Verifier}, ".")
}

func readExternalState(r *http.Request) (externalAuthState, error) {
	cookie, err := r.Cookie(externalAuthCookie)
	if err != nil {
		return externalAuthState{}, err
	}
	parts := strings.Split(cookie.Value, ".")
	if len(parts) != 4 {
		return externalAuthState{}, errors.New("invalid state cookie")
	}
	return externalAuthState{Provider: parts[0], State: parts[1], Nonce: parts[2], Verifier: parts[3]}, nil
}

func clearExternalState(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: externalAuthCookie, Path: "/api/auth/", HttpOnly: true, Secure: secureCookie(r), SameSite: http.SameSiteLaxMode, MaxAge: -1})
}

func parseRSAPrivateKey(raw string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(raw))
	if block == nil {
		return nil, fmt.Errorf("invalid SAML SP private key")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("invalid SAML SP private key")
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("SAML SP private key must be RSA")
	}
	return rsaKey, nil
}

func parseCertificate(raw string) (*x509.Certificate, error) {
	block, _ := pem.Decode([]byte(raw))
	if block == nil {
		return nil, fmt.Errorf("invalid SAML SP certificate")
	}
	return x509.ParseCertificate(block.Bytes)
}
