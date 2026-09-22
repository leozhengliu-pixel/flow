package main

import (
	"net/http"
	"path/filepath"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestJiraOAuthConfigAndSyncLinkLifecycle(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "jira.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})

	// Unsupported without connect still 404 via supportedIntegrationHandler for removed providers.
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/integrations/figma/oauth/start?workspace=test-workspace", nil, http.StatusNotFound)

	// Custom personal requires OAuth endpoint fields.
	requestJSON[map[string]any](t, handler, http.MethodPut, "/api/integrations/jira?workspace=test-workspace", map[string]any{
		"name":   "Jira",
		"config": map[string]string{"mode": "custom_personal"},
	}, http.StatusUnprocessableEntity)

	connection := requestJSON[domain.IntegrationConnection](t, handler, http.MethodPut, "/api/integrations/jira?workspace=test-workspace", map[string]any{
		"name": "Jira Cloud",
		"config": map[string]string{
			"mode": "cloud",
		},
	}, http.StatusOK)
	if connection.Provider != "jira" || connection.Status != "configured" {
		t.Fatalf("unexpected connection: %+v", connection)
	}
	if len(connection.Scopes) == 0 {
		t.Fatal("expected default jira scopes")
	}

	// OAuth start without deployment secrets is an honest unavailable error.
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/integrations/jira/oauth/start?workspace=test-workspace", nil, http.StatusServiceUnavailable)

	// Sync link requires a connected OAuth token — configured-only is not enough.
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/jira/links?workspace=test-workspace", map[string]any{
		"jiraProjectId": "10000",
		"teamId":        "missing",
		"syncDirection": "bidirectional",
	}, http.StatusConflict)

	// Mark connection connected with a token via workspace mutate fixture.
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap?workspace=test-workspace", nil, http.StatusOK)
	teamID := ""
	if len(bootstrap.Teams) > 0 {
		teamID = bootstrap.Teams[0].ID
	}
	if teamID == "" {
		t.Fatal("expected fixture team")
	}
	err = repository.MutateWorkspace(t.Context(), "test-workspace", "jira.fixture_connected", connection.ID, nil, func(data *domain.Bootstrap) error {
		for i := range data.IntegrationConnections {
			if data.IntegrationConnections[i].ID == connection.ID {
				now := data.IntegrationConnections[i].UpdatedAt
				data.IntegrationConnections[i].Status = "connected"
				data.IntegrationConnections[i].OAuthAccessToken = "atlassian-token"
				data.IntegrationConnections[i].OAuthCompletedAt = &now
				data.IntegrationConnections[i].LastError = ""
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	remote := requestJSON[map[string]any](t, handler, http.MethodGet, "/api/jira/remote/projects?workspace=test-workspace", nil, http.StatusOK)
	if remote["status"] != "unavailable" {
		t.Fatalf("expected unavailable remote projects, got %#v", remote)
	}

	link := requestJSON[domain.JiraLink](t, handler, http.MethodPost, "/api/jira/links?workspace=test-workspace", map[string]any{
		"jiraProjectId":   "10000",
		"jiraProjectKey":  "OPS",
		"jiraProjectName": "Operations",
		"teamId":          teamID,
		"syncDirection":   "unidirectional",
		"statusMap":       map[string]string{"Done": "state-completed"},
	}, http.StatusCreated)
	if link.JiraProjectID != "10000" || link.SyncDirection != "unidirectional" || link.TeamID != teamID {
		t.Fatalf("unexpected link: %+v", link)
	}

	updated := requestJSON[domain.JiraLink](t, handler, http.MethodPatch, "/api/jira/links/"+link.ID+"?workspace=test-workspace", map[string]any{
		"syncDirection": "bidirectional",
	}, http.StatusOK)
	if updated.SyncDirection != "bidirectional" {
		t.Fatalf("expected bidirectional, got %s", updated.SyncDirection)
	}

	statuses := requestJSON[map[string]any](t, handler, http.MethodGet, "/api/jira/remote/projects/10000/statuses?workspace=test-workspace", nil, http.StatusOK)
	if statuses["status"] != "unavailable" {
		t.Fatalf("expected unavailable statuses, got %#v", statuses)
	}

	requestJSON[any](t, handler, http.MethodDelete, "/api/integrations/jira?workspace=test-workspace", nil, http.StatusNoContent)
	links := requestJSON[[]domain.JiraLink](t, handler, http.MethodGet, "/api/jira/links?workspace=test-workspace", nil, http.StatusOK)
	if len(links) != 0 {
		t.Fatalf("expected links cleared after disconnect, got %#v", links)
	}
}

func TestJiraCustomPersonalConnectStoresEndpoints(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "jira-custom.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	connection := requestJSON[domain.IntegrationConnection](t, handler, http.MethodPut, "/api/integrations/jira?workspace=test-workspace", map[string]any{
		"name": "Jira Server",
		"config": map[string]string{
			"mode":             "custom_personal",
			"authorizationURL": "https://jira.example.test/oauth/authorize",
			"tokenURL":         "https://jira.example.test/oauth/token",
			"clientID":         "flow-jira",
			"redirectURI":      "https://flow.example.test/api/integrations/jira/oauth/callback",
			"clientSecretEnv":  "FLOW_INTEGRATION_JIRA_CLIENT_SECRET",
			"siteURL":          "https://jira.example.test",
		},
	}, http.StatusOK)
	if connection.Config["mode"] != "custom_personal" || connection.Config["clientID"] != "flow-jira" {
		t.Fatalf("unexpected config: %#v", connection.Config)
	}
	if _, ok := connection.Config["clientSecret"]; ok {
		t.Fatal("clientSecret must not be persisted")
	}
}
