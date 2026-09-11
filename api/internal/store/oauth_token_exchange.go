package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"flow/api/internal/domain"
)

// ExchangeOAuthGrant commits replacement credentials and consumption together.
// Invalid client/PKCE requests and storage failures leave the original usable.
func (s *SQLiteStore) ExchangeOAuthGrant(ctx context.Context, kind, token, clientID, refreshToken string, key domain.APIKey, validateCode func(domain.OAuthAuthorizationCode) bool, approved func(*domain.Bootstrap, string, []string) bool) (domain.OAuthRefreshGrant, error) {
	table, identity, consumed := "oauth_refresh_tokens", "token_hash", "revoked_at"
	if kind == "authorization_code" {
		table, identity, consumed = "oauth_authorization_codes", "code_hash", "used_at"
	} else if kind != "refresh_token" {
		return domain.OAuthRefreshGrant{}, ErrAuthForbidden
	}
	hash := tokenHash(token)
	var initial []byte
	if err := s.db.QueryRowContext(ctx, "SELECT data FROM "+table+" WHERE "+identity+"=?", hash).Scan(&initial); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.OAuthRefreshGrant{}, ErrAuthForbidden
		}
		return domain.OAuthRefreshGrant{}, err
	}
	var grant domain.OAuthRefreshGrant
	if err := json.Unmarshal(initial, &grant); err != nil {
		return grant, err
	}
	workspace := grant.WorkspaceKey
	var event domain.DomainEvent
	apply := func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		current, ok := s.workspaces[workspace]
		if !ok {
			return ErrAuthForbidden
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		lock := ""
		if s.dialect != "sqlite" {
			lock = " FOR UPDATE"
		}
		var raw []byte
		var expiration string
		var used sql.NullString
		if err := tx.QueryRowContext(ctx, "SELECT data,expires_at,"+consumed+" FROM "+table+" WHERE "+identity+"=?"+lock, hash).Scan(&raw, &expiration, &used); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrAuthForbidden
			}
			return err
		}
		expires, err := time.Parse(time.RFC3339Nano, expiration)
		if err != nil || used.Valid || !time.Now().UTC().Before(expires) {
			return ErrAuthForbidden
		}
		if err := json.Unmarshal(raw, &grant); err != nil {
			return err
		}
		if grant.ClientID != clientID || grant.WorkspaceKey != workspace {
			return ErrAuthForbidden
		}
		if kind == "authorization_code" {
			var code domain.OAuthAuthorizationCode
			if err := json.Unmarshal(raw, &code); err != nil {
				return err
			}
			if validateCode == nil || !validateCode(code) {
				return ErrAuthForbidden
			}
		}
		var authorization domain.OAuthAuthorization
		if err := tx.QueryRowContext(ctx, `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='oauthAuthorizations' AND record_key=?`+lock, workspace, grant.AuthorizationID).Scan(&raw); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrAuthForbidden
			}
			return err
		}
		if err := json.Unmarshal(raw, &authorization); err != nil {
			return err
		}
		if authorization.RevokedAt != nil || authorization.ClientID != grant.ClientID || authorization.UserID != grant.UserID || slices.ContainsFunc(grant.Scopes, func(scope string) bool { return !slices.Contains(authorization.Scopes, scope) }) {
			return ErrAuthForbidden
		}
		// Policy reads are narrow metadata rows, never issue/discussion hydration.
		policy := domain.Bootstrap{Settings: map[string]any{}}
		rows, err := tx.QueryContext(ctx, `SELECT field,record_key,data FROM workspace_metadata_records WHERE workspace_key=? AND ((field='workspaceSettings' AND record_key='reviewThirdPartyApplications') OR (field='settings' AND record_key='applicationPolicies'))`+lock, workspace)
		if err != nil {
			return err
		}
		for rows.Next() {
			var field, id string
			var value []byte
			if err := rows.Scan(&field, &id, &value); err != nil {
				rows.Close()
				return err
			}
			if field == "workspaceSettings" {
				err = json.Unmarshal(value, &policy.WorkspaceSettings.ReviewThirdPartyApplications)
			} else {
				var policies any
				err = json.Unmarshal(value, &policies)
				policy.Settings[id] = policies
			}
			if err != nil {
				rows.Close()
				return err
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if approved != nil && !approved(&policy, grant.ClientID, grant.Scopes) {
			return ErrAuthForbidden
		}
		key.CreatorID = grant.UserID
		key.Scopes = slices.Clone(grant.Scopes)
		key.OAuthClientID = grant.ClientID
		key.AuthorizationID = grant.AuthorizationID
		keyRaw, err := json.Marshal(key)
		if err != nil {
			return err
		}
		var order int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MIN(collection_order),0)-1 FROM workspace_metadata_records WHERE workspace_key=? AND field='apiKeys'`, workspace).Scan(&order); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'apiKeys',?,?,?)`, workspace, key.ID, order, keyRaw); err != nil {
			return err
		}
		// Older empty workspaces can omit the array shape until the first key.
		var rootRaw []byte
		if err := tx.QueryRowContext(ctx, `SELECT data FROM workspace_states WHERE workspace_key=?`+lock, workspace).Scan(&rootRaw); err != nil {
			return err
		}
		var root map[string]json.RawMessage
		if err := json.Unmarshal(rootRaw, &root); err != nil {
			return err
		}
		shapes := map[string]string{}
		if err := json.Unmarshal(root[metadataCollectionsKey], &shapes); err != nil {
			return err
		}
		if shapes["apiKeys"] != "array" {
			shapes["apiKeys"] = "array"
			root[metadataCollectionsKey], err = json.Marshal(shapes)
			if err != nil {
				return err
			}
			root["apiKeys"] = json.RawMessage(`[]`)
			rootRaw, err = json.Marshal(root)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE workspace_states SET data=? WHERE workspace_key=?`, rootRaw, workspace); err != nil {
				return err
			}
		}
		grant.ExpiresAt = time.Now().UTC().Add(30 * 24 * time.Hour)
		refreshRaw, err := json.Marshal(grant)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		if _, err := tx.ExecContext(ctx, `INSERT INTO oauth_refresh_tokens(token_hash,data,expires_at,created_at) VALUES(?,?,?,?)`, tokenHash(refreshToken), refreshRaw, grant.ExpiresAt.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, "UPDATE "+table+" SET "+consumed+"=? WHERE "+identity+"=? AND "+consumed+" IS NULL", now.Format(time.RFC3339Nano), hash)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count != 1 {
			return ErrAuthForbidden
		}
		event = domain.DomainEvent{ID: fmt.Sprintf("evt_%d", now.UnixNano()), Type: "oauth_token.created", AggregateID: grant.AuthorizationID, Payload: json.RawMessage(`null`), CreatedAt: now}
		if _, err := tx.ExecContext(ctx, `INSERT INTO domain_events(id,event_type,aggregate_id,payload,previous_values,created_at) VALUES(?,?,?,?,?,?)`, event.ID, event.Type, event.AggregateID, []byte(event.Payload), []byte(nil), now.Format(time.RFC3339Nano)); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		current.APIKeys = append([]domain.APIKey{key}, current.APIKeys...)
		s.workspaces[workspace] = current
		return nil
	}
	var err error
	if s.coordinator != nil {
		err = s.coordinator.WithWorkspaceLock(ctx, workspace, apply)
	} else {
		err = apply()
	}
	if err != nil {
		return grant, err
	}
	if sink := s.webhook(); sink != nil {
		sink(workspace, event)
	}
	if sink := s.realtime(); sink != nil {
		sink(workspace, domain.RealtimeEvent{ID: event.ID, Type: event.Type, AggregateID: event.AggregateID, ActorID: grant.UserID, CreatedAt: event.CreatedAt})
	}
	return grant, nil
}
