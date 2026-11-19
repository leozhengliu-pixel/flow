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
// is not proven bounded (scaleP21RouteTemperature, SW550) runs in a COLD
// scope for its whole lifetime, authentication included. Its COLD needs take
// the replica's COLD slot per operation only (store/scale_p21_default_cold.go)
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
		if _, pattern := mux.Handler(r); pattern != "" && !scaleP21RouteHot(pattern) {
			var scope *store.ColdScope
			ctx, scope = store.WithColdScope(ctx, pattern)
			defer scope.Close()
			// The COLD projection gate replaces the request's projection
			// hold: acquireProjection, projectionRefused and the 503 +
			// Retry-After answers work unchanged.
			gate := s.coldGate()
			hold := &projectionHold{gate: gate}
			defer func() {
				hold.mu.Lock()
				held := hold.held
				hold.held = false
				hold.mu.Unlock()
				if held {
					<-gate.slots
				}
			}()
			ctx = context.WithValue(ctx, projectionHoldKey{}, hold)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// coldGate is the COLD projection gate: COLD requests' projections, bounded
// apart from HOT ones (FLOW_COLD_PROJECTION_CONCURRENCY, default 2;
// FLOW_COLD_QUEUE_LIMIT, default 64; FLOW_COLD_QUEUE_TIMEOUT, default 15s).
func (s *server) coldGate() *metadataReadGate {
	s.coldProjectionGateOnce.Do(func() {
		if s.coldProjectionGate == nil {
			s.coldProjectionGate = newMetadataReadGate(envInt("FLOW_COLD_PROJECTION_CONCURRENCY", 2), envInt("FLOW_COLD_QUEUE_LIMIT", 64), envDuration("FLOW_COLD_QUEUE_TIMEOUT", 15*time.Second), 0)
		}
	})
	return s.coldProjectionGate
}

// scaleP21RouteHot reports a route proven bounded (SW550). Any other
// pattern, including one missing from the generated table, runs COLD.
func scaleP21RouteHot(pattern string) bool {
	return scaleP21RouteTemperature[pattern] == "hot"
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
		// A default COLD request's projection is read from its legacy
		// view of the installed workspace (no COLD slot); a HOT route's
		// falls back to the live store, which refuses it.
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
