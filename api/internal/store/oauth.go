package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

func (s *SQLiteStore) RegisterOAuthClient(ctx context.Context, client domain.OAuthClient) error {
	raw, err := json.Marshal(client)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO oauth_clients(client_id,data,created_at) VALUES(?,?,?)`, client.ClientID, raw, client.CreatedAt.Format(time.RFC3339Nano))
	return err
}

// FindOAuthClientByMetadata reuses dynamic public clients such as Codex MCP.
// Identity is client_name plus redirect host and path. Loopback ports are
// ignored because CLI callbacks bind an ephemeral local listener each time.
// ClientURI, LogoURI, and grant metadata are not part of identity: MCP
// clients omit or vary those fields across registrations.
func (s *SQLiteStore) FindOAuthClientByMetadata(ctx context.Context, client domain.OAuthClient) (domain.OAuthClient, bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM oauth_clients ORDER BY created_at DESC`)
	if err != nil {
		return domain.OAuthClient{}, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return domain.OAuthClient{}, false, err
		}
		var current domain.OAuthClient
		if json.Unmarshal(raw, &current) != nil {
			continue
		}
		if current.ClientName != client.ClientName || current.TokenEndpointAuthMethod != client.TokenEndpointAuthMethod || !oauthReusableClientName(client.ClientName) {
			continue
		}
		if !sameOAuthRedirects(current.RedirectURIs, client.RedirectURIs) {
			continue
		}
		return current, true, nil
	}
	return domain.OAuthClient{}, false, rows.Err()
}

func oauthReusableClientName(name string) bool {
	name = strings.TrimSpace(name)
	return name != "" && !strings.EqualFold(name, "MCP client")
}

func sameOAuthStringSet(left, right []string) bool {
	left = slices.Clone(left)
	right = slices.Clone(right)
	slices.Sort(left)
	slices.Sort(right)
	return slices.Equal(slices.Compact(left), slices.Compact(right))
}

// CLI loopback listener ports change across logins. Nothing else in a redirect
// URI is interchangeable: trusting only a public client name would allow an
// unrelated registration to append its own callback to an existing client.
func sameOAuthRedirects(left, right []string) bool {
	l, lok := oauthRedirectIdentities(left)
	r, rok := oauthRedirectIdentities(right)
	return lok && rok && len(l) > 0 && sameOAuthStringSet(l, r)
}

func oauthRedirectIdentities(values []string) ([]string, bool) {
	result := make([]string, 0, len(values))
	for _, value := range values {
		identity, ok := oauthRedirectIdentity(value)
		if !ok {
			return nil, false
		}
		result = append(result, identity)
	}
	return result, true
}

func oauthRedirectIdentity(raw string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", false
	}
	host := strings.ToLower(parsed.Hostname())
	if parsed.Scheme == "http" && oauthLoopbackHost(host) {
		if strings.Contains(host, ":") {
			parsed.Host = "[" + host + "]"
		} else {
			parsed.Host = host
		}
	}
	return parsed.String(), true
}

func oauthLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// OAuthRedirectURIAllowed reports whether candidate may be used with a registered
// public client. Loopback ports are ignored so an ephemeral CLI listener does
// not need a new registration.
func OAuthRedirectURIAllowed(registered []string, candidate string) bool {
	identity, ok := oauthRedirectIdentity(candidate)
	if !ok {
		return false
	}
	for _, value := range registered {
		current, currentOK := oauthRedirectIdentity(value)
		if currentOK && current == identity {
			return true
		}
	}
	return false
}

func (s *SQLiteStore) UpdateOAuthClient(ctx context.Context, client domain.OAuthClient) error {
	raw, err := json.Marshal(client)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE oauth_clients SET data=? WHERE client_id=?`, raw, client.ClientID)
	return err
}

func MergeOAuthRedirectURIs(existing, incoming []string) []string {
	result := append([]string(nil), existing...)
	for _, candidate := range incoming {
		found := false
		for _, value := range result {
			if strings.TrimSpace(value) == strings.TrimSpace(candidate) {
				found = true
				break
			}
		}
		if !found {
			result = append(result, candidate)
		}
	}
	return result
}

func (s *SQLiteStore) OAuthClient(ctx context.Context, clientID string) (domain.OAuthClient, error) {
	var raw []byte
	if err := s.db.QueryRowContext(ctx, `SELECT data FROM oauth_clients WHERE client_id=?`, clientID).Scan(&raw); err != nil {
		return domain.OAuthClient{}, err
	}
	var client domain.OAuthClient
	if err := json.Unmarshal(raw, &client); err != nil {
		return domain.OAuthClient{}, err
	}
	return client, nil
}

func (s *SQLiteStore) OAuthApplicationClient(clientID string) (domain.OAuthClient, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, data := range s.workspaces {
		for _, app := range data.OAuthApplications {
			if app.ClientID == clientID {
				return domain.OAuthClient{ClientID: app.ClientID, ClientName: app.Name, RedirectURIs: app.RedirectURIs, GrantTypes: []string{"authorization_code", "refresh_token"}, ResponseTypes: []string{"code"}, TokenEndpointAuthMethod: "none", CreatedAt: app.CreatedAt}, true
			}
		}
	}
	return domain.OAuthClient{}, false
}

func (s *SQLiteStore) CreateOAuthAuthorizationCode(ctx context.Context, code string, grant domain.OAuthAuthorizationCode) error {
	raw, err := json.Marshal(grant)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	_, err = s.db.ExecContext(ctx, `INSERT INTO oauth_authorization_codes(code_hash,data,expires_at,created_at) VALUES(?,?,?,?)`, tokenHash(code), raw, grant.ExpiresAt.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	return err
}

func (s *SQLiteStore) ConsumeOAuthAuthorizationCode(ctx context.Context, code string) (domain.OAuthAuthorizationCode, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.OAuthAuthorizationCode{}, err
	}
	defer tx.Rollback()
	var raw []byte
	var expiresRaw string
	var used sql.NullString
	hash := tokenHash(code)
	if err := tx.QueryRowContext(ctx, `SELECT data,expires_at,used_at FROM oauth_authorization_codes WHERE code_hash=?`, hash).Scan(&raw, &expiresRaw, &used); err != nil {
		return domain.OAuthAuthorizationCode{}, err
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, expiresRaw)
	if err != nil || used.Valid || time.Now().UTC().After(expiresAt) {
		return domain.OAuthAuthorizationCode{}, ErrAuthForbidden
	}
	if _, err := tx.ExecContext(ctx, `UPDATE oauth_authorization_codes SET used_at=? WHERE code_hash=? AND used_at IS NULL`, time.Now().UTC().Format(time.RFC3339Nano), hash); err != nil {
		return domain.OAuthAuthorizationCode{}, err
	}
	var grant domain.OAuthAuthorizationCode
	if err := json.Unmarshal(raw, &grant); err != nil {
		return grant, err
	}
	if err := tx.Commit(); err != nil {
		return grant, err
	}
	return grant, nil
}

func (s *SQLiteStore) CreateOAuthRefreshToken(ctx context.Context, token string, grant domain.OAuthRefreshGrant) error {
	raw, err := json.Marshal(grant)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO oauth_refresh_tokens(token_hash,data,expires_at,created_at) VALUES(?,?,?,?)`, tokenHash(token), raw, grant.ExpiresAt.Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (s *SQLiteStore) ConsumeOAuthRefreshToken(ctx context.Context, token string) (domain.OAuthRefreshGrant, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.OAuthRefreshGrant{}, err
	}
	defer tx.Rollback()
	var raw []byte
	var expiresRaw string
	var revoked sql.NullString
	hash := tokenHash(token)
	if err := tx.QueryRowContext(ctx, `SELECT data,expires_at,revoked_at FROM oauth_refresh_tokens WHERE token_hash=?`, hash).Scan(&raw, &expiresRaw, &revoked); err != nil {
		return domain.OAuthRefreshGrant{}, err
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, expiresRaw)
	if err != nil || revoked.Valid || time.Now().UTC().After(expiresAt) {
		return domain.OAuthRefreshGrant{}, ErrAuthForbidden
	}
	if _, err := tx.ExecContext(ctx, `UPDATE oauth_refresh_tokens SET revoked_at=? WHERE token_hash=? AND revoked_at IS NULL`, time.Now().UTC().Format(time.RFC3339Nano), hash); err != nil {
		return domain.OAuthRefreshGrant{}, err
	}
	var grant domain.OAuthRefreshGrant
	if err := json.Unmarshal(raw, &grant); err != nil {
		return grant, err
	}
	if err := tx.Commit(); err != nil {
		return grant, err
	}
	return grant, nil
}

func (s *SQLiteStore) RevokeOAuthRefreshToken(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE oauth_refresh_tokens SET revoked_at=? WHERE token_hash=?`, time.Now().UTC().Format(time.RFC3339Nano), tokenHash(token))
	return err
}

func (s *SQLiteStore) FindAPIKey(secretHash string) (string, domain.APIKey, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for workspaceKey, data := range s.workspaces {
		for _, key := range data.APIKeys {
			if key.SecretHash == secretHash && key.RevokedAt == nil && (key.ExpiresAt == nil || time.Now().UTC().Before(*key.ExpiresAt)) {
				return workspaceKey, key, true
			}
		}
	}
	return "", domain.APIKey{}, false
}

func (s *SQLiteStore) UserByID(ctx context.Context, id string) (domain.User, error) {
	return s.authUserByID(ctx, id)
}

func IsOAuthNotFound(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}
