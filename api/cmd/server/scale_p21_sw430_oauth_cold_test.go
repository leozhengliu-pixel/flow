package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// SW430: the OAuth routes with their own metadata consumers, served through
// the real cookie/Bearer authentication and route mux on the legacy store, a
// warm bounded view and a bounded view loaded from SQL:
//   - GET /api/oauth/authorized-applications (COLD: the registered cold
//     facade lists every member's grants, the folded MCP entry, installed
//     entries and the application lookups), in full;
//   - POST /oauth/token (ExchangeOAuthGrant, HOT: the bounded OAuth metadata
//     transaction), then the new token authenticating (its usage stamp
//     included), the authorization listing, refresh, replay, /oauth/revoke's
//     family revocation and app-token supersession; GET /api/api-keys is
//     not compared (listAPIKeys is a separate unmigrated consumer whose
//     bounded authorization answers 403 on every request);
//   - PATCH /api/oauth-applications/{id} (COLD: the registered cold mutation)
//     with its identity sync and webhook reconcile.
// Responses, every owned row, audit, events, publications and the installed
// image must agree byte for byte except generated IDs, secrets and clocks;
// bounded, the installed image must equal a reload exactly (no stale field).

func sw430Handler(s *server) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/oauth/authorized-applications", s.listAuthorizedApplications)
	mux.HandleFunc("PATCH /api/oauth-applications/{id}", s.updateOAuthApplication)
	mux.HandleFunc("POST /oauth/token", s.exchangeMCPToken)
	mux.HandleFunc("POST /oauth/revoke", s.revokeOAuthToken)
	mux.HandleFunc("DELETE /api/oauth/authorizations/{id}", s.revokeOAuthAuthorization)
	return s.withColdMetadataRouteMux(mux, s.authenticate(mux))
}

// sw430Do serves one request through authentication: user's session cookie,
// or bearer when set.
func sw430Do(t *testing.T, s *server, method, target, body, workspace string, user domain.User, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	r := httptest.NewRequest(method, target, reader)
	if strings.HasPrefix(target, "/oauth/") {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if workspace != "" {
		r.Header.Set("X-Workspace-Key", workspace)
	}
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	} else if user.ID != "" {
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sw410Session(user)})
	}
	w := httptest.NewRecorder()
	sw430Handler(s).ServeHTTP(w, r)
	return w
}

// sw430Quiet waits for the work a request leaves behind: the application
// webhook reconcile an application edit starts, and the usage stamp of an
// authenticated token (asynchronous; key and grant rows and the store's
// copy). Both are part of the compared state.
func sw430Quiet(t *testing.T, s *server, workspace, token string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		buffer := make([]byte, 1<<20)
		buffer = buffer[:runtime.Stack(buffer, true)]
		// The delivery tick's reconcile and the per-workspace reconciles that
		// application lifecycle events start (dispatchWebhookEvent) both hold
		// request work while they run.
		stacks := string(buffer)
		busy := strings.Contains(stacks, "reconcileAllApplicationWebhooks") || strings.Contains(stacks, "reconcileApplicationWebhooks(")
		if !busy && token != "" {
			busy = !sw430UseStamped(t, s, workspace, token)
		}
		if !busy {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("background request work did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// sw430UseStamped reports whether the token's key row and the store's copy
// of it (the legacy snapshot or the bounded installed image) carry a use.
func sw430UseStamped(t *testing.T, s *server, workspace, token string) bool {
	t.Helper()
	keyWorkspace, key, found := s.store.FindAPIKey(secretHash(token))
	if !found || keyWorkspace != workspace {
		return true
	}
	var keys []domain.APIKey
	if err := json.Unmarshal(scaleP21SW294Image(t, s.store, workspace, "apiKeys")["apiKeys"], &keys); err != nil {
		t.Fatal(err)
	}
	for _, item := range keys {
		if item.ID == key.ID {
			return item.LastUsedAt != nil && key.LastUsedAt != nil
		}
	}
	return false
}

type sw430Tokens struct {
	Access, Refresh string
}

func sw430TokenResponse(w *httptest.ResponseRecorder) sw430Tokens {
	var body struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return sw430Tokens{body.AccessToken, body.RefreshToken}
}

// sw430Differential is sw410Differential (no stale installed field allowed)
// with two comparisons made explicit:
//   - an apiKeys row a later writer re-marshalled from the store's copy: the
//     legacy copy keeps a key as its writer handed it until a reload, which
//     fills an empty teamRestriction ("all", or "selected" with team IDs);
//     the bounded installed image is the reload. Rows are compared after
//     that load normalization (legacy after a restart writes the same).
//   - a replayable refresh row's sealed successor (random nonce);
//   - FLOW_SW430_DUMP=<dir> writes each source's normalized outcome.
var sw430Sealed = regexp.MustCompile(`"sealed":"[^"]*"`)

func (f scaleP21TeamCreateFixture) sw430Differential(t *testing.T, name string, setup func(*testing.T), call func(*server) string) []sw410Outcome {
	t.Helper()
	prepare := f.sw294Prepare(setup)
	f.scaleP21TriageWriterFixture.prepare = func(t *testing.T) {
		f.exec(t, `DELETE FROM oauth_authorization_codes WHERE 1=?`, 1)
		f.exec(t, `DELETE FROM oauth_refresh_tokens WHERE 1=?`, 1)
		f.exec(t, `DELETE FROM auth_sessions WHERE user_id IN (?,?,?)`, f.data.Viewer.ID, f.principals[1].user.ID, f.principals[2].user.ID)
		stamp := time.Now().UTC()
		for _, user := range []domain.User{f.data.Viewer, f.principals[1].user, f.principals[2].user} {
			f.exec(t, `INSERT INTO auth_sessions(token_hash,user_id,expires_at,created_at,last_seen_at) VALUES(?,?,?,?,?)`, secretHash(sw410Session(user)), user.ID,
				stamp.Add(24*time.Hour).Format(time.RFC3339Nano), stamp.Format(time.RFC3339Nano), stamp.Format(time.RFC3339Nano))
		}
		prepare(t)
	}
	defer func() { f.scaleP21TriageWriterFixture.prepare = nil }()
	var normalized []string
	var outcomes []sw410Outcome
	for _, source := range []string{"legacy", "warm", "reloaded"} {
		got, started, decoded := f.sw410Run(t, source, call)
		if decoded != 0 {
			t.Logf("%s: %s: cold accessors on the route decoded full metadata %d times", name, source, decoded)
		}
		if source != "legacy" && len(got.Stale) != 0 {
			t.Errorf("%s: %s: installed fields not equal to a reload %q", name, source, got.Stale)
		}
		for _, row := range got.State.Metadata {
			if fmt.Sprint(row[0]) != "apiKeys" {
				continue
			}
			text, ok := row[3].(string)
			if !ok {
				continue
			}
			var item map[string]any
			if json.Unmarshal([]byte(text), &item) != nil {
				continue
			}
			if item["teamRestriction"] == nil {
				item["teamRestriction"] = "all"
				if teams, _ := item["teamIds"].([]any); len(teams) > 0 {
					item["teamRestriction"] = "selected"
				}
			}
			raw, _ := json.Marshal(item)
			row[3] = string(raw)
		}
		// A replayable refresh token's successor is sealed with a random nonce.
		for _, row := range got.Refresh {
			if text, ok := row[0].(string); ok {
				row[0] = sw430Sealed.ReplaceAllString(text, `"sealed":"<sealed>"`)
			}
		}
		outcomes = append(outcomes, got)
		normalized = append(normalized, sw410Normalize(t, got, started))
		if dir := os.Getenv("FLOW_SW430_DUMP"); dir != "" {
			_ = os.WriteFile(filepath.Join(dir, name+"-"+source+".json"), []byte(normalized[len(normalized)-1]), 0o600)
		}
		if len(normalized) > 1 && normalized[len(normalized)-1] != normalized[0] {
			t.Errorf("%s: %s changed: %s", name, source, scaleP21M2Diff(normalized[0], normalized[len(normalized)-1]))
		}
	}
	return outcomes
}

func TestScaleP21SW430OAuthColdRoutes(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21TeamCreateSetup(t)
	oldSlots := webhookDeliverySlots
	webhookDeliverySlots = make(chan struct{})
	t.Cleanup(func() { webhookDeliverySlots = oldSlots })
	workspace := f.data.Workspace.URLKey
	owner, admin, member := f.data.Viewer, f.principals[1].user, f.principals[2].user
	at := time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC)
	revoked := at.Add(time.Hour)
	recent := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Hour)
	expired := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Hour)
	live := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Hour)
	const mcpA, mcpB, mcpInstalled = "flow_mcp_sw430a", "flow_mcp_sw430b", "flow_mcp_sw430installed"
	apps := func(t *testing.T) {
		f.exec(t, `DELETE FROM oauth_clients WHERE client_id LIKE ?`, "flow_mcp_sw430%")
		for _, client := range []domain.OAuthClient{
			{ClientID: mcpA, ClientName: "SW430 Claude", ClientURI: "https://claude.example.test", LogoURI: "https://claude.example.test/logo.png", RedirectURIs: []string{"https://claude.example.test/cb"}, CreatedAt: at},
			{ClientID: mcpInstalled, ClientName: "SW430 Installed MCP", ClientURI: "https://mcp.example.test", RedirectURIs: []string{"https://mcp.example.test/cb"}, CreatedAt: at},
		} {
			if err := f.repository.RegisterOAuthClient(t.Context(), client); err != nil {
				t.Fatal(err)
			}
		}
		f.mutate(t, "scale_p21_sw430.apps", func(data *domain.Bootstrap) {
			data.OAuthApplications = append(data.OAuthApplications,
				domain.OAuthApplication{ID: "sw410-app-hook", ClientID: sw410Hook, ClientSecret: "sw410-hook-secret", Name: "SW410 Hook", Description: "Delivers hooks",
					LogoURL: "https://example.test/sw410.png", RedirectURIs: []string{sw410Callback}, Scopes: []string{"read", "write", "app:assignable"},
					SupportsClientCredentials: true, CredentialsAuthorizedBy: owner.ID, CreatorID: owner.ID, CreatedAt: at, UpdatedAt: at,
					DeveloperName: "SW410 Developer", DeveloperURL: "https://example.test/developer", GitHubUsername: "sw410-bot[bot]",
					WebhookEnabled: true, WebhookURL: "https://hooks.example.test/sw410", WebhookResourceTypes: []string{"Issue"}, WebhookSecretPrefix: "whsec_sw410"},
				domain.OAuthApplication{ID: "sw410-app-plain", ClientID: sw410Plain, ClientSecret: "sw410-plain-secret", Name: "SW410 Plain", RedirectURIs: []string{sw410Callback}, Scopes: []string{"read", "write"}, CreatorID: owner.ID,
					GitHubUsername: "SW410-Plain-User", CreatedAt: at, UpdatedAt: at},
				domain.OAuthApplication{ID: "sw410-app-dup-1", ClientID: sw410Dup, Name: "SW410 Dup first", CreatorID: owner.ID, CreatedAt: at, UpdatedAt: at},
				domain.OAuthApplication{ID: "sw410-app-dup-2", ClientID: sw410Dup, Name: "SW410 Dup second", WebhookEnabled: true, WebhookURL: "https://hooks.example.test/dup", CreatorID: owner.ID, CreatedAt: at, UpdatedAt: at},
				domain.OAuthApplication{ID: "sw410-app-large", ClientID: sw410Large, Name: "SW410 Large", Description: strings.Repeat("large application description ", 40000), RedirectURIs: []string{}, Scopes: []string{"read"}, CreatorID: owner.ID, CreatedAt: at, UpdatedAt: at},
			)
		})
	}
	install := func(client, name string, active bool) func(*testing.T) {
		return func(t *testing.T) {
			if _, err := f.repository.InstallApplication(t.Context(), workspace, domain.ApplicationInstallation{ClientID: client, Name: name, AvatarURL: "https://example.test/old.png", InstalledBy: owner.ID, Scopes: []string{"read", "write"}, TeamIDs: []string{f.public}, Active: true}, ""); err != nil {
				t.Fatal(err)
			}
			if !active {
				if _, err := f.repository.InstallApplication(t.Context(), workspace, domain.ApplicationInstallation{ClientID: client, Name: name, InstalledBy: owner.ID, Scopes: []string{"read", "write"}, TeamIDs: []string{f.public}, Active: false}, ""); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	steps := func(fns ...func(*testing.T)) func(*testing.T) {
		return func(t *testing.T) {
			for _, fn := range fns {
				fn(t)
			}
		}
	}
	grants := func(t *testing.T) {
		f.mutate(t, "scale_p21_sw430.grants", func(data *domain.Bootstrap) {
			data.OAuthAuthorizations = append(data.OAuthAuthorizations,
				domain.OAuthAuthorization{ID: "sw430-grant-hook", ClientID: sw410Hook, ClientName: "Stored hook name", UserID: member.ID, Scopes: []string{"read"}, CreatedAt: at, LastUsedAt: &recent},
				domain.OAuthAuthorization{ID: "sw430-grant-hook-admin", ClientID: sw410Hook, ClientName: "Stored hook name", UserID: admin.ID, Scopes: []string{"read", "write"}, CreatedAt: at.Add(time.Minute)},
				domain.OAuthAuthorization{ID: "sw430-grant-dup", ClientID: sw410Dup, ClientName: "Stored dup", UserID: admin.ID, Scopes: []string{"read"}, CreatedAt: at},
				domain.OAuthAuthorization{ID: "sw430-grant-unknown", ClientID: "sw430-unregistered", ClientName: "Unregistered", UserID: member.ID, Scopes: []string{"read"}, CreatedAt: at},
				domain.OAuthAuthorization{ID: "sw430-grant-revoked", ClientID: sw410Large, ClientName: "Revoked large", UserID: member.ID, Scopes: []string{"read"}, CreatedAt: at, RevokedAt: &revoked},
				domain.OAuthAuthorization{ID: "sw430-grant-app", ClientID: sw410Plain, ClientName: "App actor", UserID: member.ID, Actor: "app", Scopes: []string{"read"}, CreatedAt: at},
				domain.OAuthAuthorization{ID: "sw430-grant-mcp-member", ClientID: mcpA, ClientName: "SW430 Claude", UserID: member.ID, Scopes: []string{"read", "write"}, CreatedAt: at.Add(2 * time.Minute), LastUsedAt: &recent},
				domain.OAuthAuthorization{ID: "sw430-grant-mcp-admin", ClientID: mcpB, ClientName: "SW430 Cursor", UserID: admin.ID, Scopes: []string{"read", "admin"}, CreatedAt: at, LastUsedAt: &at},
				domain.OAuthAuthorization{ID: "sw430-grant-mcp-member-b", ClientID: mcpB, ClientName: "SW430 Cursor", UserID: member.ID, Scopes: []string{"read"}, CreatedAt: at.Add(-time.Minute)},
				domain.OAuthAuthorization{ID: "sw430-grant-mcp-revoked", ClientID: mcpA, ClientName: "SW430 Claude", UserID: owner.ID, Scopes: []string{"read"}, CreatedAt: at, RevokedAt: &revoked},
				domain.OAuthAuthorization{ID: "sw430-grant-mcp-installed", ClientID: mcpInstalled, ClientName: "Stored installed MCP", UserID: member.ID, Scopes: []string{"read"}, CreatedAt: at},
			)
		})
	}
	// Keys the exchange's sweep sees: OAuth keys past expiry (deleted), an
	// OAuth key still live, a personal key past expiry (kept), and the app
	// user's earlier client-credentials key (kept on supersession).
	keys := func(t *testing.T) {
		f.mutate(t, "scale_p21_sw430.keys", func(data *domain.Bootstrap) {
			data.APIKeys = append(data.APIKeys,
				domain.APIKey{ID: "sw430-key-expired-hook", Name: "Expired hook token", Prefix: "flow_oauth_x1", SecretHash: "sw430-hash-expired-hook", CreatorID: member.ID, SubjectUserID: member.ID, Scopes: []string{"read"}, TeamIDs: []string{}, OAuthClientID: sw410Hook, AuthorizationID: "sw430-grant-hook", GrantType: "authorization_code", CreatedAt: at, ExpiresAt: &expired},
				domain.APIKey{ID: "sw430-key-expired-mcp", Name: "Expired MCP token", Prefix: "flow_oauth_x2", SecretHash: "sw430-hash-expired-mcp", CreatorID: member.ID, Scopes: []string{"read"}, TeamIDs: []string{}, OAuthClientID: mcpA, CreatedAt: at, ExpiresAt: &at},
				domain.APIKey{ID: "sw430-key-live-mcp", Name: "Live MCP token", Prefix: "flow_oauth_x3", SecretHash: "sw430-hash-live-mcp", CreatorID: member.ID, Scopes: []string{"read"}, TeamIDs: []string{}, OAuthClientID: mcpA, CreatedAt: at, ExpiresAt: &live, LastUsedAt: &recent},
				domain.APIKey{ID: "sw430-key-expired-personal", Name: "Expired personal key", Prefix: "flow_api_x4", SecretHash: "sw430-hash-expired-personal", CreatorID: member.ID, Scopes: []string{"read"}, TeamIDs: []string{}, CreatedAt: at, ExpiresAt: &expired},
			)
		})
	}
	appKeys := func(t *testing.T) {
		// The installed hook's app user and its keys: an earlier code token
		// (superseded), its client-credentials token (kept) and a revoked one.
		installations, err := f.repository.ListApplications(t.Context(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		appUser := ""
		for _, item := range installations {
			if item.ClientID == sw410Hook {
				appUser = item.UserID
			}
		}
		if appUser == "" {
			t.Fatal("hook app user missing")
		}
		f.mutate(t, "scale_p21_sw430.app_keys", func(data *domain.Bootstrap) {
			data.APIKeys = append(data.APIKeys,
				domain.APIKey{ID: "sw430-key-app-old", Name: "MCP OAuth token", Prefix: "flow_oauth_a1", SecretHash: "sw430-hash-app-old", CreatorID: appUser, SubjectUserID: appUser, IssuedByUserID: owner.ID, Actor: "app", TeamRestriction: "application", Scopes: []string{"read"}, TeamIDs: []string{}, OAuthClientID: sw410Hook, GrantType: "authorization_code", CreatedAt: at, ExpiresAt: &live, LastUsedAt: &recent},
				domain.APIKey{ID: "sw430-key-app-creator", Name: "MCP OAuth token", Prefix: "flow_oauth_a2", SecretHash: "sw430-hash-app-creator", CreatorID: appUser, Actor: "app", TeamRestriction: "application", Scopes: []string{"read"}, TeamIDs: []string{}, OAuthClientID: sw410Hook, GrantType: "refresh_token", CreatedAt: at, ExpiresAt: &live},
				domain.APIKey{ID: "sw430-key-app-credentials", Name: "Client credentials", Prefix: "flow_oauth_a3", SecretHash: "sw430-hash-app-credentials", CreatorID: appUser, SubjectUserID: appUser, Actor: "app", Scopes: []string{"read"}, TeamIDs: []string{}, OAuthClientID: sw410Hook, GrantType: "client_credentials", CreatedAt: at, ExpiresAt: &live},
				domain.APIKey{ID: "sw430-key-app-revoked", Name: "Revoked app token", Prefix: "flow_oauth_a4", SecretHash: "sw430-hash-app-revoked", CreatorID: appUser, SubjectUserID: appUser, Actor: "app", Scopes: []string{"read"}, TeamIDs: []string{}, OAuthClientID: sw410Hook, GrantType: "authorization_code", CreatedAt: at, ExpiresAt: &live, RevokedAt: &revoked},
				domain.APIKey{ID: "sw430-key-other-user", Name: "Member hook token", Prefix: "flow_oauth_a5", SecretHash: "sw430-hash-other-user", CreatorID: member.ID, SubjectUserID: member.ID, Scopes: []string{"read"}, TeamIDs: []string{}, OAuthClientID: sw410Hook, GrantType: "authorization_code", CreatedAt: at, ExpiresAt: &live},
			)
		})
	}

	// decide approves an authorization request (the consent route, as SW410
	// drives it) and returns its code.
	decide := func(t *testing.T, s *server, user domain.User, client, actor string) string {
		body, _ := json.Marshal(map[string]any{"clientId": client, "redirectUri": sw410Callback, "responseType": "code", "scope": "read write", "actor": actor, "state": "sw430", "approve": true, "workspaceKey": workspace, "allPublicTeams": true})
		r := sw410Request(s, http.MethodPost, "/api/oauth/authorize", strings.NewReader(string(body)), workspace, user)
		w := sw410Serve(r, s.decideOAuthAuthorization)
		var decided struct{ Redirect string }
		if json.Unmarshal(w.Body.Bytes(), &decided) != nil || decided.Redirect == "" {
			t.Fatalf("decide %s: %d %s", client, w.Code, w.Body.String())
		}
		redirect, err := url.Parse(decided.Redirect)
		if err != nil || redirect.Query().Get("code") == "" {
			t.Fatalf("decide %s: no code in %s", client, decided.Redirect)
		}
		return redirect.Query().Get("code")
	}
	type trace struct {
		lines []string
	}
	record := func(out *trace, name string, w *httptest.ResponseRecorder) {
		out.lines = append(out.lines, sw410Response(name, w))
	}
	exchange := func(t *testing.T, s *server, out *trace, form url.Values) sw430Tokens {
		w := sw430Do(t, s, http.MethodPost, "/oauth/token", form.Encode(), "", domain.User{}, "")
		record(out, "token "+form.Get("grant_type"), w)
		return sw430TokenResponse(w)
	}
	codeForm := func(client, secret, code string) url.Values {
		return url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {client}, "client_secret": {secret}, "redirect_uri": {sw410Callback}}
	}
	// probe authenticates token through the middleware on the listing route
	// and waits for its usage stamp.
	probe := func(t *testing.T, s *server, out *trace, name, token string) {
		w := sw430Do(t, s, http.MethodGet, "/api/oauth/authorized-applications", "", workspace, domain.User{}, token)
		record(out, name, w)
		// Authentication stamps the use before the handler runs (the listing
		// itself answers 401 to a token without a browser session); only a
		// refused token ("Sign in required") stamps nothing.
		stamp := token
		if w.Code == http.StatusUnauthorized && strings.Contains(w.Body.String(), "Sign in required") {
			stamp = ""
		}
		if _, key, found := s.store.FindAPIKey(secretHash(token)); !found || key.RevokedAt != nil {
			stamp = ""
		}
		sw430Quiet(t, s, workspace, stamp)
	}
	listing := func(t *testing.T, s *server, out *trace, name string, user domain.User) {
		record(out, name, sw430Do(t, s, http.MethodGet, "/api/oauth/authorized-applications", "", workspace, user, ""))
	}
	run := func(steps ...func(*testing.T, *server, *trace)) func(*server) string {
		return func(s *server) string {
			out := &trace{}
			for _, step := range steps {
				step(t, s, out)
			}
			return strings.Join(out.lines, "\n")
		}
	}
	list := func(name string, user domain.User) func(*testing.T, *server, *trace) {
		return func(t *testing.T, s *server, out *trace) { listing(t, s, out, name, user) }
	}
	listOther := func(t *testing.T, s *server, out *trace) {
		record(out, "listing-unknown-workspace", sw430Do(t, s, http.MethodGet, "/api/oauth/authorized-applications", "", "sw430-missing", owner, ""))
	}
	revokeGrant := func(id string, user domain.User) func(*testing.T, *server, *trace) {
		return func(t *testing.T, s *server, out *trace) {
			record(out, "revoke "+id, sw430Do(t, s, http.MethodDelete, "/api/oauth/authorizations/"+id, "", workspace, user, ""))
		}
	}
	// userFlow: a member authorizes the hook as themselves, exchanges the
	// code, authenticates with the token, and the admins list keys and
	// applications.
	userFlow := func(t *testing.T, s *server, out *trace) {
		tokens := exchange(t, s, out, codeForm(sw410Hook, "sw410-hook-secret", decide(t, s, owner, sw410Hook, "user")))
		probe(t, s, out, "probe", tokens.Access)
		listing(t, s, out, "listing", owner)
	}
	refreshRevoke := func(t *testing.T, s *server, out *trace) {
		first := exchange(t, s, out, codeForm(sw410Plain, "sw410-plain-secret", decide(t, s, owner, sw410Plain, "user")))
		probe(t, s, out, "probe-first", first.Access)
		second := exchange(t, s, out, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {first.Refresh}, "client_id": {sw410Plain}, "client_secret": {"sw410-plain-secret"}})
		probe(t, s, out, "probe-second", second.Access)
		// Replay inside the grace window: the same successor, a new access token.
		replay := exchange(t, s, out, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {first.Refresh}, "client_id": {sw410Plain}, "client_secret": {"sw410-plain-secret"}})
		if replay.Refresh != second.Refresh {
			out.lines = append(out.lines, "replay returned another successor")
		}
		record(out, "revoke-refresh", sw430Do(t, s, http.MethodPost, "/oauth/revoke", url.Values{"token": {second.Refresh}, "client_id": {sw410Plain}, "client_secret": {"sw410-plain-secret"}}.Encode(), "", domain.User{}, ""))
		probe(t, s, out, "probe-first-after-revoke", first.Access)
		probe(t, s, out, "probe-second-after-revoke", second.Access)
		probe(t, s, out, "probe-replay-after-revoke", replay.Access)
		exchange(t, s, out, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {second.Refresh}, "client_id": {sw410Plain}, "client_secret": {"sw410-plain-secret"}})
	}
	appSupersede := func(t *testing.T, s *server, out *trace) {
		first := exchange(t, s, out, codeForm(sw410Hook, "sw410-hook-secret", decide(t, s, owner, sw410Hook, "app")))
		probe(t, s, out, "probe-app-first", first.Access)
		second := exchange(t, s, out, codeForm(sw410Hook, "sw410-hook-secret", decide(t, s, owner, sw410Hook, "app")))
		probe(t, s, out, "probe-app-first-superseded", first.Access)
		probe(t, s, out, "probe-app-second", second.Access)
		listing(t, s, out, "listing", owner)
	}
	revokedGrant := func(t *testing.T, s *server, out *trace) {
		code := decide(t, s, owner, sw410Plain, "user")
		id := ""
		for _, row := range f.rows(t, `SELECT record_key,data FROM workspace_metadata_records WHERE workspace_key=? AND field='oauthAuthorizations'`, workspace) {
			var grant domain.OAuthAuthorization
			if err := json.Unmarshal([]byte(fmt.Sprint(row[1])), &grant); err != nil {
				t.Fatal(err)
			}
			if grant.ClientID == sw410Plain && grant.UserID == owner.ID {
				id = fmt.Sprint(row[0])
			}
		}
		if id == "" {
			t.Fatal("decided grant missing")
		}
		revokeGrant(id, owner)(t, s, out)
		exchange(t, s, out, codeForm(sw410Plain, "sw410-plain-secret", code))
		listing(t, s, out, "listing", owner)
	}
	badClient := func(client, secret string) func(*testing.T, *server, *trace) {
		return func(t *testing.T, s *server, out *trace) {
			code := decide(t, s, owner, sw410Plain, "user")
			exchange(t, s, out, codeForm(client, secret, code))
			// The code is still usable by its own client.
			exchange(t, s, out, codeForm(sw410Plain, "sw410-plain-secret", code))
		}
	}
	reusedCode := func(t *testing.T, s *server, out *trace) {
		code := decide(t, s, owner, sw410Plain, "user")
		tokens := exchange(t, s, out, codeForm(sw410Plain, "sw410-plain-secret", code))
		exchange(t, s, out, codeForm(sw410Plain, "sw410-plain-secret", code))
		probe(t, s, out, "probe", tokens.Access)
	}
	edit := func(user domain.User, id string, body map[string]any) func(*testing.T, *server, *trace) {
		return func(t *testing.T, s *server, out *trace) {
			raw, _ := json.Marshal(body)
			w := sw430Do(t, s, http.MethodPatch, "/api/oauth-applications/"+id, string(raw), workspace, user, "")
			record(out, "edit "+id, w)
			sw430Quiet(t, s, workspace, "")
		}
	}
	renamed := "SW430 Renamed"
	cases := []struct {
		name  string
		setup func(*testing.T)
		call  func(*server) string
	}{
		{"listing-owner", steps(apps, grants, install(sw410Hook, "SW410 Hook", true), install(sw410Plain, "SW410 Plain", false), install(sw410Dup, "Dup installation", true), install(sw410Large, "Large installation", true), install("sw430-orphan", "Orphan installation", true), install(mcpInstalled, "Installed MCP", true)),
			run(list("listing", owner))},
		{"listing-admin", steps(apps, grants, install(sw410Hook, "SW410 Hook", true)), run(list("listing", admin))},
		{"listing-member-forbidden", steps(apps, grants), run(list("listing", member))},
		{"listing-unknown-workspace", steps(apps, grants), run(listOther)},
		{"listing-empty", nil, run(list("listing", owner))},
		{"listing-after-revoke", steps(apps, grants, install(sw410Hook, "SW410 Hook", true)), run(revokeGrant("sw430-grant-hook", member), revokeGrant("sw430-grant-mcp-member", member), revokeGrant("sw430-grant-hook-admin", member), list("listing", owner))},
		{"exchange-user", steps(apps, grants, keys), run(userFlow)},
		{"exchange-user-no-keys", apps, run(userFlow)},
		{"exchange-refresh-revoke", steps(apps, keys), run(refreshRevoke)},
		{"exchange-app-supersede", steps(apps, keys, install(sw410Hook, "SW410 Hook", true), appKeys), run(appSupersede)},
		{"exchange-revoked-grant", apps, run(revokedGrant)},
		{"exchange-unknown-client", apps, run(badClient("sw430-unknown", "secret"))},
		{"exchange-wrong-secret", apps, run(badClient(sw410Plain, "wrong-secret"))},
		{"exchange-other-client", apps, run(badClient(sw410Hook, "sw410-hook-secret"))},
		{"exchange-reused-code", steps(apps, keys), run(reusedCode)},
		{"edit-rename-installed", steps(apps, install(sw410Plain, "SW410 Plain", true)), run(edit(owner, "sw410-app-plain", map[string]any{"name": renamed, "description": "Renamed by SW430"}), list("listing", owner))},
		{"edit-rename-uninstalled", steps(apps, install(sw410Plain, "SW410 Plain", false)), run(edit(owner, "sw410-app-plain", map[string]any{"name": renamed}))},
		// Without grants: the reconcile creates one managed hook (several are
		// created in map order, which a reload then orders by).
		{"edit-hook-webhook", steps(apps, install(sw410Hook, "SW410 Hook", true)), run(edit(owner, "sw410-app-hook", map[string]any{"name": renamed, "developerName": "SW430 Developer"}), list("listing", owner))},
		{"edit-scopes", steps(apps, install(sw410Plain, "SW410 Plain", true)), run(edit(admin, "sw410-app-plain", map[string]any{"scopes": []string{"read"}, "supportsClientCredentials": true}))},
		{"edit-unchanged", apps, run(edit(owner, "sw410-app-plain", map[string]any{"name": "SW410 Plain"}))},
		{"edit-member-forbidden", apps, run(edit(member, "sw410-app-plain", map[string]any{"name": renamed}))},
		{"edit-unknown-app", apps, run(edit(owner, "sw430-missing-app", map[string]any{"name": renamed}))},
		{"edit-github-taken", apps, run(edit(owner, "sw410-app-plain", map[string]any{"githubUsername": "SW410-BOT[bot]"}))},
		{"edit-invalid", apps, run(edit(owner, "sw410-app-plain", map[string]any{"name": "  "}))},
		{"edit-duplicate-client", steps(apps, install(sw410Dup, "Dup", true)), run(edit(owner, "sw410-app-dup-2", map[string]any{"name": renamed}))},
		{"edit-large", steps(apps, install(sw410Large, "Large", true)), run(edit(owner, "sw410-app-large", map[string]any{"name": renamed}))},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			outcomes := f.sw430Differential(t, test.name, test.setup, test.call)
			t.Logf("legacy result %s", outcomes[0].Result)
		})
	}
}
