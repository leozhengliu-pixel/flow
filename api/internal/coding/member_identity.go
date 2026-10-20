package coding

import (
	"context"
	"errors"
	"log"
	"strings"

	"flow/api/internal/codehost"
	"flow/api/internal/domain"
)

// Pushing as the member (docs/coding-sessions-design.md §13). When the
// session's creator linked their GitHub or GitLab account, the session's
// branch carries their prefix, its commits are authored and committed by
// them (display name + host email), its pushes go through Flow's git relay,
// which adds their own token (OAuth, GitHub App user token, or a GitLab
// personal access token) for the session's one repository server side
// (git_relay.go; the sandbox only ever sees a placeholder), and
// Flow opens the PR/MR with their token. Without a linked account everything
// falls back to the workspace App / connection credential, with the member
// still the commit author. The bot identity only posts Flow's back-links.

// creatorAccount is the creator's linked account on the environment's host
// (the GitHub App host when the environment is not chosen yet).
func (s *Service) creatorAccount(ctx context.Context, workspaceKey, userID, environmentID string) (MemberAccount, bool) {
	if s.Members == nil || userID == "" {
		return MemberAccount{}, false
	}
	codeHost, hostName := domain.CodeHostGitHub, s.cfg.GitHubApp.Host
	if environmentID != "" && s.envs != nil {
		if env, err := s.envs.CodingEnvironment(ctx, workspaceKey, environmentID); err == nil {
			codeHost, hostName = fallback(env.CodeHost, domain.CodeHostGitHub), env.HostName
		}
	}
	if codeHost == domain.CodeHostGitHub && hostName == "" {
		hostName = "github.com"
	}
	return s.memberAccount(ctx, workspaceKey, userID, codeHost, hostName)
}

func (s *Service) memberAccount(ctx context.Context, workspaceKey, userID, codeHost, hostName string) (MemberAccount, bool) {
	if s.Members == nil || userID == "" {
		return MemberAccount{}, false
	}
	account, ok, err := s.Members.MemberAccount(ctx, workspaceKey, userID, fallback(codeHost, domain.CodeHostGitHub), hostName)
	if err != nil {
		log.Printf("coding sessions: linked account of %s: %v", userID, err)
		return MemberAccount{}, false
	}
	return account, ok
}

// memberGitCredential is the session creator's own credential for the git
// relay to forward the session's pushes with (server side only; it is never
// returned to a sandbox). ok is false when the workspace credential must be
// used instead (no linked account, a snapshot build or a gh/glab CLI
// request). Long-lived tokens (personal access tokens, non-expiring OAuth
// tokens) are allowed: this credential only ever leaves Flow inside the
// relayed request to the code host.
func (s *Service) memberGitCredential(ctx context.Context, session domain.CodingSession, purpose string, access codehost.Access) (codehost.Credential, bool, error) {
	if s.Members == nil || session.CreatorID == "" || session.ID == "" || purpose == "cli" || access != codehost.AccessWrite {
		return codehost.Credential{}, false, nil
	}
	repo := codehost.RepoRef{CodeHost: session.CodeHost, HostName: session.HostName, FullName: session.Repository}
	credential, err := s.Members.MemberGitCredential(ctx, session.WorkspaceKey, session.CreatorID, repo)
	if errors.Is(err, codehost.ErrUserNotLinked) {
		return codehost.Credential{}, false, nil
	}
	if err != nil {
		return codehost.Credential{}, false, err
	}
	if credential.Password == "" {
		return codehost.Credential{}, false, nil
	}
	// Only git gets it; the CLI env the runner persists never carries it.
	credential.CLIEnv = nil
	return credential, true, nil
}

// memberActor is the creator's token for opening the PR/MR as them.
func (s *Service) memberActor(ctx context.Context, session domain.CodingSession, repo codehost.RepoRef) *codehost.ActingUser {
	if s.Members == nil || session.CreatorID == "" {
		return nil
	}
	actor, err := s.Members.MemberActor(ctx, session.WorkspaceKey, session.CreatorID, repo)
	if err != nil {
		log.Printf("coding session %s: acting as the creator: %v", session.ID, err)
		return nil
	}
	if actor != nil && actor.FlowUserID != session.CreatorID {
		return nil // never another member's token
	}
	return actor
}

// memberGitIdentity is the commit identity when pushing as the member:
// author and committer are the member (their display name and host email).
// Signed commits keep the member's signing key.
func memberGitIdentity(account MemberAccount, user UserInfo, signed bool) BootstrapGit {
	name := fallback(account.Name, fallback(user.Name, account.Login))
	git := BootstrapGit{AuthorName: name, AuthorEmail: account.Email, CommitterName: name, CommitterEmail: account.Email}
	if signed && user.SigningFormat != "" {
		git.SigningFormat, git.SigningPublicKey = user.SigningFormat, user.SigningPublicKey
	}
	return git
}

// HostNoreplyEmail is the host's private commit email for an account:
// GitHub <id>+<login>@users.noreply.<host>, GitLab
// <id>-<username>@users.noreply.<host>.
func HostNoreplyEmail(codeHost, hostName, externalID, login string) string {
	hostName = strings.ToLower(strings.TrimSpace(hostName))
	if index := strings.LastIndex(hostName, ":"); index > 0 {
		hostName = hostName[:index]
	}
	if login == "" || hostName == "" {
		return ""
	}
	if codeHost == domain.CodeHostGitLab {
		if externalID == "" {
			return login + "@users.noreply." + hostName
		}
		return externalID + "-" + login + "@users.noreply." + hostName
	}
	if externalID == "" {
		return login + "@users.noreply." + hostName
	}
	return externalID + "+" + login + "@users.noreply." + hostName
}

// flowBranch reports whether a branch is one Flow created: under the
// deployment prefix, or the branch of a session linked to the PR/MR (member
// branches carry the member's own prefix).
func flowBranch(branch, prefix string, linked []domain.CodingSession) bool {
	if prefix != "" && strings.HasPrefix(branch, prefix) {
		return true
	}
	for _, session := range linked {
		if session.BranchName != "" && session.BranchName == branch && !session.ExistingBranch {
			return true
		}
	}
	return false
}

// sessionBranchPrefix is the prefix the runner may push under: the
// deployment prefix, or the member prefix ("<username>/") of a session that
// pushes as its creator.
func sessionBranchPrefix(session domain.CodingSession, deploymentPrefix string) string {
	if session.ExistingBranch || strings.HasPrefix(session.BranchName, deploymentPrefix) || session.PushIdentity == nil || session.PushIdentity.Via != domain.CodeHostAccountActorMember {
		return deploymentPrefix
	}
	if index := strings.Index(session.BranchName, "/"); index > 0 {
		return session.BranchName[:index+1]
	}
	return deploymentPrefix
}

// sessionGitIdentity is the bootstrap commit identity: the member's own
// identity when they linked an account on the session's host, otherwise
// the member as author and the bot as committer (gitIdentity).
func (s *Service) sessionGitIdentity(ctx context.Context, session domain.CodingSession, user UserInfo, signed bool) BootstrapGit {
	if account, ok := s.memberAccount(ctx, session.WorkspaceKey, session.CreatorID, session.CodeHost, session.HostName); ok && account.Email != "" {
		return memberGitIdentity(account, user, signed)
	}
	return s.gitIdentity(session, user, signed)
}
