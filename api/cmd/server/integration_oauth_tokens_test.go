package main

import (
	"context"
	"testing"

	"flow/api/internal/store"
)

// seedIntegrationOAuthTokens stores a connection's OAuth tokens the way the
// OAuth callback does (encrypted), enabling a test key when none is set.
func seedIntegrationOAuthTokens(t *testing.T, repository *store.SQLiteStore, workspaceKey, connectionID, access, refresh string) {
	t.Helper()
	if !store.ConnectorSecretsConfigured() {
		enableConnectorSecrets(t)
	}
	data, _ := repository.WorkspaceMetadata(workspaceKey)
	if err := repository.PutIntegrationOAuthTokens(context.Background(), integrationWorkspace(data, workspaceKey), connectionID, store.IntegrationOAuthTokens{AccessToken: access, RefreshToken: refresh}); err != nil {
		t.Fatal(err)
	}
}

// storedIntegrationOAuthTokens reads a connection's tokens from the store.
func storedIntegrationOAuthTokens(t *testing.T, repository *store.SQLiteStore, workspaceKey, connectionID string) store.IntegrationOAuthTokens {
	t.Helper()
	data, _ := repository.WorkspaceMetadata(workspaceKey)
	tokens, _, err := repository.IntegrationOAuthTokens(context.Background(), integrationWorkspace(data, workspaceKey), connectionID)
	if err != nil {
		t.Fatal(err)
	}
	return tokens
}
