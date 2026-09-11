package store

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"flow/api/internal/domain"
)

func oauthPolicyMetadata(ctx context.Context, tx metadataReader, workspace, lock string) (domain.Bootstrap, error) {
	data := domain.Bootstrap{Settings: map[string]any{}}
	rows, err := tx.QueryContext(ctx, `SELECT field,record_key,data FROM workspace_metadata_records WHERE workspace_key=? AND ((field='workspaceSettings' AND record_key='reviewThirdPartyApplications') OR (field='settings' AND record_key='applicationPolicies'))`+lock, workspace)
	if err != nil {
		return data, err
	}
	defer rows.Close()
	for rows.Next() {
		var field, id string
		var raw []byte
		if err := rows.Scan(&field, &id, &raw); err != nil {
			return data, err
		}
		if field == "workspaceSettings" {
			err = json.Unmarshal(raw, &data.WorkspaceSettings.ReviewThirdPartyApplications)
		} else {
			var value any
			err = json.Unmarshal(raw, &value)
			data.Settings[id] = value
		}
		if err != nil {
			return data, err
		}
	}
	return data, rows.Err()
}

// The legacy metadata writer replaces absent rows, so these targeted writes must
// share its lock until both SQL and the immutable cache replacement are visible.
func (s *SQLiteStore) oauthMetadataTransaction(ctx context.Context, workspace string, mutate func(*sqlTx, string, domain.Bootstrap) (domain.Bootstrap, error)) error {
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
		next, err := mutate(tx, lock, current)
		if err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		s.workspaces[workspace] = next
		return nil
	}
	if s.coordinator != nil {
		return s.coordinator.WithWorkspaceLock(ctx, workspace, apply)
	}
	return apply()
}

func ensureOAuthMetadataShape(ctx context.Context, tx *sqlTx, workspace, field, shape, lock string) error {
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT data FROM workspace_states WHERE workspace_key=?`+lock, workspace).Scan(&raw); err != nil {
		return err
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return err
	}
	shapes := map[string]string{}
	if err := json.Unmarshal(root[metadataCollectionsKey], &shapes); err != nil {
		return err
	}
	if shapes[field] == shape {
		return nil
	}
	shapes[field] = shape
	var err error
	root[metadataCollectionsKey], err = json.Marshal(shapes)
	if err != nil {
		return err
	}
	if shape == "array" {
		root[field] = json.RawMessage(`[]`)
	} else {
		root[field] = json.RawMessage(`{}`)
	}
	raw, err = json.Marshal(root)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE workspace_states SET data=? WHERE workspace_key=?`, raw, workspace)
	return err
}

func oauthRecordEvent(ctx context.Context, tx *sqlTx, eventType, id string) (domain.DomainEvent, error) {
	now := time.Now().UTC()
	event := domain.DomainEvent{ID: fmt.Sprintf("evt_%d", now.UnixNano()), Type: eventType, AggregateID: id, Payload: json.RawMessage(`null`), CreatedAt: now}
	_, err := tx.ExecContext(ctx, `INSERT INTO domain_events(id,event_type,aggregate_id,payload,previous_values,created_at) VALUES(?,?,?,?,?,?)`, event.ID, event.Type, id, []byte(event.Payload), []byte(nil), now.Format(time.RFC3339Nano))
	return event, err
}

func (s *SQLiteStore) publishOAuthRecordEvent(workspace string, event domain.DomainEvent) {
	if sink := s.webhook(); sink != nil {
		sink(workspace, event)
	}
	if sink := s.realtime(); sink != nil && !domain.RoutineCredentialEvent(event.Type) {
		sink(workspace, domain.RealtimeEvent{ID: event.ID, Type: event.Type, AggregateID: event.AggregateID, CreatedAt: event.CreatedAt})
	}
}

func (s *SQLiteStore) CreateOAuthAuthorizationGrant(ctx context.Context, code string, grant domain.OAuthAuthorizationCode, authorization domain.OAuthAuthorization, approved func(*domain.Bootstrap, string, []string) bool) (domain.OAuthAuthorizationCode, error) {
	var event domain.DomainEvent
	err := s.oauthMetadataTransaction(ctx, grant.WorkspaceKey, func(tx *sqlTx, lock string, current domain.Bootstrap) (domain.Bootstrap, error) {
		if err := oauthActiveMembership(ctx, tx, current.Workspace.ID, grant.UserID, lock); err != nil {
			return current, err
		}
		policy, err := oauthPolicyMetadata(ctx, tx, grant.WorkspaceKey, lock)
		if err != nil {
			return current, err
		}
		if approved != nil && !approved(&policy, grant.ClientID, grant.Scopes) {
			return current, ErrAuthForbidden
		}
		text := "CAST(data AS TEXT)"
		if s.dialect == "mysql" {
			text = "CAST(data AS CHAR)"
		}
		if s.dialect == "postgres" {
			text = "convert_from(data,'UTF8')"
		}
		rows, err := tx.QueryContext(ctx, `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='oauthAuthorizations' AND `+text+` LIKE ? ESCAPE '!' AND `+text+` LIKE ? ESCAPE '!' ORDER BY collection_order,record_key`+lock, grant.WorkspaceKey, labelReferencePattern(grant.ClientID), labelReferencePattern(grant.UserID))
		if err != nil {
			return current, err
		}
		var existing *domain.OAuthAuthorization
		for rows.Next() {
			var raw []byte
			var candidate domain.OAuthAuthorization
			if err := rows.Scan(&raw); err != nil {
				rows.Close()
				return current, err
			}
			if err := json.Unmarshal(raw, &candidate); err != nil {
				rows.Close()
				return current, err
			}
			if candidate.RevokedAt == nil && candidate.ClientID == grant.ClientID && candidate.UserID == grant.UserID && sameOAuthStringSet(candidate.Scopes, grant.Scopes) {
				existing = &candidate
				break
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return current, err
		}
		if existing != nil {
			authorization = *existing
		} else {
			authorization.ClientID = grant.ClientID
			authorization.UserID = grant.UserID
			authorization.Scopes = slices.Clone(grant.Scopes)
			if authorization.CreatedAt.IsZero() {
				authorization.CreatedAt = time.Now().UTC()
			}
			raw, err := json.Marshal(authorization)
			if err != nil {
				return current, err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'oauthAuthorizations',?,?,?)`, grant.WorkspaceKey, authorization.ID, -authorization.CreatedAt.UnixMicro(), raw); err != nil {
				return current, err
			}
			if err := ensureOAuthMetadataShape(ctx, tx, grant.WorkspaceKey, "oauthAuthorizations", "array", lock); err != nil {
				return current, err
			}
		}
		grant.AuthorizationID = authorization.ID
		raw, err := json.Marshal(grant)
		if err != nil {
			return current, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO oauth_authorization_codes(code_hash,data,expires_at,created_at) VALUES(?,?,?,?)`, tokenHash(code), raw, grant.ExpiresAt.Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return current, err
		}
		eventType := "oauth_authorization.created"
		if existing != nil {
			eventType = "oauth_authorization.reused"
		}
		event, err = oauthRecordEvent(ctx, tx, eventType, authorization.ID)
		if err != nil {
			return current, err
		}
		if !slices.ContainsFunc(current.OAuthAuthorizations, func(item domain.OAuthAuthorization) bool { return item.ID == authorization.ID }) {
			current.OAuthAuthorizations = append([]domain.OAuthAuthorization{authorization}, current.OAuthAuthorizations...)
		}
		return current, nil
	})
	if err == nil {
		s.publishOAuthRecordEvent(grant.WorkspaceKey, event)
	}
	return grant, err
}

func oauthActiveMembership(ctx context.Context, tx *sqlTx, workspaceID, userID, lock string) error {
	var status string
	err := tx.QueryRowContext(ctx, `SELECT status FROM workspace_memberships WHERE workspace_id=? AND user_id=?`+lock, workspaceID, userID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) || err == nil && status != "active" {
		return ErrAuthForbidden
	}
	return err
}

func (s *SQLiteStore) RequestOAuthApplicationApproval(ctx context.Context, workspace, clientID string, mutate func(*domain.Bootstrap) error) error {
	var event domain.DomainEvent
	err := s.oauthMetadataTransaction(ctx, workspace, func(tx *sqlTx, lock string, current domain.Bootstrap) (domain.Bootstrap, error) {
		policy, err := oauthPolicyMetadata(ctx, tx, workspace, lock)
		if err != nil {
			return current, err
		}
		if err := mutate(&policy); err != nil {
			return current, err
		}
		raw, err := json.Marshal(policy.Settings["applicationPolicies"])
		if err != nil {
			return current, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'settings','applicationPolicies',0,?) ON CONFLICT(workspace_key,field,record_key) DO UPDATE SET data=excluded.data`, workspace, raw); err != nil {
			return current, err
		}
		if err := ensureOAuthMetadataShape(ctx, tx, workspace, "settings", "map", lock); err != nil {
			return current, err
		}
		event, err = oauthRecordEvent(ctx, tx, "application_policy.updated", clientID)
		if err != nil {
			return current, err
		}
		current.Settings = maps.Clone(current.Settings)
		if current.Settings == nil {
			current.Settings = map[string]any{}
		}
		current.Settings["applicationPolicies"] = policy.Settings["applicationPolicies"]
		return current, nil
	})
	if err == nil {
		s.publishOAuthRecordEvent(workspace, event)
	}
	return err
}

func (s *SQLiteStore) RevokeOAuthAuthorizationRecords(ctx context.Context, workspace, id, userID string) error {
	var event domain.DomainEvent
	err := s.oauthMetadataTransaction(ctx, workspace, func(tx *sqlTx, lock string, current domain.Bootstrap) (domain.Bootstrap, error) {
		var raw []byte
		if err := tx.QueryRowContext(ctx, `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='oauthAuthorizations' AND record_key=?`+lock, workspace, id).Scan(&raw); err != nil {
			return current, err
		}
		var authorization domain.OAuthAuthorization
		if err := json.Unmarshal(raw, &authorization); err != nil {
			return current, err
		}
		if authorization.UserID != userID {
			return current, ErrAuthForbidden
		}
		now := time.Now().UTC()
		authorization.RevokedAt = &now
		raw, err := json.Marshal(authorization)
		if err != nil {
			return current, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='oauthAuthorizations' AND record_key=?`, raw, workspace, id); err != nil {
			return current, err
		}
		text := "CAST(data AS TEXT)"
		if s.dialect == "mysql" {
			text = "CAST(data AS CHAR)"
		}
		if s.dialect == "postgres" {
			text = "convert_from(data,'UTF8')"
		}
		rows, err := tx.QueryContext(ctx, `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='apiKeys' AND `+text+` LIKE ? ESCAPE '!'`+lock, workspace, labelReferencePattern(id))
		if err != nil {
			return current, err
		}
		keys := map[string]domain.APIKey{}
		for rows.Next() {
			var item domain.APIKey
			if err := rows.Scan(&raw); err != nil {
				rows.Close()
				return current, err
			}
			if err := json.Unmarshal(raw, &item); err != nil {
				rows.Close()
				return current, err
			}
			if item.AuthorizationID == id && item.RevokedAt == nil {
				item.RevokedAt = &now
				keys[item.ID] = item
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return current, err
		}
		for keyID, item := range keys {
			raw, err := json.Marshal(item)
			if err != nil {
				return current, err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='apiKeys' AND record_key=?`, raw, workspace, keyID); err != nil {
				return current, err
			}
		}
		// Refresh and code exchanges check the authorization row in their transaction;
		// revocation therefore blocks both without scanning historical token tables.
		event, err = oauthRecordEvent(ctx, tx, "oauth_authorization.revoked", id)
		if err != nil {
			return current, err
		}
		current.OAuthAuthorizations = slices.Clone(current.OAuthAuthorizations)
		for i := range current.OAuthAuthorizations {
			if current.OAuthAuthorizations[i].ID == id {
				current.OAuthAuthorizations[i] = authorization
			}
		}
		if len(keys) > 0 {
			current.APIKeys = slices.Clone(current.APIKeys)
			for i := range current.APIKeys {
				if item, ok := keys[current.APIKeys[i].ID]; ok {
					current.APIKeys[i] = item
				}
			}
		}
		return current, nil
	})
	if errors.Is(err, sql.ErrNoRows) {
		return sql.ErrNoRows
	}
	if err == nil {
		s.publishOAuthRecordEvent(workspace, event)
	}
	return err
}

// RevokeOAuthAccessToken is idempotent and only touches the credential matched
// by the secret-hash index. Recheck the hash after locking to handle rotation.
func (s *SQLiteStore) RevokeOAuthAccessToken(ctx context.Context, secretHash string) error {
	var workspace, id string
	err := s.db.QueryRowContext(ctx, `SELECT workspace_key,key_id FROM api_key_lookup WHERE secret_hash=? LIMIT 1`, secretHash).Scan(&workspace, &id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var event domain.DomainEvent
	err = s.oauthMetadataTransaction(ctx, workspace, func(tx *sqlTx, lock string, current domain.Bootstrap) (domain.Bootstrap, error) {
		var raw []byte
		err := tx.QueryRowContext(ctx, `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='apiKeys' AND record_key=?`+lock, workspace, id).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			return current, nil
		}
		if err != nil {
			return current, err
		}
		var key domain.APIKey
		if err := json.Unmarshal(raw, &key); err != nil {
			return current, err
		}
		if subtle.ConstantTimeCompare([]byte(key.SecretHash), []byte(secretHash)) != 1 || key.RevokedAt != nil {
			return current, nil
		}
		now := time.Now().UTC()
		key.RevokedAt = &now
		raw, err = json.Marshal(key)
		if err != nil {
			return current, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='apiKeys' AND record_key=?`, raw, workspace, id); err != nil {
			return current, err
		}
		event, err = oauthRecordEvent(ctx, tx, "oauth_token.revoked", id)
		if err != nil {
			return current, err
		}
		current.APIKeys = slices.Clone(current.APIKeys)
		for i := range current.APIKeys {
			if current.APIKeys[i].ID == id {
				current.APIKeys[i] = key
				break
			}
		}
		return current, nil
	})
	if err == nil && event.ID != "" {
		s.publishOAuthRecordEvent(workspace, event)
	}
	return err
}
