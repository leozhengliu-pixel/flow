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
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return "", false
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 4 || parts[0] != "api" || parts[1] != "teams" || parts[2] == "" || parts[3] != "resources" {
		return "", false
	}
	return parts[2], true
}

func (s *server) boundedTeamResourceAuthorization(r *http.Request, workspace, userID string) (allowed, handled bool) {
	if !s.store.BoundedMetadataEnabled() {
		return false, false
	}
	ref, selected := scaleP21SW307TeamResourceAuthorizationRequest(r)
	if !selected {
		return false, false
	}
	var credential *domain.APIKey
	if key, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok {
		credential = &key
	}
	for attempt := 0; attempt < 3; attempt++ {
		read, err := s.store.BeginTeamResourceAuthorizationRead(r.Context(), workspace, userID, ref, r.Method == http.MethodGet, credential)
		if err == nil {
			if !read.Allowed || r.Method != http.MethodGet {
				allowed := read.Allowed
				read.Close()
				return allowed, true
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
				return false, true
			}
			// Preserve the canonical ID. A raw team-key alias still fails the
			// unchanged handler's TeamID == PathValue check; HEAD has no rows.
			*r = *r.WithContext(context.WithValue(r.Context(), teamResourcesReadKey{}, rows))
			return true, true
		}
		if read != nil {
			read.Close()
		}
		if !errors.Is(err, store.ErrResourceAccessChanged) || attempt == 2 {
			return false, true
		}
		if credential != nil {
			user, current, selectedWorkspace := s.authenticateAPIKey(r)
			if current == nil || user.ID != userID || selectedWorkspace != workspace || !apiKeyAllowsRequest(r, *current) {
				return false, true
			}
			credential = current
			*r = *r.WithContext(context.WithValue(r.Context(), apiKeyContextKey{}, *current))
		}
	}
	return false, true
}
