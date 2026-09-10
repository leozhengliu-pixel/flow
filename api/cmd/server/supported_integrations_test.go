package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestRemovedIntegrationsCannotConnectOrExecute(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	removed := []string{"codex", "cursor", "zapier", "notion", "figma", "intercom", "sentry", "google-calendar", "google"}
	err = repository.MutateWorkspace(context.Background(), "test-workspace", "integration.fixture", "legacy", nil, func(data *domain.Bootstrap) error {
		for _, provider := range removed {
			data.IntegrationConnections = append(data.IntegrationConnections, domain.IntegrationConnection{ID: provider, Provider: provider, Status: "connected"})
		}
		data.IntegrationConnections = append(data.IntegrationConnections, domain.IntegrationConnection{ID: "github", Provider: "github", Status: "configured"})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	for _, provider := range removed {
		t.Run(provider, func(t *testing.T) {
			base := "/api/integrations/" + provider
			for _, endpoint := range []struct{ method, path string }{
				{http.MethodPut, base}, {http.MethodPost, base + "/configure"},
				{http.MethodPost, base + "/actions"}, {http.MethodGet, base + "/jobs"},
				{http.MethodPost, base + "/oauth/start"}, {http.MethodGet, base + "/oauth/callback?state=legacy&code=unused"},
				{http.MethodPost, base + "/" + provider + "/oauth/refresh"}, {http.MethodPost, base + "/webhook"},
			} {
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, httptest.NewRequest(endpoint.method, endpoint.path, nil))
				if response.Code != http.StatusNotFound && response.Code != http.StatusMethodNotAllowed {
					t.Fatalf("Removed endpoint %s %s returned %d", endpoint.method, endpoint.path, response.Code)
				}
			}
		})
	}
	connections := requestJSON[[]domain.IntegrationConnection](t, handler, http.MethodGet, "/api/integrations", nil, http.StatusOK)
	for _, connection := range connections {
		if !supportedIntegration(connection.Provider) {
			t.Fatalf("Removed provider still listed: %s", connection.Provider)
		}
	}
}
