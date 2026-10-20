package coding

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/codehost"
	"flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/sandbox"
)

// memberSessions serves sessions per sandbox token: each token is one
// sandbox of one session with its own creator.
type memberSessions struct {
	CoreStore
	grants       map[string]SandboxGrant
	sessions     map[string]domain.CodingSession
	placeholders map[string]string // git relay placeholder → its sandbox token
}

func (m memberSessions) AuthenticateSandboxToken(_ context.Context, token string) (SandboxGrant, error) {
	grant, ok := m.grants[token]
	if !ok || strings.HasPrefix(token, GitRelayPlaceholderPrefix) {
		return SandboxGrant{}, ErrTokenInvalid
	}
	return grant, nil
}

func (m memberSessions) RegisterGitRelayPlaceholder(_ context.Context, token, placeholder string) error {
	if _, ok := m.grants[token]; !ok {
		return ErrTokenInvalid
	}
	m.placeholders[placeholder] = token
	return nil
}

func (m memberSessions) AuthenticateGitRelayPlaceholder(_ context.Context, placeholder string) (SandboxGrant, error) {
	token, ok := m.placeholders[placeholder]
	if !ok {
		return SandboxGrant{}, ErrTokenInvalid
	}
	return m.grants[token], nil
}

func (m memberSessions) CodingSession(_ context.Context, _ string, id string) (domain.CodingSession, error) {
	session, ok := m.sessions[id]
	if !ok {
		return domain.CodingSession{}, ErrNotFound
	}
	return session, nil
}

// fakeMembers is a MemberCodeHost with one linked account per user.
type fakeMembers struct {
	mu       sync.Mutex
	accounts map[string]MemberAccount
	tokens   map[string]string // user → short-lived token
	pats     map[string]string // user → long-lived token (GitLab PAT, non-expiring OAuth)
	unlinked map[string]bool   // user → unlinked since the session started
	asked    []string
}

func (f *fakeMembers) MemberAccount(_ context.Context, _, userID, codeHost, hostName string) (MemberAccount, bool, error) {
	account, ok := f.accounts[userID]
	if !ok || account.CodeHost != codeHost || !strings.EqualFold(account.HostName, hostName) {
		return MemberAccount{}, false, nil
	}
	return account, true, nil
}

func (f *fakeMembers) MemberGitCredential(_ context.Context, _, userID string, repo codehost.RepoRef) (codehost.Credential, error) {
	f.mu.Lock()
	f.asked = append(f.asked, userID)
	f.mu.Unlock()
	if f.unlinked[userID] || f.accounts[userID].HostName != repo.HostName {
		return codehost.Credential{}, codehost.ErrUserNotLinked
	}
	if pat, ok := f.pats[userID]; ok {
		// Long-lived: no expiry. CLIEnv is set to prove the service never
		// passes it on.
		return codehost.Credential{Username: f.accounts[userID].Login, Password: pat, CLIEnv: map[string]string{"GITLAB_TOKEN": pat}}, nil
	}
	token, ok := f.tokens[userID]
	if !ok {
		return codehost.Credential{}, codehost.ErrUserNotLinked
	}
	return codehost.Credential{Username: "x-access-token", Password: token, ExpiresAt: time.Now().Add(8 * time.Hour), CLIEnv: map[string]string{"GH_TOKEN": token}}, nil
}

func (f *fakeMembers) MemberActor(_ context.Context, _, userID string, _ codehost.RepoRef) (*codehost.ActingUser, error) {
	if pat, ok := f.pats[userID]; ok {
		return &codehost.ActingUser{FlowUserID: userID, Login: f.accounts[userID].Login, Token: pat}, nil
	}
	token, ok := f.tokens[userID]
	if !ok {
		return nil, nil
	}
	return &codehost.ActingUser{FlowUserID: userID, Login: f.accounts[userID].Login, Token: token, Bearer: true}, nil
}

const testRuntimeURL = "http://flow-runtime:8080"

func memberFixture(phase domain.CodingSessionPhase) (*Service, *mintingHost, *fakeMembers) {
	asMember := func(login string) *domain.CodingPushIdentity {
		return &domain.CodingPushIdentity{Via: domain.CodeHostAccountActorMember, CodeHost: domain.CodeHostGitHub, HostName: "github.com", Login: login}
	}
	sessions := map[string]domain.CodingSession{
		"s-alice": {ID: "s-alice", WorkspaceKey: "w", CreatorID: "alice", SandboxID: "sb-alice", Phase: phase, CodeHost: domain.CodeHostGitHub, HostName: "github.com", Repository: "acme/app", PushIdentity: asMember("alice-gh")},
		"s-bob":   {ID: "s-bob", WorkspaceKey: "w", CreatorID: "bob", SandboxID: "sb-bob", Phase: phase, CodeHost: domain.CodeHostGitHub, HostName: "github.com", Repository: "acme/app", PushIdentity: asMember("bob-gh")},
		"s-carol": {ID: "s-carol", WorkspaceKey: "w", CreatorID: "carol", SandboxID: "sb-carol", Phase: phase, CodeHost: domain.CodeHostGitHub, HostName: "github.com", Repository: "acme/app"},
	}
	grants := map[string]SandboxGrant{}
	for id, session := range sessions {
		grants[SandboxTokenPrefix+id] = SandboxGrant{WorkspaceKey: "w", SandboxID: session.SandboxID, SessionID: id, Kind: sandbox.KindSession}
	}
	members := &fakeMembers{
		accounts: map[string]MemberAccount{
			"alice": {CodeHost: domain.CodeHostGitHub, HostName: "github.com", Login: "alice-gh", ExternalID: "11", Name: "Alice A", Email: "11+alice-gh@users.noreply.github.com", Username: "alice"},
			"bob":   {CodeHost: domain.CodeHostGitHub, HostName: "github.com", Login: "bob-gh", ExternalID: "22", Name: "Bob B", Email: "22+bob-gh@users.noreply.github.com", Username: "bob"},
		},
		tokens: map[string]string{"alice": "ghu_alice_secret", "bob": "ghu_bob_secret"},
	}
	host := &mintingHost{Stub: codehost.Stub{HostKind: domain.CodeHostGitHub}}
	service := &Service{store: memberSessions{grants: grants, sessions: sessions, placeholders: map[string]string{}}, hosts: map[string]codehost.Host{domain.CodeHostGitHub: host}, now: time.Now, Members: members, cfg: config.CodingConfig{RuntimeURL: testRuntimeURL}}
	return service, host, members
}

// relayCredentialRequest is what git asks the helper for the relay push URL
// (credential.useHttpPath: the path without the leading slash).
func relayCredentialRequest(path string) CredentialRequest {
	return CredentialRequest{Protocol: "http", Host: "flow-runtime:8080", Path: "internal/coding-runtime/git/" + path, Purpose: "git"}
}

// The credential helper never gets a member's token: straight to the host
// it gets the workspace's repository-scoped credential (the member's token
// is not even looked up), and for the relay push URL only a placeholder.
func TestCredentialsNeverServeTheMemberToken(t *testing.T) {
	service, host, members := memberFixture(domain.CodingPhaseRunning)
	for _, token := range []string{SandboxTokenPrefix + "s-alice", SandboxTokenPrefix + "s-bob"} {
		response, err := service.Credentials(context.Background(), token, CredentialRequest{Protocol: "https", Host: "github.com", Path: "acme/app.git", Purpose: "git"})
		if err != nil || response.Password != "ghs_minted" || response.CLIEnv != nil {
			t.Fatalf("%s direct: %+v %v", token, response, err)
		}
		relay, err := service.Credentials(context.Background(), token, relayCredentialRequest("github.com/acme/app.git"))
		if err != nil || !strings.HasPrefix(relay.Password, GitRelayPlaceholderPrefix) || relay.Username != GitRelayUsername {
			t.Fatalf("%s relay: %+v %v", token, relay, err)
		}
		if strings.Contains(relay.Password, "ghu_") || strings.Contains(relay.Password, strings.TrimPrefix(token, SandboxTokenPrefix)) {
			t.Fatalf("%s: the placeholder carries a secret: %q", token, relay.Password)
		}
		again, _ := service.Credentials(context.Background(), token, relayCredentialRequest("github.com/acme/app.git"))
		if again.Password != relay.Password {
			t.Fatal("the placeholder must be stable for a sandbox (git asks on every operation)")
		}
	}
	if len(members.asked) != 0 {
		t.Fatalf("a member token was looked up for the sandbox: %v", members.asked)
	}
	if len(host.requests) != 2 {
		t.Fatalf("workspace credentials minted %d, want 2", len(host.requests))
	}
	// The relay URL of another repository or host, a session that does not
	// push as its creator, a snapshot build and the CLI get nothing.
	for name, request := range map[string]CredentialRequest{
		"other repository": relayCredentialRequest("github.com/acme/other.git"),
		"other host":       relayCredentialRequest("evil.example/acme/app.git"),
		"api host":         relayCredentialRequest("api.github.com/acme/app.git"),
		"cli purpose":      {Protocol: "http", Host: "flow-runtime:8080", Path: "internal/coding-runtime/git/github.com/acme/app.git", Purpose: "cli"},
		"other flow host":  {Protocol: "http", Host: "attacker.example", Path: "internal/coding-runtime/git/github.com/acme/app.git", Purpose: "git"},
	} {
		if _, err := service.Credentials(context.Background(), SandboxTokenPrefix+"s-alice", request); !errors.Is(err, ErrForbidden) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if _, err := service.Credentials(context.Background(), SandboxTokenPrefix+"s-carol", relayCredentialRequest("github.com/acme/app.git")); !errors.Is(err, ErrForbidden) {
		t.Fatalf("a bot session has no relay: %v", err)
	}
}

// The relay swaps a session's placeholder for that session creator's own
// token, only for the session's repository and only while it runs. The
// placeholder is not a sandbox token, and a sandbox token is not a
// placeholder.
func TestGitRelayIsBoundToTheSessionRepository(t *testing.T) {
	service, host, _ := memberFixture(domain.CodingPhaseRunning)
	placeholders := map[string]string{}
	for _, id := range []string{"s-alice", "s-bob"} {
		response, err := service.Credentials(context.Background(), SandboxTokenPrefix+id, relayCredentialRequest("github.com/acme/app.git"))
		if err != nil {
			t.Fatal(err)
		}
		placeholders[id] = response.Password
	}
	push := GitRelayPath{HostName: "github.com", Repository: "acme/app", Service: GitServiceReceivePack}
	for id, want := range map[string]string{"s-alice": "ghu_alice_secret", "s-bob": "ghu_bob_secret"} {
		target, err := service.GitRelay(context.Background(), placeholders[id], push)
		if err != nil || target.Password != want || !target.Member || target.HostName != "github.com" || target.Repository != "acme/app" {
			t.Fatalf("%s: %+v %v", id, target, err)
		}
	}
	for name, path := range map[string]GitRelayPath{
		"other repository": {HostName: "github.com", Repository: "acme/other", Service: GitServiceReceivePack},
		"api host":         {HostName: "api.github.com", Repository: "acme/app", Service: GitServiceReceivePack},
	} {
		if _, err := service.GitRelay(context.Background(), placeholders["s-alice"], path); !errors.Is(err, ErrForbidden) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	for name, credential := range map[string]string{"sandbox token": SandboxTokenPrefix + "s-alice", "forged": GitRelayPlaceholderPrefix + "forged", "member token": "ghu_alice_secret"} {
		if _, err := service.GitRelay(context.Background(), credential, push); !errors.Is(err, ErrTokenInvalid) {
			t.Fatalf("%s accepted by the relay: %v", name, err)
		}
	}
	if _, err := service.Authenticate(context.Background(), placeholders["s-alice"]); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("a placeholder must not authenticate the runtime API: %v", err)
	}
	if len(host.requests) != 0 {
		t.Fatalf("workspace credential minted for linked members: %+v", host.requests)
	}
	done, _, _ := memberFixture(domain.CodingPhaseDone)
	done.store.(memberSessions).placeholders[placeholders["s-alice"]] = SandboxTokenPrefix + "s-alice"
	if _, err := done.GitRelay(context.Background(), placeholders["s-alice"], push); !errors.Is(err, ErrGone) {
		t.Fatalf("a finished session's relay: %v", err)
	}
}

func TestParseGitRelayPath(t *testing.T) {
	valid := map[string]GitRelayPath{
		"GET github.com/acme/app.git/info/refs?git-upload-pack":  {HostName: "github.com", Repository: "acme/app", Service: GitServiceUploadPack, Advertisement: true},
		"GET GitHub.com/acme/app.git/info/refs?git-receive-pack": {HostName: "github.com", Repository: "acme/app", Service: GitServiceReceivePack, Advertisement: true},
		"POST github.com/acme/app.git/git-receive-pack?":         {HostName: "github.com", Repository: "acme/app", Service: GitServiceReceivePack},
		"POST gitlab.example/group/sub/app.git/git-upload-pack?": {HostName: "gitlab.example", Repository: "group/sub/app", Service: GitServiceUploadPack},
	}
	parse := func(spec string) (GitRelayPath, bool) {
		method, rest, _ := strings.Cut(spec, " ")
		path, service, _ := strings.Cut(rest, "?")
		return ParseGitRelayPath(method, path, service)
	}
	for spec, want := range valid {
		if got, ok := parse(spec); !ok || got != want {
			t.Fatalf("%s: %+v %v", spec, got, ok)
		}
	}
	for _, spec := range []string{
		"GET github.com/acme/app.git/info/refs?",                   // dumb HTTP
		"GET github.com/acme/app.git/info/refs?git-upload-archive", // other service
		"POST github.com/acme/app.git/info/refs?git-upload-pack",
		"GET github.com/acme/app.git/git-upload-pack?",
		"GET github.com/acme/app.git/objects/info/packs?",
		"GET api.github.com/repos/acme/app?",
		"POST github.com/app.git/git-receive-pack?",               // no owner
		"POST github.com/acme/../other/app.git/git-receive-pack?", // traversal
		"POST github.com//acme/app.git/git-receive-pack?",
		"POST github.com/acme/app/git-receive-pack?",
	} {
		if got, ok := parse(spec); ok {
			t.Fatalf("%s parsed as %+v", spec, got)
		}
	}
}

// gh / glab get the workspace credential: the runner writes the CLI env to
// disk, so a member token is never served for it.
func TestCredentialsCLIPurposeNeverGetsTheMemberToken(t *testing.T) {
	service, host, members := memberFixture(domain.CodingPhaseRunning)
	response, err := service.Credentials(context.Background(), SandboxTokenPrefix+"s-alice", CredentialRequest{Protocol: "https", Host: "github.com", Purpose: "cli"})
	if err != nil || response.Password != "ghs_minted" || len(host.requests) != 1 || len(members.asked) != 0 {
		t.Fatalf("cli credential %+v %v (minted %d, member lookups %v)", response, err, len(host.requests), members.asked)
	}
}

// Without a linked account (or after the member unlinked it), the session
// falls back to the workspace installation / project token.
func TestCredentialsFallBackToTheWorkspaceToken(t *testing.T) {
	service, host, members := memberFixture(domain.CodingPhaseRunning)
	members.unlinked = map[string]bool{"bob": true}
	for _, token := range []string{SandboxTokenPrefix + "s-carol", SandboxTokenPrefix + "s-bob"} {
		response, err := service.Credentials(context.Background(), token, CredentialRequest{Protocol: "https", Host: "github.com", Path: "acme/app", Purpose: "git"})
		if err != nil || response.Password != "ghs_minted" {
			t.Fatalf("%s: %+v %v", token, response, err)
		}
	}
	if len(host.requests) != 2 || host.requests[0].Access != codehost.AccessWrite {
		t.Fatalf("mint requests %+v", host.requests)
	}
	// Bob pushes through the relay, but he unlinked his account since the
	// session started: the relay forwards with the workspace credential.
	relay, err := service.Credentials(context.Background(), SandboxTokenPrefix+"s-bob", relayCredentialRequest("github.com/acme/app.git"))
	if err != nil {
		t.Fatal(err)
	}
	target, err := service.GitRelay(context.Background(), relay.Password, GitRelayPath{HostName: "github.com", Repository: "acme/app", Service: GitServiceReceivePack})
	if err != nil || target.Password != "ghs_minted" || target.Member {
		t.Fatalf("relay fallback %+v %v", target, err)
	}
}

// A member linked with a long-lived token (a GitLab personal access token)
// pushes as themselves through the relay: the relay forwards with the PAT,
// while the credential helper (git and CLI), the CLI env and the bootstrap
// never carry it.
func TestGitRelayForwardsALongLivedMemberToken(t *testing.T) {
	service, _, members := memberFixture(domain.CodingPhaseRunning)
	const pat = "glpat-dave-secret"
	sessions := service.store.(memberSessions)
	sessions.sessions["s-dave"] = domain.CodingSession{ID: "s-dave", WorkspaceKey: "w", CreatorID: "dave", SandboxID: "sb-dave", Phase: domain.CodingPhaseRunning, CodeHost: domain.CodeHostGitLab, HostName: "gitlab.example", Repository: "acme/api",
		BranchName: "dave/eng-2-x", PushIdentity: &domain.CodingPushIdentity{Via: domain.CodeHostAccountActorMember, CodeHost: domain.CodeHostGitLab, HostName: "gitlab.example", Login: "dave-gl"}}
	sessions.grants[SandboxTokenPrefix+"s-dave"] = SandboxGrant{WorkspaceKey: "w", SandboxID: "sb-dave", SessionID: "s-dave", Kind: sandbox.KindSession}
	members.accounts["dave"] = MemberAccount{CodeHost: domain.CodeHostGitLab, HostName: "gitlab.example", Login: "dave-gl", ExternalID: "33", Name: "Dave D", Email: "33-dave-gl@users.noreply.gitlab.example", Username: "dave"}
	members.pats = map[string]string{"dave": pat}
	gitlabHost := &mintingHost{Stub: codehost.Stub{HostKind: domain.CodeHostGitLab}}
	service.hosts[domain.CodeHostGitLab] = gitlabHost
	token := SandboxTokenPrefix + "s-dave"

	for _, request := range []CredentialRequest{
		{Protocol: "https", Host: "gitlab.example", Path: "acme/api.git", Purpose: "git"},
		{Protocol: "https", Host: "gitlab.example", Purpose: "cli"},
		relayCredentialRequest("gitlab.example/acme/api.git"),
	} {
		response, err := service.Credentials(context.Background(), token, request)
		raw, _ := json.Marshal(response)
		if err != nil || strings.Contains(string(raw), pat) {
			t.Fatalf("%+v: %s %v", request, raw, err)
		}
	}
	relay, _ := service.Credentials(context.Background(), token, relayCredentialRequest("gitlab.example/acme/api.git"))
	if !strings.HasPrefix(relay.Password, GitRelayPlaceholderPrefix) {
		t.Fatalf("relay placeholder %+v", relay)
	}
	minted := len(gitlabHost.requests)
	target, err := service.GitRelay(context.Background(), relay.Password, GitRelayPath{HostName: "gitlab.example", Repository: "acme/api", Service: GitServiceReceivePack})
	if err != nil || target.Password != pat || target.Username != "dave-gl" || !target.Member || target.CodeHost != domain.CodeHostGitLab {
		t.Fatalf("relay target %+v %v", target, err)
	}
	if len(gitlabHost.requests) != minted {
		t.Fatal("the relay minted a workspace credential although the member's PAT is usable")
	}
	credential, ok, err := service.memberGitCredential(context.Background(), sessions.sessions["s-dave"], "git", codehost.AccessWrite)
	if err != nil || !ok || credential.CLIEnv != nil {
		t.Fatalf("the CLI env must never carry the member's token: %+v %v %v", credential, ok, err)
	}
	if _, ok, _ := service.memberGitCredential(context.Background(), sessions.sessions["s-dave"], "cli", codehost.AccessWrite); ok {
		t.Fatal("a CLI request got the member's token")
	}

	service.workspaces = staticWorkspaces{}
	response, err := service.buildBootstrap(context.Background(), SandboxGrant{SandboxID: "sb-dave", SessionID: "s-dave"}, sessions.sessions["s-dave"], nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(response)
	if strings.Contains(string(raw), pat) {
		t.Fatalf("bootstrap leaks the PAT: %s", raw)
	}
	if response.Repo.PushURL != testRuntimeURL+"/internal/coding-runtime/git/gitlab.example/acme/api.git" {
		t.Fatalf("PAT member session must push through the relay: %+v", response.Repo)
	}
}

// Serving stops when the session ends: a done session's sandbox token gets
// nothing, and the member's token is not even looked up.
func TestCredentialsStopAtSessionEnd(t *testing.T) {
	service, host, members := memberFixture(domain.CodingPhaseDone)
	_, err := service.Credentials(context.Background(), SandboxTokenPrefix+"s-alice", CredentialRequest{Protocol: "https", Host: "github.com", Path: "acme/app", Purpose: "git"})
	if !errors.Is(err, ErrGone) || len(members.asked) != 0 || len(host.requests) != 0 {
		t.Fatalf("done session: err %v, member lookups %v, minted %d", err, members.asked, len(host.requests))
	}
}

// Another repository or host is refused before any token is looked up.
func TestCredentialsRefuseOtherRepositories(t *testing.T) {
	service, _, members := memberFixture(domain.CodingPhaseRunning)
	for _, request := range []CredentialRequest{{Host: "github.com", Path: "acme/other.git"}, {Host: "evil.example", Path: "acme/app.git"}} {
		if _, err := service.Credentials(context.Background(), SandboxTokenPrefix+"s-alice", request); !errors.Is(err, ErrForbidden) {
			t.Fatalf("%+v: %v", request, err)
		}
	}
	if len(members.asked) != 0 {
		t.Fatalf("member token looked up for a refused request: %v", members.asked)
	}
}

type staticWorkspaces struct{}

func (staticWorkspaces) CodingWorkspace(context.Context, string) (WorkspaceInfo, error) {
	return WorkspaceInfo{}, nil
}

// The bootstrap (the sandbox's whole spec and environment) carries the
// member's commit identity and branch prefix but never their token.
func TestBootstrapUsesMemberIdentityWithoutTheToken(t *testing.T) {
	service, _, _ := memberFixture(domain.CodingPhaseRunning)
	service.workspaces = staticWorkspaces{}
	session := domain.CodingSession{ID: "s-alice", WorkspaceKey: "w", CreatorID: "alice", SandboxID: "sb-alice", CodeHost: domain.CodeHostGitHub, HostName: "github.com", Repository: "acme/app",
		BranchName: "alice/eng-1-fix-abc123", PushIdentity: &domain.CodingPushIdentity{Via: domain.CodeHostAccountActorMember, Login: "alice-gh"}}
	response, err := service.buildBootstrap(context.Background(), SandboxGrant{SandboxID: "sb-alice", SessionID: "s-alice"}, session, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(response)
	if strings.Contains(string(raw), "ghu_alice_secret") || strings.Contains(string(raw), "ghu_bob_secret") {
		t.Fatalf("bootstrap leaks a token: %s", raw)
	}
	git := response.Git
	if git.AuthorName != "Alice A" || git.AuthorEmail != "11+alice-gh@users.noreply.github.com" || git.CommitterName != "Alice A" || git.CommitterEmail != git.AuthorEmail || git.CoAuthorTrailer != "" {
		t.Fatalf("member git identity %+v", git)
	}
	if response.Repo.BranchPrefix != "alice/" {
		t.Fatalf("branch prefix %q", response.Repo.BranchPrefix)
	}
	if response.Repo.PushURL != testRuntimeURL+"/internal/coding-runtime/git/github.com/acme/app.git" || response.Repo.CloneURL != "https://github.com/acme/app.git" {
		t.Fatalf("member session pushes through the relay, fetches from the host: %+v", response.Repo)
	}
	// Unlinked creator: the member is the author, the bot commits.
	carol := domain.CodingSession{ID: "s-carol", WorkspaceKey: "w", CreatorID: "carol", CodeHost: domain.CodeHostGitHub, HostName: "github.com", Repository: "acme/app", BranchName: "flow/eng-1-x"}
	fallback, err := service.buildBootstrap(context.Background(), SandboxGrant{SandboxID: "sb-carol", SessionID: "s-carol"}, carol, nil)
	if err != nil || fallback.Git.CommitterName != "Flow" || fallback.Repo.BranchPrefix != "flow/" || fallback.Repo.PushURL != "" {
		t.Fatalf("fallback identity %+v %v", fallback.Git, err)
	}
}

type publishingHost struct {
	codehost.Stub
	inputs []codehost.CreateChangeRequestInput
}

func (p *publishingHost) BranchHead(context.Context, string, codehost.RepoRef, string) (string, error) {
	return "abc", nil
}
func (p *publishingHost) FindChangeRequest(context.Context, string, codehost.RepoRef, string) (*codehost.ChangeRequest, error) {
	return nil, nil
}
func (p *publishingHost) CreateChangeRequest(_ context.Context, _ string, input codehost.CreateChangeRequestInput) (codehost.ChangeRequest, error) {
	p.inputs = append(p.inputs, input)
	change := codehost.ChangeRequest{Repo: input.Repo, Number: 7, URL: "https://github.com/acme/app/pull/7", State: "open", HeadBranch: input.Head, AuthorLogin: "flow-dev[bot]"}
	if input.ActAs != nil {
		change.AuthorLogin, change.OpenedByMember = input.ActAs.Login, true
	}
	return change, nil
}

// Flow opens the PR as the creator with their own token; without a link the
// workspace App opens it and the PR says so.
func TestPublishOpensThePullRequestAsTheCreator(t *testing.T) {
	service, _, _ := memberFixture(domain.CodingPhaseRunning)
	host := &publishingHost{Stub: codehost.Stub{HostKind: domain.CodeHostGitHub}}
	service.hosts = map[string]codehost.Host{domain.CodeHostGitHub: host}
	alice := domain.CodingSession{ID: "s-alice", WorkspaceKey: "w", CreatorID: "alice", CodeHost: domain.CodeHostGitHub, HostName: "github.com", Repository: "acme/app", BranchName: "alice/x", BaseRef: "main"}
	outcome := service.publish(context.Background(), alice)
	if outcome.PullRequest == nil || outcome.PullRequest.OpenedVia != domain.CodeHostAccountActorMember || outcome.PullRequest.OpenedAs != "alice-gh" {
		t.Fatalf("member publish %+v", outcome)
	}
	if host.inputs[0].ActAs == nil || host.inputs[0].ActAs.FlowUserID != "alice" || host.inputs[0].ActAs.Token != "ghu_alice_secret" {
		t.Fatalf("acting user %+v", host.inputs[0].ActAs)
	}
	carol := alice
	carol.ID, carol.CreatorID = "s-carol", "carol"
	outcome = service.publish(context.Background(), carol)
	if outcome.PullRequest == nil || outcome.PullRequest.OpenedVia != domain.CodeHostAccountActorBot || host.inputs[1].ActAs != nil {
		t.Fatalf("fallback publish %+v %+v", outcome, host.inputs[1].ActAs)
	}
}

// A member resolver that returns someone else's token is ignored.
type wrongMembers struct{ fakeMembers }

func (w *wrongMembers) MemberActor(context.Context, string, string, codehost.RepoRef) (*codehost.ActingUser, error) {
	return &codehost.ActingUser{FlowUserID: "mallory", Token: "ghu_other"}, nil
}

func TestMemberActorMustBeTheCreator(t *testing.T) {
	service := &Service{Members: &wrongMembers{}}
	if actor := service.memberActor(context.Background(), domain.CodingSession{ID: "s", CreatorID: "alice"}, codehost.RepoRef{}); actor != nil {
		t.Fatalf("another member's token was accepted: %+v", actor)
	}
}

func TestMemberBranchesAndNoreplyEmails(t *testing.T) {
	if got := HostNoreplyEmail(domain.CodeHostGitHub, "github.com", "11", "alice"); got != "11+alice@users.noreply.github.com" {
		t.Fatal(got)
	}
	if got := HostNoreplyEmail(domain.CodeHostGitLab, "gitlab.example.com:8443", "5", "al"); got != "5-al@users.noreply.gitlab.example.com" {
		t.Fatal(got)
	}
	member := domain.CodingSession{BranchName: "alice/eng-1-x", PushIdentity: &domain.CodingPushIdentity{Via: domain.CodeHostAccountActorMember}}
	if sessionBranchPrefix(member, "flow/") != "alice/" {
		t.Fatal("member prefix")
	}
	explicit := domain.CodingSession{BranchName: "feature/x"}
	if sessionBranchPrefix(explicit, "flow/") != "flow/" {
		t.Fatal("a non-member session keeps the deployment prefix")
	}
	linked := []domain.CodingSession{{BranchName: "alice/eng-1-x"}}
	if !flowBranch("alice/eng-1-x", "flow/", linked) || flowBranch("alice/other", "flow/", linked) || !flowBranch("flow/y", "flow/", nil) {
		t.Fatal("flowBranch")
	}
}
