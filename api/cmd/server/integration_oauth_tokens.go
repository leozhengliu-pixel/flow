package main

import (
	"context"
	"log"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Integration OAuth tokens live in the encrypted connector secret store,
// keyed by the workspace URL key and the connection id; the connection in the
// workspace document only carries OAuthAuthorized and the expiry. Readers
// fetch the tokens here when they make a provider call.

// integrationOAuthKeyMissing is returned before an OAuth integration flow
// starts when the tokens it would obtain could not be stored.
const integrationOAuthKeyMissing = "Configure FLOW_CONNECTOR_SECRET_KEY before connecting integrations with OAuth"

// integrationWorkspace is the workspace URL key integration tokens are stored
// under (fallback when the snapshot does not carry one).
func integrationWorkspace(data domain.Bootstrap, fallback string) string {
	if data.Workspace.URLKey != "" {
		return data.Workspace.URLKey
	}
	return fallback
}

// integrationOAuthTokens returns a connection's stored tokens. ok is false
// when the connection has none or they cannot be read (no key, or a key that
// no longer opens them); the reason is logged without the tokens.
func (s *server) integrationOAuthTokens(ctx context.Context, workspace string, connection domain.IntegrationConnection) (store.IntegrationOAuthTokens, bool) {
	if !connection.OAuthAuthorized {
		return store.IntegrationOAuthTokens{}, false
	}
	tokens, found, err := s.store.IntegrationOAuthTokens(ctx, workspace, connection.ID)
	if err != nil {
		log.Printf("integration %s (%s): OAuth tokens are not readable: %v", connection.ID, connection.Provider, err)
		return store.IntegrationOAuthTokens{}, false
	}
	return tokens, found
}

// integrationAccessToken is the connection's stored OAuth access token, or "".
func (s *server) integrationAccessToken(ctx context.Context, workspace string, connection domain.IntegrationConnection) string {
	tokens, _ := s.integrationOAuthTokens(ctx, workspace, connection)
	return tokens.AccessToken
}
