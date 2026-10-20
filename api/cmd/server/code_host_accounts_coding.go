package main

import (
	"context"
	"strings"
	"time"

	"flow/api/internal/codehost"
	"flow/api/internal/coding"
	"flow/api/internal/domain"
)

// codingMemberHosts gives coding sessions the creator's own linked account
// (coding.MemberCodeHost). Every lookup is keyed by the session creator the
// coding service passes; nothing here can return another member's token.
//
// Credential delivery follows docs/coding-sessions-security.md: the
// member's token reaches the sandbox only through the git credential helper
// (fetched per git operation from Flow's runtime API with the sandbox
// token, never in the environment or on disk), only for the session's one
// repository, only while the session runs, and only when it is short-lived
// (GitHub App user tokens, GitLab OAuth tokens). Personal access tokens and
// non-expiring OAuth tokens stay server side: Flow uses them to open the
// PR/MR as the member, while git pushes with the workspace credential.
type codingMemberHosts struct{ s *server }

var _ coding.MemberCodeHost = codingMemberHosts{}

func (m codingMemberHosts) MemberAccount(ctx context.Context, workspaceKey, userID, codeHost, hostName string) (coding.MemberAccount, bool, error) {
	account, _, ok := m.s.workspaceMemberAccount(ctx, workspaceKey, userID, codeHost, hostName)
	if !ok {
		return coding.MemberAccount{}, false, nil
	}
	result := coding.MemberAccount{CodeHost: account.CodeHost, HostName: account.HostName, Login: account.Login, ExternalID: account.ExternalUserID}
	result.Email = strings.TrimSpace(account.Email)
	if result.Email == "" {
		result.Email = coding.HostNoreplyEmail(account.CodeHost, account.HostName, account.ExternalUserID, account.Login)
	}
	data, found := m.s.store.WorkspaceMetadataFields(workspaceKey, "users", "members", "userSettings")
	if found {
		applyUsernames(&data)
		for _, user := range bootstrapUsers(&data) {
			if user.ID == userID {
				result.Name = firstNonEmpty(strings.TrimSpace(user.DisplayName), strings.TrimSpace(user.Name))
				result.Username = user.Username
				break
			}
		}
	}
	if result.Name == "" {
		if user, err := m.s.store.UserByID(ctx, userID); err == nil {
			result.Name = firstNonEmpty(user.DisplayName, user.Name)
		}
	}
	return result, true, nil
}

func (m codingMemberHosts) MemberGitCredential(ctx context.Context, workspaceKey, userID string, repo codehost.RepoRef) (codehost.Credential, error) {
	account, _, ok := m.s.workspaceMemberAccount(ctx, workspaceKey, userID, firstNonEmpty(repo.CodeHost, domain.CodeHostGitHub), repo.HostName)
	if !ok || account.UserID != userID {
		return codehost.Credential{}, codehost.ErrUserNotLinked
	}
	if account.AuthType == domain.CodeHostAuthPAT {
		return codehost.Credential{}, codehost.ErrUserNotLinked // never in a sandbox
	}
	tokens, err := m.s.memberHostTokens(ctx, account)
	if err != nil {
		return codehost.Credential{}, err
	}
	if tokens.PrivateToken || tokens.ExpiresAt.IsZero() || time.Until(tokens.ExpiresAt) > codeHostSandboxTokenMaxLifetime {
		return codehost.Credential{}, codehost.ErrUserNotLinked // long-lived: server side only
	}
	username := "x-access-token"
	if account.CodeHost == domain.CodeHostGitLab {
		username = "oauth2"
	}
	return codehost.Credential{Username: username, Password: tokens.AccessToken, ExpiresAt: tokens.ExpiresAt}, nil
}

func (m codingMemberHosts) MemberActor(ctx context.Context, workspaceKey, userID string, repo codehost.RepoRef) (*codehost.ActingUser, error) {
	account, _, ok := m.s.workspaceMemberAccount(ctx, workspaceKey, userID, firstNonEmpty(repo.CodeHost, domain.CodeHostGitHub), repo.HostName)
	if !ok || account.UserID != userID {
		return nil, nil
	}
	tokens, err := m.s.memberHostTokens(ctx, account)
	if err != nil {
		if err == codehost.ErrUserNotLinked {
			return nil, nil
		}
		return nil, err
	}
	return &codehost.ActingUser{FlowUserID: userID, Login: account.Login, Token: tokens.AccessToken, Bearer: !tokens.PrivateToken}, nil
}
