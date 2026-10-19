package github

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"flow/api/internal/codehost"
)

// parsePrivateKey reads the App's PEM private key (PKCS#1 as GitHub issues
// it, or PKCS#8). Literal "\n" sequences from single-line env values are
// accepted.
func parsePrivateKey(raw string) (*rsa.PrivateKey, error) {
	text := strings.TrimSpace(raw)
	if !strings.Contains(text, "\n") && strings.Contains(text, `\n`) {
		text = strings.ReplaceAll(text, `\n`, "\n")
	}
	block, _ := pem.Decode([]byte(text))
	if block == nil {
		return nil, errors.New("not a PEM private key")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("the GitHub App private key must be RSA")
	}
	return key, nil
}

// signAppJWT builds the App JWT: RS256, iat = now-60s (clock skew),
// exp = now+9m (GitHub's maximum is 10 minutes), iss = App id.
func signAppJWT(key *rsa.PrivateKey, appID string, now time.Time) (string, time.Time, error) {
	expires := now.Add(9 * time.Minute)
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, err := json.Marshal(map[string]any{"iat": now.Add(-60 * time.Second).Unix(), "exp": expires.Unix(), "iss": appID})
	if err != nil {
		return "", time.Time{}, err
	}
	unsigned := header + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", time.Time{}, err
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature), expires, nil
}

// appJWT returns a cached App JWT, re-signing a minute before it expires.
func (h *Host) appJWT() (string, error) {
	if err := h.ready(); err != nil {
		return "", err
	}
	now := h.now()
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.jwt != "" && now.Add(time.Minute).Before(h.jwtExpires) {
		return h.jwt, nil
	}
	token, expires, err := signAppJWT(h.key, h.cfg.AppID, now)
	if err != nil {
		return "", fmt.Errorf("github: sign App JWT: %w", err)
	}
	h.jwt, h.jwtExpires = token, expires
	return token, nil
}

// Permission sets for installation tokens (least privilege per purpose).
var (
	// permissionsSandboxWrite is what the runner's git and gh need.
	permissionsSandboxWrite = map[string]string{"contents": "write", "pull_requests": "write", "checks": "read", "actions": "read", "metadata": "read"}
	// permissionsSandboxRead clones and reads CI and comments only.
	permissionsSandboxRead = map[string]string{"contents": "read", "pull_requests": "read", "checks": "read", "actions": "read", "metadata": "read"}
	// permissionsFlow is what Flow itself uses: PRs, comments, CI, branch heads.
	permissionsFlow = map[string]string{"contents": "read", "pull_requests": "write", "checks": "read", "statuses": "read", "metadata": "read"}
	// permissionsMetadata reads repository metadata and collaborator permissions.
	permissionsMetadata = map[string]string{"metadata": "read"}
)

// tokenRequest names an installation token in the cache.
type tokenRequest struct {
	installationID string
	repository     string // repository name (without owner); "" = every repository of the installation
	permissions    map[string]string
	scope          string // separates tokens that must be revocable on their own (one per sandbox)
}

func (r tokenRequest) cacheKey() string {
	keys := make([]string, 0, len(r.permissions))
	for key := range r.permissions {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	var permissions strings.Builder
	for _, key := range keys {
		permissions.WriteString(key + "=" + r.permissions[key] + ",")
	}
	return r.installationID + "|" + strings.ToLower(r.repository) + "|" + permissions.String() + "|" + r.scope
}

// installationToken returns a cached installation token or mints one through
// POST /app/installations/{id}/access_tokens. Concurrent callers for the same
// key share one request.
func (h *Host) installationToken(ctx context.Context, request tokenRequest) (cachedToken, string, error) {
	key := request.cacheKey()
	for {
		h.mu.Lock()
		if cached, ok := h.tokens[key]; ok && h.now().Add(tokenRefreshMargin).Before(cached.expiresAt) {
			h.mu.Unlock()
			return cached, key, nil
		}
		if pending, ok := h.inflight[key]; ok {
			h.mu.Unlock()
			select {
			case <-pending.done:
			case <-ctx.Done():
				return cachedToken{}, key, fmt.Errorf("%w: %v", codehost.ErrTransient, ctx.Err())
			}
			if pending.err != nil {
				return cachedToken{}, key, pending.err
			}
			continue
		}
		pending := &inflightToken{done: make(chan struct{})}
		h.inflight[key] = pending
		h.mu.Unlock()

		pending.token, pending.err = h.mintInstallationToken(ctx, request)
		h.mu.Lock()
		delete(h.inflight, key)
		if pending.err == nil {
			h.tokens[key] = pending.token
			h.pruneTokensLocked()
		}
		h.mu.Unlock()
		close(pending.done)
		return pending.token, key, pending.err
	}
}

func (h *Host) pruneTokensLocked() {
	now := h.now()
	for key, item := range h.tokens {
		if !now.Before(item.expiresAt) {
			delete(h.tokens, key)
		}
	}
}

func (h *Host) mintInstallationToken(ctx context.Context, request tokenRequest) (cachedToken, error) {
	body := map[string]any{"permissions": request.permissions}
	if request.repository != "" {
		body["repositories"] = []string{request.repository}
	}
	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	err := h.appRequest(ctx, "mint_token", http.MethodPost, "/app/installations/"+pathEscape(request.installationID)+"/access_tokens", body, &out)
	switch {
	case errors.Is(err, codehost.ErrNotFound):
		return cachedToken{}, fmt.Errorf("%w: GitHub App installation %s no longer exists", codehost.ErrNoAccess, request.installationID)
	case isStatus(err, http.StatusUnprocessableEntity):
		// The repository is not part of the installation, or the App lacks
		// one of the requested permissions.
		return cachedToken{}, fmt.Errorf("%w: %v", codehost.ErrNoAccess, err)
	case err != nil:
		return cachedToken{}, err
	}
	if out.Token == "" {
		return cachedToken{}, fmt.Errorf("%w: GitHub returned an empty installation token", codehost.ErrTransient)
	}
	if out.ExpiresAt.IsZero() {
		out.ExpiresAt = h.now().Add(time.Hour)
	}
	return cachedToken{token: out.Token, expiresAt: out.ExpiresAt, installationID: request.installationID}, nil
}

// repoToken is the token Flow uses for its own API calls on a repository.
func (h *Host) repoToken(ctx context.Context, workspaceKey string, repo codehost.RepoRef, permissions map[string]string) (string, string, string, error) {
	owner, name, err := h.checkRepo(repo)
	if err != nil {
		return "", "", "", err
	}
	installationID, err := h.installationFor(ctx, workspaceKey, repo)
	if err != nil {
		return "", "", "", err
	}
	token, _, err := h.installationToken(ctx, tokenRequest{installationID: installationID, repository: name, permissions: permissions, scope: "flow"})
	if err != nil {
		return "", "", "", err
	}
	return token.token, owner, name, nil
}
