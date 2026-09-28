package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestFigmaIsSupportedIntegration(t *testing.T) {
	if !supportedIntegration("figma") {
		t.Fatal("figma should be a supported OAuth integration")
	}
	if supportedIntegration("sentry") {
		t.Fatal("sentry stays MCP-only until App OAuth exists")
	}
}

func TestIntegrationOAuthCompletePath(t *testing.T) {
	t.Setenv("FLOW_APP_URL", "https://flow.example.test")
	figma := integrationOAuthCompletePath("figma", "acme", "connected", "")
	if !strings.Contains(figma, "/connect/figma/callback") || !strings.Contains(figma, "status=connected") {
		t.Fatalf("figma complete path = %s", figma)
	}
	generic := integrationOAuthCompletePath("slack", "acme", "error", "access_denied")
	if !strings.Contains(generic, "/connect/oauth/complete") || !strings.Contains(generic, "error=access_denied") {
		t.Fatalf("generic complete path = %s", generic)
	}
}

func TestFigmaOAuthFinishJSONAndHTMLRedirect(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()

	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"figma-token","refresh_token":"figma-refresh","expires_in":3600}`))
	}))
	defer tokenServer.Close()

	t.Setenv("FLOW_APP_URL", "https://flow.example.test")
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	_ = requestJSON[domain.IntegrationConnection](t, handler, http.MethodPut, "/api/integrations/figma?workspace=test-workspace", map[string]any{
		"name": "Figma",
		"config": map[string]string{
			"authorizationURL": "https://www.figma.com/oauth",
			"tokenURL":         tokenServer.URL,
			"clientID":         "figma-client",
			"redirectURI":      "https://flow.example.test/connect/figma/callback",
		},
	}, http.StatusOK)

	started := requestJSON[map[string]string](t, handler, http.MethodPost, "/api/integrations/figma/oauth/start?workspace=test-workspace", nil, http.StatusOK)
	if started["state"] == "" {
		t.Fatal("expected oauth state")
	}
	if !strings.Contains(started["authorizationURL"], "file_content%3Aread") && !strings.Contains(started["authorizationURL"], "file_content:read") {
		t.Fatalf("expected figma scopes in %s", started["authorizationURL"])
	}

	finished := requestJSON[map[string]string](t, handler, http.MethodPost, "/api/integrations/figma/oauth/finish?workspace=test-workspace", map[string]any{
		"code":  "figma-code",
		"state": started["state"],
	}, http.StatusOK)
	if finished["status"] != "connected" {
		t.Fatalf("finish status=%#v", finished)
	}

	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/integrations/figma/oauth/finish?workspace=test-workspace", map[string]any{
		"code":  "figma-code",
		"state": started["state"],
	}, http.StatusNotFound)

	startedHTML := requestJSON[map[string]string](t, handler, http.MethodPost, "/api/integrations/figma/oauth/start?workspace=test-workspace", nil, http.StatusOK)
	req := httptest.NewRequest(http.MethodGet, "/api/integrations/figma/oauth/callback?workspace=test-workspace&state="+url.QueryEscape(startedHTML["state"])+"&code=html-code", nil)
	req.Header.Set("Accept", "text/html")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d body=%s", rec.Code, rec.Body.String())
	}
	location := rec.Header().Get("Location")
	if !strings.Contains(location, "/connect/figma/callback") || !strings.Contains(location, "status=connected") {
		t.Fatalf("unexpected redirect location %s", location)
	}
}
