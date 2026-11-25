package store

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// SW431: ExchangeOAuthGrant's key sweep and the API key use stamp on the
// legacy store and a bounded view. The sweep deletes expired OAuth keys at
// the start, middle and end of the collection (revoked or not), keeps live,
// personal and soon-expiring keys (selected by the margin, unchanged), and
// inserts the new key at its load position; the stamp then marks the new key
// and its grant. Every apiKeys/lookup/grant row, the result and the store's
// copy must agree across the stores, and the bounded installed image must
// equal a view loaded afresh from SQL after each step.

func scaleP21SW431Image(t *testing.T, repo *SQLiteStore, workspace string, fields ...string) map[string]string {
	t.Helper()
	result := map[string]string{}
	if !repo.BoundedMetadataEnabled() {
		data, ok := repo.BootstrapFor(workspace)
		if !ok {
			t.Fatal("workspace missing")
		}
		raw, _ := json.Marshal(data)
		var all map[string]json.RawMessage
		if err := json.Unmarshal(raw, &all); err != nil {
			t.Fatal(err)
		}
		for _, field := range fields {
			result[field] = string(all[field])
		}
	} else {
		read, found, err := repo.BeginInstalledMetadataRead(t.Context(), workspace)
		if err != nil || !found {
			t.Fatal("installed read", err)
		}
		for _, field := range fields {
			var raw strings.Builder
			if err := read.StreamFieldJSON(t.Context(), field, &raw); err != nil {
				t.Fatal(err)
			}
			result[field] = raw.String()
		}
		read.Close()
	}
	// Records only: a load fills an empty key's team restriction, the legacy
	// copy keeps a key as its writer handed it.
	for field, raw := range result {
		var items []map[string]any
		if err := json.Unmarshal([]byte(raw), &items); err != nil {
			t.Fatal(field, err)
		}
		for _, item := range items {
			if field == "apiKeys" && item["teamRestriction"] == nil {
				item["teamRestriction"] = "all"
			}
		}
		encoded, _ := json.Marshal(items)
		result[field] = string(encoded)
	}
	return result
}

func TestScaleP21SW431OAuthExchangeSweepDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	f := scaleP09Database(t)
	ctx := ContextWithActor(t.Context(), f.data.Viewer)
	workspace := f.data.Workspace.URLKey
	viewer := f.data.Viewer.ID
	at := time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC)
	now := time.Now().UTC()
	expired, soon, live := now.Add(-time.Hour).Truncate(time.Second), now.Add(2*time.Minute).Truncate(time.Second), now.Add(48*time.Hour).Truncate(time.Second)
	grant := domain.OAuthRefreshGrant{ClientID: "sw431-client", WorkspaceKey: workspace, UserID: viewer, AuthorizationID: "sw431-grant", Scopes: []string{"read"}, ExpiresAt: live}
	key := func(id, client string, expires *time.Time, revoked bool) domain.APIKey {
		item := domain.APIKey{ID: id, Name: id, Prefix: "flow_oauth_" + id, SecretHash: "sw431-hash-" + id, CreatorID: viewer, Scopes: []string{"read"}, TeamIDs: []string{}, OAuthClientID: client, CreatedAt: at, ExpiresAt: expires}
		if revoked {
			item.RevokedAt = &at
		}
		return item
	}
	for _, layout := range []struct {
		name string
		keys []domain.APIKey
	}{
		{"mixed", []domain.APIKey{
			key("sw431-expired-first", "sw431-client", &expired, false),
			key("sw431-live", "sw431-client", &live, false),
			key("sw431-expired-other-client", "sw431-other", &expired, true),
			key("sw431-personal-expired", "", &expired, false),
			key("sw431-soon", "sw431-other", &soon, false),
			key("sw431-no-expiry", "sw431-other", nil, false),
			key("sw431-expired-last", "sw431-other", &at, false),
		}},
		{"all-expired", []domain.APIKey{key("sw431-expired-a", "sw431-client", &expired, false), key("sw431-expired-b", "sw431-other", &at, false)}},
		{"none", nil},
	} {
		t.Run(layout.name, func(t *testing.T) {
			var compared []string
			for mode := range 2 {
				if err := f.ReloadWorkspace(ctx, workspace); err != nil {
					t.Fatal(err)
				}
				if err := f.MutateWorkspace(ctx, workspace, "scale_p21_sw431.seed", "", nil, func(data *domain.Bootstrap) error {
					data.APIKeys = slices.Clone(layout.keys)
					data.OAuthAuthorizations = []domain.OAuthAuthorization{{ID: grant.AuthorizationID, ClientID: grant.ClientID, UserID: viewer, Scopes: grant.Scopes, CreatedAt: at}}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				// Spread the seeded keys' load order so the new key (most
				// negative order) lands first and the deletions shift the rest.
				for index, item := range layout.keys {
					if _, err := f.db.ExecContext(ctx, `UPDATE workspace_metadata_records SET collection_order=? WHERE workspace_key=? AND field='apiKeys' AND record_key=?`, index, workspace, item.ID); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := f.db.ExecContext(ctx, `DELETE FROM oauth_refresh_tokens WHERE 1=?`, 1); err != nil {
					t.Fatal(err)
				}
				if err := f.ReloadWorkspace(ctx, workspace); err != nil {
					t.Fatal(err)
				}
				if err := f.CreateOAuthRefreshToken(ctx, "sw431-refresh", grant); err != nil {
					t.Fatal(err)
				}
				source := f.SQLiteStore
				if mode == 1 {
					var err error
					if source, err = f.BoundedMetadataViewForTesting(ctx); err != nil {
						t.Fatal(err)
					}
				}
				var steps []string
				check := func(step string) {
					t.Helper()
					rows := []string{}
					for _, query := range []string{
						`SELECT field,record_key,data FROM workspace_metadata_records WHERE workspace_key=? AND field IN ('apiKeys','oauthAuthorizations') ORDER BY field,collection_order,record_key`,
						`SELECT key_id,secret_hash,'' FROM api_key_lookup WHERE workspace_key=? ORDER BY key_id`,
					} {
						result, err := f.db.QueryContext(ctx, query, workspace)
						if err != nil {
							t.Fatal(err)
						}
						for result.Next() {
							var a, b string
							var c []byte
							if err := result.Scan(&a, &b, &c); err != nil {
								t.Fatal(err)
							}
							rows = append(rows, a+"\t"+b+"\t"+string(c))
						}
						result.Close()
					}
					installed := scaleP21SW431Image(t, source, workspace, "apiKeys", "oauthAuthorizations")
					if source.BoundedMetadataEnabled() {
						// A view captures its owner's copy: reload the owner from
						// SQL first, so the fresh view is what a reload reads.
						if err := f.ReloadWorkspace(ctx, workspace); err != nil {
							t.Fatal(err)
						}
						fresh, err := f.BoundedMetadataViewForTesting(ctx)
						if err != nil {
							t.Fatal(err)
						}
						reloaded := scaleP21SW431Image(t, fresh, workspace, "apiKeys", "oauthAuthorizations")
						for field := range installed {
							if installed[field] != reloaded[field] {
								t.Errorf("%s: installed %s %s, reload %s", step, field, installed[field], reloaded[field])
							}
						}
						if source.metadataResidency.work.used != 0 {
							t.Errorf("%s: retained work %d", step, source.metadataResidency.work.used)
						}
					}
					raw, _ := json.Marshal(map[string]any{"rows": rows, "installed": installed})
					steps = append(steps, step+" "+string(raw))
				}
				newKey := domain.APIKey{ID: "sw431-new", Name: "MCP OAuth token", Prefix: "flow_oauth_new", SecretHash: "sw431-hash-new", TeamIDs: []string{}, CreatedAt: at, ExpiresAt: &live}
				result, err := source.ExchangeOAuthGrant(ctx, "refresh_token", "sw431-refresh", grant.ClientID, "sw431-next", newKey, nil, nil)
				result.ExpiresAt = time.Time{}
				encoded, _ := json.Marshal(result)
				steps = append(steps, fmt.Sprintf("exchange %v %s", err, encoded))
				check("exchange")
				// A second exchange of the consumed token inside the grace
				// window replays: a new key, the same successor.
				replayKey := newKey
				replayKey.ID, replayKey.SecretHash = "sw431-replay", "sw431-hash-replay"
				result, err = source.ExchangeOAuthGrant(ctx, "refresh_token", "sw431-refresh", grant.ClientID, "sw431-unused", replayKey, nil, nil)
				result.ExpiresAt = time.Time{}
				encoded, _ = json.Marshal(result)
				steps = append(steps, fmt.Sprintf("replay %v %s", err, encoded))
				check("replay")
				use := apiKeyUse{workspace: workspace, keyID: "sw431-new", authorizationID: grant.AuthorizationID, at: now.Add(time.Minute).Truncate(time.Second)}
				if err := source.writeAPIKeyUse(ctx, use); err != nil {
					t.Fatal(err)
				}
				check("use")
				// Wrong client: refused before any write.
				_, err = source.ExchangeOAuthGrant(ctx, "refresh_token", "sw431-next", "sw431-other", "sw431-never", domain.APIKey{ID: "sw431-never"}, nil, nil)
				steps = append(steps, fmt.Sprintf("wrong-client %v", err))
				check("wrong-client")
				text := strings.Join(steps, "\n")
				// The family and refresh IDs derive from the token hashes only.
				compared = append(compared, text)
				if mode == 1 && compared[1] != compared[0] {
					t.Fatalf("bounded differs:\nlegacy  %s\nbounded %s", compared[0], compared[1])
				}
			}
		})
	}
}
