package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// The routes that resolve a registered OAuth application by client ID
// (OAuthApplicationByClientID) on the legacy store, a warm bounded view and a
// bounded view loaded from SQL: the consent screen and its realtime flag, an
// app authorization and its token exchange (the grant's realtime flag decides
// the installation's WithoutRealtime), a developer token created with an API
// key (its credential audit), the authorized applications listing before and
// after a revocation, the GitHub app-user map, and an application edit that
// renames its app users. Responses, every owned row, audit entries, domain
// events, publications and the installed image must agree byte for byte
// except generated IDs, secrets and clocks.

var (
	oAuthClientLookupSecret = regexp.MustCompile(`flow_(code|oauth|refresh|webhook)_[A-Za-z0-9_-]+`)
	oauthClientLookupHash   = regexp.MustCompile(`[0-9a-f]{64}`)
	// A new record's collection order is derived from the clock.
	oauthClientLookupOrder = regexp.MustCompile(`,-1[0-9]{15},`)
	// A token exchange starts a random refresh family.
	oauthClientLookupFamily = regexp.MustCompile(`family_[0-9a-f]{24}`)
)

const (
	oAuthCOLDHook             = "sw410-hook"
	oAuthCOLDPlain            = "sw410-plain"
	noURL                     = "sw410-nourl"
	oAuthClientLookupDup      = "sw410-dup"
	oAuthClientLookupLarge    = "sw410-large"
	oAuthClientLookupCallback = "https://example.test/sw410/callback"
)

type oauthClientLookupOutcome struct {
	oauthMetadataOutcome
	Codes, Refresh [][]any
	// Stale lists the installed fields that are not what a reload reads
	// (another writer on the route); reported, compared only across the
	// bounded sources.
	Stale []string `json:"-"`
}

// oauthClientLookupRun is oauthMetadataRun for routes whose other accessors are not this
// group's: it reports (does not fail on) a full decode elsewhere on the
// route, and adds the global grant and refresh rows.
func (f scaleTeamCreateFixture) oauthClientLookupRun(t *testing.T, source string, call func(*server) string) (oauthClientLookupOutcome, time.Time, int64) {
	t.Helper()
	started := time.Now().UTC()
	service := f.service(t, source)
	workspace := f.data.Workspace.URLKey
	got := oauthClientLookupOutcome{}
	var mu sync.Mutex
	service.store.SetWebhookSink(func(_ string, event domain.DomainEvent) {
		mu.Lock()
		defer mu.Unlock()
		got.Webhooks = append(got.Webhooks, discussionRoutesEnvelope(t, map[string]any{
			"type": event.Type, "aggregateId": event.AggregateID, "actorId": event.ActorID,
			"payload": event.Payload, "previousValues": event.PreviousValues, "snapshot": event.Snapshot,
		}))
	})
	service.store.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
		mu.Lock()
		defer mu.Unlock()
		got.Realtime = append(got.Realtime, discussionRoutesEnvelope(t, map[string]any{
			"type": event.Type, "aggregateId": event.AggregateID, "actorId": event.ActorID,
			"clientId": event.ClientID, "payload": event.Payload,
		}))
	})
	prior := map[any]bool{}
	for _, event := range f.state(t).Events {
		prior[event[0]] = true
	}
	before := oauthMetadataImage(t, service.store, workspace, oauthMetadataFields...)
	decodes := service.store.WorkspaceMetadataDecodes()
	got.Result = call(service)
	mu.Lock()
	service.store.SetWebhookSink(nil)
	service.store.SetRealtimeSink(nil)
	mu.Unlock()
	decoded := service.store.WorkspaceMetadataDecodes() - decodes
	got.State = f.state(t)
	var events [][]any
	for _, event := range got.State.Events {
		if !prior[event[0]] {
			events = append(events, event[1:])
		}
	}
	slices.SortFunc(events, func(a, b []any) int { return strings.Compare(fmt.Sprint(a...), fmt.Sprint(b...)) })
	slices.SortFunc(got.Webhooks, func(a, b json.RawMessage) int { return strings.Compare(string(a), string(b)) })
	slices.SortFunc(got.Realtime, func(a, b json.RawMessage) int { return strings.Compare(string(a), string(b)) })
	got.State.Events = events
	got.Extra = f.oAuthMetadataExtra(t)
	got.Codes = f.rows(t, `SELECT data,expires_at,used_at IS NULL FROM oauth_authorization_codes WHERE 1=? ORDER BY created_at,data`, 1)
	got.Refresh = f.rows(t, `SELECT data,expires_at,revoked_at IS NULL FROM oauth_refresh_tokens WHERE 1=? ORDER BY created_at,data`, 1)
	if service.store.BoundedMetadataEnabled() && service.store.MetadataWorkUsedForTesting() != 0 {
		t.Fatalf("%s: retained metadata work %d", source, service.store.MetadataWorkUsedForTesting())
	}
	installed := oauthMetadataImage(t, service.store, workspace, exactFields...)
	reloaded, err := store.OpenDatabase(f.reader.config)
	if err != nil {
		t.Fatal(err)
	}
	fresh := oauthMetadataImage(t, reloaded, workspace, exactFields...)
	if err := reloaded.Close(); err != nil {
		t.Fatal(err)
	}
	if service.store.BoundedMetadataEnabled() {
		for _, name := range exactFields {
			want, have := scaleTriageNormalize(t, fresh[name], started), scaleTriageNormalize(t, installed[name], started)
			if want != have {
				got.Stale = append(got.Stale, name)
				t.Logf("%s: installed %s is not the reloaded one: %s", source, name, policyFirstDifference(want, have))
			}
		}
	}
	got.Installed = oauthMetadataDelta(t, before, installed)
	for _, name := range exactFields {
		if !slices.Contains(oauthMetadataFields, name) {
			delete(fresh, name)
		}
	}
	got.Reloaded, _ = json.Marshal(fresh)
	return got, started, decoded
}

func oauthClientLookupNormalize(t *testing.T, outcome oauthClientLookupOutcome, started time.Time) string {
	t.Helper()
	text := scaleTriageNormalize(t, outcome, started)
	text = oAuthClientLookupSecret.ReplaceAllString(text, "flow_${1}_<secret>")
	text = oauthClientLookupOrder.ReplaceAllString(text, ",<order>,")
	text = oauthClientLookupFamily.ReplaceAllString(text, "family_<random>")
	return oauthClientLookupHash.ReplaceAllString(text, "<hash>")
}

func (f scaleTeamCreateFixture) oauthClientLookupDifferential(t *testing.T, name string, setup func(*testing.T), call func(*server) string, stale ...string) []oauthClientLookupOutcome {
	t.Helper()
	prepare := f.oauthMetadataPrepare(setup)
	f.scaleTriageWriterFixture.prepare = func(t *testing.T) {
		f.exec(t, `DELETE FROM oauth_authorization_codes WHERE 1=?`, 1)
		f.exec(t, `DELETE FROM oauth_refresh_tokens WHERE 1=?`, 1)
		f.exec(t, `DELETE FROM auth_sessions WHERE user_id IN (?,?,?)`, f.data.Viewer.ID, f.principals[1].user.ID, f.principals[2].user.ID)
		stamp := time.Now().UTC()
		for _, user := range []domain.User{f.data.Viewer, f.principals[1].user, f.principals[2].user} {
			f.exec(t, `INSERT INTO auth_sessions(token_hash,user_id,expires_at,created_at,last_seen_at) VALUES(?,?,?,?,?)`, secretHash(oAuthClientLookupSession(user)), user.ID,
				stamp.Add(24*time.Hour).Format(time.RFC3339Nano), stamp.Format(time.RFC3339Nano), stamp.Format(time.RFC3339Nano))
		}
		prepare(t)
	}
	defer func() { f.scaleTriageWriterFixture.prepare = nil }()
	var normalized []string
	var outcomes []oauthClientLookupOutcome
	for _, source := range []string{"legacy", "warm", "reloaded"} {
		got, started, decoded := f.oauthClientLookupRun(t, source, call)
		if decoded != 0 {
			t.Logf("%s: %s: other accessors on the route decoded full metadata %d times", name, source, decoded)
		}
		if source != "legacy" && !slices.Equal(got.Stale, stale) {
			t.Errorf("%s: %s: installed fields not equal to a reload %q, want %q", name, source, got.Stale, stale)
		}
		if len(stale) != 0 {
			// The exchange writer's installed delta differs by construction;
			// its rows, events and the reload are still compared.
			got.Installed = nil
		}
		outcomes = append(outcomes, got)
		normalized = append(normalized, oauthClientLookupNormalize(t, got, started))
		if len(normalized) > 1 && normalized[len(normalized)-1] != normalized[0] {
			t.Errorf("%s: %s changed: %s", name, source, policyFirstDifference(normalized[0], normalized[len(normalized)-1]))
		}
	}
	return outcomes
}

// session is the session token the fixture seeds for user.
func oAuthClientLookupSession(user domain.User) string { return "sw410-session-" + user.ID }

// oauthClientLookupRequest is a request of user to workspace with user's session cookie.
func oauthClientLookupRequest(s *server, method, target string, body io.Reader, workspace string, user domain.User, values ...any) *http.Request {
	r := httptest.NewRequest(method, target, body)
	if method == http.MethodPost && strings.HasPrefix(target, "/oauth/") {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	r.Header.Set("X-Workspace-Key", workspace)
	ctx := store.ContextWithActor(context.WithValue(r.Context(), authUserContextKey{}, user), user)
	for index := 0; index+1 < len(values); index += 2 {
		ctx = context.WithValue(ctx, values[index], values[index+1])
	}
	if user.ID != "" {
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: oAuthClientLookupSession(user)})
	}
	return r.WithContext(ctx)
}

// oauthClientLookupServe runs handler under the request's resource-read scope, as the
// server's middleware does.
func oauthClientLookupServe(r *http.Request, handler func(http.ResponseWriter, *http.Request)) *httptest.ResponseRecorder {
	r, closeOutput := beginRequestResourceReads(r)
	defer closeOutput()
	w := httptest.NewRecorder()
	handler(w, r)
	return w
}

func oauthClientLookupResponse(name string, w *httptest.ResponseRecorder) string {
	return fmt.Sprintf("%s %d %s", name, w.Code, strings.TrimSpace(w.Body.String()))
}

func TestOAuthApplicationByClientIDRoutes(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleTeamCreateSetup(t)
	oldSlots := webhookDeliverySlots
	webhookDeliverySlots = make(chan struct{})
	t.Cleanup(func() { webhookDeliverySlots = oldSlots })
	workspace := f.data.Workspace.URLKey
	owner, admin, member := f.data.Viewer, f.principals[1].user, f.principals[2].user
	at := time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC)
	revoked := at.Add(time.Hour)
	apps := func(t *testing.T) {
		f.mutate(t, "scale_p21_sw410.apps", func(data *domain.Bootstrap) {
			data.OAuthApplications = append(data.OAuthApplications,
				domain.OAuthApplication{ID: "sw410-app-hook", ClientID: oAuthCOLDHook, ClientSecret: "sw410-hook-secret", Name: "SW410 Hook", Description: "Delivers hooks",
					LogoURL: "https://example.test/sw410.png", RedirectURIs: []string{oAuthClientLookupCallback}, Scopes: []string{"read", "write", "app:assignable"},
					SupportsClientCredentials: true, CredentialsAuthorizedBy: owner.ID, CreatorID: owner.ID, CreatedAt: at, UpdatedAt: at,
					DeveloperName: "SW410 Developer", DeveloperURL: "https://example.test/developer", GitHubUsername: "sw410-bot[bot]",
					WebhookEnabled: true, WebhookURL: "https://hooks.example.test/sw410", WebhookResourceTypes: []string{"Issue"}, WebhookSecretPrefix: "whsec_sw410"},
				domain.OAuthApplication{ID: "sw410-app-plain", ClientID: oAuthCOLDPlain, Name: "SW410 Plain", RedirectURIs: []string{oAuthClientLookupCallback}, CreatorID: owner.ID,
					GitHubUsername: "SW410-Plain-User", CreatedAt: at, UpdatedAt: at},
				domain.OAuthApplication{ID: "sw410-app-nourl", ClientID: noURL, ClientSecret: "sw410-nourl-secret", Name: "SW410 No URL", RedirectURIs: []string{oAuthClientLookupCallback}, Scopes: []string{},
					WebhookEnabled: true, CreatorID: owner.ID, CreatedAt: at, UpdatedAt: at},
				domain.OAuthApplication{ID: "sw410-app-dup-1", ClientID: oAuthClientLookupDup, Name: "SW410 Dup first", CreatorID: owner.ID, CreatedAt: at, UpdatedAt: at},
				domain.OAuthApplication{ID: "sw410-app-dup-2", ClientID: oAuthClientLookupDup, Name: "SW410 Dup second", WebhookEnabled: true, WebhookURL: "https://hooks.example.test/dup", CreatorID: owner.ID, CreatedAt: at, UpdatedAt: at},
				domain.OAuthApplication{ID: "sw410-app-empty", Name: "SW410 Empty client", CreatorID: owner.ID, CreatedAt: at, UpdatedAt: at},
				domain.OAuthApplication{ID: "sw410-app-large", ClientID: oAuthClientLookupLarge, Name: "SW410 Large", Description: strings.Repeat("large application description ", 40000), RedirectURIs: []string{}, Scopes: []string{"read"}, CreatorID: owner.ID, CreatedAt: at, UpdatedAt: at},
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
		f.mutate(t, "scale_p21_sw410.grants", func(data *domain.Bootstrap) {
			data.OAuthAuthorizations = append(data.OAuthAuthorizations,
				domain.OAuthAuthorization{ID: "sw410-grant-hook", ClientID: oAuthCOLDHook, ClientName: "Stored hook name", UserID: member.ID, Scopes: []string{"read"}, CreatedAt: at},
				domain.OAuthAuthorization{ID: "sw410-grant-nourl", ClientID: noURL, ClientName: "Stored no URL name", UserID: admin.ID, Scopes: []string{"read"}, CreatedAt: at},
				domain.OAuthAuthorization{ID: "sw410-grant-unknown", ClientID: "sw410-unregistered", ClientName: "Unregistered", UserID: member.ID, Scopes: []string{"read"}, CreatedAt: at},
				domain.OAuthAuthorization{ID: "sw410-grant-revoked", ClientID: oAuthClientLookupLarge, ClientName: "Revoked large", UserID: member.ID, Scopes: []string{"read"}, CreatedAt: at, RevokedAt: &revoked},
			)
		})
	}
	consent := func(client, actor string) func(*server) string {
		return func(s *server) string {
			query := url.Values{"client_id": {client}, "redirect_uri": {oAuthClientLookupCallback}, "response_type": {"code"}, "scope": {"read write"}, "actor": {actor}, "state": {"sw410"}}
			if client == oAuthCOLDPlain {
				query.Set("code_challenge", strings.Repeat("A", 43))
				query.Set("code_challenge_method", "S256")
			}
			r := oauthClientLookupRequest(s, http.MethodGet, "/api/oauth/authorization-request?"+query.Encode(), nil, workspace, owner)
			return oauthClientLookupResponse("consent", oauthClientLookupServe(r, s.getOAuthAuthorizationRequest))
		}
	}
	authorize := func(client, secret, actor string) func(*server) string {
		return func(s *server) string {
			body, _ := json.Marshal(map[string]any{"clientId": client, "redirectUri": oAuthClientLookupCallback, "responseType": "code", "scope": "read write", "actor": actor, "state": "sw410", "approve": true, "workspaceKey": workspace, "allPublicTeams": true})
			r := oauthClientLookupRequest(s, http.MethodPost, "/api/oauth/authorize", strings.NewReader(string(body)), workspace, owner)
			w := oauthClientLookupServe(r, s.decideOAuthAuthorization)
			result := []string{oauthClientLookupResponse("decide", w)}
			var decided struct{ Redirect string }
			if json.Unmarshal(w.Body.Bytes(), &decided) != nil || decided.Redirect == "" {
				return strings.Join(result, "\n")
			}
			redirect, err := url.Parse(decided.Redirect)
			if err != nil || redirect.Query().Get("code") == "" {
				return strings.Join(result, "\n")
			}
			form := url.Values{"grant_type": {"authorization_code"}, "code": {redirect.Query().Get("code")}, "client_id": {client}, "client_secret": {secret}, "redirect_uri": {oAuthClientLookupCallback}}
			r = oauthClientLookupRequest(s, http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()), "", domain.User{})
			w = oauthClientLookupServe(r, s.exchangeMCPToken)
			return strings.Join(append(result, oauthClientLookupResponse("token", w)), "\n")
		}
	}
	developerToken := func(client string) func(*server) string {
		return func(s *server) string {
			key := domain.APIKey{ID: "sw410-admin-key", Name: "SW410 admin key", CreatorID: owner.ID, SubjectUserID: owner.ID, Scopes: []string{"admin"}}
			r := oauthClientLookupRequest(s, http.MethodPost, "/api/oauth-applications/x/developer-token", nil, workspace, owner, apiKeyContextKey{}, key)
			w := oauthClientLookupServe(r, func(w http.ResponseWriter, r *http.Request) {
				s.issueApplicationToken(w, r, workspace, client, "", owner.ID, "app", []string{"read"}, "developer_token")
			})
			return oauthClientLookupResponse("developer-token", w)
		}
	}
	// Its installed entries carry the application lookup's fields, compared
	// exactly; authorizations and authorization-only entries are dropped
	// here (OAUTH cold compares the whole listing through the real routes).
	listing := func(s *server) string {
		r := oauthClientLookupRequest(s, http.MethodGet, "/api/oauth/authorized-applications", nil, workspace, owner)
		w := oauthClientLookupServe(r, s.listAuthorizedApplications)
		var entries []map[string]any
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &entries) != nil {
			return oauthClientLookupResponse("authorized-applications", w)
		}
		ctx, closeOutputs := applicationOutputContext(t.Context())
		defer closeOutputs()
		installations, err := s.store.ListApplications(ctx, workspace)
		if err != nil {
			t.Fatal(err)
		}
		kept := []map[string]any{}
		for _, entry := range entries {
			if slices.ContainsFunc(installations, func(item domain.ApplicationInstallation) bool {
				return item.ClientID == entry["clientId"] && item.Active && !item.Builtin
			}) {
				delete(entry, "authorizations")
				kept = append(kept, entry)
			}
		}
		raw, _ := json.Marshal(kept)
		return fmt.Sprintf("authorized-applications %d installed entries %s", w.Code, raw)
	}
	revokeThenList := func(s *server) string {
		r := oauthClientLookupRequest(s, http.MethodDelete, "/api/oauth/authorizations/sw410-grant-hook", nil, workspace, member)
		r.SetPathValue("id", "sw410-grant-hook")
		w := oauthClientLookupServe(r, s.revokeOAuthAuthorization)
		return oauthClientLookupResponse("revoke", w) + "\n" + listing(s)
	}
	githubUsers := func(s *server) string {
		// A GitHub delivery or review sync of the workspace.
		r := oauthClientLookupRequest(s, http.MethodPost, "/api/webhooks/github", nil, workspace, domain.User{})
		var users map[string]domain.User
		oauthClientLookupServe(r, func(_ http.ResponseWriter, r *http.Request) { users = s.githubAppUsers(r.Context(), workspace) })
		raw, _ := json.Marshal(users)
		return "github-app-users " + string(raw)
	}
	githubUsersTick := func(s *server) string {
		// The review sync tick and review import call it outside a request.
		raw, _ := json.Marshal(s.githubAppUsers(t.Context(), workspace))
		return "github-app-users-tick " + string(raw)
	}
	syncIdentity := func(client string) func(*server) string {
		return func(s *server) string {
			// The application edit routes sync after their write.
			r := oauthClientLookupRequest(s, http.MethodPatch, "/api/oauth-applications/x", nil, workspace, owner)
			var err error
			oauthClientLookupServe(r, func(_ http.ResponseWriter, r *http.Request) {
				err = s.store.SyncApplicationIdentity(r.Context(), client)
			})
			return "sync " + oauthMetadataResult(err)
		}
	}
	renamed := func(client string) func(*testing.T) {
		return func(t *testing.T) {
			f.mutate(t, "scale_p21_sw410.rename", func(data *domain.Bootstrap) {
				for index := range data.OAuthApplications {
					if data.OAuthApplications[index].ClientID == client {
						data.OAuthApplications[index].Name = "SW410 Renamed directly"
						data.OAuthApplications[index].LogoURL = "https://example.test/renamed.png"
					}
				}
			})
		}
	}
	// The token exchange (ExchangeOAuthGrant) installs the keys it writes:
	// no installed field differs from a reload (OAUTH cold covers its sweep).
	var exchanged []string
	cases := []struct {
		name  string
		setup func(*testing.T)
		call  func(*server) string
		stale []string
	}{
		{"consent-hook-app", apps, consent(oAuthCOLDHook, "app"), nil},
		{"consent-hook-user", apps, consent(oAuthCOLDHook, "user"), nil},
		{"consent-plain", apps, consent(oAuthCOLDPlain, "user"), nil},
		{"consent-unknown", apps, consent("sw410-unknown", "user"), nil},
		{"authorize-app-install", apps, authorize(oAuthCOLDHook, "sw410-hook-secret", "app"), exchanged},
		{"authorize-app-update-access", steps(apps, install(oAuthCOLDHook, "SW410 Hook", true)), authorize(oAuthCOLDHook, "sw410-hook-secret", "app"), exchanged},
		{"authorize-app-reinstall-uninstalled", steps(apps, install(oAuthCOLDHook, "SW410 Hook", false)), authorize(oAuthCOLDHook, "sw410-hook-secret", "app"), exchanged},
		{"authorize-app-no-url", apps, authorize(noURL, "sw410-nourl-secret", "app"), nil},
		{"authorize-user", apps, authorize(oAuthCOLDHook, "sw410-hook-secret", "user"), exchanged},
		{"developer-token", steps(apps, install(oAuthCOLDHook, "SW410 Hook", true)), developerToken(oAuthCOLDHook), nil},
		{"developer-token-uninstalled", steps(apps, install(oAuthCOLDHook, "SW410 Hook", false)), developerToken(oAuthCOLDHook), nil},
		{"authorized-applications", steps(apps, grants, install(oAuthCOLDHook, "SW410 Hook", true), install(oAuthCOLDPlain, "SW410 Plain", false), install(oAuthClientLookupDup, "Dup installation", true), install(oAuthClientLookupLarge, "Large installation", true), install("sw410-orphan", "Orphan installation", true)), listing, nil},
		{"authorized-applications-revoke", steps(apps, grants, install(oAuthCOLDHook, "SW410 Hook", true)), revokeThenList, nil},
		{"github-app-users", steps(apps, install(oAuthCOLDHook, "SW410 Hook", true), install(oAuthCOLDPlain, "SW410 Plain", true), install(noURL, "SW410 No URL", true), install(oAuthClientLookupDup, "Dup", true)), githubUsers, nil},
		{"github-app-users-tick", steps(apps, install(oAuthCOLDHook, "SW410 Hook", true), install(oAuthCOLDPlain, "SW410 Plain", false)), githubUsersTick, nil},
		{"github-app-users-uninstalled", steps(apps, install(oAuthCOLDHook, "SW410 Hook", false), install(oAuthCOLDPlain, "SW410 Plain", true)), githubUsers, nil},
		{"sync-renamed", steps(apps, install(oAuthCOLDPlain, "SW410 Plain", true), renamed(oAuthCOLDPlain)), syncIdentity(oAuthCOLDPlain), nil},
		{"sync-renamed-uninstalled", steps(apps, install(oAuthCOLDPlain, "SW410 Plain", false), renamed(oAuthCOLDPlain)), syncIdentity(oAuthCOLDPlain), nil},
		{"sync-duplicate-client", steps(apps, install(oAuthClientLookupDup, "Old dup", true)), syncIdentity(oAuthClientLookupDup), nil},
		{"sync-unknown", steps(apps, install("sw410-orphan", "Orphan", true)), syncIdentity("sw410-orphan"), nil},
		{"sync-builtin", steps(apps, func(t *testing.T) {
			if _, err := f.repository.EnsureBuiltinApplication(t.Context(), workspace, true); err != nil {
				t.Fatal(err)
			}
		}), syncIdentity(store.BuiltinApplicationClientID), nil},
		{"sync-large", steps(apps, install(oAuthClientLookupLarge, "Old large", true), renamed(oAuthClientLookupLarge)), syncIdentity(oAuthClientLookupLarge), nil},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			outcomes := f.oauthClientLookupDifferential(t, test.name, test.setup, test.call, test.stale...)
			t.Logf("legacy result %s", outcomes[0].Result)
		})
	}
}
