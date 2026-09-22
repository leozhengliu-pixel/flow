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

func TestP2DLongTailProvidersSupported(t *testing.T) {
	for _, provider := range []string{"microsoftteams", "pagerduty", "front"} {
		if !supportedIntegration(provider) {
			t.Fatalf("%s should be a supported browser OAuth integration", provider)
		}
	}
	// Deferred / out of this batch
	for _, provider := range []string{"salesforce", "gong", "discord", "zendesk"} {
		if supportedIntegration(provider) {
			t.Fatalf("%s should stay deferred until a dedicated epic", provider)
		}
	}
}

func TestP2DOauthDefaults(t *testing.T) {
	t.Setenv("FLOW_APP_URL", "https://flow.example.test")

	teams := integrationOAuthConfig{}
	applyIntegrationOAuthDefaults("microsoftteams", &teams)
	if !strings.Contains(teams.AuthorizationURL, "login.microsoftonline.com") ||
		!strings.Contains(teams.TokenURL, "oauth2/v2.0/token") ||
		!strings.HasSuffix(teams.RedirectURI, "/api/integrations/microsoftteams/oauth/callback") {
		t.Fatalf("teams defaults=%+v", teams)
	}

	pd := integrationOAuthConfig{}
	applyIntegrationOAuthDefaults("pagerduty", &pd)
	if pd.AuthorizationURL != "https://identity.pagerduty.com/oauth/authorize" ||
		pd.TokenURL != "https://identity.pagerduty.com/oauth/token" {
		t.Fatalf("pagerduty defaults=%+v", pd)
	}

	front := integrationOAuthConfig{}
	applyIntegrationOAuthDefaults("front", &front)
	if front.AuthorizationURL != "https://app.frontapp.com/oauth/authorize" ||
		front.TokenURL != "https://app.frontapp.com/oauth/token" {
		t.Fatalf("front defaults=%+v", front)
	}
}

func TestP2DBrowserOAuthStartCompleteUsesSharedFlash(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()

	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		// Front Basic auth path
		user, pass, ok := r.BasicAuth()
		if !ok || user != "front-client" || pass != "front-secret" {
			t.Fatalf("expected Front basic auth, got ok=%v user=%q", ok, user)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"front-token","refresh_token":"front-refresh","expires_in":3600}`))
	}))
	defer tokenServer.Close()

	t.Setenv("FLOW_APP_URL", "https://flow.example.test")
	t.Setenv("FLOW_INTEGRATION_FRONT_CLIENT_SECRET", "front-secret")
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})

	_ = requestJSON[domain.IntegrationConnection](t, handler, http.MethodPut, "/api/integrations/front?workspace=test-workspace", map[string]any{
		"name": "Front",
		"config": map[string]string{
			"authorizationURL": "https://app.frontapp.com/oauth/authorize",
			"tokenURL":         tokenServer.URL,
			"clientID":         "front-client",
			"redirectURI":      "https://flow.example.test/api/integrations/front/oauth/callback",
		},
	}, http.StatusOK)

	started := requestJSON[map[string]string](t, handler, http.MethodPost, "/api/integrations/front/oauth/start?workspace=test-workspace", nil, http.StatusOK)
	if started["state"] == "" || !strings.Contains(started["authorizationURL"], "app.frontapp.com/oauth/authorize") {
		t.Fatalf("start=%#v", started)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/integrations/front/oauth/callback?workspace=test-workspace&state="+url.QueryEscape(started["state"])+"&code=front-code", nil)
	req.Header.Set("Accept", "text/html")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("callback status=%d body=%s", rec.Code, rec.Body.String())
	}
	location := rec.Header().Get("Location")
	if !strings.Contains(location, "/connect/oauth/complete") || !strings.Contains(location, "provider=front") || !strings.Contains(location, "status=connected") {
		t.Fatalf("expected CompleteOAuthView redirect, got %s", location)
	}
}

func TestP2DMicrosoftTeamsStartIncludesGraphScopes(t *testing.T) {
	handler, _ := enterpriseTestServer(t)
	t.Setenv("FLOW_APP_URL", "https://flow.example.test")
	t.Setenv("FLOW_INTEGRATION_MICROSOFTTEAMS_CLIENT_ID", "teams-client")
	t.Setenv("FLOW_INTEGRATION_MICROSOFTTEAMS_CLIENT_SECRET", "teams-secret")

	_ = requestJSON[domain.IntegrationConnection](t, handler, http.MethodPut, "/api/integrations/microsoftteams?workspace=test-workspace", map[string]any{
		"name": "Microsoft Teams",
		"config": map[string]string{
			"clientID": "teams-client",
		},
	}, http.StatusOK)

	started := requestJSON[map[string]string](t, handler, http.MethodPost, "/api/integrations/microsoftteams/oauth/start?workspace=test-workspace", nil, http.StatusOK)
	authURL := started["authorizationURL"]
	if !strings.Contains(authURL, "login.microsoftonline.com") {
		t.Fatalf("auth url missing microsoft host: %s", authURL)
	}
	if !strings.Contains(authURL, "graph.microsoft.com") || !strings.Contains(authURL, "response_mode=query") {
		t.Fatalf("expected Teams scopes + response_mode in %s", authURL)
	}
}

func TestP2DPagerDutyStartUsesIdentityHost(t *testing.T) {
	handler, _ := enterpriseTestServer(t)
	t.Setenv("FLOW_APP_URL", "https://flow.example.test")
	t.Setenv("FLOW_INTEGRATION_PAGERDUTY_CLIENT_ID", "pd-client")
	t.Setenv("FLOW_INTEGRATION_PAGERDUTY_CLIENT_SECRET", "pd-secret")

	_ = requestJSON[domain.IntegrationConnection](t, handler, http.MethodPut, "/api/integrations/pagerduty?workspace=test-workspace", map[string]any{
		"name":   "PagerDuty",
		"config": map[string]string{"clientID": "pd-client"},
	}, http.StatusOK)

	started := requestJSON[map[string]string](t, handler, http.MethodPost, "/api/integrations/pagerduty/oauth/start?workspace=test-workspace", nil, http.StatusOK)
	if !strings.Contains(started["authorizationURL"], "identity.pagerduty.com/oauth/authorize") {
		t.Fatalf("pagerduty auth url=%s", started["authorizationURL"])
	}
	if !strings.Contains(started["authorizationURL"], "incidents.read") {
		t.Fatalf("expected pagerduty scopes in %s", started["authorizationURL"])
	}
}

func TestP2DOauthUnavailableWithoutClientID(t *testing.T) {
	handler, _ := enterpriseTestServer(t)
	t.Setenv("FLOW_APP_URL", "https://flow.example.test")
	// Clear any leaked env from sibling tests
	t.Setenv("FLOW_INTEGRATION_PAGERDUTY_CLIENT_ID", "")
	t.Setenv("FLOW_INTEGRATION_PAGERDUTY_CLIENT_SECRET", "")

	_ = requestJSON[domain.IntegrationConnection](t, handler, http.MethodPut, "/api/integrations/pagerduty?workspace=test-workspace", map[string]any{
		"name": "PagerDuty",
	}, http.StatusOK)
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/integrations/pagerduty/oauth/start?workspace=test-workspace", nil, http.StatusServiceUnavailable)
}
