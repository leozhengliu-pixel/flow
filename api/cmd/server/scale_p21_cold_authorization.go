package main

import (
	"context"
	"net/http"

	"flow/api/internal/store"
)

type coldAuthorizationReadKey struct{}
type coldAuthorizationMuxKey struct{}

// Authorization runs before ServeMux fills Request.Pattern. Carry the actual
// complete mux without selecting or executing a handler before authorization.
// A cold-only mux could incorrectly claim a more-specific HOT route.
func (s *server) withColdMetadataRouteMux(mux *http.ServeMux, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.store == nil || !s.store.BoundedMetadataEnabled() {
			next.ServeHTTP(w, r)
			return
		}
		ctx := context.WithValue(r.Context(), coldAuthorizationMuxKey{}, mux)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func scaleP21ColdAuthorizationRoute(r *http.Request) (scaleP21ColdRoute, bool) {
	mux, ok := r.Context().Value(coldAuthorizationMuxKey{}).(*http.ServeMux)
	if !ok || mux == nil {
		return scaleP21ColdRoute{}, false
	}
	_, pattern := mux.Handler(r)
	if pattern == "" {
		return scaleP21ColdRoute{}, false
	}
	for _, route := range scaleP21ColdRoutes {
		if pattern == route.method+" "+route.path {
			return route, true
		}
	}
	return scaleP21ColdRoute{}, false
}

// legacyColdAuthorizationRepository is called only when literal resource auth
// reaches its whole-workspace projection. Membership/admin/feature refusals
// and earlier resource-specific authorization do not acquire a cold slot.
func (s *server) legacyColdAuthorizationRepository(r *http.Request) (*store.SQLiteStore, error) {
	if !s.store.BoundedMetadataEnabled() {
		return s.store, nil
	}
	route, selected := scaleP21ColdAuthorizationRoute(r)
	if !selected {
		return s.store, nil
	}
	scope, ok := r.Context().Value(requestResourceReadsKey{}).(*requestResourceReads)
	if !ok || scope == nil {
		return nil, store.ErrBoundedMetadataCoverage
	}
	scope.mu.Lock()
	closed := scope.closed
	scope.mu.Unlock()
	if closed {
		return nil, store.ErrBoundedMetadataCoverage
	}
	if held, ok := r.Context().Value(coldAuthorizationReadKey{}).(*store.ColdMetadataRead); ok {
		if repo := held.Store(); repo != nil {
			return repo, nil
		}
		return nil, store.ErrBoundedMetadataCoverage
	}
	read, err := s.store.BeginColdMetadataRead(r.Context(), route.consumer)
	if err != nil {
		return nil, err
	}
	if !registerRequestReadClose(r.Context(), read.Close) {
		read.Close()
		return nil, store.ErrBoundedMetadataCoverage
	}
	ctx := context.WithValue(read.Context(), coldAuthorizationReadKey{}, read)
	*r = *r.WithContext(ctx)
	return read.Store(), nil
}
