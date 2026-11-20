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
	"regexp"
	"slices"
	"strings"
	"time"

	"flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/store"

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
	State, Nonce, Verifier, Provider, ClientKey string
	// Link marks a round trip started from account settings to attach the
	// identity to the signed-in account; LinkWorkspace is where to return.
	Link          bool
	LinkWorkspace string
}

// identityConflictMessage is the sign-in error for an identity whose email
// belongs to an account the provider may not claim.
const identityConflictMessage = "An account with this email already exists. Sign in to it and link this provider from your account settings."

var linkWorkspacePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// oidcEmailVerified is true only for an explicit email_verified: true (or
// "true", which some providers send as a string).
func oidcEmailVerified(claims map[string]any) bool {
	switch value := claims["email_verified"].(type) {
	case bool:
		return value
	case string:
		return strings.EqualFold(strings.TrimSpace(value), "true")
	}
	return false
}

// linkSessionUser is the account whose live session completes a link round
// trip: the proof of ownership the email match lacks.
func (s *server) linkSessionUser(r *http.Request) (domain.User, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return domain.User{}, false
	}
	user, err := s.store.AuthenticateSession(r.Context(), cookie.Value)
	return user, err == nil && user.ID != ""
}

func linkSettingsRedirect(appURL, workspace string) string {
	base := strings.TrimRight(appURL, "/")
	if workspace == "" || !linkWorkspacePattern.MatchString(workspace) {
		return base + "/"
	}
	return base + "/" + url.PathEscape(workspace) + "/settings/account/connections"
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
	if options.IDPMetadata == nil || strings.TrimSpace(options.IDPMetadata.EntityID) == "" {
		return nil, fmt.Errorf("SAML IdP metadata has no entityID")
	}
	idpMetadataResult, err = samlsp.New(options)
	if err != nil {
		return nil, err
	}
	// Assertions must come from this IdP and carry a user NameID; see
	// saml_assertion_guard.go (GO-2026-6909).
	idpMetadataResult.AssertionHandler = samlAssertionPolicy{idpEntityID: options.IDPMetadata.EntityID}
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
	response := map[string]any{"email": email, "providers": providers}
	if s.defaultLanguage != "" {
		// The operator's default language (defaults.user.language) for the
		// sign-in pages of a browser that has not chosen one.
		response["defaultLanguage"] = s.defaultLanguage
	}
	writeJSON(w, http.StatusOK, response)
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
	s.beginOIDC(w, r, false)
}

// startOIDCLink starts the provider round trip for a signed-in account that
// links the identity from its settings (GET /api/account/identities/link/{provider}/start).
func (s *server) startOIDCLink(w http.ResponseWriter, r *http.Request) {
	s.beginOIDC(w, r, true)
}

func (s *server) beginOIDC(w http.ResponseWriter, r *http.Request, link bool) {
	providerID := r.PathValue("provider")
	if s.externalAuth == nil || s.externalAuth.providers[providerID] == nil {
		http.NotFound(w, r)
		return
	}
	state := randomURLToken(32)
	nonce := randomURLToken(32)
	verifier := randomURLToken(48)
	clientKey := strings.TrimSpace(r.URL.Query().Get("client_key"))
	external := externalAuthState{State: state, Nonce: nonce, Verifier: verifier, Provider: providerID, ClientKey: clientKey}
	if link {
		if authUser(r).ID == "" {
			writeError(w, http.StatusUnauthorized, "Sign in required")
			return
		}
		external.ClientKey, external.Link = "", true
		if workspace := strings.TrimSpace(r.URL.Query().Get("workspace")); linkWorkspacePattern.MatchString(workspace) {
			external.LinkWorkspace = workspace
		}
	}
	encoded := encodeExternalState(external)
	http.SetCookie(w, &http.Cookie{Name: externalAuthCookie, Value: encoded, Path: "/api/auth/", HttpOnly: true, Secure: secureCookie(r), SameSite: http.SameSiteLaxMode, MaxAge: 600})
	challenge := sha256.Sum256([]byte(verifier))
	redirect := s.externalAuth.providers[providerID].oauth.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.SetAuthURLParam("code_challenge", base64.RawURLEncoding.EncodeToString(challenge[:])), oauth2.SetAuthURLParam("code_challenge_method", "S256"))
	http.Redirect(w, r, redirect, http.StatusFound)
}

func (s *server) finishOIDC(w http.ResponseWriter, r *http.Request) {
	providerID := r.PathValue("provider")
	appURL := ""
	if s.externalAuth != nil {
		appURL = s.externalAuth.appURL
	}
	client := (*oidcClient)(nil)
	if s.externalAuth != nil {
		client = s.externalAuth.providers[providerID]
	}
	state, err := readExternalState(r)
	if client == nil || err != nil || state.Provider != providerID || state.State != r.URL.Query().Get("state") {
		redirectAuthError(w, r, appURL, "invalid authentication state")
		return
	}
	token, err := client.oauth.Exchange(r.Context(), r.URL.Query().Get("code"), oauth2.SetAuthURLParam("code_verifier", state.Verifier))
	if err != nil {
		redirectAuthError(w, r, appURL, "identity provider token exchange failed")
		return
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		redirectAuthError(w, r, appURL, "identity provider did not return an ID token")
		return
	}
	idToken, err := client.verifier.Verify(r.Context(), rawIDToken)
	if err != nil {
		redirectAuthError(w, r, appURL, "invalid identity token")
		return
	}
	var claims map[string]any
	if idToken.Claims(&claims) != nil {
		redirectAuthError(w, r, appURL, "identity is not allowed")
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
	// The email is used to find or create an account only when the provider
	// states email_verified: true; an absent claim is not verified.
	claimedEmail := stringClaim(claims, "email")
	email, emailVerified := claimedEmail, oidcEmailVerified(claims)
	name := stringClaim(claims, "name")
	if name == "" {
		name = stringClaim(claims, "preferred_username")
	}
	picture := stringClaim(claims, "picture")
	if subject == "" || nonce != state.Nonce || (claimedEmail != "" && !allowedExternalEmail(claimedEmail, s.externalAuth.config.AllowedDomains)) {
		s.recordSignInFailure(r, claimedEmail, "identity_not_allowed")
		redirectAuthError(w, r, appURL, "identity is not allowed")
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
	if state.Link {
		user, ok := s.linkSessionUser(r)
		if !ok {
			redirectAuthError(w, r, appURL, "Sign in to your account to link this provider.")
			return
		}
		if err := s.store.LinkExternalIdentity(r.Context(), user.ID, providerID, issuer, subject, username, string(claimsJSON)); err != nil {
			message := "could not link this identity"
			if errors.Is(err, store.ErrIdentityConflict) {
				message = "This identity is already linked to another account."
			}
			redirectAuthError(w, r, appURL, message)
			return
		}
		clearExternalState(w, r)
		http.Redirect(w, r, linkSettingsRedirect(appURL, state.LinkWorkspace), http.StatusFound)
		return
	}
	// A configured provider that states a verified email in an allowed
	// domain may attach to the account owning that email on first sign-in.
	policy := store.ExternalLinkPolicy{TrustedEmail: email != "" && emailVerified, UnverifiedEmail: !emailVerified}
	session, sessionToken, err := s.store.LoginExternalIdentityWithPolicy(r.Context(), providerID, issuer, subject, username, email, name, picture, string(claimsJSON), s.externalAuth.config.AutoProvision, policy)
	if err != nil {
		s.recordSignInFailure(r, email, signInFailureReason(err))
		if errors.Is(err, store.ErrIdentityConflict) {
			redirectAuthError(w, r, appURL, identityConflictMessage)
			return
		}
		redirectAuthError(w, r, appURL, "could not create Flow session")
		return
	}
	if err := s.store.SetSessionAuthentication(r.Context(), sessionToken, providerID, issuer, verifiedMFAClaim(claims)); err != nil {
		writeError(w, http.StatusInternalServerError, "could not save authentication context")
		return
	}
	if role := externalOIDCRole(s.externalAuth.config.OIDC, claims); role != "" {
		for _, membership := range session.Memberships {
			if s.scimManagesMember(r.Context(), membership.Workspace.ID, session.User.ID) {
				continue // SCIM provisioning owns this member's role
			}
			if err := s.store.UpdateMemberRole(r.Context(), membership.Workspace.ID, session.User.ID, role); err != nil {
				writeError(w, http.StatusForbidden, "could not apply identity role")
				return
			}
		}
	}
	clearExternalState(w, r)
	setSessionCookie(w, r, sessionToken, session.ExpiresAt)
	s.afterSignIn(w, r, session)
	http.Redirect(w, r, oidcSuccessRedirect(s.externalAuth.appURL, state, r), http.StatusFound)
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
	// SAML signs in by issuer and subject like every other provider. This IdP
	// is configured by the server operator, so it is a trusted provider: an
	// asserted email that passes the operator's domain restriction (checked
	// above, or no restriction) attaches to the account owning that email on
	// the first sign-in, and the issuer+subject identity row created then is
	// what later sign-ins use. Enterprise OIDC providers, configured by
	// workspace admins, keep the stricter rules.
	claims, _ := json.Marshal(map[string]any{"subject": subject, "name": name})
	identityIssuer := strings.TrimSpace(s.externalAuth.config.SAML.EntityID)
	if identityIssuer == "" {
		identityIssuer = "saml"
	}
	flowSession, token, err := s.store.LoginExternalIdentityWithPolicy(r.Context(), "saml", identityIssuer, subject, subject, email, name, "", string(claims), s.externalAuth.config.AutoProvision, store.ExternalLinkPolicy{TrustedEmail: email != ""})
	if err != nil {
		s.recordSignInFailure(r, email, signInFailureReason(err))
		if errors.Is(err, store.ErrIdentityConflict) {
			redirectAuthError(w, r, s.externalAuth.appURL, identityConflictMessage)
			return
		}
		writeError(w, http.StatusForbidden, "could not create Flow session")
		return
	}
	issuer := ""
	if s.externalAuth.saml != nil && s.externalAuth.saml.ServiceProvider.IDPMetadata != nil {
		issuer = s.externalAuth.saml.ServiceProvider.IDPMetadata.EntityID
	}
	if err := s.store.SetSessionAuthentication(r.Context(), token, "saml", issuer, false); err != nil {
		writeError(w, http.StatusInternalServerError, "could not save authentication context")
		return
	}
	setSessionCookie(w, r, token, flowSession.ExpiresAt)
	s.afterSignIn(w, r, flowSession)
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

func redirectAuthError(w http.ResponseWriter, r *http.Request, appURL, message string) {
	target := strings.TrimRight(appURL, "/") + "/auth/error?error=" + url.QueryEscape(message)
	if prefersJSONAuthError(r) {
		writeError(w, http.StatusBadRequest, message)
		return
	}
	http.Redirect(w, r, target, http.StatusFound)
}

func prefersJSONAuthError(r *http.Request) bool {
	accept := strings.ToLower(r.Header.Get("Accept"))
	return strings.Contains(accept, "application/json") && !strings.Contains(accept, "text/html")
}

func oidcSuccessRedirect(appURL string, state externalAuthState, r *http.Request) string {
	base := strings.TrimRight(appURL, "/")
	target := base + "/auth/google/callback"
	if state.Provider != "" && state.Provider != "google" {
		target = base + "/auth/" + url.PathEscape(state.Provider) + "/callback"
	}
	query := url.Values{}
	if state.ClientKey != "" {
		query.Set("client_key", state.ClientKey)
	}
	if mobile := strings.TrimSpace(r.URL.Query().Get("isMobileAppLogin")); mobile != "" {
		query.Set("isMobileAppLogin", mobile)
	}
	if redirectURI := strings.TrimSpace(r.URL.Query().Get("mobileRedirectUri")); redirectURI != "" {
		query.Set("mobileRedirectUri", redirectURI)
	}
	if encoded := query.Encode(); encoded != "" {
		return target + "?" + encoded
	}
	return target
}

func encodeExternalState(state externalAuthState) string {
	parts := []string{state.Provider, state.State, state.Nonce, state.Verifier}
	if state.ClientKey != "" || state.Link {
		parts = append(parts, state.ClientKey)
	}
	if state.Link {
		parts = append(parts, "link:"+state.LinkWorkspace)
	}
	return strings.Join(parts, ".")
}

func readExternalState(r *http.Request) (externalAuthState, error) {
	cookie, err := r.Cookie(externalAuthCookie)
	if err != nil {
		return externalAuthState{}, err
	}
	parts := strings.Split(cookie.Value, ".")
	if len(parts) < 4 || len(parts) > 6 {
		return externalAuthState{}, errors.New("invalid state cookie")
	}
	state := externalAuthState{Provider: parts[0], State: parts[1], Nonce: parts[2], Verifier: parts[3]}
	if len(parts) >= 5 {
		state.ClientKey = parts[4]
	}
	if len(parts) == 6 {
		if !strings.HasPrefix(parts[5], "link:") {
			return externalAuthState{}, errors.New("invalid state cookie")
		}
		state.Link, state.LinkWorkspace = true, strings.TrimPrefix(parts[5], "link:")
	}
	return state, nil
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
