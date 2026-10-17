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
//
// A consumed refresh token can be replayed for OAuthRefreshReplayGrace while
// its successor is unused: the replay issues a new access token and returns
// the same successor in grant.ReplayedRefreshToken. validateCode checks the
// authorization code; for refresh tokens it is optional and receives the
// stored grant decoded as a code (client and PKCE metadata only).
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
	replayed := ""
	apply := func() error {
		replayed = ""
		s.lockWorkspaceWrites()
		defer s.unlockWorkspaceWrites()
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
		if err := oauthActiveMembership(ctx, tx, current.Workspace.ID, grant.UserID, lock); err != nil {
			return err
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
		if err != nil || !time.Now().UTC().Before(expires) {
			return ErrAuthForbidden
		}
		if used.Valid {
			if kind != "refresh_token" {
				return ErrAuthForbidden
			}
			successor, ok, err := oauthRefreshReplay(ctx, tx, token, lock)
			if err != nil {
				return err
			}
			if !ok {
				return ErrAuthForbidden
			}
			replayed = successor
		}
		if err := json.Unmarshal(raw, &grant); err != nil {
			return err
		}
		if grant.ClientID == "" || grant.ClientID != clientID || grant.WorkspaceKey != workspace {
			return ErrAuthForbidden
		}
		if kind == "authorization_code" || validateCode != nil {
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
		policy, err := oauthPolicyMetadata(ctx, tx, workspace, lock)
		if err != nil {
			return err
		}
		if approved != nil && !approved(&policy, grant.ClientID, grant.Scopes) {
			return ErrAuthForbidden
		}
		if grant.Actor == "app" {
			var raw []byte
			var installation domain.ApplicationInstallation
			if err := tx.QueryRowContext(ctx, `SELECT data FROM application_installations WHERE workspace_key=? AND user_id=?`+lock, workspace, grant.UserID).Scan(&raw); err != nil {
				return ErrAuthForbidden
			}
			if json.Unmarshal(raw, &installation) != nil || !installation.Active || slices.ContainsFunc(grant.Scopes, func(scope string) bool { return !slices.Contains(installation.Scopes, scope) }) {
				return ErrAuthForbidden
			}
		}
		key.CreatorID = grant.UserID
		key.SubjectUserID = grant.UserID
		key.IssuedByUserID = grant.UserID
		key.Actor = "user"
		key.GrantType = kind
		key.Scopes = slices.Clone(grant.Scopes)
		key.OAuthClientID = grant.ClientID
		key.AuthorizationID = grant.AuthorizationID
		if grant.FamilyID == "" {
			// A code exchange (or a refresh token minted before families
			// existed) starts a new chain.
			grant.FamilyID = "family_" + tokenHash(hash + key.ID)[:24]
		}
		key.RefreshFamilyID = grant.FamilyID
		if grant.Actor == "app" {
			key.IssuedByUserID = grant.InstallerID
			key.Actor = "app"
			key.TeamRestriction = "application"
		}
		// Access tokens live an hour and each exchange issues a new one, so
		// drop expired OAuth token rows instead of letting them pile up. Live
		// tokens stay: one authorization can back several client sessions.
		// An app installation holds one token chain at a time:,
		// redeeming a new authorization code for an actor=app grant (after
		// "Authorize", "Update access" or "Continue") revokes the access and
		// refresh tokens issued to the app user before. Client-credentials
		// tokens are separate and stay.
		supersede := kind == "authorization_code" && grant.Actor == "app"
		if supersede {
			if _, err := tx.ExecContext(ctx, `UPDATE oauth_refresh_tokens SET revoked_at=? WHERE revoked_at IS NULL AND `+s.jsonText("data", "clientId")+`=? AND `+s.jsonText("data", "userId")+`=?`, time.Now().UTC().Format(time.RFC3339Nano), grant.ClientID, grant.UserID); err != nil {
				return err
			}
		}
		kept := current.APIKeys[:0:0]
		for _, existing := range current.APIKeys {
			if supersede && existing.RevokedAt == nil && existing.OAuthClientID == grant.ClientID && existing.Actor == "app" && existing.SubjectID() == grant.UserID && existing.GrantType != "client_credentials" {
				revokedAt := time.Now().UTC()
				existing.RevokedAt = &revokedAt
				raw, err := json.Marshal(existing)
				if err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='apiKeys' AND record_key=?`, raw, workspace, existing.ID); err != nil {
					return err
				}
			}
			if existing.OAuthClientID != "" && existing.ExpiresAt != nil && !time.Now().UTC().Before(*existing.ExpiresAt) {
				if _, err := tx.ExecContext(ctx, `DELETE FROM workspace_metadata_records WHERE workspace_key=? AND field='apiKeys' AND record_key=?`, workspace, existing.ID); err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, `DELETE FROM api_key_lookup WHERE workspace_key=? AND key_id=?`, workspace, existing.ID); err != nil {
					return err
				}
				continue
			}
			kept = append(kept, existing)
		}
		keyRaw, err := json.Marshal(key)
		if err != nil {
			return err
		}
		order := -time.Now().UTC().UnixMicro()
		if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'apiKeys',?,?,?)`, workspace, key.ID, order, keyRaw); err != nil {
			return err
		}
		if err := upsertAPIKeyLookup(ctx, tx, workspace, key.ID, key.SecretHash); err != nil {
			return err
		}
		// Older empty workspaces can omit the array shape until the first key.
		if err := ensureOAuthMetadataShape(ctx, tx, workspace, "apiKeys", "array", lock); err != nil {
			return err
		}
		grant.ExpiresAt = time.Now().UTC().Add(30 * 24 * time.Hour)
		now := time.Now().UTC()
		if replayed == "" {
			refreshRaw, err := json.Marshal(grant)
			if err != nil {
				return err
			}
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
			if kind == "refresh_token" {
				if err := storeOAuthRefreshReplay(ctx, tx, token, refreshToken, now); err != nil {
					return err
				}
			}
		}
		event = domain.DomainEvent{ID: fmt.Sprintf("evt_%d", now.UnixNano()), Type: "oauth_token.created", AggregateID: grant.AuthorizationID, Payload: json.RawMessage(`null`), CreatedAt: now}
		if _, err := insertDomainEvent(ctx, tx, domainEventRow{ID: event.ID, Type: event.Type, AggregateID: event.AggregateID, Payload: []byte(event.Payload), PreviousValues: []byte(nil), CreatedAt: now.Format(time.RFC3339Nano), WorkspaceID: current.Workspace.ID}); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		current.APIKeys = append([]domain.APIKey{key}, kept...)
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
	grant.ReplayedRefreshToken = replayed
	if sink := s.webhook(); sink != nil {
		sink(workspace, event)
	}
	// Token rotation does not change browser workspace data. Keep its durable
	// audit/webhook event without waking every connected workspace client.
	return grant, nil
}
