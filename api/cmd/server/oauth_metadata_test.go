package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

// SW294: the OAuth/application metadata transaction (application install,
// deactivation and uninstall, credential issue and revocation, refresh-family
// revocation, grants, application policies and the built-in agent) on the
// legacy store, a warm bounded view and a bounded view loaded from SQL. The
// callbacks' results, every metadata row and its order, the root, the
// membership/installation/user/lookup/departure tables, domain events,
// webhook and realtime publications, the installed records they change and a
// reload must agree byte for byte except generated IDs and clocks; bounded,
// the installed image must also equal the reload exactly.

var scaleP21SW294Fields = []string{"users", "apiKeys", "oauthAuthorizations", "settings", "webhooks", "oauthApplications"}

// scaleP21SW294ExactFields are compared between a bounded installed image and
// a reload; the legacy snapshot does not derive a new user's settings.
var scaleP21SW294ExactFields = append(slices.Clone(scaleP21SW294Fields), "userSettings", "notificationPreferences", "members", "teamMembers")

type scaleP21SW294Outcome struct {
	Result              string
	State               scaleP21TeamCreateState
	Extra               map[string][][]any
	Webhooks, Realtime  []json.RawMessage
	Installed, Reloaded json.RawMessage
}

// scaleP21SW294Image reads the fields from repo's installed image (the legacy
// snapshot or the bounded installed logical source), each decoded and
// re-encoded so an empty collection's spelling is not state.
func scaleP21SW294Image(t *testing.T, repo *store.SQLiteStore, workspace string, names ...string) map[string]json.RawMessage {
	t.Helper()
	result := map[string]json.RawMessage{}
	if !repo.BoundedMetadataEnabled() {
		full, ok := repo.BootstrapFor(workspace)
		if !ok {
			t.Fatal("workspace missing")
		}
		raw, err := json.Marshal(full)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		for _, name := range names {
			result[name] = fields[name]
		}
	} else {
		read, found, err := repo.BeginInstalledMetadataRead(t.Context(), workspace)
		if err != nil || !found {
			t.Fatal("installed read", err)
		}
		for _, name := range names {
			var raw strings.Builder
			if err := read.StreamFieldJSON(t.Context(), name, &raw); err != nil {
				t.Fatal(err)
			}
			result[name] = json.RawMessage(raw.String())
		}
		read.Close()
	}
	for name, value := range result {
		var decoded any
		if len(value) != 0 {
			if err := json.Unmarshal(value, &decoded); err != nil {
				t.Fatal(name, err)
			}
		}
		if items, ok := decoded.([]any); ok && len(items) == 0 || decoded == nil {
			result[name] = json.RawMessage("null")
			continue
		}
		if items, ok := decoded.(map[string]any); ok && len(items) == 0 {
			result[name] = json.RawMessage("null")
			continue
		}
		result[name], _ = json.Marshal(decoded)
	}
	return result
}

// scaleP21SW294Delta lists, per field, the installed entries a call added or
// changed, sorted (the legacy snapshot appends where a load orders).
func scaleP21SW294Delta(t *testing.T, before, after map[string]json.RawMessage) json.RawMessage {
	t.Helper()
	entries := func(name string, raw json.RawMessage) map[string]string {
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			t.Fatal(err)
		}
		result := map[string]string{}
		add := func(key string, item any) {
			// The legacy snapshot keeps a written key or hook as the writer
			// handed it; a load (and the bounded image) fills its team
			// restriction (normalize).
			if object, ok := item.(map[string]any); ok && (name == "apiKeys" || name == "webhooks") {
				if restriction, _ := object["teamRestriction"].(string); restriction == "" {
					if teams, _ := object["teamIds"].([]any); len(teams) > 0 {
						object["teamRestriction"] = "selected"
					} else {
						object["teamRestriction"] = "all"
					}
				}
			}
			encoded, _ := json.Marshal(item)
			result[key+"\t"+string(encoded)] = key
		}
		switch value := value.(type) {
		case []any:
			for index, item := range value {
				key := fmt.Sprint("#", index)
				if object, ok := item.(map[string]any); ok {
					if id, ok := object["id"].(string); ok {
						key = id
					}
				}
				add(key, item)
			}
		case map[string]any:
			for key, item := range value {
				add(key, item)
			}
		}
		return result
	}
	result := map[string][]string{}
	for _, name := range scaleP21SW294Fields {
		old := entries(name, before[name])
		for entry := range entries(name, after[name]) {
			if _, found := old[entry]; !found {
				result[name] = append(result[name], entry)
			}
		}
		slices.Sort(result[name])
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func (f scaleP21TeamCreateFixture) sw294Extra(t *testing.T) map[string][][]any {
	t.Helper()
	workspace, workspaceID := f.data.Workspace.URLKey, f.data.Workspace.ID
	return map[string][][]any{
		"lookup":     f.rows(t, `SELECT key_id,secret_hash FROM api_key_lookup WHERE workspace_key=? ORDER BY key_id`, workspace),
		"departures": f.rows(t, `SELECT user_id,role,joined_at,left_at FROM workspace_departures WHERE workspace_id=? ORDER BY user_id`, workspaceID),
		"deliveries": f.rows(t, `SELECT webhook_id,secret_ref,kind,event_id,event_type,action,url,payload,status,attempt,max_attempts,redelivery_of FROM webhook_deliveries WHERE workspace_key=? ORDER BY webhook_id,event_type,action,event_id`, workspace),
		"appUsers":   f.rows(t, `SELECT id,name,display_name,avatar_url,active FROM auth_users WHERE id IN (SELECT user_id FROM workspace_memberships WHERE workspace_id=? AND role='app') ORDER BY id`, workspaceID),
	}
}

// sw294Prepare restores the fixture (team-create prepare: team-move seed,
// installations and their memberships, audit rows), clears the auxiliary rows
// earlier runs wrote and applies the case's setup through the legacy store.
func (f scaleP21TeamCreateFixture) sw294Prepare(setup func(*testing.T)) func(*testing.T) {
	return f.prepare(func(t *testing.T) {
		workspace, workspaceID := f.data.Workspace.URLKey, f.data.Workspace.ID
		f.exec(t, `DELETE FROM api_key_lookup WHERE workspace_key=? AND key_id LIKE ?`, workspace, "sw294-%")
		f.exec(t, `DELETE FROM workspace_departures WHERE workspace_id=? AND user_id LIKE ?`, workspaceID, "app_%")
		f.exec(t, `DELETE FROM webhook_deliveries WHERE workspace_key=?`, workspace)
		if setup != nil {
			setup(t)
		}
	})
}

func (f scaleP21TeamCreateFixture) sw294Run(t *testing.T, source string, call func(*server) string) (scaleP21SW294Outcome, time.Time) {
	t.Helper()
	started := time.Now().UTC()
	service := f.service(t, source)
	workspace := f.data.Workspace.URLKey
	got := scaleP21SW294Outcome{}
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
	service.store.SetWebhookSink(nil)
	service.store.SetRealtimeSink(nil)
	got.State = f.state(t)
	var events [][]any
	for _, event := range got.State.Events {
		if !prior[event[0]] {
			events = append(events, event[1:])
		}
	}
	// Concurrent callers commit in any order.
	slices.SortFunc(events, func(a, b []any) int { return strings.Compare(fmt.Sprint(a...), fmt.Sprint(b...)) })
	slices.SortFunc(got.Webhooks, func(a, b json.RawMessage) int { return strings.Compare(string(a), string(b)) })
	slices.SortFunc(got.Realtime, func(a, b json.RawMessage) int { return strings.Compare(string(a), string(b)) })
	got.State.Events = events
	got.Extra = f.sw294Extra(t)
	if service.store.BoundedMetadataEnabled() && (service.store.WorkspaceMetadataDecodes() != decodes || service.store.MetadataWorkUsedForTesting() != 0) {
		t.Fatalf("%s: bounded OAuth metadata write decoded full metadata (%d) or retained work (%d)", source,
			service.store.WorkspaceMetadataDecodes(), service.store.MetadataWorkUsedForTesting())
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
		// The installed image is exactly what a reload reads, in its order
		// (a load derives settings it never stored with its own clock).
		for _, name := range scaleP21SW294ExactFields {
			want, have := scaleP21TriageNormalize(t, fresh[name], started), scaleP21TriageNormalize(t, installed[name], started)
			if want != have {
				t.Fatalf("%s: installed %s is not the reloaded one: %s", source, name, scaleP21M2Diff(want, have))
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
	return got, started
}

func (f scaleP21TeamCreateFixture) sw294Differential(t *testing.T, name string, setup func(*testing.T), call func(*server) string) []scaleP21SW294Outcome {
	t.Helper()
	f.scaleP21TriageWriterFixture.prepare = f.sw294Prepare(setup)
	defer func() { f.scaleP21TriageWriterFixture.prepare = nil }()
	var normalized []string
	var outcomes []scaleP21SW294Outcome
	for _, source := range []string{"legacy", "warm", "reloaded"} {
		got, started := f.sw294Run(t, source, call)
		outcomes = append(outcomes, got)
		normalized = append(normalized, scaleP21TriageNormalize(t, got, started))
		if len(normalized) > 1 && normalized[len(normalized)-1] != normalized[0] {
			t.Fatalf("%s: %s changed: %s", name, source, scaleP21M2Diff(normalized[0], normalized[len(normalized)-1]))
		}
	}
	return outcomes
}

func scaleP21SW294Result(values ...any) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		if err, ok := value.(error); ok || value == nil {
			kind := "ok"
			for _, known := range []error{sql.ErrNoRows, store.ErrAuthForbidden, store.ErrBuiltinApplication, store.ErrOAuthTokenLimit, store.ErrMetadataFieldInline, store.ErrMetadataWorkBudget, store.ErrIssueQuery} {
				if err != nil && errors.Is(err, known) {
					kind = known.Error()
				}
			}
			if err != nil && kind == "ok" {
				kind = "error: " + err.Error()
			}
			parts = append(parts, kind)
			continue
		}
		raw, _ := json.Marshal(value)
		parts = append(parts, string(raw))
	}
	return strings.Join(parts, " | ")
}

// scaleP21SW294Hash is the installation/app-user hash of writeApplicationInstallationTx.
func scaleP21SW294Hash(workspace, client string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(workspace+":"+client)))[:32]
}

type scaleP21SW294Case struct {
	name  string
	setup func(*testing.T)
	call  func(*server) string
	check func(*testing.T, scaleP21SW294Outcome)
	// interleaved: concurrent calls decide which intermediate writes,
	// events and deliveries happen; only the converged state is compared.
	interleaved bool
}

func TestScaleP21SW294OAuthMetadataDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21TeamCreateSetup(t)
	workspace := f.data.Workspace.URLKey
	owner, admin, member := f.data.Viewer, f.principals[1].user, f.principals[2].user
	at := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	later := at.Add(48 * time.Hour)
	const client, secret = "sw294-client-a", "sw294-client-secret"
	appUser, installation := "app_"+scaleP21SW294Hash(workspace, client), "installation_"+scaleP21SW294Hash(workspace, client)
	builtin := "installation_" + scaleP21SW294Hash(workspace, store.BuiltinApplicationClientID)
	install := func(name string, active bool, scopes ...string) domain.ApplicationInstallation {
		if len(scopes) == 0 {
			scopes = []string{"read", "write"}
		}
		return domain.ApplicationInstallation{ClientID: client, Name: name, AvatarURL: "https://example.test/sw294.png", InstalledBy: owner.ID, Scopes: scopes, TeamIDs: []string{f.public}, Active: active}
	}
	seedApp := func(t *testing.T) {
		f.mutate(t, "scale_p21_sw294.app", func(data *domain.Bootstrap) {
			data.OAuthApplications = append(data.OAuthApplications, domain.OAuthApplication{
				ID: "sw294-app-a", ClientID: client, ClientSecret: secret, Name: "SW294 App", Scopes: []string{"read", "write", "app:assignable"},
				SupportsClientCredentials: true, CreatorID: owner.ID, RedirectURIs: []string{"https://example.test/callback"}, CreatedAt: at, UpdatedAt: at,
			})
		})
	}
	installed := func(t *testing.T) {
		seedApp(t)
		if _, err := f.repository.InstallApplication(t.Context(), workspace, install("SW294 App", true), "sw294-webhook-secret"); err != nil {
			t.Fatal(err)
		}
	}
	// Credentials around the app user: its own keys and grants (one via
	// CreatorID only), a key of another subject the app user created, other
	// members' keys and grants, and revoked ones.
	credentials := func(t *testing.T) {
		f.mutate(t, "scale_p21_sw294.credentials", func(data *domain.Bootstrap) {
			revoked := at.Add(time.Hour)
			data.APIKeys = append(data.APIKeys,
				domain.APIKey{ID: "sw294-key-subject", Name: "App token", SubjectUserID: appUser, CreatorID: owner.ID, OAuthClientID: client, Actor: "app", GrantType: "client_credentials", Scopes: []string{"read"}, SecretHash: "sw294-hash-subject", CreatedAt: at},
				domain.APIKey{ID: "sw294-key-creator", Name: "Creator token", CreatorID: appUser, Scopes: []string{"read"}, SecretHash: "sw294-hash-creator", CreatedAt: at.Add(time.Minute)},
				domain.APIKey{ID: "sw294-key-other-subject", Name: "Other subject", SubjectUserID: member.ID, CreatorID: appUser, Scopes: []string{"read"}, SecretHash: "sw294-hash-other", CreatedAt: at.Add(2 * time.Minute)},
				domain.APIKey{ID: "sw294-key-member", Name: "Member token", SubjectUserID: member.ID, CreatorID: member.ID, OAuthClientID: client, Scopes: []string{"read"}, SecretHash: "sw294-hash-member", CreatedAt: at.Add(3 * time.Minute)},
				domain.APIKey{ID: "sw294-key-revoked", Name: "Revoked", SubjectUserID: appUser, CreatorID: owner.ID, OAuthClientID: client, Scopes: []string{"read"}, SecretHash: "sw294-hash-revoked", CreatedAt: at, RevokedAt: &revoked},
				domain.APIKey{ID: "sw294-key-family-a", Name: "Family A", SubjectUserID: member.ID, CreatorID: member.ID, OAuthClientID: client, Scopes: []string{"read"}, SecretHash: "sw294-hash-family-a", CreatedAt: at, RefreshFamilyID: "sw294-family"},
				domain.APIKey{ID: "sw294-key-family-b", Name: "Family B", SubjectUserID: member.ID, CreatorID: member.ID, OAuthClientID: client, Scopes: []string{"read"}, SecretHash: "sw294-hash-family-b", CreatedAt: at.Add(time.Second), RefreshFamilyID: "sw294-family", AuthorizationID: "sw294-grant-member"},
				domain.APIKey{ID: "sw294-key-family-gone", Name: "Family revoked", SubjectUserID: member.ID, CreatorID: member.ID, OAuthClientID: client, Scopes: []string{"read"}, SecretHash: "sw294-hash-family-gone", CreatedAt: at, RefreshFamilyID: "sw294-family", RevokedAt: &revoked},
				domain.APIKey{ID: "sw294-key-family-other", Name: "Other family", SubjectUserID: member.ID, CreatorID: member.ID, OAuthClientID: client, Scopes: []string{"read"}, SecretHash: "sw294-hash-family-other", CreatedAt: at, RefreshFamilyID: "sw294-other-family"},
			)
			data.OAuthAuthorizations = append(data.OAuthAuthorizations,
				domain.OAuthAuthorization{ID: "sw294-grant-app", Actor: "app", ClientID: client, UserID: appUser, Scopes: []string{"read"}, CreatedAt: at},
				domain.OAuthAuthorization{ID: "sw294-grant-member", ClientID: client, UserID: member.ID, Scopes: []string{"read"}, CreatedAt: at},
				domain.OAuthAuthorization{ID: "sw294-grant-other-client", ClientID: "sw294-client-other", UserID: member.ID, Scopes: []string{"read"}, CreatedAt: at},
				domain.OAuthAuthorization{ID: "sw294-grant-revoked", ClientID: client, UserID: admin.ID, Scopes: []string{"read"}, CreatedAt: at, RevokedAt: &revoked},
			)
		})
	}
	ensure := func(enabled ...bool) func(*testing.T) {
		return func(t *testing.T) {
			for _, value := range enabled {
				if _, err := f.repository.EnsureBuiltinApplication(t.Context(), workspace, value); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	var refreshToken string
	issueKey := func(id string) domain.APIKey {
		return domain.APIKey{ID: id, Name: "Issued " + id, Prefix: "lin_oauth", SecretHash: id + "-hash", GrantType: "client_credentials", CreatedAt: at.Add(time.Hour)}
	}
	grant := func(actor, user string, scopes ...string) domain.OAuthAuthorizationCode {
		return domain.OAuthAuthorizationCode{Actor: actor, InstallerID: owner.ID, ClientID: client, WorkspaceKey: workspace, UserID: user, RedirectURI: "https://example.test/callback", Scopes: scopes, TeamIDs: []string{f.public}, ExpiresAt: later, Realtime: true}
	}
	policy := func(p *domain.Bootstrap) error {
		if p.Settings == nil {
			p.Settings = map[string]any{}
		}
		p.Settings["applicationPolicies"] = map[string]any{"requests": []any{map[string]any{"clientId": client, "status": "pending"}}}
		return nil
	}
	cases := []scaleP21SW294Case{
		{name: "install-new", setup: seedApp, call: func(s *server) string {
			return scaleP21SW294Result(s.store.InstallApplication(t.Context(), workspace, install("SW294 App", true), "sw294-webhook-secret"))
		}},
		{name: "install-again-renamed", setup: installed, call: func(s *server) string {
			return scaleP21SW294Result(s.store.InstallApplication(t.Context(), workspace, install("Renamed app", true, "read", "write", "app:assignable"), ""))
		}},
		{name: "install-unknown-team", setup: seedApp, call: func(s *server) string {
			bad := install("SW294 App", true)
			bad.TeamIDs = []string{"sw294-missing-team"}
			return scaleP21SW294Result(s.store.InstallApplication(t.Context(), workspace, bad, ""))
		}},
		{name: "install-not-admin", setup: seedApp, call: func(s *server) string {
			bad := install("SW294 App", true)
			bad.InstalledBy = member.ID
			return scaleP21SW294Result(s.store.InstallApplication(t.Context(), workspace, bad, ""))
		}},
		{name: "deactivate-revokes-credentials", setup: func(t *testing.T) { installed(t); credentials(t) }, call: func(s *server) string {
			return scaleP21SW294Result(s.store.InstallApplication(t.Context(), workspace, install("SW294 App", false), ""))
		}, check: func(t *testing.T, outcome scaleP21SW294Outcome) {
			for _, key := range []string{"sw294-key-subject", "sw294-key-creator", "sw294-grant-app"} {
				if !strings.Contains(string(outcome.Installed), key) {
					t.Fatalf("deactivation did not revoke %s: %s", key, outcome.Installed)
				}
			}
			for _, key := range []string{"sw294-key-other-subject", "sw294-key-member", "sw294-grant-member"} {
				if strings.Contains(string(outcome.Installed), key) {
					t.Fatalf("deactivation revoked %s", key)
				}
			}
		}},
		{name: "uninstall", setup: func(t *testing.T) { installed(t); credentials(t) }, call: func(s *server) string {
			return scaleP21SW294Result(s.store.UninstallApplication(t.Context(), workspace, installation))
		}, check: func(t *testing.T, outcome scaleP21SW294Outcome) {
			for _, key := range []string{"sw294-key-subject", "sw294-grant-app", "sw294-grant-member", appUser} {
				if !strings.Contains(string(outcome.Installed), key) {
					t.Fatalf("uninstall did not change %s: %s", key, outcome.Installed)
				}
			}
		}},
		{name: "uninstall-missing", setup: installed, call: func(s *server) string {
			return scaleP21SW294Result(s.store.UninstallApplication(t.Context(), workspace, "installation_sw294_missing"))
		}},
		{name: "uninstall-builtin", setup: ensure(true), call: func(s *server) string {
			return scaleP21SW294Result(s.store.UninstallApplication(t.Context(), workspace, builtin))
		}},
		{name: "issue-client-credentials", setup: func(t *testing.T) { installed(t); credentials(t) }, call: func(s *server) string {
			return scaleP21SW294Result(s.store.IssueApplicationCredential(t.Context(), workspace, client, secret, "", "app", []string{"read", "write"}, issueKey("sw294-issued-a"), nil))
		}},
		{name: "issue-client-credentials-new-installation", setup: seedApp, call: func(s *server) string {
			return scaleP21SW294Result(s.store.IssueApplicationCredential(t.Context(), workspace, client, secret, "", "app", []string{"read"}, issueKey("sw294-issued-b"), nil))
		}},
		{name: "issue-wrong-secret", setup: installed, call: func(s *server) string {
			return scaleP21SW294Result(s.store.IssueApplicationCredential(t.Context(), workspace, client, "wrong", "", "app", nil, issueKey("sw294-issued-c"), nil))
		}},
		{name: "issue-user-token", setup: func(t *testing.T) { seedApp(t); credentials(t) }, call: func(s *server) string {
			key := issueKey("sw294-issued-user")
			key.GrantType = "authorization_code"
			return scaleP21SW294Result(s.store.IssueApplicationCredential(t.Context(), workspace, client, "", owner.ID, "user", []string{"read"}, key, nil))
		}},
		{name: "revoke-access-token", setup: func(t *testing.T) { installed(t); credentials(t) }, call: func(s *server) string {
			return scaleP21SW294Result(s.store.RevokeOAuthAccessToken(t.Context(), "sw294-hash-subject"))
		}},
		{name: "revoke-access-token-unknown", setup: func(t *testing.T) { installed(t); credentials(t) }, call: func(s *server) string {
			return scaleP21SW294Result(s.store.RevokeOAuthAccessToken(t.Context(), "sw294-hash-unknown"))
		}},
		{name: "revoke-refresh-family", setup: func(t *testing.T) {
			seedApp(t)
			credentials(t)
			refreshToken = idgen.New("sw294-refresh-")
			if err := f.repository.CreateOAuthRefreshToken(t.Context(), refreshToken, domain.OAuthRefreshGrant{ClientID: client, WorkspaceKey: workspace, UserID: member.ID, Scopes: []string{"read"}, ExpiresAt: later, FamilyID: "sw294-family"}); err != nil {
				t.Fatal(err)
			}
		}, call: func(s *server) string {
			return scaleP21SW294Result(s.store.RevokeOAuthRefreshToken(t.Context(), refreshToken))
		}, check: func(t *testing.T, outcome scaleP21SW294Outcome) {
			if !strings.Contains(string(outcome.Installed), "sw294-key-family-a") || !strings.Contains(string(outcome.Installed), "sw294-key-family-b") || strings.Contains(string(outcome.Installed), "sw294-key-family-other") {
				t.Fatalf("family revocation changed %s", outcome.Installed)
			}
		}},
		{name: "grant-user-created", setup: seedApp, call: func(s *server) string {
			return scaleP21SW294Result(s.store.CreateOAuthAuthorizationGrant(t.Context(), idgen.New("sw294-code-"), grant("user", member.ID, "read"), domain.OAuthAuthorization{ID: "sw294-grant-new", CreatedAt: at.Add(3 * time.Hour)}, nil))
		}},
		{name: "grant-user-reused", setup: func(t *testing.T) {
			seedApp(t)
			if _, err := f.repository.CreateOAuthAuthorizationGrant(t.Context(), idgen.New("sw294-code-"), grant("user", member.ID, "read"), domain.OAuthAuthorization{ID: "sw294-grant-first", CreatedAt: at.Add(3 * time.Hour)}, nil); err != nil {
				t.Fatal(err)
			}
		}, call: func(s *server) string {
			return scaleP21SW294Result(s.store.CreateOAuthAuthorizationGrant(t.Context(), idgen.New("sw294-code-"), grant("user", member.ID, "read"), domain.OAuthAuthorization{ID: "sw294-grant-second", CreatedAt: at.Add(4 * time.Hour)}, nil))
		}},
		{name: "grant-app-installs", setup: seedApp, call: func(s *server) string {
			return scaleP21SW294Result(s.store.CreateOAuthAuthorizationGrant(t.Context(), idgen.New("sw294-code-"), grant("app", "", "read", "write"), domain.OAuthAuthorization{ID: "sw294-grant-app-new", CreatedAt: at.Add(5 * time.Hour)}, nil))
		}},
		{name: "grant-app-update-access", setup: installed, call: func(s *server) string {
			return scaleP21SW294Result(s.store.CreateOAuthAuthorizationGrant(t.Context(), idgen.New("sw294-code-"), grant("app", "", "read", "app:assignable"), domain.OAuthAuthorization{ID: "sw294-grant-app-update", CreatedAt: at.Add(5 * time.Hour)}, nil))
		}},
		{name: "grant-refused-by-policy", setup: seedApp, call: func(s *server) string {
			return scaleP21SW294Result(s.store.CreateOAuthAuthorizationGrant(t.Context(), idgen.New("sw294-code-"), grant("user", member.ID, "read"), domain.OAuthAuthorization{ID: "sw294-grant-refused", CreatedAt: at}, func(*domain.Bootstrap, string, []string) bool { return false }))
		}},
		{name: "request-approval", setup: seedApp, call: func(s *server) string {
			return scaleP21SW294Result(s.store.RequestOAuthApplicationApproval(t.Context(), workspace, client, policy))
		}},
		{name: "request-approval-replaces", setup: func(t *testing.T) {
			seedApp(t)
			if err := f.repository.RequestOAuthApplicationApproval(t.Context(), workspace, client, func(p *domain.Bootstrap) error {
				p.Settings["applicationPolicies"] = map[string]any{"previous": true}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}, call: func(s *server) string {
			return scaleP21SW294Result(s.store.RequestOAuthApplicationApproval(t.Context(), workspace, client, policy))
		}},
		{name: "revoke-authorization-own", setup: func(t *testing.T) { seedApp(t); credentials(t) }, call: func(s *server) string {
			return scaleP21SW294Result(s.store.RevokeOAuthAuthorizationRecords(t.Context(), workspace, "sw294-grant-member", member.ID))
		}},
		{name: "revoke-authorization-admin", setup: func(t *testing.T) { seedApp(t); credentials(t) }, call: func(s *server) string {
			return scaleP21SW294Result(s.store.RevokeOAuthAuthorizationRecords(t.Context(), workspace, "sw294-grant-member", owner.ID))
		}},
		{name: "revoke-authorization-forbidden", setup: func(t *testing.T) { seedApp(t); credentials(t) }, call: func(s *server) string {
			return scaleP21SW294Result(s.store.RevokeOAuthAuthorizationRecords(t.Context(), workspace, "sw294-grant-member", f.principals[3].user.ID))
		}},
		{name: "revoke-authorization-missing", setup: func(t *testing.T) { seedApp(t); credentials(t) }, call: func(s *server) string {
			return scaleP21SW294Result(s.store.RevokeOAuthAuthorizationRecords(t.Context(), workspace, "sw294-grant-missing", owner.ID))
		}},
		{name: "ensure-builtin-new", call: func(s *server) string {
			return scaleP21SW294Result(s.store.EnsureBuiltinApplication(t.Context(), workspace, true))
		}},
		{name: "ensure-builtin-noop", setup: ensure(true), call: func(s *server) string {
			return scaleP21SW294Result(s.store.EnsureBuiltinApplication(t.Context(), workspace, true))
		}},
		{name: "ensure-builtin-suspend", setup: ensure(true), call: func(s *server) string {
			return scaleP21SW294Result(s.store.EnsureBuiltinApplication(t.Context(), workspace, false))
		}},
		{name: "ensure-builtin-resume", setup: ensure(true, false), call: func(s *server) string {
			return scaleP21SW294Result(s.store.EnsureBuiltinApplication(t.Context(), workspace, true))
		}},
		{name: "ensure-builtin-disabled-absent", call: func(s *server) string {
			return scaleP21SW294Result(s.store.EnsureBuiltinApplication(t.Context(), workspace, false))
		}},
		{name: "cancelled", setup: seedApp, call: func(s *server) string {
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			return scaleP21SW294Result(s.store.InstallApplication(ctx, workspace, install("SW294 App", true), ""))
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			outcomes := f.sw294Differential(t, test.name, test.setup, test.call)
			t.Logf("legacy result %s", outcomes[0].Result)
			if test.check != nil {
				test.check(t, outcomes[0])
			}
		})
	}
}

// Concurrent OAuth transactions of one workspace on one store: installs of
// several applications, the built-in agent, a client-credentials token, a
// token revocation and an approval request. Each commits whole; the final
// rows, events and installed records agree whatever order they ran in.
func TestScaleP21SW294OAuthMetadataConcurrent(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21TeamCreateSetup(t)
	workspace := f.data.Workspace.URLKey
	owner := f.data.Viewer
	at := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	setup := func(t *testing.T) {
		f.mutate(t, "scale_p21_sw294.concurrent", func(data *domain.Bootstrap) {
			data.OAuthApplications = append(data.OAuthApplications, domain.OAuthApplication{
				ID: "sw294-app-c", ClientID: "sw294-client-c", ClientSecret: "sw294-secret-c", Name: "SW294 C", Scopes: []string{"read", "write"},
				SupportsClientCredentials: true, CreatorID: owner.ID, RedirectURIs: []string{}, CreatedAt: at, UpdatedAt: at,
			})
			data.APIKeys = append(data.APIKeys, domain.APIKey{ID: "sw294-key-concurrent", Name: "Concurrent", SubjectUserID: owner.ID, CreatorID: owner.ID, Scopes: []string{"read"}, SecretHash: "sw294-hash-concurrent", CreatedAt: at})
		})
	}
	f.sw294Differential(t, "concurrent", setup, func(s *server) string {
		var group sync.WaitGroup
		results := make([]string, 8)
		run := func(index int, call func() string) {
			group.Add(1)
			go func() {
				defer group.Done()
				results[index] = call()
			}()
		}
		for index, client := range []string{"sw294-client-w", "sw294-client-x", "sw294-client-y", "sw294-client-z"} {
			run(index, func() string {
				return scaleP21SW294Result(s.store.InstallApplication(t.Context(), workspace, domain.ApplicationInstallation{ClientID: client, Name: "Concurrent " + client, InstalledBy: owner.ID, Scopes: []string{"read"}, TeamIDs: []string{f.public}, Active: true}, ""))
			})
		}
		run(4, func() string {
			return scaleP21SW294Result(s.store.EnsureBuiltinApplication(t.Context(), workspace, true))
		})
		run(5, func() string {
			key := domain.APIKey{ID: "sw294-issued-concurrent", Name: "Issued", Prefix: "lin_oauth", SecretHash: "sw294-issued-concurrent-hash", GrantType: "client_credentials", CreatedAt: at.Add(time.Hour)}
			return scaleP21SW294Result(s.store.IssueApplicationCredential(t.Context(), workspace, "sw294-client-c", "sw294-secret-c", "", "app", []string{"read"}, key, nil))
		})
		run(6, func() string {
			return scaleP21SW294Result(s.store.RevokeOAuthAccessToken(t.Context(), "sw294-hash-concurrent"))
		})
		run(7, func() string {
			return scaleP21SW294Result(s.store.RequestOAuthApplicationApproval(t.Context(), workspace, "sw294-client-c", func(p *domain.Bootstrap) error {
				p.Settings["applicationPolicies"] = map[string]any{"concurrent": true}
				return nil
			}))
		})
		group.Wait()
		return strings.Join(results, "\n")
	})
}

// A bounded transaction whose selected records exceed the metadata work
// budget refuses before writing (ErrMetadataWorkBudget): no row, event or
// installed record changes and no work is retained. Legacy holds the whole
// workspace and revokes; the refusal is the bounded store's budget contract.
func TestScaleP21SW294OAuthMetadataBudget(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21TeamCreateSetup(t)
	workspace := f.data.Workspace.URLKey
	owner := f.data.Viewer
	const client = "sw294-client-budget"
	appUser := "app_" + scaleP21SW294Hash(workspace, client)
	at := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	install := func(active bool) domain.ApplicationInstallation {
		return domain.ApplicationInstallation{ClientID: client, Name: "Budget app", InstalledBy: owner.ID, Scopes: []string{"read"}, TeamIDs: []string{f.public}, Active: active}
	}
	f.scaleP21TriageWriterFixture.prepare = f.sw294Prepare(func(t *testing.T) {
		if _, err := f.repository.InstallApplication(t.Context(), workspace, install(true), ""); err != nil {
			t.Fatal(err)
		}
		// Three 15 MiB revoked keys of the app user, each written by its own
		// transaction (a transaction writes at most 32 MiB). The callback
		// reads them (and leaves them revoked); handing them to it needs more
		// than the 256 MiB process work budget.
		for index := range 3 {
			f.mutate(t, "scale_p21_sw294.budget", func(data *domain.Bootstrap) {
				data.APIKeys = append(data.APIKeys, domain.APIKey{ID: fmt.Sprint("sw294-key-large-", index), Name: strings.Repeat("h", 15<<20), SubjectUserID: appUser, CreatorID: owner.ID, Scopes: []string{"read"}, SecretHash: fmt.Sprint("sw294-hash-large-", index), CreatedAt: at, RevokedAt: &at})
			})
		}
		f.mutate(t, "scale_p21_sw294.budget", func(data *domain.Bootstrap) {
			data.APIKeys = append(data.APIKeys, domain.APIKey{ID: "sw294-key-small", Name: "Small", SubjectUserID: appUser, CreatorID: owner.ID, Scopes: []string{"read"}, SecretHash: "sw294-hash-small", CreatedAt: at})
		})
	})
	defer func() { f.scaleP21TriageWriterFixture.prepare = nil }()
	for _, source := range []string{"legacy", "warm", "reloaded"} {
		service := f.service(t, source)
		before := f.state(t)
		image := scaleP21SW294Image(t, service.store, workspace, scaleP21SW294Fields...)
		published := 0
		service.store.SetWebhookSink(func(string, domain.DomainEvent) { published++ })
		_, err := service.store.InstallApplication(t.Context(), workspace, install(false), "")
		service.store.SetWebhookSink(nil)
		after := f.state(t)
		if source == "legacy" {
			if err != nil || len(after.Events) == len(before.Events) {
				t.Fatalf("legacy deactivation %v", err)
			}
			continue
		}
		if !errors.Is(err, store.ErrMetadataWorkBudget) {
			t.Fatalf("%s: over-budget selection returned %v", source, err)
		}
		if fmt.Sprint(before) != fmt.Sprint(after) || published != 0 {
			t.Fatalf("%s: refused transaction changed rows or published %d events", source, published)
		}
		if fmt.Sprint(image) != fmt.Sprint(scaleP21SW294Image(t, service.store, workspace, scaleP21SW294Fields...)) {
			t.Fatalf("%s: refused transaction changed the installed image", source)
		}
		if service.store.MetadataWorkUsedForTesting() != 0 || service.store.WorkspaceMetadataDecodes() != 0 {
			t.Fatalf("%s: refusal retained work or decoded the workspace", source)
		}
	}
}
