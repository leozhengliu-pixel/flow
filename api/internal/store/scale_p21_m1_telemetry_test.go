package store

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP21M1APIKeyTelemetryDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var original [2][]byte
	for _, revoked := range []bool{false, true} {
		var results [2][2][]byte
		for mode := range 2 {
			if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
			if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.seed_telemetry", "", nil, func(data *domain.Bootstrap) error {
				key := domain.APIKey{ID: "m1-telemetry-key", CreatorID: f.data.Viewer.ID, Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{}}
				grant := domain.OAuthAuthorization{ID: "m1-telemetry-grant", UserID: f.data.Viewer.ID, Scopes: []string{"read"}}
				if revoked {
					key.RevokedAt, grant.RevokedAt = &at, &at
				}
				data.APIKeys, data.OAuthAuthorizations = []domain.APIKey{key}, []domain.OAuthAuthorization{grant}
				data.Settings["m1-unrelated"] = strings.Repeat("unrelated ", 40000)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			source := f.SQLiteStore
			if mode == 1 {
				var err error
				source, err = f.BoundedMetadataViewForTesting(t.Context())
				if err != nil {
					t.Fatal(err)
				}
			}
			read := func(field, id string) []byte {
				t.Helper()
				var raw []byte
				if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?`, workspace, field, id).Scan(&raw); err != nil {
					t.Fatal(err)
				}
				return raw
			}
			unrelated := read("settings", "m1-unrelated")
			original = [2][]byte{read("apiKeys", "m1-telemetry-key"), read("oauthAuthorizations", "m1-telemetry-grant")}
			if err := source.writeAPIKeyUse(t.Context(), apiKeyUse{workspace: workspace, keyID: "m1-telemetry-key", authorizationID: "m1-telemetry-grant", at: at.Add(time.Hour)}); err != nil {
				t.Fatal(err)
			}
			results[mode] = [2][]byte{read("apiKeys", "m1-telemetry-key"), read("oauthAuthorizations", "m1-telemetry-grant")}
			if !bytes.Equal(unrelated, read("settings", "m1-unrelated")) || mode == 1 && source.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("telemetry changed unrelated metadata or decoded the workspace")
			}
			for i := range original {
				if bytes.Equal(original[i], results[mode][i]) != revoked {
					t.Fatal("usage timestamp update ignored the live revoked predicate")
				}
			}
		}
		for i := range results[0] {
			if !bytes.Equal(results[0][i], results[1][i]) {
				t.Fatal("telemetry canonical SQL differs between store representations")
			}
		}
	}
}
