package store

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"flow/api/internal/domain"
)

type APIKeyAuthentication struct {
	Workspace domain.Workspace
	Key       domain.APIKey
	User      domain.User
	Role      string
}

// Read the live credential row on every request: the hash index is only a
// locator, never a cache of authorization or revocation decisions.
func (s *SQLiteStore) AuthenticateAPIKeyRecord(ctx context.Context, workspaceHint, hash string, approved func(*domain.Bootstrap, string, []string) bool) (APIKeyAuthentication, error) {
	result := APIKeyAuthentication{}
	where, args := "locator.secret_hash=?", []any{hash}
	if workspaceHint != "" {
		where += " AND locator.workspace_key=?"
		args = append(args, workspaceHint)
	}
	var raw []byte
	var workspace string
	err := s.db.QueryRowContext(ctx, `SELECT locator.workspace_key,record.data FROM api_key_lookup locator JOIN workspace_metadata_records record ON record.workspace_key=locator.workspace_key AND record.field='apiKeys' AND `+s.metadataRecordKeyEquals("record.record_key", "locator.key_id")+` WHERE `+where+` LIMIT 1`, args...).Scan(&workspace, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return result, ErrAuthForbidden
	}
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal(raw, &result.Key); err != nil {
		return result, err
	}
	key := result.Key
	if subtle.ConstantTimeCompare([]byte(key.SecretHash), []byte(hash)) != 1 || key.RevokedAt != nil || key.ExpiresAt != nil && !key.ExpiresAt.After(time.Now().UTC()) {
		return result, ErrAuthForbidden
	}
	// The workspace identity is a scalar row in workspace_states; project and
	// issue collections live elsewhere and are never decoded here.
	if err := s.db.QueryRowContext(ctx, "SELECT "+s.jsonText("data", "workspace")+" FROM workspace_states WHERE workspace_key=?", workspace).Scan(&raw); err != nil {
		return result, err
	}
	if err := json.Unmarshal(raw, &result.Workspace); err != nil {
		return result, err
	}
	role, status, err := s.WorkspaceRole(ctx, result.Workspace.ID, key.CreatorID)
	if err != nil || status != "active" {
		return result, ErrAuthForbidden
	}
	result.Role = role
	result.User, err = s.UserByID(ctx, key.CreatorID)
	if err != nil {
		return result, err
	}
	if !result.User.Active {
		return result, ErrAuthForbidden
	}
	if key.AuthorizationID != "" {
		var authorization domain.OAuthAuthorization
		if err := s.db.QueryRowContext(ctx, `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='oauthAuthorizations' AND record_key=?`, workspace, key.AuthorizationID).Scan(&raw); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return result, ErrAuthForbidden
			}
			return result, err
		}
		if err := json.Unmarshal(raw, &authorization); err != nil {
			return result, err
		}
		if authorization.RevokedAt != nil || authorization.UserID != key.CreatorID || authorization.ClientID != key.OAuthClientID || key.Scopes == nil && authorization.Scopes != nil || slices.ContainsFunc(key.Scopes, func(scope string) bool { return !slices.Contains(authorization.Scopes, scope) }) {
			return result, ErrAuthForbidden
		}
	}
	if key.OAuthClientID != "" {
		policy, err := oauthPolicyMetadata(ctx, s.db, workspace, "")
		if err != nil {
			return result, err
		}
		policy.Workspace = result.Workspace
		if approved != nil && !approved(&policy, key.OAuthClientID, key.Scopes) {
			return result, ErrAuthForbidden
		}
	}
	return result, nil
}

type apiKeyUse struct {
	workspace, keyID, authorizationID string
	at                                time.Time
}

// Usage is telemetry, not an authentication dependency. A bounded worker queue
// coalesces bursts; database predicates also throttle competing instances.
func (s *SQLiteStore) RecordAPIKeyUse(ctx context.Context, workspace, keyID, authorizationID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.apiKeyUseMu.Lock()
	defer s.apiKeyUseMu.Unlock()
	if s.apiKeyUseClosed {
		return context.Canceled
	}
	s.apiKeyUseOnce.Do(func() {
		s.apiKeyUseQueue = make(chan apiKeyUse, 256)
		s.apiKeyUseTimes = map[string]time.Time{}
		s.apiKeyUseWorkers.Add(1)
		go s.runAPIKeyUses()
	})
	now := time.Now().UTC()
	identity := workspace + ":" + keyID
	if at, ok := s.apiKeyUseTimes[identity]; ok && now.Sub(at) < time.Minute {
		return nil
	}
	if len(s.apiKeyUseTimes) >= 4096 {
		for key, at := range s.apiKeyUseTimes {
			if now.Sub(at) >= time.Minute {
				delete(s.apiKeyUseTimes, key)
			}
		}
		if len(s.apiKeyUseTimes) >= 4096 {
			return nil
		}
	}
	select {
	case s.apiKeyUseQueue <- apiKeyUse{workspace, keyID, authorizationID, now}:
		s.apiKeyUseTimes[identity] = now
	default:
	}
	return nil
}

func (s *SQLiteStore) runAPIKeyUses() {
	defer s.apiKeyUseWorkers.Done()
	for {
		select {
		case <-s.WorkerContext().Done():
			return
		case use := <-s.apiKeyUseQueue:
			ctx, cancel := context.WithTimeout(s.WorkerContext(), 2*time.Second)
			err := s.writeAPIKeyUse(ctx, use)
			cancel()
			if err != nil {
				s.apiKeyUseMu.Lock()
				delete(s.apiKeyUseTimes, use.workspace+":"+use.keyID)
				s.apiKeyUseMu.Unlock()
			}
		}
	}
}

func (s *SQLiteStore) writeAPIKeyUse(ctx context.Context, use apiKeyUse) error {
	expression := "CAST(json_set(data,'$.lastUsedAt',?) AS BLOB)"
	if s.dialect == "mysql" {
		expression = "JSON_SET(CONVERT(data USING utf8mb4),'$.lastUsedAt',?)"
	}
	if s.dialect == "postgres" {
		expression = "convert_to(jsonb_set(convert_from(data,'UTF8')::jsonb,'{lastUsedAt}',to_jsonb(?::text))::text,'UTF8')"
	}
	for _, record := range []struct{ field, id string }{{"apiKeys", use.keyID}, {"oauthAuthorizations", use.authorizationID}} {
		if record.id == "" {
			continue
		}
		_, err := s.db.ExecContext(ctx, "UPDATE workspace_metadata_records SET data="+expression+" WHERE workspace_key=? AND field=? AND record_key=? AND "+s.jsonText("data", "revokedAt")+" IS NULL AND ("+s.jsonText("data", "lastUsedAt")+" IS NULL OR "+s.jsonText("data", "lastUsedAt")+"<?)", use.at.Format(time.RFC3339Nano), use.workspace, record.field, record.id, use.at.Add(-time.Minute).Format(time.RFC3339Nano))
		if err != nil {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.workspaces[use.workspace]
	if !ok {
		return nil
	}
	if index := slices.IndexFunc(data.APIKeys, func(key domain.APIKey) bool { return key.ID == use.keyID }); index >= 0 && data.APIKeys[index].RevokedAt == nil {
		data.APIKeys = slices.Clone(data.APIKeys)
		at := use.at
		data.APIKeys[index].LastUsedAt = &at
	}
	if index := slices.IndexFunc(data.OAuthAuthorizations, func(item domain.OAuthAuthorization) bool { return item.ID == use.authorizationID }); index >= 0 && data.OAuthAuthorizations[index].RevokedAt == nil {
		data.OAuthAuthorizations = slices.Clone(data.OAuthAuthorizations)
		at := use.at
		data.OAuthAuthorizations[index].LastUsedAt = &at
	}
	s.workspaces[use.workspace] = data
	return nil
}
