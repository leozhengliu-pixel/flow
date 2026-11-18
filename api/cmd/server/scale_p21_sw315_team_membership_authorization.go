package main

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func scaleP21SW315TeamMembershipAuthorizationRequest(r *http.Request) (string, bool) {
	var pattern string
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		pattern = "/api/teams/{id}/members"
	case http.MethodPut, http.MethodDelete:
		pattern = "/api/teams/{id}/members/{userId}"
	case http.MethodPost:
		pattern = "/api/teams/{id}/members/sync"
	default:
		return "", false
	}
	parts, selected := scaleP21EscapedRouteParts(r, pattern)
	if !selected || len(parts) < 3 {
		return "", false
	}
	return parts[2], true
}

func (s *server) boundedTeamMembershipAuthorization(r *http.Request, workspace, userID string) (allowed, handled bool) {
	if !s.store.BoundedMetadataEnabled() {
		return false, false
	}
	ref, selected := scaleP21SW315TeamMembershipAuthorizationRequest(r)
	if !selected {
		return false, false
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) >= 4 && (parts[3] == "resources" || parts[3] == "resource-sections") {
		return s.boundedTeamResourceDecision(r, workspace, userID, ref, false), true
	}
	return s.boundedTeamRowAuthorization(r, workspace, userID, ref), true
}

// Keep the literal independent installed-directory resolver. A team row may
// remain visible to an admin who cannot read that private team's content.
func (s *server) boundedTeamRowAuthorization(r *http.Request, workspace, userID, ref string) bool {
	var credential *domain.APIKey
	if key, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok {
		credential = &key
	}
	for attempt := 0; attempt < 3; attempt++ {
		access, err := s.store.ResolveAccess(r.Context(), workspace, userID, credential)
		if err == nil {
			teamID := s.store.ResolveTeamID(workspace, ref)
			var read *store.TeamMemberRequestRead
			var found bool
			read, found, err = s.store.BeginTeamMemberRequestRead(r.Context(), workspace, teamID, &access, false)
			if err == nil {
				allowed := found && read != nil && read.Team.ID == teamID
				read.Close()
				return allowed
			}
			if read != nil {
				read.Close()
			}
		}
		if !errors.Is(err, store.ErrResourceAccessChanged) || attempt == 2 {
			return false
		}
		if credential != nil {
			user, current, selectedWorkspace := s.authenticateAPIKey(r)
			if current == nil || user.ID != userID || selectedWorkspace != workspace || !apiKeyAllowsRequest(r, *current) {
				return false
			}
			credential = current
			*r = *r.WithContext(context.WithValue(r.Context(), apiKeyContextKey{}, *current))
		}
	}
	return false
}
