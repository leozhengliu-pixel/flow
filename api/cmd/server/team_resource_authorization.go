package main

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func scaleP21SW307TeamResourceAuthorizationRequest(r *http.Request) (string, bool) {
	var patterns []string
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		patterns = []string{"/api/teams/{id}/resources"}
	case http.MethodPost:
		patterns = []string{"/api/teams/{id}/resources", "/api/teams/{id}/resource-sections"}
	case http.MethodPatch, http.MethodDelete:
		patterns = []string{"/api/teams/{id}/resources/{resourceId}", "/api/teams/{id}/resource-sections/{sectionId}"}
	}
	for _, pattern := range patterns {
		if parts, selected := scaleP21EscapedRouteParts(r, pattern); selected && len(parts) >= 3 {
			return parts[2], true
		}
	}
	return "", false
}

func (s *server) boundedTeamResourceAuthorization(r *http.Request, workspace, userID string) (allowed, handled bool) {
	if !s.store.BoundedMetadataEnabled() {
		return false, false
	}
	ref, selected := scaleP21SW307TeamResourceAuthorizationRequest(r)
	if !selected {
		return false, false
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	content := len(parts) >= 4 && (parts[3] == "resources" || parts[3] == "resource-sections")
	if !content {
		return s.boundedTeamRowAuthorization(r, workspace, userID, ref), true
	}
	output := r.Method == http.MethodGet && len(parts) == 4 && parts[3] == "resources"
	return s.boundedTeamResourceDecision(r, workspace, userID, ref, output), true
}

// output follows the literal decoded handoff condition independently of the
// registered escaped suffix. Membership routes borrow only the predicate.
func (s *server) boundedTeamResourceDecision(r *http.Request, workspace, userID, ref string, output bool) bool {
	var credential *domain.APIKey
	if key, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok {
		credential = &key
	}
	for attempt := 0; attempt < 3; attempt++ {
		read, err := s.store.BeginTeamResourceAuthorizationRead(r.Context(), workspace, userID, ref, output, credential)
		if err == nil {
			if !read.Allowed || !output {
				allowed := read.Allowed
				read.Close()
				return allowed
			}
			rows := teamResourcesRead{
				TeamID:   read.Data.Teams[0].ID,
				Sections: read.Data.TeamResourceSections, Resources: read.Data.TeamPinnedResources,
			}
			if !registerRequestReadClose(r.Context(), func() {
				clear(rows.Sections)
				clear(rows.Resources)
				read.Close()
			}) {
				read.Close()
				return false
			}
			*r = *r.WithContext(context.WithValue(r.Context(), teamResourcesReadKey{}, rows))
			return true
		}
		if read != nil {
			read.Close()
		}
		if !errors.Is(err, store.ErrResourceAccessChanged) || attempt == 2 {
			noteAuthorizationBusy(r, err)
			return false
		}
		if credential != nil {
			user, current, selectedWorkspace, err := s.authenticateAPIKey(r)
			noteAuthorizationBusy(r, err)
			if current == nil || user.ID != userID || selectedWorkspace != workspace || !apiKeyAllowsRequest(r, *current) {
				return false
			}
			credential = current
			*r = *r.WithContext(context.WithValue(r.Context(), apiKeyContextKey{}, *current))
		}
	}
	return false
}
