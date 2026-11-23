package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"flow/api/internal/coordination"
	"flow/api/internal/store"
)

// busyDeadlineRetryAfter is the Retry-After of a request that ran out of its
// own time budget (a handler's context deadline) or of the metadata work budget.
const busyDeadlineRetryAfter = 2 * time.Second

// retryableBusy reports whether err means "the server was too busy right now"
// and how long the client should wait: a full or timed-out COLD slot, a lock
// another replica holds, an exhausted metadata work budget, or the request's
// own deadline. A cancelled context (the client left) is not busy.
func retryableBusy(err error) (time.Duration, bool) {
	switch {
	case err == nil, errors.Is(err, context.Canceled):
		return 0, false
	case errors.Is(err, store.ErrColdMetadataBusy):
		return store.ColdMetadataBusyRetryAfter, true
	case errors.Is(err, coordination.ErrWorkspaceLockBusy):
		return workspaceLockBusyRetryAfter, true
	case errors.Is(err, store.ErrMetadataWorkBudget), errors.Is(err, context.DeadlineExceeded):
		return busyDeadlineRetryAfter, true
	}
	return 0, false
}

// writeRetryableBusy answers a retryable busy error with the shared 503 +
// Retry-After response and logs which wait it was (the error names the slot:
// COLD slot, workspace lock, work budget or deadline) with the request's route
// and whether it ran inside a COLD scope. It returns false for any other error.
func writeRetryableBusy(w http.ResponseWriter, r *http.Request, err error) bool {
	retryAfter, busy := retryableBusy(err)
	if !busy {
		return false
	}
	if r != nil {
		_, cold := store.ColdScopeFromContext(r.Context())
		slog.WarnContext(r.Context(), "request answered busy", "request_id", requestIDFrom(r.Context()), "method", r.Method, "path", r.URL.Path, "cold_scope", cold, "retry_after", retryAfter, "error", err.Error())
	}
	(&metadataReadGate{}).busy(w, retryAfter)
	return true
}

// writeStoreBusy answers a store that was too busy to serve the request (a
// full or timed-out COLD slot, a workspace lock another replica holds, a
// metadata budget concurrent work held, or database lock contention) with
// the shared 503 + Retry-After. Unclassified deadlines and permanent size
// refusals keep the response their handler gives.
func writeStoreBusy(w http.ResponseWriter, err error) bool {
	retryAfter := storeBusyRetryAfter(err)
	if retryAfter == 0 {
		return false
	}
	(&metadataReadGate{}).busy(w, retryAfter)
	return true
}

func storeBusyRetryAfter(err error) time.Duration {
	switch {
	case errors.Is(err, store.ErrColdMetadataBusy):
		return store.ColdMetadataBusyRetryAfter
	case errors.Is(err, coordination.ErrWorkspaceLockBusy):
		return workspaceLockBusyRetryAfter
	case store.MetadataWorkContended(err), errors.Is(err, store.ErrRecordSpoolContended), errors.Is(err, store.ErrDatabaseWriteBusy):
		return busyDeadlineRetryAfter
	}
	return 0
}

func isRetryableBusy(err error) bool {
	_, busy := retryableBusy(err)
	return busy
}
