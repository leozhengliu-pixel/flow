package gitlab

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"flow/api/internal/codehost"
)

// Shared project tokens (several Flow replicas).
//
// A project token is minted once per session and project and reused by
// every git operation. The value exists only in Flow's answer to GitLab's
// create/rotate call, so a replica that does not hold it can only rotate the
// token, and rotation revokes the old value at once. With per-process copies
// two replicas invalidated each other: one rotated (or revoked, at session
// stop or in the sweep) while the other kept answering with the old value,
// and git failed with "HTTP Basic: Access denied" (typically when a stopped
// session was continued in a new sandbox whose requests reached the other
// replica). With a shared store every replica serves the one current value,
// minting is serialized across replicas by TokenLocker, and revocation drops
// the stored value for all of them.

// SharedTokenStore keeps each owner's current project tokens where every
// replica reads them: Flow's encrypted connector secret store.
type SharedTokenStore interface {
	PutConnectorSecret(ctx context.Context, id string, data []byte, expires time.Time) error
	// ReadConnectorSecret returns sql.ErrNoRows for a missing or expired id.
	ReadConnectorSecret(ctx context.Context, id string, consume bool) ([]byte, error)
	DeleteConnectorSecret(ctx context.Context, id string) error
}

// TokenLocker runs action while holding a lock shared by all replicas
// (coordination.Redis.WithWorkspaceLock); nil with a single replica.
type TokenLocker func(ctx context.Context, name string, action func() error) error

// SetSharedTokens makes every replica serve project tokens from store,
// minting under lock. Without it tokens are served from process memory,
// which is only correct with one replica.
func (h *Host) SetSharedTokens(store SharedTokenStore, lock TokenLocker) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.shared, h.sharedLock = store, lock
}

func (h *Host) sharedTokens() (SharedTokenStore, TokenLocker) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.shared, h.sharedLock
}

// sharedRecord is one owner's (token name's) tokens, by connection and project.
type sharedRecord struct {
	Tokens []sharedToken `json:"tokens"`
}

type sharedToken struct {
	ConnectionID string    `json:"connectionId"`
	Project      string    `json:"project"`
	TokenID      int64     `json:"tokenId"`
	Token        string    `json:"token"`
	ExpiresAt    time.Time `json:"expiresAt"`
}

func (r *sharedRecord) find(connectionID, project string) *sharedToken {
	for i := range r.Tokens {
		if r.Tokens[i].ConnectionID == connectionID && r.Tokens[i].Project == project {
			return &r.Tokens[i]
		}
	}
	return nil
}

func (r *sharedRecord) set(token sharedToken) {
	if existing := r.find(token.ConnectionID, token.Project); existing != nil {
		*existing = token
		return
	}
	r.Tokens = append(r.Tokens, token)
}

func (r *sharedRecord) removeWhere(match func(sharedToken) bool) []sharedToken {
	kept, removed := r.Tokens[:0], []sharedToken{}
	for _, token := range r.Tokens {
		if match(token) {
			removed = append(removed, token)
		} else {
			kept = append(kept, token)
		}
	}
	r.Tokens = kept
	return removed
}

// sharedTokenID is the store id of an owner's record (no token name or
// workspace in clear).
func sharedTokenID(workspaceKey, name string) string {
	sum := sha256.Sum256([]byte(workspaceKey + "\x00" + name))
	return "gitlab-project-token|" + hex.EncodeToString(sum[:20])
}

// fingerprint is the redacted identity of a token value for logs.
func fingerprint(token string) string { return codehost.CredentialFingerprint(token)[:12] }

func sharedStoreError(action string, err error) error {
	log.Printf("coding sessions: shared GitLab project token store: %s: %v", action, err)
	return &Error{Kind: codehost.ErrTransient, Message: "Flow could not " + action + " the session's GitLab project token; try again."}
}

// withOwner runs action under the owner's process lock and, with several
// replicas, the shared lock.
func (h *Host) withOwner(ctx context.Context, workspaceKey, name string, action func() error) error {
	unlock := h.tokens.lock("shared\x00" + workspaceKey + "\x00" + name)
	defer unlock()
	_, lock := h.sharedTokens()
	if lock == nil {
		return action()
	}
	var actionErr error
	if err := lock(ctx, sharedTokenID(workspaceKey, name), func() error { actionErr = action(); return nil }); err != nil {
		return sharedStoreError("lock", err)
	}
	return actionErr
}

func (h *Host) readShared(ctx context.Context, store SharedTokenStore, id string) (sharedRecord, error) {
	var record sharedRecord
	raw, err := store.ReadConnectorSecret(ctx, id, false)
	if errors.Is(err, sql.ErrNoRows) {
		return record, nil
	}
	if err != nil {
		// Never mint on a failed read: a rotation would revoke the value the
		// other replicas are serving.
		return record, sharedStoreError("read", err)
	}
	if err := json.Unmarshal(raw, &record); err != nil {
		return sharedRecord{}, nil // unreadable: replaced by the next mint
	}
	return record, nil
}

func (h *Host) writeShared(ctx context.Context, store SharedTokenStore, id string, record sharedRecord) error {
	if len(record.Tokens) == 0 {
		if err := store.DeleteConnectorSecret(ctx, id); err != nil {
			return sharedStoreError("update", err)
		}
		return nil
	}
	expires := time.Time{}
	for _, token := range record.Tokens {
		if token.ExpiresAt.After(expires) {
			expires = token.ExpiresAt
		}
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if err := store.PutConnectorSecret(ctx, id, raw, expires); err != nil {
		return sharedStoreError("save", err)
	}
	return nil
}

// mintSharedProjectToken serves the owner's current token from the shared
// store, minting (create or rotate) under the shared lock only when there is
// none or it is about to expire.
func (h *Host) mintSharedProjectToken(ctx context.Context, store SharedTokenStore, connection Connection, request codehost.CredentialRequest, name, project, key string) (codehost.Credential, error) {
	var credential codehost.Credential
	var tokenID int64
	id := sharedTokenID(connection.WorkspaceKey, name)
	err := h.withOwner(ctx, connection.WorkspaceKey, name, func() error {
		record, err := h.readShared(ctx, store, id)
		if err != nil {
			return err
		}
		if current := record.find(connection.ID, project); current != nil && current.ExpiresAt.Sub(h.now()) > tokenRefreshMargin {
			credential = projectCredential(connection, project, name, current.TokenID, current.Token, current.ExpiresAt)
			tokenID = current.TokenID
			return nil
		}
		minted, expiresAt, err := h.createOrRotateProjectToken(ctx, connection, project, name)
		if err != nil {
			return err
		}
		record.set(sharedToken{ConnectionID: connection.ID, Project: project, TokenID: minted.ID, Token: minted.Token, ExpiresAt: expiresAt})
		if err := h.writeShared(ctx, store, id, record); err != nil {
			// No replica could reuse this value and the next mint would
			// rotate it away under a running git command: revoke it now.
			_ = h.revoke(ctx, connection.WorkspaceKey, connection.ID, project, minted.ID)
			return err
		}
		log.Printf("coding sessions: minted GitLab project token %s id=%d fp=%s project=%s", name, minted.ID, fingerprint(minted.Token), project)
		credential = projectCredential(connection, project, name, minted.ID, minted.Token, expiresAt)
		tokenID = minted.ID
		return nil
	})
	if err != nil {
		return codehost.Credential{}, err
	}
	h.tokens.put(key, &mintedToken{workspaceKey: connection.WorkspaceKey, connectionID: connection.ID, project: project, ownerID: tokenOwner(name), tokenID: tokenID, credential: credential}, request.Repo)
	return credential, nil
}

// forgetSharedToken drops a revoked token from its owner's shared record.
func (h *Host) forgetSharedToken(ctx context.Context, workspaceKey, name string, tokenID int64) error {
	store, _ := h.sharedTokens()
	if store == nil || name == "" {
		return nil
	}
	id := sharedTokenID(workspaceKey, name)
	return h.withOwner(ctx, workspaceKey, name, func() error {
		record, err := h.readShared(ctx, store, id)
		if err != nil {
			return err
		}
		if len(record.removeWhere(func(token sharedToken) bool { return token.TokenID == tokenID })) == 0 {
			return nil
		}
		return h.writeShared(ctx, store, id, record)
	})
}

// revokeSharedTokens revokes every token in the owner's shared record (minted
// by any replica) and deletes the record. skip lists ids already revoked.
func (h *Host) revokeSharedTokens(ctx context.Context, workspaceKey, name string, skip map[int64]bool) error {
	store, _ := h.sharedTokens()
	if store == nil {
		return nil
	}
	id := sharedTokenID(workspaceKey, name)
	return h.withOwner(ctx, workspaceKey, name, func() error {
		record, err := h.readShared(ctx, store, id)
		if err != nil || len(record.Tokens) == 0 {
			return err
		}
		var errs []error
		for _, token := range record.Tokens {
			if skip[token.TokenID] {
				continue
			}
			if err := h.revoke(ctx, workspaceKey, token.ConnectionID, token.Project, token.TokenID); err != nil {
				errs = append(errs, err)
			}
		}
		if len(errs) > 0 {
			return errors.Join(errs...) // keep the record: the sweep retries
		}
		return h.writeShared(ctx, store, id, sharedRecord{})
	})
}

// InvalidateCredential implements codehost.CredentialInvalidator: git
// reported that GitLab rejected the token with this fingerprint. The token
// is dropped (shared record and process copy) only when it is the owner's
// current token and GitLab confirms it is no longer active, so the next
// request mints a fresh one; a token GitLab still reports active is kept
// (the rejection was about access, not a stale value).
func (h *Host) InvalidateCredential(ctx context.Context, request codehost.CredentialRequest, fingerprintHex string) (bool, error) {
	fingerprintHex = strings.ToLower(strings.TrimSpace(fingerprintHex))
	if h.CredentialMode() != ModeProjectToken || len(fingerprintHex) != 64 {
		return false, nil
	}
	connection, err := h.connection(ctx, request.WorkspaceKey, request.Repo.HostName)
	if err != nil {
		return false, err
	}
	name := TokenName(request)
	if name == SandboxTokenPrefix {
		return false, nil
	}
	project := projectPath(request.Repo)
	key := tokenKey(connection.WorkspaceKey, connection.ID, project, name)
	matches := func(token string) bool { return token != "" && codehost.CredentialFingerprint(token) == fingerprintHex }
	if store, _ := h.sharedTokens(); store != nil {
		dropped := false
		id := sharedTokenID(connection.WorkspaceKey, name)
		err := h.withOwner(ctx, connection.WorkspaceKey, name, func() error {
			record, err := h.readShared(ctx, store, id)
			if err != nil {
				return err
			}
			current := record.find(connection.ID, project)
			if current == nil || !matches(current.Token) {
				return nil
			}
			active, err := h.projectTokenActive(ctx, connection, project, current.TokenID)
			if err != nil || active {
				return err
			}
			record.removeWhere(func(token sharedToken) bool { return token.TokenID == current.TokenID })
			if err := h.writeShared(ctx, store, id, record); err != nil {
				return err
			}
			log.Printf("coding sessions: dropped rejected GitLab project token %s id=%d fp=%s (no longer active)", name, current.TokenID, fingerprintHex[:12])
			dropped = true
			return nil
		})
		if dropped {
			h.tokens.drop(key)
		}
		return dropped, err
	}
	unlock := h.tokens.lock(key)
	defer unlock()
	cached := h.tokens.get(key)
	if cached == nil || !matches(cached.credential.Password) {
		return false, nil
	}
	active, err := h.projectTokenActive(ctx, connection, project, cached.tokenID)
	if err != nil || active {
		return false, err
	}
	h.tokens.drop(key)
	log.Printf("coding sessions: dropped rejected GitLab project token %s id=%d fp=%s (no longer active)", name, cached.tokenID, fingerprintHex[:12])
	return true, nil
}

// projectTokenActive asks GitLab (with the connection token) whether a
// project token is still active.
func (h *Host) projectTokenActive(ctx context.Context, connection Connection, project string, tokenID int64) (bool, error) {
	client, err := h.client(ctx, connection)
	if err != nil {
		return false, err
	}
	var token apiAccessToken
	if _, err := client.do(ctx, http.MethodGet, fmt.Sprintf("/projects/%s/access_tokens/%d", project, tokenID), nil, &token); err != nil {
		if errors.Is(err, codehost.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return token.Active && !token.Revoked, nil
}

var _ codehost.CredentialInvalidator = (*Host)(nil)
