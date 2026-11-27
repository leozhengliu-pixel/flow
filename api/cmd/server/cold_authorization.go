package main

import (
	"context"
	"net/http"
	"time"

	"flow/api/internal/store"
)

type coldAuthorizationReadKey struct{}
type coldAuthorizationMuxKey struct{}

// Authorization runs before ServeMux fills Request.Pattern. Carry the actual
// complete mux without selecting or executing a handler before authorization.
// A cold-only mux could incorrectly claim a more-specific HOT route.
//
// Default COLD routing: with bounded metadata on, every request whose route
// is not proven bounded (scaleRouteTemperature, route temperature guard) runs in a COLD
// scope for its whole lifetime, authentication included. Its COLD needs take
// the replica's COLD slot per operation only (store/default_cold.go)
// and its projections take the COLD projection gate in place of the
// projection gate, so a COLD request never holds a slot a HOT request waits
// for; a proven-bounded route never opens a scope.
func (s *server) withColdMetadataRouteMux(mux *http.ServeMux, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.store == nil || !s.store.BoundedMetadataEnabled() {
			next.ServeHTTP(w, r)
			return
		}
		ctx := context.WithValue(r.Context(), coldAuthorizationMuxKey{}, mux)
		// A refused legacy-view wait answers 503 + Retry-After
		// (legacy_view_busy.go).
		var endRequest func()
		ctx, w, endRequest = withLegacyViewBusy(ctx, w)
		defer endRequest()
		if _, pattern := mux.Handler(r); pattern != "" && !scaleRouteHot(pattern) {
			var scope *store.ColdScope
			ctx, scope = store.WithColdScope(ctx, pattern)
			defer scope.Close()
			// The COLD projection gate replaces the request's projection
			// hold: acquireProjection, projectionRefused and the 503 +
			// Retry-After answers work unchanged.
			gate := s.coldGate()
			hold := &projectionHold{gate: gate}
			defer hold.release()
			ctx = context.WithValue(ctx, projectionHoldKey{}, hold)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// coldGate is the COLD projection gate: COLD requests' projections, bounded
// apart from HOT ones. FLOW_COLD_PROJECTION_CONCURRENCY overrides the default
// from the replica's memory target. Waits use the COLD queue settings.
func (s *server) coldGate() *metadataReadGate {
	s.coldProjectionGateOnce.Do(func() {
		if s.coldProjectionGate == nil {
			s.coldProjectionGate = newMetadataReadGate(envInt("FLOW_COLD_PROJECTION_CONCURRENCY", store.BoundedColdProjectionConcurrency()), envInt("FLOW_COLD_QUEUE_LIMIT", 64), envDuration("FLOW_COLD_QUEUE_TIMEOUT", 15*time.Second), 0)
		}
	})
	return s.coldProjectionGate
}

// scaleRouteHot reports a route proven bounded. Any other
// pattern, including one missing from the generated table, runs COLD.
func scaleRouteHot(pattern string) bool {
	return scaleRouteTemperature[pattern] == "hot"
}

func releaseColdWriteProjection(r *http.Request) {
	if r.Method == http.MethodGet || r.Method == http.MethodHead || !store.InColdScope(r.Context()) {
		return
	}
	releaseProjection(r)
}

func scaleColdAuthorizationRoute(r *http.Request) (scaleColdRoute, bool) {
	mux, ok := r.Context().Value(coldAuthorizationMuxKey{}).(*http.ServeMux)
	if !ok || mux == nil {
		return scaleColdRoute{}, false
	}
	_, pattern := mux.Handler(r)
	if pattern == "" {
		return scaleColdRoute{}, false
	}
	for _, route := range scaleColdRoutes {
		if pattern == route.method+" "+route.path {
			return route, true
		}
	}
	return scaleColdRoute{}, false
}

// legacyColdAuthorizationRepository supplies the literal resource auth reads.
// PagedWorkspaceMetadata can project directly from the shared COLD image.
// Select the facade for registered routes whose handlers also use it.
func (s *server) legacyColdAuthorizationRepository(r *http.Request, _ bool) (*store.SQLiteStore, error) {
	if !s.store.BoundedMetadataEnabled() {
		return s.store, nil
	}
	route, selected := scaleColdAuthorizationRoute(r)
	if !selected {
		return s.store.ColdRequestView(r.Context(), workspaceKey(r))
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
