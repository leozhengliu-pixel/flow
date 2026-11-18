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
	for _, kind := range []string{"projects", "initiatives"} {
		for _, tail := range []string{"", "/relations", "/updates", "/updates/diff-preview", "/history"} {
			parts, matched := scaleP21EscapedRouteParts(r, "/api/"+kind+"/{id}"+tail)
			if !matched {
				continue
			}
			if len(parts) < 3 {
				return kind, "", true
			}
			return kind, parts[2], true
		}
	}
	return "", "", false
}

// Detail and these read subviews share the literal source-resource ACL.
// Relation-ID paths have another predicate and are intentionally unhandled.
func (s *server) boundedPointResourceAuthorization(r *http.Request, workspace, userID string) (allowed, handled bool) {
	if !s.store.BoundedMetadataEnabled() {
		return false, false
	}
	kind, ref, selected := scaleP21SW255ResourceAuthorizationRequest(r)
	if !selected {
		return false, false
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 3 {
		// An escaped "/" ID trims to the literal collection decision.
		request := r.Clone(r.Context())
		location := *r.URL
		location.Path, location.RawPath = "/api/"+kind, ""
		request.URL = &location
		return s.boundedHotCollectionAuthorization(request, workspace, userID)
	}
	if strings.TrimSpace(ref) == "" {
		return false, true // MatchSlugRef rejects the empty decoded ref.
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
				if allowed && len(parts) >= 5 && parts[3] == "relations" {
					allowed, err = s.boundedPointRelationAuthorization(r, workspace, userID, kind, ref, parts[4], access)
				}
				if err == nil {
					return allowed, true
				}
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

// Literal auth projects the viewer before applying key restrictions in its
// point predicates. Keep that order; the relation-list handler filters keys.
func (s *server) boundedPointRelationAuthorization(r *http.Request, workspace, userID, kind, ref, relationID string, access store.AccessContext) (bool, error) {
	read, err := s.store.BeginResourceRelationRead(r.Context(), workspace, userID, nil, kind)
	if err != nil {
		return false, err
	}
	defer read.Close()
	if err := read.Select(r.Context(), ref); err != nil {
		return false, err
	}
	relatedAllowed := func(related string) (bool, error) {
		point, err := s.store.BeginResourceAuthorizationRead(r.Context(), access, kind, related)
		if err != nil {
			return false, err
		}
		allowed := point.Allowed
		point.Close()
		return allowed, nil
	}
	if kind == "projects" {
		for _, relation := range read.ProjectRelations {
			if relation.ID != relationID || relation.ProjectID != ref {
				continue
			}
			allowed, err := relatedAllowed(relation.RelatedProjectID)
			if err != nil || allowed {
				return allowed, err
			}
		}
	} else {
		for _, relation := range read.InitiativeRelations {
			if relation.ID != relationID || relation.InitiativeID != ref {
				continue
			}
			allowed, err := relatedAllowed(relation.RelatedInitiativeID)
			if err != nil || allowed {
				return allowed, err
			}
		}
	}
	return false, s.store.ValidateAccess(r.Context(), access)
}
