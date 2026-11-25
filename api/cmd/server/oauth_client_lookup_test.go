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

// SW410: the routes that resolve a registered OAuth application by client ID
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
	sw410Secret = regexp.MustCompile(`flow_(code|oauth|refresh|webhook)_[A-Za-z0-9_-]+`)
	sw410Hash   = regexp.MustCompile(`[0-9a-f]{64}`)
	// A new record's collection order is derived from the clock.
	sw410Order = regexp.MustCompile(`,-1[0-9]{15},`)
	// A token exchange starts a random refresh family.
	sw410Family = regexp.MustCompile(`family_[0-9a-f]{24}`)
)

const (
	sw410Hook     = "sw410-hook"
	sw410Plain    = "sw410-plain"
	sw410NoURL    = "sw410-nourl"
	sw410Dup      = "sw410-dup"
	sw410Large    = "sw410-large"
	sw410Callback = "https://example.test/sw410/callback"
)

type sw410Outcome struct {
	scaleP21SW294Outcome
	Codes, Refresh [][]any
	// Stale lists the installed fields that are not what a reload reads
	// (another writer on the route); reported, compared only across the
	// bounded sources.
	Stale []string `json:"-"`
}

// sw410Run is sw294Run for routes whose other accessors are not this
// group's: it reports (does not fail on) a full decode elsewhere on the
// route, and adds the global grant and refresh rows.
func (f scaleP21TeamCreateFixture) sw410Run(t *testing.T, source string, call func(*server) string) (sw410Outcome, time.Time, int64) {
	t.Helper()
	started := time.Now().UTC()
	service := f.service(t, source)
	workspace := f.data.Workspace.URLKey
	got := sw410Outcome{}
	var mu sync.Mutex
	service.store.SetWebhookSink(func(_ string, event domain.DomainEvent) {
		mu.Lock()
		defer mu.Unlock()
		got.Webhooks = append(got.Webhooks, scaleP21SW106Envelope(t, map[string]any{
			"type": event.Type, "aggregateId": event.AggregateID, "actorId": event.ActorID,
			"payload": event.Payload, "previousValues": event.PreviousValues, "snapshot": event.Snapshot,
		}))
	})
	service.store.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
		mu.Lock()
		defer mu.Unlock()
		got.Realtime = append(got.Realtime, scaleP21SW106Envelope(t, map[string]any{
			"type": event.Type, "aggregateId": event.AggregateID, "actorId": event.ActorID,
			"clientId": event.ClientID, "payload": event.Payload,
		}))
	})
	prior := map[any]bool{}
	for _, event := range f.state(t).Events {
		prior[event[0]] = true
	}
	before := scaleP21SW294Image(t, service.store, workspace, scaleP21SW294Fields...)
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
	got.Extra = f.sw294Extra(t)
	got.Codes = f.rows(t, `SELECT data,expires_at,used_at IS NULL FROM oauth_authorization_codes WHERE 1=? ORDER BY created_at,data`, 1)
	got.Refresh = f.rows(t, `SELECT data,expires_at,revoked_at IS NULL FROM oauth_refresh_tokens WHERE 1=? ORDER BY created_at,data`, 1)
	if service.store.BoundedMetadataEnabled() && service.store.MetadataWorkUsedForTesting() != 0 {
		t.Fatalf("%s: retained metadata work %d", source, service.store.MetadataWorkUsedForTesting())
	}
	installed := scaleP21SW294Image(t, service.store, workspace, scaleP21SW294ExactFields...)
	reloaded, err := store.OpenDatabase(f.reader.config)
	if err != nil {
		t.Fatal(err)
	}
	fresh := scaleP21SW294Image(t, reloaded, workspace, scaleP21SW294ExactFields...)
	if err := reloaded.Close(); err != nil {
		t.Fatal(err)
	}
	if service.store.BoundedMetadataEnabled() {
		for _, name := range scaleP21SW294ExactFields {
			want, have := scaleP21TriageNormalize(t, fresh[name], started), scaleP21TriageNormalize(t, installed[name], started)
			if want != have {
				got.Stale = append(got.Stale, name)
				t.Logf("%s: installed %s is not the reloaded one: %s", source, name, scaleP21M2Diff(want, have))
			}
		}
	}
	got.Installed = scaleP21SW294Delta(t, before, installed)
	for _, name := range scaleP21SW294ExactFields {
		if !slices.Contains(scaleP21SW294Fields, name) {
			delete(fresh, name)
		}
	}
	got.Reloaded, _ = json.Marshal(fresh)
	return got, started, decoded
}

func sw410Normalize(t *testing.T, outcome sw410Outcome, started time.Time) string {
	t.Helper()
	text := scaleP21TriageNormalize(t, outcome, started)
	text = sw410Secret.ReplaceAllString(text, "flow_${1}_<secret>")
	text = sw410Order.ReplaceAllString(text, ",<order>,")
	text = sw410Family.ReplaceAllString(text, "family_<random>")
	return sw410Hash.ReplaceAllString(text, "<hash>")
}

func (f scaleP21TeamCreateFixture) sw410Differential(t *testing.T, name string, setup func(*testing.T), call func(*server) string, stale ...string) []sw410Outcome {
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
		normalized = append(normalized, sw410Normalize(t, got, started))
		if len(normalized) > 1 && normalized[len(normalized)-1] != normalized[0] {
			t.Errorf("%s: %s changed: %s", name, source, scaleP21M2Diff(normalized[0], normalized[len(normalized)-1]))
		}
	}
	return outcomes
}

// sw410Session is the session token the fixture seeds for user.
func sw410Session(user domain.User) string { return "sw410-session-" + user.ID }

// sw410Request is a request of user to workspace with user's session cookie.
func sw410Request(s *server, method, target string, body io.Reader, workspace string, user domain.User, values ...any) *http.Request {
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
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sw410Session(user)})
	}
	return r.WithContext(ctx)
}

// sw410Serve runs handler under the request's resource-read scope, as the
// server's middleware does.
func sw410Serve(r *http.Request, handler func(http.ResponseWriter, *http.Request)) *httptest.ResponseRecorder {
	r, closeOutput := beginRequestResourceReads(r)
	defer closeOutput()
	w := httptest.NewRecorder()
	handler(w, r)
	return w
}

func sw410Response(name string, w *httptest.ResponseRecorder) string {
	return fmt.Sprintf("%s %d %s", name, w.Code, strings.TrimSpace(w.Body.String()))
}

func TestScaleP21SW410OAuthApplicationByClientIDRoutes(t *testing.T) {
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
	apps := func(t *testing.T) {
		f.mutate(t, "scale_p21_sw410.apps", func(data *domain.Bootstrap) {
			data.OAuthApplications = append(data.OAuthApplications,
				domain.OAuthApplication{ID: "sw410-app-hook", ClientID: sw410Hook, ClientSecret: "sw410-hook-secret", Name: "SW410 Hook", Description: "Delivers hooks",
					LogoURL: "https://example.test/sw410.png", RedirectURIs: []string{sw410Callback}, Scopes: []string{"read", "write", "app:assignable"},
					SupportsClientCredentials: true, CredentialsAuthorizedBy: owner.ID, CreatorID: owner.ID, CreatedAt: at, UpdatedAt: at,
					DeveloperName: "SW410 Developer", DeveloperURL: "https://example.test/developer", GitHubUsername: "sw410-bot[bot]",
					WebhookEnabled: true, WebhookURL: "https://hooks.example.test/sw410", WebhookResourceTypes: []string{"Issue"}, WebhookSecretPrefix: "whsec_sw410"},
				domain.OAuthApplication{ID: "sw410-app-plain", ClientID: sw410Plain, Name: "SW410 Plain", RedirectURIs: []string{sw410Callback}, CreatorID: owner.ID,
					GitHubUsername: "SW410-Plain-User", CreatedAt: at, UpdatedAt: at},
				domain.OAuthApplication{ID: "sw410-app-nourl", ClientID: sw410NoURL, ClientSecret: "sw410-nourl-secret", Name: "SW410 No URL", RedirectURIs: []string{sw410Callback}, Scopes: []string{},
					WebhookEnabled: true, CreatorID: owner.ID, CreatedAt: at, UpdatedAt: at},
				domain.OAuthApplication{ID: "sw410-app-dup-1", ClientID: sw410Dup, Name: "SW410 Dup first", CreatorID: owner.ID, CreatedAt: at, UpdatedAt: at},
				domain.OAuthApplication{ID: "sw410-app-dup-2", ClientID: sw410Dup, Name: "SW410 Dup second", WebhookEnabled: true, WebhookURL: "https://hooks.example.test/dup", CreatorID: owner.ID, CreatedAt: at, UpdatedAt: at},
				domain.OAuthApplication{ID: "sw410-app-empty", Name: "SW410 Empty client", CreatorID: owner.ID, CreatedAt: at, UpdatedAt: at},
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
		f.mutate(t, "scale_p21_sw410.grants", func(data *domain.Bootstrap) {
			data.OAuthAuthorizations = append(data.OAuthAuthorizations,
				domain.OAuthAuthorization{ID: "sw410-grant-hook", ClientID: sw410Hook, ClientName: "Stored hook name", UserID: member.ID, Scopes: []string{"read"}, CreatedAt: at},
				domain.OAuthAuthorization{ID: "sw410-grant-nourl", ClientID: sw410NoURL, ClientName: "Stored no URL name", UserID: admin.ID, Scopes: []string{"read"}, CreatedAt: at},
				domain.OAuthAuthorization{ID: "sw410-grant-unknown", ClientID: "sw410-unregistered", ClientName: "Unregistered", UserID: member.ID, Scopes: []string{"read"}, CreatedAt: at},
				domain.OAuthAuthorization{ID: "sw410-grant-revoked", ClientID: sw410Large, ClientName: "Revoked large", UserID: member.ID, Scopes: []string{"read"}, CreatedAt: at, RevokedAt: &revoked},
			)
		})
	}
	consent := func(client, actor string) func(*server) string {
		return func(s *server) string {
			query := url.Values{"client_id": {client}, "redirect_uri": {sw410Callback}, "response_type": {"code"}, "scope": {"read write"}, "actor": {actor}, "state": {"sw410"}}
			if client == sw410Plain {
				query.Set("code_challenge", strings.Repeat("A", 43))
				query.Set("code_challenge_method", "S256")
			}
			r := sw410Request(s, http.MethodGet, "/api/oauth/authorization-request?"+query.Encode(), nil, workspace, owner)
			return sw410Response("consent", sw410Serve(r, s.getOAuthAuthorizationRequest))
		}
	}
	authorize := func(client, secret, actor string) func(*server) string {
		return func(s *server) string {
			body, _ := json.Marshal(map[string]any{"clientId": client, "redirectUri": sw410Callback, "responseType": "code", "scope": "read write", "actor": actor, "state": "sw410", "approve": true, "workspaceKey": workspace, "allPublicTeams": true})
			r := sw410Request(s, http.MethodPost, "/api/oauth/authorize", strings.NewReader(string(body)), workspace, owner)
			w := sw410Serve(r, s.decideOAuthAuthorization)
			result := []string{sw410Response("decide", w)}
			var decided struct{ Redirect string }
			if json.Unmarshal(w.Body.Bytes(), &decided) != nil || decided.Redirect == "" {
				return strings.Join(result, "\n")
			}
			redirect, err := url.Parse(decided.Redirect)
			if err != nil || redirect.Query().Get("code") == "" {
				return strings.Join(result, "\n")
			}
			form := url.Values{"grant_type": {"authorization_code"}, "code": {redirect.Query().Get("code")}, "client_id": {client}, "client_secret": {secret}, "redirect_uri": {sw410Callback}}
			r = sw410Request(s, http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()), "", domain.User{})
			w = sw410Serve(r, s.exchangeMCPToken)
			return strings.Join(append(result, sw410Response("token", w)), "\n")
		}
	}
	developerToken := func(client string) func(*server) string {
		return func(s *server) string {
			key := domain.APIKey{ID: "sw410-admin-key", Name: "SW410 admin key", CreatorID: owner.ID, SubjectUserID: owner.ID, Scopes: []string{"admin"}}
			r := sw410Request(s, http.MethodPost, "/api/oauth-applications/x/developer-token", nil, workspace, owner, apiKeyContextKey{}, key)
			w := sw410Serve(r, func(w http.ResponseWriter, r *http.Request) {
				s.issueApplicationToken(w, r, workspace, client, "", owner.ID, "app", []string{"read"}, "developer_token")
			})
			return sw410Response("developer-token", w)
		}
	}
	// Its installed entries carry the application lookup's fields, compared
	// exactly; authorizations and authorization-only entries are dropped
	// here (SW430 compares the whole listing through the real routes).
	listing := func(s *server) string {
		r := sw410Request(s, http.MethodGet, "/api/oauth/authorized-applications", nil, workspace, owner)
		w := sw410Serve(r, s.listAuthorizedApplications)
		var entries []map[string]any
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &entries) != nil {
			return sw410Response("authorized-applications", w)
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
		r := sw410Request(s, http.MethodDelete, "/api/oauth/authorizations/sw410-grant-hook", nil, workspace, member)
		r.SetPathValue("id", "sw410-grant-hook")
		w := sw410Serve(r, s.revokeOAuthAuthorization)
		return sw410Response("revoke", w) + "\n" + listing(s)
	}
	githubUsers := func(s *server) string {
		// A GitHub delivery or review sync of the workspace.
		r := sw410Request(s, http.MethodPost, "/api/webhooks/github", nil, workspace, domain.User{})
		var users map[string]domain.User
		sw410Serve(r, func(_ http.ResponseWriter, r *http.Request) { users = s.githubAppUsers(r.Context(), workspace) })
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
			r := sw410Request(s, http.MethodPatch, "/api/oauth-applications/x", nil, workspace, owner)
			var err error
			sw410Serve(r, func(_ http.ResponseWriter, r *http.Request) {
				err = s.store.SyncApplicationIdentity(r.Context(), client)
			})
			return "sync " + scaleP21SW294Result(err)
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
	// no installed field differs from a reload (SW430 covers its sweep).
	var exchanged []string
	cases := []struct {
		name  string
		setup func(*testing.T)
		call  func(*server) string
		stale []string
	}{
		{"consent-hook-app", apps, consent(sw410Hook, "app"), nil},
		{"consent-hook-user", apps, consent(sw410Hook, "user"), nil},
		{"consent-plain", apps, consent(sw410Plain, "user"), nil},
		{"consent-unknown", apps, consent("sw410-unknown", "user"), nil},
		{"authorize-app-install", apps, authorize(sw410Hook, "sw410-hook-secret", "app"), exchanged},
		{"authorize-app-update-access", steps(apps, install(sw410Hook, "SW410 Hook", true)), authorize(sw410Hook, "sw410-hook-secret", "app"), exchanged},
		{"authorize-app-reinstall-uninstalled", steps(apps, install(sw410Hook, "SW410 Hook", false)), authorize(sw410Hook, "sw410-hook-secret", "app"), exchanged},
		{"authorize-app-no-url", apps, authorize(sw410NoURL, "sw410-nourl-secret", "app"), nil},
		{"authorize-user", apps, authorize(sw410Hook, "sw410-hook-secret", "user"), exchanged},
		{"developer-token", steps(apps, install(sw410Hook, "SW410 Hook", true)), developerToken(sw410Hook), nil},
		{"developer-token-uninstalled", steps(apps, install(sw410Hook, "SW410 Hook", false)), developerToken(sw410Hook), nil},
		{"authorized-applications", steps(apps, grants, install(sw410Hook, "SW410 Hook", true), install(sw410Plain, "SW410 Plain", false), install(sw410Dup, "Dup installation", true), install(sw410Large, "Large installation", true), install("sw410-orphan", "Orphan installation", true)), listing, nil},
		{"authorized-applications-revoke", steps(apps, grants, install(sw410Hook, "SW410 Hook", true)), revokeThenList, nil},
		{"github-app-users", steps(apps, install(sw410Hook, "SW410 Hook", true), install(sw410Plain, "SW410 Plain", true), install(sw410NoURL, "SW410 No URL", true), install(sw410Dup, "Dup", true)), githubUsers, nil},
		{"github-app-users-tick", steps(apps, install(sw410Hook, "SW410 Hook", true), install(sw410Plain, "SW410 Plain", false)), githubUsersTick, nil},
		{"github-app-users-uninstalled", steps(apps, install(sw410Hook, "SW410 Hook", false), install(sw410Plain, "SW410 Plain", true)), githubUsers, nil},
		{"sync-renamed", steps(apps, install(sw410Plain, "SW410 Plain", true), renamed(sw410Plain)), syncIdentity(sw410Plain), nil},
		{"sync-renamed-uninstalled", steps(apps, install(sw410Plain, "SW410 Plain", false), renamed(sw410Plain)), syncIdentity(sw410Plain), nil},
		{"sync-duplicate-client", steps(apps, install(sw410Dup, "Old dup", true)), syncIdentity(sw410Dup), nil},
		{"sync-unknown", steps(apps, install("sw410-orphan", "Orphan", true)), syncIdentity("sw410-orphan"), nil},
		{"sync-builtin", steps(apps, func(t *testing.T) {
			if _, err := f.repository.EnsureBuiltinApplication(t.Context(), workspace, true); err != nil {
				t.Fatal(err)
			}
		}), syncIdentity(store.BuiltinApplicationClientID), nil},
		{"sync-large", steps(apps, install(sw410Large, "Old large", true), renamed(sw410Large)), syncIdentity(sw410Large), nil},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			outcomes := f.sw410Differential(t, test.name, test.setup, test.call, test.stale...)
			t.Logf("legacy result %s", outcomes[0].Result)
		})
	}
}
