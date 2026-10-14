package store

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

var ErrOAuthGrantDisabled = errors.New("client credentials are disabled")
var ErrOAuthTokenLimit = errors.New("application token limit reached")

func (s *SQLiteStore) readWorkspaceOAuthApplication(ctx context.Context, reader metadataReader, workspace, clientID, lock string) (domain.OAuthApplication, error) {
	rows, err := reader.QueryContext(ctx, `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='oauthApplications' AND `+s.jsonText("data", "clientId")+`=? LIMIT 1`+lock, workspace, clientID)
	if err != nil {
		return domain.OAuthApplication{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var app domain.OAuthApplication
		if err := rows.Scan(&raw); err != nil {
			return app, err
		}
		if err := json.Unmarshal(raw, &app); err != nil {
			return app, err
		}
		if app.ClientID == clientID {
			return app, nil
		}
	}
	if err := rows.Err(); err != nil {
		return domain.OAuthApplication{}, err
	}
	return domain.OAuthApplication{}, ErrAuthForbidden
}

func (s *SQLiteStore) workspaceOAuthApplication(ctx context.Context, workspace, clientID string) (domain.OAuthApplication, error) {
	return s.readWorkspaceOAuthApplication(ctx, s.db, workspace, clientID, "")
}

// A confidential client may only mint app-actor credentials in its home workspace.
// Interactive developer issuance is separately authorized against the live role.
func (s *SQLiteStore) IssueApplicationCredential(ctx context.Context, workspace, clientID, clientSecret, issuerID, actor string, requested []string, key domain.APIKey, approved func(*domain.Bootstrap, string, []string) bool) (domain.APIKey, error) {
	var event domain.DomainEvent
	err := s.oauthMetadataTransaction(ctx, workspace, func(tx *sqlTx, lock string, current domain.Bootstrap) (domain.Bootstrap, error) {
		app, err := s.readWorkspaceOAuthApplication(ctx, tx, workspace, clientID, lock)
		if err != nil {
			return current, err
		}
		if key.GrantType == "client_credentials" {
			if clientSecret == "" || subtle.ConstantTimeCompare([]byte(clientSecret), []byte(app.ClientSecret)) != 1 {
				return current, ErrAuthForbidden
			}
			if !app.SupportsClientCredentials {
				return current, ErrOAuthGrantDisabled
			}
			actor = "app"
			issuerID = app.CreatorID
			if app.CredentialsAuthorizedBy != "" {
				issuerID = app.CredentialsAuthorizedBy
			}
			key.ClientSecretHash = tokenHash(app.ClientSecret)
		} else {
			var role, status string
			if err := tx.QueryRowContext(ctx, `SELECT role,status FROM workspace_memberships WHERE workspace_id=? AND user_id=?`+lock, current.Workspace.ID, issuerID).Scan(&role, &status); err != nil || status != "active" || !isWorkspaceAdminRole(role) {
				return current, ErrAuthForbidden
			}
		}
		if actor != "app" && actor != "user" {
			return current, ErrAuthForbidden
		}
		if requested == nil {
			// admin / timeSchedule:write are user-actor scopes; an app's
			// default token carries only the scopes an app actor can hold.
			requested = slices.DeleteFunc(slices.Clone(app.Scopes), domain.UserOnlyOAuthScope)
		}
		scopes, err := domain.NormalizeApplicationScopes(requested)
		if err != nil || slices.ContainsFunc(scopes, func(scope string) bool { return !slices.Contains(app.Scopes, scope) }) {
			return current, ErrAuthForbidden
		}
		policy, err := oauthPolicyMetadata(ctx, tx, workspace, lock)
		if err != nil {
			return current, err
		}
		if approved != nil && !approved(&policy, clientID, scopes) {
			return current, ErrAuthForbidden
		}
		key.SubjectUserID, key.CreatorID, key.IssuedByUserID = issuerID, issuerID, issuerID
		key.Actor, key.OAuthClientID, key.Scopes = actor, clientID, scopes
		if actor == "app" {
			// A copy: decoding an existing installation below reuses this
			// slice's backing array, which would overwrite the token's scopes.
			installation := domain.ApplicationInstallation{ClientID: clientID, Name: app.Name, AvatarURL: app.LogoURL, InstalledBy: issuerID, AllPublicTeams: true, Scopes: slices.Clone(scopes), Active: true}
			var raw []byte
			err := tx.QueryRowContext(ctx, `SELECT data FROM application_installations WHERE workspace_key=? AND client_id=?`+lock, workspace, clientID).Scan(&raw)
			if err == nil {
				if err := json.Unmarshal(raw, &installation); err != nil {
					return current, err
				}
				if !installation.Active {
					return current, ErrAuthForbidden
				}
				if slices.ContainsFunc(scopes, func(scope string) bool { return !slices.Contains(installation.Scopes, scope) }) {
					installation.Scopes = slices.Clone(installation.Scopes)
					for _, scope := range scopes {
						if !slices.Contains(installation.Scopes, scope) {
							installation.Scopes = append(installation.Scopes, scope)
						}
					}
					installation.InstalledBy = issuerID
					current, installation, err = s.installApplicationTx(ctx, tx, lock, current, installation, "")
					if err != nil {
						return current, err
					}
				}
			} else if IsOAuthNotFound(err) {
				current, installation, err = s.installApplicationTx(ctx, tx, lock, current, installation, "")
				if err != nil {
					return current, err
				}
			} else {
				return current, err
			}
			key.SubjectUserID, key.CreatorID = installation.UserID, installation.UserID
			key.TeamRestriction = "application"
		} else if slices.ContainsFunc(scopes, func(scope string) bool {
			return !slices.Contains([]string{"read", "write", "create_issues", "create_comments"}, scope)
		}) {
			return current, ErrAuthForbidden
		}
		keys := slices.Clone(current.APIKeys)
		activeTokens := 0
		now := time.Now().UTC()
		for index := range keys {
			old := &keys[index]
			if old.OAuthClientID != clientID || old.SubjectID() != key.SubjectID() || old.RevokedAt != nil || old.ExpiresAt != nil && !old.ExpiresAt.After(now) {
				continue
			}
			if actor == "app" && key.GrantType == "client_credentials" && !sameOAuthStringSet(old.Scopes, scopes) {
				old.RevokedAt = &now
				raw, err := json.Marshal(old)
				if err != nil {
					return current, err
				}
				if _, err := tx.ExecContext(ctx, `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='apiKeys' AND record_key=?`, raw, workspace, old.ID); err != nil {
					return current, err
				}
			} else {
				activeTokens++
			}
		}
		if activeTokens >= 1000 {
			return current, ErrOAuthTokenLimit
		}
		raw, err := json.Marshal(key)
		if err != nil {
			return current, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'apiKeys',?,?,?)`, workspace, key.ID, -key.CreatedAt.UnixMicro(), raw); err != nil {
			return current, err
		}
		if err := upsertAPIKeyLookup(ctx, tx, workspace, key.ID, key.SecretHash); err != nil {
			return current, err
		}
		if err := ensureOAuthMetadataShape(ctx, tx, workspace, "apiKeys", "array", lock); err != nil {
			return current, err
		}
		event, err = oauthRecordEvent(ctx, tx, "oauth_token.created", key.ID, map[string]string{"subjectUserId": key.SubjectID(), "issuedByUserId": key.IssuedByUserID, "actor": key.Actor, "grantType": key.GrantType, "clientId": clientID})
		if err != nil {
			return current, err
		}
		current.APIKeys = append([]domain.APIKey{key}, keys...)
		return current, nil
	})
	if err == nil {
		s.publishOAuthRecordEvent(workspace, event)
	}
	return key, err
}

func (s *SQLiteStore) OAuthApplicationHome(clientID string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for workspace, data := range s.workspaces {
		if slices.ContainsFunc(data.OAuthApplications, func(app domain.OAuthApplication) bool { return app.ClientID == clientID }) {
			return workspace, true
		}
	}
	return "", false
}

// An application's app user holds one developer token at a time (the reference app's
// "Create & copy token › App" copies the existing token instead of minting
// another). The token itself is kept, sealed like webhook signing secrets,
// so the settings page can copy it again; the API key record decides
// whether it is still active.
func applicationDeveloperTokenRef(workspace, clientID string) string {
	return "devtoken:" + workspace + ":" + clientID
}

// SetApplicationDeveloperToken remembers the app developer token issued as
// API key keyID.
func (s *SQLiteStore) SetApplicationDeveloperToken(ctx context.Context, workspace, clientID, keyID, token string) error {
	if keyID == "" || token == "" || strings.ContainsRune(keyID, ' ') {
		return ErrIssueQuery
	}
	return s.SetWebhookSigningSecret(ctx, applicationDeveloperTokenRef(workspace, clientID), workspace, keyID+" "+token)
}

// ApplicationDeveloperToken returns the remembered app developer token and
// its API key ID, if one was issued.
func (s *SQLiteStore) ApplicationDeveloperToken(ctx context.Context, workspace, clientID string) (string, string, bool, error) {
	stored, ok, err := s.WebhookSigningSecret(ctx, applicationDeveloperTokenRef(workspace, clientID))
	if err != nil || !ok {
		return "", "", false, err
	}
	keyID, token, found := strings.Cut(stored, " ")
	if !found || keyID == "" || token == "" {
		return "", "", false, nil
	}
	return keyID, token, true, nil
}

// DeleteApplicationDeveloperToken forgets the remembered app developer token.
func (s *SQLiteStore) DeleteApplicationDeveloperToken(ctx context.Context, workspace, clientID string) error {
	return s.DeleteWebhookSigningSecret(ctx, applicationDeveloperTokenRef(workspace, clientID))
}
