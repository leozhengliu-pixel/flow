package main

import (
	"bufio"
	"context"
	"net"
	"net/http"

	"flow/api/internal/store"
)

// With bounded metadata on, a request's legacy accessor whose wait for the
// replica's legacy-view lane is refused (queue full or
// FLOW_COLD_QUEUE_TIMEOUT; see store/scale_p21_legacy_view_lane.go) answers
// "no workspace", because its signature carries no error. The handler then
// answers that (403, 404 or an error) although the workspace is only busy.
// withLegacyViewBusy turns such an error answer into 503 + Retry-After, as
// the COLD slot's refusal is answered: the request carries a marker that
// bounds its own waits and that the store marks when one of them is refused,
// so only the refused request is answered 503 (another request's genuine
// error is kept). Only error statuses written before any body change; a
// success answer is never rewritten. end must be called when the handler
// returns: work the request left running then waits as background work does.

func withLegacyViewBusy(ctx context.Context, w http.ResponseWriter) (context.Context, http.ResponseWriter, func()) {
	ctx, refused, end := store.WithLegacyViewBusyRecorder(ctx)
	return ctx, &legacyViewBusyWriter{ResponseWriter: w, busy: refused}, end
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
