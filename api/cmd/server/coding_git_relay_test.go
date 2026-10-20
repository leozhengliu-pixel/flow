package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/coding"
	"flow/api/internal/domain"
)

// fakeGitHost is a code host's git smart-HTTP endpoint: it records every
// request with the credential it carried.
type fakeGitHost struct {
	*httptest.Server
	mu        sync.Mutex
	requests  []string // "METHOD path?query password"
	usernames []string
	bodies    []string
}

// newFakeGitHost accepts only accept as the HTTP Basic password.
func newFakeGitHost(t *testing.T, accept string) *fakeGitHost {
	f := &fakeGitHost{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, _ := r.BasicAuth()
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.requests = append(f.requests, r.Method+" "+r.URL.RequestURI()+" "+password)
		f.usernames = append(f.usernames, username)
		f.bodies = append(f.bodies, string(body))
		f.mu.Unlock()
		if password != accept {
			w.Header().Set("WWW-Authenticate", `Basic realm="GitHub"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/info/refs") {
			w.Header().Set("Content-Type", "application/x-"+r.URL.Query().Get("service")+"-advertisement")
			_, _ = w.Write([]byte("001f# service=git-receive-pack\n0000"))
			return
		}
		w.Header().Set("Content-Type", "application/x-git-receive-pack-result")
		_, _ = w.Write([]byte("000eunpack ok\n0000"))
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeGitHost) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}

func basicAuth(username, password string) http.Header {
	return http.Header{"Authorization": {"Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))}}
}

// A session started by a member who linked GitHub pushes through Flow's git
// relay: the sandbox's credential helper only ever gets the workspace's
// repository token (straight to the host) or an opaque placeholder (the
// relay URL); the relay adds the member's token only to smart-HTTP requests
// for the session's repository. The placeholder gets nothing anywhere else.
func TestGitRelayPushesAsTheMemberWithoutTheTokenInTheSandbox(t *testing.T) {
	e := newCodingE2E(t)
	ctx := context.Background()
	e.installGitHubApp()
	gitHost := newFakeGitHost(t, "ghu_member_token")
	previous := gitRelayUpstreamBase
	gitRelayUpstreamBase = func(hostName string) string {
		if hostName != "github.com" {
			t.Errorf("relay upstream for %q", hostName)
		}
		return gitHost.URL
	}
	t.Cleanup(func() { gitRelayUpstreamBase = previous })

	// The viewer linked their GitHub account (GitHub App user token, 8h).
	account, err := e.repo.UpsertCodeHostAccount(ctx, domain.CodeHostAccount{ID: "cha_relay", UserID: e.viewer.ID, CodeHost: domain.CodeHostGitHub, HostName: "github.com", BaseURL: "https://github.com", ExternalUserID: "77", Login: e.login, AuthType: domain.CodeHostAuthGitHubApp})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.s.writeCodeHostTokens(ctx, account.ID, codeHostAccountTokens{AccessToken: "ghu_member_token", RefreshToken: "ghr_member", ExpiresAt: time.Now().Add(8 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.codingStore().CreateCodingEnvironment(ctx, domain.CodingEnvironment{WorkspaceKey: "test-workspace", Name: "Web", CodeHost: domain.CodeHostGitHub, HostName: "github.com", Repository: "acme/web", DefaultBranch: "main", SandboxSize: domain.CodingSizeMedium}); err != nil {
		t.Fatal(err)
	}
	issue := e.createIssue("Checkout returns 500")
	agentUser := e.s.builtinAgentUserID(ctx, "test-workspace")
	requestJSON[domain.Issue](t, e.handler, http.MethodPatch, "/api/issue-records/"+issue.ID, map[string]any{"delegateId": agentUser}, http.StatusOK)
	var started coding.TriggerResult
	e.decode(e.do(http.MethodPost, "/api/issues/"+issue.ID+"/coding-session", map[string]any{}, "", nil), http.StatusCreated, &started)
	session := e.drive(started.Session.ID, domain.CodingPhaseStarting)
	if session.PushIdentity == nil || session.PushIdentity.Via != domain.CodeHostAccountActorMember {
		t.Fatalf("the session must push as the member: %+v", session.PushIdentity)
	}
	token := e.provider.token(session.SandboxID)

	var boot coding.BootstrapResponse
	e.decode(e.do(http.MethodGet, "/internal/coding-runtime/bootstrap", nil, token, nil), http.StatusOK, &boot)
	relayURL := "http://flow.test/internal/coding-runtime/git/github.com/acme/web.git"
	if boot.Repo.PushURL != relayURL || boot.Repo.CloneURL != "https://github.com/acme/web.git" {
		t.Fatalf("bootstrap repo %+v", boot.Repo)
	}

	// The credential helper: straight to the host, the workspace token; for
	// the relay URL, a placeholder; never the member's token.
	var direct, relay coding.CredentialResponse
	e.decode(e.do(http.MethodPost, "/internal/coding-runtime/credentials", coding.CredentialRequest{Protocol: "https", Host: "github.com", Path: "acme/web.git", Purpose: "git"}, token, nil), http.StatusOK, &direct)
	e.decode(e.do(http.MethodPost, "/internal/coding-runtime/credentials", coding.CredentialRequest{Protocol: "http", Host: "flow.test", Path: "internal/coding-runtime/git/github.com/acme/web.git", Purpose: "git"}, token, nil), http.StatusOK, &relay)
	if direct.Password != "ghs_installation_token" || !strings.HasPrefix(relay.Password, coding.GitRelayPlaceholderPrefix) {
		t.Fatalf("credentials direct=%+v relay=%+v", direct, relay)
	}
	for _, request := range []coding.CredentialRequest{
		{Protocol: "https", Host: "api.github.com", Purpose: "git"},
		{Protocol: "http", Host: "flow.test", Path: "internal/coding-runtime/git/github.com/acme/other.git", Purpose: "git"},
	} {
		if rec := e.do(http.MethodPost, "/internal/coding-runtime/credentials", request, token, nil); rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), "ghu_") {
			t.Fatalf("credential for %+v: %d %s", request, rec.Code, rec.Body.String())
		}
	}
	placeholder := basicAuth(coding.GitRelayUsername, relay.Password)

	// git's first request carries no credentials: the relay challenges it.
	if rec := e.do(http.MethodGet, "/internal/coding-runtime/git/github.com/acme/web.git/info/refs?service=git-receive-pack", nil, "", nil); rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("unauthenticated: %d %v", rec.Code, rec.Header())
	}
	// The session's push: advertisement and receive-pack reach the host
	// with the member's token, and the pack is passed through.
	refs := e.do(http.MethodGet, "/internal/coding-runtime/git/github.com/acme/web.git/info/refs?service=git-receive-pack", nil, "", placeholder)
	if refs.Code != http.StatusOK || refs.Header().Get("Content-Type") != "application/x-git-receive-pack-advertisement" || !strings.Contains(refs.Body.String(), "service=git-receive-pack") {
		t.Fatalf("info/refs: %d %v %s", refs.Code, refs.Header(), refs.Body.String())
	}
	pushHeader := basicAuth(coding.GitRelayUsername, relay.Password)
	pushHeader.Set("Content-Type", "application/x-git-receive-pack-request")
	push := e.do(http.MethodPost, "/internal/coding-runtime/git/github.com/acme/web.git/git-receive-pack", []byte("PACK-DATA"), "", pushHeader)
	if push.Code != http.StatusOK || !strings.Contains(push.Body.String(), "unpack ok") {
		t.Fatalf("receive-pack: %d %s", push.Code, push.Body.String())
	}
	want := []string{
		"GET /acme/web.git/info/refs?service=git-receive-pack ghu_member_token",
		"POST /acme/web.git/git-receive-pack ghu_member_token",
	}
	if got := gitHost.snapshot(); strings.Join(got, "\n") != strings.Join(want, "\n") || gitHost.bodies[1] != "PACK-DATA" {
		t.Fatalf("host requests %q bodies %q", got, gitHost.bodies)
	}

	// Another repository, another host (the API), a non smart-HTTP path, a
	// forged placeholder or the sandbox token itself: refused, nothing reaches
	// the host.
	for name, attempt := range map[string]struct {
		method, target string
		header         http.Header
		status         int
	}{
		"other repository":  {http.MethodGet, "/internal/coding-runtime/git/github.com/acme/other.git/info/refs?service=git-receive-pack", placeholder, http.StatusForbidden},
		"other repo push":   {http.MethodPost, "/internal/coding-runtime/git/github.com/acme/other.git/git-receive-pack", placeholder, http.StatusForbidden},
		"api host":          {http.MethodGet, "/internal/coding-runtime/git/api.github.com/acme/web.git/info/refs?service=git-upload-pack", placeholder, http.StatusForbidden},
		"api path":          {http.MethodGet, "/internal/coding-runtime/git/github.com/repos/acme/web", placeholder, http.StatusNotFound},
		"dumb http":         {http.MethodGet, "/internal/coding-runtime/git/github.com/acme/web.git/objects/info/packs", placeholder, http.StatusNotFound},
		"forged":            {http.MethodGet, "/internal/coding-runtime/git/github.com/acme/web.git/info/refs?service=git-upload-pack", basicAuth("x", coding.GitRelayPlaceholderPrefix+"forged"), http.StatusUnauthorized},
		"sandbox token":     {http.MethodGet, "/internal/coding-runtime/git/github.com/acme/web.git/info/refs?service=git-upload-pack", basicAuth("x", token), http.StatusUnauthorized},
		"member token used": {http.MethodGet, "/internal/coding-runtime/git/github.com/acme/web.git/info/refs?service=git-upload-pack", basicAuth("x", "ghu_member_token"), http.StatusUnauthorized},
	} {
		rec := e.do(attempt.method, attempt.target, nil, "", attempt.header)
		if rec.Code != attempt.status || strings.Contains(rec.Body.String(), "ghu_") {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	if got := gitHost.snapshot(); len(got) != 2 {
		t.Fatalf("refused requests reached the host: %q", got)
	}
	// The placeholder is no sandbox credential: not for the runtime API, the
	// LLM proxy, or the egress proxy.
	if rec := e.do(http.MethodPost, "/internal/coding-runtime/credentials", coding.CredentialRequest{Protocol: "https", Host: "github.com", Purpose: "git"}, relay.Password, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("placeholder as a runtime token: %d", rec.Code)
	}
	if grant, err := e.s.codingStore().AuthenticateSandboxToken(ctx, relay.Password); err == nil {
		t.Fatalf("placeholder authenticated as a sandbox: %+v", grant)
	}
	// Nothing on the GitHub API side ever saw the member's token.
	for _, call := range e.github.unknown {
		if strings.Contains(call, "ghu_member_token") {
			t.Fatalf("member token sent to the API: %s", call)
		}
	}

	// Revoking the sandbox (session end) stops the relay.
	if err := e.repo.RevokeSandboxTokens(ctx, session.SandboxID); err != nil {
		t.Fatal(err)
	}
	if rec := e.do(http.MethodGet, "/internal/coding-runtime/git/github.com/acme/web.git/info/refs?service=git-receive-pack", nil, "", placeholder); rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked sandbox: %d %s", rec.Code, rec.Body.String())
	}
}

// A member who linked GitLab with a personal access token pushes as
// themselves through the relay: the relay forwards with the PAT, while the
// sandbox's env, its token file, the bootstrap and every credential-helper
// answer (git or gh/glab CLI) never carry it.
func TestGitRelayPushesWithAMemberPersonalAccessToken(t *testing.T) {
	e := newCodingE2E(t)
	ctx := context.Background()
	const pat = "glpat-member-relay-secret"
	var connected gitlabConnectResponse
	e.decode(e.do(http.MethodPut, "/api/integrations/gitlab?workspace=test-workspace", map[string]any{"config": map[string]string{"apiToken": "glpat-fake-api", "host": e.gitlab.URL}}, "", nil), http.StatusOK, &connected)
	host := strings.TrimPrefix(e.gitlab.URL, "http://")
	gitHost := newFakeGitHost(t, pat)
	previous := gitRelayUpstreamBase
	gitRelayUpstreamBase = func(hostName string) string {
		if hostName != host {
			t.Errorf("relay upstream for %q", hostName)
		}
		return gitHost.URL
	}
	t.Cleanup(func() { gitRelayUpstreamBase = previous })

	account, err := e.repo.UpsertCodeHostAccount(ctx, domain.CodeHostAccount{ID: "cha_pat_relay", UserID: e.viewer.ID, CodeHost: domain.CodeHostGitLab, HostName: host, BaseURL: e.gitlab.URL, ExternalUserID: "42", Login: "alice-gl",
		AuthType: domain.CodeHostAuthPAT, WorkspaceKey: "test-workspace", ConnectionID: connected.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.s.writeCodeHostTokens(ctx, account.ID, codeHostAccountTokens{AccessToken: pat, PrivateToken: true}); err != nil {
		t.Fatal(err)
	}
	environment, err := e.s.codingStore().CreateCodingEnvironment(ctx, domain.CodingEnvironment{WorkspaceKey: "test-workspace", Name: "API", CodeHost: domain.CodeHostGitLab, HostName: host, Repository: "acme/api", DefaultBranch: "main", SandboxSize: domain.CodingSizeMedium})
	if err != nil {
		t.Fatal(err)
	}
	issue := e.createIssue("Add rate limiting")
	var started coding.TriggerResult
	e.decode(e.do(http.MethodPost, "/api/issues/"+issue.ID+"/coding-session", map[string]any{"environmentId": environment.ID}, "", nil), http.StatusCreated, &started)
	session := e.drive(started.Session.ID, domain.CodingPhaseStarting)
	if session.PushIdentity == nil || session.PushIdentity.Via != domain.CodeHostAccountActorMember || session.PushIdentity.Login != "alice-gl" {
		t.Fatalf("the session must push as the member: %+v", session.PushIdentity)
	}
	token := e.provider.token(session.SandboxID)
	if token == "" || strings.Contains(token, pat) {
		t.Fatal("the sandbox token file must be the sandbox token only")
	}
	spec, ok := e.provider.Spec(session.SandboxID)
	if !ok {
		t.Fatal("sandbox spec not recorded")
	}
	if raw, _ := json.Marshal(spec); strings.Contains(string(raw), pat) {
		t.Fatalf("the sandbox spec (env, files) carries the PAT: %s", raw)
	}

	boot := e.do(http.MethodGet, "/internal/coding-runtime/bootstrap", nil, token, nil)
	if boot.Code != http.StatusOK || strings.Contains(boot.Body.String(), pat) {
		t.Fatalf("bootstrap: %d leaks=%v", boot.Code, strings.Contains(boot.Body.String(), pat))
	}
	var bootstrap coding.BootstrapResponse
	_ = json.Unmarshal(boot.Body.Bytes(), &bootstrap)
	if bootstrap.Repo.PushURL != "http://flow.test/internal/coding-runtime/git/"+host+"/acme/api.git" {
		t.Fatalf("bootstrap repo %+v", bootstrap.Repo)
	}

	// Every credential-helper answer: straight to the host (git and the
	// glab CLI) and for the relay URL. None carries the PAT.
	var relay coding.CredentialResponse
	for _, request := range []coding.CredentialRequest{
		{Protocol: "http", Host: host, Path: "acme/api.git", Purpose: "git"},
		{Protocol: "http", Host: host, Purpose: "cli"},
		{Protocol: "http", Host: "flow.test", Path: "internal/coding-runtime/git/" + host + "/acme/api.git", Purpose: "cli"},
		{Protocol: "http", Host: "flow.test", Path: "internal/coding-runtime/git/" + host + "/acme/api.git", Purpose: "git"},
	} {
		rec := e.do(http.MethodPost, "/internal/coding-runtime/credentials", request, token, nil)
		if strings.Contains(rec.Body.String(), pat) {
			t.Fatalf("credential helper served the PAT for %+v", request)
		}
		if request.Purpose == "git" && strings.HasPrefix(request.Path, "internal/") {
			e.decode(rec, http.StatusOK, &relay)
		}
	}
	if !strings.HasPrefix(relay.Password, coding.GitRelayPlaceholderPrefix) || relay.Username != coding.GitRelayUsername {
		t.Fatalf("relay credential %+v", relay)
	}

	// The push goes through the relay, which adds the PAT server side.
	placeholder := basicAuth(coding.GitRelayUsername, relay.Password)
	refs := e.do(http.MethodGet, "/internal/coding-runtime/git/"+host+"/acme/api.git/info/refs?service=git-receive-pack", nil, "", placeholder)
	if refs.Code != http.StatusOK || strings.Contains(refs.Body.String(), pat) {
		t.Fatalf("info/refs: %d %s", refs.Code, refs.Body.String())
	}
	pushHeader := basicAuth(coding.GitRelayUsername, relay.Password)
	pushHeader.Set("Content-Type", "application/x-git-receive-pack-request")
	push := e.do(http.MethodPost, "/internal/coding-runtime/git/"+host+"/acme/api.git/git-receive-pack", []byte("PACK-DATA"), "", pushHeader)
	if push.Code != http.StatusOK || !strings.Contains(push.Body.String(), "unpack ok") || strings.Contains(push.Body.String(), pat) {
		t.Fatalf("receive-pack: %d %s", push.Code, push.Body.String())
	}
	want := []string{
		"GET /acme/api.git/info/refs?service=git-receive-pack " + pat,
		"POST /acme/api.git/git-receive-pack " + pat,
	}
	if got := gitHost.snapshot(); strings.Join(got, "\n") != strings.Join(want, "\n") || gitHost.bodies[1] != "PACK-DATA" {
		t.Fatalf("host requests %q bodies %q", got, gitHost.bodies)
	}
	for _, username := range gitHost.usernames {
		if username != "alice-gl" {
			t.Fatalf("PAT sent with username %q", username)
		}
	}
	// Another repository gets nothing, and nothing more reaches the host.
	if rec := e.do(http.MethodGet, "/internal/coding-runtime/git/"+host+"/acme/other.git/info/refs?service=git-receive-pack", nil, "", placeholder); rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), pat) {
		t.Fatalf("other repository: %d %s", rec.Code, rec.Body.String())
	}
	if len(gitHost.snapshot()) != 2 {
		t.Fatalf("refused request reached the host: %q", gitHost.snapshot())
	}
	// Session end stops the relay.
	if err := e.repo.RevokeSandboxTokens(ctx, session.SandboxID); err != nil {
		t.Fatal(err)
	}
	if rec := e.do(http.MethodGet, "/internal/coding-runtime/git/"+host+"/acme/api.git/info/refs?service=git-receive-pack", nil, "", placeholder); rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked sandbox: %d %s", rec.Code, rec.Body.String())
	}
}
