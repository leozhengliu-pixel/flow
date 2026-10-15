package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"flow/api/internal/domain"
)

// OAuthRefreshReplayGrace is how long a consumed refresh token may be
// replayed to retrieve the same successor (a client that lost the response
// to a network error retries the original request).
const OAuthRefreshReplayGrace = 30 * time.Minute

// A replay record lives in oauth_refresh_tokens under a hash derived from the
// consumed token, so only a holder of that token can find it. The successor
// is sealed with a key derived from the consumed token: database readers see
// neither. The row is written with revoked_at already set, so it can never be
// redeemed as a refresh token itself.
type oauthRefreshReplayRecord struct {
	Replay struct {
		Sealed string `json:"sealed"`
	} `json:"replay"`
}

func oauthReplayHash(token string) string { return tokenHash("flow-oauth-refresh-replay\x00" + token) }

func oauthReplayCipher(token string) (cipher.AEAD, error) {
	key := sha256.Sum256([]byte("flow-oauth-refresh-replay-key\x00" + token))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func storeOAuthRefreshReplay(ctx context.Context, tx *sqlTx, token, successor string, now time.Time) error {
	aead, err := oauthReplayCipher(token)
	if err != nil {
		return err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	hash := oauthReplayHash(token)
	var record oauthRefreshReplayRecord
	record.Replay.Sealed = base64.RawStdEncoding.EncodeToString(aead.Seal(nonce, nonce, []byte(successor), []byte(hash)))
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	stamp := now.Format(time.RFC3339Nano)
	_, err = tx.ExecContext(ctx, `INSERT INTO oauth_refresh_tokens(token_hash,data,expires_at,revoked_at,created_at) VALUES(?,?,?,?,?)`, hash, raw, now.Add(OAuthRefreshReplayGrace).Format(time.RFC3339Nano), stamp, stamp)
	return err
}

// oauthRefreshReplay returns the successor issued when token was consumed,
// provided the grace window is open and the successor is still unused.
func oauthRefreshReplay(ctx context.Context, tx *sqlTx, token, lock string) (string, bool, error) {
	hash := oauthReplayHash(token)
	var raw []byte
	var expiration string
	if err := tx.QueryRowContext(ctx, `SELECT data,expires_at FROM oauth_refresh_tokens WHERE token_hash=?`+lock, hash).Scan(&raw, &expiration); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	expires, err := time.Parse(time.RFC3339Nano, expiration)
	if err != nil || !time.Now().UTC().Before(expires) {
		return "", false, nil
	}
	var record oauthRefreshReplayRecord
	if json.Unmarshal(raw, &record) != nil || record.Replay.Sealed == "" {
		return "", false, nil
	}
	sealed, err := base64.RawStdEncoding.DecodeString(record.Replay.Sealed)
	if err != nil {
		return "", false, nil
	}
	aead, err := oauthReplayCipher(token)
	if err != nil {
		return "", false, err
	}
	if len(sealed) < aead.NonceSize() {
		return "", false, nil
	}
	plain, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], []byte(hash))
	if err != nil {
		return "", false, nil
	}
	successor := string(plain)
	var successorExpiration string
	var consumed sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT expires_at,revoked_at FROM oauth_refresh_tokens WHERE token_hash=?`+lock, tokenHash(successor)).Scan(&successorExpiration, &consumed); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	successorExpires, err := time.Parse(time.RFC3339Nano, successorExpiration)
	if err != nil || consumed.Valid || !time.Now().UTC().Before(successorExpires) {
		return "", false, nil
	}
	return successor, true, nil
}

// deleteOAuthRefreshReplay closes the grace window of a consumed token.
func (s *SQLiteStore) deleteOAuthRefreshReplay(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM oauth_refresh_tokens WHERE token_hash=?`, oauthReplayHash(token))
	return err
}

// OAuthRefreshTokenState describes a refresh token for revocation and RFC
// 7662 introspection.
type OAuthRefreshTokenState struct {
	Grant     domain.OAuthRefreshGrant
	CreatedAt time.Time
	ExpiresAt time.Time
	// Active is false once the token was consumed, revoked or expired.
	Active bool
}

// OAuthRefreshToken looks a refresh token up by its secret. Replay records are
// never returned.
func (s *SQLiteStore) OAuthRefreshToken(ctx context.Context, token string) (OAuthRefreshTokenState, bool, error) {
	var raw []byte
	var expiration, created string
	var revoked sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT data,expires_at,revoked_at,created_at FROM oauth_refresh_tokens WHERE token_hash=?`, tokenHash(token)).Scan(&raw, &expiration, &revoked, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return OAuthRefreshTokenState{}, false, nil
	}
	if err != nil {
		return OAuthRefreshTokenState{}, false, err
	}
	var state OAuthRefreshTokenState
	if err := json.Unmarshal(raw, &state.Grant); err != nil || state.Grant.ClientID == "" {
		return OAuthRefreshTokenState{}, false, nil
	}
	state.ExpiresAt, _ = time.Parse(time.RFC3339Nano, expiration)
	state.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	state.Active = !revoked.Valid && time.Now().UTC().Before(state.ExpiresAt)
	return state, true, nil
}

// OAuthClientSecret authenticates a registered (confidential) OAuth
// application. confidential is false for unknown and dynamically registered
// public clients; ok reports whether secret matched.
func (s *SQLiteStore) OAuthClientSecret(clientID, secret string) (confidential, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, data := range s.workspaces {
		for _, app := range data.OAuthApplications {
			if app.ClientID == clientID && app.ClientSecret != "" {
				return true, secret != "" && subtle.ConstantTimeCompare([]byte(secret), []byte(app.ClientSecret)) == 1
			}
		}
	}
	return false, false
}

// revokeOAuthAccessFamily revokes every live access token of one refresh
// chain. Other sessions of the same authorization keep working.
func (s *SQLiteStore) revokeOAuthAccessFamily(ctx context.Context, workspace, family string) error {
	var event domain.DomainEvent
	err := s.oauthMetadataTransaction(ctx, workspace, func(tx *sqlTx, lock string, current domain.Bootstrap) (domain.Bootstrap, error) {
		now := time.Now().UTC()
		keys := slices.Clone(current.APIKeys)
		changed := false
		for index := range keys {
			key := &keys[index]
			if key.RefreshFamilyID != family || key.RevokedAt != nil {
				continue
			}
			key.RevokedAt = &now
			raw, err := json.Marshal(key)
			if err != nil {
				return current, err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='apiKeys' AND record_key=?`, raw, workspace, key.ID); err != nil {
				return current, err
			}
			changed = true
		}
		if !changed {
			return current, nil
		}
		var err error
		event, err = oauthRecordEvent(ctx, tx, current.Workspace.ID, "oauth_token.revoked", family)
		if err != nil {
			return current, err
		}
		current.APIKeys = keys
		return current, nil
	})
	if errors.Is(err, ErrAuthForbidden) {
		// The workspace no longer exists: nothing left to revoke.
		return nil
	}
	if err == nil && event.ID != "" {
		s.publishOAuthRecordEvent(workspace, event)
	}
	return err
}
