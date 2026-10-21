package gitlab

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"flow/api/internal/codehost"
	"flow/api/internal/domain"
)

var fixedNow = time.Date(2026, 10, 18, 9, 30, 0, 0, time.UTC)

func mintRequest(fake *fakeGitLab, sessionID string) codehost.CredentialRequest {
	return codehost.CredentialRequest{WorkspaceKey: "ws", SessionID: sessionID, SandboxID: "sb1", Repo: testRepo(fake), Access: codehost.AccessWrite}
}

func TestProjectTokenMintReuseRestartRotateAndRevoke(t *testing.T) {
	fake := newFakeGitLab(t)
	host, _ := newTestHost(t, fake, ModeProjectToken, fixedNow)
	ctx := context.Background()
	project := fake.project("acme/platform/api")

	first, err := host.MintCredential(ctx, mintRequest(fake, "01jsession"))
	if err != nil {
		t.Fatal(err)
	}
	created := fake.lastBody("POST /projects/acme%2Fplatform%2Fapi/access_tokens")
	if created["name"] != "flow-cs-01jsession" || created["access_level"] != float64(30) || created["expires_at"] != "2026-10-19" {
		t.Fatalf("project token request: %#v", created)
	}
	if scopes := created["scopes"].([]any); len(scopes) != 3 || scopes[0] != "read_repository" || scopes[1] != "write_repository" {
		t.Fatalf("scopes: %#v", scopes)
	}
	if first.Password == "" || first.RevokeID == "" || !first.ExpiresAt.Equal(time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("credential: %#v", first)
	}
	if first.CLIEnv[EnvGitLabToken] != first.Password || first.CLIEnv[EnvGitLabHost] == "" || first.CLIEnv[EnvGlabConfigDir] != GlabConfigDir || first.CLIEnv[EnvAPIProtocol] != "http" {
		t.Fatalf("cli env: %#v", first.CLIEnv)
	}

	// The credential helper asks again on every git operation: same token, no new mint.
	again, err := host.MintCredential(ctx, mintRequest(fake, "01jsession"))
	if err != nil || again.Password != first.Password {
		t.Fatalf("second mint: %v %#v", err, again)
	}
	if n := fake.count("POST /projects/acme%2Fplatform%2Fapi/access_tokens"); n != 1 {
		t.Fatalf("expected one token creation, got %d", n)
	}

	// After a Flow restart the cache is empty: the existing token is rotated, not duplicated.
	restarted, _ := newTestHost(t, fake, ModeProjectToken, fixedNow)
	rotated, err := restarted.MintCredential(ctx, mintRequest(fake, "01jsession"))
	if err != nil {
		t.Fatal(err)
	}
	if rotated.Password == first.Password || fake.count("POST /projects/acme%2Fplatform%2Fapi/access_tokens/") != 1 {
		t.Fatalf("restart should rotate: %#v requests=%v", rotated, fake.requests)
	}
	active := 0
	for _, token := range project.Tokens {
		if token.Active {
			active++
		}
	}
	if active != 1 {
		t.Fatalf("expected exactly one active token, got %d", active)
	}

	// Revocation at session end, idempotently.
	if err := restarted.RevokeCredential(ctx, "ws", rotated); err != nil {
		t.Fatal(err)
	}
	if err := restarted.RevokeCredential(ctx, "ws", rotated); err != nil {
		t.Fatalf("second revoke must succeed: %v", err)
	}
	for _, token := range project.Tokens {
		if token.Active {
			t.Fatalf("token still active after revoke: %#v", token)
		}
	}
	// OAuth/connection credentials carry no RevokeID: revoke is a no-op.
	if err := restarted.RevokeCredential(ctx, "ws", codehost.Credential{Password: "x"}); err != nil {
		t.Fatal(err)
	}
}

func TestRevokeSessionCredentialsAndSandboxTokens(t *testing.T) {
	fake := newFakeGitLab(t)
	host, _ := newTestHost(t, fake, ModeProjectToken, fixedNow)
	ctx := context.Background()
	if _, err := host.MintCredential(ctx, mintRequest(fake, "s1")); err != nil {
		t.Fatal(err)
	}
	snapshot := codehost.CredentialRequest{WorkspaceKey: "ws", SandboxID: "snap1", Repo: testRepo(fake)}
	if _, err := host.MintCredential(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	if err := host.RevokeSessionCredentials(ctx, "ws", "s1"); err != nil {
		t.Fatal(err)
	}
	for _, token := range fake.project("acme/platform/api").Tokens {
		if token.Name == "flow-cs-s1" && token.Active {
			t.Fatal("session token not revoked")
		}
		if token.Name == "flow-sb-snap1" && !token.Active {
			t.Fatal("sandbox token revoked with the session")
		}
	}
	if _, err := host.MintCredential(ctx, codehost.CredentialRequest{WorkspaceKey: "ws", Repo: testRepo(fake)}); err == nil {
		t.Fatal("mint without session or sandbox id must fail")
	}
}

func TestProjectTokenErrorsExplainMaintainerAndPremium(t *testing.T) {
	fake := newFakeGitLab(t)
	fake.project("acme/platform/api").AccessLevel = 30 // Developer: cannot create project tokens
	host, source := newTestHost(t, fake, ModeProjectToken, fixedNow)
	ctx := context.Background()
	_, err := host.MintCredential(ctx, mintRequest(fake, "s1"))
	if !errors.Is(err, ErrProjectTokensUnavailable) || ErrorCode(err) != domain.CodingErrNoCodeAccess {
		t.Fatalf("expected project token error, got %v (%s)", err, ErrorCode(err))
	}
	if message := ErrorMessage(err); !strings.Contains(message, "Maintainer") || !strings.Contains(message, "Premium") || !strings.Contains(message, "api scope") {
		t.Fatalf("message must explain requirements: %q", message)
	}
	if err := host.CheckCredentialMode(ctx, "ws", testRepo(fake)); !errors.Is(err, ErrProjectTokensUnavailable) {
		t.Fatalf("preflight: %v", err)
	}

	source.connections[0].Readonly = true
	if _, err := host.MintCredential(ctx, mintRequest(fake, "s1")); !errors.Is(err, ErrReadOnlyConnection) || ErrorCode(err) != domain.CodingErrNoCodeAccess {
		t.Fatalf("read-only connection: %v", err)
	}
	source.connections = nil
	if _, err := host.MintCredential(ctx, mintRequest(fake, "s1")); ErrorCode(err) != domain.CodingErrNoGitLabConnection {
		t.Fatalf("no connection: %v (%s)", err, ErrorCode(err))
	}
}

func TestOAuthAndConnectionModes(t *testing.T) {
	fake := newFakeGitLab(t)
	ctx := context.Background()

	oauthHost, source := newTestHost(t, fake, ModeOAuth, fixedNow)
	if _, err := oauthHost.MintCredential(ctx, mintRequest(fake, "s1")); !errors.Is(err, ErrOAuthRequired) || ErrorCode(err) != domain.CodingErrNoGitLabConnection {
		t.Fatalf("oauth mode with a token connection: %v", err)
	}
	if err := oauthHost.CheckCredentialMode(ctx, "ws", testRepo(fake)); !errors.Is(err, ErrOAuthRequired) {
		t.Fatalf("oauth preflight: %v", err)
	}
	source.connections = []Connection{fakeConnection(fake, ConnectionOAuth)}
	source.oauthToken, source.oauthExpiry = "glpat-connection", fixedNow.Add(2*time.Hour)
	credential, err := oauthHost.MintCredential(ctx, mintRequest(fake, "s1"))
	if err != nil {
		t.Fatal(err)
	}
	if credential.Username != "oauth2" || credential.Password != "glpat-connection" || !credential.ExpiresAt.Equal(source.oauthExpiry) || credential.RevokeID != "" {
		t.Fatalf("oauth credential: %#v", credential)
	}
	// OAuth API calls use the Bearer header (the fake accepts it).
	if _, err := oauthHost.BranchHead(ctx, "ws", testRepo(fake), "main"); err != nil {
		t.Fatalf("bearer API call: %v", err)
	}

	connectionHost, _ := newTestHost(t, fake, ModeConnection, fixedNow)
	credential, err = connectionHost.MintCredential(ctx, mintRequest(fake, "s1"))
	if err != nil {
		t.Fatal(err)
	}
	if credential.Password != "glpat-connection" || credential.RevokeID != "" || !credential.ExpiresAt.Equal(fixedNow.Add(time.Hour)) || credential.CLIEnv[EnvGitLabToken] != "glpat-connection" {
		t.Fatalf("connection credential: %#v", credential)
	}
	if fake.count("POST /projects/acme%2Fplatform%2Fapi/access_tokens") != 0 {
		t.Fatal("oauth/connection modes must not mint project tokens")
	}
}

func TestCreateDraftMergeRequestIsIdempotent(t *testing.T) {
	fake := newFakeGitLab(t)
	host, _ := newTestHost(t, fake, ModeProjectToken, fixedNow)
	ctx := context.Background()
	project := fake.project("acme/platform/api")
	project.Branches["flow/fix-login"] = strings.Repeat("b", 40)
	input := codehost.CreateChangeRequestInput{Repo: testRepo(fake), Head: "flow/fix-login", Title: "Fix login", Body: "Fixes ENG-1", Draft: true, Labels: []string{"flow"}}

	existing, err := host.FindChangeRequest(ctx, "ws", input.Repo, input.Head)
	if err != nil || existing != nil {
		t.Fatalf("no MR yet: %v %#v", err, existing)
	}
	created, err := host.CreateChangeRequest(ctx, "ws", input)
	if err != nil {
		t.Fatal(err)
	}
	body := fake.lastBody("POST /projects/acme%2Fplatform%2Fapi/merge_requests")
	if body["title"] != "Draft: Fix login" || body["target_branch"] != "main" || body["remove_source_branch"] != true || body["labels"] != "flow" || body["description"] != "Fixes ENG-1" {
		t.Fatalf("create body: %#v", body)
	}
	if created.Number != 1 || !created.Draft || created.State != "open" || created.HeadSHA != strings.Repeat("b", 40) || created.URL == "" || created.Repo.ID != "7" {
		t.Fatalf("created: %#v", created)
	}
	again, err := host.CreateChangeRequest(ctx, "ws", input)
	if err != nil || again.Number != created.Number {
		t.Fatalf("second create: %v %#v", err, again)
	}
	if n := fake.count("POST /projects/acme%2Fplatform%2Fapi/merge_requests"); n != 1 {
		t.Fatalf("expected one MR creation, got %d", n)
	}
	found, err := host.FindChangeRequest(ctx, "ws", input.Repo, input.Head)
	if err != nil || found == nil || found.Number != 1 {
		t.Fatalf("find: %v %#v", err, found)
	}
	got, err := host.GetChangeRequest(ctx, "ws", input.Repo, 1)
	if err != nil || got.HeadBranch != "flow/fix-login" || got.BaseBranch != "main" {
		t.Fatalf("get: %v %#v", err, got)
	}

	// A concurrent creation answers 409: resolve to the winner.
	project.Branches["flow/race"] = strings.Repeat("c", 40)
	fake.mrConflict = true
	raced, err := host.CreateChangeRequest(ctx, "ws", codehost.CreateChangeRequestInput{Repo: input.Repo, Head: "flow/race", Base: "main", Title: "Draft: Race", Draft: true})
	if err != nil || raced.HeadBranch != "flow/race" || raced.Title != "Draft: Race" {
		t.Fatalf("409 race: %v %#v", err, raced)
	}

	if err := host.UpdateChangeRequestBody(ctx, "ws", input.Repo, 1, "new body"); err != nil {
		t.Fatal(err)
	}
	if project.MRs[0]["description"] != "new body" {
		t.Fatalf("description not updated: %#v", project.MRs[0])
	}
	// GetChangeRequest returns the description as Body (PR body decoration
	// reads it before appending its sections).
	if refreshed, err := host.GetChangeRequest(ctx, "ws", input.Repo, 1); err != nil || refreshed.Body != "new body" {
		t.Fatalf("get after update: %v %q", err, refreshed.Body)
	}
	if err := host.CloseChangeRequest(ctx, "ws", input.Repo, 1); err != nil || project.MRs[0]["state"] != "closed" {
		t.Fatalf("close: %v %#v", err, project.MRs[0])
	}
	if err := host.Comment(ctx, "ws", input.Repo, 1, "Session finished"); err != nil {
		t.Fatal(err)
	}
	if len(project.Notes[1]) != 1 || project.Notes[1][0] != "Session finished" {
		t.Fatalf("note not posted: %#v", project.Notes)
	}
	if _, err := host.GetChangeRequest(ctx, "ws", input.Repo, 99); !errors.Is(err, codehost.ErrNotFound) {
		t.Fatalf("missing MR: %v", err)
	}
}

func TestDraftTitle(t *testing.T) {
	for input, want := range map[string]string{"Fix": "Draft: Fix", "Draft: Fix": "Draft: Fix", "[Draft] Fix": "[Draft] Fix", "": "Draft: Flow coding session", "WIP: x": "WIP: x"} {
		if got := DraftTitle(input); got != want {
			t.Errorf("DraftTitle(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestCIStatus(t *testing.T) {
	fake := newFakeGitLab(t)
	host, _ := newTestHost(t, fake, ModeProjectToken, fixedNow)
	ctx := context.Background()
	project := fake.project("acme/platform/api")
	repo := testRepo(fake)
	sha := strings.Repeat("d", 40)

	status, err := host.CIStatus(ctx, "ws", repo, "flow/none")
	if err != nil || status.State != "none" {
		t.Fatalf("no pipeline: %v %#v", err, status)
	}
	project.Pipelines = append(project.Pipelines,
		map[string]any{"id": 11, "status": "running", "ref": "flow/a", "sha": sha, "web_url": "https://gitlab.example/p/11"},
		map[string]any{"id": 12, "status": "failed", "ref": "flow/b", "sha": strings.Repeat("e", 40), "web_url": "https://gitlab.example/p/12"},
		map[string]any{"id": 13, "status": "success", "ref": "flow/c", "sha": strings.Repeat("f", 40), "web_url": "https://gitlab.example/p/13"},
	)
	project.FailedJobs[12] = []map[string]any{{"name": "unit", "stage": "test"}, {"name": "lint", "stage": "test"}}
	cases := []struct {
		ref, state, summary string
	}{
		{"flow/a", "pending", "Pipeline #11 running"},
		{sha, "pending", "Pipeline #11 running"},
		{"flow/b", "failure", "Pipeline #12 failed: failed jobs test/unit, test/lint"},
		{"flow/c", "success", "Pipeline #13 success"},
	}
	for _, c := range cases {
		status, err := host.CIStatus(ctx, "ws", repo, c.ref)
		if err != nil || status.State != c.state || status.Summary != c.summary || status.URL == "" {
			t.Errorf("CIStatus(%s) = %#v, %v; want %s %q", c.ref, status, err, c.state, c.summary)
		}
	}
	for status, want := range map[string]string{"success": "success", "failed": "failure", "canceled": "failure", "skipped": "none", "manual": "pending", "created": "pending", "waiting_for_resource": "pending"} {
		if got := normalizePipelineStatus(status); got != want {
			t.Errorf("normalizePipelineStatus(%s) = %s, want %s", status, got, want)
		}
	}
}

func TestRepositoriesBranchesAndUserAccess(t *testing.T) {
	fake := newFakeGitLab(t)
	fake.addProject(8, "acme/web", "develop", 30)
	fake.addProject(9, "acme/docs", "main", 30)
	host, _ := newTestHost(t, fake, ModeProjectToken, fixedNow)
	ctx := context.Background()

	page, err := host.ListRepositories(ctx, "ws", codehost.ListOptions{Limit: 2})
	if err != nil || len(page.Repositories) != 2 || page.NextCursor != "2" {
		t.Fatalf("page 1: %v %#v", err, page)
	}
	if repo := page.Repositories[0]; repo.FullName != "acme/platform/api" || repo.ID != "7" || repo.DefaultBranch != "main" || !repo.Private || repo.InstallationID != "integration_gitlab_1" || repo.CodeHost != "gitlab" {
		t.Fatalf("repository: %#v", repo)
	}
	page, err = host.ListRepositories(ctx, "ws", codehost.ListOptions{Limit: 2, Cursor: page.NextCursor})
	if err != nil || len(page.Repositories) != 1 || page.NextCursor != "" {
		t.Fatalf("page 2: %v %#v", err, page)
	}
	page, err = host.ListRepositories(ctx, "ws", codehost.ListOptions{Query: "web"})
	if err != nil || len(page.Repositories) != 1 || page.Repositories[0].FullName != "acme/web" {
		t.Fatalf("search: %v %#v", err, page)
	}
	if _, err := host.ListRepositories(ctx, "ws", codehost.ListOptions{HostName: "gitlab.other.example"}); ErrorCode(err) != domain.CodingErrNoGitLabConnection {
		t.Fatalf("unknown host: %v", err)
	}

	resolved, err := host.ResolveRepository(ctx, "ws", codehost.RepoRef{CodeHost: "gitlab", FullName: "acme/web"})
	if err != nil || resolved.DefaultBranch != "develop" || !strings.HasSuffix(resolved.CloneURL, "/acme/web.git") {
		t.Fatalf("resolve: %v %#v", err, resolved)
	}
	if _, err := host.ResolveRepository(ctx, "ws", codehost.RepoRef{FullName: "acme/secret"}); !errors.Is(err, codehost.ErrNoAccess) || ErrorCode(err) != domain.CodingErrNoCodeAccess {
		t.Fatalf("hidden project: %v", err)
	}
	// Numeric ids work as well as paths.
	if _, err := host.ResolveRepository(ctx, "ws", codehost.RepoRef{FullName: "ignored", ID: "8"}); err != nil {
		t.Fatalf("resolve by id: %v", err)
	}

	head, err := host.BranchHead(ctx, "ws", testRepo(fake), "main")
	if err != nil || head != strings.Repeat("a", 40) {
		t.Fatalf("branch head: %v %q", err, head)
	}
	if _, err := host.BranchHead(ctx, "ws", testRepo(fake), "flow/missing"); !errors.Is(err, codehost.ErrNotFound) {
		t.Fatalf("missing branch: %v", err)
	}

	project := fake.project("acme/platform/api")
	fake.users["dev"], fake.users["reporter"], fake.users["outsider"] = 1, 2, 3
	project.Members[1], project.Members[2] = 30, 20
	repo := testRepo(fake)
	if err := host.CheckUserAccess(ctx, "ws", repo, codehost.UserIdentity{Login: "dev"}); err != nil {
		t.Fatalf("developer: %v", err)
	}
	for login, want := range map[string]domain.CodingErrorCode{"": domain.CodingErrRequiresGitLabConnection, "reporter": domain.CodingErrPersonalAccessDenied, "outsider": domain.CodingErrPersonalAccessDenied, "nobody": domain.CodingErrPersonalAccessDenied} {
		if err := host.CheckUserAccess(ctx, "ws", repo, codehost.UserIdentity{Login: login}); ErrorCode(err) != want {
			t.Errorf("CheckUserAccess(%q) = %v (%s), want %s", login, err, ErrorCode(err), want)
		}
	}
}

func TestErrorMapping(t *testing.T) {
	fake := newFakeGitLab(t)
	host, source := newTestHost(t, fake, ModeProjectToken, fixedNow)
	ctx := context.Background()
	repo := testRepo(fake)
	cases := []struct {
		name     string
		status   int
		code     domain.CodingErrorCode
		rejected bool
	}{
		{"revoked token", http.StatusUnauthorized, domain.CodingErrNoGitLabConnection, false},
		{"forbidden", http.StatusForbidden, domain.CodingErrNoCodeAccess, false},
		{"rate limited", http.StatusTooManyRequests, domain.CodingErrGitLabTransientError, false},
		{"server error", http.StatusBadGateway, domain.CodingErrGitLabTransientError, false},
		{"validation", http.StatusUnprocessableEntity, domain.CodingErrInternal, true},
	}
	for _, c := range cases {
		fake.mu.Lock()
		fake.statusFor = map[string]int{"PUT /projects/": c.status}
		fake.mu.Unlock()
		err := host.UpdateChangeRequestBody(ctx, "ws", repo, 1, "x")
		if ErrorCode(err) != c.code || ErrRejected(err) != c.rejected {
			t.Errorf("%s: %v → %s (rejected=%t), want %s", c.name, err, ErrorCode(err), ErrRejected(err), c.code)
		}
		if strings.Contains(err.Error(), fake.apiToken) || strings.Contains(ErrorMessage(err), fake.apiToken) {
			t.Errorf("%s: token leaked: %v", c.name, err)
		}
		var gitlabErr *Error
		if !errors.As(err, &gitlabErr) || strings.Contains(gitlabErr.Body, fake.apiToken) || !strings.Contains(gitlabErr.Body, "[redacted]") && c.status != http.StatusUnauthorized {
			t.Errorf("%s: body not redacted: %#v", c.name, gitlabErr)
		}
	}
	fake.mu.Lock()
	fake.statusFor = map[string]int{}
	fake.mu.Unlock()
	source.staleToken = "glpat-revoked"

	if _, err := host.BranchHead(ctx, "ws", repo, "main"); ErrorCode(err) != domain.CodingErrNoGitLabConnection || ErrorMessage(err) != MessageTokenRevoked {
		t.Fatalf("revoked connection token: %v", err)
	}

	// Network failure is transient.
	source.staleToken = ""
	source.connections[0].BaseURL = "http://127.0.0.1:1"
	if _, err := host.BranchHead(ctx, "ws", repo, "main"); ErrorCode(err) != domain.CodingErrGitLabTransientError {
		t.Fatalf("network error: %v (%s)", err, ErrorCode(err))
	}
	if ErrorCode(nil) != "" || ErrorCode(errors.New("boom")) != domain.CodingErrInternal {
		t.Fatal("nil/unknown mapping")
	}
}

func TestTokenExpiry(t *testing.T) {
	cases := []struct {
		now  time.Time
		want string
	}{
		{time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC), "2026-10-19"},
		{time.Date(2026, 10, 18, 11, 59, 0, 0, time.UTC), "2026-10-19"},
		{time.Date(2026, 10, 18, 12, 1, 0, 0, time.UTC), "2026-10-20"},
		{time.Date(2026, 12, 31, 23, 0, 0, 0, time.UTC), "2027-01-02"},
	}
	for _, c := range cases {
		got, at := tokenExpiry(c.now)
		if got != c.want || at.Sub(c.now) < 12*time.Hour {
			t.Errorf("tokenExpiry(%s) = %s (%s), want %s", c.now, got, at, c.want)
		}
	}
}

func TestSweepProjectTokens(t *testing.T) {
	fake := newFakeGitLab(t)
	host, _ := newTestHost(t, fake, ModeProjectToken, fixedNow)
	ctx := context.Background()
	project := fake.project("acme/platform/api")
	old := fixedNow.Add(-48 * time.Hour)
	project.Tokens = []*fakeToken{
		{ID: 1, Name: "flow-cs-live", Active: true, CreatedAt: fixedNow},
		{ID: 2, Name: "flow-cs-ended", Active: true, CreatedAt: fixedNow},
		{ID: 3, Name: "flow-sb-snap", Active: true, CreatedAt: old},
		{ID: 4, Name: "ci-deploy-token", Active: true, CreatedAt: old},
	}
	keep := func(name string, createdAt time.Time) bool {
		if strings.HasPrefix(name, SandboxTokenPrefix) {
			return fixedNow.Sub(createdAt) < 2*time.Hour
		}
		return name == "flow-cs-live"
	}
	revoked, err := host.SweepProjectTokens(ctx, "ws", []codehost.RepoRef{testRepo(fake)}, keep)
	if err != nil || revoked != 2 {
		t.Fatalf("sweep: revoked=%d err=%v", revoked, err)
	}
	want := map[string]bool{"flow-cs-live": true, "flow-cs-ended": false, "flow-sb-snap": false, "ci-deploy-token": true}
	for _, token := range project.Tokens {
		if token.Active != want[token.Name] {
			t.Errorf("%s active=%t, want %t", token.Name, token.Active, want[token.Name])
		}
	}
	// Idempotent: a second sweep revokes nothing.
	if revoked, err := host.SweepProjectTokens(ctx, "ws", []codehost.RepoRef{testRepo(fake)}, keep); err != nil || revoked != 0 {
		t.Fatalf("second sweep: %d %v", revoked, err)
	}
	// Projects minted in by this process are swept even when not listed.
	if _, err := host.MintCredential(ctx, mintRequest(fake, "gone")); err != nil {
		t.Fatal(err)
	}
	if revoked, err := host.SweepProjectTokens(ctx, "ws", nil, keep); err != nil || revoked != 1 {
		t.Fatalf("sweep of minted project: %d %v", revoked, err)
	}
}

func TestEgressHostsAndConnected(t *testing.T) {
	fake := newFakeGitLab(t)
	host, source := newTestHost(t, fake, ModeProjectToken, fixedNow)
	ctx := context.Background()
	hosts, err := host.EgressHosts(ctx, "ws")
	if err != nil || len(hosts) != 2 || hosts[0] != "gitlab.internal.example" || hosts[1] != "127.0.0.1" {
		t.Fatalf("workspace egress hosts: %v %v", hosts, err)
	}
	hosts, _ = host.EgressHosts(ctx, "")
	if len(hosts) != 2 || hosts[1] != "gitlab.com" {
		t.Fatalf("global egress hosts: %v", hosts)
	}
	if ok, err := host.Connected(ctx, "ws"); !ok || err != nil {
		t.Fatalf("connected: %t %v", ok, err)
	}
	source.connections = nil
	if ok, _ := host.Connected(ctx, "ws"); ok {
		t.Fatal("no connections must read as not connected")
	}
	unwired, _ := New(codehost.Dependencies{})
	if ok, err := unwired.Connected(ctx, "ws"); ok || err != nil {
		t.Fatalf("unwired host: %t %v", ok, err)
	}
	if _, err := unwired.ListRepositories(ctx, "ws", codehost.ListOptions{}); ErrorCode(err) != domain.CodingErrNoGitLabConnection {
		t.Fatalf("unwired list: %v", err)
	}
}

func TestGlabContractAndGuidance(t *testing.T) {
	selfManaged := Connection{BaseURL: "https://code.example.com/gitlab", HostName: "code.example.com"}
	env := CLIEnv(selfManaged, "tok")
	if env[EnvGitLabHost] != "code.example.com/gitlab" || env[EnvAPIProtocol] != "https" || env[EnvGitLabToken] != "tok" || env[EnvNoPrompt] != "1" {
		t.Fatalf("self-managed env: %#v", env)
	}
	commands := GlabConfigCommands(env)
	if len(commands) != 3 || strings.Join(commands[0], " ") != "glab config set --global host code.example.com/gitlab" {
		t.Fatalf("glab config commands: %v", commands)
	}
	for _, command := range commands {
		if strings.Contains(strings.Join(command, " "), "tok") {
			t.Fatal("glab config must never contain the token")
		}
	}
	if GlabConfigCommands(CLIEnv(Connection{BaseURL: "https://gitlab.com", HostName: "gitlab.com"}, "tok")) != nil {
		t.Fatal("gitlab.com needs no glab config")
	}
	if CanWriteNotes(ModeProjectToken, []string{"read_api"}) || !CanWriteNotes(ModeProjectToken, []string{"api"}) || !CanWriteNotes(ModeOAuth, nil) {
		t.Fatal("CanWriteNotes")
	}
	withNotes, readOnly := AgentGuidance("gitlab.com", true), AgentGuidance("", false)
	for _, want := range []string{"glab mr view", "glab ci status", "glab ci view", "Never run `glab mr create`"} {
		if !strings.Contains(withNotes, want) || !strings.Contains(readOnly, want) {
			t.Errorf("guidance misses %q", want)
		}
	}
	if !strings.Contains(withNotes, "glab mr note") || strings.Contains(readOnly, "glab mr note <iid> -m") || !strings.Contains(readOnly, "cannot post merge request notes") {
		t.Fatal("note guidance must follow the scope")
	}
	if info := CredentialModeInfo(ModeProjectToken); !strings.Contains(info.Requirements, "Maintainer") || !strings.Contains(info.Requirements, "Premium") {
		t.Fatalf("project-token copy: %#v", info)
	}
	if CredentialModeInfo(ModeConnection).Warning == "" || CredentialModeInfo("bogus").Mode != ModeProjectToken {
		t.Fatal("mode copy")
	}
}
