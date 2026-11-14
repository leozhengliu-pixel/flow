package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21SW17OAuthClientSkipsUnloadedWorkspace(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	ctx := ContextWithActor(t.Context(), f.data.Viewer)
	prefix := idgen.New("sw17-client-")
	clientID := prefix + "-duplicate"
	workspaces := []string{}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		for _, workspace := range workspaces {
			if err := f.DeleteWorkspace(cleanup, workspace); err != nil {
				t.Error(err)
			}
		}
	})
	for _, suffix := range []string{"-a", "-z"} {
		data, err := f.CreateWorkspace(ctx, "SW17 client scope", prefix+suffix, "us")
		if err != nil {
			t.Fatal(err)
		}
		workspaces = append(workspaces, data.Workspace.URLKey)
		if err := f.MutateWorkspace(WithMetadataFields(ctx, "oauthApplications"), data.Workspace.URLKey, "scale_p21_sw17.clients", "", nil, func(data *domain.Bootstrap) error {
			app := domain.OAuthApplication{
				ID: prefix + suffix + "-app", ClientID: clientID, Name: suffix, ClientSecret: "fixture-secret",
				RedirectURIs: []string{"https://example.test/callback"}, Scopes: []string{"read", "write"},
				DeveloperURL: "https://example.test/developer", LogoURL: "/uploads/fixture.png",
				SupportsClientCredentials: true, PublicEnabled: true, CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
			}
			if suffix == "-a" {
				app.DeveloperURL += strings.Repeat("/unloaded-large-scalar", 20000)
			}
			data.OAuthApplications = []domain.OAuthApplication{app}
			if suffix == "-a" {
				app.ID += "-second"
				data.OAuthApplications = append(data.OAuthApplications, app)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	bounded, err := f.BoundedMetadataViewForTesting(ctx)
	if err != nil {
		t.Fatal(err)
	}
	withCatalog := func(source *SQLiteStore, workspace string, read func()) {
		t.Helper()
		source.mu.Lock()
		catalog := source.workspaces
		source.workspaces = map[string]domain.Bootstrap{workspace: catalog[workspace]}
		source.mu.Unlock()
		defer func() {
			source.mu.Lock()
			source.workspaces = catalog
			source.mu.Unlock()
		}()
		read()
	}
	var expected, actual domain.OAuthClient
	withCatalog(f.SQLiteStore, workspaces[1], func() {
		var found bool
		expected, found = f.OAuthApplicationClient(clientID)
		if !found || expected.HomeWorkspaceKey != workspaces[1] || expected.ClientName != "-z" {
			t.Fatal("legacy catalog fixture did not select its loaded application")
		}
	})
	withCatalog(bounded, workspaces[1], func() {
		var found bool
		actual, found = bounded.OAuthApplicationClient(clientID)
		if !found || !reflect.DeepEqual(expected, actual) {
			t.Fatal("native client lookup stopped at an unloaded workspace")
		}
		if _, found := bounded.OAuthApplicationClient(clientID + "-missing"); found {
			t.Fatal("missing client matched an unloaded candidate")
		}
	})
	if bounded.WorkspaceMetadataDecodes() != 0 || bounded.metadataResidency.work.used != 0 {
		t.Fatal("skipping unloaded clients decoded complete metadata or leaked admission")
	}
}

func TestScaleP21SW17NativeOAuthDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	same := func(left, right any) bool {
		leftJSON, leftErr := json.Marshal(left)
		rightJSON, rightErr := json.Marshal(right)
		return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
	}
	workspace := f.data.Workspace.URLKey
	stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	past, future := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	prefix := idgen.New("sw17-oauth-")
	app := domain.OAuthApplication{
		ID: prefix + "-app", ClientID: prefix + "-client", Name: "null",
		Description: strings.Repeat("unrelated manifest description ", 20000),
		LogoURL:     "/uploads/owned-icon.png", ClientSecret: "fixture-secret", DeveloperURL: "https://example.test/developer",
		Scopes: []string{"read", "write"}, RedirectURIs: []string{"https://example.test/callback"},
		SupportsClientCredentials: true, PublicEnabled: true, CreatorID: f.data.Viewer.ID, CreatedAt: stamp,
	}
	keys := []domain.APIKey{}
	for _, spec := range []struct {
		name, restriction string
		scopes, teams     []string
		revoked, expires  *time.Time
	}{
		{"session-equivalent", "", nil, nil, nil, nil},
		{"unrestricted", "all", []string{"read"}, nil, nil, &future},
		{"selected-empty", "selected", []string{}, []string{}, nil, nil},
		{"selected-set", "selected", []string{"read", "write"}, []string{"private-team", "inherited-team"}, nil, nil},
		{"revoked", "all", []string{"admin"}, nil, &past, nil},
		{"expired", "all", []string{"read"}, nil, nil, &past},
		{"no-subject-membership-check", "all", nil, nil, nil, nil},
	} {
		keys = append(keys, domain.APIKey{
			ID: prefix + "-key-" + spec.name, Name: spec.name, SecretHash: prefix + "-hash-" + spec.name,
			CreatorID: f.data.Viewer.ID, SubjectUserID: prefix + "-subject", Scopes: spec.scopes, TeamIDs: spec.teams,
			TeamRestriction: spec.restriction, RevokedAt: spec.revoked, ExpiresAt: spec.expires, CreatedAt: stamp,
		})
	}
	largeKey := domain.APIKey{ID: prefix + "-large-key", Name: "Large selected set", SecretHash: prefix + "-large-hash", CreatorID: f.data.Viewer.ID, TeamRestriction: "selected", CreatedAt: stamp}
	for index := range 16000 {
		largeKey.TeamIDs = append(largeKey.TeamIDs, fmt.Sprintf("retained-team-reference-%05d", index))
	}
	keys = append(keys, largeKey)
	keys = append(keys,
		domain.APIKey{ID: prefix + "-duplicate-revoked", SecretHash: prefix + "-duplicate-hash", RevokedAt: &past},
		domain.APIKey{ID: prefix + "-duplicate-live", SecretHash: prefix + "-duplicate-hash", CreatorID: f.data.Viewer.ID},
		domain.APIKey{ID: prefix + "-empty-hash", CreatorID: f.data.Viewer.ID},
	)
	reset := func(current domain.OAuthApplication) {
		t.Helper()
		if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "oauthApplications", "apiKeys"), workspace, "scale_p21_sw17.oauth", "", nil, func(data *domain.Bootstrap) error {
			data.OAuthApplications = []domain.OAuthApplication{current}
			data.APIKeys = slices.Clone(keys)
			for index := range 65 {
				data.OAuthApplications = append(data.OAuthApplications, domain.OAuthApplication{
					ID: fmt.Sprintf("%s-unused-app-%d", prefix, index), ClientID: fmt.Sprintf("%s-unused-client-%d", prefix, index),
					Description: "Retained unrelated application", Name: "Unrelated",
				})
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	reset(app)
	bounded, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, current := range []domain.OAuthApplication{
		app,
		func() domain.OAuthApplication {
			next := app
			next.Scopes, next.RedirectURIs = nil, nil
			next.ClientSecret = ""
			next.PublicEnabled, next.SupportsClientCredentials = false, false
			return next
		}(),
		func() domain.OAuthApplication {
			next := app
			next.Scopes, next.RedirectURIs = []string{}, []string{}
			return next
		}(),
		func() domain.OAuthApplication {
			next := app
			next.DeveloperURL = "https://example.test/" + strings.Repeat("legal-long-url/", 22000)
			return next
		}(),
	} {
		reset(current)
		expected, found := f.OAuthApplicationClient(current.ClientID)
		actual, present := bounded.OAuthApplicationClient(current.ClientID)
		if !found || !present || !reflect.DeepEqual(expected, actual) {
			t.Fatal("native client metadata lost a field, nil/empty shape, large output or secret-presence decision")
		}
		for _, key := range keys {
			lookup := func(source *SQLiteStore) (string, domain.APIKey, bool) {
				if key.SecretHash != "" {
					return source.FindAPIKey(key.SecretHash)
				}
				// Multiple workspaces may contain empty legacy hashes on the
				// persistent CI database. Their old map order is unspecified.
				source.mu.Lock()
				catalog := source.workspaces
				source.workspaces = map[string]domain.Bootstrap{workspace: catalog[workspace]}
				source.mu.Unlock()
				matchedWorkspace, matched, found := source.FindAPIKey("")
				source.mu.Lock()
				source.workspaces = catalog
				source.mu.Unlock()
				return matchedWorkspace, matched, found
			}
			oldWorkspace, old, oldFound := lookup(f.SQLiteStore)
			newWorkspace, actual, present := lookup(bounded)
			if oldWorkspace != newWorkspace || oldFound != present || !same(old, actual) {
				t.Fatalf("native key changed %s credential fields or expiry/revocation behavior", key.Name)
			}
		}
	}
	for _, value := range []string{prefix + "-missing", strings.ToUpper(app.ClientID), app.ID, app.ClientID + "' OR 1=1 --", app.ClientID + "\x00", "\x00" + app.ClientID, app.ClientID[:len(app.ClientID)/2] + "\x00" + app.ClientID[len(app.ClientID)/2:]} {
		if old, found := f.OAuthApplicationClient(value); found {
			if current, present := bounded.OAuthApplicationClient(value); !present || !reflect.DeepEqual(old, current) {
				t.Fatal("an exact client reference changed")
			}
		} else if _, present := bounded.OAuthApplicationClient(value); present {
			t.Fatal("a missing, differently cased or malformed client reference matched")
		}
	}
	for _, hash := range []string{prefix + "-missing", strings.ToUpper(keys[0].SecretHash), keys[0].ID, keys[0].SecretHash + "\x00", "\x00" + keys[0].SecretHash} {
		if _, _, found := bounded.FindAPIKey(hash); found {
			t.Fatal("a missing, differently cased or malformed credential hash matched")
		}
	}
	row := func(kind, id string) []byte {
		t.Helper()
		var raw []byte
		if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?`, workspace, kind, id).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		return raw
	}
	before := row("apiKeys", largeKey.ID)
	_, owned, found := bounded.FindAPIKey(largeKey.SecretHash)
	if !found || len(owned.TeamIDs) != len(largeKey.TeamIDs) {
		t.Fatal("legal selected output larger than a detail page was omitted")
	}
	owned.TeamIDs[0] = "caller-owned"
	if !bytes.Equal(before, row("apiKeys", largeKey.ID)) {
		t.Fatal("returned credential aliases canonical data")
	}
	keys[0].RevokedAt = &past
	reset(app)
	if _, _, found := bounded.FindAPIKey(keys[0].SecretHash); found {
		t.Fatal("key lookup reused revoked policy")
	}
	keys[0].RevokedAt = nil
	keys[0].SecretHash += "-rotated"
	reset(app)
	if _, _, found := bounded.FindAPIKey(strings.TrimSuffix(keys[0].SecretHash, "-rotated")); found {
		t.Fatal("key lookup accepted the predecessor of a rotated hash")
	}
	if _, current, found := bounded.FindAPIKey(keys[0].SecretHash); !found || !same(current, keys[0]) {
		t.Fatal("key lookup did not observe a fresh rotated hash")
	}
	if bounded.WorkspaceMetadataDecodes() != 0 || bounded.metadataResidency.work.used != 0 {
		t.Fatal("native OAuth lookup decoded complete metadata or leaked work")
	}
}
