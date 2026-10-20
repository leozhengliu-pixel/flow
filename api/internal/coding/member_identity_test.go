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
	"flow/api/internal/domain"
	"flow/api/internal/sandbox"
)

// memberSessions serves sessions per sandbox token: each token is one
// sandbox of one session with its own creator.
type memberSessions struct {
	CoreStore
	grants   map[string]SandboxGrant
	sessions map[string]domain.CodingSession
}

func (m memberSessions) AuthenticateSandboxToken(_ context.Context, token string) (SandboxGrant, error) {
	grant, ok := m.grants[token]
	if !ok {
		return SandboxGrant{}, ErrTokenInvalid
	}
	return grant, nil
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
	mu        sync.Mutex
	accounts  map[string]MemberAccount
	tokens    map[string]string // user → short-lived token
	longLived map[string]bool   // user → only a long-lived token (PAT)
	asked     []string
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
	token, ok := f.tokens[userID]
	if !ok || f.longLived[userID] || f.accounts[userID].HostName != repo.HostName {
		return codehost.Credential{}, codehost.ErrUserNotLinked
	}
	return codehost.Credential{Username: "x-access-token", Password: token, ExpiresAt: time.Now().Add(8 * time.Hour), CLIEnv: map[string]string{"GH_TOKEN": token}}, nil
}

func (f *fakeMembers) MemberActor(_ context.Context, _, userID string, _ codehost.RepoRef) (*codehost.ActingUser, error) {
	token, ok := f.tokens[userID]
	if !ok {
		return nil, nil
	}
	return &codehost.ActingUser{FlowUserID: userID, Login: f.accounts[userID].Login, Token: token, Bearer: true}, nil
}

func memberFixture(phase domain.CodingSessionPhase) (*Service, *mintingHost, *fakeMembers) {
	sessions := map[string]domain.CodingSession{
		"s-alice": {ID: "s-alice", WorkspaceKey: "w", CreatorID: "alice", SandboxID: "sb-alice", Phase: phase, CodeHost: domain.CodeHostGitHub, HostName: "github.com", Repository: "acme/app"},
		"s-bob":   {ID: "s-bob", WorkspaceKey: "w", CreatorID: "bob", SandboxID: "sb-bob", Phase: phase, CodeHost: domain.CodeHostGitHub, HostName: "github.com", Repository: "acme/app"},
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
	service := &Service{store: memberSessions{grants: grants, sessions: sessions}, hosts: map[string]codehost.Host{domain.CodeHostGitHub: host}, now: time.Now, Members: members}
	return service, host, members
}

// Each session's git helper gets its own creator's token, never another
// member's, and the workspace token is not minted for them.
func TestCredentialsServeOnlyTheCreatorsOwnToken(t *testing.T) {
	service, host, members := memberFixture(domain.CodingPhaseRunning)
	request := CredentialRequest{Protocol: "https", Host: "github.com", Path: "acme/app.git", Purpose: "git"}
	for token, want := range map[string]string{SandboxTokenPrefix + "s-alice": "ghu_alice_secret", SandboxTokenPrefix + "s-bob": "ghu_bob_secret"} {
		response, err := service.Credentials(context.Background(), token, request)
		if err != nil || response.Password != want {
			t.Fatalf("%s: got %q %v, want %q", token, response.Password, err, want)
		}
		if response.CLIEnv != nil {
			t.Fatalf("%s: a git credential must not carry CLI env", token)
		}
	}
	if len(host.requests) != 0 {
		t.Fatalf("the workspace token was minted for linked members: %+v", host.requests)
	}
	if strings.Join(members.asked, ",") != "alice,bob" && strings.Join(members.asked, ",") != "bob,alice" {
		t.Fatalf("member lookups %v", members.asked)
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

// Without a linked account (or with only a long-lived token), the session
// falls back to the workspace installation / project token.
func TestCredentialsFallBackToTheWorkspaceToken(t *testing.T) {
	service, host, members := memberFixture(domain.CodingPhaseRunning)
	members.longLived = map[string]bool{"bob": true}
	for _, token := range []string{SandboxTokenPrefix + "s-carol", SandboxTokenPrefix + "s-bob"} {
		response, err := service.Credentials(context.Background(), token, CredentialRequest{Protocol: "https", Host: "github.com", Path: "acme/app", Purpose: "git"})
		if err != nil || response.Password != "ghs_minted" {
			t.Fatalf("%s: %+v %v", token, response, err)
		}
	}
	if len(host.requests) != 2 || host.requests[0].Access != codehost.AccessWrite {
		t.Fatalf("mint requests %+v", host.requests)
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
	// Unlinked creator: the member is the author, the bot commits.
	carol := domain.CodingSession{ID: "s-carol", WorkspaceKey: "w", CreatorID: "carol", CodeHost: domain.CodeHostGitHub, HostName: "github.com", Repository: "acme/app", BranchName: "flow/eng-1-x"}
	fallback, err := service.buildBootstrap(context.Background(), SandboxGrant{SandboxID: "sb-carol", SessionID: "s-carol"}, carol, nil)
	if err != nil || fallback.Git.CommitterName != "Flow" || fallback.Repo.BranchPrefix != "flow/" {
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
