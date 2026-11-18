package main

import (
	"context"
	"net/http"

	"flow/api/internal/store"
)

type coldAuthorizationReadKey struct{}
type coldAuthorizationMuxKey struct{}
type coldProjectionStoreKey struct{}

// Authorization runs before ServeMux fills Request.Pattern. Carry the actual
// complete mux without selecting or executing a handler before authorization.
// A cold-only mux could incorrectly claim a more-specific HOT route.
//
// Default COLD routing: with bounded metadata on, every request whose route
// is not proven bounded (scaleP21RouteTemperature, SW550) runs in a COLD
// scope for its whole lifetime, authentication included. The scope takes the
// replica's COLD slot at its first COLD need (the authorization projection in
// place of the projection slot, a COLD facade, a legacy write) and releases
// it when the request ends; a proven-bounded route never opens one.
func (s *server) withColdMetadataRouteMux(mux *http.ServeMux, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.store == nil || !s.store.BoundedMetadataEnabled() {
			next.ServeHTTP(w, r)
			return
		}
		ctx := context.WithValue(r.Context(), coldAuthorizationMuxKey{}, mux)
		if _, pattern := mux.Handler(r); pattern != "" && !scaleP21RouteHot(pattern) {
			var scope *store.ColdScope
			ctx, scope = store.WithColdScope(ctx, pattern)
			ctx = context.WithValue(ctx, coldProjectionStoreKey{}, s.store)
			defer scope.Close()
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// scaleP21RouteHot reports a route proven bounded (SW550). Any other
// pattern, including one missing from the generated table, runs COLD.
func scaleP21RouteHot(pattern string) bool {
	return scaleP21RouteTemperature[pattern] == "hot"
}

// holdColdProjection is acquireProjection for a COLD request: the request's
// COLD slot replaces the projection slot, so a COLD request never holds a
// projection slot HOT requests wait for, and the two slots are never taken
// in opposite orders. handled is false outside a COLD scope.
func holdColdProjection(r *http.Request) (held, handled bool) {
	repo, ok := r.Context().Value(coldProjectionStoreKey{}).(*store.SQLiteStore)
	if !ok || repo == nil {
		return false, false
	}
	held, err := repo.HoldColdScope(r.Context())
	if err != nil {
		return false, true
	}
	return held, held
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
	if !selected && !store.InColdScope(r.Context()) {
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
	if store.InColdScope(r.Context()) {
		// The authorization projection's output outlives this call: the
		// request's scope holds the COLD slot for the rest of the request.
		if _, err := s.store.HoldColdScope(r.Context()); err != nil {
			return nil, err
		}
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
