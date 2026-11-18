package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// SW450: the API key and MCP authorization settings routes, served through
// the real cookie/Bearer authentication and route mux on the legacy store, a
// warm bounded view and a bounded view loaded from SQL:
//   - GET /api/api-keys (COLD: the registered cold facade), for every role,
//     a member outside the private team, a guest, an unknown workspace, an
//     empty and a long list, revoked, expired and OAuth keys;
//   - POST/PATCH/DELETE /api/api-keys and the secret rotation;
//   - GET /api/oauth/authorized-applications and DELETE
//     /api/oauth/authorized-applications/mcp (COLD read of the grants, the
//     revocations on the bounded OAuth metadata transaction), by admins and
//     members, and the single-grant revocation;
//   - then the revoked keys and tokens authenticating (refused as legacy).
// Responses, every owned row, audit, events, publications and the installed
// image must agree byte for byte except generated IDs, secrets and clocks;
// bounded, the installed image must equal a reload exactly.

func sw450Handler(s *server) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/api-keys", s.listAPIKeys)
	mux.HandleFunc("POST /api/api-keys", s.createAPIKey)
	mux.HandleFunc("PATCH /api/api-keys/{id}", s.updateAPIKey)
	mux.HandleFunc("DELETE /api/api-keys/{id}", s.revokeAPIKey)
	mux.HandleFunc("POST /api/api-keys/{id}/rotate-secret", s.rotateAPIKeySecret)
	mux.HandleFunc("GET /api/oauth/authorized-applications", s.listAuthorizedApplications)
	mux.HandleFunc("DELETE /api/oauth/authorized-applications/mcp", s.revokeMCPAuthorizations)
	mux.HandleFunc("DELETE /api/oauth/authorizations/{id}", s.revokeOAuthAuthorization)
	mux.HandleFunc("GET /api/oauth-applications", s.listOAuthApplications)
	mux.HandleFunc("GET /api/oauth-applications/{id}", s.getOAuthApplication)
	mux.HandleFunc("POST /api/oauth-applications", s.createOAuthApplication)
	mux.HandleFunc("DELETE /api/oauth-applications/{id}", s.deleteOAuthApplication)
	return s.withColdMetadataRouteMux(mux, s.authenticate(mux))
}

func sw450Do(s *server, method, target, body, workspace string, user domain.User, bearer string) *httptest.ResponseRecorder {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, target, nil)
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
	sw450Handler(s).ServeHTTP(w, r)
	return w
}

// Generated API key secrets and their 17-character prefixes, OAuth client
// IDs and secrets. Fixture values use other prefixes, so only generated ones
// are replaced.
var sw450Generated = regexp.MustCompile(`flow_(api|client|secret)_[A-Za-z0-9_-]{8,}`)

// sw450Differential is sw430Differential with sessions for every principal
// and generated API key secrets normalized.
func (f scaleP21TeamCreateFixture) sw450Differential(t *testing.T, name string, setup func(*testing.T), call func(*server) string) []sw410Outcome {
	t.Helper()
	prepare := f.sw294Prepare(setup)
	users := []domain.User{}
	for _, principal := range f.principals {
		users = append(users, principal.user)
	}
	f.scaleP21TriageWriterFixture.prepare = func(t *testing.T) {
		f.exec(t, `DELETE FROM oauth_authorization_codes WHERE 1=?`, 1)
		f.exec(t, `DELETE FROM oauth_refresh_tokens WHERE 1=?`, 1)
		stamp := time.Now().UTC()
		for _, user := range users {
			f.exec(t, `DELETE FROM auth_sessions WHERE user_id=?`, user.ID)
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
		// A legacy key a writer handed keeps an empty teamRestriction until a
		// reload fills it (sw430Differential).
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
		outcomes = append(outcomes, got)
		text := sw450Generated.ReplaceAllString(sw410Normalize(t, got, started), "flow_${1}_<generated>")
		normalized = append(normalized, text)
		if dir := os.Getenv("FLOW_SW450_DUMP"); dir != "" {
			_ = os.WriteFile(filepath.Join(dir, name+"-"+source+".json"), []byte(text), 0o600)
		}
		if len(normalized) > 1 && normalized[len(normalized)-1] != normalized[0] {
			t.Errorf("%s: %s changed: %s", name, source, scaleP21M2Diff(normalized[0], normalized[len(normalized)-1]))
		}
	}
	return outcomes
}

func TestScaleP21SW450APIKeyAndMCPSettingsRoutes(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21TeamCreateSetup(t)
	oldSlots := webhookDeliverySlots
	webhookDeliverySlots = make(chan struct{})
	t.Cleanup(func() { webhookDeliverySlots = oldSlots })
	workspace := f.data.Workspace.URLKey
	if len(f.principals) < 5 {
		t.Fatalf("principals %d", len(f.principals))
	}
	// owner; admin (private team); member of the public team only (outside the
	// private team); guest (public team); member of the private team only.
	owner, admin, member, guest, inside := f.principals[0].user, f.principals[1].user, f.principals[2].user, f.principals[3].user, f.principals[4].user
	at := time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC)
	revoked := at.Add(time.Hour)
	recent := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Hour)
	expired := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Hour)
	live := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Hour)
	const (
		ownerSecret    = "sw450-secret-owner-key"
		memberSecret   = "sw450-secret-member-key"
		selectedSecret = "sw450-secret-member-selected"
		revokedSecret  = "sw450-secret-owner-revoked"
		expiredSecret  = "sw450-secret-member-expired"
		mcpSecret      = "sw450-secret-mcp-member"
		mcpAdminSecret = "sw450-secret-mcp-admin"
		appSecret      = "sw450-secret-app-member"
		mcpA, mcpB     = "flow_mcp_sw450a", "flow_mcp_sw450b"
		mcpInstalled   = "flow_mcp_sw450installed"
	)
	key := func(id, name, creator, secret string, change func(*domain.APIKey)) domain.APIKey {
		item := domain.APIKey{ID: id, Name: name, Prefix: "sw450_" + strings.TrimPrefix(id, "sw450-key-"), SecretHash: secretHash(secret), CreatorID: creator, CreatedAt: at, TeamIDs: []string{}, TeamRestriction: "all"}
		if change != nil {
			change(&item)
		}
		return item
	}
	keys := func(t *testing.T) {
		f.mutate(t, "scale_p21_sw450.keys", func(data *domain.Bootstrap) {
			data.APIKeys = append(data.APIKeys,
				key("sw450-key-owner", "Owner key", owner.ID, ownerSecret, func(k *domain.APIKey) { k.LastUsedAt = &recent }),
				key("sw450-key-owner-revoked", "Owner revoked", owner.ID, revokedSecret, func(k *domain.APIKey) { k.RevokedAt = &revoked }),
				key("sw450-key-owner-scoped", "Owner scoped", owner.ID, "sw450-secret-owner-scoped", func(k *domain.APIKey) {
					k.Scopes = []string{"read", "admin"}
					k.ExpiresAt = &live
				}),
				key("sw450-key-admin", "Admin key", admin.ID, "sw450-secret-admin", nil),
				key("sw450-key-member", "Member key", member.ID, memberSecret, func(k *domain.APIKey) { k.Scopes = []string{"read", "write"} }),
				key("sw450-key-member-selected", "Member selected", member.ID, selectedSecret, func(k *domain.APIKey) {
					k.TeamRestriction = "selected"
					k.TeamIDs = []string{f.public}
				}),
				key("sw450-key-member-expired", "Member expired", member.ID, expiredSecret, func(k *domain.APIKey) { k.ExpiresAt = &expired }),
				key("sw450-key-member-empty-scopes", "Member deny all", member.ID, "sw450-secret-member-deny", func(k *domain.APIKey) { k.Scopes = []string{} }),
				key("sw450-key-guest", "Guest key", guest.ID, "sw450-secret-guest", nil),
				key("sw450-key-inside", "Inside key", inside.ID, "sw450-secret-inside", func(k *domain.APIKey) {
					k.TeamRestriction = "selected"
					k.TeamIDs = []string{f.private}
				}),
				// An MCP OAuth token of the member's grant (revoked with it).
				key("sw450-key-mcp-member", "MCP OAuth token", member.ID, mcpSecret, func(k *domain.APIKey) {
					k.SubjectUserID, k.OAuthClientID, k.AuthorizationID, k.GrantType, k.ExpiresAt = member.ID, mcpA, "sw450-grant-mcp-member", "authorization_code", &live
					k.Scopes = []string{"read", "write"}
				}),
				key("sw450-key-mcp-admin", "MCP OAuth token", admin.ID, mcpAdminSecret, func(k *domain.APIKey) {
					k.SubjectUserID, k.OAuthClientID, k.AuthorizationID, k.GrantType, k.ExpiresAt = admin.ID, mcpB, "sw450-grant-mcp-admin", "authorization_code", &live
					k.Scopes = []string{"read"}
				}),
				key("sw450-key-app-member", "App OAuth token", member.ID, appSecret, func(k *domain.APIKey) {
					k.SubjectUserID, k.OAuthClientID, k.AuthorizationID, k.GrantType, k.ExpiresAt = member.ID, sw410Plain, "sw450-grant-app", "authorization_code", &live
					k.Scopes = []string{"read"}
				}),
			)
		})
	}
	many := func(t *testing.T) {
		f.mutate(t, "scale_p21_sw450.many", func(data *domain.Bootstrap) {
			for index := range 120 {
				creator := member.ID
				if index%3 == 0 {
					creator = admin.ID
				}
				item := key(fmt.Sprintf("sw450-key-bulk-%03d", index), fmt.Sprintf("Bulk key %03d", index), creator, fmt.Sprintf("sw450-secret-bulk-%03d", index), nil)
				item.CreatedAt = at.Add(time.Duration(index) * time.Minute)
				if index%7 == 0 {
					item.RevokedAt = &revoked
				}
				if index%11 == 0 {
					item.TeamRestriction, item.TeamIDs = "selected", []string{f.public, f.private}
				}
				data.APIKeys = append(data.APIKeys, item)
			}
		})
	}
	apps := func(t *testing.T) {
		f.exec(t, `DELETE FROM oauth_clients WHERE client_id LIKE ?`, "flow_mcp_sw450%")
		for _, client := range []domain.OAuthClient{
			{ClientID: mcpA, ClientName: "SW450 Claude", ClientURI: "https://claude.example.test", RedirectURIs: []string{"https://claude.example.test/cb"}, CreatedAt: at},
			{ClientID: mcpB, ClientName: "SW450 Cursor", ClientURI: "https://cursor.example.test", RedirectURIs: []string{"https://cursor.example.test/cb"}, CreatedAt: at},
			{ClientID: mcpInstalled, ClientName: "SW450 Installed MCP", RedirectURIs: []string{"https://mcp.example.test/cb"}, CreatedAt: at},
		} {
			if err := f.repository.RegisterOAuthClient(t.Context(), client); err != nil {
				t.Fatal(err)
			}
		}
		f.mutate(t, "scale_p21_sw450.apps", func(data *domain.Bootstrap) {
			data.OAuthApplications = append(data.OAuthApplications,
				domain.OAuthApplication{ID: "sw450-app-plain", ClientID: sw410Plain, ClientSecret: "sw450-plain-secret", Name: "SW450 Plain", GitHubUsername: "SW450-Plain-User", RedirectURIs: []string{sw410Callback}, Scopes: []string{"read", "write"}, CreatorID: owner.ID, CreatedAt: at, UpdatedAt: at})
			data.OAuthAuthorizations = append(data.OAuthAuthorizations,
				domain.OAuthAuthorization{ID: "sw450-grant-mcp-member", ClientID: mcpA, ClientName: "SW450 Claude", UserID: member.ID, Scopes: []string{"read", "write"}, CreatedAt: at, LastUsedAt: &recent},
				domain.OAuthAuthorization{ID: "sw450-grant-mcp-member-b", ClientID: mcpB, ClientName: "SW450 Cursor", UserID: member.ID, Scopes: []string{"read"}, CreatedAt: at.Add(time.Minute)},
				domain.OAuthAuthorization{ID: "sw450-grant-mcp-admin", ClientID: mcpB, ClientName: "SW450 Cursor", UserID: admin.ID, Scopes: []string{"read", "admin"}, CreatedAt: at, LastUsedAt: &at},
				domain.OAuthAuthorization{ID: "sw450-grant-mcp-owner", ClientID: mcpA, ClientName: "SW450 Claude", UserID: owner.ID, Scopes: []string{"read"}, CreatedAt: at.Add(2 * time.Minute)},
				domain.OAuthAuthorization{ID: "sw450-grant-mcp-guest", ClientID: mcpA, ClientName: "SW450 Claude", UserID: guest.ID, Scopes: []string{"read"}, CreatedAt: at},
				domain.OAuthAuthorization{ID: "sw450-grant-mcp-revoked", ClientID: mcpA, ClientName: "SW450 Claude", UserID: owner.ID, Scopes: []string{"read"}, CreatedAt: at, RevokedAt: &revoked},
				domain.OAuthAuthorization{ID: "sw450-grant-mcp-app-actor", ClientID: mcpB, ClientName: "SW450 Cursor", UserID: member.ID, Actor: "app", Scopes: []string{"read"}, CreatedAt: at},
				domain.OAuthAuthorization{ID: "sw450-grant-mcp-installed", ClientID: mcpInstalled, ClientName: "Installed MCP", UserID: member.ID, Scopes: []string{"read"}, CreatedAt: at},
				domain.OAuthAuthorization{ID: "sw450-grant-app", ClientID: sw410Plain, ClientName: "SW450 Plain", UserID: member.ID, Scopes: []string{"read"}, CreatedAt: at},
			)
		})
		if _, err := f.repository.InstallApplication(t.Context(), workspace, domain.ApplicationInstallation{ClientID: mcpInstalled, Name: "Installed MCP", InstalledBy: owner.ID, Scopes: []string{"read"}, TeamIDs: []string{f.public}, Active: true}, ""); err != nil {
			t.Fatal(err)
		}
	}
	steps := func(fns ...func(*testing.T)) func(*testing.T) {
		return func(t *testing.T) {
			for _, fn := range fns {
				fn(t)
			}
		}
	}
	type trace struct{ lines []string }
	// current is the running case: steps fail it, not the parent test.
	current := t
	type step func(*testing.T, *server, *trace)
	record := func(out *trace, name string, w *httptest.ResponseRecorder) {
		out.lines = append(out.lines, sw410Response(name, w))
	}
	run := func(steps ...step) func(*server) string {
		return func(s *server) string {
			out := &trace{}
			for _, step := range steps {
				step(current, s, out)
			}
			return strings.Join(out.lines, "\n")
		}
	}
	do := func(name, method, target, body string, user domain.User) step {
		return func(t *testing.T, s *server, out *trace) {
			record(out, name, sw450Do(s, method, target, body, workspace, user, ""))
		}
	}
	list := func(user domain.User) step {
		return do("list-keys "+user.Name, http.MethodGet, "/api/api-keys", "", user)
	}
	// bearer authenticates secret through the middleware (a valid key is
	// refused on the personal routes, an invalid one is not authenticated)
	// and waits for its usage stamp.
	bearer := func(name, target, secret string) step {
		return func(t *testing.T, s *server, out *trace) {
			w := sw450Do(s, http.MethodGet, target, "", workspace, domain.User{}, secret)
			record(out, "bearer "+name, w)
			// Authentication stamps every key it accepts (whatever the
			// route answers next); a refused key stamps nothing.
			stamp := secret
			if w.Code == http.StatusUnauthorized && strings.Contains(w.Body.String(), "Invalid authorization credentials") {
				stamp = ""
			}
			if _, item, found := s.store.FindAPIKey(secretHash(secret)); !found || item.RevokedAt != nil {
				stamp = ""
			}
			sw430Quiet(t, s, workspace, stamp)
		}
	}
	create := func(user domain.User, body map[string]any) step {
		raw, _ := json.Marshal(body)
		return do("create "+user.Name, http.MethodPost, "/api/api-keys", string(raw), user)
	}
	update := func(user domain.User, id string, body map[string]any) step {
		raw, _ := json.Marshal(body)
		return do("update "+id+" "+user.Name, http.MethodPatch, "/api/api-keys/"+id, string(raw), user)
	}
	revoke := func(user domain.User, id string) step {
		return do("revoke "+id+" "+user.Name, http.MethodDelete, "/api/api-keys/"+id, "", user)
	}
	rotate := func(user domain.User, id string) step {
		return do("rotate "+id+" "+user.Name, http.MethodPost, "/api/api-keys/"+id+"/rotate-secret", "", user)
	}
	applications := func(user domain.User) step {
		return do("applications "+user.Name, http.MethodGet, "/api/oauth/authorized-applications", "", user)
	}
	revokeMCP := func(user domain.User) step {
		return do("revoke-mcp "+user.Name, http.MethodDelete, "/api/oauth/authorized-applications/mcp", "", user)
	}
	revokeGrant := func(user domain.User, id string) step {
		return do("revoke-grant "+id+" "+user.Name, http.MethodDelete, "/api/oauth/authorizations/"+id, "", user)
	}
	other := func(name, method, target string) step {
		return func(t *testing.T, s *server, out *trace) {
			record(out, name, sw450Do(s, method, target, "", "sw450-missing", owner, ""))
		}
	}
	// probeTokens authenticates every fixture token after the case.
	probeTokens := func() []step {
		return []step{
			bearer("owner", "/api/oauth/authorized-applications", ownerSecret),
			bearer("owner-revoked", "/api/oauth/authorized-applications", revokedSecret),
			bearer("member", "/api/oauth/authorized-applications", memberSecret),
			bearer("member-expired", "/api/oauth/authorized-applications", expiredSecret),
			bearer("mcp-member", "/api/oauth/authorized-applications", mcpSecret),
			bearer("mcp-admin", "/api/oauth/authorized-applications", mcpAdminSecret),
			bearer("app-member", "/api/oauth/authorized-applications", appSecret),
		}
	}
	// with authenticates every fixture token before and after the steps.
	with := func(probes []step, rest ...step) []step {
		return append(append(append([]step{}, probes...), rest...), probes...)
	}
	permission := func(value string) func(*testing.T) {
		return func(t *testing.T) {
			f.mutate(t, "scale_p21_sw450.permission", func(data *domain.Bootstrap) { data.WorkspaceSettings.APIKeyPermission = value })
		}
	}
	application := func(name, method, target, body string, user domain.User, secret string) step {
		return func(t *testing.T, s *server, out *trace) {
			record(out, name, sw450Do(s, method, target, body, workspace, user, secret))
			// The application webhook reconcile a create starts.
			sw430Quiet(t, s, workspace, "")
		}
	}
	installPlain := func(t *testing.T) {
		if _, err := f.repository.InstallApplication(t.Context(), workspace, domain.ApplicationInstallation{ClientID: sw410Plain, Name: "SW450 Plain", InstalledBy: owner.ID, Scopes: []string{"read", "write"}, TeamIDs: []string{f.public}, Active: true}, ""); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name  string
		setup func(*testing.T)
		call  func(*server) string
	}{
		{"list-owner", keys, run(list(owner))},
		{"list-admin", keys, run(list(admin))},
		{"list-member", keys, run(list(member))},
		{"list-member-private-only", keys, run(list(inside))},
		{"list-guest", keys, run(list(guest))},
		{"list-unknown-workspace", keys, run(other("list-keys-unknown", http.MethodGet, "/api/api-keys"))},
		{"list-empty", nil, run(list(owner), list(member))},
		{"list-many", steps(keys, many), run(list(owner), list(admin), list(member))},
		{"list-bearer", keys, run(bearer("list-owner", "/api/api-keys", ownerSecret), bearer("list-selected", "/api/api-keys", selectedSecret), bearer("list-revoked", "/api/api-keys", revokedSecret), bearer("list-expired", "/api/api-keys", expiredSecret))},
		{"create-owner", keys, run(create(owner, map[string]any{"name": "New owner key", "scopes": []string{"read", "admin"}}), list(owner))},
		{"create-member-selected", keys, run(create(member, map[string]any{"name": "New member key", "teamIds": []string{f.public}, "expiresAt": live.Format(time.RFC3339)}), list(member))},
		{"create-member-duplicate", keys, run(create(member, map[string]any{"name": "member key"}))},
		{"create-member-admin-scope", keys, run(create(member, map[string]any{"name": "Wants admin", "scopes": []string{"admin"}}))},
		{"create-member-unknown-team", keys, run(create(member, map[string]any{"name": "Unknown team", "teamIds": []string{"sw450-missing-team"}}))},
		{"create-member-private-team", keys, run(create(member, map[string]any{"name": "Private team", "teamIds": []string{f.private}}))},
		{"create-member-empty-scopes", keys, run(create(member, map[string]any{"name": "Deny all", "scopes": []string{}}), list(member))},
		{"create-guest", keys, run(create(guest, map[string]any{"name": "Guest key"}))},
		{"create-member-admins-only", steps(keys, permission("admins")), run(create(member, map[string]any{"name": "Refused"}), create(admin, map[string]any{"name": "Allowed"}))},
		{"create-invalid", keys, run(create(owner, map[string]any{"name": " "}), create(owner, map[string]any{"name": "Bad", "teamRestriction": "some"}), create(owner, map[string]any{"name": "Bad", "expiresAt": "2001-01-01T00:00:00Z"}))},
		{"create-many", steps(keys, many), run(create(admin, map[string]any{"name": "One more"}), list(admin))},
		{"update-rename", keys, run(update(member, "sw450-key-member", map[string]any{"name": "Renamed member key"}), list(member))},
		{"update-rename-duplicate", keys, run(update(member, "sw450-key-member", map[string]any{"name": "MEMBER SELECTED"}))},
		{"update-unchanged", keys, run(update(member, "sw450-key-member", map[string]any{"name": "Member key"}))},
		{"update-scopes-teams", keys, run(update(member, "sw450-key-member", map[string]any{"scopes": []string{"read"}, "teamIds": []string{f.public}}), update(member, "sw450-key-member-selected", map[string]any{"teamRestriction": nil}), list(member))},
		{"update-other-member", keys, run(update(member, "sw450-key-admin", map[string]any{"name": "Taken over"}), update(guest, "sw450-key-member", map[string]any{"name": "Guest edit"}))},
		{"update-admin-other", keys, run(update(admin, "sw450-key-member", map[string]any{"name": "Admin renamed", "expiresAt": nil}), list(member))},
		{"update-revoked-missing", keys, run(update(owner, "sw450-key-owner-revoked", map[string]any{"name": "Revived"}), update(owner, "sw450-missing", map[string]any{"name": "Missing"}))},
		{"rotate", keys, run(append([]step{rotate(member, "sw450-key-member"), rotate(member, "sw450-key-admin"), rotate(owner, "sw450-key-owner-revoked"), list(member)}, probeTokens()...)...)},
		{"revoke-own", keys, run(with(probeTokens(), revoke(owner, "sw450-key-owner"), list(owner))...)},
		{"revoke-member-own", keys, run(with(probeTokens(), revoke(member, "sw450-key-member"), list(member))...)},
		{"revoke-member-other-forbidden", keys, run(with(probeTokens(), revoke(member, "sw450-key-owner"), revoke(guest, "sw450-key-member"), list(owner))...)},
		{"revoke-admin-other", keys, run(with(probeTokens(), revoke(admin, "sw450-key-member"), revoke(admin, "sw450-key-owner-revoked"), revoke(admin, "sw450-missing"), list(member))...)},
		{"revoke-many", steps(keys, many), run(revoke(member, "sw450-key-bulk-001"), revoke(admin, "sw450-key-bulk-002"), list(member))},
		{"applications-admin", steps(keys, apps), run(applications(owner), applications(admin), applications(member))},
		{"mcp-revoke-owner", steps(keys, apps), run(with(probeTokens(), revokeMCP(owner), applications(owner), list(member))...)},
		{"mcp-revoke-admin", steps(keys, apps), run(with(probeTokens(), revokeMCP(admin), applications(admin))...)},
		{"mcp-revoke-member-own", steps(keys, apps), run(with(probeTokens(), revokeMCP(member), applications(owner))...)},
		{"mcp-revoke-guest-own", steps(keys, apps), run(with(probeTokens(), revokeMCP(guest), applications(owner))...)},
		{"mcp-revoke-none", keys, run(revokeMCP(owner), revokeMCP(member))},
		{"mcp-revoke-twice", steps(keys, apps), run(revokeMCP(member), revokeMCP(member), revokeMCP(owner), revokeMCP(owner), applications(owner))},
		{"mcp-revoke-unknown-workspace", steps(keys, apps), run(other("revoke-mcp-unknown", http.MethodDelete, "/api/oauth/authorized-applications/mcp"))},
		{"mcp-revoke-one", steps(keys, apps), run(with(probeTokens(), revokeGrant(member, "sw450-grant-mcp-member"), revokeGrant(member, "sw450-grant-mcp-admin"), revokeGrant(admin, "sw450-grant-mcp-admin"), applications(owner))...)},
		{"oauth-applications", steps(keys, apps), run(do("oauth-apps owner", http.MethodGet, "/api/oauth-applications", "", owner), do("oauth-app owner", http.MethodGet, "/api/oauth-applications/sw450-app-plain", "", owner), do("oauth-app admin", http.MethodGet, "/api/oauth-applications/sw450-app-plain", "", admin), do("oauth-app missing", http.MethodGet, "/api/oauth-applications/sw450-missing", "", owner), do("oauth-apps member", http.MethodGet, "/api/oauth-applications", "", member), do("oauth-app guest", http.MethodGet, "/api/oauth-applications/sw450-app-plain", "", guest), bearer("oauth-apps admin key", "/api/oauth-applications", "sw450-secret-owner-scoped"))},
		{"oauth-applications-empty", keys, run(do("oauth-apps owner", http.MethodGet, "/api/oauth-applications", "", owner))},
		{"oauth-application-create", steps(keys, apps), run(application("create owner", http.MethodPost, "/api/oauth-applications", `{"name":"SW450 Created","description":"Made by SW450","redirectUris":["https://example.test/sw450/cb"],"githubUsername":"sw450-created"}`, owner, ""), do("oauth-apps owner", http.MethodGet, "/api/oauth-applications", "", owner))},
		{"oauth-application-create-key", steps(keys, apps), run(application("create admin key", http.MethodPost, "/api/oauth-applications", `{"name":"SW450 Key Created"}`, domain.User{}, "sw450-secret-owner-scoped"), application("create member key", http.MethodPost, "/api/oauth-applications", `{"name":"SW450 Member Key"}`, domain.User{}, memberSecret))},
		{"oauth-application-create-refused", steps(keys, apps), run(application("create member", http.MethodPost, "/api/oauth-applications", `{"name":"SW450 Member"}`, member, ""), application("create blank", http.MethodPost, "/api/oauth-applications", `{"name":"  "}`, owner, ""), application("create github taken", http.MethodPost, "/api/oauth-applications", `{"name":"SW450 Taken","githubUsername":"sw450-plain-user"}`, owner, ""))},
		{"oauth-application-delete", steps(keys, apps, installPlain), run(application("delete owner", http.MethodDelete, "/api/oauth-applications/sw450-app-plain", "", owner, ""), do("oauth-apps owner", http.MethodGet, "/api/oauth-applications", "", owner), applications(owner))},
		{"oauth-application-delete-refused", steps(keys, apps), run(application("delete member", http.MethodDelete, "/api/oauth-applications/sw450-app-plain", "", member, ""), application("delete missing", http.MethodDelete, "/api/oauth-applications/sw450-missing", "", owner, ""))},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			current = t
			outcomes := f.sw450Differential(t, test.name, test.setup, test.call)
			t.Logf("legacy result %s", outcomes[0].Result)
		})
	}
}
