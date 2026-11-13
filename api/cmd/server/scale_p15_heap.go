package main

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type hydratedLegacyAdmission struct {
	mu      sync.Mutex
	bytes   int64
	readers int
	changed chan struct{}
}

var hydratedLegacyDownloads = hydratedLegacyAdmission{changed: make(chan struct{})}

type hydratedLegacyDownloadKey struct{}
type hydratedLegacyEstimateKey struct{}
type hydratedProducerReleaseKey struct{}

func releaseHydratedProducer(ctx context.Context) {
	if release, ok := ctx.Value(hydratedProducerReleaseKey{}).(func()); ok {
		release()
	}
}

func (g *hydratedLegacyAdmission) acquire(ctx context.Context, bytes int64) (func(), error) {
	bytes = max(1<<20, min(bytes, store.HydratedSnapshotReservationMaxBytes))
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		g.mu.Lock()
		if g.readers < 4 && bytes <= store.HydratedSnapshotReservationMaxBytes-g.bytes {
			g.bytes += bytes
			g.readers++
			g.mu.Unlock()
			return func() {
				g.mu.Lock()
				g.bytes -= bytes
				g.readers--
				close(g.changed)
				g.changed = make(chan struct{})
				g.mu.Unlock()
			}, nil
		}
		changed := g.changed
		g.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-changed:
		}
	}
}

// Once the shared spool fills, a download retains its snapshot until the
// client drains it. Admit these producers before they copy metadata, across
// both bootstrap routes and exports, so their snapshots cannot accumulate.
func boundHydratedLegacyDownloads(next http.Handler, estimates ...func(*http.Request) int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		compatibility := r.Method == http.MethodGet && (r.URL.Path == "/api/bootstrap" || r.URL.Path == "/api/issue-records/bootstrap" ||
			strings.HasPrefix(r.URL.Path, "/api/exports/") && strings.HasSuffix(r.URL.Path, "/download"))
		if !store.RecordHydrationEnabled() || !compatibility {
			next.ServeHTTP(w, r)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
		defer cancel()
		bytes := store.HydratedSnapshotReservationMaxBytes
		if len(estimates) > 0 {
			bytes = estimates[0](r)
		}
		releaseAdmission, err := hydratedLegacyDownloads.acquire(ctx, bytes)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "Workspace snapshot is busy; retry later")
			return
		}
		defer releaseAdmission()
		if ctx.Err() != nil {
			writeError(w, http.StatusServiceUnavailable, "Workspace snapshot is busy; retry later")
			return
		}
		controller := http.NewResponseController(w)
		_ = controller.SetWriteDeadline(time.Now().Add(5 * time.Minute))
		defer controller.SetWriteDeadline(time.Time{})
		ctx, release := store.BeginHydratedSnapshotReuse(ctx)
		defer release()
		ctx = context.WithValue(ctx, hydratedProducerReleaseKey{}, release)
		ctx = context.WithValue(ctx, hydratedLegacyDownloadKey{}, true)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *server) hydratedLegacySnapshotReservation(r *http.Request) int64 {
	if s.store != nil {
		return s.store.WorkspaceReadSnapshotReservation(workspaceKey(r))
	}
	if bytes, ok := r.Context().Value(hydratedLegacyEstimateKey{}).(int64); ok {
		return bytes
	}
	return store.HydratedSnapshotReservationMaxBytes
}

// These bodies alone are a lower bound on the encoded bootstrap. If they
// cannot fit, bypass the doomed disk prefix and keep the admitted producer.
func prepareHydratedBootstrapSpool(w http.ResponseWriter, value any) {
	data, ok := value.(domain.Bootstrap)
	if !ok {
		return
	}
	var bytes int64
	for i := range data.Projects {
		bytes += int64(len(data.Projects[i].Description))
	}
	for i := range data.Documents {
		bytes += int64(len(data.Documents[i].Content))
	}
	for w != nil {
		if spool, ok := w.(*legacyResponseSpool); ok && spool.file != nil && !spool.passthrough && !spool.file.CanAppend(bytes) {
			removeLegacySpool(spool.file)
			spool.file = nil
			spool.passthrough = true
			spool.ResponseWriter.WriteHeader(spool.status)
		}
		unwrapper, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			break
		}
		w = unwrapper.Unwrap()
	}
}
