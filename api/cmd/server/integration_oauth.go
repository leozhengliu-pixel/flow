package main

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"flow/api/internal/domain"
)

func supportedIntegration(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "github", "gitlab", "slack", "jira", "figma":
		return true
	default:
		return false
	}
}

// figmaOAuthDefaults fills public Figma OAuth endpoints when unset. Client
// secrets still come from FLOW_INTEGRATION_FIGMA_* env or connection config.
func applyIntegrationOAuthDefaults(provider string, cfg *integrationOAuthConfig) {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "figma":
		if cfg.AuthorizationURL == "" {
			cfg.AuthorizationURL = "https://www.figma.com/oauth"
		}
		if cfg.TokenURL == "" {
			cfg.TokenURL = "https://api.figma.com/v1/oauth/token"
		}
		if cfg.RedirectURI == "" {
			appURL := strings.TrimRight(os.Getenv("FLOW_APP_URL"), "/")
			if appURL == "" {
				appURL = "http://localhost:5173"
			}
			cfg.RedirectURI = appURL + "/connect/figma/callback"
		}
	case "jira":
		// Atlassian Cloud defaults (LS-0364). Never invent success without secrets.
		if cfg.AuthorizationURL == "" {
			cfg.AuthorizationURL = "https://auth.atlassian.com/authorize"
		}
		if cfg.TokenURL == "" {
			cfg.TokenURL = "https://auth.atlassian.com/oauth/token"
		}
	}
}

func integrationOAuthCompletePath(provider, workspace, status, errMsg string) string {
	appURL := strings.TrimRight(os.Getenv("FLOW_APP_URL"), "/")
	if appURL == "" {
		appURL = "http://localhost:5173"
	}
	path := "/connect/oauth/complete"
	if strings.EqualFold(provider, "figma") {
		path = "/connect/figma/callback"
	}
	u, err := url.Parse(appURL + path)
	if err != nil {
		return appURL + path
	}
	q := u.Query()
	if provider != "" {
		q.Set("provider", provider)
	}
	if workspace != "" {
		q.Set("workspace", workspace)
	}
	if status != "" {
		q.Set("status", status)
	}
	if errMsg != "" {
		q.Set("error", errMsg)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func wantsHTMLRedirect(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	return strings.Contains(accept, "text/html") || (!strings.Contains(accept, "application/json") && strings.Contains(r.Header.Get("User-Agent"), "Mozilla"))
}

func supportedIntegrationHandler(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !supportedIntegration(r.PathValue("provider")) {
			writeError(w, http.StatusNotFound, "Unsupported integration provider")
			return
		}
		next(w, r)
	}
}

// integrationOAuthConfig resolves OAuth metadata from the connection first and
// then from deployment environment variables. Secrets are intentionally read
// from environment only; they are never persisted in workspace state.
type integrationOAuthConfig struct {
	AuthorizationURL string
	TokenURL         string
	RevokeURL        string
	ClientID         string
	ClientSecret     string
	RedirectURI      string
}

func integrationOAuthConfigFor(provider string, config map[string]string) integrationOAuthConfig {
	provider = strings.ToUpper(strings.TrimSpace(provider))
	value := func(key string, envSuffix string) string {
		if config != nil {
			if v := strings.TrimSpace(config[key]); v != "" {
				return v
			}
		}
		return strings.TrimSpace(os.Getenv("FLOW_INTEGRATION_" + provider + "_" + envSuffix))
	}
	secret := ""
	if config != nil {
		if envName := strings.TrimSpace(config["clientSecretEnv"]); strings.HasPrefix(envName, "FLOW_INTEGRATION_") {
			secret = strings.TrimSpace(os.Getenv(envName))
		}
	}
	if secret == "" {
		secret = value("clientSecret", "CLIENT_SECRET")
	}
	cfg := integrationOAuthConfig{
		AuthorizationURL: value("authorizationURL", "AUTHORIZATION_URL"),
		TokenURL:         value("tokenURL", "TOKEN_URL"),
		RevokeURL:        value("revokeURL", "REVOKE_URL"),
		ClientID:         value("clientID", "CLIENT_ID"),
		ClientSecret:     secret,
		RedirectURI:      value("redirectURI", "REDIRECT_URI"),
	}
	applyIntegrationOAuthDefaults(provider, &cfg)
	return cfg
}

func integrationEndpoint(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return errors.New("OAuth endpoint must be an HTTP(S) URL")
	}
	return nil
}

func integrationEndpointSafe(ctx context.Context, raw string, allowLocal bool) bool {
	if allowLocal && safeLocalDevelopmentURL(raw) {
		return true
	}
	return safeOutboundHTTPS(ctx, raw)
}

func integrationOAuthUnavailable(field string) error {
	return errors.New("OAuth integration is unavailable: " + field + " is not configured")
}

func persistOAuthIntegrationError(s *server, r *http.Request, provider, id, message string) {
	if s == nil || s.store == nil || id == "" {
		return
	}
	_ = s.store.MutateWorkspace(r.Context(), workspaceKey(r), "integration.oauth_error", id, map[string]string{"provider": provider, "error": message}, func(data *domain.Bootstrap) error {
		for i := range data.IntegrationConnections {
			if data.IntegrationConnections[i].ID == id && data.IntegrationConnections[i].Provider == provider {
				data.IntegrationConnections[i].LastError = message
				data.IntegrationConnections[i].UpdatedAt = time.Now().UTC()
				return nil
			}
		}
		return errNotFound
	})
}
