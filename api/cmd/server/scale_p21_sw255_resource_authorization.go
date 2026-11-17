package main

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func scaleP21SW255ResourceAuthorizationRequest(r *http.Request) (kind, ref string, handled bool) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return "", "", false
	}
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) != 4 && len(parts) != 5 || parts[0] != "" || parts[1] != "api" ||
		parts[2] != "projects" && parts[2] != "initiatives" || parts[3] == "" {
		return "", "", false
	}
	if len(parts) == 5 && parts[4] != "relations" && parts[4] != "updates" {
		return "", "", false
	}
	return parts[2], parts[3], true
}

// Detail and these two list subviews share the literal source-resource ACL.
// Relation-ID paths have another predicate and are intentionally unhandled.
func (s *server) boundedPointResourceAuthorization(r *http.Request, workspace, userID string) (allowed, handled bool) {
	if !s.store.BoundedMetadataEnabled() {
		return false, false
	}
	kind, ref, selected := scaleP21SW255ResourceAuthorizationRequest(r)
	if !selected {
		return false, false
	}
	var credential *domain.APIKey
	if key, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok {
		credential = &key
	}
	for attempt := 0; attempt < 3; attempt++ {
		access, err := s.store.ResolveAccess(r.Context(), workspace, userID, credential)
		if err == nil {
			var read *store.ResourceAuthorizationRead
			read, err = s.store.BeginResourceAuthorizationRead(r.Context(), access, kind, ref)
			if err == nil {
				allowed = read.Allowed
				read.Close()
				return allowed, true
			}
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
