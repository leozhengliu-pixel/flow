package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func validOAuthManifest() map[string]any {
	return map[string]any{
		"$schema":       "https://flow.example.test/.well-known/oauth-app-manifest.schema.json",
		"schemaVersion": "1.0.0",
		"distribution":  "public",
		"display":       map[string]any{"description": "Adds issue creation to Acme Agent.", "iconUrl": "https://acme.example/icon.png"},
		"developer":     map[string]any{"name": "Acme"},
		"oauth": map[string]any{
			"client_name":   "Acme Agent",
			"client_uri":    "https://acme.example/flow",
			"redirect_uris": []string{oauthTestRedirect, "https://acme.example/api/callback/preview"},
			"grant_types":   []string{"authorization_code", "client_credentials"},
			"scopes":        []string{"read", "write", "issues:create"},
		},
		"webhook": map[string]any{"enabled": true, "url": "https://acme.example/api/webhook", "resourceTypes": []string{"Issue", "comments", "OAuthAuthorization"}},
		"agent":   map[string]any{"assignable": true},
	}
}

type manifestValidation struct {
	Valid  bool                    `json:"valid"`
	Issues []oauthAppManifestIssue `json:"issues"`
}

func TestOAuthAppManifestValidation(t *testing.T) {
	_, host, admin, data := applicationHTTPFixture(t)
	workspace := data.Workspace.URLKey
	validate := func(manifest any) manifestValidation {
		t.Helper()
		return authRequest[manifestValidation](t, admin, "POST", host.URL+"/api/oauth-applications/manifest/validate", manifest, workspace, 200)
	}
	if result := validate(validOAuthManifest()); !result.Valid || len(result.Issues) != 0 {
		t.Fatalf("valid manifest rejected: %+v", result)
	}
	cases := map[string]func(map[string]any){
		"schemaVersion":       func(m map[string]any) { m["schemaVersion"] = "2.0.0" },
		"distribution":        func(m map[string]any) { m["distribution"] = "everyone" },
		"developer.name":      func(m map[string]any) { delete(m, "developer") },
		"oauth.client_name":   func(m map[string]any) { m["oauth"].(map[string]any)["client_name"] = "Flow helper" },
		"oauth.client_uri":    func(m map[string]any) { m["oauth"].(map[string]any)["client_uri"] = "acme.example" },
		"oauth.redirect_uris": func(m map[string]any) { m["oauth"].(map[string]any)["redirect_uris"] = []string{} },
		"oauth.redirect_uris[0]": func(m map[string]any) {
			m["oauth"].(map[string]any)["redirect_uris"] = []string{"http://acme.example/callback"}
		},
		"oauth.grant_types": func(m map[string]any) { m["oauth"].(map[string]any)["grant_types"] = []string{"client_credentials"} },
		"oauth.scopes":      func(m map[string]any) { m["oauth"].(map[string]any)["scopes"] = []string{"read", "everything"} },
		"webhook.url":       func(m map[string]any) { m["webhook"].(map[string]any)["url"] = "https://10.0.0.5/hook" },
		"webhook.resourceTypes": func(m map[string]any) {
			m["webhook"].(map[string]any)["resourceTypes"] = []string{"Issue", "Spaceship"}
		},
		"display.iconUrl": func(m map[string]any) { m["display"].(map[string]any)["iconUrl"] = "ftp://acme.example/icon.png" },
	}
	for path, mutate := range cases {
		manifest := validOAuthManifest()
		mutate(manifest)
		result := validate(manifest)
		if result.Valid || !slices.ContainsFunc(result.Issues, func(issue oauthAppManifestIssue) bool { return issue.Path == path }) {
			t.Errorf("%s: %+v", path, result)
		}
	}
	manifest := validOAuthManifest()
	manifest["webhook"].(map[string]any)["url"] = "http://acme.example/hook"
	if result := validate(manifest); result.Valid {
		t.Error("plain HTTP webhook accepted")
	}
	manifest = validOAuthManifest()
	manifest["unexpected"] = true
	if result := validate(manifest); result.Valid {
		t.Error("unknown top-level field accepted")
	}
	// The JSON Schema is public.
	response, err := http.Get(host.URL + "/.well-known/oauth-app-manifest.schema.json")
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("schema = %v %v", response, err)
	}
	var schema map[string]any
	_ = json.NewDecoder(response.Body).Decode(&schema)
	response.Body.Close()
	if schema["title"] == nil || schema["properties"].(map[string]any)["oauth"] == nil {
		t.Fatalf("schema = %v", schema)
	}
}

func TestOAuthAppManifestImportExport(t *testing.T) {
	_, host, admin, data := applicationHTTPFixture(t)
	workspace := data.Workspace.URLKey
	authRequest[map[string]any](t, admin, "POST", host.URL+"/api/oauth-applications/manifest", map[string]any{"schemaVersion": "1.0.0"}, workspace, 400)
	created := authRequest[manifestApplicationResponse](t, admin, "POST", host.URL+"/api/oauth-applications/manifest", validOAuthManifest(), workspace, 201)
	if created.WebhookSecret == "" || created.WebhookSecretPrefix == "" {
		t.Fatalf("enabled manifest webhook has no signing secret: %+v", created)
	}
	if created.ClientSecret == "" || created.Name != "Acme Agent" || created.DeveloperName != "Acme" || created.DeveloperURL != "https://acme.example/flow" || !created.PublicEnabled || !created.SupportsClientCredentials || len(created.RedirectURIs) != 2 {
		t.Fatalf("created = %+v", created)
	}
	if !slices.Equal(created.Scopes, []string{"read", "write", "create_issues", "app:assignable"}) {
		t.Fatalf("scopes = %v", created.Scopes)
	}
	if !created.WebhookEnabled || created.WebhookURL != "https://acme.example/api/webhook" || !slices.Equal(created.WebhookResourceTypes, []string{"Issue", "Comment", "OAuthApp"}) {
		t.Fatalf("webhook = %v %q %v", created.WebhookEnabled, created.WebhookURL, created.WebhookResourceTypes)
	}
	exported := authRequest[map[string]any](t, admin, "GET", host.URL+"/api/oauth-applications/"+created.ID+"/manifest", nil, workspace, 200)
	if exported["schemaVersion"] != "1.0.0" || !strings.HasSuffix(exported["$schema"].(string), "/.well-known/oauth-app-manifest.schema.json") || exported["agent"].(map[string]any)["assignable"] != true {
		t.Fatalf("exported = %v", exported)
	}
	// Exported manifests re-import cleanly.
	if result := authRequest[manifestValidation](t, admin, "POST", host.URL+"/api/oauth-applications/manifest/validate", exported, workspace, 200); !result.Valid {
		t.Fatalf("export does not validate: %+v", result)
	}
	// PUT replaces the configuration but keeps the credentials.
	next := validOAuthManifest()
	next["distribution"] = "private"
	delete(next, "webhook")
	delete(next, "agent")
	next["oauth"].(map[string]any)["grant_types"] = []string{"authorization_code"}
	updated := authRequest[manifestApplicationResponse](t, admin, "PUT", host.URL+"/api/oauth-applications/"+created.ID+"/manifest", next, workspace, 200)
	if updated.ClientID != created.ClientID || updated.ClientSecret != "" || updated.WebhookSecret != "" || updated.PublicEnabled || updated.SupportsClientCredentials || updated.WebhookURL != "" || slices.Contains(updated.Scopes, "app:assignable") {
		t.Fatalf("updated = %+v", updated)
	}
	authRequest[map[string]any](t, admin, "PUT", host.URL+"/api/oauth-applications/missing/manifest", next, workspace, 404)

	// Installs list shows this workspace's grants of the application.
	authorizeCode(t, host, admin, workspace, created.ClientID, "read", "")
	installs := authRequest[struct {
		Authorizations []domain.OAuthAuthorization `json:"authorizations"`
	}](t, admin, "GET", host.URL+"/api/oauth-applications/"+created.ID+"/installs", nil, workspace, 200)
	if len(installs.Authorizations) != 1 || installs.Authorizations[0].UserID != data.Viewer.ID {
		t.Fatalf("installs = %+v", installs)
	}
}

func TestOAuthViaApplicationAttribution(t *testing.T) {
	s, host, admin, data := applicationHTTPFixture(t)
	workspace := data.Workspace.URLKey
	app := createConfidentialApp(t, host, admin, workspace, []string{"read", "write"})
	code := authorizeCode(t, host, admin, workspace, app.ClientID, "read write", "")
	pair := oauthForm[oauthTokenPair](t, host.URL+"/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {oauthTestRedirect}}, app.ClientID, app.ClientSecret, 200)

	// actor=user: "<User> via <App>".
	var issue domain.Issue
	_ = json.Unmarshal(applicationBearerRequest(t, host, pair.AccessToken, "POST", "/api/issues", `{"title":"Via app","teamId":"`+data.Teams[0].ID+`"}`, 201), &issue)
	if issue.Creator.ID != data.Viewer.ID || issue.BotActor == nil || issue.BotActor.Name != "Acme Sync" || issue.BotActor.Type != domain.ActorBotTypeOAuthApplication || issue.BotActor.AvatarURL != "https://client.example.test/icon.png" {
		t.Fatalf("issue attribution = %+v %+v", issue.Creator, issue.BotActor)
	}
	var comment domain.Comment
	_ = json.Unmarshal(applicationBearerRequest(t, host, pair.AccessToken, "POST", "/api/issues/"+issue.ID+"/comments", `{"body":"From the app"}`, 201), &comment)
	if comment.User.ID != data.Viewer.ID || comment.BotActor == nil || comment.BotActor.ID != app.ClientID || comment.BotActor.UserDisplayName != "" {
		t.Fatalf("comment attribution = %+v", comment.BotActor)
	}
	applicationBearerRequest(t, host, pair.AccessToken, "POST", "/api/issues/"+issue.ID+"/comments", `{"body":"x","createAsUser":"Mark"}`, 400)

	// A session (no OAuth) comment is not attributed.
	plain := authRequest[domain.Comment](t, admin, "POST", host.URL+"/api/issues/"+issue.ID+"/comments", map[string]string{"body": "typed in Flow"}, workspace, 201)
	if plain.BotActor != nil {
		t.Fatalf("session comment attributed: %+v", plain.BotActor)
	}
	stored, ok := s.store.BootstrapFor(workspace)
	if !ok {
		t.Fatal("workspace missing")
	}
	activities := stored.Activities[issue.ID]
	if !slices.ContainsFunc(activities, func(item domain.ActivityEvent) bool {
		return item.Type == "comment.created" && item.BotActor != nil && item.BotActor.Name == "Acme Sync"
	}) {
		t.Fatalf("activity not attributed: %+v", activities)
	}
	if slices.ContainsFunc(activities, func(item domain.ActivityEvent) bool {
		return item.Type == "comment.created" && item.Metadata["commentId"] == plain.ID && item.BotActor != nil
	}) {
		t.Fatal("session activity attributed to the application")
	}

	// actor=app with createAsUser: "<external user> via <App>".
	authRequest[domain.OAuthApplication](t, admin, "PATCH", host.URL+"/api/oauth-applications/"+app.ID, map[string]bool{"supportsClientCredentials": true}, workspace, 200)
	machine := applicationFormToken(t, host, app, "read write", 200)
	var external domain.Comment
	_ = json.Unmarshal(applicationBearerRequest(t, host, machine.Token, "POST", "/api/issues/"+issue.ID+"/comments", `{"body":"Customer reply","createAsUser":"Mark","displayIconUrl":"https://cdn.example.test/mark.png"}`, 201), &external)
	if external.User.ID != machine.Subject || external.BotActor == nil || external.BotActor.UserDisplayName != "Mark" || external.BotActor.UserAvatarURL != "https://cdn.example.test/mark.png" || external.BotActor.Name != "Acme Sync" {
		t.Fatalf("createAsUser attribution = %+v %+v", external.User, external.BotActor)
	}
	var appComment domain.Comment
	_ = json.Unmarshal(applicationBearerRequest(t, host, machine.Token, "POST", "/api/issues/"+issue.ID+"/comments", `{"body":"As the app"}`, 201), &appComment)
	if appComment.BotActor != nil {
		t.Fatalf("app-actor comment without createAsUser attributed: %+v", appComment.BotActor)
	}
	applicationBearerRequest(t, host, machine.Token, "POST", "/api/issues/"+issue.ID+"/comments", `{"body":"x","displayIconUrl":"https://cdn.example.test/a.png"}`, 400)
	applicationBearerRequest(t, host, machine.Token, "POST", "/api/issues/"+issue.ID+"/comments", `{"body":"x","createAsUser":"Mark","displayIconUrl":"javascript:alert(1)"}`, 400)
}

// Manifest saves reconcile the managed per-workspace application webhook the
// same way the webhook settings endpoint does.
func TestOAuthAppManifestReconcilesApplicationWebhooks(t *testing.T) {
	s, host, admin, data := applicationHTTPFixture(t)
	workspace := data.Workspace.URLKey
	created := authRequest[manifestApplicationResponse](t, admin, "POST", host.URL+"/api/oauth-applications/manifest", validOAuthManifest(), workspace, 201)
	authorizeCode(t, host, admin, workspace, created.ClientID, "read", "")
	managed := func(want string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			hooks, _ := s.store.WorkspaceMetadataFields(workspace, "webhooks")
			url := ""
			for _, hook := range hooks.Webhooks {
				if hook.ManagedBy == "application" && hook.ApplicationClientID == created.ClientID {
					url = hook.URL
				}
			}
			if url == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("managed webhook URL = %q, want %q", url, want)
			}
			time.Sleep(25 * time.Millisecond)
		}
	}
	managed("https://acme.example/api/webhook")
	next := validOAuthManifest()
	next["webhook"].(map[string]any)["url"] = "https://acme.example/api/webhook/v2"
	authRequest[manifestApplicationResponse](t, admin, "PUT", host.URL+"/api/oauth-applications/"+created.ID+"/manifest", next, workspace, 200)
	managed("https://acme.example/api/webhook/v2")
	delete(next, "webhook")
	authRequest[manifestApplicationResponse](t, admin, "PUT", host.URL+"/api/oauth-applications/"+created.ID+"/manifest", next, workspace, 200)
	managed("")
}
