package store

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
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
	return s.checkLiveKeyRecord(ctx, result, workspace, hash, approved)
}

// APIKeyAuthenticationByID is AuthenticateAPIKeyRecord for a caller that holds
// the key's identifier instead of its secret (a file link signed for the key):
// the same live checks, including revocation, expiry, the owner's membership,
// application and authorization state.
func (s *SQLiteStore) APIKeyAuthenticationByID(ctx context.Context, workspace, keyID string, approved func(*domain.Bootstrap, string, []string) bool) (APIKeyAuthentication, error) {
	result := APIKeyAuthentication{}
	if workspace == "" || keyID == "" {
		return result, ErrAuthForbidden
	}
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='apiKeys' AND record_key=?`, workspace, keyID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return result, ErrAuthForbidden
	}
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal(raw, &result.Key); err != nil {
		return result, err
	}
	if result.Key.ID != keyID || result.Key.SecretHash == "" {
		return result, ErrAuthForbidden
	}
	return s.checkLiveKeyRecord(ctx, result, workspace, result.Key.SecretHash, approved)
}

func (s *SQLiteStore) checkLiveKeyRecord(ctx context.Context, result APIKeyAuthentication, workspace, hash string, approved func(*domain.Bootstrap, string, []string) bool) (APIKeyAuthentication, error) {
	var raw []byte
	key := result.Key
	if key.GrantType == "" && key.AuthorizationID == "" && strings.HasPrefix(key.OAuthClientID, "flow_client_") {
		return result, ErrAuthForbidden
	}
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
	role, status, err := s.WorkspaceRole(ctx, result.Workspace.ID, key.SubjectID())
	if err != nil {
		return result, err
	}
	if status != "active" {
		return result, ErrAuthForbidden
	}
	result.Role = role
	result.User, err = s.UserByID(ctx, key.SubjectID())
	if err != nil {
		return result, err
	}
	if !result.User.Active {
		return result, ErrAuthForbidden
	}
	if result.User.App {
		app, err := s.ApplicationByUser(ctx, result.User.ID)
		if err != nil {
			return result, err
		}
		if !app.Active || app.WorkspaceKey != workspace || key.OAuthClientID != app.ClientID || !slices.Contains(app.Scopes, "read") {
			return result, ErrAuthForbidden
		}
		if key.Scopes == nil || slices.ContainsFunc(key.Scopes, func(scope string) bool { return !slices.Contains(app.Scopes, scope) }) {
			return result, ErrAuthForbidden
		}
		result.Key.Actor = "app"
		teams := slices.Clone(app.AccessTeamIDs())
		if key.TeamRestriction == "selected" || key.TeamRestriction == "" && len(key.TeamIDs) > 0 {
			teams = slices.DeleteFunc(teams, func(id string) bool { return !slices.Contains(key.TeamIDs, id) })
		}
		result.Key.TeamRestriction = "selected"
		result.Key.TeamIDs = teams
	}
	if key.GrantType == "client_credentials" || key.GrantType == "developer_token" || strings.HasPrefix(key.OAuthClientID, "flow_client_") {
		home, ok, err := s.LookupOAuthApplicationHome(ctx, key.OAuthClientID)
		if err != nil {
			return result, err
		}
		if !ok {
			return result, ErrAuthForbidden
		}
		client, err := s.workspaceOAuthApplication(ctx, home, key.OAuthClientID)
		if err != nil {
			return result, err
		}
		if slices.ContainsFunc(key.Scopes, func(scope string) bool { return !slices.Contains(client.Scopes, scope) }) {
			return result, ErrAuthForbidden
		}
		if key.GrantType == "client_credentials" && (!client.SupportsClientCredentials || key.ClientSecretHash == "" || subtle.ConstantTimeCompare([]byte(key.ClientSecretHash), []byte(tokenHash(client.ClientSecret))) != 1) {
			return result, ErrAuthForbidden
		}
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
		if authorization.RevokedAt != nil || authorization.UserID != key.SubjectID() || authorization.ClientID != key.OAuthClientID || key.Scopes == nil && authorization.Scopes != nil || slices.ContainsFunc(key.Scopes, func(scope string) bool { return !slices.Contains(authorization.Scopes, scope) }) {
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
	// target is the store the use is recorded in: the store that served the
	// request, or the live store behind a COLD facade. The owning store's
	// queue and worker only carry it.
	target *SQLiteStore
}

// Usage is telemetry, not an authentication dependency. A bounded worker queue
// coalesces bursts; database predicates also throttle competing instances.
func (s *SQLiteStore) RecordAPIKeyUse(ctx context.Context, workspace, keyID, authorizationID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// A COLD facade records in the live store behind it. Facades and test
	// views share the owning store's queue and worker (its shutdown joins
	// them), but each store keeps its own uses: a test view records in itself.
	target := s
	for target.coldParent != nil {
		target = target.coldParent
	}
	s = s.workerOwner()
	s.workerMu.Lock()
	defer s.workerMu.Unlock()
	if s.workersClosed {
		return context.Canceled
	}
	s.apiKeyUseMu.Lock()
	defer s.apiKeyUseMu.Unlock()
	if s.apiKeyUseQueue == nil {
		s.apiKeyUseQueue = make(chan apiKeyUse, 256)
		s.apiKeyUseTimes = map[string]time.Time{}
		s.workers.Add(1)
		go s.runAPIKeyUses()
	}
	now := time.Now().UTC()
	identity := fmt.Sprintf("%p:%s:%s", target, workspace, keyID)
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
	case s.apiKeyUseQueue <- apiKeyUse{workspace, keyID, authorizationID, now, target}:
		s.apiKeyUseTimes[identity] = now
	default:
	}
	return nil
}

func (s *SQLiteStore) runAPIKeyUses() {
	defer s.workers.Done()
	ctx := s.WorkerContext()
	for {
		select {
		case <-ctx.Done():
			return
		case use := <-s.apiKeyUseQueue:
			pass, cancel := context.WithTimeout(ctx, 2*time.Second)
			err := use.target.writeAPIKeyUse(pass, use)
			cancel()
			if err != nil {
				s.apiKeyUseMu.Lock()
				delete(s.apiKeyUseTimes, fmt.Sprintf("%p:%s:%s", use.target, use.workspace, use.keyID))
				s.apiKeyUseMu.Unlock()
			}
		}
	}
}

func (s *SQLiteStore) writeAPIKeyUse(ctx context.Context, use apiKeyUse) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.writeAPIKeyUseRows(ctx, tx, use); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.dropMetadataCache(ctx, use.workspace)
	if s.metadataResidency != nil {
		// The bounded resident copy holds no credentials: the use is
		// installed into the logical generation (write_chain.go),
		// as apiKeyUseMirror patches the legacy copy.
		return s.publishBoundedAPIKeyUse(ctx, use)
	}
	s.lockWorkspaceWrites()
	defer s.unlockWorkspaceWrites()
	data, ok := s.workspaces[use.workspace]
	if !ok {
		return nil
	}
	data = apiKeyUseMirror(data, use)
	s.setWorkspaceLocked(use.workspace, data)
	return nil
}

// apiKeyUseMirror stamps the use on the copy's key and grant, as the rows'
// update does except for its once-a-minute predicate.
func apiKeyUseMirror(data domain.Bootstrap, use apiKeyUse) domain.Bootstrap {
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
	return data
}

func (s *SQLiteStore) writeAPIKeyUseRows(ctx context.Context, tx *sqlTx, use apiKeyUse) error {
	expression := "CAST(json_set(data,'$.lastUsedAt',?) AS BLOB)"
	if s.dialect == "mysql" {
		expression = "JSON_SET(CONVERT(data USING utf8mb4),'$.lastUsedAt',?)"
	}
	if s.dialect == "postgres" {
		expression = "convert_to(jsonb_set(flow_utf8(data)::jsonb,'{lastUsedAt}',to_jsonb(?::text))::text,'UTF8')"
	}
	lastUsed := s.jsonText("data", "lastUsedAt")
	for _, record := range []struct{ field, id string }{{"apiKeys", use.keyID}, {"oauthAuthorizations", use.authorizationID}} {
		if record.id == "" {
			continue
		}
		_, err := tx.ExecContext(ctx, "UPDATE workspace_metadata_records SET data="+expression+" WHERE workspace_key=? AND field=? AND record_key=? AND "+s.jsonText("data", "revokedAt")+" IS NULL AND ("+lastUsed+" IS NULL OR "+s.timestampTextKey(lastUsed)+"<?)", use.at.Format(time.RFC3339Nano), use.workspace, record.field, record.id, formatStoredTime(use.at.Add(-time.Minute)))
		if err != nil {
			return err
		}
	}
	return nil
}
