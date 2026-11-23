package main

import (
	"context"
	"errors"
	"io"
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

// writeStoreBusy answers a store that was too busy to serve the request (a
// full or timed-out COLD slot, a workspace lock another replica holds, a
// metadata budget concurrent work held, or a database refusal) with
// the shared 503 + Retry-After. Unclassified deadlines and permanent size
// refusals keep the response their handler gives.
func writeStoreBusy(w http.ResponseWriter, err error) bool {
	retryAfter := storeBusyRetryAfter(err)
	if retryAfter == 0 {
		return false
	}
	if errors.Is(err, store.ErrDatabaseUnavailable) {
		if r := apiErrorRequest(w); r != nil {
			slog.WarnContext(r.Context(), "workspace reload failed before mutation",
				"request_id", serverRequestIDFrom(r.Context()), "cause", requestErrorCode(err))
		}
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
	case store.MetadataWorkContended(err), errors.Is(err, store.ErrRecordSpoolContended), errors.Is(err, store.ErrDatabaseWriteBusy), errors.Is(err, store.ErrDatabaseUnavailable):
		return busyDeadlineRetryAfter
	}
	return 0
}

func isRetryableBusy(err error) bool {
	_, busy := retryableBusy(err)
	return busy
}

func requestErrorCode(err error) string {
	if code := store.DatabaseErrorCode(err); code != "" {
		return code
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, store.ErrColdMetadataBusy):
		return "cold_slot_busy"
	case errors.Is(err, coordination.ErrWorkspaceLockBusy):
		return "workspace_lock_busy"
	case errors.Is(err, store.ErrBoundedMetadataCoverage):
		return "metadata_coverage"
	case errors.Is(err, store.ErrMetadataWorkBudget):
		return "work_budget"
	case errors.Is(err, store.ErrRecordSpoolContended):
		return "scratch_busy"
	case errors.Is(err, io.ErrUnexpectedEOF):
		return "unexpected_eof"
	default:
		return "unknown"
	}
}
