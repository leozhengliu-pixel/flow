package main

import (
	"net/http"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

type inviteLinkPreview struct {
	Token               string           `json:"token"`
	Workspace           domain.Workspace `json:"workspace"`
	AllowedAuthServices []string         `json:"allowedAuthServices"`
	AlreadyMember       bool             `json:"alreadyMember"`
}

func (s *server) getWorkspaceInviteLink(w http.ResponseWriter, r *http.Request) {
	data, ok := s.store.WorkspaceMetadata(r.PathValue("workspaceKey"))
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	if !data.WorkspaceSettings.InviteLinksEnabled {
		writeError(w, http.StatusForbidden, "Workspace invite links are disabled")
		return
	}
	if data.WorkspaceInviteLink == nil || !data.WorkspaceInviteLink.Enabled {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
		return
	}
	writeJSON(w, http.StatusOK, data.WorkspaceInviteLink)
}

func (s *server) createOrRotateWorkspaceInviteLink(w http.ResponseWriter, r *http.Request) {
	data, ok := s.store.WorkspaceMetadata(r.PathValue("workspaceKey"))
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	if !data.WorkspaceSettings.InviteLinksEnabled {
		writeError(w, http.StatusForbidden, "Workspace invite links are disabled")
		return
	}
	var created domain.WorkspaceInviteLink
	err := s.store.MutateWorkspace(r.Context(), r.PathValue("workspaceKey"), "workspace_invite_link.rotated", "", nil, func(workspace *domain.Bootstrap) error {
		if !workspace.WorkspaceSettings.InviteLinksEnabled {
			return errInvalid
		}
		now := time.Now().UTC()
		createdBy := ""
		if user := authUser(r); user.ID != "" {
			createdBy = user.ID
		} else if workspace.Viewer.ID != "" {
			createdBy = workspace.Viewer.ID
		}
		created = domain.WorkspaceInviteLink{
			Token:     randomURLToken(24),
			Enabled:   true,
			CreatedBy: createdBy,
			CreatedAt: now,
			UpdatedAt: now,
		}
		workspace.WorkspaceInviteLink = &created
		return nil
	})
	respondMutation(w, err, http.StatusOK, created)
}

func (s *server) disableWorkspaceInviteLink(w http.ResponseWriter, r *http.Request) {
	err := s.store.MutateWorkspace(r.Context(), r.PathValue("workspaceKey"), "workspace_invite_link.disabled", "", nil, func(workspace *domain.Bootstrap) error {
		if workspace.WorkspaceInviteLink == nil {
			return errNotFound
		}
		workspace.WorkspaceInviteLink.Enabled = false
		workspace.WorkspaceInviteLink.UpdatedAt = time.Now().UTC()
		return nil
	})
	respondMutation(w, err, http.StatusNoContent, nil)
}

func (s *server) inviteLinkPreview(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.PathValue("token"))
	if token == "" {
		writeError(w, http.StatusNotFound, "This invite link is invalid or has expired")
		return
	}
	for _, key := range s.store.WorkspaceKeys() {
		data, ok := s.store.BootstrapFor(key)
		if !ok || data.WorkspaceInviteLink == nil {
			continue
		}
		materializeDevelopmentMembers(&data)
		link := data.WorkspaceInviteLink
		if link.Token != token || !link.Enabled || !data.WorkspaceSettings.InviteLinksEnabled {
			continue
		}
		allowed := []string{}
		if data.WorkspaceSettings.GoogleAuthEnabled {
			allowed = append(allowed, "google")
		}
		if data.WorkspaceSettings.EmailAuthEnabled {
			allowed = append(allowed, "email")
		}
		already := false
		user := authUser(r)
		if user.ID == "" && s.authDisabled {
			user = data.Viewer
		}
		if user.ID != "" {
			already = slices.ContainsFunc(data.Members, func(member domain.WorkspaceMember) bool {
				return (member.User.ID == user.ID || strings.EqualFold(member.User.Email, user.Email)) && member.Status != "suspended"
			})
		}
		writeJSON(w, http.StatusOK, inviteLinkPreview{
			Token:               token,
			Workspace:           data.Workspace,
			AllowedAuthServices: allowed,
			AlreadyMember:       already,
		})
		return
	}
	writeError(w, http.StatusNotFound, "This invite link is invalid or has expired")
}

func (s *server) joinOrganization(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Token string `json:"token"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Token = strings.TrimSpace(input.Token)
	if input.Token == "" {
		writeError(w, http.StatusBadRequest, "Invite token is required")
		return
	}
	user := authUser(r)
	if user.ID == "" && !s.authDisabled {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}
	for _, key := range s.store.WorkspaceKeys() {
		var membership domain.WorkspaceMembership
		found := false
		joined := false
		joinedUserID := ""
		err := s.store.MutateWorkspace(r.Context(), key, "workspace_invite_link.joined", input.Token, nil, func(workspace *domain.Bootstrap) error {
			materializeDevelopmentMembers(workspace)
			if workspace.WorkspaceInviteLink == nil || !workspace.WorkspaceInviteLink.Enabled || workspace.WorkspaceInviteLink.Token != input.Token {
				return errNotFound
			}
			if !workspace.WorkspaceSettings.InviteLinksEnabled {
				return errInvalid
			}
			now := time.Now().UTC()
			memberUser := user
			if memberUser.ID == "" {
				memberUser = workspace.Viewer
			}
			if idx := slices.IndexFunc(workspace.Members, func(member domain.WorkspaceMember) bool {
				return member.User.ID == memberUser.ID || strings.EqualFold(member.User.Email, memberUser.Email)
			}); idx >= 0 {
				member := workspace.Members[idx]
				if member.Status == "suspended" {
					return errInvalid
				}
				membership = domain.WorkspaceMembership{
					Workspace:  workspace.Workspace,
					Role:       member.Role,
					JoinedAt:   member.JoinedAt,
					IssueCount: len(workspace.Issues),
				}
				found = true
				return nil
			}
			if memberUser.ID == "" {
				return errInvalid
			}
			if !slices.ContainsFunc(workspace.Users, func(item domain.User) bool { return item.ID == memberUser.ID }) {
				workspace.Users = append(workspace.Users, memberUser)
			}
			joined, joinedUserID = true, memberUser.ID
			workspace.Members = append(workspace.Members, domain.WorkspaceMember{
				User:       memberUser,
				Role:       "member",
				Status:     "active",
				JoinedAt:   now,
				LastSeenAt: &now,
			})
			membership = domain.WorkspaceMembership{
				Workspace:  workspace.Workspace,
				Role:       "member",
				JoinedAt:   now,
				IssueCount: len(workspace.Issues),
			}
			found = true
			return nil
		})
		if err == nil && found {
			if joined {
				s.sendWelcomeMessage(r.Context(), membership.Workspace.URLKey, joinedUserID)
			}
			writeJSON(w, http.StatusOK, membership)
			return
		}
		if err != nil && err != errNotFound {
			respondMutation(w, err, http.StatusOK, nil)
			return
		}
	}
	writeError(w, http.StatusBadRequest, "This invite link is invalid or has expired")
}

func (s *server) rotateSCIMToken(w http.ResponseWriter, r *http.Request) {
	if !s.requireWorkspaceAdmin(w, r) {
		return
	}
	data := s.workspaceData(r)
	tokenID := r.PathValue("id")
	existing, err := s.store.ListSCIMTokens(r.Context(), data.Workspace.ID)
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	name := ""
	for _, item := range existing {
		if item.ID == tokenID {
			name = item.Name
			break
		}
	}
	if name == "" {
		writeError(w, http.StatusNotFound, "SCIM token not found")
		return
	}
	rotatedName := strings.TrimSpace(strings.TrimSuffix(name, "(rotated)"))
	if rotatedName == "" {
		rotatedName = "SCIM"
	}
	rotatedName = rotatedName + " (rotated)"
	item, err := s.store.CreateSCIMToken(r.Context(), data.Workspace.ID, rotatedName)
	if err != nil {
		respondMutation(w, err, http.StatusCreated, nil)
		return
	}
	_ = s.store.RevokeSCIMToken(r.Context(), data.Workspace.ID, tokenID)
	_ = s.store.MutateWorkspace(r.Context(), workspaceKey(r), "scim.token_rotated", tokenID, nil, func(next *domain.Bootstrap) error {
		next.WorkspaceSettings.SCIMEnabled = true
		return nil
	})
	respondMutation(w, nil, http.StatusCreated, item)
}

func (s *server) listOAuthAppFailures(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	appID := r.PathValue("id")
	result := make([]domain.WebhookFailureEvent, 0)
	for _, item := range data.WebhookFailureEvents {
		if item.ApplicationID == appID {
			result = append(result, item)
			continue
		}
		for _, hook := range data.Webhooks {
			if hook.ID == item.WebhookID && hook.ApplicationID == appID {
				result = append(result, item)
				break
			}
		}
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) listOAuthSyncGroupRequests(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	appID := r.PathValue("id")
	result := make([]domain.OAuthSyncGroupRequest, 0)
	for _, item := range data.OAuthSyncGroupRequests {
		if item.ApplicationID == appID {
			result = append(result, item)
		}
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) decideOAuthSyncGroupRequest(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Status string `json:"status"`
		TeamID string `json:"teamId"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Status = strings.ToLower(strings.TrimSpace(input.Status))
	if input.Status != "approved" && input.Status != "denied" {
		writeError(w, http.StatusUnprocessableEntity, "status must be approved or denied")
		return
	}
	requestID := r.PathValue("requestId")
	var updated domain.OAuthSyncGroupRequest
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "oauth_sync_group."+input.Status, requestID, input, func(workspace *domain.Bootstrap) error {
		index := slices.IndexFunc(workspace.OAuthSyncGroupRequests, func(item domain.OAuthSyncGroupRequest) bool {
			return item.ID == requestID && item.ApplicationID == r.PathValue("id")
		})
		if index < 0 {
			return errNotFound
		}
		now := time.Now().UTC()
		workspace.OAuthSyncGroupRequests[index].Status = input.Status
		workspace.OAuthSyncGroupRequests[index].ReviewedAt = &now
		if input.TeamID != "" {
			workspace.OAuthSyncGroupRequests[index].TeamID = input.TeamID
		}
		if user := authUser(r); user.ID != "" {
			workspace.OAuthSyncGroupRequests[index].ReviewedBy = user.ID
		}
		updated = workspace.OAuthSyncGroupRequests[index]
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}
