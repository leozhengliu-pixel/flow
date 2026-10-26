package main

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestValidApplicationIconURL(t *testing.T) {
	for _, item := range []struct {
		url     string
		uploads bool
		want    bool
	}{
		{"https://cdn.example.test/agent.png", false, true},
		{"https://cdn.example.test/agent.png?size=64", false, true},
		{"/uploads/oauth_app_icon_abc.png", true, true},
		{"/uploads/oauth_app_icon_abc.png", false, false},
		{"/uploads/../secret", true, false},
		{"/uploads/a/b.png", true, false},
		{"http://cdn.example.test/agent.png", false, false},
		{"javascript:alert(1)", true, false},
		{"data:image/png;base64,AAAA", true, false},
		{"//cdn.example.test/agent.png", true, false},
		{"https://user:pass@cdn.example.test/agent.png", false, false},
		{"https://cdn.example.test/agent.png#x", false, false},
		{"https://cdn.example.test/a b.png", false, false},
		{" https://cdn.example.test/agent.png", false, false},
		{"https://cdn.example.test/" + strings.Repeat("a", maxApplicationIconURL), false, false},
	} {
		if got := validApplicationIconURL(item.url, item.uploads); got != item.want {
			t.Errorf("validApplicationIconURL(%q, %v) = %v, want %v", item.url, item.uploads, got, item.want)
		}
	}
}

// TestApplicationAvatarURLsAreValidated covers every way a third-party
// application's avatar enters Flow: the OAuth application settings, the
// installation (which copies the registration's logo) and the list of
// installed applications (which drops logos stored before validation).
func TestApplicationAvatarURLsAreValidated(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "app-icons.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	for _, logo := range []string{"javascript:alert(1)", "data:image/svg+xml,<svg/>", "http://cdn.example.test/a.png", "https://cdn.example.test/" + strings.Repeat("a", maxApplicationIconURL)} {
		requestJSON[any](t, handler, http.MethodPost, "/api/oauth-applications", map[string]any{"name": "Icon app", "logoUrl": logo}, http.StatusBadRequest)
	}
	app := requestJSON[domain.OAuthApplication](t, handler, http.MethodPost, "/api/oauth-applications", map[string]any{"name": "Icon app", "logoUrl": "https://cdn.example.test/agent.png"}, http.StatusCreated)
	if app.LogoURL != "https://cdn.example.test/agent.png" {
		t.Fatalf("logo = %q", app.LogoURL)
	}
	requestJSON[any](t, handler, http.MethodPatch, "/api/oauth-applications/"+app.ID, map[string]any{"logoUrl": "javascript:alert(1)"}, http.StatusBadRequest)

	// A registration stored before validation keeps its logo out of the
	// avatar.
	if err := repository.RegisterOAuthClient(t.Context(), domain.OAuthClient{ClientID: "legacy-logo", ClientName: "Legacy logo", LogoURI: "javascript:alert(1)", RedirectURIs: []string{"https://example.test/callback"}}); err != nil {
		t.Fatal(err)
	}
	data := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	installed := requestJSON[struct {
		Application domain.ApplicationInstallation `json:"application"`
	}](t, handler, http.MethodPost, "/api/application-installations", map[string]any{"clientId": "legacy-logo", "teamIds": []string{data.Teams[0].ID}, "scopes": []string{"read"}, "active": true}, http.StatusOK)
	if installed.Application.AvatarURL != "" {
		t.Fatalf("unsafe registration logo became the avatar: %q", installed.Application.AvatarURL)
	}
	for _, item := range requestJSON[[]domain.ApplicationInstallation](t, handler, http.MethodGet, "/api/application-installations", nil, http.StatusOK) {
		if !item.Builtin && item.AvatarURL != "" && !validApplicationIconURL(item.AvatarURL, true) {
			t.Fatalf("installed application %q lists avatar %q", item.Name, item.AvatarURL)
		}
	}
}
