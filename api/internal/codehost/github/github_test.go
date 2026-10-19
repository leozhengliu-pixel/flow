package github

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/codehost"
	"flow/api/internal/config"
	"flow/api/internal/domain"
)

var testKey = func() *rsa.PrivateKey {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return key
}()

func pkcs1PEM() string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(testKey)}))
}

// fakeInstallations implements codehost.Installations.
type fakeInstallations struct {
	byWorkspace map[string][]domain.CodeHostInstallation
}

func (f *fakeInstallations) CodeHostInstallations(_ context.Context, workspaceKey, _ string) ([]domain.CodeHostInstallation, error) {
	return f.byWorkspace[workspaceKey], nil
}
func (f *fakeInstallations) WorkspacesForInstallation(context.Context, string, string, string) ([]string, error) {
	return nil, nil
}
func (f *fakeInstallations) IntegrationConnections(context.Context, string, string) ([]domain.IntegrationConnection, error) {
	return nil, nil
}

type fakeSecrets map[string][]byte

func (f fakeSecrets) ReadConnectorSecret(_ context.Context, id string, _ bool) ([]byte, error) {
	if value, ok := f[id]; ok {
		return value, nil
	}
	return nil, sql.ErrNoRows
}

// fakeGitHub is an httptest GitHub API: it verifies the App JWT, issues
// numbered installation tokens and serves a small PR store.
type fakeGitHub struct {
	t        *testing.T
	server   *httptest.Server
	mu       sync.Mutex
	mints    []map[string]any
	revoked  []string
	pulls    []map[string]any
	posts    int
	tokenTTL time.Duration
	now      func() time.Time
	// overrides by "METHOD path" → status and body
	override map[string]func(w http.ResponseWriter, r *http.Request) bool
	// repoInstallation maps owner/name → installation id (absent → 404).
	repoInstallation map[string]int64
	permission       map[string]string
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	f := &fakeGitHub{t: t, tokenTTL: time.Hour, now: time.Now, override: map[string]func(http.ResponseWriter, *http.Request) bool{},
		repoInstallation: map[string]int64{"acme/app": 42, "acme/other": 99}, permission: map[string]string{"alice": "write", "bob": "read"}}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

func writeJSONResponse(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (f *fakeGitHub) verifyJWT(r *http.Request) bool {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return false
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return false
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(&testKey.PublicKey, crypto.SHA256, digest[:], signature) != nil {
		return false
	}
	raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var claims struct {
		Iat, Exp int64
		Iss      string
	}
	if json.Unmarshal(raw, &claims) != nil || claims.Iss != "123" || claims.Exp-claims.Iat > 600 {
		return false
	}
	return true
}

func (f *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	override := f.override[r.Method+" "+r.URL.Path]
	f.mu.Unlock()
	if override != nil && override(w, r) {
		return
	}
	path := r.URL.Path
	switch {
	case strings.HasPrefix(path, "/app/") || strings.HasSuffix(path, "/installation") && strings.HasPrefix(path, "/repos/"):
		if !f.verifyJWT(r) {
			writeJSONResponse(w, http.StatusUnauthorized, map[string]string{"message": "bad JWT"})
			return
		}
	case path == "/login/oauth/access_token":
	default:
		if !strings.HasPrefix(r.Header.Get("Authorization"), "token ghs_") && !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer gho_") {
			writeJSONResponse(w, http.StatusUnauthorized, map[string]string{"message": "Bad credentials"})
			return
		}
	}
	switch {
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/repos/") && strings.HasSuffix(path, "/installation"):
		repo := strings.TrimSuffix(strings.TrimPrefix(path, "/repos/"), "/installation")
		id, ok := f.repoInstallation[strings.ToLower(repo)]
		if !ok {
			writeJSONResponse(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
			return
		}
		writeJSONResponse(w, http.StatusOK, map[string]any{"id": id})
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/access_tokens"):
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.mints = append(f.mints, body)
		n := len(f.mints)
		f.mu.Unlock()
		writeJSONResponse(w, http.StatusCreated, map[string]any{"token": fmt.Sprintf("ghs_%d", n), "expires_at": f.now().Add(f.tokenTTL).UTC().Format(time.RFC3339)})
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/app/installations/"):
		writeJSONResponse(w, http.StatusOK, map[string]any{"id": 42, "account": map[string]string{"login": "acme", "type": "Organization"}, "repository_selection": "selected"})
	case r.Method == http.MethodDelete && path == "/installation/token":
		f.mu.Lock()
		f.revoked = append(f.revoked, r.Header.Get("Authorization"))
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodGet && path == "/installation/repositories":
		page := r.URL.Query().Get("page")
		repos := []map[string]any{{"id": 1, "full_name": "acme/app", "default_branch": "main", "clone_url": "https://github.com/acme/app.git", "html_url": "https://github.com/acme/app", "private": true}}
		if page == "2" {
			repos = []map[string]any{{"id": 2, "full_name": "acme/web", "default_branch": "trunk"}}
		}
		writeJSONResponse(w, http.StatusOK, map[string]any{"total_count": 2, "repositories": repos})
	case r.Method == http.MethodGet && strings.Contains(path, "/collaborators/"):
		login := strings.TrimSuffix(path[strings.LastIndex(path, "/collaborators/")+len("/collaborators/"):], "/permission")
		permission, ok := f.permission[login]
		if !ok {
			writeJSONResponse(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
			return
		}
		writeJSONResponse(w, http.StatusOK, map[string]any{"permission": permission})
	case r.Method == http.MethodGet && strings.Contains(path, "/git/ref/heads/"):
		branch := path[strings.Index(path, "/git/ref/heads/")+len("/git/ref/heads/"):]
		if branch == "missing" {
			writeJSONResponse(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
			return
		}
		writeJSONResponse(w, http.StatusOK, map[string]any{"ref": "refs/heads/" + branch, "object": map[string]string{"sha": "sha-" + branch}})
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/pulls"):
		head := r.URL.Query().Get("head")
		f.mu.Lock()
		result := []map[string]any{}
		for _, pull := range f.pulls {
			if "acme:"+pull["head"].(map[string]any)["ref"].(string) == head && pull["state"] == r.URL.Query().Get("state") {
				result = append(result, pull)
			}
		}
		f.mu.Unlock()
		writeJSONResponse(w, http.StatusOK, result)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/pulls"):
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.posts++
		number := 10 + len(f.pulls)
		pull := map[string]any{"id": 1000 + number, "number": number, "html_url": fmt.Sprintf("https://github.com/acme/app/pull/%d", number), "title": body["title"], "body": body["body"], "state": "open", "draft": body["draft"],
			"head": map[string]any{"ref": body["head"], "sha": "abc"}, "base": map[string]any{"ref": body["base"], "repo": map[string]any{"full_name": "acme/app"}}, "user": map[string]any{"login": "flow[bot]"}}
		f.pulls = append(f.pulls, pull)
		f.mu.Unlock()
		writeJSONResponse(w, http.StatusCreated, pull)
	case r.Method == http.MethodGet && strings.Contains(path, "/pulls/"):
		number := path[strings.LastIndex(path, "/")+1:]
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, pull := range f.pulls {
			if fmt.Sprint(pull["number"]) == number {
				writeJSONResponse(w, http.StatusOK, pull)
				return
			}
		}
		writeJSONResponse(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
	case r.Method == http.MethodPatch && strings.Contains(path, "/pulls/"):
		w.WriteHeader(http.StatusOK)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/comments"):
		writeJSONResponse(w, http.StatusCreated, map[string]any{"id": 1})
	case r.Method == http.MethodPost && path == "/login/oauth/access_token":
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["code"] != "good" || body["client_secret"] != "csecret" {
			writeJSONResponse(w, http.StatusOK, map[string]string{"error": "bad_verification_code"})
			return
		}
		writeJSONResponse(w, http.StatusOK, map[string]string{"access_token": "gho_user"})
	case r.Method == http.MethodGet && path == "/user":
		writeJSONResponse(w, http.StatusOK, map[string]any{"login": "alice", "id": 7})
	default:
		writeJSONResponse(w, http.StatusNotFound, map[string]string{"message": "Not Found " + r.Method + " " + path})
	}
}

func (f *fakeGitHub) setOverride(key string, handler func(w http.ResponseWriter, r *http.Request) bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.override[key] = handler
}

func (f *fakeGitHub) mintCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.mints)
}

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time          { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *testClock) Advance(d time.Duration) { c.mu.Lock(); c.now = c.now.Add(d); c.mu.Unlock() }

func newTestHost(t *testing.T, fake *fakeGitHub, installations map[string][]domain.CodeHostInstallation) (*Host, *testClock) {
	t.Helper()
	clock := &testClock{now: time.Now().UTC()}
	fake.now = clock.Now
	cfg := config.CodingConfig{GitHubApp: config.GitHubAppConfig{AppID: "123", Slug: "flow-dev", ClientID: "cid", ClientSecret: "csecret", PrivateKey: pkcs1PEM(), WebhookSecret: "whsec", Host: "github.com", APIURL: fake.server.URL}}
	if installations == nil {
		installations = map[string][]domain.CodeHostInstallation{"ws": {{WorkspaceKey: "ws", CodeHost: "github", HostName: "github.com", InstallationID: "42", AccountLogin: "acme"}}}
	}
	built, err := New(codehost.Dependencies{Config: cfg, HTTPClient: fake.server.Client(), Installations: &fakeInstallations{byWorkspace: installations}, Secrets: fakeSecrets{}, Now: clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	host := built.(*Host)
	host.webURL = fake.server.URL
	if !host.Configured() {
		t.Fatal("host must be configured")
	}
	return host, clock
}

var appRepo = codehost.RepoRef{CodeHost: "github", HostName: "github.com", FullName: "acme/app"}

func TestParsePrivateKey(t *testing.T) {
	pkcs8, _ := x509.MarshalPKCS8PrivateKey(testKey)
	pkcs8PEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8}))
	for name, raw := range map[string]string{"pkcs1": pkcs1PEM(), "pkcs8": pkcs8PEM, "escaped": strings.ReplaceAll(strings.TrimSpace(pkcs1PEM()), "\n", `\n`)} {
		key, err := parsePrivateKey(raw)
		if err != nil || !key.Equal(testKey) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := parsePrivateKey("not a key"); err == nil {
		t.Error("garbage must be rejected")
	}
	built, err := New(codehost.Dependencies{Config: config.CodingConfig{GitHubApp: config.GitHubAppConfig{AppID: "1", PrivateKey: "bad", WebhookSecret: "x"}}})
	if err != nil {
		t.Fatalf("a bad key must not fail construction: %v", err)
	}
	_, err = built.MintCredential(t.Context(), codehost.CredentialRequest{WorkspaceKey: "ws", Repo: appRepo})
	if codehost.ErrorCode(domain.CodeHostGitHub, err) != domain.CodingErrNoGitHubConnection {
		t.Fatalf("bad key → noGitHubConnection, got %v", err)
	}
}

func TestAppJWT(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	token, expires, err := signAppJWT(testKey, "123", now)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT: %s", token)
	}
	header, _ := base64.RawURLEncoding.DecodeString(parts[0])
	claims, _ := base64.RawURLEncoding.DecodeString(parts[1])
	if string(header) != `{"alg":"RS256","typ":"JWT"}` {
		t.Fatalf("header %s", header)
	}
	var decoded map[string]any
	_ = json.Unmarshal(claims, &decoded)
	if decoded["iss"] != "123" || int64(decoded["iat"].(float64)) != now.Unix()-60 || int64(decoded["exp"].(float64)) != now.Add(9*time.Minute).Unix() || !expires.Equal(now.Add(9*time.Minute)) {
		t.Fatalf("claims %v", decoded)
	}
	signature, _ := base64.RawURLEncoding.DecodeString(parts[2])
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(&testKey.PublicKey, crypto.SHA256, digest[:], signature); err != nil {
		t.Fatalf("signature: %v", err)
	}

	fake := newFakeGitHub(t)
	host, clock := newTestHost(t, fake, nil)
	first, _ := host.appJWT()
	clock.Advance(7 * time.Minute)
	if again, _ := host.appJWT(); again != first {
		t.Fatal("JWT must be reused while it has more than a minute left")
	}
	clock.Advance(90 * time.Second)
	if renewed, _ := host.appJWT(); renewed == first {
		t.Fatal("JWT must be re-signed near expiry")
	}
	installation, err := host.Installation(t.Context(), "42")
	if err != nil || installation.AccountLogin != "acme" || installation.InstallationID != "42" || installation.HostName != "github.com" {
		t.Fatalf("installation: %+v %v", installation, err)
	}
	if _, err := host.Installation(t.Context(), "42/../x"); err == nil {
		t.Fatal("non-numeric installation ids must be rejected")
	}
}

func TestMintCredentialScopesAndCaches(t *testing.T) {
	fake := newFakeGitHub(t)
	host, clock := newTestHost(t, fake, nil)
	ctx := t.Context()
	request := codehost.CredentialRequest{WorkspaceKey: "ws", SessionID: "s1", SandboxID: "sb1", Repo: appRepo, Access: codehost.AccessWrite}
	credential, err := host.MintCredential(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if credential.Username != "x-access-token" || credential.Password != "ghs_1" || credential.CLIEnv["GH_TOKEN"] != "ghs_1" || credential.CLIEnv["GH_HOST"] != "github.com" || credential.RevokeID == "" {
		t.Fatalf("credential: %+v", credential)
	}
	mint := fake.mints[0]
	if repos := mint["repositories"].([]any); len(repos) != 1 || repos[0] != "app" {
		t.Fatalf("token must be scoped to the one repository: %v", mint)
	}
	permissions := mint["permissions"].(map[string]any)
	want := map[string]any{"contents": "write", "pull_requests": "write", "checks": "read", "actions": "read", "metadata": "read"}
	if fmt.Sprint(permissions) != fmt.Sprint(want) {
		t.Fatalf("permissions %v", permissions)
	}
	// Cached for the same sandbox, until 5 minutes before expiry.
	if again, _ := host.MintCredential(ctx, request); again.Password != "ghs_1" || fake.mintCount() != 1 {
		t.Fatalf("expected cached token, mints=%d", fake.mintCount())
	}
	// Another sandbox gets its own token (revocable on its own).
	other := request
	other.SandboxID = "sb2"
	if second, _ := host.MintCredential(ctx, other); second.Password != "ghs_2" {
		t.Fatalf("second sandbox: %+v", second)
	}
	// Read access asks for read-only permissions.
	read := request
	read.Access = codehost.AccessRead
	if _, err := host.MintCredential(ctx, read); err != nil {
		t.Fatal(err)
	}
	if fake.mints[2]["permissions"].(map[string]any)["contents"] != "read" {
		t.Fatalf("read permissions: %v", fake.mints[2])
	}
	clock.Advance(56 * time.Minute)
	if refreshed, _ := host.MintCredential(ctx, request); refreshed.Password == "ghs_1" {
		t.Fatal("a token within 5 minutes of expiry must be refreshed")
	}
	// Concurrent callers share one mint.
	before := fake.mintCount()
	concurrent := request
	concurrent.SandboxID = "sb-concurrent"
	var wg sync.WaitGroup
	var failures atomic.Int32
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := host.MintCredential(ctx, concurrent); err != nil {
				failures.Add(1)
			}
		}()
	}
	wg.Wait()
	if failures.Load() != 0 || fake.mintCount() != before+1 {
		t.Fatalf("concurrent mints: %d (failures %d)", fake.mintCount()-before, failures.Load())
	}
	// Revocation deletes the token at GitHub and drops it from the cache.
	if err := host.RevokeCredential(ctx, "ws", credential); err != nil {
		t.Fatal(err)
	}
	if len(fake.revoked) != 1 || fake.revoked[0] != "token ghs_1" {
		t.Fatalf("revoked %v", fake.revoked)
	}
}

func TestMintCredentialGHEEnv(t *testing.T) {
	fake := newFakeGitHub(t)
	cfg := config.CodingConfig{GitHubApp: config.GitHubAppConfig{AppID: "123", PrivateKey: pkcs1PEM(), WebhookSecret: "whsec", Host: "ghe.acme.dev", APIURL: fake.server.URL}}
	built, _ := New(codehost.Dependencies{Config: cfg, HTTPClient: fake.server.Client(), Installations: &fakeInstallations{byWorkspace: map[string][]domain.CodeHostInstallation{"ws": {{InstallationID: "42", HostName: "ghe.acme.dev"}}}}})
	credential, err := built.MintCredential(t.Context(), codehost.CredentialRequest{WorkspaceKey: "ws", SandboxID: "sb", Repo: codehost.RepoRef{HostName: "ghe.acme.dev", FullName: "acme/app"}, Access: codehost.AccessWrite})
	if err != nil {
		t.Fatal(err)
	}
	if credential.CLIEnv["GH_HOST"] != "ghe.acme.dev" || credential.CLIEnv["GH_ENTERPRISE_TOKEN"] == "" {
		t.Fatalf("GHE env: %v", credential.CLIEnv)
	}
	hosts, _ := built.EgressHosts(t.Context(), "ws")
	if len(hosts) != 2 || hosts[0] != "ghe.acme.dev" {
		t.Fatalf("GHE egress (host plus API host): %v", hosts)
	}
	if _, err := built.MintCredential(t.Context(), codehost.CredentialRequest{WorkspaceKey: "ws", Repo: appRepo}); !errors.Is(err, codehost.ErrNotConnected) {
		t.Fatalf("a github.com repo on a GHE App is not connected: %v", err)
	}
}

func TestErrorCodeMapping(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name          string
		installations map[string][]domain.CodeHostInstallation
		repo          codehost.RepoRef
		setup         func(f *fakeGitHub)
		want          domain.CodingErrorCode
	}{
		{name: "no installation", installations: map[string][]domain.CodeHostInstallation{}, repo: appRepo, want: domain.CodingErrNoGitHubConnection},
		{name: "repo not installed", repo: codehost.RepoRef{FullName: "acme/private"}, want: domain.CodingErrNoCodeAccess},
		{name: "installation of another workspace", repo: codehost.RepoRef{FullName: "acme/other"}, want: domain.CodingErrNoCodeAccess},
		{name: "suspended", installations: map[string][]domain.CodeHostInstallation{"ws": {{InstallationID: "42", SuspendedAt: &now}}}, repo: appRepo, want: domain.CodingErrNoCodeAccess},
		{name: "installation deleted", repo: appRepo, setup: func(f *fakeGitHub) {
			f.setOverride("POST /app/installations/42/access_tokens", func(w http.ResponseWriter, _ *http.Request) bool {
				writeJSONResponse(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
				return true
			})
		}, want: domain.CodingErrNoCodeAccess},
		{name: "repo not in selected repositories", repo: appRepo, setup: func(f *fakeGitHub) {
			f.setOverride("POST /app/installations/42/access_tokens", func(w http.ResponseWriter, _ *http.Request) bool {
				writeJSONResponse(w, http.StatusUnprocessableEntity, map[string]string{"message": "There is at least one repository that does not exist or is not accessible"})
				return true
			})
		}, want: domain.CodingErrNoCodeAccess},
		{name: "server error", repo: appRepo, setup: func(f *fakeGitHub) {
			f.setOverride("POST /app/installations/42/access_tokens", func(w http.ResponseWriter, _ *http.Request) bool {
				writeJSONResponse(w, http.StatusBadGateway, map[string]string{"message": "bad gateway"})
				return true
			})
		}, want: domain.CodingErrGitHubTransientError},
		{name: "rate limited", repo: appRepo, setup: func(f *fakeGitHub) {
			f.setOverride("GET /repos/acme/app/installation", func(w http.ResponseWriter, _ *http.Request) bool {
				w.Header().Set("X-RateLimit-Remaining", "0")
				writeJSONResponse(w, http.StatusForbidden, map[string]string{"message": "API rate limit exceeded"})
				return true
			})
		}, want: domain.CodingErrGitHubTransientError},
		{name: "other host", repo: codehost.RepoRef{HostName: "ghe.other.dev", FullName: "acme/app"}, want: domain.CodingErrNoGitHubConnection},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake := newFakeGitHub(t)
			if c.setup != nil {
				c.setup(fake)
			}
			host, _ := newTestHost(t, fake, c.installations)
			_, err := host.MintCredential(t.Context(), codehost.CredentialRequest{WorkspaceKey: "ws", SandboxID: "sb", Repo: c.repo, Access: codehost.AccessWrite})
			if got := codehost.ErrorCode(domain.CodeHostGitHub, err); got != c.want {
				t.Fatalf("got %s (%v), want %s", got, err, c.want)
			}
		})
	}
	// Transport failures are transient too.
	fake := newFakeGitHub(t)
	host, _ := newTestHost(t, fake, nil)
	fake.server.Close()
	_, err := host.MintCredential(t.Context(), codehost.CredentialRequest{WorkspaceKey: "ws", SandboxID: "sb", Repo: appRepo})
	if codehost.ErrorCode(domain.CodeHostGitHub, err) != domain.CodingErrGitHubTransientError {
		t.Fatalf("network error: %v", err)
	}
}

func TestCheckUserAccess(t *testing.T) {
	fake := newFakeGitHub(t)
	host, _ := newTestHost(t, fake, nil)
	ctx := t.Context()
	if err := host.CheckUserAccess(ctx, "ws", appRepo, codehost.UserIdentity{Login: "alice"}); err != nil {
		t.Fatalf("write access: %v", err)
	}
	for _, login := range []string{"bob", "mallory"} {
		err := host.CheckUserAccess(ctx, "ws", appRepo, codehost.UserIdentity{Login: login})
		if codehost.ErrorCode(domain.CodeHostGitHub, err) != domain.CodingErrPersonalAccessDenied {
			t.Fatalf("%s → personalAccessDenied, got %v", login, err)
		}
	}
	err := host.CheckUserAccess(ctx, "ws", appRepo, codehost.UserIdentity{FlowUserID: "u1"})
	if codehost.ErrorCode(domain.CodeHostGitHub, err) != domain.CodingErrRequiresGitHubConnection {
		t.Fatalf("unlinked user → requiresGithubConnection, got %v", err)
	}
	link, _ := json.Marshal(UserLink{Login: "alice", ID: 7})
	host.deps.Secrets = fakeSecrets{UserLinkSecretID("github.com", "u1"): link}
	if err := host.CheckUserAccess(ctx, "ws", appRepo, codehost.UserIdentity{FlowUserID: "u1"}); err != nil {
		t.Fatalf("linked user: %v", err)
	}
}

func TestUserAuthorization(t *testing.T) {
	fake := newFakeGitHub(t)
	host, _ := newTestHost(t, fake, nil)
	if !strings.Contains(host.AuthorizeURL("st", "https://flow/cb"), "client_id=cid") {
		t.Fatal("authorize URL needs the client id")
	}
	if got := host.InstallURL("a b"); got != fake.server.URL+"/apps/flow-dev/installations/new?state=a+b" {
		t.Fatalf("install URL %s", got)
	}
	link, err := host.ExchangeUserCode(t.Context(), "good", "")
	if err != nil || link.Login != "alice" || link.ID != 7 || link.HostName != "github.com" {
		t.Fatalf("link %+v %v", link, err)
	}
	if _, err := host.ExchangeUserCode(t.Context(), "bad", ""); err == nil {
		t.Fatal("bad code must fail")
	}
}

func TestCreateChangeRequestIsIdempotent(t *testing.T) {
	fake := newFakeGitHub(t)
	host, _ := newTestHost(t, fake, nil)
	ctx := t.Context()
	input := codehost.CreateChangeRequestInput{Repo: appRepo, Head: "flow/fix-1", Base: "main", Title: "Fix bug", Body: "Fixes ENG-1", Draft: true}
	first, err := host.CreateChangeRequest(ctx, "ws", input)
	if err != nil {
		t.Fatal(err)
	}
	if first.Number != 10 || first.State != "open" || !first.Draft || first.HeadBranch != "flow/fix-1" || first.Repo.FullName != "acme/app" || first.ExternalID != "1010" {
		t.Fatalf("created %+v", first)
	}
	second, err := host.CreateChangeRequest(ctx, "ws", input)
	if err != nil || second.Number != first.Number || fake.posts != 1 {
		t.Fatalf("second create must return the open PR: %+v posts=%d err=%v", second, fake.posts, err)
	}
	if got, err := host.GetChangeRequest(ctx, "ws", appRepo, 10); err != nil || got.Body != "Fixes ENG-1" || got.Number != 10 {
		t.Fatalf("get must return the body: %+v %v", got, err)
	}
	found, err := host.FindChangeRequest(ctx, "ws", appRepo, "flow/fix-1")
	if err != nil || found == nil || found.Number != 10 || found.Body != "Fixes ENG-1" {
		t.Fatalf("find %+v %v", found, err)
	}
	if none, err := host.FindChangeRequest(ctx, "ws", appRepo, "flow/other"); err != nil || none != nil {
		t.Fatalf("no PR expected: %+v %v", none, err)
	}
	// A closed PR is not reused: a new one is opened.
	fake.mu.Lock()
	fake.pulls[0]["state"] = "closed"
	fake.mu.Unlock()
	reopened, err := host.CreateChangeRequest(ctx, "ws", input)
	if err != nil || reopened.Number == first.Number || fake.posts != 2 {
		t.Fatalf("closed PR must lead to a new one: %+v %v", reopened, err)
	}
	if err := host.UpdateChangeRequestBody(ctx, "ws", appRepo, reopened.Number, "new body"); err != nil {
		t.Fatal(err)
	}
	if err := host.Comment(ctx, "ws", appRepo, reopened.Number, "hi"); err != nil {
		t.Fatal(err)
	}
}

func TestCreateChangeRequestRaceAndDraftFallback(t *testing.T) {
	fake := newFakeGitHub(t)
	host, _ := newTestHost(t, fake, nil)
	ctx := t.Context()
	// Another replica created the PR between our lookup and our POST.
	fake.setOverride("POST /repos/acme/app/pulls", func(w http.ResponseWriter, _ *http.Request) bool {
		fake.mu.Lock()
		fake.pulls = append(fake.pulls, map[string]any{"id": 5, "number": 5, "state": "open", "title": "x", "head": map[string]any{"ref": "flow/race"}, "base": map[string]any{"ref": "main"}})
		fake.mu.Unlock()
		writeJSONResponse(w, http.StatusUnprocessableEntity, map[string]any{"message": "Validation Failed", "errors": []map[string]string{{"message": "A pull request already exists for acme:flow/race."}}})
		return true
	})
	got, err := host.CreateChangeRequest(ctx, "ws", codehost.CreateChangeRequestInput{Repo: appRepo, Head: "flow/race", Base: "main", Title: "x", Draft: true})
	if err != nil || got.Number != 5 {
		t.Fatalf("race must resolve to the existing PR: %+v %v", got, err)
	}
	// Draft PRs unsupported: retried as a regular PR.
	var drafts []any
	fake.setOverride("POST /repos/acme/app/pulls", func(w http.ResponseWriter, r *http.Request) bool {
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		drafts = append(drafts, body["draft"])
		if body["draft"] == true {
			writeJSONResponse(w, http.StatusUnprocessableEntity, map[string]string{"message": "Draft pull requests are not supported in this repository."})
			return true
		}
		writeJSONResponse(w, http.StatusCreated, map[string]any{"id": 9, "number": 9, "state": "open", "head": map[string]any{"ref": "flow/nodraft"}, "base": map[string]any{"ref": "main"}})
		return true
	})
	got, err = host.CreateChangeRequest(ctx, "ws", codehost.CreateChangeRequestInput{Repo: appRepo, Head: "flow/nodraft", Base: "main", Title: "y", Draft: true})
	if err != nil || got.Number != 9 || len(drafts) != 2 || drafts[1] != false {
		t.Fatalf("draft fallback: %+v %v %v", got, err, drafts)
	}
	// Validation errors are not transient.
	fake.setOverride("POST /repos/acme/app/pulls", func(w http.ResponseWriter, _ *http.Request) bool {
		writeJSONResponse(w, http.StatusUnprocessableEntity, map[string]string{"message": "No commits between main and flow/empty"})
		return true
	})
	_, err = host.CreateChangeRequest(ctx, "ws", codehost.CreateChangeRequestInput{Repo: appRepo, Head: "flow/empty", Base: "main", Title: "z"})
	if err == nil || errors.Is(err, codehost.ErrTransient) || codehost.ErrorCode(domain.CodeHostGitHub, err) != domain.CodingErrInternal {
		t.Fatalf("validation error: %v", err)
	}
}

func TestBranchHeadAndRepositories(t *testing.T) {
	fake := newFakeGitHub(t)
	host, _ := newTestHost(t, fake, nil)
	ctx := t.Context()
	if sha, err := host.BranchHead(ctx, "ws", appRepo, "flow/fix-1"); err != nil || sha != "sha-flow/fix-1" {
		t.Fatalf("branch head %q %v", sha, err)
	}
	if _, err := host.BranchHead(ctx, "ws", appRepo, "missing"); !errors.Is(err, codehost.ErrNotFound) {
		t.Fatalf("missing branch: %v", err)
	}
	page, err := host.ListRepositories(ctx, "ws", codehost.ListOptions{Limit: 1})
	if err != nil || len(page.Repositories) != 1 || page.Repositories[0].FullName != "acme/app" || page.Repositories[0].InstallationID != "42" || page.NextCursor != "0:2" || !page.Repositories[0].Private {
		t.Fatalf("page 1: %+v %v", page, err)
	}
	page, err = host.ListRepositories(ctx, "ws", codehost.ListOptions{Limit: 1, Cursor: page.NextCursor})
	if err != nil || len(page.Repositories) != 1 || page.Repositories[0].DefaultBranch != "trunk" || page.NextCursor != "" {
		t.Fatalf("page 2: %+v %v", page, err)
	}
	if page, _ := host.ListRepositories(ctx, "ws", codehost.ListOptions{Query: "nothing"}); len(page.Repositories) != 0 {
		t.Fatalf("query filter: %+v", page)
	}
	if _, err := host.ListRepositories(ctx, "ws", codehost.ListOptions{Cursor: "x"}); err == nil {
		t.Fatal("bad cursor must fail")
	}
	if _, err := host.ListRepositories(ctx, "nobody", codehost.ListOptions{}); !errors.Is(err, codehost.ErrNotConnected) {
		t.Fatalf("no installation: %v", err)
	}
	fake.setOverride("GET /repos/acme/app", func(w http.ResponseWriter, _ *http.Request) bool {
		writeJSONResponse(w, http.StatusOK, map[string]any{"id": 1, "full_name": "acme/app", "default_branch": "main", "clone_url": "https://github.com/acme/app.git"})
		return true
	})
	repo, err := host.ResolveRepository(ctx, "ws", appRepo)
	if err != nil || repo.DefaultBranch != "main" || repo.CloneURL == "" || repo.ID != "1" {
		t.Fatalf("resolve %+v %v", repo, err)
	}
	if ok, _ := host.Connected(ctx, "ws"); !ok {
		t.Fatal("workspace with an installation is connected")
	}
	if ok, _ := host.Connected(ctx, "nobody"); ok {
		t.Fatal("workspace without installation is not connected")
	}
	hosts, _ := host.EgressHosts(ctx, "ws")
	if len(hosts) != 5 || hosts[0] != "github.com" {
		t.Fatalf("egress %v", hosts)
	}
	// Invalidation forgets the repository → installation lookup.
	host.InvalidateInstallation("42")
	if len(host.repoInstall) != 0 {
		t.Fatal("invalidate must drop cached lookups")
	}
}

func TestCIStatus(t *testing.T) {
	cases := []struct {
		name     string
		runs     []map[string]string
		statuses []map[string]string
		want     string
	}{
		{name: "none", want: "none"},
		{name: "success", runs: []map[string]string{{"status": "completed", "conclusion": "success"}, {"status": "completed", "conclusion": "skipped"}}, want: "success"},
		{name: "pending run", runs: []map[string]string{{"status": "in_progress"}, {"status": "completed", "conclusion": "success"}}, want: "pending"},
		{name: "failure wins", runs: []map[string]string{{"status": "in_progress"}, {"status": "completed", "conclusion": "timed_out", "html_url": "https://ci/1"}}, want: "failure"},
		{name: "status error", statuses: []map[string]string{{"state": "error", "target_url": "https://ci/2"}}, want: "failure"},
		{name: "status pending", statuses: []map[string]string{{"state": "pending"}}, want: "pending"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake := newFakeGitHub(t)
			fake.setOverride("GET /repos/acme/app/commits/abc/check-runs", func(w http.ResponseWriter, _ *http.Request) bool {
				writeJSONResponse(w, http.StatusOK, map[string]any{"total_count": len(c.runs), "check_runs": c.runs})
				return true
			})
			fake.setOverride("GET /repos/acme/app/commits/abc/status", func(w http.ResponseWriter, _ *http.Request) bool {
				writeJSONResponse(w, http.StatusOK, map[string]any{"state": "pending", "statuses": c.statuses})
				return true
			})
			host, _ := newTestHost(t, fake, nil)
			status, err := host.CIStatus(t.Context(), "ws", appRepo, "abc")
			if err != nil || status.State != c.want {
				t.Fatalf("got %+v %v, want %s", status, err, c.want)
			}
			if c.want == "failure" && status.URL == "" {
				t.Fatal("failure needs a link")
			}
		})
	}
}
