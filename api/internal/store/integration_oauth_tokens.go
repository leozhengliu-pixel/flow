package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"flow/api/internal/domain"
)

// Workspace integrations connected with OAuth (GitHub, GitLab, Jira, Slack,
// Figma and the other providers of /api/integrations/{provider}/oauth) keep
// their access and refresh tokens in connector_secrets, sealed under
// FLOW_CONNECTOR_SECRET_KEY. The workspace document only records that they
// exist (IntegrationConnection.OAuthAuthorized) and when the access token
// expires, so the tokens never reach metadata rows, domain events, exports,
// API responses or caches.

// IntegrationOAuthTokens are an integration connection's OAuth credentials.
type IntegrationOAuthTokens struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken,omitempty"`
}

// integrationOAuthOwner is the ConnectorSecretID owner of integration tokens.
const integrationOAuthOwner = "integration-oauth"

// ErrIntegrationOAuthTokensChanged is returned by RotateIntegrationOAuthTokens
// when the stored refresh token is no longer the one the caller exchanged
// (another refresh or a reconnect replaced it).
var ErrIntegrationOAuthTokensChanged = errors.New("integration OAuth tokens changed")

// IntegrationOAuthSecretID is the connector_secrets id of a connection's
// tokens. workspace is the workspace URL key.
func IntegrationOAuthSecretID(workspace, connectionID string) string {
	return ConnectorSecretID(workspace, integrationOAuthOwner, connectionID)
}

// integrationOAuthSecretExpiry keeps the row well past any token lifetime;
// every refresh writes it again.
func integrationOAuthSecretExpiry() time.Time { return time.Now().UTC().AddDate(5, 0, 0) }

// PutIntegrationOAuthTokens stores a connection's tokens, replacing any
// stored ones. Without FLOW_CONNECTOR_SECRET_KEY it returns
// ErrSecretKeyMissing.
func (s *SQLiteStore) PutIntegrationOAuthTokens(ctx context.Context, workspace, connectionID string, tokens IntegrationOAuthTokens) error {
	if tokens.AccessToken == "" && tokens.RefreshToken == "" {
		return s.DeleteIntegrationOAuthTokens(ctx, workspace, connectionID)
	}
	raw, err := json.Marshal(tokens)
	if err != nil {
		return err
	}
	return s.PutConnectorSecret(ctx, IntegrationOAuthSecretID(workspace, connectionID), raw, integrationOAuthSecretExpiry())
}

// IntegrationOAuthTokens returns a connection's tokens; found is false when
// none are stored.
func (s *SQLiteStore) IntegrationOAuthTokens(ctx context.Context, workspace, connectionID string) (IntegrationOAuthTokens, bool, error) {
	var tokens IntegrationOAuthTokens
	raw, err := s.ReadConnectorSecret(ctx, IntegrationOAuthSecretID(workspace, connectionID), false)
	if errors.Is(err, sql.ErrNoRows) {
		return tokens, false, nil
	}
	if err != nil {
		return tokens, false, err
	}
	if err := json.Unmarshal(raw, &tokens); err != nil {
		return IntegrationOAuthTokens{}, false, errors.New("stored integration OAuth tokens are invalid")
	}
	return tokens, tokens.AccessToken != "" || tokens.RefreshToken != "", nil
}

// RotateIntegrationOAuthTokens stores the result of a refresh exchange only
// while the stored refresh token is still refreshToken, the one exchanged, so
// two concurrent refreshes cannot overwrite each other's rotation. An empty
// next.RefreshToken keeps the stored one (providers that do not rotate it).
func (s *SQLiteStore) RotateIntegrationOAuthTokens(ctx context.Context, workspace, connectionID, refreshToken string, next IntegrationOAuthTokens) error {
	err := s.UpdateConnectorSecret(ctx, IntegrationOAuthSecretID(workspace, connectionID), integrationOAuthSecretExpiry(), func(current []byte) ([]byte, error) {
		var stored IntegrationOAuthTokens
		if json.Unmarshal(current, &stored) != nil || stored.RefreshToken != refreshToken {
			return nil, ErrIntegrationOAuthTokensChanged
		}
		if next.RefreshToken == "" {
			next.RefreshToken = stored.RefreshToken
		}
		return json.Marshal(next)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return ErrIntegrationOAuthTokensChanged
	}
	return err
}

func (s *SQLiteStore) DeleteIntegrationOAuthTokens(ctx context.Context, workspace, connectionID string) error {
	return s.DeleteConnectorSecret(ctx, IntegrationOAuthSecretID(workspace, connectionID))
}

// RemoveIntegrationOAuthTokens deletes a connection's tokens when the stored
// access token is still accessToken (the one just revoked); removed is false
// when a reconnect stored other tokens meanwhile. Tokens that no configured
// key opens are unusable and always deleted.
func (s *SQLiteStore) RemoveIntegrationOAuthTokens(ctx context.Context, workspace, connectionID, accessToken string) (bool, error) {
	id := IntegrationOAuthSecretID(workspace, connectionID)
	var encrypted []byte
	err := s.db.QueryRowContext(ctx, `SELECT ciphertext FROM connector_secrets WHERE id=?`, id).Scan(&encrypted)
	if errors.Is(err, sql.ErrNoRows) {
		return accessToken == "", nil
	}
	if err != nil {
		return false, err
	}
	if plain, _, openErr := openSecret(encrypted, []byte(id)); openErr == nil {
		var stored IntegrationOAuthTokens
		if json.Unmarshal(plain, &stored) == nil && stored.AccessToken != accessToken {
			return false, nil
		}
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM connector_secrets WHERE id=? AND ciphertext=?`, id, encrypted)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

// integrationOAuthTokensMigrationVersion records the move of integration
// OAuth tokens out of the workspace document.
const integrationOAuthTokensMigrationVersion = 1025

// IntegrationOAuthKeyMissingError is the error a connection shows when its
// plain-text tokens were dropped because no key was configured to seal them.
const IntegrationOAuthKeyMissingError = "Reconnect this integration: its OAuth tokens were removed from plain-text storage because FLOW_CONNECTOR_SECRET_KEY was not set"

// legacyTokenFields are the keys older releases wrote the tokens under.
var legacyTokenFields = []string{"oauthAccessToken", "oauthRefreshToken"}

// migrateIntegrationOAuthTokens moves the tokens older releases kept in the
// integrationConnections records into connector_secrets and rewrites those
// records without them. Without a key the tokens cannot be sealed, so they
// are dropped and the connection asks to be reconnected (an OAuth grant can
// always be obtained again; a plain-text copy must not stay). It then removes
// token copies from domain event history. Records without tokens are left
// alone, so it is idempotent.
func (s *SQLiteStore) migrateIntegrationOAuthTokens(ctx context.Context) error {
	keyed := ConnectorSecretsConfigured()
	moved, dropped := 0, 0
	for _, key := range s.WorkspaceKeys() {
		legacy, err := s.legacyIntegrationOAuthTokens(ctx, key)
		if err != nil {
			return fmt.Errorf("workspace %q: %w", key, err)
		}
		if len(legacy) == 0 {
			continue
		}
		stored, ok := s.storedWorkspace(key)
		if !ok {
			continue
		}
		workspace := stored.Workspace.URLKey
		if workspace == "" {
			workspace = key
		}
		if keyed {
			for id, tokens := range legacy {
				if err := s.PutIntegrationOAuthTokens(ctx, workspace, id, tokens); err != nil {
					return fmt.Errorf("workspace %q: store integration tokens: %w", key, err)
				}
			}
		}
		// The connection type no longer has token fields, so rewriting a
		// record stores it without them. Changing a field makes the delta
		// writer rewrite exactly the records that held tokens.
		mutationCtx := WithMetadataFields(WithoutIssueRecordMutations(ctx), "integrationConnections")
		err = s.MutateWorkspace(mutationCtx, key, "integration.oauth_tokens_secured", "", nil, func(data *domain.Bootstrap) error {
			changed := false
			for i := range data.IntegrationConnections {
				connection := &data.IntegrationConnections[i]
				if _, ok := legacy[connection.ID]; !ok {
					continue
				}
				changed = true
				if keyed {
					connection.OAuthAuthorized = true
					moved++
					continue
				}
				connection.OAuthAuthorized, connection.OAuthExpiresAt = false, nil
				connection.Status, connection.LastError = "error", IntegrationOAuthKeyMissingError
				dropped++
			}
			if !changed {
				return ErrNoMutation
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("workspace %q: rewrite integration connections: %w", key, err)
		}
	}
	scrubbed, err := s.scrubIntegrationTokenEvents(ctx)
	if err != nil {
		return err
	}
	if moved+dropped+scrubbed > 0 {
		slog.Info("secured integration OAuth tokens", "encrypted", moved, "dropped_without_key", dropped, "events_scrubbed", scrubbed)
	}
	if dropped > 0 {
		slog.Warn("integration OAuth tokens were dropped because FLOW_CONNECTOR_SECRET_KEY is not set; reconnect the affected integrations", "connections", dropped)
	}
	return nil
}

// legacyIntegrationOAuthTokens reads token fields from the stored
// integrationConnections records (and from the document root of a workspace
// whose collections were never split into records), by connection id.
func (s *SQLiteStore) legacyIntegrationOAuthTokens(ctx context.Context, key string) (map[string]IntegrationOAuthTokens, error) {
	items := []json.RawMessage{}
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='integrationConnections'`, key)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, raw)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	var root []byte
	if err := s.db.QueryRowContext(ctx, `SELECT data FROM workspace_states WHERE workspace_key=?`, key).Scan(&root); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if bytes.Contains(root, []byte(`"oauth`)) {
		var document struct {
			IntegrationConnections []json.RawMessage `json:"integrationConnections"`
		}
		if json.Unmarshal(root, &document) == nil {
			items = append(items, document.IntegrationConnections...)
		}
	}
	result := map[string]IntegrationOAuthTokens{}
	for _, raw := range items {
		if !bytes.Contains(raw, []byte(`"oauth`)) {
			continue
		}
		var item struct {
			ID           string `json:"id"`
			AccessToken  string `json:"oauthAccessToken"`
			RefreshToken string `json:"oauthRefreshToken"`
		}
		if json.Unmarshal(raw, &item) != nil || item.ID == "" || (item.AccessToken == "" && item.RefreshToken == "") {
			continue
		}
		result[item.ID] = IntegrationOAuthTokens{AccessToken: item.AccessToken, RefreshToken: item.RefreshToken}
	}
	return result, nil
}

// scrubIntegrationTokenEvents removes token fields from domain event history.
// Integration mutations recorded the changed connection's previous fields,
// tokens included, in previous_values (TEXT on every dialect, so it can be
// filtered in SQL). Payloads are mutation input, which never accepted
// tokens; integration events' payloads are checked as well.
func (s *SQLiteStore) scrubIntegrationTokenEvents(ctx context.Context) (int, error) {
	scrubbed := 0
	after := ""
	for {
		type row struct {
			id       string
			payload  []byte
			previous sql.NullString
		}
		rows, err := s.db.QueryContext(ctx, `SELECT id,payload,previous_values FROM domain_events WHERE id>? AND (previous_values LIKE ? OR previous_values LIKE ? OR event_type LIKE ?) ORDER BY id LIMIT ?`, after, "%oauthAccessToken%", "%oauthRefreshToken%", "integration.%", resealBatch)
		if err != nil {
			return scrubbed, err
		}
		batch := []row{}
		for rows.Next() {
			var item row
			if err := rows.Scan(&item.id, &item.payload, &item.previous); err != nil {
				rows.Close()
				return scrubbed, err
			}
			batch = append(batch, item)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return scrubbed, err
		}
		for _, item := range batch {
			after = item.id
			payload, payloadChanged := stripTokenFields(item.payload)
			previous, previousChanged := []byte(nil), false
			if item.previous.Valid {
				previous, previousChanged = stripTokenFields([]byte(item.previous.String))
			}
			if !payloadChanged && !previousChanged {
				continue
			}
			args := []any{}
			assignments := ""
			if payloadChanged {
				assignments, args = "payload=?", append(args, payload)
			}
			if previousChanged {
				if assignments != "" {
					assignments += ","
				}
				assignments += "previous_values=?"
				args = append(args, string(previous))
			}
			if _, err := s.db.ExecContext(ctx, `UPDATE domain_events SET `+assignments+` WHERE id=?`, append(args, item.id)...); err != nil {
				return scrubbed, err
			}
			scrubbed++
		}
		if len(batch) < resealBatch {
			return scrubbed, nil
		}
	}
}

// stripTokenFields removes the legacy token keys at any depth of a JSON
// value. changed is false (and raw returned as is) when there was none.
func stripTokenFields(raw []byte) ([]byte, bool) {
	if len(raw) == 0 || !bytes.Contains(raw, []byte(`"oauth`)) {
		return raw, false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return raw, false
	}
	changed := false
	var visit func(any)
	visit = func(node any) {
		switch typed := node.(type) {
		case map[string]any:
			for _, field := range legacyTokenFields {
				if _, ok := typed[field]; ok {
					delete(typed, field)
					changed = true
				}
			}
			for _, child := range typed {
				visit(child)
			}
		case []any:
			for _, child := range typed {
				visit(child)
			}
		}
	}
	visit(value)
	if !changed {
		return raw, false
	}
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if encoder.Encode(value) != nil {
		return raw, false
	}
	return bytes.TrimRight(encoded.Bytes(), "\n"), true
}
