package main

import (
	"context"
	"errors"
	"net/http"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// scaleP21RecordPredicate applies a literal record predicate to the unfiltered
// headers with the viewer's access policy. An error (a changed policy) retries.
type scaleP21RecordPredicate func(store.AccessContext, domain.Bootstrap) (bool, error)

// boundedRecordDecision reads the unfiltered authorization headers of the
// records of one kind named by ref and applies decide with the viewer's
// access policy. It keeps SW255's retry: a changed policy re-resolves access
// and re-authenticates an API key against the same request.
func (s *server) boundedRecordDecision(r *http.Request, workspace, userID, kind, ref string, decide scaleP21RecordPredicate) bool {
	var credential *domain.APIKey
	if key, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok {
		credential = &key
	}
	for attempt := 0; attempt < 3; attempt++ {
		access, err := s.store.ResolveAccess(r.Context(), workspace, userID, credential)
		if err == nil {
			var data domain.Bootstrap
			var read *store.RecordAuthorizationRead
			if kind != "" {
				read, err = s.store.BeginRecordAuthorizationRead(r.Context(), access, kind, ref)
				if err == nil {
					data = read.Data
				}
			}
			if err == nil {
				var allowed bool
				allowed, err = decide(access, data)
				read.Close()
				if err == nil {
					return allowed
				}
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

// cycleAllowed: the literal projection keeps a cycle only when the viewer
// reads its team's content, and the predicate then requires teamAllowed of
// that same team, so the unfiltered header with TeamContentAllowed decides it.
func boundedCycleAllowed(ref string) scaleP21RecordPredicate {
	return func(access store.AccessContext, data domain.Bootstrap) (bool, error) {
		for _, item := range data.Cycles {
			if item.ID == ref && access.TeamContentAllowed(item.TeamID) {
				return true, nil
			}
		}
		return false, nil
	}
}

// scaleP21SW490RecordPredicate names the record kind to read (none for a
// create root or a setting) and the literal predicate of a record shape. For
// record shapes, output marks a create root.
func (s *server) scaleP21SW490RecordPredicate(r *http.Request, userID string, shape scaleP21SW490Shape) (string, scaleP21RecordPredicate) {
	switch shape.kind {
	case "cycles":
		return "cycles", boundedCycleAllowed(shape.ref)
	case "savedViews":
		if shape.output {
			return "", s.boundedViewPredicate(r, userID, "", true)
		}
		return "savedViews", s.boundedViewPredicate(r, userID, shape.ref, false)
	case "dashboards":
		return "", s.boundedDashboardPredicate(r, userID, shape.ref, shape.output)
	}
	return "", nil
}
