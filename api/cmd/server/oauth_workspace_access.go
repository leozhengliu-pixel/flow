package main

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type oauthWorkspaceAccessKey struct{}
type oauthWorkspaceAccess struct {
	workspace string
	userID    string
	data      domain.Bootstrap
}

func oauthAuthorizationEndpoint(r *http.Request) bool {
	if r.URL.Path == "/api/oauth/authorization-request" {
		return r.Method == http.MethodGet || r.Method == http.MethodPost
	}
	return r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/oauth/authorizations/") && len(strings.Split(strings.Trim(r.URL.Path, "/"), "/")) == 4
}

func (s *server) oauthWorkspaceForRequest(w http.ResponseWriter, r *http.Request, workspace string, actor domain.User) (domain.Bootstrap, bool) {
	if cached, ok := r.Context().Value(oauthWorkspaceAccessKey{}).(oauthWorkspaceAccess); ok && cached.workspace == workspace && cached.userID == actor.ID {
		return cached.data, true
	}
	data, err := s.store.OAuthWorkspaceAccess(r.Context(), workspace, actor.ID)
	if err != nil {
		if errors.Is(err, store.ErrAuthForbidden) {
			writeOAuthError(w, http.StatusForbidden, "access_denied", "You do not have access to that workspace")
		} else {
			writeOAuthError(w, http.StatusServiceUnavailable, "server_error", "Could not check workspace access")
		}
		return data, false
	}
	data.Viewer = actor
	if !s.authDisabled {
		cookie, cookieErr := r.Cookie(sessionCookieName)
		duration := data.WorkspaceSettings.SessionDurationDays
		if duration < 1 {
			duration = 30
		}
		if cookieErr != nil || !s.store.EnforceSessionDuration(r.Context(), cookie.Value, duration) {
			writeOAuthError(w, http.StatusUnauthorized, "access_denied", "Your workspace session has expired")
			return data, false
		}
		if !s.authorizeAuthenticationPolicy(w, r, data, data.ViewerRole) {
			return data, false
		}
	}
	*r = *r.WithContext(context.WithValue(r.Context(), oauthWorkspaceAccessKey{}, oauthWorkspaceAccess{workspace: workspace, userID: actor.ID, data: data}))
	return data, true
}
