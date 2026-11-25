package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
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

const databaseUnavailableMessage = "Database is temporarily unavailable"

func setRetryAfter(w http.ResponseWriter, delay time.Duration) bool {
	if delay <= 0 {
		return false
	}
	w.Header().Set("Retry-After", strconv.Itoa(max(1, int(delay/time.Second))))
	return true
}

// writeStoreBusy writes the shared 503 for a known transient store failure.
// Connection loss can also happen after a write was sent.
func writeStoreBusy(w http.ResponseWriter, err error) bool {
	retryAfter := storeBusyRetryAfter(err)
	if retryAfter == 0 {
		return false
	}
	if errors.Is(err, store.ErrDatabaseUnavailable) || store.IsDatabaseConnectionError(err) {
		if r := apiErrorRequest(w); r != nil {
			slog.WarnContext(r.Context(), "database unavailable",
				"request_id", serverRequestIDFrom(r.Context()), "cause", requestErrorCode(err))
		}
		setRetryAfter(w, retryAfter)
		writeError(w, http.StatusServiceUnavailable, databaseUnavailableMessage)
		return true
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
	case store.IsDatabaseConnectionError(err):
		return busyDeadlineRetryAfter
	}
	return 0
}

// storeReadRetryAfter also covers deadlines returned by credential and
// realtime reads. A bare deadline on a general write is ambiguous.
func storeReadRetryAfter(err error) time.Duration {
	if errors.Is(err, context.DeadlineExceeded) {
		return busyDeadlineRetryAfter
	}
	return storeBusyRetryAfter(err)
}

func databaseReadUnavailable(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, store.ErrDatabaseUnavailable) || store.IsDatabaseConnectionError(err)
}

func writeStoreReadUnavailable(w http.ResponseWriter, err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		setRetryAfter(w, busyDeadlineRetryAfter)
		writeError(w, http.StatusServiceUnavailable, databaseUnavailableMessage)
		return true
	}
	return writeStoreBusy(w, err)
}

func isRetryableBusy(err error) bool {
	_, busy := retryableBusy(err)
	return busy
}

func requestErrorCode(err error) string {
	if code := store.DatabaseErrorCode(err); code != "" {
		return code
	}
	var handlerErr *internalHandlerError
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
	case errors.As(err, &handlerErr):
		return "internal_handler"
	default:
		return "unknown"
	}
}
