package main

import (
	"bufio"
	"context"
	"net"
	"net/http"

	"flow/api/internal/store"
)

// With bounded metadata on, a legacy accessor whose wait for the replica's
// legacy-view lane is refused (queue full or FLOW_COLD_QUEUE_TIMEOUT; see
// store/scale_p21_legacy_view_lane.go) answers "no workspace", because its
// signature carries no error. The handler then answers that (403, 404 or an
// error) although the workspace is only busy. withLegacyViewBusy turns such
// an error answer into 503 + Retry-After, as the COLD slot's refusal is
// answered: the request carries a recorder the store marks when one of its
// own operations is refused, and a refusal no recorder saw (an accessor that
// takes no context) during the request counts too. Only error statuses
// written before any body change; a success answer is never rewritten.

func withLegacyViewBusy(ctx context.Context, w http.ResponseWriter) (context.Context, http.ResponseWriter) {
	ctx, refused := store.WithLegacyViewBusyRecorder(ctx)
	before := store.UnrecordedLegacyViewRefusals()
	return ctx, &legacyViewBusyWriter{ResponseWriter: w, busy: func() bool {
		return refused() || store.UnrecordedLegacyViewRefusals() != before
	}}
}

type legacyViewBusyWriter struct {
	http.ResponseWriter
	busy    func() bool
	written bool
	dropped bool
}

func (w *legacyViewBusyWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *legacyViewBusyWriter) WriteHeader(status int) {
	if w.written {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	w.written = true
	if status >= http.StatusBadRequest && status != http.StatusServiceUnavailable && w.busy() {
		w.dropped = true
		header := w.Header()
		header.Del("Content-Length")
		header.Del("Content-Encoding")
		(&metadataReadGate{}).busy(w.ResponseWriter, store.ColdMetadataBusyRetryAfter)
		return
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *legacyViewBusyWriter) Write(p []byte) (int, error) {
	if w.dropped {
		return len(p), nil
	}
	w.written = true
	return w.ResponseWriter.Write(p)
}

func (w *legacyViewBusyWriter) Flush() {
	if !w.dropped {
		w.written = true
		_ = http.NewResponseController(w.ResponseWriter).Flush()
	}
}

func (w *legacyViewBusyWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(w.ResponseWriter).Hijack()
}
