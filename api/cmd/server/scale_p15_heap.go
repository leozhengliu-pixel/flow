package main

import (
	"context"
	"net/http"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

var hydratedLegacyDownloadSlot = make(chan struct{}, 1)

// Once the shared spool fills, a download retains its snapshot until the
// client drains it. Admit these producers before they copy metadata, across
// both bootstrap routes and exports, so their snapshots cannot accumulate.
func boundHydratedLegacyDownloads(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		compatibility := r.Method == http.MethodGet && (r.URL.Path == "/api/bootstrap" || r.URL.Path == "/api/issue-records/bootstrap" ||
			strings.HasPrefix(r.URL.Path, "/api/exports/") && strings.HasSuffix(r.URL.Path, "/download"))
		if !store.RecordHydrationEnabled() || !compatibility {
			next.ServeHTTP(w, r)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
		defer cancel()
		select {
		case hydratedLegacyDownloadSlot <- struct{}{}:
			defer func() { <-hydratedLegacyDownloadSlot }()
		case <-ctx.Done():
			writeError(w, http.StatusServiceUnavailable, "Workspace snapshot is busy; retry later")
			return
		}
		if ctx.Err() != nil {
			writeError(w, http.StatusServiceUnavailable, "Workspace snapshot is busy; retry later")
			return
		}
		controller := http.NewResponseController(w)
		_ = controller.SetWriteDeadline(time.Now().Add(5 * time.Minute))
		defer controller.SetWriteDeadline(time.Time{})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
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
