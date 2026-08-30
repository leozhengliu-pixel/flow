package main

import (
	"net/http"
	"path/filepath"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func enterpriseTestServer(t *testing.T) (http.Handler, *store.SQLiteStore) {
	t.Helper()
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	return newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true, allowedOrigin: "http://flow.test"}), repository
}

func TestEnterpriseIdentityGitAndDeliveryPersistence(t *testing.T) {
	handler, repository := enterpriseTestServer(t)
	bootstrap, _ := repository.BootstrapFor("test-workspace")
	provider := requestJSON[domain.IdentityProvider](t, handler, http.MethodPost, "/api/identity-providers?workspace=test-workspace", map[string]any{
		"type": "oidc", "name": "Corporate SSO", "issuer": "https://id.example.com", "clientId": "flow", "clientSecretEnv": "FLOW_IDP_TEST_SECRET", "domains": []string{"Example.COM"}, "enabled": true,
	}, http.StatusCreated)
	if provider.Domains[0] != "example.com" || provider.DiscoveryStatus != "unverified" {
		t.Fatalf("provider not normalized: %#v", provider)
	}
	requestJSON[domain.IdentityProvider](t, handler, http.MethodPatch, "/api/identity-providers/"+provider.ID+"?workspace=test-workspace", map[string]any{"enforced": true}, http.StatusOK)

	team := bootstrap.Teams[0]
	state := bootstrap.States[0]
	automation := requestJSON[domain.GitAutomationState](t, handler, http.MethodPost, "/api/git-automations?workspace=test-workspace", map[string]any{"teamId": team.ID, "repository": "acme/api", "event": "pull_request.merged", "workflowStateId": state.ID, "syncComments": true, "enabled": true}, http.StatusOK)
	if automation.ID == "" || !automation.SyncComments {
		t.Fatalf("automation not persisted: %#v", automation)
	}
	branch := requestJSON[domain.TargetBranch](t, handler, http.MethodPost, "/api/target-branches?workspace=test-workspace", map[string]any{"teamId": team.ID, "repository": "acme/api", "branch": "main", "default": true}, http.StatusOK)
	if branch.ID == "" || !branch.Default {
		t.Fatalf("branch not persisted: %#v", branch)
	}

	connection := requestJSON[domain.IntegrationConnection](t, handler, http.MethodPut, "/api/integrations/slack?workspace=test-workspace", map[string]any{"name": "Slack", "config": map[string]string{"deliveryURL": "https://hooks.example.com/flow"}, "scopes": []string{"events:write"}, "channels": []string{"engineering"}, "linkbackEnabled": true}, http.StatusOK)
	delivery := requestJSON[domain.IntegrationDelivery](t, handler, http.MethodPost, "/api/integration-deliveries?workspace=test-workspace", map[string]any{"connectionId": connection.ID, "eventType": "issue.updated", "resourceId": "issue_1", "channel": "engineering", "payload": map[string]any{"id": "issue_1"}}, http.StatusAccepted)
	if delivery.Status != "pending" || delivery.Channel != "engineering" {
		t.Fatalf("delivery not queued: %#v", delivery)
	}

	updated, _ := repository.BootstrapFor("test-workspace")
	if len(updated.IdentityProviders) != 1 || len(updated.GitAutomationStates) != 1 || len(updated.TargetBranches) != 1 || len(updated.IntegrationDeliveries) != 1 {
		t.Fatalf("enterprise state missing after persistence")
	}
}

func TestIdentityProviderValidationAndPublicDiscovery(t *testing.T) {
	handler, _ := enterpriseTestServer(t)
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/identity-providers?workspace=test-workspace", map[string]any{"type": "oidc", "name": "Bad", "issuer": "http://insecure.example", "clientId": "x"}, http.StatusUnprocessableEntity)
	items := requestJSON[[]map[string]string](t, handler, http.MethodGet, "/api/auth/discovery?email=user@example.com", nil, http.StatusOK)
	if len(items) != 0 {
		t.Fatalf("unverified provider must not be discoverable")
	}
}
