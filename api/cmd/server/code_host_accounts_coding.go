package main

import (
	"context"
	"strings"

	"flow/api/internal/codehost"
	"flow/api/internal/coding"
	"flow/api/internal/domain"
)

// codingMemberHosts gives coding sessions the creator's own linked account
// (coding.MemberCodeHost). Every lookup is keyed by the session creator the
// coding service passes; nothing here can return another member's token.
//
// Credential use follows docs/coding-sessions-security.md: the member's
// token never reaches the sandbox. Flow's git relay (coding_git_relay.go)
// adds it server side to the session's own smart-HTTP requests for its one
// repository, and only while the session runs; the sandbox's credential
// helper only gets an opaque placeholder for the relay URL. Every kind of
// member token is used this way: GitHub App user tokens, GitLab OAuth tokens,
// and also GitLab personal access tokens and non-expiring OAuth tokens. The
// long-lived ones are safe here because they never leave Flow: no sandbox
// env, file, bootstrap, credential-helper answer or gh/glab CLI env carries
// them.
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
	tokens, err := m.s.memberHostTokens(ctx, account)
	if err != nil {
		return codehost.Credential{}, err
	}
	if tokens.AccessToken == "" {
		return codehost.Credential{}, codehost.ErrUserNotLinked
	}
	// The relay is this credential's only consumer (coding.GitRelay), so a
	// personal access token or non-expiring OAuth token is allowed: it is
	// added to the forwarded request server side and never reaches the
	// sandbox. ExpiresAt is zero for those.
	username := "x-access-token"
	if account.CodeHost == domain.CodeHostGitLab {
		username = "oauth2"
		if tokens.PrivateToken || account.AuthType == domain.CodeHostAuthPAT {
			// GitLab accepts a personal access token as the HTTP Basic
			// password with any non-empty username.
			username = firstNonEmpty(account.Login, "oauth2")
		}
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
